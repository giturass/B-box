package io.nekohasekai.sfa.compose.screen.dashboard

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class DashboardCardSettingsTest {
    @Test
    fun legacyOrderMergesTrafficAtFirstPosition() {
        val restored = restoreDashboardCardOrder(
            listOf("Profiles", "DownloadTraffic", "ClashMode", "UploadTraffic", "Connections", "Debug"),
        )
        assertEquals(
            listOf(CardGroup.Profiles, CardGroup.Traffic, CardGroup.ClashMode, CardGroup.Debug, CardGroup.SystemProxy),
            restored,
        )
    }

    @Test
    fun mixedOldAndNewNamesDoNotDuplicateTraffic() {
        val restored = restoreDashboardCardOrder(listOf("Traffic", "UploadTraffic", "Traffic", "DownloadTraffic", "FutureCard"))
        assertEquals(defaultDashboardCardOrder, restored)
        assertEquals(restored, restoreDashboardCardOrder(restored.map { it.name }))
    }

    @Test
    fun emptyOrderRestoresDefaults() {
        assertEquals(defaultDashboardCardOrder, restoreDashboardCardOrder(emptyList()))
    }

    @Test
    fun eitherLegacyTrafficCardVisibleKeepsMergedCardVisible() {
        for (disabled in listOf(emptySet(), setOf("UploadTraffic"), setOf("DownloadTraffic"))) {
            assertFalse(CardGroup.Traffic in restoreDashboardDisabledCards(disabled))
        }
    }

    @Test
    fun bothLegacyTrafficCardsHiddenKeepMergedCardHidden() {
        assertEquals(
            setOf(CardGroup.Traffic, CardGroup.Debug),
            restoreDashboardDisabledCards(setOf("UploadTraffic", "DownloadTraffic", "Debug", "Connections", "Profiles")),
        )
    }

    @Test
    fun newTrafficPreferenceSurvivesReload() {
        assertEquals(setOf(CardGroup.Traffic), restoreDashboardDisabledCards(setOf("Traffic")))
        assertTrue(restoreDashboardDisabledCards(setOf("Profiles", "Connections", "FutureCard")).isEmpty())
    }

    @Test
    fun trafficAndDebugPairInEitherOrder() {
        for (order in listOf(listOf(CardGroup.Traffic, CardGroup.Debug), listOf(CardGroup.Debug, CardGroup.Traffic))) {
            assertEquals(listOf(CardRenderItem(order, true)), processCardsForRendering(order, order.toSet()))
        }
    }

    @Test
    fun hiddenOrSeparatedCardsRemainFullWidth() {
        val order = listOf(CardGroup.Traffic, CardGroup.Profiles, CardGroup.Debug)
        assertTrue(processCardsForRendering(order, order.toSet()).none { it.isRow })
        assertEquals(
            listOf(CardRenderItem(listOf(CardGroup.Traffic), false)),
            processCardsForRendering(defaultDashboardCardOrder, setOf(CardGroup.Traffic)),
        )
    }
}
