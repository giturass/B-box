package io.nekohasekai.sfa.flutter

import androidx.lifecycle.Lifecycle
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import io.nekohasekai.sfa.R
import io.nekohasekai.sfa.constant.Alert
import io.nekohasekai.sfa.database.Settings
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.async
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class BoxHostExitTest {
    @Test fun alertsDuringExitReportFailureWithoutOpeningAnotherPermissionRequest() = runBlocking<Unit> {
        withTimeout(30_000) {
            val context = InstrumentationRegistry.getInstrumentation().targetContext
            withContext(Dispatchers.IO) {
                Settings.dataStore.initialize()
                Settings.dataStore.flush()
            }
            val draining = CompletableDeferred<Unit>()
            val releaseDrain = CompletableDeferred<Unit>()
            val closed = CompletableDeferred<Unit>()
            val uiRequests = mutableListOf<String>()
            val errors = mutableListOf<String>()
            val host = withContext(Dispatchers.Main.immediate) {
                // Keep initialization at the attach gate: this test needs no
                // profile, libbox startup, service connection or external UI.
                BoxHost(context, waitForUiAttach = true, requestUi = { method, _ ->
                    uiRequests.add(method)
                    false
                }).apply {
                    reportError = { errors.add(it) }
                    onClosed = { closed.complete(Unit) }
                    prepareBridgeExit = {
                        draining.complete(Unit)
                        releaseDrain.await()
                    }
                }
            }
            try {
                withContext(Dispatchers.Main.immediate) { host.close() }
                draining.await()
                withContext(Dispatchers.Main.immediate) {
                    assertFalse(closed.isCompleted)
                    assertEquals(Lifecycle.State.CREATED, host.lifecycle.currentState)
                    host.onServiceAlert(Alert.RequestLocationPermission, null)
                    assertTrue(uiRequests.isEmpty())
                    assertEquals(listOf(context.getString(R.string.location_permission_description)), errors)

                    // Fatal startup errors still reach the departing UI while
                    // recovery is draining; only new permission prompts stop.
                    host.onServiceAlert(Alert.CreateService, "Late service failure")
                    assertEquals("Late service failure", errors.last())
                    assertEquals(2, errors.size)
                    assertTrue(uiRequests.isEmpty())
                    assertFalse(closed.isCompleted)
                }
                releaseDrain.complete(Unit)
                host.awaitClosed()
                withContext(Dispatchers.Main.immediate) {
                    assertTrue(closed.isCompleted)
                    assertEquals(Lifecycle.State.DESTROYED, host.lifecycle.currentState)
                    assertTrue(uiRequests.isEmpty())
                }
            } finally {
                releaseDrain.complete(Unit)
                withContext(NonCancellable) {
                    withContext(Dispatchers.Main.immediate) { host.close() }
                    withTimeout(15_000) { host.awaitClosed() }
                }
            }
        }
    }

    @Test fun returningDuringBackgroundDrainRestoresPermissionsBeforeTheOldDrainFinishes() = runBlocking<Unit> {
        withTimeout(30_000) {
            val context = InstrumentationRegistry.getInstrumentation().targetContext
            withContext(Dispatchers.IO) {
                Settings.dataStore.initialize()
                Settings.dataStore.flush()
            }
            val draining = CompletableDeferred<Unit>()
            val releaseDrain = CompletableDeferred<Unit>()
            val uiRequests = mutableListOf<String>()
            val errors = mutableListOf<String>()
            var resumes = 0
            val host = withContext(Dispatchers.Main.immediate) {
                BoxHost(context, waitForUiAttach = true, requestUi = { method, _ ->
                    uiRequests.add(method)
                    false
                }).apply {
                    reportError = { errors.add(it) }
                    resumeBridge = { resumes++ }
                    prepareBridgeExit = {
                        draining.complete(Unit)
                        releaseDrain.await()
                    }
                }
            }
            val prepared = async(Dispatchers.Main.immediate) { host.prepareForExit() }
            try {
                draining.await()
                withContext(Dispatchers.Main.immediate) {
                    assertFalse(prepared.isCompleted)
                    host.onServiceAlert(Alert.RequestLocationPermission, null)
                    assertTrue(uiRequests.isEmpty())
                    assertEquals(listOf(context.getString(R.string.location_permission_description)), errors)

                    host.updateForeground(true)
                    assertEquals(1, resumes)
                    assertEquals(Lifecycle.State.RESUMED, host.lifecycle.currentState)
                    host.onServiceAlert(Alert.RequestLocationPermission, null)
                    assertEquals(listOf("permission.location"), uiRequests)
                    assertEquals(2, errors.size)
                    assertFalse(prepared.isCompleted)
                }
                releaseDrain.complete(Unit)
                prepared.await()
                withContext(Dispatchers.Main.immediate) {
                    // A stale drain cannot re-enter shutdown after the user
                    // has resumed, or block the next native permission alert.
                    assertEquals(Lifecycle.State.RESUMED, host.lifecycle.currentState)
                    host.onServiceAlert(Alert.RequestLocationPermission, null)
                    assertEquals(listOf("permission.location", "permission.location"), uiRequests)
                    assertEquals(3, errors.size)
                }
            } finally {
                releaseDrain.complete(Unit)
                withContext(NonCancellable) {
                    withTimeout(15_000) {
                        prepared.join()
                        withContext(Dispatchers.Main.immediate) { host.close() }
                        host.awaitClosed()
                    }
                }
            }
        }
    }
}
