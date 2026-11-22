package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withContext
import org.junit.Assert.*
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class UiExitPolicyTest {
    @Test fun stopsOnlyAfterAllActivitiesAreInvisibleAndReturningCancelsTheTimer() = runTest {
        var exits = 0
        var drains = 0
        val policy = UiExitPolicy(this, { true }, { drains++ }, { exits++ })
        val main = Any()
        val share = Any()
        policy.activityStarted(main, "Main")
        policy.activityStarted(share, "Share")
        policy.activityStopped(main)
        advanceTimeBy(IDLE_TIMEOUT_MILLIS + 1)
        runCurrent()
        assertEquals(0, drains)
        assertEquals(0, exits)
        policy.activityStopped(share)
        advanceTimeBy(IDLE_TIMEOUT_MILLIS - 1)
        runCurrent()
        assertEquals(0, drains)
        assertEquals(0, exits)
        policy.activityStarted(main, "Main")
        advanceTimeBy(IDLE_TIMEOUT_MILLIS + 1)
        runCurrent()
        assertEquals(0, drains)
        assertEquals(0, exits)
        policy.activityStopped(main)
        advanceTimeBy(IDLE_TIMEOUT_MILLIS - 1)
        runCurrent()
        assertEquals(0, drains)
        assertEquals(0, exits)
        advanceTimeBy(1)
        runCurrent()
        assertEquals(1, drains)
        assertEquals(1, exits)
        policy.close()
    }

    @Test fun repeatedBackgroundVisitsDoNotAccumulateIdleTime() = runTest {
        var exits = 0
        var drains = 0
        val policy = UiExitPolicy(this, { true }, { drains++ }, { exits++ })
        val activity = Any()
        policy.activityStarted(activity, "Main")
        repeat(3) {
            policy.activityStopped(activity)
            advanceTimeBy(120_000)
            runCurrent()
            assertEquals(0, drains)
            assertEquals(0, exits)
            policy.activityStarted(activity, "Main")
            advanceTimeBy(10_000)
            runCurrent()
        }
        // Total background time already exceeds five minutes, but each return resets it.
        policy.activityStopped(activity)
        advanceTimeBy(IDLE_TIMEOUT_MILLIS - 1)
        runCurrent()
        assertEquals(0, drains)
        assertEquals(0, exits)
        advanceTimeBy(1)
        runCurrent()
        assertEquals(1, drains)
        assertEquals(1, exits)
        policy.close()
    }

    @Test fun externalResultsKeepTheProcessAliveAndClosingALeaseIsIdempotent() = runTest {
        var exits = 0
        var drains = 0
        val policy = UiExitPolicy(this, { true }, { drains++ }, { exits++ })
        val activity = Any()
        policy.activityStarted(activity, "Main")
        val document = policy.acquireLease()
        val permission = policy.acquireLease()
        policy.activityStopped(activity)
        advanceTimeBy(IDLE_TIMEOUT_MILLIS + 1)
        runCurrent()
        assertEquals(0, drains)
        assertEquals(0, exits)
        document.close()
        document.close()
        advanceTimeBy(IDLE_TIMEOUT_MILLIS + 1)
        runCurrent()
        assertEquals(0, drains)
        assertEquals(0, exits)
        permission.close()
        advanceTimeBy(IDLE_TIMEOUT_MILLIS - 1)
        runCurrent()
        assertEquals(0, drains)
        assertEquals(0, exits)
        advanceTimeBy(1)
        runCurrent()
        assertEquals(1, drains)
        assertEquals(1, exits)
        policy.close()
    }

    @Test fun anExternalRequestInterruptsTheTimerAndItsCompletionStartsAFullIdlePeriod() = runTest {
        var exits = 0
        var drains = 0
        val policy = UiExitPolicy(this, { true }, { drains++ }, { exits++ })
        val activity = Any()
        policy.activityStarted(activity, "Main")
        policy.activityStopped(activity)
        advanceTimeBy(IDLE_TIMEOUT_MILLIS - 1)
        runCurrent()
        assertEquals(0, drains)
        assertEquals(0, exits)
        val request = policy.acquireLease()
        advanceTimeBy(IDLE_TIMEOUT_MILLIS + 1)
        runCurrent()
        assertEquals(0, drains)
        assertEquals(0, exits)
        request.close()
        advanceTimeBy(IDLE_TIMEOUT_MILLIS - 1)
        runCurrent()
        assertEquals(0, drains)
        assertEquals(0, exits)
        advanceTimeBy(1)
        runCurrent()
        assertEquals(1, drains)
        assertEquals(1, exits)
        policy.close()
    }

    @Test fun aSlowConfigurationReplacementIsNotTreatedAsBackgrounding() = runTest {
        var exits = 0
        var drains = 0
        val policy = UiExitPolicy(this, { true }, { drains++ }, { exits++ })
        val original = Any()
        policy.activityStarted(original, "Main")
        policy.activityStopped(original, "Main")
        advanceTimeBy(IDLE_TIMEOUT_MILLIS + 1)
        runCurrent()
        assertEquals(0, drains)
        assertEquals(0, exits)
        val replacement = Any()
        policy.activityStarted(replacement, "Main")
        // An old instance's delayed destroy callback cannot restore an already satisfied gate.
        policy.activityStopped(original, "Main")
        policy.activityStopped(replacement)
        advanceTimeBy(IDLE_TIMEOUT_MILLIS)
        runCurrent()
        assertEquals(1, drains)
        assertEquals(1, exits)
        policy.close()
    }

    @Test fun simultaneousRecreationsOfTheSameActivityClassKeepIndependentGates() = runTest {
        class ShareWindow
        var exits = 0
        var drains = 0
        val policy = UiExitPolicy(this, { true }, { drains++ }, { exits++ })
        val first = ShareWindow()
        val second = ShareWindow()
        val firstIdentity = "00000000-0000-0000-0000-000000000001"
        val secondIdentity = "00000000-0000-0000-0000-000000000002"
        assertEquals(first.javaClass, second.javaClass)
        policy.activityStarted(first, firstIdentity)
        policy.activityStarted(second, secondIdentity)
        policy.activityStopped(first, firstIdentity)
        policy.activityStopped(second, secondIdentity)
        advanceTimeBy(IDLE_TIMEOUT_MILLIS + 1)
        runCurrent()
        assertEquals(0, drains)
        assertEquals(0, exits)

        val firstReplacement = ShareWindow()
        policy.activityStarted(firstReplacement, firstIdentity)
        policy.activityStopped(firstReplacement)
        // Completing the first recreation cannot clear the second window's gate.
        advanceTimeBy(IDLE_TIMEOUT_MILLIS + 1)
        runCurrent()
        assertEquals(0, drains)
        assertEquals(0, exits)
        // A destroyed old instance cannot add its already satisfied gate again.
        policy.activityStopped(first, firstIdentity)

        val secondReplacement = ShareWindow()
        policy.activityStarted(secondReplacement, secondIdentity)
        policy.activityStopped(secondReplacement)
        advanceTimeBy(IDLE_TIMEOUT_MILLIS)
        runCurrent()
        assertEquals(1, drains)
        assertEquals(1, exits)
        policy.close()
    }

    @Test fun resumingDuringEvenANonCancellableDrainPreventsExit() = runTest {
        var exits = 0
        val draining = CompletableDeferred<Unit>()
        val drained = CompletableDeferred<Unit>()
        val policy = UiExitPolicy(this, { true }, {
            draining.complete(Unit)
            withContext(NonCancellable) { drained.await() }
        }, { exits++ })
        val activity = Any()
        policy.activityStarted(activity, "Main")
        policy.activityStopped(activity)
        advanceTimeBy(IDLE_TIMEOUT_MILLIS)
        runCurrent()
        assertTrue(draining.isCompleted)
        policy.activityStarted(activity, "Main")
        drained.complete(Unit)
        runCurrent()
        assertEquals(0, exits)
        policy.close()
    }

    @Test fun anExternalRequestStartingDuringDrainPreventsExitUntilItCompletes() = runTest {
        var exits = 0
        val drained = CompletableDeferred<Unit>()
        val policy = UiExitPolicy(this, { true }, {
            withContext(NonCancellable) { drained.await() }
        }, { exits++ })
        val activity = Any()
        policy.activityStarted(activity, "Main")
        policy.activityStopped(activity)
        advanceTimeBy(IDLE_TIMEOUT_MILLIS)
        runCurrent()
        val request = policy.acquireLease()
        drained.complete(Unit)
        runCurrent()
        assertEquals(0, exits)
        request.close()
        advanceTimeBy(IDLE_TIMEOUT_MILLIS - 1)
        runCurrent()
        assertEquals(0, exits)
        advanceTimeBy(1)
        runCurrent()
        assertEquals(1, exits)
        policy.close()
    }

    @Test fun theDefaultProcessAndUnknownIdentitiesCannotBeKilled() = runTest {
        assertTrue(isUiProcessName("io.ericlee.bfa", "io.ericlee.bfa:ui"))
        for (name in listOf(null, "io.ericlee.bfa", "io.ericlee.bfa:ui-worker", "io.ericlee.bfa:core", "another.app:ui")) {
            assertFalse(isUiProcessName("io.ericlee.bfa", name))
        }
        var exits = 0
        var drains = 0
        var uiProcess = false
        val policy = UiExitPolicy(this, { uiProcess }, { drains++ }, { exits++ })
        val activity = Any()
        policy.activityStarted(activity, "Main")
        policy.activityStopped(activity)
        advanceTimeBy(IDLE_TIMEOUT_MILLIS + 1)
        runCurrent()
        assertEquals(0, drains)
        assertEquals(0, exits)
        uiProcess = true
        policy.activityStarted(activity, "Main")
        policy.activityStopped(activity)
        advanceTimeBy(IDLE_TIMEOUT_MILLIS - 1)
        uiProcess = false
        advanceTimeBy(1)
        runCurrent()
        assertEquals(0, drains)
        assertEquals(0, exits)
        policy.close()
    }

    @Test fun aFailedDrainLeavesTheProcessAlive() = runTest {
        var exits = 0
        var failures = 0
        val policy = UiExitPolicy(this, { true }, { error("Drain failed") }, { exits++ }, { failures++ })
        val activity = Any()
        policy.activityStarted(activity, "Main")
        policy.activityStopped(activity)
        advanceTimeBy(IDLE_TIMEOUT_MILLIS - 1)
        runCurrent()
        assertEquals(0, exits)
        assertEquals(0, failures)
        advanceTimeBy(10_001)
        runCurrent()
        assertEquals(0, exits)
        assertEquals(1, failures)
        policy.close()
    }

    companion object {
        private const val IDLE_TIMEOUT_MILLIS = 5 * 60 * 1_000L
    }
}
