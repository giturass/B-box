package io.nekohasekai.sfa.flutter

internal object BoxPreferences {
    private val cards = setOf("Status", "Network speed", "Outbound mode", "Current profile", "Connections", "Proxy groups", "Memory")
    private val choices = mapOf(
        "theme" to setOf("system", "light", "dark"),
        "locale" to setOf("system", "en", "zh"),
        "colorStyle" to setOf("vivid", "tinted", "soft", "monochrome"),
        "proxySort" to setOf("Default", "Name", "Latency"),
        "proxyLayout" to setOf("Tabs", "List", "Compact"),
    )

    fun validate(values: Map<String, Any?>): Map<String, Any?> {
        values.forEach { (key, value) ->
            val valid = when (key) {
                in choices -> value in choices.getValue(key)
                "floatingNavigation", "closeConnectionsOnSelect" -> value is Boolean
                "proxyColumns" -> value is Number && value.toDouble() == value.toInt().toDouble() && value.toInt() in 1..4
                "fontScale" -> value is Number && value.toDouble() in listOf(0.8, 1.0, 1.1, 1.2, 1.4)
                "color" -> value is Number && value.toLong() in 0..0xffffffffL
                "dashboardCards", "hiddenCards" -> value is List<*> && value.all { it in cards } &&
                    value.distinct().size == value.size &&
                    if (key == "dashboardCards") value.toSet() == cards else "Status" !in value
                else -> false
            }
            require(valid) { "Invalid interface preference: " + key }
        }
        return values
    }
}
