package io.nekohasekai.sfa.flutter

import io.nekohasekai.sfa.compose.screen.tools.TailscaleStatusViewModel
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.terminal.DEFAULT_SSH_TERMINAL_TYPE
import io.nekohasekai.sfa.terminal.TailscaleSSHPresentedSession
import io.nekohasekai.sfa.terminal.TailscaleSSHSessionStore
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.launch
import java.io.Closeable

/** Flutter owns presentation; the process-wide Ghostty store owns SSH. */
internal class BoxSSH(scope: CoroutineScope, private val tailscale: TailscaleStatusViewModel,
    private val emit: (String, Any?) -> Unit) : Closeable {
    private val store = TailscaleSSHSessionStore
    private val watch = scope.launch {
        store.state.collectLatest { state ->
            emit("terminal", snapshot())
            coroutineScope {
                state.sessions.forEach { session ->
                    launch { session.phase.collect { emit("terminal", snapshot()) } }
                    launch { session.banner.collect { emit("terminal", snapshot()) } }
                    launch { session.terminalSession.title.collect { emit("terminal", snapshot()) } }
                    launch { session.terminalSession.workingDirectory.collect { emit("terminal", snapshot()) } }
                    launch { session.terminalSession.progress.collect { emit("terminal", snapshot()) } }
                }
            }
        }
    }

    fun snapshot(): Map<String, Any?> = mapOf(
        "activeId" to store.state.value.activeSessionId,
        "sessions" to store.state.value.sessions.map { session ->
            mapOf("id" to session.id, "name" to (session.terminalSession.title.value ?: session.presentedSession.peerHostName),
                "host" to session.presentedSession.peerHostName, "username" to session.presentedSession.username,
                "phase" to session.phase.value.name, "banner" to session.banner.value,
                "directory" to session.terminalSession.workingDirectory.value,
                "progress" to session.terminalSession.progress.value?.let { mapOf("state" to it.state.name, "percent" to it.percent) })
        },
    )

    fun invoke(method: String, args: Map<String, Any?>): Any? {
        fun string(key: String) = args[key] as? String ?: error("Missing terminal argument: $key")
        when (method) {
            "ssh.snapshot" -> return snapshot()
            "ssh.options", "ssh.start" -> {
                val endpoint = string("endpoint")
                val peer = tailscale.peer(endpoint, string("peer")) ?: error("Peer no longer exists")
                require(peer.sshHostKeys.isNotEmpty()) { "Peer has no authenticated SSH host keys" }
                if (method == "ssh.options") return mapOf(
                    "username" to (Settings.tailscaleSSHRememberedUsernames[peer.stableID] ?: "root"),
                    "terminalType" to (Settings.tailscaleSSHRememberedTerminalTypes[peer.stableID] ?: DEFAULT_SSH_TERMINAL_TYPE),
                    "remember" to Settings.tailscaleSSHQuickConnectPeers.contains(peer.stableID),
                )
                val username = string("username").trim()
                val terminalType = (args["terminalType"] as? String).orEmpty().trim().ifEmpty { DEFAULT_SSH_TERMINAL_TYPE }
                require(username.isNotEmpty() && username.length <= 256 && username.none { it.isISOControl() }) { "Invalid SSH username" }
                require(terminalType.length <= 128 && terminalType.matches(Regex("[A-Za-z0-9._+-]+"))) { "Invalid terminal type" }
                val address = peer.tailscaleIPs.firstOrNull() ?: error("Peer has no address")
                Settings.tailscaleSSHRememberedUsernames = Settings.tailscaleSSHRememberedUsernames.toMutableMap().apply { put(peer.stableID, username) }
                Settings.tailscaleSSHRememberedTerminalTypes = Settings.tailscaleSSHRememberedTerminalTypes.toMutableMap().apply { put(peer.stableID, terminalType) }
                Settings.tailscaleSSHQuickConnectPeers = Settings.tailscaleSSHQuickConnectPeers.toMutableSet().apply {
                    if (args["remember"] == true) add(peer.stableID) else remove(peer.stableID)
                }
                store.addSession(TailscaleSSHPresentedSession(endpoint, peer.hostName, address, username, terminalType, peer.sshHostKeys))
            }
            "ssh.select" -> {
                val id = string("id")
                require(store.state.value.sessions.any { it.id == id }) { "Session no longer exists" }
                store.switchSession(id)
            }
            "ssh.duplicate" -> store.duplicateCurrentSession()
            "ssh.close" -> store.removeSession(string("id"))
            else -> error("Unknown terminal operation")
        }
        return snapshot()
    }

    override fun close() { watch.cancel() }
}
