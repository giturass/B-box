package io.nekohasekai.sfa.flutter

import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class BoxSettingsTest {
    @Test
    fun validatesWholeBackupBeforeMutation() {
        val value = mapOf("perAppMode" to 2, "perAppList" to listOf("org.example.app"), "oomMemoryLimitMB" to 50)
        assertEquals(value, BoxSettings.validate(value))
        assertThrows(IllegalArgumentException::class.java) { BoxSettings.validate(value + ("oomMemoryLimitMB" to 9)) }
        assertThrows(IllegalArgumentException::class.java) { BoxSettings.validate(value + ("allowBypass" to "true")) }
        assertThrows(IllegalArgumentException::class.java) { BoxSettings.validate(value + ("serviceMode" to "VPN")) }
    }

    @Test
    fun preferencesRejectInventedFeaturesAndMissingStatusCard() {
        assertThrows(IllegalArgumentException::class.java) { BoxPreferences.validate(mapOf("clashScript" to "code")) }
        assertThrows(IllegalArgumentException::class.java) { BoxPreferences.validate(mapOf("hiddenCards" to listOf("Status"))) }
        assertThrows(IllegalArgumentException::class.java) { BoxPreferences.validate(mapOf("proxyColumns" to 1.5)) }
        BoxPreferences.validate(mapOf("theme" to "dark", "proxyColumns" to 2, "hiddenCards" to listOf("Memory")))
    }

    @Test
    fun retiredAppearanceChoicesPreserveOtherPreferences() {
        val current = mapOf("theme" to "dark", "pureBlack" to true, "proxyLayout" to "List")
        val fonts = mapOf("fontFamily" to "JetBrainsMono", "fontScale" to 1.4, "customFontScale" to true)
        for (locale in listOf("system", "en", "zh")) {
            assertEquals(current, BoxPreferences.validate(current + fonts + ("locale" to locale)))
        }
        assertEquals(current, BoxPreferences.validate(current + mapOf("fontFamily" to null, "fontScale" to 0.8)))
    }

    @Test
    fun systemHttpProxyCardLayoutSurvivesPreferenceAndBackupValidation() {
        val shown = mapOf("dashboardCards" to listOf("System HTTP proxy", "Status"), "hiddenCards" to listOf("Memory"))
        assertEquals(shown, BoxPreferences.validate(shown))
        val hidden = shown + ("hiddenCards" to listOf("Memory", "System HTTP proxy"))
        assertEquals(hidden, BoxPreferences.validate(hidden))
    }
}
