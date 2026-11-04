package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.conflate
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.merge
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
        val fallback = flow {
            while (true) {
                emit(Unit)
                delay(interval)
            }
        }
        // Finish the current refresh, then handle the newest change. Callback bursts
        // cannot grow a queue or repeatedly cancel a profile read already in progress.
        merge(changes, fallback).conflate().collect { sample() }
    }
}
