package io.nekohasekai.sfa.backend.base

import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.asSharedFlow

/**
 * Backend errors observed by the Flutter host.
 */
object GlobalEventBus {
    private val _events =
        MutableSharedFlow<UiEvent>(
            replay = 0,
            extraBufferCapacity = 10,
        )

    val events: SharedFlow<UiEvent> = _events.asSharedFlow()

    /**
     * Emit an event to the global event bus.
     * This should be called by ViewModels to send events that need global handling.
     */
    suspend fun emit(event: UiEvent) {
        _events.emit(event)
    }

}
