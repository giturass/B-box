package io.nekohasekai.sfa.utils

import android.content.Context
import io.nekohasekai.sfa.bg.PackageEntry
import io.nekohasekai.sfa.bg.ParceledListSlice
import io.nekohasekai.sfa.constant.SettingsKey
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.database.preference.KeyValueEntity
import io.nekohasekai.sfa.xposed.HookModuleVersion
import io.nekohasekai.sfa.xposed.HookStatusKeys
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext

object PrivilegeSettingsClient {
    private val access = Mutex()
    private var appContext: Context? = null
    private var lastSynced: Pair<Int, String>? = null

    suspend fun register(context: Context) = withContext(Dispatchers.IO) {
        access.withLock {
            appContext = context.applicationContext
            HookStatusClient.register(context)
            refreshLocked()
        }
    }

    fun read(): PrivilegeSettingsConfig = PrivilegeSettingsConfig(
        Settings.privilegeSettingsEnabled, Settings.privilegeSettingsList.toSet(),
        Settings.privilegeSettingsInterfaceRenameEnabled, Settings.privilegeSettingsInterfacePrefix,
    )

    suspend fun refresh(): Map<String, Any?> = withContext(Dispatchers.IO) { access.withLock { refreshLocked() } }

    private fun refreshLocked(): Map<String, Any?> {
        val config = read()
        val status = HookStatusClient.refresh()
        var error = HookStatusClient.lastError
        if (status?.compatible == true) {
            try {
                if (lastSynced != (status.systemPid to config.revision())) sync(config)
            } catch (failure: Exception) {
                error = failure.message ?: failure.toString()
            }
        } else lastSynced = null
        return mapOf(
            "settings" to config.toWire(), "revision" to config.revision(),
            "active" to (status?.active == true), "compatible" to (status?.compatible == true),
            "expectedVersion" to HookModuleVersion.CURRENT, "version" to status?.version,
            "systemPid" to status?.systemPid, "lastPatchedAt" to status?.lastPatchedAt,
            "synced" to (status?.compatible == true && lastSynced == (status.systemPid to config.revision())),
            "error" to error,
        )
    }

    suspend fun update(
        revision: String,
        next: PrivilegeSettingsConfig,
        checkReady: suspend () -> Unit,
        restart: suspend (previous: PrivilegeSettingsConfig, next: PrivilegeSettingsConfig, recover: Boolean) -> Unit,
    ): Map<String, Any?> = withContext(Dispatchers.IO) {
        access.withLock {
            checkReady()
            next.validate()
            val previous = read()
            require(revision == previous.revision()) { "Privilege settings changed; refresh before saving" }
            requireCompatible()
            PrivilegeSettingsChange.apply(previous, next, ::write, ::sync) { recover ->
                restart(previous, next, recover)
            }
            refreshLocked()
        }
    }

    private fun requireCompatible(): HookStatusClient.Status {
        val status = HookStatusClient.refresh()
        check(status?.active == true) { "LSPosed module is not active in the Android system" }
        check(status.compatible) { "LSPosed module version changed; reboot Android before applying settings" }
        return status
    }

    private fun sync(config: PrivilegeSettingsConfig) {
        val status = requireCompatible()
        config.validate()
        val context = checkNotNull(appContext) { "LSPosed bridge is not initialized" }
        val binder = checkNotNull(ConnectivityBinderUtils.getBinder(context)) { "Android connectivity service is unavailable" }
        lastSynced = null
        ConnectivityBinderUtils.withParcel { data, reply ->
            data.writeInterfaceToken(HookStatusKeys.DESCRIPTOR)
            data.writeInt(if (config.enabled) 1 else 0)
            ParceledListSlice(config.packages.sorted().map(::PackageEntry)).writeToParcel(data, 0)
            data.writeInt(if (config.renameInterface) 1 else 0)
            data.writeString(config.interfacePrefix)
            check(binder.transact(HookStatusKeys.TRANSACTION_UPDATE_PRIVILEGE_SETTINGS, data, reply, 0)) {
                "LSPosed rejected the settings transaction"
            }
            reply.readException()
        }
        lastSynced = status.systemPid to config.revision()
    }

    private suspend fun write(config: PrivilegeSettingsConfig) {
        Settings.dataStore.putBatchConfirmed(listOf(
            KeyValueEntity(SettingsKey.PRIVILEGE_SETTINGS_ENABLED).put(config.enabled),
            KeyValueEntity(SettingsKey.PRIVILEGE_SETTINGS_LIST).put(config.packages),
            KeyValueEntity(SettingsKey.PRIVILEGE_SETTINGS_INTERFACE_RENAME_ENABLED).put(config.renameInterface),
            KeyValueEntity(SettingsKey.PRIVILEGE_SETTINGS_INTERFACE_PREFIX).put(config.interfacePrefix),
        ))
    }
}
