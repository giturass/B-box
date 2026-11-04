package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.launch

/** Callback collection and fallback checks both stop in the background or with the screen off. */
internal fun CoroutineScope.refreshOnNetworkChanges(
    foreground: StateFlow<Boolean>,
    screenOn: StateFlow<Boolean>,
    visible: StateFlow<Boolean>,
    changes: Flow<Unit>,
    visibleIntervalMillis: Long = 10_000,
    hiddenIntervalMillis: Long = 60_000,
    sample: suspend () -> Unit,
): Job = launch {
    combine(foreground, screenOn, visible) { active, awake, requested ->
        if (!active || !awake) 0L else if (requested) visibleIntervalMillis else hiddenIntervalMillis
    }.distinctUntilChanged().collectLatest { interval ->
        if (interval == 0L) return@collectLatest
        coroutineScope {
            val requests = Channel<Unit>(Channel.CONFLATED)
            launch { changes.collect { requests.trySend(Unit) } }
            launch {
                while (true) {
                    requests.trySend(Unit)
                    delay(interval)
                }
            }
            // A separate producer drains callbacks while a read is suspended.
            // Keep only one pending refresh, regardless of upstream flow buffers.
            try {
                for (request in requests) sample()
            } finally {
                requests.cancel()
            }
        }
    }
}
