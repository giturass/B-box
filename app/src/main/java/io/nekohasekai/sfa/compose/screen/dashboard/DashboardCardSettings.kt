package io.nekohasekai.sfa.compose.screen.dashboard

internal val defaultDashboardCardOrder = listOf(
    CardGroup.Traffic,
    CardGroup.Debug,
    CardGroup.SystemProxy,
    CardGroup.ClashMode,
    CardGroup.Profiles,
)

/** Merge the old traffic cards at their first saved position and ignore removed cards. */
internal fun restoreDashboardCardOrder(savedNames: List<String>): List<CardGroup> {
    val savedCards = savedNames.mapNotNull { name ->
        when (name) {
            "UploadTraffic", "DownloadTraffic" -> CardGroup.Traffic
            else -> CardGroup.entries.firstOrNull { it.name == name }
        }
    }
    return (savedCards + defaultDashboardCardOrder).distinct()
}

/** Preserve visible traffic when either of the old cards was enabled. */
internal fun restoreDashboardDisabledCards(savedNames: Set<String>): Set<CardGroup> {
    val disabled = CardGroup.entries.filter { it.name in savedNames }.toMutableSet()
    if ("UploadTraffic" in savedNames && "DownloadTraffic" in savedNames) {
        disabled.add(CardGroup.Traffic)
    }
    disabled.remove(CardGroup.Profiles)
    return disabled
}
