package io.nekohasekai.sfa.flutter

import io.nekohasekai.libbox.TailscaleSSHHandler
import io.nekohasekai.libbox.TailscaleSSHOptions
import io.nekohasekai.libbox.TailscaleSSHSession
import io.nekohasekai.sfa.compose.screen.tools.TailscaleStatusViewModel
import io.nekohasekai.sfa.ktx.toStringIterator
import io.nekohasekai.sfa.utils.CommandTarget
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.Closeable
import java.util.concurrent.ConcurrentHashMap

internal class BoxSSH(
    private val scope: CoroutineScope,
    private val tailscale: TailscaleStatusViewModel,
    private val emit: (String, Any?) -> Unit,
) : Closeable {
    private class Handle(val client: io.nekohasekai.libbox.CommandClient) {
        @Volatile var session: TailscaleSSHSession? = null
        var closed = false

        @Synchronized
        fun set(value: TailscaleSSHSession) {
            if (closed) value.close() else session = value
        }

        @Synchronized
        fun close() {
            if (closed) return
            closed = true
            runCatching { session?.close() }
            runCatching { client.disconnect() }
            session = null
        }
    }

    // Cleanup must survive Activity lifecycle cancellation.
    private val cleanupScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val sessions = ConcurrentHashMap<String, Handle>()

    suspend fun invoke(method: String, args: Map<String, Any?>): Any? {
        val id = args["id"] as? String ?: error("Missing terminal ID")
        fun number(key: String, fallback: Int) = ((args[key] as? Number)?.toInt() ?: fallback).coerceIn(1, 10000)
        when (method) {
            "ssh.start" -> {
                require(!sessions.containsKey(id)) { "Terminal already exists" }
                val endpoint = args["endpoint"] as? String ?: error("Missing endpoint")
                val peerID = args["peer"] as? String ?: error("Missing peer")
                val peer = tailscale.peer(endpoint, peerID) ?: error("Peer no longer exists")
                require(peer.sshHostKeys.isNotEmpty()) { "Peer has no authenticated SSH host keys" }
                val username = (args["username"] as? String)?.trim().orEmpty()
                require(username.isNotEmpty()) { "SSH username is required" }
                val client = CommandTarget.ownedStandaloneClient()
                val handle = Handle(client)
                sessions[id] = handle
                try {
                    withContext(Dispatchers.IO) {
                        val options = TailscaleSSHOptions().apply {
                            endpointTag = endpoint
                            peerAddress = peer.tailscaleIPs.firstOrNull() ?: error("Peer has no address")
                            this.username = username
                            terminalType = "xterm-256color"
                            columns = number("columns", 80)
                            rows = number("rows", 24)
                            widthPixels = 0
                            heightPixels = 0
                            hostKeys = peer.sshHostKeys.toStringIterator()
                            forwardAgent = false
                        }
                        handle.set(client.startTailscaleSSHSession(options, object : TailscaleSSHHandler {
                            override fun onReady() = send(id, "ready", null)
                            override fun onOutput(data: ByteArray) = send(id, "output", data)
                            override fun onAuthBanner(message: String) = send(id, "banner", message)
                            override fun onExit(exitCode: Int, signal: String, errorMessage: String) {
                                send(id, "exit", mapOf("code" to exitCode, "signal" to signal, "error" to errorMessage))
                                release(id)
                            }
                            override fun onError(message: String) {
                                send(id, "error", message)
                                release(id)
                            }
                        }))
                    }
                } catch (e: Exception) {
                    release(id)
                    throw e
                }
                return id
            }
            "ssh.input" -> withContext(Dispatchers.IO) {
                val session = sessions[id]?.session ?: error("Terminal is not connected")
                session.sendInput((args["text"] as? String ?: "").toByteArray(Charsets.UTF_8))
            }
            "ssh.resize" -> withContext(Dispatchers.IO) {
                sessions[id]?.session?.sendResize(number("columns", 80), number("rows", 24), 0, 0)
            }
            "ssh.close" -> release(id)
            else -> error("Unknown terminal operation")
        }
        return null
    }

    private fun send(id: String, type: String, value: Any?) = emit("ssh", mapOf("id" to id, "event" to type, "value" to value))

    private fun release(id: String) {
        val handle = sessions.remove(id) ?: return
        cleanupScope.launch { handle.close() }
    }

    override fun close() = sessions.keys.toList().forEach { release(it) }
}
