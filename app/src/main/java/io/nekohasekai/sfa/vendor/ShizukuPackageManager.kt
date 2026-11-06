package io.nekohasekai.sfa.vendor

import android.content.pm.PackageInfo
import android.os.Process
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import rikka.shizuku.Shizuku

object ShizukuPackageManager {

    private val binderDeadListener = Shizuku.OnBinderDeadListener {
        ShizukuPrivilegedServiceClient.reset()
    }

    fun registerListeners() {
        Shizuku.addBinderDeadListener(binderDeadListener)
    }

    fun unregisterListeners() {
        Shizuku.removeBinderDeadListener(binderDeadListener)
    }

    fun requestPermission() = ShizukuAccess.requestPermission()

    suspend fun getInstalledPackages(flags: Int): List<PackageInfo> = withContext(Dispatchers.IO) {
        val service = ShizukuPrivilegedServiceClient.getService()
        val userId = Process.myUserHandle().hashCode()
        val slice = service.getInstalledPackages(flags, userId)
        @Suppress("UNCHECKED_CAST")
        slice.list as List<PackageInfo>
    }
}
