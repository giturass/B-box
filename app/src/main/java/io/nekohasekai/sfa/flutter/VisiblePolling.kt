package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

internal fun CoroutineScope.pollWhileVisible(
    visible: StateFlow<Boolean>,
    foreground: StateFlow<Boolean>,
    screenOn: StateFlow<Boolean>,
    intervalMillis: Long = 2000,
    sample: suspend () -> Unit,
): Job = launch {
    combine(visible, foreground, screenOn) { requested, active, awake -> requested && active && awake }
        .distinctUntilChanged().collectLatest { active ->
            if (!active) return@collectLatest
            while (isActive) {
                sample()
                delay(intervalMillis)
            }
        }
}
