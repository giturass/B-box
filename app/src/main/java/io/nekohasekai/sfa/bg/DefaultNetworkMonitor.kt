package io.nekohasekai.sfa.bg

import android.net.Network
import android.net.NetworkCapabilities
import android.os.Build
import android.util.Log
import io.nekohasekai.libbox.InterfaceUpdateListener
import io.nekohasekai.sfa.Application
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import java.net.NetworkInterface

object DefaultNetworkMonitor {

    private val access = Any()
    private val lifecycle = Mutex()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var monitor: DefaultInterfaceMonitor<DefaultNetworkListener.NetworkState>? = null
    @Volatile internal var networkState: DefaultNetworkListener.NetworkState? = null
        private set
    val defaultNetwork: Network? get() = networkState?.network

    suspend fun start() = lifecycle.withLock {
        val newMonitor = synchronized(access) {
            check(monitor == null) { "Default network monitor already started" }
            networkState = null
            DefaultInterfaceMonitor(scope, ::findDefaultInterface) { error ->
                Log.e("DefaultNetworkMonitor", "Unable to update default interface", error)
            }.also { monitor = it }
        }
        try {
            DefaultNetworkListener.start(this) { state ->
                synchronized(access) {
                    if (monitor === newMonitor) {
                        networkState = state
                        // Wait for the callback's properties/capabilities without polling a null
                        // snapshot. Only a known interface whose index is late needs retries.
                        newMonitor.updateNetwork(state?.takeIf { Build.VERSION.SDK_INT < Build.VERSION_CODES.O || it.isReady })
                    }
                }
            }
        } catch (error: Exception) {
            synchronized(access) {
                if (monitor === newMonitor) {
                    monitor = null
                    networkState = null
                }
            }
            withContext(NonCancellable) {
                try {
                    DefaultNetworkListener.stop(DefaultNetworkMonitor)
                } catch (cleanupError: Exception) {
                    if (cleanupError !== error) error.addSuppressed(cleanupError)
                } finally {
                    newMonitor.close()
                }
            }
            throw error
        }
    }

    suspend fun stop() = lifecycle.withLock {
        val previous = synchronized(access) {
            val previous = monitor
            monitor = null
            networkState = null
            previous
        }
        withContext(NonCancellable) {
            try {
                DefaultNetworkListener.stop(DefaultNetworkMonitor)
            } finally {
                previous?.close()
            }
        }
    }

    suspend fun require(): Network {
        val network = defaultNetwork
        if (network != null) {
            return network
        }
        return DefaultNetworkListener.get()
    }

    fun setListener(listener: InterfaceUpdateListener?) {
        val initialPublication = synchronized(access) {
            check(monitor != null || listener == null) { "Default network monitor stopped" }
            monitor?.setListener(listener?.let { target ->
                { networkInterface ->
                    target.updateDefaultInterface(networkInterface?.name.orEmpty(), networkInterface?.index ?: -1, false, false)
                }
            })
        }
        // libbox starts router downloads after this synchronous bridge returns.
        // Even the initial empty update must finish populating its interfaces.
        // The worker can acquire access, so never await while holding that lock.
        initialPublication?.let { publication -> runBlocking { publication.await() } }
    }

    private fun findDefaultInterface(state: DefaultNetworkListener.NetworkState): DefaultInterface? {
        // Android 7 only sends onAvailable for an already existing default network.
        // Fill its initial snapshot on the worker, then let the new state drive the
        // core update. API 26+ supplies both values in the callbacks themselves.
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O && !state.isReady) {
            val refreshed = state.copy(
                linkProperties = state.linkProperties ?: Application.connectivity.getLinkProperties(state.network),
                capabilities = state.capabilities ?: Application.connectivity.getNetworkCapabilities(state.network),
            )
            if (refreshed.isReady) synchronized(access) {
                if (networkState === state) {
                    networkState = refreshed
                    monitor?.updateNetwork(refreshed)
                }
            }
            return null
        }
        val name = state.linkProperties?.interfaceName ?: return null
        val networkInterface = NetworkInterface.getByName(name) ?: return null
        if (networkInterface.index <= 0) return null
        return DefaultInterface(name, networkInterface.index)
    }

    private val DefaultNetworkListener.NetworkState.isReady: Boolean
        get() = !linkProperties?.interfaceName.isNullOrEmpty() &&
            capabilities?.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) == true &&
            capabilities?.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN) == true
}
