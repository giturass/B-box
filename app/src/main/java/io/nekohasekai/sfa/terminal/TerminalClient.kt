package io.nekohasekai.sfa.terminal

import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.content.ServiceConnection
import android.os.Binder
import android.os.IBinder
import android.os.Looper
import android.os.ParcelFileDescriptor
import android.os.RemoteException
import io.github.sagernet.libghostty.GhosttyTerminalSession
import io.nekohasekai.sfa.terminal.TerminalStream.readBytesFrame
import io.nekohasekai.sfa.terminal.TerminalStream.writeBytesFrame
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.cancel
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import java.io.BufferedInputStream
import java.io.BufferedOutputStream
import java.io.ByteArrayOutputStream
import java.io.DataInputStream
import java.io.DataOutputStream
import java.io.IOException
import java.util.concurrent.atomic.AtomicBoolean

/** UI-side mirrors. Closing the client never closes a backend terminal session. */
internal class TerminalClient(context: Context, private val uiScope: CoroutineScope) {
    private val context = context.applicationContext
    private var service: IBoxTerminalService? = null
    private var ready = CompletableDeferred<IBoxTerminalService>()
    private var bound = false
    private var closed = false
    private val attachments = mutableSetOf<Attachment>()

    private val connection = object : ServiceConnection {
        override fun onServiceConnected(name: ComponentName, binder: IBinder) {
            if (closed || !bound || !binder.isBinderAlive) return
            val connected = IBoxTerminalService.Stub.asInterface(binder)
            service = connected
            ready.complete(connected)
        }

        override fun onServiceDisconnected(name: ComponentName) {
            lostConnection("Terminal backend disconnected", unbind = false)
        }

        override fun onBindingDied(name: ComponentName) {
            lostConnection("Terminal backend binding died", unbind = true)
        }

        override fun onNullBinding(name: ComponentName) {
            lostConnection("Terminal backend unavailable", unbind = true)
        }
    }

    private fun lostConnection(message: String, unbind: Boolean) {
        if (closed) return
        service = null
        val previous = ready
        ready = CompletableDeferred()
        if (unbind && bound) {
            bound = false
            runCatching { context.unbindService(connection) }
        }
        attachments.toList().forEach { it.close() }
        // Main.immediate waiters can resume inline. Publish all new binding
        // state before failing their old future so they can bind immediately.
        previous.completeExceptionally(IllegalStateException(message))
    }

    private suspend fun service(): IBoxTerminalService {
        for (attempt in 0 until 3) {
            check(!closed) { "Terminal view has been disposed" }
            service?.let {
                if (it.asBinder().isBinderAlive) return it
                lostConnection("Terminal backend Binder died", unbind = true)
            }
            if (!bound) {
                bound = true
                try {
                    check(context.bindService(Intent(context, BoxTerminalService::class.java), connection, Context.BIND_AUTO_CREATE)) {
                        "Unable to bind the terminal backend"
                    }
                } catch (error: Exception) {
                    bound = false
                    throw error
                }
            }
            val awaiting = ready
            try {
                val connected = withTimeout(15_000) { awaiting.await() }
                if (connected.asBinder().isBinderAlive) return connected
                lostConnection("Terminal backend Binder died while connecting", unbind = true)
            } catch (error: Exception) {
                if (error is CancellationException && error !is TimeoutCancellationException) throw error
                if (closed || attempt == 2) throw error
                // A lifecycle callback has already failed the old future and
                // published a new one. A timeout must also release its bind.
                if (awaiting === ready) lostConnection("Terminal backend connection timed out", unbind = true)
            }
        }
        error("Terminal backend unavailable")
    }

    suspend fun attach(id: String, onClosed: (Attachment) -> Unit): Attachment {
        for (attempt in 0 until 2) {
            try {
                return attachOnce(id, onClosed)
            } catch (error: Exception) {
                if (closed || attempt == 1 || error is CancellationException) throw error
                if (error !is RemoteException && error !is IOException) throw error
                if (error is RemoteException) lostConnection("Terminal backend died during attachment", unbind = true)
                // This also covers process death after Binder returned but
                // before the initial snapshot reached a registered attachment.
                delay(100)
            }
        }
        error("Terminal attachment unavailable")
    }

    private suspend fun attachOnce(id: String, onClosed: (Attachment) -> Unit): Attachment {
        val backend = service()
        val output = ParcelFileDescriptor.createPipe()
        val input = try { ParcelFileDescriptor.createPipe() } catch (error: Exception) {
            output.forEach { it.close() }
            throw error
        }
        val token = Binder()
        val endpoint = try {
            withContext(Dispatchers.IO) { backend.attach(id, output[1], input[0], token) }
        } catch (error: Exception) {
            output[0].close()
            input[1].close()
            throw error
        } finally {
            output[1].close()
            input[0].close()
        }
        val attachment = Attachment(endpoint, token, output[0], input[1], onClosed)
        attachments.add(attachment)
        try {
            attachment.initialize()
            if (attachment.isClosed) throw IOException("Terminal stream disconnected during restoration")
            return attachment
        } catch (error: Exception) {
            attachment.close()
            throw error
        }
    }

    fun close() {
        if (closed) return
        closed = true
        attachments.toList().forEach { it.close() }
        ready.cancel()
        if (bound) runCatching { context.unbindService(connection) }
        bound = false
    }

    private sealed interface InputFrame {
        data class Input(val data: ByteArray) : InputFrame
        data class Acknowledged(val sequence: Long, val replies: ByteArray) : InputFrame
        data class Resize(val columns: Int, val rows: Int, val widthPixels: Int, val heightPixels: Int) : InputFrame
        data class Appearance(val dark: Boolean) : InputFrame
    }

    inner class Attachment internal constructor(
        private val endpoint: IBoxTerminalSession,
        // Keep the local Binder alive until the complete attachment is gone.
        private val token: IBinder,
        private val outputFD: ParcelFileDescriptor,
        private val inputFD: ParcelFileDescriptor,
        private val onClosed: (Attachment) -> Unit,
    ) {
        private val ioScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        private val output = DataInputStream(BufferedInputStream(ParcelFileDescriptor.AutoCloseInputStream(outputFD)))
        private val input = DataOutputStream(BufferedOutputStream(ParcelFileDescriptor.AutoCloseOutputStream(inputFD)))
        private val outgoing = Channel<InputFrame>(Channel.UNLIMITED)
        private val closed = AtomicBoolean()
        private var generatedReplies: ByteArrayOutputStream? = null
        lateinit var session: GhosttyTerminalSession
            private set
        val isClosed: Boolean get() = closed.get()

        suspend fun initialize() {
            val (type, snapshot) = withContext(Dispatchers.IO) {
                check(output.readInt() == TerminalStream.MAGIC && output.readInt() == TerminalStream.VERSION) { "Invalid terminal stream" }
                val type = output.readUTF()
                check(output.readInt() == TerminalStream.SNAPSHOT) { "Missing terminal snapshot" }
                type to output.readBytesFrame()
            }
            if (closed.get()) throw IOException("Terminal stream disconnected before restoration")
            session = GhosttyTerminalSession.restore(context, snapshot,
                GhosttyTerminalSession.Options(reportedVersion = "sing-box", terminfoName = type))
                ?: error("Unable to restore terminal state")
            session.transport = object : GhosttyTerminalSession.Transport {
                override fun sendInput(data: ByteArray) {
                    val replies = generatedReplies
                    if (replies != null) replies.write(data) else outgoing.trySend(InputFrame.Input(data.copyOf()))
                }

                override fun sendResize(columns: Int, rows: Int, widthPixels: Int, heightPixels: Int) {
                    outgoing.trySend(InputFrame.Resize(columns, rows, widthPixels, heightPixels))
                }

                override fun close() = this@Attachment.close()
            }
            ioScope.launch {
                try {
                    for (frame in outgoing) {
                        when (frame) {
                            is InputFrame.Input -> {
                                input.writeInt(TerminalStream.INPUT)
                                input.writeBytesFrame(frame.data)
                                input.flush()
                            }
                            is InputFrame.Acknowledged -> {
                                input.writeInt(TerminalStream.ACKNOWLEDGED)
                                input.writeLong(frame.sequence)
                                input.writeBytesFrame(frame.replies)
                                input.flush()
                            }
                            is InputFrame.Resize -> endpoint.resize(frame.columns, frame.rows, frame.widthPixels, frame.heightPixels)
                            is InputFrame.Appearance -> endpoint.setAppearance(frame.dark)
                        }
                    }
                } catch (_: Exception) {
                    // Process death and ordinary view disposal close the pipe.
                } finally { close() }
            }
            ioScope.launch {
                try {
                    while (!closed.get()) {
                        when (output.readInt()) {
                            TerminalStream.OUTPUT -> {
                                val sequence = output.readLong()
                                val data = output.readBytesFrame()
                                withContext(Dispatchers.Main.immediate) {
                                    if (!closed.get()) {
                                        val replies = ByteArrayOutputStream()
                                        generatedReplies = replies
                                        try { session.feedOutput(data) } finally { generatedReplies = null }
                                        outgoing.trySend(InputFrame.Acknowledged(sequence, replies.toByteArray()))
                                    }
                                }
                            }
                            TerminalStream.FINISHED -> {
                                val status = output.readInt()
                                withContext(Dispatchers.Main.immediate) { if (!closed.get()) session.finish(status) }
                            }
                            else -> error("Invalid terminal output frame")
                        }
                    }
                } catch (_: Exception) {
                    // Reattaching reads a fresh complete backend snapshot.
                } finally { close() }
            }
        }

        fun setAppearance(dark: Boolean, apply: () -> Unit) {
            // The backend updates its own palette and sends the appearance
            // report. Suppress that report from the UI copy to avoid duplicates.
            generatedReplies = ByteArrayOutputStream()
            try { apply() } finally { generatedReplies = null }
            outgoing.trySend(InputFrame.Appearance(dark))
        }

        fun close() {
            if (!closed.compareAndSet(false, true)) return
            runCatching { outputFD.close() }
            runCatching { inputFD.close() }
            outgoing.cancel()
            ioScope.cancel()
            runCatching { if (token.isBinderAlive) endpoint.detach() }
            val finish = {
                if (::session.isInitialized) session.close()
                attachments.remove(this)
                onClosed(this)
            }
            if (Looper.myLooper() == Looper.getMainLooper()) finish() else uiScope.launch { finish() }
        }
    }
}
