package io.nekohasekai.sfa.backend.dashboard.groups

import androidx.lifecycle.viewModelScope
import io.nekohasekai.libbox.OutboundGroup
import io.nekohasekai.sfa.backend.base.BaseViewModel
import io.nekohasekai.sfa.backend.model.Group
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.utils.AppLifecycleObserver
import io.nekohasekai.sfa.utils.CommandClient
import io.nekohasekai.sfa.utils.RemoteControlManager
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

data class GroupsUiState(val groups: List<Group> = emptyList(), val isLoading: Boolean = false)

class GroupsViewModel : BaseViewModel<GroupsUiState>(), CommandClient.Handler {
    private val commandClient = CommandClient(viewModelScope, CommandClient.ConnectionType.Groups, this)

    private val _serviceStatus = MutableStateFlow(Status.Stopped)
    val serviceStatus = _serviceStatus.asStateFlow()
    private var lastServiceStatus: Status = Status.Stopped

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
                    updateState { copy(isLoading = true) }
                    commandClient.connect()
                } else {
                    commandClient.disconnect()
                }
            }
        }
    }

    private data class SessionTarget(val connect: Boolean, val remoteServerId: Long?)

    override fun createInitialState() = GroupsUiState()

    override fun onCleared() {
        super.onCleared()
        commandClient.disconnect()
    }

    private fun handleServiceStatusChange(status: Status) {
        if (RemoteControlManager.remoteServer.value != null) {
            return
        }
        if (status != Status.Started) {
            updateState {
                copy(
                    groups = emptyList(),
                    isLoading = false,
                )
            }
        }
    }

    fun updateServiceStatus(status: Status) {
        if (status == lastServiceStatus) {
            return
        }
        lastServiceStatus = status
        viewModelScope.launch {
            _serviceStatus.emit(status)
            handleServiceStatusChange(status)
        }
    }

    override fun onDisconnected() {
        // Closing the UI subscription in the background does not change the
        // local route. Keep its snapshot until fresh groups arrive on return.
        val keepSnapshot = !AppLifecycleObserver.isForeground.value &&
            RemoteControlManager.remoteServer.value == null && serviceStatus.value == Status.Started
        viewModelScope.launch(Dispatchers.Main) {
            updateState {
                copy(
                    groups = if (keepSnapshot) groups else emptyList(),
                    isLoading = false,
                )
            }
        }
    }

    override fun updateGroups(newGroups: MutableList<OutboundGroup>) {
        viewModelScope.launch(Dispatchers.Default) {
            val currentGroups = uiState.value.groups
            val currentByTag = currentGroups.associateBy { it.tag }
            val mergedGroups = newGroups.map { goGroup ->
                val converted = Group(goGroup)
                val existing = currentByTag[converted.tag]
                if (existing == converted) existing else converted
            }

            withContext(Dispatchers.Main) {
                updateState {
                    copy(
                        groups = if (mergedGroups == groups) groups else mergedGroups,
                        isLoading = false,
                    )
                }
            }
        }
    }
}
