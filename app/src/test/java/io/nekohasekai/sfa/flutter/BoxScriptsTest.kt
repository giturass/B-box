package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.job
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Assert.*
import org.junit.Test
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference

class BoxScriptsTest {
    private val revision = "a".repeat(64)
    private fun request(id: String, index: Int = 0) = ScriptRunRequest(id, 1, revision, "generic", index)
    private val output = mapOf("result" to "{\"ok\":true}", "error" to "", "logs" to listOf("script output"))

    private class Task(
        private val output: Map<String, Any?>,
        private val resultGate: CountDownLatch = CountDownLatch(0),
        private val exitGate: CountDownLatch = CountDownLatch(0),
        private val onClose: () -> Unit = {},
    ) : ScriptDebugTask {
        val entered = CompletableDeferred<Unit>()
        val closing = CompletableDeferred<Unit>()
        val joining = CompletableDeferred<Unit>()
        val waits = AtomicInteger()
        override fun close() {
            onClose()
            closing.complete(Unit)
            resultGate.countDown()
        }
        override fun awaitResult(): Map<String, Any?> {
            waits.incrementAndGet()
            entered.complete(Unit)
            check(resultGate.await(5, TimeUnit.SECONDS))
            check(exitGate.await(5, TimeUnit.SECONDS))
            return output
        }
        override fun awaitClosed() {
            joining.complete(Unit)
            check(exitGate.await(5, TimeUnit.SECONDS))
        }
    }

    @Test fun cancellationWhileWaitingForConfigurationLocksNeverCreatesATask() = runBlocking {
        val entered = CompletableDeferred<Unit>()
        val release = CompletableDeferred<Unit>()
        var tasks = 0
        val scripts = BoxScripts(
            prepare = { _: ScriptRunRequest, created: (ScriptDebugTask) -> Unit ->
                entered.complete(Unit)
                release.await()
                tasks++
                created(Task(output))
                Unit
            },
            validate = { _: Unit -> fail("Cancelled preparation must not validate a result") },
        )
        try {
            val running = async(Dispatchers.Default) { scripts.run(request("waiting")) }
            withTimeout(5_000) { entered.await() }
            assertFalse(scripts.cancel("other"))
            assertTrue(scripts.cancel("waiting"))
            withTimeout(5_000) { running.join() }
            assertTrue(running.isCancelled)
            assertEquals(0, tasks)
            assertFalse(scripts.cancel("waiting"))
        } finally { scripts.close(); release.complete(Unit) }
    }

    @Test fun differentRequestIdsRunInParallelAndRetainTheirStructuredOutput() = runBlocking {
        val prepared = AtomicInteger()
        val bothPrepared = CompletableDeferred<Unit>()
        val release = CompletableDeferred<Unit>()
        val validated = AtomicInteger()
        val scripts = BoxScripts<Int>(
            prepare = { request: ScriptRunRequest, created: (ScriptDebugTask) -> Unit ->
                created(Task(output + ("result" to "{\"index\":${request.index}}")))
                if (prepared.incrementAndGet() == 2) bothPrepared.complete(Unit)
                release.await()
                request.index
            },
            validate = { _: Int -> validated.incrementAndGet(); Unit },
        )
        try {
            val first = async(Dispatchers.Default) { scripts.run(request("first", 1)) }
            val second = async(Dispatchers.Default) { scripts.run(request("second", 2)) }
            withTimeout(5_000) { bothPrepared.await() }
            assertEquals(2, prepared.get())
            release.complete(Unit)
            assertEquals("{\"index\":1}", withTimeout(5_000) { first.await() }["result"])
            assertEquals("{\"index\":2}", withTimeout(5_000) { second.await() }["result"])
            assertEquals(2, validated.get())
            assertFalse(scripts.cancel("first"))
            assertFalse(scripts.cancel("second"))
        } finally { release.complete(Unit); scripts.close() }
    }

    @Test fun duplicateIdsAreRejectedWithoutStartingAnotherTask() = runBlocking {
        val prepared = CompletableDeferred<Unit>()
        val calls = AtomicInteger()
        val scripts = BoxScripts<Unit>(
            prepare = { _: ScriptRunRequest, _: (ScriptDebugTask) -> Unit ->
                calls.incrementAndGet()
                prepared.complete(Unit)
                awaitCancellation()
            },
            validate = { _: Unit -> fail("Cancelled preparation must not validate") },
        )
        try {
            val first = async(Dispatchers.Default) { scripts.run(request("same")) }
            withTimeout(5_000) { prepared.await() }
            assertTrue(runCatching { scripts.run(request("same")) }.exceptionOrNull() is IllegalArgumentException)
            assertEquals(1, calls.get())
            scripts.cancel("same")
            withTimeout(5_000) { first.join() }
        } finally { scripts.close() }
    }

    @Test fun cancellationClosesBlockedJNIAndWaitsForItsWorkerToExit() = runBlocking {
        val exit = CountDownLatch(1)
        val task = Task(output, CountDownLatch(1), exit)
        val scripts = BoxScripts<Unit>(
            prepare = { _: ScriptRunRequest, created: (ScriptDebugTask) -> Unit -> created(task); Unit },
            validate = { _: Unit -> fail("Cancelled JNI must not publish its result") },
        )
        try {
            val running = async(Dispatchers.Default) { scripts.run(request("native")) }
            withTimeout(5_000) { task.entered.await() }
            assertTrue(scripts.cancel("native"))
            withTimeout(5_000) { task.closing.await() }
            assertFalse(running.isCompleted)
            exit.countDown()
            withTimeout(5_000) { running.join() }
            assertTrue(running.isCancelled)
            assertFalse(scripts.cancel("native"))
        } finally { exit.countDown(); scripts.close() }
    }

    @Test fun cancellationAfterAdmissionBeforeAwaitStillJoinsAndRetainsRegistration() = runBlocking {
        val exit = CountDownLatch(1)
        val task = Task(output, exitGate = exit)
        val prepared = CompletableDeferred<Unit>()
        val scripts = BoxScripts<Unit>(
            prepare = { _: ScriptRunRequest, created: (ScriptDebugTask) -> Unit ->
                created(task)
                prepared.complete(Unit)
                awaitCancellation()
            },
            validate = { _: Unit -> fail("Cancelled admission must not publish") },
        )
        try {
            val running = async(Dispatchers.Default) { scripts.run(request("admitted")) }
            withTimeout(5_000) { prepared.await() }
            assertTrue(scripts.cancel("admitted"))
            withTimeout(5_000) { task.joining.await() }
            assertEquals(0, task.waits.get())
            assertFalse(running.isCompleted)
            // Keep the identity until the admitted native worker has exited.
            assertTrue(scripts.cancel("admitted"))
            exit.countDown()
            withTimeout(5_000) { running.join() }
            assertTrue(running.isCancelled)
            assertFalse(scripts.cancel("admitted"))
        } finally { exit.countDown(); scripts.close() }
    }

    @Test fun changedProfileGenerationRejectsALateNativeResult() = runBlocking {
        val resultGate = CountDownLatch(1)
        val task = Task(output, resultGate)
        val generation = AtomicInteger(1)
        val scripts = BoxScripts(
            prepare = { _: ScriptRunRequest, created: (ScriptDebugTask) -> Unit -> created(task); generation.get() },
            validate = { expected: Int -> require(expected == generation.get()) { "Profile generation changed" } },
        )
        try {
            val running = async(Dispatchers.Default) { runCatching { scripts.run(request("late")) } }
            withTimeout(5_000) { task.entered.await() }
            generation.incrementAndGet()
            resultGate.countDown()
            assertTrue(withTimeout(5_000) { running.await() }.exceptionOrNull() is IllegalArgumentException)
            withTimeout(5_000) { task.joining.await() }
            assertFalse(scripts.cancel("late"))
        } finally { resultGate.countDown(); scripts.close() }
    }

    @Test fun closingAHostCancelsEveryRequestAndRejectsFurtherAdmission() = runBlocking {
        val task = Task(output, CountDownLatch(1))
        val scripts = BoxScripts(
            prepare = { _: ScriptRunRequest, created: (ScriptDebugTask) -> Unit -> created(task); Unit },
            validate = { _: Unit -> Unit },
        )
        val running = async(Dispatchers.Default) { scripts.run(request("closing")) }
        withTimeout(5_000) { task.entered.await() }
        scripts.close()
        withTimeout(5_000) { running.join() }
        assertTrue(running.isCancelled)
        assertTrue(runCatching { scripts.run(request("new")) }.exceptionOrNull() is IllegalStateException)
    }

    @Test fun closingAHostCancelsJobsBeforeUnblockingNativeResults() = runBlocking {
        val exit = CountDownLatch(1)
        val firstOwner = AtomicReference<Job>()
        val secondOwner = AtomicReference<Job>()
        val firstCancelledAtClose = CompletableDeferred<Boolean>()
        val secondCancelledAtClose = CompletableDeferred<Boolean>()
        // Each native worker stays behind its own result gate until it is
        // closed. Record cancellation before close releases that gate.
        val firstTask = Task(output, CountDownLatch(1), exit,
            onClose = { firstCancelledAtClose.complete(firstOwner.get().isCancelled); Unit })
        val secondTask = Task(output, CountDownLatch(1), exit,
            onClose = { secondCancelledAtClose.complete(secondOwner.get().isCancelled); Unit })
        val scripts = BoxScripts(
            prepare = { request: ScriptRunRequest, created: (ScriptDebugTask) -> Unit ->
                val first = request.index == 1
                // prepare runs in the same coroutineScope as the registered job.
                (if (first) firstOwner else secondOwner).set(currentCoroutineContext().job)
                created(if (first) firstTask else secondTask)
                Unit
            },
            validate = { _: Unit -> fail("A closed host must not publish a native result") },
        )
        try {
            val first = async(Dispatchers.Default) { scripts.run(request("first-close", 1)) }
            val second = async(Dispatchers.Default) { scripts.run(request("second-close", 2)) }
            withTimeout(5_000) { firstTask.entered.await(); secondTask.entered.await() }
            scripts.close()
            assertTrue(withTimeout(5_000) { firstCancelledAtClose.await() })
            assertTrue(withTimeout(5_000) { secondCancelledAtClose.await() })
            assertTrue(firstTask.closing.isCompleted)
            assertTrue(secondTask.closing.isCompleted)
            assertTrue(firstOwner.get().isCancelled)
            assertTrue(secondOwner.get().isCancelled)
            // Cancellation signals the workers; their registration and caller
            // remain pending until the actual native exit barrier opens.
            assertFalse(first.isCompleted)
            assertFalse(second.isCompleted)
            assertTrue(scripts.cancel("first-close"))
            assertTrue(scripts.cancel("second-close"))
            assertTrue(runCatching { scripts.run(request("after-close")) }.exceptionOrNull() is IllegalStateException)
            exit.countDown()
            withTimeout(5_000) { first.join(); second.join() }
            assertTrue(first.isCancelled)
            assertTrue(second.isCancelled)
            assertFalse(scripts.cancel("first-close"))
            assertFalse(scripts.cancel("second-close"))
        } finally { exit.countDown(); scripts.close() }
    }

    @Test fun aTaskAttachedDuringHostClosureCancelsItsOwnerBeforeReleasingItsResult() = runBlocking {
        val firstCloseEntered = CompletableDeferred<Unit>()
        val firstCloseRelease = CountDownLatch(1)
        val firstExit = CountDownLatch(1)
        val secondPreparing = CompletableDeferred<Unit>()
        val secondAttach = CompletableDeferred<Unit>()
        val secondExit = CountDownLatch(1)
        val secondOwner = AtomicReference<Job>()
        val secondCancelledAtClose = CompletableDeferred<Boolean>()
        val firstTask = Task(output, CountDownLatch(1), firstExit, onClose = {
            firstCloseEntered.complete(Unit)
            check(firstCloseRelease.await(5, TimeUnit.SECONDS))
        })
        val secondTask = Task(output, CountDownLatch(1), secondExit,
            onClose = { secondCancelledAtClose.complete(secondOwner.get().isCancelled); Unit })
        val scripts = BoxScripts(
            prepare = { request: ScriptRunRequest, created: (ScriptDebugTask) -> Unit ->
                if (request.index == 1) {
                    created(firstTask)
                } else {
                    secondOwner.set(currentCoroutineContext().job)
                    secondPreparing.complete(Unit)
                    secondAttach.await()
                    created(secondTask)
                }
                Unit
            },
            validate = { _: Unit -> fail("A task attached to a closed host must not publish a result") },
        )
        try {
            val first = async(Dispatchers.Default) { scripts.run(request("blocking-close", 1)) }
            withTimeout(5_000) { firstTask.entered.await() }
            val second = async(Dispatchers.Default) { scripts.run(request("late-attach", 2)) }
            withTimeout(5_000) { secondPreparing.await() }
            // close marks the host closed, then visits the first registration.
            // Its synchronous native close cannot reach the second registration
            // until this test releases the independent close gate.
            val closing = async(Dispatchers.IO) { scripts.close() }
            withTimeout(5_000) { firstCloseEntered.await() }
            assertFalse(closing.isCompleted)
            assertTrue(secondOwner.get().isActive)
            secondAttach.complete(Unit)
            assertTrue(withTimeout(5_000) { secondCancelledAtClose.await() })
            withTimeout(5_000) { secondTask.joining.await() }
            assertTrue(secondOwner.get().isCancelled)
            assertTrue(secondTask.closing.isCompleted)
            assertFalse(secondTask.entered.isCompleted)
            assertFalse(second.isCompleted)
            assertTrue(scripts.cancel("late-attach"))
            secondExit.countDown()
            withTimeout(5_000) { second.join() }
            assertTrue(second.isCancelled)
            assertFalse(scripts.cancel("late-attach"))
            assertFalse(closing.isCompleted)
            assertTrue(runCatching { scripts.run(request("after-late-attach")) }.exceptionOrNull() is IllegalStateException)
            firstCloseRelease.countDown()
            withTimeout(5_000) { closing.await() }
            assertTrue(firstTask.closing.isCompleted)
            assertFalse(first.isCompleted)
            firstExit.countDown()
            withTimeout(5_000) { first.join() }
            assertTrue(first.isCancelled)
            assertFalse(scripts.cancel("blocking-close"))
        } finally {
            secondAttach.complete(Unit)
            firstCloseRelease.countDown()
            firstExit.countDown()
            secondExit.countDown()
            scripts.close()
        }
    }

    @Test fun requestParsingRejectsFractionalOrOverflowingOriginalArrayIndexes() {
        val args = mapOf("requestId" to "index", "profileId" to 1L, "revision" to revision, "type" to "generic", "index" to 3)
        assertEquals(3, ScriptRunRequest.read(args).index)
        for (index in listOf<Number>(-1, 1.5, Int.MAX_VALUE.toLong() + 1, Double.NaN)) {
            assertTrue(runCatching { ScriptRunRequest.read(args + ("index" to index)) }.isFailure)
        }
    }
}
