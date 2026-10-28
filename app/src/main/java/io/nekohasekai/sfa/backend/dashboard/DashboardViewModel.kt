package io.nekohasekai.sfa.backend.dashboard

import androidx.lifecycle.viewModelScope
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.libbox.StatusMessage
import io.nekohasekai.sfa.backend.base.BaseViewModel
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.utils.AppLifecycleObserver
import io.nekohasekai.sfa.utils.CommandClient
import io.nekohasekai.sfa.utils.RemoteControlManager
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

data class DashboardUiState(
    val serviceStatus: Status = Status.Stopped,
    val connectionsCount: Int = 0,
    val serviceStartTime: Long? = null,
    val deprecatedNotes: List<DeprecatedNote> = emptyList(),
    val memory: String = "",
    val goroutines: String = "",
    val trafficVisible: Boolean = false,
    val uplinkTotal: String = "0 B",
    val downlinkTotal: String = "0 B",
    val clashModeVisible: Boolean = false,
    val clashModes: List<String> = emptyList(),
    val selectedClashMode: String = "",
    val systemProxyVisible: Boolean = false,
    val systemProxyEnabled: Boolean = false,
    val systemProxySwitching: Boolean = false,
) {
    data class DeprecatedNote(val message: String, val migrationLink: String?)
}

class DashboardViewModel :
    BaseViewModel<DashboardUiState>(),
    CommandClient.Handler {
    private val _serviceStatus = MutableStateFlow(Status.Stopped)
    val serviceStatus: StateFlow<Status> = _serviceStatus.asStateFlow()

    internal val commandClient =
        CommandClient(
            viewModelScope,
            listOf(
                CommandClient.ConnectionType.Status,
                CommandClient.ConnectionType.ClashMode,
            ),
            this,
        )

    override fun createInitialState() = DashboardUiState()

    init {

        viewModelScope.launch {
            combine(
                AppLifecycleObserver.isForeground,
                RemoteControlManager.remoteServer,
                RemoteControlManager.isConnected,
                _serviceStatus,
            ) { foreground, remoteServer, remoteConnected, status ->
                SessionTarget(
                    connect = foreground &&
                        if (remoteServer != null) remoteConnected else status == Status.Started,
                    remoteServerId = remoteServer?.id,
                )
            }.distinctUntilChanged().collect { target ->
                if (target.connect) {
                    commandClient.connect()
                } else {
                    commandClient.disconnect()
                }
            }
        }
    }

    private data class SessionTarget(val connect: Boolean, val remoteServerId: Long?)

    override fun onCleared() {
        super.onCleared()
        commandClient.disconnect()
    }

    private fun checkDeprecatedNotes() {
        viewModelScope.launch(Dispatchers.IO) {
            runCatching {
                // Check if deprecated warnings are disabled
                if (Settings.disableDeprecatedWarnings) {
                    return@launch
                }

                val notes = Libbox.newStandaloneCommandClient().deprecatedNotes
                if (notes.hasNext()) {
                    val notesList = mutableListOf<DashboardUiState.DeprecatedNote>()
                    while (notes.hasNext()) {
                        val note = notes.next()
                        notesList.add(
                            DashboardUiState.DeprecatedNote(
                                message = note.message(),
                                migrationLink = note.migrationLink,
                            ),
                        )
                    }
                    withContext(Dispatchers.Main) {
                        updateState {
                            copy(
                                deprecatedNotes = notesList,
                            )
                        }
                    }
                }
            }
        }
    }

    fun updateServiceStatus(status: Status) {
        viewModelScope.launch {
            _serviceStatus.emit(status)
            updateState {
                copy(
                    serviceStatus = status,
                )
            }
            handleServiceStatusChange(status)
        }
    }

    private fun handleServiceStatusChange(status: Status) {
        val isRemote = RemoteControlManager.remoteServer.value != null
        when (status) {
            Status.Started -> {
                checkDeprecatedNotes()
                if (isRemote) {
                    return
                }
                reloadSystemProxyStatus()
                reloadStartedAt()
            }

            Status.Stopped -> {
                if (isRemote) {
                    return
                }
                updateState {
                    copy(
                        connectionsCount = 0,
                        serviceStartTime = null,
                        clashModeVisible = false,
                        systemProxyVisible = false,
                        trafficVisible = false,
                        memory = "",
                        goroutines = "",
                        uplinkTotal = "0 B",
                        downlinkTotal = "0 B",
                    )
                }
            }

            else -> {}
        }
    }

    private fun reloadStartedAt() {
        viewModelScope.launch(Dispatchers.IO) {
            try {
                val startedAt = Libbox.newStandaloneCommandClient().startedAt
                withContext(Dispatchers.Main) {
                    updateState {
                        copy(serviceStartTime = startedAt)
                    }
                }
            } catch (_: Exception) {
            }
        }
    }

    private fun reloadSystemProxyStatus() {
        viewModelScope.launch(Dispatchers.IO) {
            try {
                val status = Libbox.newStandaloneCommandClient().systemProxyStatus
                withContext(Dispatchers.Main) {
                    updateState {
                        copy(
                            systemProxyVisible = status.available,
                            systemProxyEnabled = status.enabled,
                        )
                    }
                }
            } catch (e: Exception) {
                // Ignore errors
            }
        }
    }

    fun toggleSystemProxy(enabled: Boolean) {
        if (currentState.systemProxySwitching) return

        viewModelScope.launch(Dispatchers.IO) {
            try {
                updateState { copy(systemProxySwitching = true) }
                Settings.systemProxyEnabled = enabled
                Libbox.newStandaloneCommandClient().setSystemProxyEnabled(enabled)
                delay(1000L)
                withContext(Dispatchers.Main) {
                    updateState {
                        copy(
                            systemProxyEnabled = enabled,
                            systemProxySwitching = false,
                        )
                    }
                }
            } catch (e: Exception) {
                sendError(e)
                updateState { copy(systemProxySwitching = false) }
            }
        }
    }

    // CommandClient.Handler implementation
    override fun onConnected() {
        viewModelScope.launch(Dispatchers.Main) {
            // Returning from remote control skipped the local reloads that
            // normally run when the service starts.
            if (RemoteControlManager.remoteServer.value == null && _serviceStatus.value == Status.Started) {
                reloadSystemProxyStatus()
                reloadStartedAt()
            }
        }
    }

    override fun onDisconnected() {
        viewModelScope.launch(Dispatchers.Main) {
            updateState {
                copy(
                    memory = "",
                    goroutines = "",
                )
            }
        }
    }

    override fun updateStatus(status: StatusMessage) {
        viewModelScope.launch(Dispatchers.Main) {
            updateState {
                // Format the total values
                val newUplinkTotal = Libbox.formatBytes(status.uplinkTotal)
                val newDownlinkTotal = Libbox.formatBytes(status.downlinkTotal)

                copy(
                    memory = Libbox.formatBytes(status.memory),
                    goroutines = status.goroutines.toString(),
                    // Only set trafficVisible to true, never back to false from status updates
                    trafficVisible = if (status.trafficAvailable) true else trafficVisible,
                    connectionsCount = status.connectionsIn,
                    // Only update total values if they've actually changed
                    uplinkTotal = if (newUplinkTotal != uplinkTotal) newUplinkTotal else uplinkTotal,
                    downlinkTotal = if (newDownlinkTotal != downlinkTotal) newDownlinkTotal else downlinkTotal,
                )
            }
        }
    }

    override fun initializeClashMode(modeList: List<String>, currentMode: String) {
        viewModelScope.launch(Dispatchers.Main) {
            updateState {
                copy(
                    clashModeVisible = modeList.size > 1,
                    clashModes = modeList,
                    selectedClashMode = currentMode,
                )
            }
        }
    }

    override fun updateClashMode(newMode: String) {
        viewModelScope.launch(Dispatchers.Main) {
            updateState {
                copy(selectedClashMode = newMode)
            }
        }
    }

}
