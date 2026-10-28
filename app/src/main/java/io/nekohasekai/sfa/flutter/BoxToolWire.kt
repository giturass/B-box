package io.nekohasekai.sfa.flutter

import io.nekohasekai.sfa.backend.tools.TailscalePeerData
import io.nekohasekai.sfa.backend.tools.TailscaleUserGroupData
import io.nekohasekai.sfa.backend.tools.TailscaleEndpointData
import io.nekohasekai.sfa.backend.tools.TailscaleStatusState
import io.nekohasekai.sfa.backend.tools.OpenConnectAuthFormChoiceData
import io.nekohasekai.sfa.backend.tools.OpenConnectAuthFormFieldData
import io.nekohasekai.sfa.backend.tools.OpenConnectAuthFormData
import io.nekohasekai.sfa.backend.tools.OpenConnectBrowserRequestData
import io.nekohasekai.sfa.backend.tools.OpenConnectAuthChallengeData
import io.nekohasekai.sfa.backend.tools.OpenConnectBrowserCookieData
import io.nekohasekai.sfa.backend.tools.OpenConnectBrowserResultData
import io.nekohasekai.sfa.backend.tools.OpenConnectTunnelInfoData
import io.nekohasekai.sfa.backend.tools.OpenConnectEndpointData
import io.nekohasekai.sfa.backend.tools.OpenConnectStatusState
import io.nekohasekai.sfa.backend.tools.OpenVPNChallengeData
import io.nekohasekai.sfa.backend.tools.OpenVPNTunnelInfoData
import io.nekohasekai.sfa.backend.tools.OpenVPNEndpointData
import io.nekohasekai.sfa.backend.tools.OpenVPNStatusState
import io.nekohasekai.sfa.backend.tools.TaildropFileData
import io.nekohasekai.sfa.backend.tools.TaildropReceivingFileData
import io.nekohasekai.sfa.backend.tools.TaildropInboxState
import io.nekohasekai.sfa.backend.tools.TailscalePingState
import io.nekohasekai.sfa.backend.tools.STUNTestState
import io.nekohasekai.sfa.backend.tools.NetworkQualityState
import io.nekohasekai.sfa.backend.usbip.UsbSharedInterfaceData
import io.nekohasekai.sfa.backend.usbip.UsbSharedDeviceData
import io.nekohasekai.sfa.backend.usbip.UsbipServerData
import io.nekohasekai.sfa.backend.usbip.USBIPStatusState

internal fun TailscalePeerData.toWire(): Map<String, Any?> = mapOf(
    "id" to id,
    "stableID" to stableID,
    "hostName" to hostName,
    "dnsName" to dnsName,
    "os" to os,
    "tailscaleIPs" to tailscaleIPs,
    "sshHostKeys" to sshHostKeys,
    "online" to online,
    "exitNode" to exitNode,
    "exitNodeOption" to exitNodeOption,
    "shareeNode" to shareeNode,
    "expired" to expired,
    "active" to active,
    "rxBytes" to rxBytes,
    "txBytes" to txBytes,
    "keyExpiry" to keyExpiry,
    "lastSeen" to lastSeen,
    "canReceiveFiles" to canReceiveFiles,
)

internal fun TailscaleUserGroupData.toWire(): Map<String, Any?> = mapOf(
    "id" to id,
    "loginName" to loginName,
    "displayName" to displayName,
    "profilePicURL" to profilePicURL,
    "peers" to peers.map { it.toWire() },
)

internal fun TailscaleEndpointData.toWire(): Map<String, Any?> = mapOf(
    "endpointTag" to endpointTag,
    "backendState" to backendState,
    "stateText" to stateText,
    "authURL" to authURL,
    "networkName" to networkName,
    "magicDNSSuffix" to magicDNSSuffix,
    "selfPeer" to selfPeer?.toWire(),
    "exitNode" to exitNode?.toWire(),
    "userGroups" to userGroups.map { it.toWire() },
    "keyAuth" to keyAuth,
    "canShareFiles" to canShareFiles,
    "waitingFileCount" to waitingFileCount,
    "receivingFileCount" to receivingFileCount,
    "unreadFileCount" to unreadFileCount,
)

internal fun TailscaleStatusState.toWire(): Map<String, Any?> = mapOf(
    "endpoints" to endpoints.map { it.toWire() },
    "isSubscribed" to isSubscribed,
    "hasUpdate" to hasUpdate,
)

internal fun OpenConnectAuthFormChoiceData.toWire(): Map<String, Any?> = mapOf(
    "value" to value,
    "label" to label,
)

internal fun OpenConnectAuthFormFieldData.toWire(): Map<String, Any?> = mapOf(
    "submissionKey" to submissionKey,
    "name" to name,
    "label" to label,
    "kind" to kind,
    "value" to value,
    "options" to options.map { it.toWire() },
)

internal fun OpenConnectAuthFormData.toWire(): Map<String, Any?> = mapOf(
    "fields" to fields.map { it.toWire() },
)

internal fun OpenConnectBrowserRequestData.toWire(): Map<String, Any?> = mapOf(
    "url" to url,
    "finalURL" to finalURL,
    "cacheID" to cacheID,
    "cookieNames" to cookieNames,
    "earlyCookieNames" to earlyCookieNames,
    "headerNames" to headerNames,
    "callbackURLPrefixes" to callbackURLPrefixes,
)

internal fun OpenConnectAuthChallengeData.toWire(): Map<String, Any?> = mapOf(
    "id" to id,
    "banner" to banner,
    "message" to message,
    "error" to error,
    "form" to form?.toWire(),
    "browser" to browser?.toWire(),
)

internal fun OpenConnectBrowserCookieData.toWire(): Map<String, Any?> = mapOf(
    "name" to name,
    "value" to value,
)

internal fun OpenConnectBrowserResultData.toWire(): Map<String, Any?> = mapOf(
    "finalURL" to finalURL,
    "cookies" to cookies.map { it.toWire() },
)

internal fun OpenConnectTunnelInfoData.toWire(): Map<String, Any?> = mapOf(
    "server" to server,
    "flavor" to flavor,
    "transport" to transport,
    "ipv4" to ipv4,
    "ipv6" to ipv6,
    "dns" to dns,
    "mtu" to mtu,
    "connectedSince" to connectedSince,
)

internal fun OpenConnectEndpointData.toWire(): Map<String, Any?> = mapOf(
    "endpointTag" to endpointTag,
    "state" to state,
    "stateText" to stateText,
    "authChallenge" to authChallenge?.toWire(),
    "error" to error,
    "tunnelInfo" to tunnelInfo?.toWire(),
)

internal fun OpenConnectStatusState.toWire(): Map<String, Any?> = mapOf(
    "endpoints" to endpoints.map { it.toWire() },
    "isSubscribed" to isSubscribed,
)

internal fun OpenVPNChallengeData.toWire(): Map<String, Any?> = mapOf(
    "id" to id,
    "kind" to kind,
    "username" to username,
    "message" to message,
    "url" to url,
    "secretMessage" to secretMessage,
    "echo" to echo,
    "previousError" to previousError,
    "deadline" to deadline,
)

internal fun OpenVPNTunnelInfoData.toWire(): Map<String, Any?> = mapOf(
    "server" to server,
    "network" to network,
    "cipher" to cipher,
    "ipv4" to ipv4,
    "ipv6" to ipv6,
    "dns" to dns,
    "mtu" to mtu,
    "connectedSince" to connectedSince,
)

internal fun OpenVPNEndpointData.toWire(): Map<String, Any?> = mapOf(
    "endpointTag" to endpointTag,
    "state" to state,
    "stateText" to stateText,
    "challenge" to challenge?.toWire(),
    "error" to error,
    "tunnelInfo" to tunnelInfo?.toWire(),
)

internal fun OpenVPNStatusState.toWire(): Map<String, Any?> = mapOf(
    "endpoints" to endpoints.map { it.toWire() },
    "isSubscribed" to isSubscribed,
)

internal fun TaildropFileData.toWire(): Map<String, Any?> = mapOf(
    "name" to name,
    "size" to size,
    "senderName" to senderName,
    "modifiedAt" to modifiedAt,
)

internal fun TaildropReceivingFileData.toWire(): Map<String, Any?> = mapOf(
    "name" to name,
    "size" to size,
    "receivedBytes" to receivedBytes,
    "senderID" to senderID,
    "senderName" to senderName,
)

internal fun TaildropInboxState.toWire(): Map<String, Any?> = mapOf(
    "files" to files.map { it.toWire() },
    "receiving" to receiving.map { it.toWire() },
    "isSubscribed" to isSubscribed,
    "hasUpdate" to hasUpdate,
)

internal fun TailscalePingState.toWire(): Map<String, Any?> = mapOf(
    "isRunning" to isRunning,
    "hasResult" to hasResult,
    "latencyMs" to latencyMs,
    "isDirect" to isDirect,
    "derpRegionCode" to derpRegionCode,
    "endpoint" to endpoint,
    "peerRelay" to peerRelay,
    "error" to error,
    "latencyHistory" to latencyHistory.map { it.toDouble() },
)

internal fun STUNTestState.toWire(): Map<String, Any?> = mapOf(
    "phase" to phase,
    "externalAddr" to externalAddr,
    "latencyMs" to latencyMs,
    "natMapping" to natMapping,
    "natFiltering" to natFiltering,
    "natTypeSupported" to natTypeSupported,
    "isRunning" to isRunning,
    "server" to server,
    "selectedOutbound" to selectedOutbound,
)

internal fun NetworkQualityState.toWire(): Map<String, Any?> = mapOf(
    "phase" to phase,
    "idleLatencyMs" to idleLatencyMs,
    "downloadCapacity" to downloadCapacity,
    "uploadCapacity" to uploadCapacity,
    "downloadRPM" to downloadRPM,
    "uploadRPM" to uploadRPM,
    "downloadCapacityAccuracy" to downloadCapacityAccuracy,
    "uploadCapacityAccuracy" to uploadCapacityAccuracy,
    "downloadRPMAccuracy" to downloadRPMAccuracy,
    "uploadRPMAccuracy" to uploadRPMAccuracy,
    "isRunning" to isRunning,
    "configURL" to configURL,
    "serial" to serial,
    "http3" to http3,
    "maxRuntime" to maxRuntime.seconds,
    "selectedOutbound" to selectedOutbound,
    "showMeteredWarning" to showMeteredWarning,
)

internal fun UsbSharedInterfaceData.toWire(): Map<String, Any?> = mapOf(
    "interfaceClass" to interfaceClass,
    "interfaceSubClass" to interfaceSubClass,
    "interfaceProtocol" to interfaceProtocol,
)

internal fun UsbSharedDeviceData.toWire(): Map<String, Any?> = mapOf(
    "busId" to busId,
    "stableId" to stableId,
    "backend" to backend,
    "state" to state,
    "deviceId" to deviceId,
    "busNum" to busNum,
    "devNum" to devNum,
    "speed" to speed,
    "vendorId" to vendorId,
    "productId" to productId,
    "bcdDevice" to bcdDevice,
    "deviceClass" to deviceClass,
    "deviceSubClass" to deviceSubClass,
    "deviceProtocol" to deviceProtocol,
    "configurationValue" to configurationValue,
    "numConfigurations" to numConfigurations,
    "serial" to serial,
    "product" to product,
    "interfaces" to interfaces.map { it.toWire() },
)

internal fun UsbipServerData.toWire(): Map<String, Any?> = mapOf(
    "serverTag" to serverTag,
    "devices" to devices.map { it.toWire() },
)

internal fun USBIPStatusState.toWire(): Map<String, Any?> = mapOf(
    "servers" to servers.map { it.toWire() },
    "isSubscribed" to isSubscribed,
)
