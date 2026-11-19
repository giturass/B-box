/*
 *                                                                             *
 *  Copyright (C) 2019 by Max Lv <max.c.lv@gmail.com>                          *
 *  Copyright (C) 2019 by Mygod Studio <contact-shadowsocks-android@mygod.be>  *
 *                                                                             *
 *  This program is free software: you can redistribute it and/or modify       *
 *  it under the terms of the GNU General Public License as published by       *
 *  the Free Software Foundation, either version 3 of the License, or          *
 *  (at your option) any later version.                                        *
 *                                                                             *
 *  This program is distributed in the hope that it will be useful,            *
 *  but WITHOUT ANY WARRANTY; without even the implied warranty of             *
 *  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the              *
 *  GNU General Public License for more details.                               *
 *                                                                             *
 *  You should have received a copy of the GNU General Public License          *
 *  along with this program. If not, see <http://www.gnu.org/licenses/>.       *
 *                                                                             *
 *******************************************************************************/

package io.nekohasekai.sfa.bg

import android.annotation.TargetApi
import android.net.ConnectivityManager
import android.net.LinkProperties
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.os.Build
import android.os.Handler
import android.os.HandlerThread
import io.nekohasekai.sfa.Application
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.DelicateCoroutinesApi
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.GlobalScope
import kotlinx.coroutines.ObsoleteCoroutinesApi
import kotlinx.coroutines.channels.actor
import kotlinx.coroutines.runBlocking

object DefaultNetworkListener {
    internal data class NetworkState(
        val network: Network,
        val linkProperties: LinkProperties? = null,
        val capabilities: NetworkCapabilities? = null,
    )

    private sealed class NetworkMessage {
        class Start(val key: Any, val listener: (NetworkState?) -> Unit) : NetworkMessage() {
            val response = CompletableDeferred<Unit>()
        }

        class Get : NetworkMessage() {
            val response = CompletableDeferred<Network>()
        }

        class Stop(val key: Any) : NetworkMessage() {
            val response = CompletableDeferred<Unit>()
        }

        sealed class Event(val generation: Long) : NetworkMessage()

        class Put(generation: Long, val network: Network) : Event(generation)

        class Properties(generation: Long, val network: Network, val properties: LinkProperties) : Event(generation)

        class Capabilities(generation: Long, val network: Network, val capabilities: NetworkCapabilities) : Event(generation)

        class Lost(generation: Long, val network: Network) : Event(generation)
    }

    @OptIn(DelicateCoroutinesApi::class, ObsoleteCoroutinesApi::class)
    private val networkActor =
        GlobalScope.actor<NetworkMessage>(Dispatchers.Unconfined) {
            val listeners = mutableMapOf<Any, (NetworkState?) -> Unit>()
            var state: NetworkState? = null
            var generation = 0L
            var callback: ConnectivityManager.NetworkCallback? = null
            val pendingRequests = arrayListOf<NetworkMessage.Get>()
            for (message in channel) {
                when (message) {
                    is NetworkMessage.Start -> {
                        try {
                            if (listeners.isEmpty()) callback = register(++generation)
                            listeners[message.key] = message.listener
                            message.listener(state)
                            message.response.complete(Unit)
                        } catch (error: Exception) {
                            message.response.completeExceptionally(error)
                        }
                    }

                    is NetworkMessage.Get -> {
                        val network = state?.network
                        if (listeners.isEmpty()) {
                            message.response.completeExceptionally(IllegalStateException("Default network monitor stopped"))
                        } else if (network == null) {
                            pendingRequests += message
                        } else {
                            message.response.complete(
                                network,
                            )
                        }
                    }

                    is NetworkMessage.Stop -> {
                        if (listeners.isNotEmpty() &&
                            // was not empty
                            listeners.remove(message.key) != null &&
                            listeners.isEmpty()
                        ) {
                            state = null
                            ++generation
                            callback?.let(::unregister)
                            callback = null
                            pendingRequests.forEach {
                                it.response.completeExceptionally(IllegalStateException("Default network monitor stopped"))
                            }
                            pendingRequests.clear()
                        }
                        message.response.complete(Unit)
                    }

                    is NetworkMessage.Event -> {
                        // A callback can already be queued when unregister returns. It must
                        // not seed a later service with the preceding registration's network.
                        if (message.generation != generation || listeners.isEmpty()) continue
                        when (message) {
                            is NetworkMessage.Put -> {
                                state = NetworkState(message.network)
                                pendingRequests.forEach { it.response.complete(message.network) }
                                pendingRequests.clear()
                            }

                            is NetworkMessage.Properties -> {
                                val current = state?.takeIf { it.network == message.network } ?: continue
                                state = current.copy(linkProperties = message.properties)
                            }

                            is NetworkMessage.Capabilities -> {
                                val current = state?.takeIf { it.network == message.network } ?: continue
                                state = current.copy(capabilities = message.capabilities)
                            }

                            is NetworkMessage.Lost -> {
                                if (state?.network != message.network) continue
                                state = null
                            }
                        }
                        listeners.values.forEach { it(state) }
                    }
                }
            }
        }

    internal suspend fun start(key: Any, listener: (NetworkState?) -> Unit) = NetworkMessage.Start(key, listener).run {
        networkActor.send(this)
        response.await()
    }

    suspend fun get(): Network = NetworkMessage.Get().run {
        networkActor.send(this)
        response.await()
    }

    suspend fun stop(key: Any) = NetworkMessage.Stop(key).run {
        networkActor.send(this)
        response.await()
    }

    // NB: this runs on callbackHandler's thread, or in ConnectivityThread below API 26 where
    // no handler can be passed. Listeners only publish snapshots here; interface probes
    // and JNI updates run in the monitor worker, so they cannot delay the next callback.
    private class Callback(private val generation: Long) : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) = runBlocking {
            networkActor.send(
                NetworkMessage.Put(
                    generation,
                    network,
                ),
            )
        }

        override fun onCapabilitiesChanged(network: Network, networkCapabilities: NetworkCapabilities) {
            runBlocking { networkActor.send(NetworkMessage.Capabilities(generation, network, networkCapabilities)) }
        }

        override fun onLinkPropertiesChanged(network: Network, linkProperties: LinkProperties) {
            runBlocking { networkActor.send(NetworkMessage.Properties(generation, network, linkProperties)) }
        }

        override fun onLost(network: Network) = runBlocking {
            networkActor.send(
                NetworkMessage.Lost(
                    generation,
                    network,
                ),
            )
        }
    }

    private val request =
        NetworkRequest.Builder().apply {
            addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
            addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_RESTRICTED)
        }.build()
    private val callbackHandler by lazy {
        Handler(HandlerThread("network-callback").apply { start() }.looper)
    }

    /**
     * Unfortunately registerDefaultNetworkCallback is going to return VPN interface since Android P DP1:
     * https://android.googlesource.com/platform/frameworks/base/+/dda156ab0c5d66ad82bdcf76cda07cbc0a9c8a2e
     *
     * This makes doing a requestNetwork with REQUEST necessary so that we don't get ALL possible networks that
     * satisfies default network capabilities but only THE default network. Unfortunately, we need to have
     * android.permission.CHANGE_NETWORK_STATE to be able to call requestNetwork.
     *
     * Source: https://android.googlesource.com/platform/frameworks/base/+/2df4c7d/services/core/java/com/android/server/ConnectivityService.java#887
     */
    private fun register(generation: Long): ConnectivityManager.NetworkCallback {
        val callback = Callback(generation)
        when (Build.VERSION.SDK_INT) {
            in 31..Int.MAX_VALUE ->
                @TargetApi(31)
                {
                    Application.connectivity.registerBestMatchingNetworkCallback(
                        request,
                        callback,
                        callbackHandler,
                    )
                }

            in 28 until 31 ->
                @TargetApi(28)
                { // we want REQUEST here instead of LISTEN
                    Application.connectivity.requestNetwork(request, callback, callbackHandler)
                }

            in 26 until 28 ->
                @TargetApi(26)
                {
                    Application.connectivity.registerDefaultNetworkCallback(callback, callbackHandler)
                }

            else -> Application.connectivity.registerDefaultNetworkCallback(callback)
        }
        return callback
    }

    private fun unregister(callback: ConnectivityManager.NetworkCallback) {
        runCatching {
            Application.connectivity.unregisterNetworkCallback(callback)
        }
    }
}
