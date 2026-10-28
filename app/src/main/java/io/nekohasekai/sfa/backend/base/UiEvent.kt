package io.nekohasekai.sfa.backend.base

sealed class UiEvent {
    data class ErrorMessage(val message: String) : UiEvent()
}
