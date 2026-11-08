package io.nekohasekai.sfa.flutter

import io.nekohasekai.sfa.constant.SettingsKey
import io.nekohasekai.sfa.database.preference.KeyValueEntity

/** Shared validation for individual edits and complete backup imports. */
internal object BoxSettings {
    private val booleanKeys = mapOf(
        "perAppEnabled" to SettingsKey.PER_APP_PROXY_ENABLED,
        "perAppManaged" to SettingsKey.PER_APP_PROXY_MANAGED_MODE,
        "allowBypass" to SettingsKey.ALLOW_BYPASS,
        "autoRedirect" to SettingsKey.AUTO_REDIRECT,
        "dynamicNotification" to SettingsKey.DYNAMIC_NOTIFICATION,
        "disableDeprecatedWarnings" to SettingsKey.DISABLE_DEPRECATED_WARNINGS,
        "oomKillerEnabled" to SettingsKey.OOM_KILLER_ENABLED,
        "oomKillerDisabled" to SettingsKey.OOM_KILLER_DISABLED,
        "powerReportEnabled" to SettingsKey.POWER_REPORT_ENABLED,
    )

    fun validate(values: Map<String, Any?>): Map<String, Any?> {
        values.forEach { (key, value) ->
            val valid = when (key) {
                in booleanKeys -> value is Boolean
                "perAppMode" -> value is Number && value.toDouble() in listOf(1.0, 2.0)
                "perAppList" -> value is List<*> && value.size <= 10000 && value.all { it is String && it.isNotBlank() && it.length < 512 }
                "oomMemoryLimitMB" -> value is Number && value.toDouble() == value.toInt().toDouble() && value.toInt() in 10..4096
                "queryMode" -> value in listOf("ROOT", "SHIZUKU")
                else -> false
            }
            require(valid) { "Invalid service setting: $key" }
        }
        return values
    }

    fun entities(values: Map<String, Any?>, managedApps: Set<String>? = null): List<KeyValueEntity> {
        validate(values)
        return values.map { (key, value) ->
            when (key) {
                in booleanKeys -> KeyValueEntity(booleanKeys.getValue(key)).put(value as Boolean)
                "perAppMode" -> KeyValueEntity(SettingsKey.PER_APP_PROXY_MODE).put((value as Number).toLong())
                "perAppList" -> KeyValueEntity(SettingsKey.PER_APP_PROXY_LIST).put((value as List<*>).map { it as String }.toSet())
                "oomMemoryLimitMB" -> KeyValueEntity(SettingsKey.OOM_MEMORY_LIMIT_MB).put((value as Number).toLong())
                "queryMode" -> KeyValueEntity(SettingsKey.PER_APP_PROXY_PACKAGE_QUERY_MODE).put(value as String)
                else -> error("Unknown setting: $key")
            }
        } + if (managedApps == null) emptyList() else listOf(KeyValueEntity(SettingsKey.PER_APP_PROXY_MANAGED_LIST).put(managedApps))
    }
}
