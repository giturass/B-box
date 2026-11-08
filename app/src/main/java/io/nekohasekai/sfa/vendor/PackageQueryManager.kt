package io.nekohasekai.sfa.vendor

import android.content.pm.PackageInfo
import android.content.pm.PackageManager
import android.os.Build
import io.nekohasekai.sfa.Application
import io.nekohasekai.sfa.BuildConfig
import io.nekohasekai.sfa.bg.RootClient
import io.nekohasekai.sfa.constant.SettingsKey
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.database.preference.KeyValueEntity
import io.nekohasekai.sfa.utils.HookStatusClient
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow

object PackageQueryManager {

    val strategy: PackageQueryStrategy
        get() = strategy(queryMode.value)

    private fun strategy(mode: String): PackageQueryStrategy = when {
        HookStatusClient.status.value?.compatible == true -> PackageQueryStrategy.Module
        BuildConfig.FLAVOR == "play" -> PackageQueryStrategy.UserSelected(mode)
        else -> PackageQueryStrategy.Direct
    }

    private val _queryMode = MutableStateFlow(Settings.perAppProxyPackageQueryMode)
    val queryMode: StateFlow<String> = _queryMode

    fun registerListeners() {
        ShizukuPackageManager.registerListeners()
        _queryMode.value = Settings.perAppProxyPackageQueryMode
    }

    fun unregisterListeners() {
        ShizukuPackageManager.unregisterListeners()
    }

    fun requestShizukuPermission() {
        ShizukuPackageManager.requestPermission()
    }

    suspend fun checkRootAvailable(): Boolean = RootClient.checkRootAvailable()

    suspend fun setQueryMode(mode: String) {
        require(mode in setOf(Settings.PACKAGE_QUERY_MODE_ROOT, Settings.PACKAGE_QUERY_MODE_SHIZUKU))
        Settings.dataStore.putBatchConfirmed(listOf(KeyValueEntity(SettingsKey.PER_APP_PROXY_PACKAGE_QUERY_MODE).put(mode)))
        refreshQueryMode()
    }

    fun refreshQueryMode() { _queryMode.value = Settings.perAppProxyPackageQueryMode }

    suspend fun getInstalledPackages(flags: Int, mode: String = queryMode.value): List<PackageInfo> = when (val s = strategy(mode)) {
        is PackageQueryStrategy.Module -> {
            // Ask the injected service before using the saved ordinary query strategy.
            val packages = try {
                HookStatusClient.getInstalledPackages(Application.application, flags.toLong(), android.os.Process.myUserHandle().hashCode())
            } catch (_: Exception) { null }
            packages ?: run {
                HookStatusClient.refresh()
                if (BuildConfig.FLAVOR != "play") getPackagesViaPackageManager(flags)
                else if (mode == Settings.PACKAGE_QUERY_MODE_ROOT) RootClient.getInstalledPackages(flags)
                else ShizukuPackageManager.getInstalledPackages(flags)
            }
        }
        is PackageQueryStrategy.UserSelected -> when (s.mode) {
            Settings.PACKAGE_QUERY_MODE_ROOT -> RootClient.getInstalledPackages(flags)
            else -> ShizukuPackageManager.getInstalledPackages(flags)
        }
        is PackageQueryStrategy.Direct -> getPackagesViaPackageManager(flags)
    }

    private fun getPackagesViaPackageManager(flags: Int): List<PackageInfo> = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
        Application.packageManager.getInstalledPackages(
            PackageManager.PackageInfoFlags.of(flags.toLong()),
        )
    } else {
        @Suppress("DEPRECATION")
        Application.packageManager.getInstalledPackages(flags)
    }
}
