package io.nekohasekai.sfa.bg

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.async
import kotlinx.coroutines.cancel
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withContext
import org.junit.Assert.*
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class DefaultInterfaceMonitorTest {
    @Test fun coreStartupWaitsForTheInitialPublicationIncludingAnEmptyInterface() = runTest {
        val wifi = DefaultInterface("wlan0", 7)
        for (network in listOf(null, "wifi")) {
            val publicationStarted = CompletableDeferred<Unit>()
            val releasePublication = CompletableDeferred<Unit>()
            val updates = mutableListOf<DefaultInterface?>()
            var coreContinued = false
            val monitor = DefaultInterfaceMonitor<String>(backgroundScope, resolve = { wifi })
            try {
                monitor.updateNetwork(network)
                val initialPublication = checkNotNull(monitor.setListener {
                    publicationStarted.complete(Unit)
                    releasePublication.await()
                    updates.add(it)
                })
                val startup = async {
                    initialPublication.await()
                    coreContinued = true
                }
                runCurrent()
                assertTrue(publicationStarted.isCompleted)
                assertFalse("Scheduling the callback must not confirm initialization", initialPublication.isCompleted)
                assertFalse("Core startup must wait until its interface update returns", coreContinued)

                releasePublication.complete(Unit)
                runCurrent()
                startup.await()
                assertTrue(coreContinued)
                assertEquals(listOf(if (network == null) null else wifi), updates)
            } finally {
                releasePublication.complete(Unit)
                monitor.close()
            }
        }
    }

    @Test fun lateBootNetworkKeepsRetryingUntilItsInterfaceIsReady() = runTest {
        val wifi = DefaultInterface("wlan0", 7)
        val updates = mutableListOf<DefaultInterface?>()
        var ready = false
        var reads = 0
        val monitor = DefaultInterfaceMonitor<String>(backgroundScope, resolve = {
            reads++
            if (ready) wifi else null
        })
        try {
            monitor.setListener { updates.add(it) }
            runCurrent()
            assertEquals(listOf<DefaultInterface?>(null), updates)
            advanceTimeBy(3_000)
            runCurrent()
            assertEquals(0, reads)

            monitor.updateNetwork("wifi")
            runCurrent()
            assertEquals(1, reads)
            val waitingNotifications = updates.size
            assertNull(updates.last())
            advanceTimeBy(1_500)
            runCurrent()
            assertTrue("Interface readiness must still be checked after one second", reads > 10)
            assertEquals("An unavailable interface should be reported once per refresh", waitingNotifications, updates.size)

            // Android need not send a second callback when the interface index
            // becomes available. The pending check must recover by itself.
            ready = true
            advanceTimeBy(100)
            runCurrent()
            assertEquals(wifi, updates.last())
            val completedReads = reads
            advanceTimeBy(30_000)
            runCurrent()
            assertEquals("A resolved interface should not keep polling", completedReads, reads)
        } finally {
            monitor.close()
        }
    }

    @Test fun aListenerAttachedAfterNetworkArrivalReceivesTheReadyInterface() = runTest {
        val wifi = DefaultInterface("wlan0", 7)
        val updates = mutableListOf<DefaultInterface?>()
        var reads = 0
        val monitor = DefaultInterfaceMonitor<String>(backgroundScope, resolve = {
            reads++
            wifi
        })
        try {
            monitor.updateNetwork("wifi")
            runCurrent()
            advanceTimeBy(1_500)
            runCurrent()
            assertEquals("Network arrival without a core listener must not resolve", 0, reads)
            assertTrue(updates.isEmpty())

            // Core initialization can finish after the last Android callback.
            // Attaching it must use the network already recorded by the monitor.
            monitor.setListener { updates.add(it) }
            runCurrent()
            assertEquals(1, reads)
            assertEquals(listOf(wifi), updates)
        } finally {
            monitor.close()
        }
    }

    @Test fun switchingNetworksDiscardsTheLatePreviousInterface() = runTest {
        val old = DefaultInterface("wlan0", 7)
        val current = DefaultInterface("rmnet_data0", 12)
        val resolverStarted = CompletableDeferred<Unit>()
        val releaseOldResolver = CompletableDeferred<Unit>()
        val updates = mutableListOf<DefaultInterface?>()
        val monitor = DefaultInterfaceMonitor<String>(backgroundScope, resolve = { network ->
            if (network == "wifi") {
                resolverStarted.complete(Unit)
                // Model a platform/JNI call which cannot stop immediately.
                withContext(NonCancellable) { releaseOldResolver.await() }
                old
            } else current
        })
        try {
            val initialPublication = checkNotNull(monitor.setListener { updates.add(it) })
            monitor.updateNetwork("wifi")
            runCurrent()
            assertTrue(resolverStarted.isCompleted)
            monitor.updateNetwork("cellular")
            runCurrent()
            assertFalse("A network refresh must preserve its listener's pending initialization", initialPublication.isCompleted)
            releaseOldResolver.complete(Unit)
            runCurrent()
            initialPublication.await()
            assertFalse("A superseded network must not be published", updates.contains(old))
            assertEquals(current, updates.last())
            assertEquals(1, updates.count { it == current })
        } finally {
            releaseOldResolver.complete(Unit)
            monitor.close()
        }
    }

    @Test fun replacingTheListenerDiscardsWorkForThePreviousCore() = runTest {
        val stale = DefaultInterface("old-interface", 7)
        val current = DefaultInterface("wlan0", 8)
        val resolverStarted = CompletableDeferred<Unit>()
        val releaseOldResolver = CompletableDeferred<Unit>()
        val previousUpdates = mutableListOf<DefaultInterface?>()
        val currentUpdates = mutableListOf<DefaultInterface?>()
        var reads = 0
        val monitor = DefaultInterfaceMonitor<String>(backgroundScope, resolve = {
            if (++reads == 1) {
                resolverStarted.complete(Unit)
                withContext(NonCancellable) { releaseOldResolver.await() }
                stale
            } else current
        })
        try {
            val previousPublication = checkNotNull(monitor.setListener { previousUpdates.add(it) })
            val previousStartup = async { previousPublication.await() }
            monitor.updateNetwork("wifi")
            runCurrent()
            assertTrue(resolverStarted.isCompleted)
            val currentPublication = checkNotNull(monitor.setListener { currentUpdates.add(it) })
            runCurrent()
            assertTrue("Replacing the listener must cancel its pending startup", previousStartup.isCancelled)
            assertTrue("The previous startup must end before the old platform call returns", previousStartup.isCompleted)
            assertFalse(currentPublication.isCompleted)
            releaseOldResolver.complete(Unit)
            runCurrent()
            currentPublication.await()
            assertTrue("The previous core must not receive a late interface", previousUpdates.filterNotNull().isEmpty())
            assertFalse("The replacement core must not receive a stale result", currentUpdates.contains(stale))
            assertEquals(listOf(current), currentUpdates.filterNotNull())
        } finally {
            releaseOldResolver.complete(Unit)
            monitor.close()
        }
    }

    @Test fun removingTheListenerCancelsItsPendingInitialization() = runTest {
        val publicationStarted = CompletableDeferred<Unit>()
        val releasePublication = CompletableDeferred<Unit>()
        val monitor = DefaultInterfaceMonitor<String>(backgroundScope, resolve = { DefaultInterface("wlan0", 7) })
        try {
            val initialPublication = checkNotNull(monitor.setListener {
                publicationStarted.complete(Unit)
                withContext(NonCancellable) { releasePublication.await() }
            })
            val startup = async { initialPublication.await() }
            runCurrent()
            assertTrue(publicationStarted.isCompleted)
            monitor.setListener(null)
            runCurrent()
            assertTrue(startup.isCancelled)
            assertTrue("Removing the listener must release the waiter before its native call returns", startup.isCompleted)
            releasePublication.complete(Unit)
            runCurrent()
            assertTrue("A late return must not confirm the removed listener", initialPublication.isCancelled)
        } finally {
            releasePublication.complete(Unit)
            monitor.close()
        }
    }

    @Test fun losingTheNetworkClearsTheInterfaceAndRecoveryNeedsNoRestart() = runTest {
        val wifi = DefaultInterface("wlan0", 7)
        val cellular = DefaultInterface("rmnet_data0", 12)
        val updates = mutableListOf<DefaultInterface?>()
        var reads = 0
        val monitor = DefaultInterfaceMonitor<String>(backgroundScope, resolve = { network ->
            reads++
            if (network == "wifi") wifi else cellular
        })
        try {
            monitor.setListener { updates.add(it) }
            monitor.updateNetwork("wifi")
            runCurrent()
            assertEquals(listOf(wifi), updates)
            monitor.updateNetwork(null)
            runCurrent()
            assertEquals(listOf(wifi, null), updates)
            advanceTimeBy(5_000)
            runCurrent()
            assertEquals("There is no interface to resolve while offline", 1, reads)
            monitor.updateNetwork("cellular")
            runCurrent()
            assertEquals(listOf(wifi, null, cellular), updates)
        } finally {
            monitor.close()
        }
    }

    @Test fun anotherCallbackForTheSameNetworkRefreshesItsInterface() = runTest {
        val first = DefaultInterface("wlan0", 7)
        val changed = DefaultInterface("wlan0", 8)
        var resolved = first
        var reads = 0
        val updates = mutableListOf<DefaultInterface?>()
        val monitor = DefaultInterfaceMonitor<String>(backgroundScope, resolve = {
            reads++
            resolved
        })
        try {
            monitor.setListener { updates.add(it) }
            monitor.updateNetwork("wifi")
            runCurrent()
            resolved = changed
            monitor.updateNetwork("wifi")
            runCurrent()
            assertEquals(listOf(first, changed), updates)
            assertEquals(2, reads)
        } finally {
            monitor.close()
        }
    }

    @Test fun closingTheMonitorStopsPendingInterfaceRetries() = runTest {
        var reads = 0
        val updates = mutableListOf<DefaultInterface?>()
        val failures = mutableListOf<Exception>()
        val monitor = DefaultInterfaceMonitor<String>(backgroundScope, resolve = {
            reads++
            null
        }, onFailure = { failures.add(it) })
        try {
            monitor.setListener { updates.add(it) }
            monitor.updateNetwork("wifi")
            runCurrent()
            advanceTimeBy(1_500)
            runCurrent()
            assertTrue(reads > 10)
            monitor.close()
            val closedReads = reads
            val closedUpdates = updates.toList()
            advanceTimeBy(30_000)
            runCurrent()
            assertEquals(closedReads, reads)
            assertEquals(closedUpdates, updates)
            assertTrue("Cancelling a retry must not be reported as failure", failures.isEmpty())
        } finally {
            monitor.close()
        }
    }

    @Test fun closeWaitsForUninterruptibleResolutionAndDiscardsItsResult() = runTest {
        val resolverStarted = CompletableDeferred<Unit>()
        val releaseResolver = CompletableDeferred<Unit>()
        val updates = mutableListOf<DefaultInterface?>()
        val failures = mutableListOf<Exception>()
        val monitor = DefaultInterfaceMonitor<String>(backgroundScope, resolve = {
            resolverStarted.complete(Unit)
            withContext(NonCancellable) { releaseResolver.await() }
            DefaultInterface("wlan0", 7)
        }, onFailure = { failures.add(it) })
        try {
            val initialPublication = checkNotNull(monitor.setListener { updates.add(it) })
            val startup = async { initialPublication.await() }
            monitor.updateNetwork("wifi")
            runCurrent()
            assertTrue(resolverStarted.isCompleted)
            val closing = async { monitor.close() }
            runCurrent()
            assertFalse("close must wait for an in-flight platform call", closing.isCompleted)
            assertTrue(startup.isCancelled)
            assertTrue("Monitor shutdown must release pending initialization before joining its worker", startup.isCompleted)
            releaseResolver.complete(Unit)
            runCurrent()
            closing.await()
            assertTrue("A closed monitor must not publish the late result", updates.filterNotNull().isEmpty())
            assertTrue(failures.isEmpty())
        } finally {
            releaseResolver.complete(Unit)
            monitor.close()
        }
    }

    @Test fun workerCancellationCancelsPendingAndLaterInitializations() = runTest {
        val workerScope = CoroutineScope(backgroundScope.coroutineContext + Job())
        val publicationStarted = CompletableDeferred<Unit>()
        val releasePublication = CompletableDeferred<Unit>()
        val monitor = DefaultInterfaceMonitor<String>(workerScope, resolve = { DefaultInterface("wlan0", 7) })
        try {
            val initialPublication = checkNotNull(monitor.setListener {
                publicationStarted.complete(Unit)
                releasePublication.await()
            })
            val startup = async { initialPublication.await() }
            runCurrent()
            assertTrue(publicationStarted.isCompleted)
            workerScope.cancel()
            runCurrent()
            assertTrue(startup.isCancelled)
            assertTrue(startup.isCompleted)
            val laterPublication = checkNotNull(monitor.setListener { })
            assertTrue("A stopped worker must not leave a later caller waiting", laterPublication.isCancelled)
        } finally {
            workerScope.cancel()
            monitor.close()
        }
    }

    @Test fun resolverOrListenerCancellationEndsInitializationWithoutStoppingLaterUpdates() = runTest {
        val wifi = DefaultInterface("wlan0", 7)
        for (cancelResolution in listOf(true, false)) {
            val expected = CancellationException("Initial interface update cancelled")
            val failures = mutableListOf<Exception>()
            val updates = mutableListOf<DefaultInterface?>()
            var cancelInitial = true
            val monitor = DefaultInterfaceMonitor<String>(backgroundScope, resolve = {
                if (cancelResolution && cancelInitial) throw expected
                wifi
            }, onFailure = { failures.add(it) })
            try {
                val initialPublication = checkNotNull(monitor.setListener {
                    if (!cancelResolution && cancelInitial) throw expected
                    updates.add(it)
                })
                val startup = async { initialPublication.await() }
                monitor.updateNetwork("wifi")
                runCurrent()
                assertTrue("An independently cancelled update must release its startup caller", startup.isCancelled)
                assertTrue(startup.isCompleted)
                assertTrue(failures.isEmpty())

                cancelInitial = false
                val recoveredPublication = checkNotNull(monitor.setListener { updates.add(it) })
                runCurrent()
                recoveredPublication.await()
                assertEquals(listOf(wifi), updates)
            } finally {
                monitor.close()
            }
        }
    }

    @Test fun cancellingTheCloserStillWaitsForResolutionToFinish() = runTest {
        val resolverStarted = CompletableDeferred<Unit>()
        val releaseResolver = CompletableDeferred<Unit>()
        val updates = mutableListOf<DefaultInterface?>()
        val failures = mutableListOf<Exception>()
        val monitor = DefaultInterfaceMonitor<String>(backgroundScope, resolve = {
            resolverStarted.complete(Unit)
            withContext(NonCancellable) { releaseResolver.await() }
            DefaultInterface("wlan0", 7)
        }, onFailure = { failures.add(it) })
        try {
            monitor.setListener { updates.add(it) }
            monitor.updateNetwork("wifi")
            runCurrent()
            assertTrue(resolverStarted.isCompleted)
            val closing = async { monitor.close() }
            runCurrent()
            assertFalse(closing.isCompleted)
            closing.cancel()
            runCurrent()
            assertTrue(closing.isCancelled)
            assertFalse("Cancelling the caller must not bypass monitor cleanup", closing.isCompleted)

            releaseResolver.complete(Unit)
            runCurrent()
            closing.join()
            assertTrue(closing.isCompleted)
            assertTrue("The cancelled closer must still discard the late interface", updates.filterNotNull().isEmpty())
            assertTrue("Neither cleanup cancellation nor the late result is a failure", failures.isEmpty())
        } finally {
            releaseResolver.complete(Unit)
            monitor.close()
        }
    }

    @Test fun resolutionFailuresAreReportedAndRetried() = runTest {
        val expected = IllegalStateException("Interface lookup temporarily failed")
        val wifi = DefaultInterface("wlan0", 7)
        val failures = mutableListOf<Exception>()
        val updates = mutableListOf<DefaultInterface?>()
        var reads = 0
        val monitor = DefaultInterfaceMonitor<String>(backgroundScope, resolve = {
            when (++reads) {
                1 -> throw expected
                2 -> null
                else -> wifi
            }
        }, onFailure = { failures.add(it) })
        try {
            monitor.setListener { updates.add(it) }
            monitor.updateNetwork("wifi")
            runCurrent()
            assertEquals(listOf(expected), failures)
            advanceTimeBy(100)
            runCurrent()
            assertEquals(2, reads)
            assertNull(updates.last())
            advanceTimeBy(100)
            runCurrent()
            assertEquals(wifi, updates.last())
            assertEquals(listOf(expected), failures)
            advanceTimeBy(1_000)
            runCurrent()
            assertEquals(3, reads)
        } finally {
            monitor.close()
        }
    }

    @Test fun publicationFailuresAreReportedAndRetried() = runTest {
        val expected = IllegalStateException("Core listener temporarily failed")
        val wifi = DefaultInterface("wlan0", 7)
        val failures = mutableListOf<Exception>()
        val updates = mutableListOf<DefaultInterface?>()
        var publications = 0
        val monitor = DefaultInterfaceMonitor<String>(backgroundScope, resolve = { wifi },
            onFailure = { failures.add(it) })
        try {
            val initialPublication = checkNotNull(monitor.setListener {
                if (++publications == 1) throw expected
                updates.add(it)
            })
            val startup = async { runCatching { initialPublication.await() } }
            monitor.updateNetwork("wifi")
            runCurrent()
            assertEquals(listOf(expected), failures)
            val actualFailure = checkNotNull(startup.await().exceptionOrNull())
            assertEquals(expected.javaClass, actualFailure.javaClass)
            assertEquals(expected.message, actualFailure.message)
            assertTrue("Initial publication failure must preserve the original cause",
                generateSequence(actualFailure) { it.cause }.any { it === expected })
            advanceTimeBy(100)
            runCurrent()
            assertEquals(listOf(wifi), updates)
            assertEquals(2, publications)
            assertEquals(listOf(expected), failures)
        } finally {
            monitor.close()
        }
    }

    @Test fun networkSwitchCancellationIsNotReportedAsFailure() = runTest {
        val resolverStarted = CompletableDeferred<Unit>()
        val pendingResolver = CompletableDeferred<Unit>()
        val cellular = DefaultInterface("rmnet_data0", 12)
        val failures = mutableListOf<Exception>()
        val updates = mutableListOf<DefaultInterface?>()
        val monitor = DefaultInterfaceMonitor<String>(backgroundScope, resolve = { network ->
            if (network == "wifi") {
                resolverStarted.complete(Unit)
                pendingResolver.await()
                DefaultInterface("wlan0", 7)
            } else cellular
        }, onFailure = { failures.add(it) })
        try {
            monitor.setListener { updates.add(it) }
            monitor.updateNetwork("wifi")
            runCurrent()
            assertTrue(resolverStarted.isCompleted)
            monitor.updateNetwork("cellular")
            runCurrent()
            assertEquals(listOf(cellular), updates.filterNotNull())
            assertTrue("Coroutine cancellation is part of a network switch", failures.isEmpty())
        } finally {
            pendingResolver.complete(Unit)
            monitor.close()
        }
    }
}
