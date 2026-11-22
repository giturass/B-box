package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.async
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.job
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withContext
import org.junit.Assert.*
import org.junit.Test
import java.io.Closeable

@OptIn(ExperimentalCoroutinesApi::class)
class UiIdleTriggerTest {
    @Test fun advancingCoroutineTimeStillRequiresAnExternalSystemStart() = runTest {
        val scheduler = FakeSystemScheduler()
        val events = mutableListOf<String>()
        val finishDispatched = CompletableDeferred<Unit>()
        val policy = UiExitPolicy(this, { true }, { events += "drain" }, { events += "exit" },
            awaitIdle = scheduler.trigger::awaitIdle, awaitExitDispatch = { finishDispatched.await() })
        val activity = Any()
        policy.activityStarted(activity, "Main")
        policy.activityStopped(activity)
        runCurrent()
        val request = scheduler.requests.single()
        assertEquals(IDLE_TIMEOUT_MILLIS, request.delayMillis)
        assertEquals(INITIAL_SYSTEM_TIME_MILLIS + IDLE_TIMEOUT_MILLIS, request.deadlineMillis)

        // Android's clock keeps moving while a cached UI's coroutine timer cannot run.
        scheduler.nowMillis += IDLE_TIMEOUT_MILLIS * 2
        advanceTimeBy(IDLE_TIMEOUT_MILLIS * 2)
        runCurrent()
        assertTrue(events.isEmpty())
        assertEquals(1, scheduler.requests.size)
        assertTrue(scheduler.start(request) { events += "finish" })
        runCurrent()
        assertEquals(listOf("drain", "finish"), events)
        finishDispatched.complete(Unit)
        runCurrent()
        assertEquals(listOf("drain", "finish", "exit"), events)
        assertEquals(0, scheduler.cancelCalls)
        policy.close()
    }

    @Test fun bothTokenAndDeadlineMustMatchForStartAndStop() = runTest {
        val scheduler = FakeSystemScheduler()
        val waiting = async { scheduler.trigger.awaitIdle(IDLE_TIMEOUT_MILLIS) }
        runCurrent()
        val request = scheduler.requests.single()
        scheduler.nowMillis = request.deadlineMillis
        var rejectedFinishes = 0
        for ((token, deadline) in listOf(
            null to request.deadlineMillis,
            "stale-token" to request.deadlineMillis,
            request.token to request.deadlineMillis - 1,
            request.token to request.deadlineMillis + 1,
        )) {
            assertFalse(scheduler.trigger.start(token, deadline) { rejectedFinishes++ })
            scheduler.trigger.stop(token, deadline)
        }
        runCurrent()
        assertTrue(waiting.isActive)
        assertFalse(waiting.isCompleted)
        assertEquals(0, rejectedFinishes)
        assertEquals(0, scheduler.cancelCalls)
        var finishes = 0
        assertTrue(scheduler.start(request) { finishes++ })
        waiting.await().close()
        assertEquals(1, finishes)
    }

    @Test fun anEarlySystemStartKeepsUiAliveWithoutRunningTheDrain() = runTest {
        val scheduler = FakeSystemScheduler()
        var drains = 0
        var exits = 0
        var finishes = 0
        val failures = mutableListOf<Exception>()
        val policy = UiExitPolicy(this, { true }, { drains++ }, { exits++ },
            onFailure = { failures += it }, awaitIdle = scheduler.trigger::awaitIdle)
        val activity = Any()
        policy.activityStarted(activity, "Main")
        policy.activityStopped(activity)
        runCurrent()
        val request = scheduler.requests.single()
        scheduler.nowMillis = request.deadlineMillis - 1
        assertFalse(scheduler.start(request) { finishes++ })
        runCurrent()
        assertEquals(0, drains)
        assertEquals(0, exits)
        assertEquals(0, finishes)
        assertTrue(failures.single() is IllegalStateException)
        assertEquals(1, scheduler.cancelCalls)
        scheduler.nowMillis = request.deadlineMillis
        assertFalse(scheduler.start(request) { finishes++ })
        assertEquals(0, finishes)
        policy.close()
    }

    @Test fun aColdProcessRejectsThePreviousProcessesJobEvenWithTheSameDeadline() = runTest {
        val previousScheduler = FakeSystemScheduler()
        val previousOwner = async { previousScheduler.trigger.awaitIdle(IDLE_TIMEOUT_MILLIS) }
        runCurrent()
        val previous = previousScheduler.requests.single()
        val scheduler = FakeSystemScheduler()
        var staleFinishes = 0
        assertFalse(scheduler.start(previous) { staleFinishes++ })
        scheduler.stop(previous)

        val currentOwner = async { scheduler.trigger.awaitIdle(IDLE_TIMEOUT_MILLIS) }
        runCurrent()
        val current = scheduler.requests.single()
        assertEquals(previous.deadlineMillis, current.deadlineMillis)
        assertNotEquals(previous.token, current.token)
        scheduler.nowMillis = current.deadlineMillis
        assertFalse(scheduler.start(previous) { staleFinishes++ })
        scheduler.stop(previous)
        runCurrent()
        assertTrue(currentOwner.isActive)
        assertEquals(0, staleFinishes)
        assertEquals(0, scheduler.cancelCalls)
        var finishes = 0
        assertTrue(scheduler.start(current) { finishes++ })
        currentOwner.await().close()
        assertEquals(1, finishes)
        previousOwner.cancel()
        runCurrent()
        assertEquals(1, previousScheduler.cancelCalls)
    }

    @Test fun duplicateStartsAndRepeatedCloseFinishTheExecutionOnlyOnce() = runTest {
        val scheduler = FakeSystemScheduler()
        val waiting = async { scheduler.trigger.awaitIdle(IDLE_TIMEOUT_MILLIS) }
        runCurrent()
        val request = scheduler.requests.single()
        scheduler.nowMillis = request.deadlineMillis
        var finishes = 0
        var duplicateFinishes = 0
        assertTrue(scheduler.start(request) { finishes++ })
        // A duplicate callback can arrive before the awaiting coroutine is dispatched.
        assertFalse(scheduler.start(request) { duplicateFinishes++ })
        val execution = waiting.await()
        assertFalse(scheduler.start(request) { duplicateFinishes++ })
        assertEquals(0, finishes)
        execution.close()
        execution.close()
        assertFalse(scheduler.start(request) { duplicateFinishes++ })
        scheduler.stop(request)
        assertEquals(1, finishes)
        assertEquals(0, duplicateFinishes)
        assertEquals(0, scheduler.cancelCalls)
    }

    @Test fun returningToForegroundCancelsTheJobAndRestartsTheFullIdlePeriod() = runTest {
        val scheduler = FakeSystemScheduler()
        var drains = 0
        var exits = 0
        var finishes = 0
        val policy = UiExitPolicy(this, { true }, { drains++ }, { exits++ },
            awaitIdle = scheduler.trigger::awaitIdle)
        val activity = Any()
        policy.activityStarted(activity, "Main")
        policy.activityStopped(activity)
        runCurrent()
        val previous = scheduler.requests.single()
        scheduler.nowMillis = previous.deadlineMillis - 1
        policy.activityStarted(activity, "Main")
        runCurrent()
        assertEquals(1, scheduler.cancelCalls)
        scheduler.nowMillis = previous.deadlineMillis
        assertFalse(scheduler.start(previous) { finishes++ })
        assertEquals(0, drains)
        assertEquals(0, exits)

        policy.activityStopped(activity)
        runCurrent()
        val current = scheduler.requests.last()
        assertEquals(2, scheduler.requests.size)
        assertNotEquals(previous.token, current.token)
        assertEquals(scheduler.nowMillis + IDLE_TIMEOUT_MILLIS, current.deadlineMillis)
        assertEquals(IDLE_TIMEOUT_MILLIS, current.delayMillis)
        scheduler.nowMillis = current.deadlineMillis
        assertTrue(scheduler.start(current) { finishes++ })
        runCurrent()
        assertEquals(1, drains)
        assertEquals(1, exits)
        assertEquals(1, finishes)
        policy.close()
    }

    @Test fun replacingAWaitingOwnerDoesNotCancelTheNewSystemJob() = runTest {
        val scheduler = FakeSystemScheduler()
        val previousOwner = async { scheduler.trigger.awaitIdle(IDLE_TIMEOUT_MILLIS) }
        runCurrent()
        val previous = scheduler.requests.single()
        val currentOwner = async { scheduler.trigger.awaitIdle(IDLE_TIMEOUT_MILLIS) }
        runCurrent()
        val current = scheduler.requests.last()
        assertEquals(2, scheduler.requests.size)
        assertNotEquals(previous.token, current.token)
        assertTrue(previousOwner.isCancelled)
        // Joining the old await must cancel its job exactly once before scheduling anew.
        assertEquals(1, scheduler.cancelCalls)
        scheduler.nowMillis = current.deadlineMillis
        assertFalse(scheduler.start(previous))
        scheduler.stop(previous)
        runCurrent()
        assertTrue(currentOwner.isActive)
        assertEquals(1, scheduler.cancelCalls)
        var finishes = 0
        assertTrue(scheduler.start(current) { finishes++ })
        currentOwner.await().close()
        assertEquals(1, finishes)
    }

    @Test fun oldExecutionCloseAndStopCannotTouchAReplacementDuringDrain() = runTest {
        val scheduler = FakeSystemScheduler()
        val drained = CompletableDeferred<Unit>()
        var previousExecution: Closeable? = null
        var previousFinishes = 0
        val previousOwner = launch {
            val execution = scheduler.trigger.awaitIdle(IDLE_TIMEOUT_MILLIS)
            previousExecution = execution
            try {
                withContext(NonCancellable) { drained.await() }
            } finally {
                execution.close()
            }
        }
        runCurrent()
        val previous = scheduler.requests.single()
        scheduler.nowMillis = previous.deadlineMillis
        assertTrue(scheduler.start(previous) { previousFinishes++ })
        runCurrent()
        assertNotNull(previousExecution)

        val currentOwner = async { scheduler.trigger.awaitIdle(IDLE_TIMEOUT_MILLIS) }
        val restartedAt = scheduler.nowMillis
        runCurrent()
        assertTrue(previousOwner.isCancelled)
        assertFalse(previousOwner.isCompleted)
        assertEquals(0, previousFinishes)
        assertEquals(1, scheduler.requests.size)
        assertTrue(currentOwner.isActive)
        // The old JobService must stay bound through recovery, while the new idle
        // deadline remains measured from this background visit rather than recovery.
        scheduler.nowMillis += 30_000L
        advanceTimeBy(30_000L)
        runCurrent()
        assertEquals(0, previousFinishes)
        assertEquals(1, scheduler.requests.size)
        drained.complete(Unit)
        runCurrent()
        val current = scheduler.requests.last()
        assertTrue(previousOwner.isCompleted)
        assertEquals(2, scheduler.requests.size)
        assertEquals(restartedAt + IDLE_TIMEOUT_MILLIS, current.deadlineMillis)
        assertEquals(IDLE_TIMEOUT_MILLIS - 30_000L, current.delayMillis)
        assertTrue(currentOwner.isActive)
        assertEquals(1, previousFinishes)
        previousExecution!!.close()
        previousExecution!!.close()
        scheduler.stop(previous)
        runCurrent()
        assertTrue(currentOwner.isActive)
        assertEquals(1, previousFinishes)
        assertEquals(0, scheduler.cancelCalls)
        scheduler.nowMillis = current.deadlineMillis
        var currentFinishes = 0
        assertTrue(scheduler.start(current) { currentFinishes++ })
        currentOwner.await().close()
        assertEquals(1, currentFinishes)
    }

    @Test fun stoppingARunningSystemJobCancelsItsDrainOwnerAndPreventsExit() = runTest {
        val scheduler = FakeSystemScheduler()
        val drained = CompletableDeferred<Unit>()
        var drainOwner: Job? = null
        var exits = 0
        var finishes = 0
        var failures = 0
        val policy = UiExitPolicy(this, { true }, {
            drainOwner = currentCoroutineContext().job
            withContext(NonCancellable) { drained.await() }
        }, { exits++ }, onFailure = { failures++ }, awaitIdle = scheduler.trigger::awaitIdle)
        val activity = Any()
        policy.activityStarted(activity, "Main")
        policy.activityStopped(activity)
        runCurrent()
        val request = scheduler.requests.single()
        scheduler.nowMillis = request.deadlineMillis
        assertTrue(scheduler.start(request) { finishes++ })
        runCurrent()
        assertTrue(drainOwner!!.isActive)
        scheduler.stop(request)
        assertTrue(drainOwner!!.isCancelled)
        assertFalse(drainOwner!!.isCompleted)
        drained.complete(Unit)
        runCurrent()
        assertTrue(drainOwner!!.isCompleted)
        assertEquals(0, exits)
        // onStopJob already ended the execution; cleanup must not call jobFinished.
        assertEquals(0, finishes)
        assertEquals(0, failures)
        assertEquals(0, scheduler.cancelCalls)
        assertFalse(scheduler.start(request) { finishes++ })
        policy.close()
    }

    @Test fun aRejectedSystemScheduleKeepsUiAliveAndAllowsTheNextIdleVisit() = runTest {
        val scheduler = FakeSystemScheduler()
        scheduler.scheduleFailure = IllegalStateException("System rejected the idle job")
        var drains = 0
        var exits = 0
        val failures = mutableListOf<Exception>()
        val policy = UiExitPolicy(this, { true }, { drains++ }, { exits++ },
            onFailure = { failures += it }, awaitIdle = scheduler.trigger::awaitIdle)
        val activity = Any()
        policy.activityStarted(activity, "Main")
        policy.activityStopped(activity)
        runCurrent()
        val rejected = scheduler.requests.single()
        scheduler.nowMillis = rejected.deadlineMillis
        advanceTimeBy(IDLE_TIMEOUT_MILLIS)
        runCurrent()
        assertEquals(0, drains)
        assertEquals(0, exits)
        assertEquals("System rejected the idle job", failures.single().message)
        assertEquals(1, scheduler.cancelCalls)
        assertFalse(scheduler.start(rejected))

        scheduler.scheduleFailure = null
        policy.activityStarted(activity, "Main")
        policy.activityStopped(activity)
        runCurrent()
        val current = scheduler.requests.last()
        assertEquals(2, scheduler.requests.size)
        assertNotEquals(rejected.token, current.token)
        assertEquals(scheduler.nowMillis + IDLE_TIMEOUT_MILLIS, current.deadlineMillis)
        scheduler.nowMillis = current.deadlineMillis
        assertTrue(scheduler.start(current))
        runCurrent()
        assertEquals(1, drains)
        assertEquals(1, exits)
        assertEquals(1, failures.size)
        policy.close()
    }

    @Test fun foregroundReturnQueuedByJobCompletionRunsBeforeTheFinalExitCheck() = runTest {
        val scheduler = FakeSystemScheduler()
        val events = mutableListOf<String>()
        var exits = 0
        val policy = UiExitPolicy(this, { true }, { events += "drain" }, { exits++ },
            awaitIdle = scheduler.trigger::awaitIdle)
        val activity = Any()
        policy.activityStarted(activity, "Main")
        policy.activityStopped(activity)
        runCurrent()
        val request = scheduler.requests.single()
        scheduler.nowMillis = request.deadlineMillis
        assertTrue(scheduler.start(request) {
            events += "finish"
            // Model the main Handler's completion message and a foreground lifecycle return.
            launch {
                events += "foreground"
                policy.activityStarted(activity, "Main")
            }
        })
        runCurrent()
        assertEquals(listOf("drain", "finish", "foreground"), events)
        assertEquals(0, exits)
        assertEquals(0, scheduler.cancelCalls)
        policy.close()
    }

    @Test fun aFailedDrainFinishesTheSystemExecutionButKeepsUiAlive() = runTest {
        val scheduler = FakeSystemScheduler()
        var exits = 0
        var finishes = 0
        val failures = mutableListOf<Exception>()
        val policy = UiExitPolicy(this, { true }, { error("Drain failed") }, { exits++ },
            onFailure = { failures += it }, awaitIdle = scheduler.trigger::awaitIdle)
        val activity = Any()
        policy.activityStarted(activity, "Main")
        policy.activityStopped(activity)
        runCurrent()
        val request = scheduler.requests.single()
        scheduler.nowMillis = request.deadlineMillis
        assertTrue(scheduler.start(request) { finishes++ })
        runCurrent()
        assertEquals(0, exits)
        assertEquals(1, finishes)
        assertEquals("Drain failed", failures.single().message)
        assertEquals(0, scheduler.cancelCalls)
        policy.close()
    }

    private data class Request(val token: String, val deadlineMillis: Long, val delayMillis: Long)

    /** Records Android scheduling calls without implementing an idle timer or request gate. */
    private class FakeSystemScheduler {
        var nowMillis = INITIAL_SYSTEM_TIME_MILLIS
        var scheduleFailure: Exception? = null
        val requests = mutableListOf<Request>()
        var cancelCalls = 0
        val trigger = UiIdleTrigger(
            nowMillis = { nowMillis },
            schedule = { token, deadline, delay ->
                requests += Request(token, deadline, delay)
                scheduleFailure?.let { throw it }
            },
            cancel = { cancelCalls++ },
        )

        fun start(request: Request, finish: () -> Unit = {}): Boolean =
            trigger.start(request.token, request.deadlineMillis, finish)

        fun stop(request: Request) = trigger.stop(request.token, request.deadlineMillis)
    }

    companion object {
        private const val IDLE_TIMEOUT_MILLIS = 5 * 60 * 1_000L
        private const val INITIAL_SYSTEM_TIME_MILLIS = 42_000L
    }
}
