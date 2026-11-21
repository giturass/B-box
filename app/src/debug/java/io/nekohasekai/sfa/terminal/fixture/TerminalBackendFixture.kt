package io.nekohasekai.sfa.terminal.fixture

import android.content.Intent
import android.os.IBinder
import android.os.ParcelFileDescriptor
import android.os.Process
import io.github.sagernet.libghostty.GhosttyTerminalSession
import io.nekohasekai.sfa.flutter.ipc.IpcPayload
import io.nekohasekai.sfa.terminal.BoxTerminalService
import io.nekohasekai.sfa.terminal.ManagedSession
import io.nekohasekai.sfa.terminal.TailscaleSSHPresentedSession
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runBlocking
import java.io.ByteArrayOutputStream

/** Real production terminal IPC with a deterministic transport; excluded from Release. */
class TerminalBackendFixture : BoxTerminalService() {
    private lateinit var managed: ManagedSession
    private val input = ByteArrayOutputStream()
    @Volatile private var transportClosed = false

    private val control = object : ITerminalBackendFixture.Stub() {
        override fun getPid(): Int = Process.myPid()

        override fun reset() = main { resetSession() }

        override fun feed(data: ParcelFileDescriptor) {
            val bytes = IpcPayload.read(IpcPayload.retain(data)) ?: error("Missing terminal fixture output")
            managed.feedOutput(bytes)
        }

        override fun inputSize(): Int = synchronized(input) { input.size() }

        override fun takeInput(): ParcelFileDescriptor {
            val bytes = synchronized(input) { input.toByteArray().also { input.reset() } }
            return IpcPayload.write(this@TerminalBackendFixture, bytes)!!
        }

        override fun transcript(): String = main { managed.terminalSession.transcriptText().orEmpty() }
        override fun screen(): String = main { managed.terminalSession.screenText().orEmpty() }
        override fun hasSurface(): Boolean = managed.hasSurface
        override fun transportClosed(): Boolean = transportClosed
    }

    override fun onCreate() {
        super.onCreate()
        resetSession()
    }

    override fun onBind(intent: Intent): IBinder =
        if (intent.action == CONTROL_ACTION) control else super.onBind(intent)

    protected override fun findSession(id: String): ManagedSession? = managed.takeIf { id == SESSION_ID }

    private fun resetSession() {
        if (::managed.isInitialized) managed.close()
        synchronized(input) { input.reset() }
        transportClosed = false
        managed = ManagedSession(
            id = SESSION_ID,
            terminalSession = GhosttyTerminalSession(this,
                GhosttyTerminalSession.Options(reportedVersion = "sing-box", terminfoName = "xterm-256color")),
            presentedSession = TailscaleSSHPresentedSession("fixture", "fixture", "127.0.0.1", "fixture", "xterm-256color", emptyList()),
        )
        managed.setTransport(object : GhosttyTerminalSession.Transport {
            override fun sendInput(data: ByteArray) { synchronized(input) { input.write(data) } }
            override fun sendResize(columns: Int, rows: Int, widthPixels: Int, heightPixels: Int) = Unit
            override fun close() { transportClosed = true }
        })
    }

    override fun onDestroy() {
        if (::managed.isInitialized) managed.close()
        super.onDestroy()
    }

    private fun <T> main(block: () -> T): T = runBlocking(Dispatchers.Main.immediate) { block() }

    companion object {
        const val CONTROL_ACTION = "terminal.fixture.control"
        const val SESSION_ID = "fixture"
    }
}
