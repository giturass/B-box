package io.nekohasekai.sfa.flutter

import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import androidx.lifecycle.lifecycleScope
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.bg.BoxService
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.database.ProfileManager
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.utils.AppLifecycleObserver
import io.nekohasekai.sfa.utils.CommandTarget
import kotlinx.coroutines.Dispatchers
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

    suspend fun egress(): Map<String, Any?> {
        require(!CommandTarget.isRemote) { "Remote servers do not expose an egress probe" }
        require(activity.serviceStatus.value == Status.Started) { "Start the service to check its egress IP" }
        val server = BoxService.dashboardServer ?: error("Service is not available")
        return withContext(Dispatchers.IO) {
            // The source is also used by FlClash; classification comes from its
            // explicit risk fields, not guesses about addresses or providers.
            val data = JSONObject(server.queryUIEgress("https://api.ipquery.io/?format=json"))
            val ip = data.optString("ip")
            require(ip.matches(Regex("[0-9a-fA-F:.]+"))) { "Invalid egress response" }
            val location = data.optJSONObject("location")
            val isp = data.optJSONObject("isp")
            val risk = data.optJSONObject("risk")
            val type = when {
                risk == null -> "Unknown type"
                risk.optBoolean("is_datacenter") -> "Hosting"
                risk.optBoolean("is_mobile") -> "Mobile"
                risk.has("is_datacenter") && risk.has("is_mobile") -> "Residential"
                else -> "Unknown type"
            }
            val risky = risk?.let { it.optBoolean("is_tor") || it.optBoolean("is_abuser") } == true
            val quality = when { risk == null -> null; risky -> "Risky"; type == "Hosting" -> "Normal"; type != "Unknown type" -> "Good"; else -> null }
            check(BoxService.dashboardServer === server && !CommandTarget.isRemote && activity.serviceStatus.value == Status.Started) { "Service changed during egress check" }
            mapOf("ip" to ip, "country" to location?.optString("country_code"), "organization" to isp?.optString("org")?.ifBlank { isp.optString("isp") },
                "asn" to isp?.optString("asn"), "city" to location?.optString("city"), "type" to type, "quality" to quality,
                "source" to "ipquery.io", "checkedAt" to System.currentTimeMillis())
        }
    }

    suspend fun releaseMemory(): Map<String, Any?> = withContext(Dispatchers.IO) {
        require(!CommandTarget.isRemote) { "Remote memory collection is not supported" }
        Libbox.collectUIRuntimeMemory()
        memory().also { emit("memory", it) }
    }

    override fun close() { polling.cancel() }
}
