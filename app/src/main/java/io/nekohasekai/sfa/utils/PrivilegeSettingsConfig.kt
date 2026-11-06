package io.nekohasekai.sfa.utils

import java.security.MessageDigest

/** The module protocol and the Flutter editor share the same bounded values. */
data class PrivilegeSettingsConfig(
    val enabled: Boolean,
    val packages: Set<String>,
    val renameInterface: Boolean,
    val interfacePrefix: String,
) {
    fun validate(): PrivilegeSettingsConfig {
        require(packages.size <= 10000 && packages.all { it.length <= 255 && PACKAGE.matches(it) }) {
            "Invalid application package list"
        }
        require(PREFIX.matches(interfacePrefix)) { "Interface prefix must contain 1–13 ASCII letters, digits or underscores and start with a letter or underscore" }
        return this
    }

    fun toWire(): Map<String, Any> = mapOf(
        "enabled" to enabled, "packages" to packages.sorted(),
        "renameInterface" to renameInterface, "interfacePrefix" to interfacePrefix,
    )

    fun revision(): String {
        val text = listOf(enabled.toString(), renameInterface.toString(), interfacePrefix, *packages.sorted().toTypedArray()).joinToString("\u0000")
        return MessageDigest.getInstance("SHA-256").digest(text.toByteArray()).joinToString("") { "%02x".format(it) }
    }

    companion object {
        private val PREFIX = Regex("[A-Za-z_][A-Za-z0-9_]{0,12}")
        private val PACKAGE = Regex("[A-Za-z_][A-Za-z0-9_]*(?:\\.[A-Za-z_][A-Za-z0-9_]*)*")

        fun fromWire(value: Map<*, *>): PrivilegeSettingsConfig {
            require(value.keys == setOf("enabled", "packages", "renameInterface", "interfacePrefix")) { "Invalid privilege settings" }
            val packages = value["packages"] as? List<*> ?: error("Missing application package list")
            require(packages.size <= 10000 && packages.all { it is String }) { "Invalid application package list" }
            return PrivilegeSettingsConfig(
                value["enabled"] as? Boolean ?: error("Missing VPN hiding state"),
                packages.map { it as String }.toSet(),
                value["renameInterface"] as? Boolean ?: error("Missing interface rename state"),
                value["interfacePrefix"] as? String ?: error("Missing interface prefix"),
            ).validate()
        }
    }
}
