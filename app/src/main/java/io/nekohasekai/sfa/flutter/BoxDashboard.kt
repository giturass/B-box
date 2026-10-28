package io.nekohasekai.sfa.flutter

import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import androidx.lifecycle.lifecycleScope
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.libbox.CommandServer
import io.nekohasekai.libbox.UIEgressProbe
import io.nekohasekai.sfa.bg.BoxService
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.database.ProfileManager
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.utils.AppLifecycleObserver
import io.nekohasekai.sfa.utils.CommandTarget
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.io.Closeable
import java.io.File

internal class BoxDashboard(private val activity: BoxActivity, private val emit: (String, Any?) -> Unit) : Closeable {
    @Volatile var remoteMemory = 0L
    private var networkStamp = ""
    private class EgressSession(val server: CommandServer) {
        @Volatile var closed = false
            private set
        private var probe: UIEgressProbe? = null
        fun attach(value: UIEgressProbe) = synchronized(this) {
            if (closed) { value.close(); throw CancellationException("IP probe cancelled") }
            probe = value
        }
        fun get(): UIEgressProbe = synchronized(this) { probe ?: error("IP probe is not available") }
        fun close() {
            val value = synchronized(this) { closed = true; probe.also { probe = null } }
            value?.close()
        }
    }
    private val egressSessions = mutableMapOf<String, EgressSession>()
    private val qualityCache = activity.getSharedPreferences("dashboard_ip_quality", 0)
    private val polling = activity.lifecycleScope.launch {
        AppLifecycleObserver.isForeground.collectLatest { foreground ->
            if (!foreground) return@collectLatest
            while (isActive) {
                val memory = withContext(Dispatchers.IO) { memory() }
                emit("memory", memory)
                val network = withContext(Dispatchers.IO) { network() }
                if (network.toString() != networkStamp) {
                    networkStamp = network.toString()
                    emit("lan", network)
                }
                delay(2000)
            }
        }
    }

    private fun memory(): Map<String, Any?> {
        if (CommandTarget.isRemote) return mapOf("remote" to true, "total" to remoteMemory, "core" to remoteMemory)
        val rss = File("/proc/self/status").useLines { lines -> lines.firstOrNull { it.startsWith("VmRSS:") }
            ?.substringAfter(':')?.trim()?.substringBefore(' ')?.toLongOrNull()?.times(1024) ?: 0L }
        val running = activity.serviceStatus.value == Status.Started
        val core = if (running) Libbox.readUIRuntimeMemory() else null
        val accounted = core?.total ?: 0L
        return mapOf("total" to maxOf(rss, accounted), "rss" to rss, "app" to maxOf(0L, rss - accounted), "core" to accounted,
            "remote" to false, "running" to running, "heapInuse" to core?.heapInuse, "heapIdle" to core?.heapIdle,
            "stack" to core?.stackInuse, "runtimeOther" to core?.runtimeOther, "goroutines" to core?.goroutines)
    }

    @Suppress("DEPRECATION")
    private suspend fun network(): Map<String, Any?> {
        val manager = activity.getSystemService(ConnectivityManager::class.java)
        val addresses = manager.allNetworks.flatMap { network ->
            if (manager.getNetworkCapabilities(network)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) != false) emptyList()
            else manager.getLinkProperties(network)?.linkAddresses.orEmpty().map { it.address }
                .filterNot { it.isLoopbackAddress || it.isLinkLocalAddress || it.isAnyLocalAddress || it.isMulticastAddress }
                .mapNotNull { it.hostAddress?.substringBefore('%') }
        }.distinct().sortedWith(compareBy<String> { ':' in it }.thenBy { it })
        val bindings = mutableListOf<Map<String, Any?>>()
        if (!CommandTarget.isRemote) {
            val profile = ProfileManager.get(Settings.selectedProfile)
            if (profile != null) runCatching {
                val root = JSONObject(Libbox.formatConfig(File(profile.typed.path).readText()).value)
                val inbounds = root.optJSONArray("inbounds")
                for (index in 0 until (inbounds?.length() ?: 0)) {
                    val inbound = inbounds!!.getJSONObject(index)
                    if (inbound.optString("type") !in setOf("mixed", "socks", "http")) continue
                    bindings += mapOf("type" to inbound.optString("type"), "tag" to inbound.optString("tag"),
                        "listen" to inbound.optString("listen", "::"), "port" to inbound.optInt("listen_port"))
                }
            }
        }
        return mapOf("addresses" to addresses, "bindings" to bindings)
    }

    private fun requireLocalService() {
        require(!CommandTarget.isRemote) { "Remote servers do not expose an egress probe" }
        require(activity.serviceStatus.value == Status.Started) { "Start the service to check its egress IP" }
    }

    private fun session(id: String): EgressSession {
        requireLocalService()
        val session = egressSessions[id] ?: error("IP probe is no longer active")
        check(!session.closed && BoxService.dashboardServer === session.server) { "Service changed during egress check" }
        return session
    }

    suspend fun egress(id: String): Map<String, Any?> {
        requireLocalService()
        require(id.length in 1..128 && id !in egressSessions && egressSessions.size < 8) { "Invalid IP probe session" }
        val server = BoxService.dashboardServer ?: error("Service is not available")
        val active = EgressSession(server)
        egressSessions[id] = active
        return try {
            val info = withContext(Dispatchers.IO) {
                val probe = server.newUIEgressProbe()
                active.attach(probe)
                jsonObject(JSONObject(probe.lookupOutboundIP()))
            }
            check(session(id) === active) { "IP probe changed during egress check" }
            info
        } catch (error: Exception) {
            if (egressSessions[id] === active) egressSessions.remove(id)
            active.close()
            throw error
        }
    }

    suspend fun query(id: String, url: String, timeoutMillis: Long): Map<String, Any?> {
        val active = session(id)
        val response = withContext(Dispatchers.IO) { active.get().get(url, timeoutMillis) }
        check(session(id) === active) { "IP probe changed during query" }
        return mapOf("status" to response.status, "body" to response.content.toString(Charsets.UTF_8))
    }

    fun closeProbe(id: String) { egressSessions.remove(id)?.close() }

    fun readQualityCache(ip: String): Map<String, Any?>? = runCatching {
        qualityCache.getString(ip, null)?.let { jsonObject(JSONObject(it)) }
    }.getOrNull()

    suspend fun writeQualityCache(ip: String, value: Map<String, Any?>) = withContext(Dispatchers.IO) {
        val encoded = JSONObject(value).toString()
        val record = JSONObject(encoded)
        val now = System.currentTimeMillis()
        require(ip.length in 1..128 && encoded.toByteArray().size <= 16384 &&
            record.optJSONObject("quality")?.optString("ip") == ip && record.optLong("checkedAt", -1) in 0..now) { "Invalid IP quality record" }
        val editor = qualityCache.edit().putString(ip, encoded)
        qualityCache.all.forEach { (key, stored) ->
            val checkedAt = runCatching { JSONObject(stored as String).optLong("checkedAt", -1) }.getOrDefault(-1)
            if (key != ip && (checkedAt < now - 3 * 24 * 60 * 60 * 1000L || checkedAt > now)) editor.remove(key)
        }
        check(editor.commit()) { "Unable to cache IP quality" }
    }

    suspend fun releaseMemory(): Map<String, Any?> = withContext(Dispatchers.IO) {
        require(!CommandTarget.isRemote) { "Remote memory collection is not supported" }
        Libbox.collectUIRuntimeMemory()
        memory().also { emit("memory", it) }
    }

    override fun close() {
        polling.cancel()
        egressSessions.values.forEach { it.close() }
        egressSessions.clear()
    }
}
