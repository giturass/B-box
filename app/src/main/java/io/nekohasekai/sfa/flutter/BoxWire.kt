package io.nekohasekai.sfa.flutter

import io.nekohasekai.sfa.backend.model.Connection
import io.nekohasekai.sfa.backend.model.Group
import io.nekohasekai.sfa.backend.dashboard.DashboardUiState
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.utils.RemoteControlManager

internal fun DashboardUiState.toWire(): Map<String, Any?> = mapOf(
    "status" to serviceStatus.name,
    "selectedProfileId" to Settings.selectedProfile,
    "startedAt" to (RemoteControlManager.startedAt.value ?: serviceStartTime),
    "memory" to memory,
    "goroutines" to goroutines,
    "uploadTotal" to uplinkTotal,
    "downloadTotal" to downlinkTotal,
    "trafficAvailable" to trafficVisible,
    "connections" to connectionsCount,
    "modes" to if (clashModeVisible) clashModes else emptyList<String>(),
    "mode" to selectedClashMode,
    "systemProxyAvailable" to (systemProxyVisible && RemoteControlManager.remoteServer.value == null),
    "systemProxy" to systemProxyEnabled,
    "remote" to RemoteControlManager.remoteServer.value?.displayName,
    "remoteConnected" to RemoteControlManager.isConnected.value,
    "deprecated" to deprecatedNotes.map { mapOf("message" to it.message, "url" to it.migrationLink) },
)

internal fun Group.toWire(): Map<String, Any?> = mapOf(
    "tag" to tag,
    "type" to displayType,
    "selectable" to selectable,
    "selected" to selected,
    "expanded" to isExpand,
    "items" to items.map { mapOf("tag" to it.tag, "type" to it.displayType, "delay" to it.urlTestDelay, "testedAt" to it.urlTestTime) },
)

internal fun Connection.toWire(): Map<String, Any?> = mapOf(
    "id" to id, "inbound" to inbound, "inboundType" to inboundType,
    "network" to network, "source" to source, "destination" to destination,
    "domain" to domain, "displayDestination" to displayDestination,
    "protocol" to protocolName, "user" to user, "createdAt" to createdAt,
    "closedAt" to closedAt, "upload" to upload, "download" to download,
    "uploadTotal" to uploadTotal, "downloadTotal" to downloadTotal,
    "rule" to rule, "outbound" to outbound, "outboundType" to outboundType,
    "chain" to chain, "process" to processInfo?.let {
        mapOf("id" to it.processId, "path" to it.processPath, "user" to it.userName, "packages" to it.packageNames)
    },
)
