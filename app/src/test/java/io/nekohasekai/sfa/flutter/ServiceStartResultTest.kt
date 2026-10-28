package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class ServiceStartResultTest {
    @Test fun waitsForAcknowledgementAndPropagatesFailure() = runTest {
        val start = ServiceStartResult()
        var commandSent = false
        var outcome: Result<Unit>? = null
        launch { outcome = runCatching { start.awaitStarted { commandSent = true } } }
        runCurrent()
        assertTrue(commandSent)
        assertNull(outcome)
        val failure = IllegalStateException("Port already in use")
        start.failed(failure)
        runCurrent()
        assertSame(failure, outcome!!.exceptionOrNull())
        outcome = null
        launch { outcome = runCatching { start.awaitStarted {} } }
        runCurrent()
        assertNull(outcome)
        start.started()
        runCurrent()
        assertTrue(outcome!!.isSuccess)
    }

    @Test fun timeoutAndSynchronousFailureReleaseTheWaiter() = runTest {
        val start = ServiceStartResult()
        var failure: Throwable? = null
        launch { failure = runCatching { start.awaitStarted(100) {} }.exceptionOrNull() }
        runCurrent()
        advanceTimeBy(100)
        runCurrent()
        assertTrue(failure is TimeoutCancellationException)
        val rejected = IllegalStateException("Foreground service rejected")
        assertSame(rejected, runCatching { start.awaitStarted { throw rejected } }.exceptionOrNull())
        start.awaitStarted { start.started() }
    }
}
