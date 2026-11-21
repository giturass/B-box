package io.nekohasekai.sfa.terminal.fixture

import android.app.Service
import android.content.ComponentName
import android.content.Context
import android.content.ContextWrapper
import android.content.Intent
import android.content.ServiceConnection
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.os.ParcelFileDescriptor
import android.os.Process
import io.nekohasekai.sfa.flutter.UiProcess
import io.nekohasekai.sfa.flutter.ipc.IpcPayload
import io.nekohasekai.sfa.terminal.TerminalClient
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

/** Runs the production mirror in the actual :ui PID; excluded from Release. */
class TerminalRendererFixture : Service() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private var client: TerminalClient? = null
    private var attachment: TerminalClient.Attachment? = null
    private val held = AtomicBoolean()
    private val release = CountDownLatch(1)

    private val binder = object : ITerminalRendererFixture.Stub() {
        override fun getPid(): Int = Process.myPid()

        override fun attach() = attachClient(interruptConnection = false)
        override fun attachWithInterruptedConnection(bindingDied: Boolean) =
            attachClient(interruptConnection = true, bindingDied = bindingDied)

        override fun sendInput(data: ParcelFileDescriptor) {
            val bytes = IpcPayload.read(IpcPayload.retain(data)) ?: error("Missing terminal fixture input")
            main { checkNotNull(attachment).session.sendRawInput(bytes) }
        }

        override fun transcript(): String = main { checkNotNull(attachment).session.transcriptText().orEmpty() }
        override fun screen(): String = main { checkNotNull(attachment).session.screenText().orEmpty() }
        override fun detach() = main { detachClient() }

        override fun holdMain() {
            scope.launch {
                held.set(true)
                try { release.await(30, TimeUnit.SECONDS) } finally { held.set(false) }
            }
        }

        override fun mainHeld(): Boolean = held.get()
        override fun releaseMain() { release.countDown() }
        override fun killProcess() {
            check(UiProcess.isUiProcess(this@TerminalRendererFixture)) { "Refusing to kill the backend fixture process" }
            Process.killProcess(Process.myPid())
        }
    }

    override fun onBind(intent: Intent): IBinder = binder

    private fun attachClient(interruptConnection: Boolean, bindingDied: Boolean = false) = runBlocking(Dispatchers.Main.immediate) {
        detachClient()
        val next = TerminalClient(BackendContext(this@TerminalRendererFixture, interruptConnection, bindingDied), scope)
        client = next
        withTimeout(5_000) {
            attachment = next.attach(TerminalBackendFixture.SESSION_ID) { closed ->
                if (attachment === closed) attachment = null
            }
        }
    }

    private fun detachClient() {
        attachment = null
        client?.close()
        client = null
    }

    override fun onDestroy() {
        release.countDown()
        detachClient()
        scope.cancel()
        super.onDestroy()
    }

    private fun <T> main(block: () -> T): T = runBlocking(Dispatchers.Main.immediate) { block() }

    private class BackendContext(base: Context, private val interruptConnection: Boolean,
        private val bindingDied: Boolean) : ContextWrapper(base) {
        private val bindings = mutableMapOf<ServiceConnection, ServiceConnection>()
        private val handler = Handler(Looper.getMainLooper())
        private var interrupted = false
        override fun getApplicationContext(): Context = this

        override fun bindService(service: Intent, connection: ServiceConnection, flags: Int): Boolean {
            val wrapped = object : ServiceConnection {
                override fun onServiceConnected(name: ComponentName, binder: IBinder) {
                    if (interruptConnection && !interrupted) {
                        interrupted = true
                        // Fail a waiter before delivering the first real Binder.
                        // The Binder and both pipes still cross Android processes.
                        if (bindingDied) connection.onBindingDied(name)
                        else {
                            connection.onServiceDisconnected(name)
                            handler.post {
                                if (bindings[connection] === this) connection.onServiceConnected(name, binder)
                            }
                        }
                    } else connection.onServiceConnected(name, binder)
                }
                override fun onServiceDisconnected(name: ComponentName) = connection.onServiceDisconnected(name)
                override fun onBindingDied(name: ComponentName) = connection.onBindingDied(name)
                override fun onNullBinding(name: ComponentName) = connection.onNullBinding(name)
            }
            bindings[connection] = wrapped
            return super.bindService(Intent(service).setClass(baseContext, TerminalBackendFixture::class.java), wrapped, flags)
                .also { if (!it) bindings.remove(connection) }
        }

        override fun unbindService(connection: ServiceConnection) =
            super.unbindService(bindings.remove(connection) ?: connection)
    }
}
