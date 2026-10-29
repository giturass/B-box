package io.nekohasekai.sfa.database

import io.nekohasekai.sfa.flutter.ServiceStartResult
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
class ProfileChangeTest {
    @Test fun restoresContentAndTimestampBeforeWaitingForServiceRecovery() = runTest {
        val start = ServiceStartResult()
        val original = IllegalStateException("New configuration failed")
        var content = "old"
        var updatedAt = 10L
        var outcome: Result<Unit>? = null
        val applications = mutableListOf<Boolean>()
        launch {
            outcome = runCatching {
                ProfileChange.apply(
                    save = { content = "new"; updatedAt = 20L },
                    restore = listOf<suspend () -> Unit>({ content = "old" }, { updatedAt = 10L }),
                ) { recover ->
                    applications.add(recover)
                    start.awaitStarted {
                        assertEquals(if (recover) "old" else "new", content)
                        assertEquals(if (recover) 10L else 20L, updatedAt)
                    }
                }
            }
        }
        runCurrent()
        assertEquals("new", content)
        assertNull(outcome)
        start.failed(original)
        runCurrent()
        assertEquals(listOf(false, true), applications)
        assertEquals("old", content)
        assertEquals(10L, updatedAt)
        assertNull(outcome)
        start.started()
        runCurrent()
        assertSame(original, outcome!!.exceptionOrNull())
    }

    @Test fun partiallyFailedSaveRestoresDataWithoutStartingAService() = runTest {
        var content = "old"
        var applications = 0
        val original = IllegalStateException("Unable to update subscription metadata")
        val failure = runCatching {
            ProfileChange.apply(
                save = { content = "new"; throw original },
                restore = listOf<suspend () -> Unit>({ content = "old" }),
            ) { applications++ }
        }.exceptionOrNull()
        assertSame(original, failure)
        assertEquals("old", content)
        assertEquals(0, applications)
    }

    @Test fun timeoutRestoresSelectionAndServiceModeBeforeRecovery() = runTest {
        val start = ServiceStartResult()
        var selection = 1L
        var mode = "VPN"
        var recovered = false
        var failure: Throwable? = null
        launch {
            failure = runCatching {
                ProfileChange.apply(
                    save = { selection = 2L; mode = "NORMAL" },
                    restore = listOf<suspend () -> Unit>({ selection = 1L }, { mode = "VPN" }),
                ) { recover ->
                    start.awaitStarted(100) {
                        if (recover) {
                            assertEquals(1L, selection)
                            assertEquals("VPN", mode)
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
        assertTrue(recovered)
        assertEquals(1L, selection)
        assertEquals("VPN", mode)
    }

    @Test fun cancellationStillRestoresDataAndRecoversTheService() = runTest {
        var content = "old"
        var recovered = false
        val job = launch {
            ProfileChange.apply(
                save = { content = "new" },
                restore = listOf<suspend () -> Unit>({
                    currentCoroutineContext().ensureActive()
                    content = "old"
                }),
            ) { recover ->
                if (recover) {
                    currentCoroutineContext().ensureActive()
                    assertEquals("old", content)
                    recovered = true
                } else awaitCancellation()
            }
        }
        runCurrent()
        job.cancel()
        runCurrent()
        job.join()
        assertTrue(job.isCancelled)
        assertEquals("old", content)
        assertTrue(recovered)
    }

    @Test fun recoveryFailureDoesNotHideTheApplicationFailure() = runTest {
        val original = IllegalStateException("Application failed")
        val recovery = IllegalStateException("Recovery failed")
        var content = "old"
        val failure = runCatching {
            ProfileChange.apply(
                save = { content = "new" },
                restore = listOf<suspend () -> Unit>({ content = "old" }),
            ) { recover -> throw if (recover) recovery else original }
        }.exceptionOrNull()!!
        assertSame(original, failure)
        assertEquals("old", content)
        assertEquals(listOf(recovery), failure.suppressed.toList())
    }

    @Test fun aFailedFileRestoreStillAttemptsOtherRollbacksWithoutStartingBadContent() = runTest {
        val original = IllegalStateException("Application failed")
        val rollback = IllegalStateException("File restore failed")
        var updatedAt = 10L
        var applications = 0
        val failure = runCatching {
            ProfileChange.apply(
                save = { updatedAt = 20L },
                restore = listOf<suspend () -> Unit>({ throw rollback }, { updatedAt = 10L }),
            ) { applications++; throw original }
        }.exceptionOrNull()!!
        assertSame(original, failure)
        assertEquals(10L, updatedAt)
        assertEquals(1, applications)
        assertEquals(listOf(rollback), failure.suppressed.toList())
    }
}
