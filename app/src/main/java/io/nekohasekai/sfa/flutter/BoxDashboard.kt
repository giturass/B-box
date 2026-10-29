package io.nekohasekai.sfa.flutter

import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import androidx.lifecycle.lifecycleScope
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.libbox.CommandServer
import io.nekohasekai.libbox.UIEgressProbe
import io.nekohasekai.sfa.bg.BoxService
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.database.ProfileContent
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
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import org.json.JSONObject
import org.json.JSONArray
import java.io.Closeable
import java.io.File
import java.net.NetworkInterface

internal class BoxDashboard(private val activity: BoxActivity, private val emit: (String, Any?) -> Unit) : Closeable {
    @Volatile var remoteMemory = 0L
    private var networkStamp = ""
    private val networkMutex = Mutex()
    private class EgressSession(val server: CommandServer?) {
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
    private val serviceChanges = activity.lifecycleScope.launch {
        activity.serviceStatus.collect {
            egressSessions.filterValues { !isCurrent(it) }.keys.toList().forEach(::closeProbe)
        }
    }
    private val qualityCache = activity.getSharedPreferences("dashboard_ip_quality", 0)
    private val polling = activity.lifecycleScope.launch {
        AppLifecycleObserver.isForeground.collectLatest { foreground ->
            if (!foreground) return@collectLatest
            while (isActive) {
                val memory = withContext(Dispatchers.IO) { memory() }
                emit("memory", memory)
                refreshNetwork()
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
        val networks = manager.allNetworks.mapNotNull { network ->
            val capabilities = manager.getNetworkCapabilities(network) ?: return@mapNotNull null
            val properties = manager.getLinkProperties(network) ?: return@mapNotNull null
            Triple(network, capabilities, properties)
        }
        val excluded = networks.filter { (_, capabilities, _) ->
            capabilities.hasTransport(NetworkCapabilities.TRANSPORT_VPN) ||
                capabilities.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR)
        }.mapNotNull { it.third.interfaceName }.toSet()
        val priorities = networks.associate { (_, capabilities, properties) ->
            properties.interfaceName to when {
                capabilities.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) -> 0
                capabilities.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) -> 1
                else -> 2
            }
        }
        // Interface enumeration also includes hotspot/tethering addresses, which
        // ConnectivityManager.allNetworks does not necessarily expose.
        val interfaces = NetworkInterface.getNetworkInterfaces()?.toList().orEmpty()
            .filter { it.name !in excluded && it.isUp && !it.isLoopback && !it.isPointToPoint }
        fun priority(name: String) = priorities[name] ?: when {
            name.startsWith("wlan") || name.startsWith("ap") || name.startsWith("swlan") -> 0
            name.startsWith("eth") -> 1
            else -> 2
        }
        val interfaceAddresses = interfaces.flatMap { networkInterface ->
            networkInterface.inetAddresses.toList()
                .filterNot { it.isLoopbackAddress || it.isLinkLocalAddress || it.isAnyLocalAddress || it.isMulticastAddress }
                .mapNotNull { address -> address.hostAddress?.substringBefore('%')?.let {
                    mapOf("address" to it, "interface" to networkInterface.name)
                } }
        }.distinct().sortedWith(compareBy<Map<String, String>> { ':' in it.getValue("address") }
            .thenBy { priority(it.getValue("interface")) }.thenBy { it.getValue("address") })
        val addresses = interfaceAddresses.map { it.getValue("address") }.distinct()
        val encodedAddresses = JSONArray(interfaceAddresses).toString()
        fun listeners(content: String): List<Map<String, Any?>> {
            val values = JSONArray(Libbox.readUILANProxyListeners(content, encodedAddresses).value)
            return (0 until values.length()).map { jsonObject(values.getJSONObject(it)) }
        }
        var bindings = emptyList<Map<String, Any?>>()
        var activeBindings = emptyList<Map<String, Any?>>()
        var profileId = -1L
        var revision: String? = null
        var configurationError: String? = null
        var running = false
        var activeProfileId = -1L
        if (!CommandTarget.isRemote) {
            try {
                val content = ProfileContent.mutex.withLock {
                    profileId = Settings.selectedProfile
                    ProfileManager.get(profileId)?.let { File(it.typed.path).readText() }
                }
                if (content != null) {
                    revision = ProfileContent.revision(content)
                    bindings = listeners(content)
                }
            } catch (error: CancellationException) {
                throw error
            } catch (error: Exception) {
                configurationError = error.message ?: error.toString()
            }
            val active = BoxService.dashboardConfig.value
            running = activity.serviceStatus.value == Status.Started && active != null &&
                BoxService.dashboardServer === active.server
            if (running && active != null) {
                activeProfileId = active.profileId
                activeBindings = if (configurationError == null && ProfileContent.revision(active.content) == revision) bindings else listeners(active.content)
            }
        }
        val stamp = "${manager.activeNetwork}:$encodedAddresses:${networks.map { (network, capabilities, _) ->
            "$network:${capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED)}"
        }.sorted()}"
        return mapOf("addresses" to addresses, "bindings" to bindings, "activeBindings" to activeBindings,
            "enabled" to (if (running) activeBindings else bindings).any { it["lan"] == true },
            "running" to running, "activeProfileId" to activeProfileId,
            "profileId" to profileId, "revision" to revision, "canToggle" to (revision != null && configurationError == null),
            "error" to configurationError, "stamp" to stamp)
    }

    suspend fun refreshNetwork(): Map<String, Any?> = networkMutex.withLock {
        val value = withContext(Dispatchers.IO) { network() }
        if (value.toString() != networkStamp) {
            networkStamp = value.toString()
            emit("lan", value)
        }
        value
    }

    private fun requireLocalProbe() {
        require(!CommandTarget.isRemote) { "Remote servers do not expose an egress probe" }
        require(activity.serviceStatus.value in listOf(Status.Stopped, Status.Started)) { "Service is changing during egress check" }
    }

    private fun isCurrent(session: EgressSession): Boolean = !session.closed && !CommandTarget.isRemote &&
        BoxService.dashboardServer === session.server &&
        activity.serviceStatus.value == (if (session.server == null) Status.Stopped else Status.Started)

    private fun session(id: String): EgressSession {
        requireLocalProbe()
        val session = egressSessions[id] ?: error("IP probe is no longer active")
        check(isCurrent(session)) { "Service changed during egress check" }
        return session
    }

    private suspend fun createProbe(id: String): EgressSession {
        requireLocalProbe()
        require(id.length in 1..128 && id !in egressSessions && egressSessions.size < 8) { "Invalid IP probe session" }
        val server = when (activity.serviceStatus.value) {
            Status.Started -> BoxService.dashboardServer ?: error("Service is not available")
            Status.Stopped -> {
                check(BoxService.dashboardServer == null) { "Service is changing during egress check" }
                null
            }
            else -> error("Service is changing during egress check")
        }
        val active = EgressSession(server)
        egressSessions[id] = active
        return try {
            withContext(Dispatchers.IO) {
                val probe = if (server == null) Libbox.newUIDirectEgressProbe() else server.newUIEgressProbe()
                active.attach(probe)
            }
            check(session(id) === active) { "IP probe changed during creation" }
            active
        } catch (error: Exception) {
            if (egressSessions[id] === active) egressSessions.remove(id)
            active.close()
            throw error
        }
    }

    suspend fun openProbe(id: String) { createProbe(id) }

    suspend fun egress(id: String): Map<String, Any?> {
        val active = createProbe(id)
        return try {
            val info = withContext(Dispatchers.IO) { jsonObject(JSONObject(active.get().lookupOutboundIP())) }
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
        serviceChanges.cancel()
        polling.cancel()
        egressSessions.values.forEach { it.close() }
        egressSessions.clear()
    }
}
