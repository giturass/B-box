package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicLong

class CaptureInjectTasksTest {
    @get:Rule val temporary = TemporaryFolder()

    private class Native(
        private val gate: CountDownLatch = CountDownLatch(0),
        private val afterWrite: () -> Unit = {},
    ) : CaptureInjectNative {
        val entered = CompletableDeferred<Unit>()
        val closes = AtomicInteger()
        var arguments: List<Any>? = null

        override fun inject(streamId: String, direction: String, opcode: Int, dataBase64: String) {
            arguments = listOf(streamId, direction, opcode, dataBase64)
            entered.complete(Unit)
            check(gate.await(5, TimeUnit.SECONDS)) { "Native injection was not released" }
            afterWrite()
        }

        override fun close() {
            closes.incrementAndGet()
            gate.countDown()
        }
        fun release() = gate.countDown()
    }

    private class Replay(
        private val gate: CountDownLatch = CountDownLatch(0),
        private val afterSend: () -> Unit = {},
    ) : CaptureReplayNative {
        val entered = CompletableDeferred<Unit>()
        val closes = AtomicInteger()
        var arguments: List<String>? = null

        override fun replay(id: String, optionsJSON: String): String {
            arguments = listOf(id, optionsJSON)
            entered.complete(Unit)
            check(gate.await(5, TimeUnit.SECONDS)) { "Native replay was not released" }
            afterSend()
            return "{\"id\":\"next\",\"status\":200}"
        }

        override fun close() {
            closes.incrementAndGet()
            gate.countDown()
        }
    }

    private fun replays(native: Replay, generation: AtomicLong = AtomicLong(1)) = CaptureReplayTasks(
        localSession = generation::get,
        checkSession = { check(it == generation.get()) { "Local command session changed" } },
        createTask = { native },
    )

    private fun tasks(native: Native, generation: AtomicLong = AtomicLong(1)) = CaptureInjectTasks(
        localSession = generation::get,
        checkSession = { check(it == generation.get()) { "Local command session changed" } },
        createTask = { native },
    )

    @Test fun successfulInjectionKeepsDirectionOpcodeAndArbitraryBytePayload() = runBlocking {
        val native = Native()
        val tasks = tasks(native)
        assertTrue(tasks.inject("success", "websocket", "server", 2, "AAH/"))
        assertEquals(listOf("websocket", "server", 2, "AAH/"), native.arguments)
        assertEquals(1, native.closes.get())
        tasks.close()
        assertEquals(1, native.closes.get())
    }

    @Test fun parentCancellationReachesABlockedNativeWriteAndClosesItOnce() = runBlocking {
        val native = Native(CountDownLatch(1))
        val tasks = tasks(native)
        try {
            val running = async(Dispatchers.Default) { tasks.inject("parent", "tcp", "client", 0, "Ynl0ZXM=") }
            withTimeout(5_000) { native.entered.await() }
            running.cancel()
            withTimeout(5_000) { running.join() }
            assertTrue(running.isCancelled)
            assertEquals(1, native.closes.get())
        } finally { tasks.close() }
    }

    @Test fun sessionAndUiCleanupCancelAllPendingWritesWithoutWaitingOnJni() = runBlocking {
        val native = Native(CountDownLatch(1))
        val tasks = tasks(native)
        try {
            val running = async(Dispatchers.Default) { tasks.inject("cleanup", "tcp", "server", 0, "Ynl0ZXM=") }
            withTimeout(5_000) { native.entered.await() }
            tasks.cancelAll()
            withTimeout(5_000) { running.join() }
            assertTrue(running.isCancelled)
            assertEquals(1, native.closes.get())
            tasks.cancelAll()
            assertEquals(1, native.closes.get())
        } finally { tasks.close() }
    }

    @Test fun targetChangeAfterNativeWriteIsNotReportedAsSuccessfulInjection() = runBlocking {
        val generation = AtomicLong(1)
        val native = Native(afterWrite = { generation.incrementAndGet() })
        val tasks = tasks(native, generation)
        val failure = runCatching { tasks.inject("session", "stream", "client", 1, "dGV4dA==") }.exceptionOrNull()
        assertTrue(failure is IllegalStateException)
        assertEquals("Local command session changed", failure?.message)
        assertEquals(1, native.closes.get())
        tasks.close()
    }

    @Test fun invalidTargetsAndClosedManagerDoNotReachNativeWrites() = runBlocking {
        val native = Native()
        val tasks = tasks(native)
        for ((id, direction, opcode) in listOf(Triple("", "client", 0), Triple("stream", "upstream", 0), Triple("stream", "client", 9))) {
            assertTrue(runCatching { tasks.inject("invalid", id, direction, opcode, "") }.exceptionOrNull() is IllegalArgumentException)
        }
        assertNull(native.arguments)
        tasks.close()
        assertTrue(runCatching { tasks.inject("closed", "stream", "client", 0, "") }.exceptionOrNull() is IllegalStateException)
        assertNull(native.arguments)
        assertEquals(0, native.closes.get())
    }

    @Test fun directedCancellationOnlyStopsItsOwnRequestAndDuplicateIdsAreRejected() = runBlocking {
        val first = Native(CountDownLatch(1))
        val second = Native(CountDownLatch(1))
        val created = AtomicInteger()
        val tasks = CaptureInjectTasks(localSession = { 1L }, checkSession = {},
            createTask = { if (created.getAndIncrement() == 0) first else second })
        try {
            val firstRequest = async(Dispatchers.Default) { tasks.inject("first", "tcp", "client", 0, "YQ==") }
            withTimeout(5_000) { first.entered.await() }
            val secondRequest = async(Dispatchers.Default) { tasks.inject("second", "tcp", "client", 0, "Yg==") }
            withTimeout(5_000) { second.entered.await() }
            assertFalse(tasks.cancel("other"))
            assertTrue(runCatching { tasks.inject("first", "tcp", "client", 0, "") }.exceptionOrNull() is IllegalArgumentException)
            assertEquals(2, created.get())
            assertTrue(tasks.cancel("first"))
            withTimeout(5_000) { firstRequest.join() }
            assertTrue(firstRequest.isCancelled)
            assertFalse(secondRequest.isCancelled)
            second.release()
            assertTrue(withTimeout(5_000) { secondRequest.await() })
            assertFalse(tasks.cancel("first"))
            assertEquals(1, first.closes.get())
            assertEquals(1, second.closes.get())
        } finally { tasks.close() }
    }

    @Test fun clearingCaptureHistoryCancelsBodyReadsAndKeepsAnActiveInjectionRunning() = runBlocking {
        val readEntered = CompletableDeferred<Unit>()
        val readGate = CountDownLatch(1)
        val readCancels = AtomicInteger()
        val bodies = CaptureBodyReaders(temporary.newFolder(), localSession = { 1L }, checkSession = {}, createTask = {
            object : CaptureBodyNative {
                override fun read(id: String, response: Boolean, offset: Long, raw: Boolean): String {
                    readEntered.complete(Unit)
                    check(readGate.await(5, TimeUnit.SECONDS)) { "Native read was not released" }
                    return "body"
                }
                override fun prepareView(path: String, id: String, response: Boolean, format: String) = error("Unused view preparation")
                override fun readView(offset: Long) = error("Unused view page")
                override fun cancel() { readCancels.incrementAndGet(); readGate.countDown() }
                override fun close() = readGate.countDown()
            }
        })
        val native = Native(CountDownLatch(1))
        val injections = tasks(native)
        val replay = Replay(CountDownLatch(1))
        val replays = replays(replay)
        val tasks = CaptureTasks(bodies, injections, replays)
        try {
            val read = async(Dispatchers.Default) { bodies.read("reader", "record", false, 0, false) }
            val injection = async(Dispatchers.Default) { injections.inject("write", "stream", "server", 2, "AAH/") }
            val resend = async(Dispatchers.Default) { replays.replay("resend", "record", "{}") }
            withTimeout(5_000) { readEntered.await(); native.entered.await(); replay.entered.await() }
            var clears = 0
            tasks.clearRecords { clears++ }
            withTimeout(5_000) { read.join() }
            assertEquals(1, clears)
            assertEquals(1, readCancels.get())
            assertTrue(read.isCancelled)
            assertFalse(injection.isCompleted)
            assertFalse(injection.isCancelled)
            assertEquals(0, native.closes.get())
            assertFalse(resend.isCompleted)
            assertEquals(0, replay.closes.get())
            native.release()
            assertTrue(withTimeout(5_000) { injection.await() })
            assertEquals(1, native.closes.get())
            tasks.cancelAll()
            withTimeout(5_000) { resend.join() }
            assertTrue(resend.isCancelled)
            assertEquals(1, replay.closes.get())
        } finally { tasks.close() }
    }

    @Test fun replayReturnsTheNativeResultAndClosesItsTaskOnce() = runBlocking {
        val replay = Replay()
        val tasks = replays(replay)
        assertEquals("{\"id\":\"next\",\"status\":200}", tasks.replay("first", "record", "{\"method\":\"POST\"}"))
        assertEquals(listOf("record", "{\"method\":\"POST\"}"), replay.arguments)
        assertEquals(1, replay.closes.get())
        assertFalse(tasks.cancel("first"))
        assertTrue(runCatching { tasks.replay("blank", " ", "{}") }.exceptionOrNull() is IllegalArgumentException)
        tasks.close()
        assertTrue(runCatching { tasks.replay("closed", "record", "{}") }.exceptionOrNull() is IllegalStateException)
        assertEquals(1, replay.closes.get())
    }

    @Test fun cancellingAReplayClosesTheBlockedNativeSendAndReportsCancellation() = runBlocking {
        val replay = Replay(CountDownLatch(1))
        val tasks = replays(replay)
        try {
            val running = async(Dispatchers.Default) { tasks.replay("slow", "record", "{}") }
            withTimeout(5_000) { replay.entered.await() }
            assertTrue(runCatching { tasks.replay("slow", "record", "{}") }.exceptionOrNull() is IllegalArgumentException)
            assertTrue(tasks.cancel("slow"))
            withTimeout(5_000) { running.join() }
            assertTrue(running.isCancelled)
            assertEquals(1, replay.closes.get())
        } finally { tasks.close() }
    }

    @Test fun replayFinishedAfterASessionChangeIsNotReportedToTheNewSession() = runBlocking {
        val generation = AtomicLong(1)
        val replay = Replay(afterSend = { generation.incrementAndGet() })
        val tasks = replays(replay, generation)
        val failure = runCatching { tasks.replay("session", "record", "{}") }.exceptionOrNull()
        assertTrue(failure is IllegalStateException)
        assertEquals("Local command session changed", failure?.message)
        assertEquals(1, replay.closes.get())
        tasks.close()
    }
}
