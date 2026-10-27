package io.nekohasekai.sfa.flutter

internal object BoxPreferences {
    private val cards = setOf("Status", "Network speed", "Outbound mode", "Current profile", "Connections", "Proxy groups", "Memory", "Intranet IP")
    private val choices = mapOf(
        "theme" to setOf("system", "light", "dark"),
        "locale" to setOf("system", "en", "zh"),
        "colorStyle" to setOf("vivid", "tinted", "soft", "monochrome"),
        "proxySort" to setOf("Default", "Name", "Latency"),
        "proxyLayout" to setOf("Tabs", "List", "Compact"),
        "proxyCardSize" to setOf("Compact", "Standard", "Loose"),
        "proxyDensity" to setOf("Tight", "Standard", "Loose"),
        "tabAnimation" to setOf("slide", "fade"),
        "liquidGlass" to setOf("none", "standard", "ultra"),
    )

    fun validate(values: Map<String, Any?>): Map<String, Any?> {
        values.forEach { (key, value) ->
            val valid = when (key) {
                in choices -> value in choices.getValue(key)
                "floatingNavigation", "closeConnectionsOnSelect", "hideTimeoutProxies", "pureBlack", "systemColor", "customFontScale" -> value is Boolean
                "proxyColumns" -> value is Number && value.toDouble() == value.toInt().toDouble() && value.toInt() in 1..4
                "fontScale" -> value is Number && value.toDouble().isFinite() && value.toDouble() in 0.8..1.4
                "fontFamily" -> value == null || value is String && value.length in 1..128
                "accentColors" -> value is List<*> && value.size <= 64 && value.distinct().size == value.size && value.all { it is Number && it.toLong() in 0..0xffffffffL }
                "color" -> value is Number && value.toLong() in 0..0xffffffffL
                "dashboardCards", "hiddenCards" -> value is List<*> && value.all { it in cards } &&
                    value.distinct().size == value.size &&
                    if (key == "dashboardCards") "Status" in value else "Status" !in value
                else -> false
            }
            require(valid) { "Invalid interface preference: " + key }
        }
        return values
    }
}
