package io.nekohasekai.sfa.vendor

import android.content.Context
import io.nekohasekai.sfa.Application
import io.nekohasekai.sfa.bg.BoxService
import io.nekohasekai.sfa.bg.RootClient
import io.nekohasekai.sfa.database.Settings
import kotlinx.coroutines.delay
import java.io.File

enum class InstallMethod {
    PACKAGE_INSTALLER,
    SHIZUKU,
    ROOT,
}

object ApkInstaller {

    private suspend fun stopServiceIfRunning() {
        val commandSocket = File(Application.application.filesDir, "command.sock")
        if (!commandSocket.exists()) {
            return
        }
        BoxService.stop()
        repeat(20) {
            delay(100)
            if (!commandSocket.exists()) {
                return
            }
        }
    }

    fun getConfiguredMethod(): InstallMethod = if (Settings.silentInstallEnabled) {
        InstallMethod.valueOf(Settings.silentInstallMethod)
    } else {
        InstallMethod.PACKAGE_INSTALLER
    }

    suspend fun install(context: Context, apkFile: File, method: InstallMethod = getConfiguredMethod()) {
        stopServiceIfRunning()
        when (method) {
            InstallMethod.SHIZUKU -> ShizukuInstaller.install(apkFile)
            InstallMethod.ROOT -> RootInstaller.install(apkFile)
            InstallMethod.PACKAGE_INSTALLER -> SystemPackageInstaller.install(context, apkFile)
        }
    }

    fun canSystemSilentInstall(): Boolean = SystemPackageInstaller.canSystemSilentInstall()

    suspend fun canSilentInstall(): Boolean {
        val method = getConfiguredMethod()
        return when (method) {
            InstallMethod.PACKAGE_INSTALLER -> canSystemSilentInstall()
            InstallMethod.SHIZUKU -> ShizukuInstaller.isAvailable() && ShizukuInstaller.checkPermission()
            InstallMethod.ROOT -> RootClient.checkRootAvailable()
        }
    }
}
