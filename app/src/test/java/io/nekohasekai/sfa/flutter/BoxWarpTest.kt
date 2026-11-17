package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.job
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withTimeout
import org.junit.Assert.*
import org.junit.Test
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

@OptIn(ExperimentalCoroutinesApi::class)
class BoxWarpTest {
    private val secretNode = "registered-private-key-and-token"
    private fun pending(warp: BoxWarp): Map<*, *> = warp.snapshot()["pending"] as Map<*, *>

    @Test fun concurrentProfileChangesRejectTheLateSaveAndRetryUsesTheRegisteredAccount() = runTest {
        var selected = 7L
        var revision = "initial"
        var session = 1L
        var calls = 0
        var writes = 0
        val response = CompletableDeferred<String>()
        val warp = BoxWarp(
            prepare = { id, expected, epoch ->
                check(id == selected && expected == revision)
                WarpProfileSnapshot(id, expected, epoch, "profile-$id")
            },
            registerNode = { _, _, _, registered -> calls++; registered(response.await()) },
            commit = { target, node ->
                check(target.profileId == selected && target.revision == revision && target.session == session)
                assertEquals(secretNode, node)
                writes++
                mapOf("tag" to "WARP 2", "profileId" to target.profileId)
            },
            localSession = { session },
        )
        val operation = async { runCatching { warp.register("request", 7L, revision, "WARP", "http3") } }
        runCurrent()
        assertEquals(1, calls)
        assertEquals(true, warp.snapshot()["registering"])
        // These changes remain possible while registration waits on its response.
        selected = 8L
        revision = "edited"
        session = 3L // A local -> remote -> local transition also invalidates the old operation.
        response.complete(secretNode)
        val error = operation.await().exceptionOrNull() as BoxWarpException
        assertEquals("warp_pending", error.code)
        assertEquals(0, writes)
        val record = pending(warp)
        assertEquals(7L, record["profileId"])
        assertFalse(record.toString().contains(secretNode))
        assertFalse(error.toString().contains(secretNode))
        assertFalse(error.details.toString().contains(secretNode))
        assertNull(error.cause)
        val pendingId = record["pendingId"] as String
        assertEquals("warp_pending", (runCatching { warp.retry(pendingId, 8L, revision) }.exceptionOrNull() as BoxWarpException).code)
        selected = 7L
        val result = warp.retry(pendingId, 7L, revision)
        assertEquals("WARP 2", result["tag"])
        assertEquals(1, calls)
        assertEquals(1, writes)
        assertNull(warp.snapshot()["pending"])
        warp.close()
    }

    @Test fun duplicateRequestsAndCancellationCannotStartASecondRegistrationOrWrite() = runTest {
        var registrations = 0
        var closes = 0
        var writes = 0
        val warp = BoxWarp(
            prepare = { id, revision, session -> WarpProfileSnapshot(id, revision, session, "profile") },
            registerNode = { _, _, _, _ ->
                registrations++
                try { awaitCancellation() } finally { closes++ }
            },
            commit = { _, _ -> writes++; emptyMap() },
            localSession = { 1L },
        )
        val operation = async { runCatching { warp.register("first", 1, "revision", "WARP", "http2") } }
        runCurrent()
        assertEquals("warp_busy", (runCatching { warp.register("second", 1, "revision", "WARP", "http2") }.exceptionOrNull() as BoxWarpException).code)
        assertFalse(warp.cancel("second"))
        assertTrue(warp.cancel("first"))
        operation.join()
        assertEquals(1, registrations)
        assertEquals(1, closes)
        assertEquals(0, writes)
        assertEquals(false, warp.snapshot()["registering"])
        assertNull(warp.snapshot()["pending"])
        warp.close()
    }

    @Test fun saveFailuresKeepTheSamePendingAccountAndNeverForwardCredentialErrors() = runTest {
        var registrations = 0
        var attempts = 0
        val warp = BoxWarp(
            prepare = { id, revision, session -> WarpProfileSnapshot(id, revision, session, "profile") },
            registerNode = { _, _, _, registered -> registrations++; registered(secretNode) },
            commit = { _, _ -> attempts++; error("Untrusted response $secretNode") },
            localSession = { 1L },
        )
        val first = runCatching { warp.register("first", 1, "revision", "WARP", "http3") }.exceptionOrNull() as BoxWarpException
        val id = pending(warp)["pendingId"] as String
        val second = runCatching { warp.retry(id, 1, "new-revision") }.exceptionOrNull() as BoxWarpException
        assertEquals(first.details, second.details)
        assertEquals(2, attempts)
        assertEquals(1, registrations)
        assertFalse(second.toString().contains(secretNode))
        assertEquals("warp_pending", (runCatching { warp.register("next", 1, "revision", "WARP", "http2") }.exceptionOrNull() as BoxWarpException).code)
        assertEquals(1, registrations)
        warp.discard(id)
        assertNull(warp.snapshot()["pending"])
        warp.close()
    }

    @Test fun invalidTargetsFailBeforeRegisteringAndNativeFailuresAreSanitized() = runTest {
        var calls = 0
        val stale = BoxWarp(
            prepare = { _, _, _ -> error("The profile is stale or the service is transitioning") },
            registerNode = { _, _, _, registered -> calls++; registered(secretNode) },
            commit = { _, _ -> error("Must not save") },
            localSession = { 1L },
        )
        assertTrue(runCatching { stale.register("stale", 1, "revision", "WARP", "http2") }.isFailure)
        assertEquals(0, calls)
        stale.close()
        val failing = BoxWarp(
            prepare = { id, revision, session -> WarpProfileSnapshot(id, revision, session, "profile") },
            registerNode = { _, _, _, _ -> calls++; error(secretNode) },
            commit = { _, _ -> error("Must not save") },
            localSession = { 1L },
        )
        val error = runCatching { failing.register("failed", 1, "revision", "WARP", "http3") }.exceptionOrNull() as BoxWarpException
        assertEquals("warp_error", error.code)
        assertFalse(error.toString().contains(secretNode))
        assertNull(error.cause)
        assertNull(failing.snapshot()["pending"])
        failing.close()
    }

    @Test fun cancellationClosesTheTaskWhileItsNativeCallIsStillBlocked() = runBlocking {
        val entered = CompletableDeferred<Unit>()
        val response = CountDownLatch(1)
        val task = async(Dispatchers.Default) {
            withWarpRegistrationTask(close = { response.countDown() }) {
                entered.complete(Unit)
                check(response.await(5, TimeUnit.SECONDS)) { "Cancellation did not reach the native request" }
                secretNode
            }
        }
        withTimeout(5_000) { entered.await() }
        task.cancel()
        withTimeout(5_000) { task.join() }
        assertTrue(task.isCancelled)
        assertEquals(0L, response.count)
    }

    @Test fun cancellationAfterTheNativeResponseKeepsCredentialsAvailableForRetry() = runBlocking {
        var writes = 0
        val warp = BoxWarp(
            prepare = { id, revision, session -> WarpProfileSnapshot(id, revision, session, "profile") },
            registerNode = { _, _, _, registered ->
                val owner = currentCoroutineContext().job
                withWarpRegistrationTask(close = {}, onResult = { node: String -> registered(node); owner.cancel() }) { secretNode }
                Unit
            },
            commit = { target, node -> writes++; assertEquals(secretNode, node); mapOf("profileId" to target.profileId, "tag" to "WARP") },
            localSession = { 1L },
        )
        val operation = async { warp.register("cancelled-response", 1, "revision", "WARP", "http3") }
        operation.join()
        assertTrue(operation.isCancelled)
        assertEquals(0, writes)
        val id = pending(warp)["pendingId"] as String
        assertEquals("WARP", warp.retry(id, 1, "revision")["tag"])
        assertEquals(1, writes)
        warp.close()
    }
}
