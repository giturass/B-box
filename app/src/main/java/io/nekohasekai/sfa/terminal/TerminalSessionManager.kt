package io.nekohasekai.sfa.terminal

import io.github.sagernet.libghostty.GhosttyTerminalSession
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import java.util.UUID

enum class TerminalSessionPhase { CONNECTING, RUNNING, FINISHED }

/** Calls are serialized with output parsing by [ManagedSession]. */
internal interface TerminalMirror {
    fun initial(snapshot: ByteArray, exitStatus: Int?): Boolean
    fun output(data: ByteArray, automaticReplies: List<ByteArray>): Boolean
    fun finish(exitStatus: Int)
    fun acknowledge(sequence: Long): Boolean
    fun commitThrough(sequence: Long): List<ByteArray>
    fun close(): List<ByteArray>
}

data class ManagedSession(
    val id: String = UUID.randomUUID().toString(),
    val terminalSession: GhosttyTerminalSession,
    val presentedSession: TailscaleSSHPresentedSession,
) {
    // The session owns its command client (a dedicated connection in remote
    // control mode) and must disconnect it when the session ends.
    var commandClient: io.nekohasekai.libbox.CommandClient? = null

    val phase = MutableStateFlow(TerminalSessionPhase.CONNECTING)

    val banner = MutableStateFlow<String?>(null)

    var exitWatcher: Job? = null

    // Exit detail for the notice written into the terminal; both stay null when
    // the session ends through a transport failure inside libghostty.
    var exitSignal: String? = null

    var exitError: String? = null

    // A Flutter process may disappear at any time. The backend keeps the VT,
    // transport and pending terminal-query replies until a surface confirms
    // parsing or its reply deadline expires. All changes share this lock,
    // including snapshots, so attaching never skips or repeats output.
    private val outputAccess = Any()
    private val generatedReplies = ThreadLocal<MutableList<ByteArray>?>()
    private var upstream: GhosttyTerminalSession.Transport? = null
    @Volatile private var mirror: TerminalMirror? = null

    val hasSurface: Boolean
        // Ghostty invokes notifications while holding its own VT lock. A
        // lock-free read avoids taking outputAccess in the opposite order.
        get() = mirror != null

    private val responseTransport = object : GhosttyTerminalSession.Transport {
        override fun sendInput(data: ByteArray) {
            val replies = generatedReplies.get()
            if (replies != null) replies.add(data.copyOf()) else upstream?.sendInput(data)
        }

        override fun sendResize(columns: Int, rows: Int, widthPixels: Int, heightPixels: Int) {
            upstream?.sendResize(columns, rows, widthPixels, heightPixels)
        }

        override fun close() {
            upstream?.close()
            upstream = null
        }
    }

    init {
        // Clipboard permissions and writes belong to the visible Android UI.
        terminalSession.systemClipboardWriteEnabled = false
    }

    fun setTransport(transport: GhosttyTerminalSession.Transport) = synchronized(outputAccess) {
        upstream = transport
        terminalSession.transport = responseTransport
    }

    fun clearTransport() = synchronized(outputAccess) {
        responseTransport.close()
        terminalSession.transport = null
    }

    fun feedOutput(data: ByteArray) = synchronized(outputAccess) {
        val surface = mirror
        val replies = if (surface != null) mutableListOf<ByteArray>() else null
        generatedReplies.set(replies)
        try {
            terminalSession.feedOutput(data)
        } finally {
            generatedReplies.remove()
        }
        if (surface != null && !surface.output(data, replies.orEmpty())) {
            detachSurface(surface)
            replies.orEmpty().forEach { upstream?.sendInput(it) }
        }
    }

    fun finish(exitCode: Int) = synchronized(outputAccess) {
        terminalSession.finish(exitCode)
        mirror?.finish(exitCode)
    }

    internal fun attachSurface(surface: TerminalMirror): Boolean = synchronized(outputAccess) {
        val snapshot = terminalSession.saveSnapshot() ?: return@synchronized false
        mirror?.let(::detachSurface)
        mirror = surface
        if (!surface.initial(snapshot, terminalSession.exitStatus.value)) {
            detachSurface(surface)
            return@synchronized false
        }
        true
    }

    internal fun detachSurface(surface: TerminalMirror) = synchronized(outputAccess) {
        val current = mirror === surface
        if (current) mirror = null
        // A dead UI cannot send an acknowledgement. Resume the backend's
        // replies in output order so a running shell never waits for its UI.
        // Even death before attachment must close under this same lock.
        val fallback = surface.close()
        if (current) fallback.forEach { upstream?.sendInput(it) }
    }

    internal fun surfaceInput(surface: TerminalMirror, data: ByteArray) = synchronized(outputAccess) {
        if (mirror === surface) upstream?.sendInput(data)
    }

    internal fun surfaceAcknowledged(surface: TerminalMirror, sequence: Long, replies: ByteArray) = synchronized(outputAccess) {
        if (mirror === surface && surface.acknowledge(sequence) && replies.isNotEmpty()) {
            upstream?.sendInput(replies)
        }
    }

    internal fun surfaceReplyTimeout(surface: TerminalMirror, sequence: Long) = synchronized(outputAccess) {
        if (mirror === surface) surface.commitThrough(sequence).forEach { upstream?.sendInput(it) }
    }

    internal fun withSurface(surface: TerminalMirror, operation: () -> Unit) = synchronized(outputAccess) {
        if (mirror === surface) operation()
    }

    fun close() = synchronized(outputAccess) {
        mirror?.close()
        mirror = null
        terminalSession.close()
    }
}
