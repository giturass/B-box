package io.nekohasekai.sfa.backend.connections

import androidx.lifecycle.viewModelScope
import io.nekohasekai.libbox.ConnectionEvents
import io.nekohasekai.libbox.Connections
import io.nekohasekai.sfa.backend.base.BaseViewModel
import io.nekohasekai.sfa.backend.model.Connection
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.ktx.toList
import io.nekohasekai.sfa.utils.AppLifecycleObserver
import io.nekohasekai.sfa.utils.CommandClient
import io.nekohasekai.sfa.utils.RemoteControlManager
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import java.util.concurrent.atomic.AtomicLong

data class ConnectionsUiState(
    val allConnections: List<Connection> = emptyList(),
    val isLoading: Boolean = false,
)

class ConnectionsViewModel :
    BaseViewModel<ConnectionsUiState>(),
    CommandClient.Handler {
    private val commandClient = CommandClient(
        viewModelScope,
        CommandClient.ConnectionType.Connections,
        this,
    )

    private val _serviceStatus = MutableStateFlow(Status.Stopped)
    val serviceStatus = _serviceStatus.asStateFlow()
    private var lastServiceStatus: Status = Status.Stopped

    private val _visibleCount = MutableStateFlow(0)

    private var connectionsStore: Connections? = null
    private val connectionsMutex = Mutex()
    private val connectionsGeneration = AtomicLong(0)

    override fun createInitialState() = ConnectionsUiState()

    init {
        viewModelScope.launch {
            combine(
                AppLifecycleObserver.isForeground,
                AppLifecycleObserver.isScreenOn,
                _visibleCount,
                _serviceStatus,
                combine(
                    RemoteControlManager.remoteServer,
                    RemoteControlManager.isConnected,
                ) { remoteServer, remoteConnected -> remoteServer?.id to remoteConnected },
            ) { foreground, screenOn, visibleCount, status, (remoteServerId, remoteConnected) ->
                val serviceReady = if (remoteServerId != null) remoteConnected else status == Status.Started
                val shouldConnect = foreground && screenOn && visibleCount > 0 && serviceReady
                shouldConnect to remoteServerId
            }.distinctUntilChanged().collect { (shouldConnect, _) ->
                if (shouldConnect) {
                    updateState { copy(isLoading = true) }
                    commandClient.connect()
                } else {
                    commandClient.disconnect()
                }
            }
        }
    }

    fun setVisible(visible: Boolean) {
        _visibleCount.value = (_visibleCount.value + if (visible) 1 else -1).coerceAtLeast(0)
    }

    override fun onCleared() {
        super.onCleared()
        commandClient.disconnect()
    }

    private suspend fun handleServiceStatusChange(status: Status) {
        if (RemoteControlManager.remoteServer.value != null) {
            return
        }
        if (status != Status.Started) {
            withContext(Dispatchers.Default) {
                connectionsMutex.withLock {
                    connectionsStore = null
                }
                connectionsGeneration.incrementAndGet()
            }
            updateState {
                copy(allConnections = emptyList(), isLoading = false)
            }
        }
    }

    fun updateServiceStatus(status: Status) {
        if (status == lastServiceStatus) return
        lastServiceStatus = status
        viewModelScope.launch {
            _serviceStatus.emit(status)
            handleServiceStatusChange(status)
        }
    }

    override fun onDisconnected() {
        viewModelScope.launch(Dispatchers.Default) {
            connectionsMutex.withLock {
                connectionsStore = null
            }
            connectionsGeneration.incrementAndGet()
            withContext(Dispatchers.Main) {
                updateState {
                    copy(allConnections = emptyList(), isLoading = false)
                }
            }
        }
    }

    override fun writeConnectionEvents(events: ConnectionEvents) {
        viewModelScope.launch(Dispatchers.Default) {
            val generation = connectionsGeneration.get()
            val snapshot = connectionsMutex.withLock {
                if (connectionsStore == null) {
                    connectionsStore = Connections()
                }
                val store = connectionsStore ?: return@withLock null
                store.applyEvents(events)
                buildConnectionLists(store)
            } ?: return@launch
            if (connectionsGeneration.get() != generation) {
                return@launch
            }
            withContext(Dispatchers.Main) {
                if (connectionsGeneration.get() != generation) {
                    return@withContext
                }
                updateState {
                    copy(
                        allConnections = snapshot,
                        isLoading = false,
                    )
                }
            }
        }
    }

    private fun buildConnectionLists(connections: Connections): List<Connection> =
        connections.iterator().toList().filter { it.outboundType != "dns" }.map { Connection.from(it) }
}
