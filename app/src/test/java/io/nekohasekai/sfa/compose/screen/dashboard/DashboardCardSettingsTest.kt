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
    fun separatedStatisticsCardsPairAtTheirFirstPosition() {
        for (pair in listOf(listOf(CardGroup.Traffic, CardGroup.Debug), listOf(CardGroup.Debug, CardGroup.Traffic))) {
            val order = listOf(CardGroup.SystemProxy, pair[0], CardGroup.Profiles, pair[1], CardGroup.ClashMode)
            assertEquals(
                listOf(
                    CardRenderItem(listOf(CardGroup.SystemProxy), false),
                    CardRenderItem(pair, true),
                    CardRenderItem(listOf(CardGroup.Profiles), false),
                    CardRenderItem(listOf(CardGroup.ClashMode), false),
                ),
                processCardsForRendering(order, order.toSet()),
            )
        }
    }

    @Test
    fun hidingEitherStatisticsCardLeavesTheOtherFullWidth() {
        val order = listOf(CardGroup.Traffic, CardGroup.Profiles, CardGroup.Debug)
        for (hidden in listOf(CardGroup.Traffic, CardGroup.Debug)) {
            val visible = order.filter { it != hidden }
            assertEquals(
                visible.map { CardRenderItem(listOf(it), false) },
                processCardsForRendering(order, visible.toSet()),
            )
        }
    }
}
