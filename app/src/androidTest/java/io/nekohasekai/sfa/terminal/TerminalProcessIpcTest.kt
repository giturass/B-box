package io.nekohasekai.sfa.terminal

import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.content.ServiceConnection
import android.os.Build
import android.os.IBinder
import android.os.Process
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import io.nekohasekai.sfa.flutter.ipc.IpcPayload
import io.nekohasekai.sfa.terminal.fixture.ITerminalBackendFixture
import io.nekohasekai.sfa.terminal.fixture.ITerminalRendererFixture
import io.nekohasekai.sfa.terminal.fixture.TerminalBackendFixture
import io.nekohasekai.sfa.terminal.fixture.TerminalRendererFixture
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import java.io.ByteArrayOutputStream
import java.io.Closeable
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference
import kotlin.concurrent.thread

/** Uses the actual :ui process, production terminal Binder/PFD code and native Ghostty. */
@RunWith(AndroidJUnit4::class)
class TerminalProcessIpcTest {
    private val context get() = InstrumentationRegistry.getInstrumentation().targetContext

    @Test fun aSnapshotKeepsAnsiUtf8AlternateScreenAndEscapeContinuationsAndLargeInput() = withBackend { backend ->
        feed(backend, "开始\r\n\u001b[31m红色\u001b[0m\r\n\u001b[?1049hALT\r\n\u001b[32".toByteArray())
        val renderer = bindRenderer()
        try {
            assertNotEquals(Process.myPid(), renderer.service.pid)
            assertNull(renderer.service.asBinder().queryLocalInterface(
                "io.nekohasekai.sfa.terminal.fixture.ITerminalRendererFixture"))
            renderer.service.attach()
            await { renderer.service.screen() == backend.screen() }
            feed(backend, "m绿色\u001b[0m\r\n\u001b[?1049l恢复😀".toByteArray())
            await { renderer.service.screen() == backend.screen() }
            assertEquals(backend.transcript(), renderer.service.transcript())
            assertTrue(renderer.service.transcript().contains("恢复😀"))

            IpcPayload.read(backend.takeInput())
            val input = ByteArray(2 * 1024 * 1024 + 17) { ('a'.code + it % 26).toByte() }
            IpcPayload.write(context, input).use { renderer.service.sendInput(it!!) }
            await { backend.inputSize() == input.size }
            assertArrayEquals(input, IpcPayload.read(backend.takeInput()))
            assertFalse(backend.transportClosed())
        } finally { closeRenderer(renderer) }
    }

    @Test fun killingAnUnresponsiveUiCommitsPendingRepliesOnceAndReattachesTheSameBackend() = withBackend { backend ->
        val query = "\u001b[c".toByteArray()
        feed(backend, query)
        val expected = IpcPayload.read(backend.takeInput())!!
        assertTrue("The native terminal must answer a device attributes query", expected.isNotEmpty())
        feed(backend, "后台保留\r\n".toByteArray())
        val renderer = bindRenderer()
        var replacement: Binding<ITerminalRendererFixture>? = null
        try {
            val uiPid = renderer.service.pid
            assertNotEquals(Process.myPid(), uiPid)
            renderer.service.attach()
            renderer.service.holdMain()
            await { renderer.service.mainHeld() }
            feed(backend, query)
            renderer.service.killProcess()
            await { !backend.hasSurface() && backend.inputSize() == expected.size }
            assertArrayEquals(expected, IpcPayload.read(backend.takeInput()))
            assertFalse(backend.transportClosed())
            assertEquals(Process.myPid(), backend.pid)
            renderer.close()

            feed(backend, "UI死亡后的输出😀\r\n".toByteArray())
            feed(backend, query)
            assertArrayEquals(expected, IpcPayload.read(backend.takeInput()))
            val rebound = bindRenderer()
            replacement = rebound
            assertNotEquals(uiPid, rebound.service.pid)
            rebound.service.attach()
            await { rebound.service.transcript() == backend.transcript() }
            assertTrue(rebound.service.transcript().contains("UI死亡后的输出😀"))
            assertFalse(backend.transportClosed())
        } finally {
            try { replacement?.let(::closeRenderer) } finally { closeRenderer(renderer) }
        }
    }

    @Test fun aReplyDeadlineKeepsAnUnresponsiveUiAttachedAndDoesNotRepeatALateAcknowledgement() = withBackend { backend ->
        val query = "\u001b[c".toByteArray()
        feed(backend, query)
        val expected = IpcPayload.read(backend.takeInput())!!
        assertTrue(expected.isNotEmpty())
        val renderer = bindRenderer()
        try {
            assertNotEquals(Process.myPid(), renderer.service.pid)
            renderer.service.attach()
            renderer.service.holdMain()
            await { renderer.service.mainHeld() }
            feed(backend, query + "deadline-output".toByteArray())
            await { backend.inputSize() == expected.size }
            assertTrue("A reply deadline should keep the terminal surface", backend.hasSurface())
            assertTrue(renderer.service.mainHeld())
            renderer.service.releaseMain()
            await { renderer.service.screen() == backend.screen() }
            val marker = "after-late-ack".toByteArray()
            IpcPayload.write(context, marker).use { renderer.service.sendInput(it!!) }
            // This input follows the queued ACK on the same pipe, so observing
            // it proves that the late ACK has already been processed.
            await { backend.inputSize() == expected.size + marker.size }
            assertArrayEquals(expected + marker, IpcPayload.read(backend.takeInput()))
            assertFalse(backend.transportClosed())
        } finally { closeRenderer(renderer) }
    }

    @Test fun disconnectingOrLosingABindingBeforeTheFirstConnectedCallbackResumesTheOriginalWaiter() = withBackend { backend ->
        feed(backend, "connection recovered😀\r\n".toByteArray())
        val renderer = bindRenderer()
        try {
            assertNotEquals(Process.myPid(), renderer.service.pid)
            // Android 7 exposes onServiceDisconnected; onBindingDied was added in API 26.
            val bindingEvents = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) listOf(false, true) else listOf(false)
            for (bindingDied in bindingEvents) {
                renderer.service.attachWithInterruptedConnection(bindingDied)
                await { renderer.service.transcript() == backend.transcript() }
                assertTrue(renderer.service.transcript().contains("connection recovered😀"))
                assertTrue(backend.hasSurface())
                assertFalse(backend.transportClosed())
            }
        } finally { closeRenderer(renderer) }
    }

    @Test fun aFullUnacknowledgedQueueClosesItsPipesWithoutBlockingTheBackendOrLosingReplies() = withBackend { backend ->
        val query = "\u001b[c".toByteArray()
        feed(backend, query)
        val expected = IpcPayload.read(backend.takeInput())!!
        assertTrue(expected.isNotEmpty())
        val renderer = bindRenderer()
        val completed = CountDownLatch(1)
        val failure = AtomicReference<Throwable?>()
        var producer: Thread? = null
        try {
            assertNotEquals(Process.myPid(), renderer.service.pid)
            renderer.service.attach()
            renderer.service.holdMain()
            await { renderer.service.mainHeld() }
            val output = ("x".repeat(4096) + "\r\n\u001b[c").toByteArray()
            // More than a pipe buffer, the output channel and the ACK ledger
            // can hold. Closing a buffered stream by flushing would hang here.
            producer = thread(name = "terminal-fixture-output") {
                try { repeat(65) { feed(backend, output) } }
                catch (error: Throwable) { failure.set(error) }
                finally { completed.countDown() }
            }
            assertTrue("The backend was blocked by an unresponsive UI", completed.await(10, TimeUnit.SECONDS))
            failure.get()?.let { throw AssertionError("Terminal output failed", it) }
            assertFalse(backend.hasSurface())
            assertFalse(backend.transportClosed())
            val replies = ByteArrayOutputStream().apply { repeat(65) { write(expected) } }.toByteArray()
            assertArrayEquals(replies, IpcPayload.read(backend.takeInput()))
            assertTrue(backend.screen().isNotEmpty())
        } finally {
            try { closeRenderer(renderer) } finally {
                producer?.join(5_000)
                assertFalse("Terminal fixture IO thread leaked", producer?.isAlive == true)
            }
        }
    }

    private fun feed(backend: ITerminalBackendFixture, data: ByteArray) {
        IpcPayload.write(context, data).use { backend.feed(it!!) }
    }

    private fun withBackend(block: suspend (ITerminalBackendFixture) -> Unit) = runBlocking {
        withTimeout(60_000) {
            val binding = bind(TerminalBackendFixture::class.java, TerminalBackendFixture.CONTROL_ACTION) {
                ITerminalBackendFixture.Stub.asInterface(it)
            }
            try {
                assertEquals("The backend must share the instrumentation's default PID", Process.myPid(), binding.service.pid)
                binding.service.reset()
                block(binding.service)
            } finally { binding.close() }
        }
    }

    private suspend fun bindRenderer(): Binding<ITerminalRendererFixture> = bind(TerminalRendererFixture::class.java) {
        ITerminalRendererFixture.Stub.asInterface(it)
    }

    private fun closeRenderer(binding: Binding<ITerminalRendererFixture>) {
        try {
            runCatching { binding.service.releaseMain() }
            val pid = runCatching { binding.service.pid }.getOrNull()
            if (pid != null && pid != Process.myPid()) {
                runCatching { binding.service.detach() }
                val died = CountDownLatch(1)
                val recipient = IBinder.DeathRecipient { died.countDown() }
                val binder = binding.service.asBinder()
                runCatching { binder.linkToDeath(recipient, 0) }.onFailure { died.countDown() }
                try {
                    runCatching { binding.service.killProcess() }
                    assertTrue("Terminal renderer fixture process leaked", died.await(5, TimeUnit.SECONDS))
                } finally { runCatching { binder.unlinkToDeath(recipient, 0) } }
            }
        } finally { binding.close() }
    }

    private suspend fun await(condition: () -> Boolean) = withTimeout(15_000) {
        while (!condition()) delay(5)
    }

    private suspend fun <T> bind(type: Class<*>, action: String? = null, decode: (IBinder) -> T): Binding<T> {
        val connected = CompletableDeferred<T>()
        val connection = object : ServiceConnection {
            override fun onServiceConnected(name: ComponentName, binder: IBinder) { connected.complete(decode(binder)) }
            override fun onServiceDisconnected(name: ComponentName) = Unit
            override fun onNullBinding(name: ComponentName) { connected.completeExceptionally(IllegalStateException("Null fixture binding")) }
        }
        withContext(Dispatchers.Main) {
            check(context.bindService(Intent(context, type).setAction(action), connection, Context.BIND_AUTO_CREATE))
        }
        return try { Binding(connected.await(), context, connection) } catch (error: Throwable) {
            withContext(Dispatchers.Main) { context.unbindService(connection) }
            throw error
        }
    }

    private class Binding<T>(val service: T, private val context: Context, private val connection: ServiceConnection) : Closeable {
        private var closed = false
        override fun close() {
            if (closed) return
            closed = true
            runBlocking(Dispatchers.Main.immediate) { context.unbindService(connection) }
        }
    }
}
