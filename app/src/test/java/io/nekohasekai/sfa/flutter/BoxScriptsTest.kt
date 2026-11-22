package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Assert.*
import org.junit.Test
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger

class BoxScriptsTest {
    private val revision = "a".repeat(64)
    private fun request(id: String, index: Int = 0) = ScriptRunRequest(id, 1, revision, "generic", index)
    private val output = mapOf("result" to "{\"ok\":true}", "error" to "", "logs" to listOf("script output"))

    private class Task(
        private val output: Map<String, Any?>,
        private val resultGate: CountDownLatch = CountDownLatch(0),
        private val exitGate: CountDownLatch = CountDownLatch(0),
    ) : ScriptDebugTask {
        val entered = CompletableDeferred<Unit>()
        val closing = CompletableDeferred<Unit>()
        val joining = CompletableDeferred<Unit>()
        val waits = AtomicInteger()
        override fun close() {
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

    @Test fun requestParsingRejectsFractionalOrOverflowingOriginalArrayIndexes() {
        val args = mapOf("requestId" to "index", "profileId" to 1L, "revision" to revision, "type" to "generic", "index" to 3)
        assertEquals(3, ScriptRunRequest.read(args).index)
        for (index in listOf<Number>(-1, 1.5, Int.MAX_VALUE.toLong() + 1, Double.NaN)) {
            assertTrue(runCatching { ScriptRunRequest.read(args + ("index" to index)) }.isFailure)
        }
    }
}
