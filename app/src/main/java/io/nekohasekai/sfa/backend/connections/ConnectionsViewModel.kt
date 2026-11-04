package io.nekohasekai.sfa.backend.connections

import androidx.lifecycle.viewModelScope
import io.nekohasekai.libbox.ConnectionEvents
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.backend.base.BaseViewModel
import io.nekohasekai.sfa.backend.model.Connection
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.utils.AppLifecycleObserver
import io.nekohasekai.sfa.utils.CommandClient
import io.nekohasekai.sfa.utils.RemoteControlManager
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.launch

data class ConnectionsUiState(
    val allConnections: Map<String, Connection> = emptyMap(),
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

    private val connectionsStore = ConnectionStore()
    private val connectionsLock = Any()

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

    private fun handleServiceStatusChange(status: Status) {
        if (RemoteControlManager.remoteServer.value != null) {
            return
        }
        if (status != Status.Started) {
            clearConnections()
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
        clearConnections()
    }

    private fun clearConnections() = synchronized(connectionsLock) {
        val snapshot = connectionsStore.clear()
        updateState { copy(allConnections = snapshot, isLoading = false) }
    }

    // CommandClient invokes this on the native stream thread. Apply before returning
    // so batches and disconnects cannot overtake queued conversion jobs.
    override fun writeConnectionEvents(events: ConnectionEvents) = synchronized(connectionsLock) {
        val iterator = events.iterator()
        val updates = sequence {
            while (iterator.hasNext()) {
                val event = iterator.next()
                val type = when (event.type) {
                    Libbox.ConnectionEventNew -> ConnectionStore.Type.NEW
                    Libbox.ConnectionEventUpdate -> ConnectionStore.Type.UPDATE
                    Libbox.ConnectionEventClosed -> ConnectionStore.Type.CLOSED
                    else -> continue
                }
                yield(ConnectionStore.Event(type, event.id,
                    if (type == ConnectionStore.Type.UPDATE) null else event.connection?.let(Connection::from),
                    event.uplinkDelta, event.downlinkDelta, event.closedAt))
            }
        }
        val snapshot = connectionsStore.apply(events.reset, updates, System.currentTimeMillis())
        updateState { copy(allConnections = snapshot, isLoading = false) }
    }
}
