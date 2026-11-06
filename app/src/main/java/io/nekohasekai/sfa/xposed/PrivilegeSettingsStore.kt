package io.nekohasekai.sfa.xposed

import android.os.SystemClock
import android.util.AtomicFile
import io.nekohasekai.sfa.utils.PrivilegeSettingsConfig
import java.io.File
import java.lang.reflect.Method
import java.util.concurrent.ConcurrentHashMap

object PrivilegeSettingsStore {
    private const val SETTINGS_DIR = "/data/system/sing-box"
    private const val SETTINGS_FILE = "privilege_settings.conf"

    @Volatile private var config = PrivilegeSettingsConfig(false, emptySet(), false, "wlan")
    private data class CachedUid(val at: Long, val selected: Boolean)
    private val uidCache = ConcurrentHashMap<Int, CachedUid>()
    private val appGlobalsClass by lazy { Class.forName("android.app.AppGlobals") }
    private val getPackageManagerMethod by lazy { appGlobalsClass.getMethod("getPackageManager") }
    private var getPackagesForUidMethod: Method? = null

    @Synchronized
    fun update(enabled: Boolean, packages: Set<String>, interfaceRenameEnabled: Boolean, interfacePrefix: String) {
        val next = PrivilegeSettingsConfig(enabled, packages.toSet(), interfaceRenameEnabled, interfacePrefix).validate()
        if (next == config) return
        val dir = File(SETTINGS_DIR)
        check(dir.isDirectory || dir.mkdirs()) { "Unable to create module settings directory" }
        val file = AtomicFile(File(dir, SETTINGS_FILE))
        val output = file.startWrite()
        try {
            val content = buildString {
                append("version=1\n")
                append("enabled=").append(if (next.enabled) "1" else "0").append('\n')
                append("rename=").append(if (next.renameInterface) "1" else "0").append('\n')
                append("prefix=").append(next.interfacePrefix).append('\n')
                append("packages=").append(next.packages.sorted().joinToString(",")).append('\n')
            }
            output.write(content.toByteArray())
            file.finishWrite(output)
        } catch (error: Exception) {
            file.failWrite(output)
            throw error
        }
        // Publish one complete snapshot only after the atomic write has succeeded.
        config = next
        uidCache.clear()
        HookErrorStore.i("PrivilegeSettingsStore", "Settings applied: enabled=$enabled size=${packages.size} rename=$interfaceRenameEnabled")
    }

    fun isEnabled(): Boolean = config.enabled
    fun shouldRenameInterface(): Boolean = config.renameInterface
    fun interfacePrefix(): String = config.interfacePrefix

    fun isUidSelected(uid: Int): Boolean {
        val now = SystemClock.uptimeMillis()
        val cached = uidCache[uid]
        if (cached != null && now - cached.at < 5000) return cached.selected
        val selected = getPackagesForUid(uid).any { config.packages.contains(it) }
        uidCache[uid] = CachedUid(now, selected)
        return selected
    }

    fun shouldHideUid(uid: Int): Boolean = config.enabled && isUidSelected(uid)

    private fun getPackagesForUid(uid: Int): List<String> {
        val pm = getPackageManager() ?: return emptyList()
        return try {
            val method = getPackagesForUidMethod ?: run {
                pm.javaClass.getMethod("getPackagesForUid", Int::class.javaPrimitiveType).also {
                    getPackagesForUidMethod = it
                }
            }
            val result = method.invoke(pm, uid)
            when (result) {
                is Array<*> -> result.filterIsInstance<String>()
                is List<*> -> result.filterIsInstance<String>()
                else -> emptyList()
            }
        } catch (e: Throwable) {
            HookErrorStore.e("PrivilegeSettingsStore", "getPackagesForUid failed for uid=$uid", e)
            emptyList()
        }
    }

    private fun getPackageManager(): Any? = try {
        getPackageManagerMethod.invoke(null)
    } catch (e: Throwable) {
        HookErrorStore.e("PrivilegeSettingsStore", "getPackageManager failed", e)
        null
    }
}
