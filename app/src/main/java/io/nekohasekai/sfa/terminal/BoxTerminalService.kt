package io.nekohasekai.sfa.terminal

import android.app.Service
import android.content.Intent
import android.os.IBinder
import android.os.ParcelFileDescriptor
import io.github.sagernet.libghostty.extras.GhosttyThemeStore
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.terminal.TerminalStream.readBytesFrame
import io.nekohasekai.sfa.terminal.TerminalStream.writeBytesFrame
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import java.io.BufferedInputStream
import java.io.BufferedOutputStream
import java.io.DataInputStream
import java.io.DataOutputStream
import java.util.concurrent.atomic.AtomicBoolean

/** A bound UI surface never owns the SSH connection or native terminal handle. */
open class BoxTerminalService : Service() {
    private val surfaces = mutableSetOf<Surface>()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)

    private val binder = object : IBoxTerminalService.Stub() {
        override fun attach(id: String, output: ParcelFileDescriptor, input: ParcelFileDescriptor, clientToken: IBinder): IBoxTerminalSession =
            try {
                runBlocking(Dispatchers.Main.immediate) {
                    val managed = findSession(id)
                        ?: error("Terminal session no longer exists")
                    // Take our own descriptors, including for callers in the same
                    // process, and release the incoming Binder copies below.
                    val outputCopy = ParcelFileDescriptor.dup(output.fileDescriptor)
                    val inputCopy = try {
                        ParcelFileDescriptor.dup(input.fileDescriptor)
                    } catch (error: Exception) {
                        outputCopy.close()
                        throw error
                    }
                    val surface = Surface(managed, outputCopy, inputCopy, clientToken)
                    try {
                        clientToken.linkToDeath(surface, 0)
                        surface.linked = true
                        surfaces.add(surface)
                        check(managed.attachSurface(surface)) { "Terminal snapshot is temporarily unavailable" }
                        surface.start()
                        surface
                    } catch (error: Exception) {
                        managed.detachSurface(surface)
                        throw error
                    }
                }
            } finally {
                runCatching { output.close() }
                runCatching { input.close() }
            }
    }

    override fun onBind(intent: Intent): IBinder = binder

    protected open fun findSession(id: String): ManagedSession? =
        TailscaleSSHSessionStore.state.value.sessions.firstOrNull { it.id == id }

    override fun onDestroy() {
        surfaces.toList().forEach { it.detach() }
        scope.cancel()
        super.onDestroy()
    }

    private sealed interface Frame {
        data class Snapshot(val data: ByteArray) : Frame
        data class Output(val sequence: Long, val data: ByteArray) : Frame
        data class Finished(val status: Int) : Frame
    }

    private inner class Surface(
        private val managed: ManagedSession,
        private val outputFD: ParcelFileDescriptor,
        private val inputFD: ParcelFileDescriptor,
        private val token: IBinder,
    ) : IBoxTerminalSession.Stub(), TerminalMirror, IBinder.DeathRecipient {
        private val surfaceScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        private val output = DataOutputStream(BufferedOutputStream(ParcelFileDescriptor.AutoCloseOutputStream(outputFD)))
        private val input = DataInputStream(BufferedInputStream(ParcelFileDescriptor.AutoCloseInputStream(inputFD)))
        private val frames = Channel<Frame>(64)
        private val closed = AtomicBoolean()
        private val replies = TerminalReplyLedger()
        var linked = false

        override fun initial(snapshot: ByteArray, exitStatus: Int?): Boolean {
            if (closed.get()) return false
            if (!frames.trySend(Frame.Snapshot(snapshot)).isSuccess) return false
            return exitStatus == null || frames.trySend(Frame.Finished(exitStatus)).isSuccess
        }

        override fun output(data: ByteArray, automaticReplies: List<ByteArray>): Boolean {
            if (closed.get()) return false
            val next = replies.reserve(automaticReplies) ?: return false
            if (frames.trySend(Frame.Output(next, data.copyOf())).isSuccess) {
                if (automaticReplies.isNotEmpty()) surfaceScope.launch {
                    // Programs querying terminal capabilities must not wait
                    // indefinitely for an unresponsive Android UI thread.
                    delay(250)
                    managed.surfaceReplyTimeout(this@Surface, next)
                }
                return true
            }
            replies.rollback(next)
            return false
        }

        override fun finish(exitStatus: Int) {
            if (!frames.trySend(Frame.Finished(exitStatus)).isSuccess) detach()
        }

        override fun acknowledge(sequence: Long): Boolean = replies.acknowledge(sequence)

        override fun commitThrough(sequence: Long): List<ByteArray> = replies.commitThrough(sequence)

        fun start() {
            surfaceScope.launch {
                try {
                    output.writeInt(TerminalStream.MAGIC)
                    output.writeInt(TerminalStream.VERSION)
                    output.writeUTF(managed.presentedSession.terminalType)
                    for (frame in frames) {
                        when (frame) {
                            is Frame.Snapshot -> {
                                output.writeInt(TerminalStream.SNAPSHOT)
                                output.writeBytesFrame(frame.data)
                            }
                            is Frame.Output -> {
                                output.writeInt(TerminalStream.OUTPUT)
                                output.writeLong(frame.sequence)
                                output.writeBytesFrame(frame.data)
                            }
                            is Frame.Finished -> {
                                output.writeInt(TerminalStream.FINISHED)
                                output.writeInt(frame.status)
                            }
                        }
                        output.flush()
                    }
                } catch (_: Exception) {
                    // The UI can close either pipe without closing SSH.
                } finally {
                    detach()
                }
            }
            surfaceScope.launch {
                try {
                    while (!closed.get()) {
                        when (input.readInt()) {
                            TerminalStream.INPUT -> {
                                val data = input.readBytesFrame()
                                withContext(Dispatchers.Main.immediate) { managed.surfaceInput(this@Surface, data) }
                            }
                            TerminalStream.ACKNOWLEDGED -> {
                                val sequence = input.readLong()
                                val replies = input.readBytesFrame()
                                withContext(Dispatchers.Main.immediate) {
                                    managed.surfaceAcknowledged(this@Surface, sequence, replies)
                                }
                            }
                            else -> error("Invalid terminal input frame")
                        }
                    }
                } catch (_: Exception) {
                    // A dead UI and a failed pipe share the same detach path.
                } finally {
                    detach()
                }
            }
        }

        override fun resize(columns: Int, rows: Int, widthPixels: Int, heightPixels: Int) {
            if (columns < 1 || rows < 1 || widthPixels < 0 || heightPixels < 0) return
            scope.launch {
                managed.withSurface(this@Surface) {
                    managed.terminalSession.resize(columns, rows, widthPixels / columns, heightPixels / rows)
                }
            }
        }

        override fun setAppearance(dark: Boolean) {
            scope.launch {
                managed.withSurface(this@Surface) {
                    val name = if (dark) Settings.tailscaleSSHDarkTheme else Settings.tailscaleSSHLightTheme
                    val custom = if (name.isEmpty()) GhosttyCustomConfig.parse(
                        if (dark) Settings.tailscaleSSHDarkConfig else Settings.tailscaleSSHLightConfig,
                    ) else null
                    val theme = custom?.theme ?: GhosttyThemeStore.loadThemeOrDefault(this@BoxTerminalService, name, dark)
                    theme?.let(managed.terminalSession::setTheme)
                    managed.terminalSession.setColorScheme(dark)
                }
            }
        }

        override fun detach() {
            managed.detachSurface(this)
        }

        override fun binderDied() {
            // This also disposes death between linkToDeath and attachSurface,
            // under the output lock shared with reply reservations.
            detach()
        }

        override fun close(): List<ByteArray> {
            if (!closed.compareAndSet(false, true)) return emptyList()
            val fallback = replies.drain()
            if (linked) runCatching { token.unlinkToDeath(this, 0) }
            frames.cancel()
            // Close descriptors before cancelling readers/writers; flushing a
            // buffered output stream here could block the backend main thread.
            runCatching { outputFD.close() }
            runCatching { inputFD.close() }
            surfaceScope.cancel()
            scope.launch { surfaces.remove(this@Surface) }
            return fallback
        }
    }
}
