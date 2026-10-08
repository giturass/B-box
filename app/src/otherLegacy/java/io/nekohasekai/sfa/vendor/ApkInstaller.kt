package io.nekohasekai.sfa.vendor

import android.content.Context
import io.nekohasekai.sfa.bg.RootClient
import io.nekohasekai.sfa.database.Settings
import java.io.File

enum class InstallMethod {
    PACKAGE_INSTALLER,
    ROOT,
}

object ApkInstaller {

    fun getConfiguredMethod(): InstallMethod = if (Settings.silentInstallEnabled) {
        val method = Settings.silentInstallMethod
        if (method == "SHIZUKU") InstallMethod.ROOT else InstallMethod.valueOf(method)
    } else {
        InstallMethod.PACKAGE_INSTALLER
    }

    suspend fun install(context: Context, apkFile: File, method: InstallMethod = getConfiguredMethod()) {
        when (method) {
            InstallMethod.ROOT -> RootInstaller.install(apkFile)
            InstallMethod.PACKAGE_INSTALLER -> SystemPackageInstaller.install(context, apkFile)
        }
    }

    fun canSystemSilentInstall(): Boolean = SystemPackageInstaller.canSystemSilentInstall()

    suspend fun canSilentInstall(): Boolean {
        val method = getConfiguredMethod()
        return when (method) {
            InstallMethod.PACKAGE_INSTALLER -> canSystemSilentInstall()
            InstallMethod.ROOT -> RootClient.checkRootAvailable()
        }
    }
}
