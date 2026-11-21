package io.nekohasekai.sfa.flutter.ipc

import android.app.Service
import android.content.Intent
import android.os.Binder
import android.os.IBinder
import android.util.Log
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import java.io.Closeable
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicLong

/** Bound only while an Activity is attached; VPN and other foreground services are independent. */
internal class BoxUiService : Service() {
    companion object {
        @Volatile var createBackend: ((BackendSession) -> Closeable)? = null
    }

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val nextSession = AtomicLong()
    private val sessions = ConcurrentHashMap<Long, BackendSession>()
    private val binder = object : IBoxUiService.Stub() {
        override fun openSession(callback: IBoxUiCallback): IBoxUiSession {
            check(Binder.getCallingUid() == applicationInfo.uid) { "UI IPC is private to this application" }
            val id = nextSession.incrementAndGet()
            val session = BackendSession(this@BoxUiService, callback) { sessions.remove(id) }
            sessions[id] = session
            try {
                session.watchUiProcess()
            } catch (error: Exception) {
                session.disconnect("UI process is unavailable", recoverable = false)
                return session.binder
            }
            scope.launch {
                if (session.isConnected) {
                    try {
                        val factory = createBackend ?: error("Native UI backend has not been initialized")
                        session.attachBackend(factory(session))
                    } catch (error: Exception) {
                        Log.e("BoxUiIPC", "Unable to create native UI host", error)
                        session.disconnect("Unable to create native UI host", recoverable = false)
                    }
                }
            }
            return session.binder
        }
    }

    override fun onBind(intent: Intent?): IBinder = binder

    override fun onDestroy() {
        sessions.values.toList().forEach { it.disconnect("Native UI service was destroyed") }
        sessions.clear()
        scope.cancel()
        super.onDestroy()
    }
}
