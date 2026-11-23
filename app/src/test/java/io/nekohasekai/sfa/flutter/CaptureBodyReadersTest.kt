package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicLong
import java.util.concurrent.atomic.AtomicReference

class CaptureBodyReadersTest {
    @get:Rule val temporary = TemporaryFolder()

    private class Native(
        val gate: CountDownLatch = CountDownLatch(1),
        val exitGate: CountDownLatch = CountDownLatch(0),
        private val afterCancel: () -> Unit = {},
    ) : CaptureBodyNative {
        val entered = CompletableDeferred<Unit>()
        val cancelled = CompletableDeferred<Unit>()
        val closed = CompletableDeferred<Unit>()
        val calls = AtomicInteger()
        val active = AtomicInteger()
        val maxActive = AtomicInteger()
        val cancels = AtomicInteger()
        val closes = AtomicInteger()
        val view = AtomicReference<File?>()

        private fun perform(value: String): String {
            calls.incrementAndGet()
            val count = active.incrementAndGet()
            maxActive.updateAndGet { maxOf(it, count) }
            entered.complete(Unit)
            try {
                check(gate.await(5, TimeUnit.SECONDS)) { "Native read was not released" }
                check(exitGate.await(5, TimeUnit.SECONDS)) { "Native read did not exit" }
                return value
            } finally {
                active.decrementAndGet()
            }
        }

        override fun read(id: String, response: Boolean, offset: Long, raw: Boolean) = perform("page:$offset")

        override fun prepareView(path: String, id: String, response: Boolean, format: String): String {
            val file = File(path)
            file.writeText("derived body")
            view.set(file)
            return perform("prepared")
        }

        override fun readView(offset: Long) = perform("view:$offset")

        override fun cancel() {
            cancels.incrementAndGet()
            cancelled.complete(Unit)
            gate.countDown()
            afterCancel()
        }

        override fun close() {
            closes.incrementAndGet()
            gate.countDown()
            view.get()?.delete()
            closed.complete(Unit)
        }

        fun release() {
            gate.countDown()
            exitGate.countDown()
        }
    }

    private fun readers(
        generation: AtomicLong = AtomicLong(1),
        create: () -> Native,
    ) = CaptureBodyReaders(
        directory = temporary.newFolder(),
        localSession = generation::get,
        checkSession = { check(it == generation.get()) { "Local command session changed" } },
        createTask = create,
    )

    @Test fun parentCancellationReachesABlockedNativeReadAndClosesItOnce() = runBlocking {
        val native = Native()
        val readers = readers { native }
        try {
            val running = async(Dispatchers.Default) { readers.read("reader", "capture", true, 0, false) }
            withTimeout(5_000) { native.entered.await() }
            running.cancel()
            withTimeout(5_000) { native.cancelled.await(); running.join(); native.closed.await() }
            assertTrue(running.isCancelled)
            assertEquals(1, native.cancels.get())
            assertEquals(1, native.closes.get())
            assertFalse(readers.closeReader("reader"))
        } finally {
            native.release()
            readers.close()
        }
    }

    @Test fun differentVisibleBodiesReadConcurrentlyAndKeepTheirTasksForLaterPages() = runBlocking {
        val firstNative = Native()
        val secondNative = Native()
        val created = AtomicInteger()
        val readers = readers { if (created.getAndIncrement() == 0) firstNative else secondNative }
        try {
            val first = async(Dispatchers.Default) { readers.read("first", "capture", false, 0, false) }
            withTimeout(5_000) { firstNative.entered.await() }
            val second = async(Dispatchers.Default) { readers.read("second", "capture", true, 0, false) }
            withTimeout(5_000) { secondNative.entered.await() }
            assertEquals(2, created.get())
            assertEquals(1, firstNative.active.get())
            assertEquals(1, secondNative.active.get())
            firstNative.release()
            secondNative.release()
            assertEquals("page:0", withTimeout(5_000) { first.await() })
            assertEquals("page:0", withTimeout(5_000) { second.await() })
            assertEquals(0, firstNative.closes.get())
            assertEquals(0, secondNative.closes.get())
            assertTrue(readers.closeReader("first"))
            assertTrue(readers.closeReader("second"))
            assertEquals(1, firstNative.closes.get())
            assertEquals(1, secondNative.closes.get())
        } finally {
            firstNative.release()
            secondNative.release()
            readers.close()
        }
    }

    @Test fun pagesOfOneVisibleBodySerializeAndReuseOneNativeTask() = runBlocking {
        val native = Native()
        val created = AtomicInteger()
        val readers = readers { created.incrementAndGet(); native }
        try {
            val first = async(Dispatchers.Default) { readers.read("reader", "capture", true, 0, false) }
            withTimeout(5_000) { native.entered.await() }
            val second = async(Dispatchers.IO, start = CoroutineStart.UNDISPATCHED) { readers.read("reader", "capture", true, 16_384, false) }
            assertEquals(1, native.calls.get())
            native.release()
            assertEquals("page:0", withTimeout(5_000) { first.await() })
            assertEquals("page:16384", withTimeout(5_000) { second.await() })
            assertEquals(2, native.calls.get())
            assertEquals(1, native.maxActive.get())
            assertEquals(1, created.get())
            assertEquals(0, native.closes.get())
            assertTrue(readers.closeReader("reader"))
            assertEquals(1, native.closes.get())
        } finally {
            native.release()
            readers.close()
        }
    }

    @Test fun closingABodyCancelsAQueuedPageBeforeItEntersNativeCode() = runBlocking {
        val native = Native()
        val readers = readers { native }
        try {
            val first = async(Dispatchers.Default) { readers.read("reader", "capture", true, 0, false) }
            withTimeout(5_000) { native.entered.await() }
            val second = async(Dispatchers.IO, start = CoroutineStart.UNDISPATCHED) { readers.read("reader", "capture", true, 16_384, false) }
            assertTrue(readers.closeReader("reader"))
            withTimeout(5_000) { first.join(); second.join(); native.closed.await() }
            assertTrue(first.isCancelled)
            assertTrue(second.isCancelled)
            assertEquals(1, native.calls.get())
            assertEquals(1, native.cancels.get())
            assertEquals(1, native.closes.get())
        } finally {
            native.release()
            readers.close()
        }
    }

    @Test fun closingCancelsTheResultOwnerBeforeTheNativeSignalReleasesItsResult() = runBlocking {
        val completed = CountDownLatch(1)
        val outcome = AtomicReference<Result<String>?>()
        val native = Native(afterCancel = {
            check(completed.await(5, TimeUnit.SECONDS)) { "The result owner did not finish after cancellation" }
        })
        val readers = readers { native }
        try {
            val running = async(Dispatchers.Default) {
                runCatching { readers.read("reader", "capture", true, 0, false) }.also {
                    outcome.set(it)
                    completed.countDown()
                }
            }
            withTimeout(5_000) { native.entered.await() }
            val closing = async(Dispatchers.IO) { readers.closeReader("reader") }
            assertTrue(withTimeout(5_000) { closing.await() })
            withTimeout(5_000) { running.join() }
            val result = requireNotNull(outcome.get())
            assertTrue(result.exceptionOrNull() is CancellationException)
            assertEquals(1, native.closes.get())
            assertFalse(readers.closeReader("reader"))
        } finally {
            completed.countDown()
            native.release()
            readers.close()
        }
    }

    @Test fun closeSignalsImmediatelyAndDefersNativeCloseUntilTheActiveCallExits() = runBlocking {
        val native = Native(exitGate = CountDownLatch(1))
        val readers = readers { native }
        try {
            val running = async(Dispatchers.Default) { readers.read("reader", "capture", true, 0, false) }
            withTimeout(5_000) { native.entered.await() }
            val closing = async(Dispatchers.IO) { readers.closeReader("reader") }
            assertTrue(withTimeout(5_000) { closing.await() })
            assertEquals(1, native.cancels.get())
            assertEquals(1, native.active.get())
            assertEquals(0, native.closes.get())
            native.exitGate.countDown()
            withTimeout(5_000) { running.join(); native.closed.await() }
            assertTrue(running.isCancelled)
            assertEquals(1, native.closes.get())
        } finally {
            native.release()
            readers.close()
        }
    }

    @Test fun aSessionChangeRejectsALateViewAndDeletesItsPrivateFile() = runBlocking {
        val generation = AtomicLong(1)
        val native = Native()
        val readers = readers(generation) { native }
        try {
            val running = async(Dispatchers.Default) {
                runCatching { readers.prepareView("reader", "capture", true, "json") }
            }
            withTimeout(5_000) { native.entered.await() }
            val path = requireNotNull(native.view.get())
            assertTrue(path.isFile)
            generation.incrementAndGet()
            native.release()
            assertTrue(withTimeout(5_000) { running.await() }.isFailure)
            assertEquals(1, native.closes.get())
            assertFalse(path.exists())
            assertFalse(readers.closeReader("reader"))
        } finally {
            native.release()
            readers.close()
        }
    }

    @Test fun hostCloseCancelsCurrentCallsAndRejectsFutureAdmission() = runBlocking {
        val native = Native()
        val created = AtomicInteger()
        val readers = readers { created.incrementAndGet(); native }
        try {
            val running = async(Dispatchers.Default) { readers.read("reader", "capture", true, 0, false) }
            withTimeout(5_000) { native.entered.await() }
            readers.close()
            withTimeout(5_000) { running.join(); native.closed.await() }
            assertTrue(running.isCancelled)
            assertTrue(runCatching { readers.read("new", "capture", true, 0, false) }.exceptionOrNull() is IllegalStateException)
            assertEquals(1, created.get())
            assertEquals(1, native.closes.get())
            readers.close()
            assertEquals(1, native.closes.get())
        } finally {
            native.release()
            readers.close()
        }
    }
}
