package io.nekohasekai.sfa.vendor

import android.content.pm.PackageManager
import android.os.Handler
import android.os.Looper
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import rikka.shizuku.Shizuku

object ShizukuAccess {

    private var nextPermissionRequestCode = 1001
    private var pendingPermission: CompletableDeferred<Boolean>? = null
    private val permissionHandler = Handler(Looper.getMainLooper())

    fun isAvailable(): Boolean = try {
        Shizuku.pingBinder()
    } catch (e: Exception) {
        false
    }

    fun checkPermission(): Boolean = try {
        if (Shizuku.isPreV11()) {
            false
        } else {
            Shizuku.checkSelfPermission() == PackageManager.PERMISSION_GRANTED
        }
    } catch (e: Exception) {
        false
    }

    /** The caller holds the UI's external-result lease while this native connection waits. */
    suspend fun requestPermission(): Boolean = withContext(Dispatchers.Main.immediate) {
        if (!isAvailable() || Shizuku.isPreV11()) return@withContext false
        if (checkPermission()) return@withContext true
        pendingPermission?.let { return@withContext it.await() }
        val request = CompletableDeferred<Boolean>()
        pendingPermission = request
        val requestCode = nextPermissionRequestCode
        nextPermissionRequestCode = if (requestCode == Int.MAX_VALUE) 1001 else requestCode + 1
        val permissionListener = Shizuku.OnRequestPermissionResultListener { code, grantResult ->
            if (code == requestCode) {
                request.complete(grantResult == PackageManager.PERMISSION_GRANTED && checkPermission())
            }
        }
        val deadListener = Shizuku.OnBinderDeadListener { request.complete(false) }
        try {
            // Posting callbacks avoids removing a listener while Shizuku is dispatching its list.
            Shizuku.addRequestPermissionResultListener(permissionListener, permissionHandler)
            Shizuku.addBinderDeadListener(deadListener, permissionHandler)
            Shizuku.requestPermission(requestCode)
            request.await()
        } finally {
            if (pendingPermission === request) pendingPermission = null
            request.cancel()
            Shizuku.removeRequestPermissionResultListener(permissionListener)
            Shizuku.removeBinderDeadListener(deadListener)
        }
    }

}
