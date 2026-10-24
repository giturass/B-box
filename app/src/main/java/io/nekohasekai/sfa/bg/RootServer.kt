package io.nekohasekai.sfa.bg

import android.content.Intent
import android.content.pm.PackageInfo
import android.content.pm.PackageManager
import android.os.Build
import android.os.IBinder
import android.os.ParcelFileDescriptor
import android.os.RemoteCallbackList
import android.os.RemoteException
import android.util.Log
import com.topjohnwu.superuser.ipc.RootService
import io.nekohasekai.libbox.AutoRedirectHandler
import io.nekohasekai.libbox.AutoRedirectSession
import io.nekohasekai.libbox.BridgeOptions
import io.nekohasekai.libbox.BridgeSession
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.libbox.NeighborEntryIterator
import io.nekohasekai.libbox.NeighborSubscription
import io.nekohasekai.libbox.NeighborUpdateListener
import io.nekohasekai.sfa.vendor.PrivilegedServiceUtils
import java.io.IOException
import java.lang.reflect.Proxy
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.Executors

class RootServer : RootService() {

    private val neighborCallbacks = RemoteCallbackList<INeighborTableCallback>()
    private var neighborSubscription: NeighborSubscription? = null

    private val hostnameByMAC = ConcurrentHashMap<String, String>()

    @Volatile
    private var lastNeighborEntries: List<Pair<String, String>>? = null

    private var tetheringCallback: Any? = null
    private var tetheringManager: Any? = null

    private val bridgeSessions = mutableSetOf<BridgeSessionBinder>()
    private val autoRedirectSessions = mutableSetOf<AutoRedirectSessionBinder>()

    private var destroyed = false

    // libsu exits the process right after onDestroy, so a session whose Start
    // is still running when the client dies must be registered before onDestroy
    // sweeps the set.
    private val autoRedirectLifecycle = Any()

    private val binder = object : IRootService.Stub() {
        override fun destroy() {
            stopSelf()
        }

        override fun getInstalledPackages(flags: Int, userId: Int): ParceledListSlice<PackageInfo> {
            val allPackages = PrivilegedServiceUtils.getInstalledPackages(flags, userId)
            return ParceledListSlice(allPackages)
        }

        override fun installPackage(apk: ParcelFileDescriptor?, size: Long, userId: Int) {
            if (apk == null) throw IOException("APK file descriptor is null")
            PrivilegedServiceUtils.installPackage(apk, size, userId)
        }

        override fun registerNeighborTableCallback(callback: INeighborTableCallback?) {
            if (callback == null) return
            neighborCallbacks.register(callback)
            synchronized(neighborCallbacks) {
                if (neighborSubscription == null) {
                    try {
                        neighborSubscription =
                            Libbox.subscribeNeighborTable(object : NeighborUpdateListener {
                                override fun updateNeighborTable(entries: NeighborEntryIterator?) {
                                    if (entries == null) return
                                    val rawList = mutableListOf<Pair<String, String>>()
                                    while (entries.hasNext()) {
                                        val entry = entries.next()
                                        rawList.add(entry.address to entry.macAddress)
                                    }
                                    lastNeighborEntries = rawList
                                    broadcastEnrichedEntries(rawList)
                                }
                            })
                    } catch (e: Exception) {
                        Log.e("RootServer", "subscribeNeighborTable failed", e)
                    }
                    startTetheringMonitor()
                }
            }
        }

        override fun unregisterNeighborTableCallback(callback: INeighborTableCallback?) {
            if (callback == null) return
            neighborCallbacks.unregister(callback)
            synchronized(neighborCallbacks) {
                if (neighborCallbacks.registeredCallbackCount == 0) {
                    neighborSubscription?.close()
                    neighborSubscription = null
                    stopTetheringMonitor()
                }
            }
        }

        override fun openBridge(
            bridgeName: String?,
            mtu: Int,
            inet4Port: String?,
            inet6Port: String?,
            ruleIndex: Int,
            routeTable: Int,
        ): IBridgeSession {
            val options = BridgeOptions()
            options.bridgeName = bridgeName ?: ""
            options.mtu = mtu
            options.inet4Port = inet4Port ?: ""
            options.inet6Port = inet6Port ?: ""
            options.ruleIndex = ruleIndex
            options.routeTable = routeTable
            val session = Libbox.newBridgeService(options)
            val binder = BridgeSessionBinder(session)
            synchronized(bridgeSessions) {
                bridgeSessions.add(binder)
            }
            return binder
        }

        // Binder only marshals a handful of exception types; a Go error or a
        // RemoteException escaping here reaches the app as a bare failure
        // without the message, so everything is converted to IllegalStateException.
        override fun startAutoRedirect(options: ByteArray?, handler: IAutoRedirectHandler?): IAutoRedirectSession = try {
            requireNotNull(options)
            val autoRedirectHandler = handler!!
            synchronized(autoRedirectLifecycle) {
                if (destroyed) throw IllegalStateException("root service destroyed")
                startAutoRedirectLocked(options, autoRedirectHandler)
            }
        } catch (e: Exception) {
            throw IllegalStateException(e.message ?: e.toString())
        }

        private fun startAutoRedirectLocked(options: ByteArray, handler: IAutoRedirectHandler): IAutoRedirectSession {
            val session = Libbox.newAutoRedirectService(
                options,
                object : AutoRedirectHandler {
                    override fun judgeFlow(
                        ipProtocol: Int,
                        sourceAddress: String?,
                        sourcePort: Int,
                        destinationAddress: String?,
                        destinationPort: Int,
                        firstPacket: ByteArray?,
                    ): Int = try {
                        handler.judgeFlow(
                            ipProtocol,
                            sourceAddress,
                            sourcePort,
                            destinationAddress,
                            destinationPort,
                            firstPacket,
                        )
                    } catch (e: RemoteException) {
                        throw IOException(e)
                    }

                    override fun writeLog(level: Int, message: String?) {
                        try {
                            handler.writeLog(level, message)
                        } catch (_: RemoteException) {
                        }
                    }

                    override fun redirectListenerFileDescriptor(): Int = try {
                        val listener = handler.redirectListener
                            ?: throw IOException("no redirect listener")
                        listener.detachFd()
                    } catch (e: RemoteException) {
                        throw IOException(e)
                    }

                    override fun routeAddressSetFileDescriptor(): Int = try {
                        val addressSet = handler.routeAddressSet
                            ?: throw IOException("no route address set")
                        addressSet.detachFd()
                    } catch (e: RemoteException) {
                        throw IOException(e)
                    }
                },
            )
            val binder = AutoRedirectSessionBinder(session, handler.asBinder())
            synchronized(autoRedirectSessions) {
                autoRedirectSessions.add(binder)
            }
            try {
                handler.asBinder().linkToDeath(binder, 0)
            } catch (e: RemoteException) {
                binder.closeSession()
                throw e
            }
            return binder
        }
    }

    private inner class BridgeSessionBinder(
        private val session: BridgeSession,
    ) : IBridgeSession.Stub() {

        override fun getFileDescriptor(): ParcelFileDescriptor = ParcelFileDescriptor.fromFd(session.fileDescriptor())

        override fun getName(): String = session.name()

        override fun isInet6Active(): Boolean = session.inet6Active()

        override fun setEgress(interfaceName: String?) {
            session.setEgress(interfaceName ?: "")
        }

        override fun close() {
            synchronized(bridgeSessions) {
                bridgeSessions.remove(this)
            }
            session.close()
        }
    }

    private inner class AutoRedirectSessionBinder(
        private val session: AutoRedirectSession,
        private val handlerBinder: IBinder,
    ) : IAutoRedirectSession.Stub(),
        IBinder.DeathRecipient {

        override fun binderDied() {
            Log.w("RootServer", "auto-redirect handler died, closing session")
            closeSession()
        }

        override fun close() {
            try {
                closeSession()
            } catch (e: Exception) {
                throw IllegalStateException(e.message ?: e.toString())
            }
            handlerBinder.unlinkToDeath(this, 0)
        }

        override fun updateRouteAddressSet() {
            try {
                session.updateRouteAddressSet()
            } catch (e: Exception) {
                throw IllegalStateException(e.message ?: e.toString())
            }
        }

        // libsu terminates the root process as soon as the last client dies,
        // so the Go-side cleanup must finish inside this call and onDestroy
        // must block on it.
        private var closed = false

        fun closeSession() {
            synchronized(this) {
                if (closed) return
                closed = true
                try {
                    session.close()
                } finally {
                    synchronized(autoRedirectSessions) {
                        autoRedirectSessions.remove(this)
                    }
                }
            }
        }
    }

    private fun broadcastEnrichedEntries(rawList: List<Pair<String, String>>) {
        val list = rawList.map { (address, mac) ->
            NeighborEntry(address, mac, hostnameByMAC[mac.uppercase()] ?: "")
        }
        Log.d("RootServer", "neighborTable updated: ${list.size} entries")
        val slice = ParceledListSlice(list)
        val count = neighborCallbacks.beginBroadcast()
        try {
            repeat(count) { i ->
                try {
                    neighborCallbacks.getBroadcastItem(i).onNeighborTableUpdated(slice)
                } catch (_: Exception) {
                }
            }
        } finally {
            neighborCallbacks.finishBroadcast()
        }
    }

    // TetheringManager reflection (API 30+)

    private val classTetheredClient by lazy {
        Class.forName("android.net.TetheredClient")
    }
    private val getMacAddress by lazy {
        classTetheredClient.getDeclaredMethod("getMacAddress")
    }
    private val getAddresses by lazy {
        classTetheredClient.getDeclaredMethod("getAddresses")
    }
    private val classAddressInfo by lazy {
        Class.forName("android.net.TetheredClient\$AddressInfo")
    }
    private val getHostname by lazy {
        classAddressInfo.getDeclaredMethod("getHostname")
    }

    private fun startTetheringMonitor() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.R) return
        try {
            val manager = getSystemService("tethering") ?: return
            tetheringManager = manager
            val callbackClass =
                Class.forName("android.net.TetheringManager\$TetheringEventCallback")
            val registerMethod = manager.javaClass.getMethod(
                "registerTetheringEventCallback",
                java.util.concurrent.Executor::class.java,
                callbackClass,
            )
            val proxy = Proxy.newProxyInstance(
                callbackClass.classLoader,
                arrayOf(callbackClass),
            ) { proxyObject, method, args ->
                when (method.name) {
                    "hashCode" -> System.identityHashCode(proxyObject)
                    "equals" -> proxyObject === args?.get(0)
                    "toString" ->
                        proxyObject.javaClass.name + "@" +
                            Integer.toHexString(System.identityHashCode(proxyObject))
                    "onClientsChanged" -> {
                        if (args != null) {
                            @Suppress("UNCHECKED_CAST")
                            handleClientsChanged(args[0] as Collection<*>)
                        }
                        null
                    }
                    else -> null
                }
            }
            tetheringCallback = proxy
            registerMethod.invoke(manager, Executors.newSingleThreadExecutor(), proxy)
            Log.d("RootServer", "TetheringManager monitor started")
        } catch (e: Exception) {
            Log.e("RootServer", "startTetheringMonitor failed", e)
        }
    }

    private fun stopTetheringMonitor() {
        val manager = tetheringManager ?: return
        val callback = tetheringCallback ?: return
        try {
            val callbackClass =
                Class.forName("android.net.TetheringManager\$TetheringEventCallback")
            val unregisterMethod = manager.javaClass.getMethod(
                "unregisterTetheringEventCallback",
                callbackClass,
            )
            unregisterMethod.invoke(manager, callback)
        } catch (e: Exception) {
            Log.e("RootServer", "stopTetheringMonitor failed", e)
        }
        tetheringCallback = null
        tetheringManager = null
        hostnameByMAC.clear()
    }

    private fun handleClientsChanged(clients: Collection<*>) {
        hostnameByMAC.clear()
        for (client in clients) {
            if (client == null) continue
            try {
                val mac = getMacAddress.invoke(client).toString().uppercase()

                @Suppress("UNCHECKED_CAST")
                val addresses = getAddresses.invoke(client) as List<*>
                for (info in addresses) {
                    if (info == null) continue
                    val hostname = getHostname.invoke(info) as? String
                    if (!hostname.isNullOrEmpty()) {
                        hostnameByMAC[mac] = hostname
                    }
                }
            } catch (e: Exception) {
                Log.e("RootServer", "handleClientsChanged reflection error", e)
            }
        }
        Log.d("RootServer", "tethered clients updated: ${hostnameByMAC.size} hostnames")
        lastNeighborEntries?.let { broadcastEnrichedEntries(it) }
    }

    override fun onBind(intent: Intent): IBinder = binder

    override fun onDestroy() {
        val sessions = synchronized(bridgeSessions) {
            val copied = bridgeSessions.toList()
            bridgeSessions.clear()
            copied
        }
        for (session in sessions) {
            try {
                session.close()
            } catch (_: Exception) {
            }
        }
        val autoRedirects = synchronized(autoRedirectLifecycle) {
            destroyed = true
            synchronized(autoRedirectSessions) {
                autoRedirectSessions.toList()
            }
        }
        for (session in autoRedirects) {
            try {
                session.closeSession()
            } catch (_: Exception) {
            }
        }
        stopTetheringMonitor()
        neighborSubscription?.close()
        neighborSubscription = null
        neighborCallbacks.kill()
        super.onDestroy()
    }
}
