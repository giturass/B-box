package io.nekohasekai.sfa.flutter

import android.net.Uri
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.lifecycleScope
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.backend.tools.MaxRuntimeOption
import io.nekohasekai.sfa.backend.tools.NetworkQualityViewModel
import io.nekohasekai.sfa.backend.tools.OpenConnectStatusViewModel
import io.nekohasekai.sfa.backend.tools.OpenVPNStatusViewModel
import io.nekohasekai.sfa.backend.tools.STUNTestViewModel
import io.nekohasekai.sfa.backend.tools.TaildropFiles
import io.nekohasekai.sfa.backend.tools.TaildropSendManager
import io.nekohasekai.sfa.backend.tools.TaildropViewModel
import io.nekohasekai.sfa.backend.tools.TailscalePingViewModel
import io.nekohasekai.sfa.backend.tools.TailscaleStatusViewModel
import io.nekohasekai.sfa.backend.usbip.USBIPStatusViewModel
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.usbip.USBIPManager
import io.nekohasekai.sfa.utils.AppLifecycleObserver
import io.nekohasekai.sfa.utils.CommandTarget
import io.nekohasekai.sfa.utils.CommandClient
import io.nekohasekai.sfa.utils.RemoteControlManager
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.Closeable

internal class BoxTools(private val activity: BoxActivity, private val emit: (String, Any?) -> Unit) : Closeable {
    private val scope = activity.lifecycleScope
    private val provider = ViewModelProvider(activity)
    val tailscale = provider[TailscaleStatusViewModel::class.java]
    val openConnect = provider[OpenConnectStatusViewModel::class.java]
    private val openVPN = provider[OpenVPNStatusViewModel::class.java]
    private val usb = provider[USBIPStatusViewModel::class.java]
    private val stun = provider[STUNTestViewModel::class.java]
    private val quality = provider[NetworkQualityViewModel::class.java]
    private val ping = provider[TailscalePingViewModel::class.java]
    private val inbox = provider[TaildropViewModel::class.java]
    private val jobs = mutableListOf<Job>()
    private val usbController = BoxUsb(activity)
    private val ssh = BoxSSH(scope, tailscale, emit)
    private val terminalSettings = BoxTerminalSettings(activity)
    private var inboxTag: String? = null
    private var sessionKey: Long? = null
    private val outbounds = CommandClient(scope, CommandClient.ConnectionType.Outbounds, object : CommandClient.Handler {
        override fun updateOutbounds(outbounds: List<io.nekohasekai.libbox.OutboundGroupItem>) {
            emit("outbounds", outbounds.map { mapOf("tag" to it.tag, "type" to it.type) })
        }
    })

    val running: Boolean
        get() = if (CommandTarget.isRemote) RemoteControlManager.isConnected.value else activity.serviceStatus.value == Status.Started

    init {
        jobs += scope.launch {
            combine(
                activity.serviceStatus, AppLifecycleObserver.isForeground,
                RemoteControlManager.remoteServer, RemoteControlManager.isConnected,
            ) { status, foreground, remote, connected ->
                val available = if (remote != null) connected else status == Status.Started
                Triple(foreground && available, available, remote?.id ?: 0L)
            }.distinctUntilChanged().collect { (connected, available, target) ->
                tailscale.cancel()
                openConnect.cancel()
                openVPN.cancel()
                usb.cancel()
                outbounds.disconnect()
                if (sessionKey != target || !available) {
                    stun.onVpnDisconnected()
                    quality.onVpnDisconnected()
                    ping.stopPing()
                    inbox.cancel()
                    inboxTag = null
                }
                sessionKey = target
                if (connected) {
                    outbounds.connect()
                    tailscale.subscribe()
                    openConnect.subscribe()
                    openVPN.subscribe()
                    usb.subscribe()
                    inboxTag?.let { inbox.subscribe(it) }
                } else {
                    inbox.cancel()
                }
            }
        }
        jobs += scope.launch { tailscale.uiState.collect { emit("tools", snapshot()) } }
        jobs += scope.launch { openConnect.uiState.collect { emit("tools", snapshot()) } }
        jobs += scope.launch { openVPN.uiState.collect { emit("tools", snapshot()) } }
        jobs += scope.launch { usb.uiState.collect { emit("tools", snapshot()) } }
        jobs += scope.launch { stun.uiState.collect { emit("stun", it.toWire() + mapOf("mapping" to Libbox.formatNATMapping(it.natMapping), "filtering" to Libbox.formatNATFiltering(it.natFiltering))) } }
        jobs += scope.launch { quality.uiState.collect { emit("quality", it.toWire()) } }
        jobs += scope.launch { ping.uiState.collect { emit("ping", it.toWire()) } }
        jobs += scope.launch { inbox.uiState.collect { emit("inbox", it.toWire()) } }
        jobs += scope.launch { TaildropSendManager.sessions.collect { sessions ->
            emit("transfers", sessions.map { session ->
                mapOf("id" to session.id, "endpointTag" to session.endpointTag, "peerName" to session.peerName,
                    "finished" to session.finished, "error" to session.errorMessage,
                    "files" to session.files.map { mapOf("name" to it.name, "size" to it.size, "sentBytes" to it.sentBytes, "completed" to it.completed) })
            })
        } }
        jobs += scope.launch { USBIPManager.state.collect { state ->
            emit("usbDevices", state.devices.map {
                mapOf("id" to it.deviceId, "server" to it.serverTag, "label" to it.label, "state" to it.state.name, "error" to it.error)
            })
        } }
    }

    fun snapshot(): Map<String, Any?> = mapOf(
        "tailscale" to tailscale.uiState.value.endpoints.map { it.toWire() },
        "openconnect" to openConnect.uiState.value.endpoints.map { it.toWire() },
        "openvpn" to openVPN.uiState.value.endpoints.map { it.toWire() },
        "usbip" to usb.uiState.value.servers.map { it.toWire() },
    )

    fun diagnostics(): Map<String, Any?> = mapOf("quality" to quality.uiState.value.toWire(), "stun" to stun.uiState.value.toWire())

    suspend fun invoke(method: String, args: Map<String, Any?>): Any? {
        if (method.startsWith("terminal.")) return terminalSettings.invoke(method, args)
        if (method.startsWith("ssh.")) return ssh.invoke(method, args)
        fun string(key: String): String = args[key] as? String ?: error("Missing argument: " + key)
        when (method) {
            "tools.snapshot" -> return snapshot()
            "stun.start" -> {
                require(!stun.uiState.value.isRunning) { "A STUN test is already running" }
                stun.updateServer(string("server"))
                stun.selectOutbound(args["outbound"] as? String ?: "")
                stun.startTest(running)
            }
            "stun.stop" -> stun.cancelTest()
            "quality.start" -> {
                require(!quality.uiState.value.isRunning) { "A network quality test is already running" }
                quality.updateConfigURL(BoxProfiles.validateUrl(string("url")))
                quality.selectOutbound(args["outbound"] as? String ?: "")
                quality.setSerial(args["serial"] == true)
                quality.setHttp3(args["http3"] == true)
                quality.setMaxRuntime(if ((args["seconds"] as? Number)?.toInt() == 60) MaxRuntimeOption.SIXTY else MaxRuntimeOption.THIRTY)
                quality.requestStartTest(activity, running)
            }
            "quality.confirm" -> quality.confirmMeteredStart(running)
            "quality.dismiss" -> quality.dismissMeteredWarning()
            "quality.stop" -> quality.cancelTest()
            "tailscale.exit" -> {
                val endpoint = tailscale.endpoint(string("endpoint")) ?: error("Endpoint no longer exists")
                val stableID = string("peer")
                require(stableID.isEmpty() || endpoint.userGroups.any { group -> group.peers.any { it.stableID == stableID && it.exitNodeOption } }) { "Not an available exit node" }
                withContext(Dispatchers.IO) { CommandTarget.standaloneClient().setTailscaleExitNode(endpoint.endpointTag, stableID) }
            }
            "tailscale.logout" -> withContext(Dispatchers.IO) { CommandTarget.standaloneClient().tailscaleLogout(string("endpoint")) }
            "tailscale.ping" -> {
                val peer = tailscale.peer(string("endpoint"), string("peer")) ?: error("Peer no longer exists")
                ping.stopPing()
                ping.startPing(string("endpoint"), peer.tailscaleIPs.firstOrNull() ?: error("Peer has no address"))
            }
            "tailscale.stopPing" -> ping.stopPing()
            "taildrop.subscribe" -> {
                inboxTag = string("endpoint")
                inbox.subscribe(inboxTag!!)
                inbox.markInboxRead(inboxTag!!)
            }
            "taildrop.unsubscribe" -> { inboxTag = null; inbox.cancel() }
            "taildrop.delete" -> {
                val name = string("name")
                require(inbox.uiState.value.files.any { it.name == name }) { "File no longer exists" }
                inbox.delete(string("endpoint"), name)
            }
            "taildrop.cancelReceive" -> inbox.cancelReceiving(string("endpoint"), string("sender"), string("name"))
            "taildrop.save" -> {
                val name = string("name")
                require(inbox.uiState.value.files.any { it.name == name }) { "File no longer exists" }
                val uri = activity.pickDocument(name) ?: return false
                val file = TaildropFiles.materialize(string("endpoint"), name)
                withContext(Dispatchers.IO) {
                    activity.contentResolver.openOutputStream(uri, "wt")?.use { output -> file.inputStream().use { it.copyTo(output) } }
                        ?: error("Unable to save received file")
                }
                return true
            }
            "share.discard" -> activity.discardShare(string("token"))
            "share.send" -> {
                val endpoint = string("endpoint")
                val peer = tailscale.peer(endpoint, string("peer")) ?: error("Peer no longer exists")
                require(peer.canReceiveFiles && peer.online) { "Peer is not available for file transfer" }
                val files = withContext(Dispatchers.IO) { activity.openShare(string("token")) }
                return TaildropSendManager.send(endpoint, peer.stableID, peer.displayName, files)
            }
            "taildrop.send" -> {
                val endpoint = string("endpoint")
                val peer = tailscale.peer(endpoint, string("peer")) ?: error("Peer no longer exists")
                require(peer.canReceiveFiles) { "This peer cannot receive files" }
                val uri = activity.pickDocument() ?: return null
                val files = withContext(Dispatchers.IO) { TaildropSendManager.openURIs(listOf(uri)) }
                return TaildropSendManager.send(endpoint, peer.stableID, peer.displayName, files)
            }
            "taildrop.cancelSend" -> TaildropSendManager.cancel((args["id"] as Number).toLong())
            "taildrop.dismissSend" -> TaildropSendManager.dismiss((args["id"] as Number).toLong())
            "openconnect.submit" -> {
                @Suppress("UNCHECKED_CAST")
                val values = args["values"] as Map<String, String>
                val error = openConnect.submitAuthFormResponse(string("endpoint"), string("challenge"), values)
                if (error != null) kotlin.error(error)
            }
            "openconnect.cancel" -> openConnect.cancelAuthChallenge(string("endpoint"), string("challenge"))
            "openvpn.submit" -> {
                val error = openVPN.submitChallengeResponse(string("endpoint"), string("challenge"),
                    args["username"] as? String ?: "", args["password"] as? String ?: "", args["secret"] as? String ?: "")
                if (error != null) kotlin.error(error)
            }
            "openvpn.cancel" -> withContext(Dispatchers.IO) {
                CommandTarget.standaloneClient().cancelOpenVPNChallenge(string("endpoint"), string("challenge"))
            }
            "usb.list" -> return usbController.devices()
            "usb.attach" -> {
                require(usb.uiState.value.servers.any { it.serverTag == string("server") }) { "USBIP server no longer exists" }
                usbController.attach(string("server"), string("device"))
            }
            "usb.detach" -> USBIPManager.detach(string("id"))
            else -> throw UnsupportedOperationException("Unknown B-box tool: " + method)
        }
        return null
    }

    override fun close() {
        jobs.forEach { it.cancel() }
        tailscale.cancel()
        openConnect.cancel()
        openVPN.cancel()
        usb.cancel()
        outbounds.disconnect()
        inbox.cancel()
        ping.stopPing()
        stun.cancelTest()
        quality.cancelTest()
        usbController.close()
        ssh.close()
    }
}
