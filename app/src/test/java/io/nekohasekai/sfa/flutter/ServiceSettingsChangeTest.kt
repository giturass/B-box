package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class ServiceSettingsChangeTest {
    private val previous = mapOf("allowBypass" to false)
    private val values = mapOf("allowBypass" to true)

    @Test fun waitsForStartedAfterPersistingTheSetting() = runTest {
        val start = ServiceStartResult()
        var saved: Map<String, Any?> = previous
        var outcome: Result<Unit>? = null
        launch {
            outcome = runCatching {
                ServiceSettingsChange.apply(previous, values, { saved = it }) { recover ->
                    assertFalse(recover)
                    start.awaitStarted { assertEquals(values, saved) }
                }
            }
        }
        runCurrent()
        assertEquals(values, saved)
        assertNull(outcome)
        start.started()
        runCurrent()
        assertTrue(outcome!!.isSuccess)
    }

    @Test fun failedRestartRestoresOnlyTheChangedSettingAndWaitsForRecovery() = runTest {
        val start = ServiceStartResult()
        val saved = mutableMapOf<String, Any?>("allowBypass" to false, "autoRedirect" to true)
        val restarts = mutableListOf<Boolean>()
        var outcome: Result<Unit>? = null
        launch {
            outcome = runCatching {
                ServiceSettingsChange.apply(previous, values, { saved.putAll(it) }) { recover ->
                    restarts.add(recover)
                    start.awaitStarted {
                        assertEquals(if (recover) false else true, saved["allowBypass"])
                    }
                }
            }
        }
        runCurrent()
        start.failed(IllegalStateException("Startup failed"))
        runCurrent()
        assertEquals(listOf(false, true), restarts)
        assertEquals(false, saved["allowBypass"])
        assertEquals(true, saved["autoRedirect"])
        assertNull(outcome)
        start.started()
        runCurrent()
        assertEquals("Startup failed", outcome!!.exceptionOrNull()!!.message)
    }

    @Test fun startupTimeoutRestoresTheSettingAndRecoversTheService() = runTest {
        val start = ServiceStartResult()
        var saved: Map<String, Any?> = previous
        var recovered = false
        var failure: Throwable? = null
        launch {
            failure = runCatching {
                ServiceSettingsChange.apply(previous, values, { saved = it }) { recover ->
                    start.awaitStarted(100) {
                        if (recover) {
                            assertEquals(previous, saved)
                            recovered = true
                            start.started()
                        }
                    }
                }
            }.exceptionOrNull()
        }
        runCurrent()
        advanceTimeBy(100)
        runCurrent()
        assertTrue(failure is TimeoutCancellationException)
        assertEquals(previous, saved)
        assertTrue(recovered)
    }

    @Test fun failedSaveRestoresTheSettingWithoutRestarting() = runTest {
        var saved: Map<String, Any?> = previous
        var restarts = 0
        val failure = runCatching {
            ServiceSettingsChange.apply(previous, values, {
                saved = it
                if (it == values) error("Unable to flush settings")
            }) { restarts++ }
        }.exceptionOrNull()
        assertEquals("Unable to flush settings", failure!!.message)
        assertEquals(previous, saved)
        assertEquals(0, restarts)
    }

    @Test fun recoveryErrorsDoNotHideTheOriginalFailure() = runTest {
        var saved: Map<String, Any?> = previous
        val failure = runCatching {
            ServiceSettingsChange.apply(previous, values, { saved = it }) { recover ->
                error(if (recover) "Recovery failed" else "Startup failed")
            }
        }.exceptionOrNull()!!
        assertEquals("Startup failed", failure.message)
        assertEquals(previous, saved)
        assertEquals(listOf("Recovery failed"), failure.suppressed.map { it.message })
    }

    @Test fun cancellationStillRestoresTheSettingAndRecovers() = runTest {
        var saved: Map<String, Any?> = previous
        var recovered = false
        val job = launch {
            ServiceSettingsChange.apply(previous, values, { saved = it }) { recover ->
                if (recover) {
                    currentCoroutineContext().ensureActive()
                    assertEquals(previous, saved)
                    recovered = true
                } else awaitCancellation()
            }
        }
        runCurrent()
        assertEquals(values, saved)
        job.cancel()
        runCurrent()
        job.join()
        assertEquals(previous, saved)
        assertTrue(recovered)
        assertTrue(job.isCancelled)
    }
}
