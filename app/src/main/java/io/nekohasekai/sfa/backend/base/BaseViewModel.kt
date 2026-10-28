package io.nekohasekai.sfa.backend.base

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/** State shared by the Android services and Flutter bridge. */
abstract class BaseViewModel<State> : ViewModel() {
    private val state by lazy { MutableStateFlow(createInitialState()) }
    val uiState = state.asStateFlow()

    abstract fun createInitialState(): State

    protected val currentState: State get() = state.value

    protected fun updateState(reducer: State.() -> State) = state.update(reducer)

    protected fun sendErrorMessage(message: String) {
        viewModelScope.launch { GlobalEventBus.emit(UiEvent.ErrorMessage(message)) }
    }

    protected fun sendError(error: Throwable) = sendErrorMessage(error.message ?: error.toString())
}
