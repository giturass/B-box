package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class NetworkRefreshTest {
    @Test fun networkChangesRefreshImmediatelyAndHotspotsKeepALowFrequencyFallback() = runTest {
        val foreground = MutableStateFlow(true)
        val screen = MutableStateFlow(true)
        val visible = MutableStateFlow(false)
        val changes = MutableSharedFlow<Unit>(extraBufferCapacity = 10)
        var samples = 0
        val job = refreshOnNetworkChanges(foreground, screen, visible, changes) { samples++ }
        runCurrent()
        assertEquals(1, samples)
        advanceTimeBy(59_999)
        runCurrent()
        assertEquals(1, samples)
        changes.emit(Unit)
        runCurrent()
        assertEquals(2, samples)
        advanceTimeBy(1)
        runCurrent()
        assertEquals(3, samples)
        visible.value = true
        runCurrent()
        assertEquals(4, samples)
        advanceTimeBy(10_000)
        runCurrent()
        assertEquals(5, samples)

        for (condition in listOf(foreground, screen)) {
            condition.value = false
            runCurrent()
            val before = samples
            assertEquals(0, changes.subscriptionCount.value)
            changes.emit(Unit)
            advanceTimeBy(120_000)
            runCurrent()
            assertEquals(before, samples)
            condition.value = true
            runCurrent()
            assertEquals(before + 1, samples)
            assertEquals(1, changes.subscriptionCount.value)
        }
        job.cancel()
        runCurrent()
        assertEquals(0, changes.subscriptionCount.value)
    }

    @Test fun callbackBurstsDoNotOverlapReadsAndBackgroundingDiscardsPendingWork() = runTest {
        val foreground = MutableStateFlow(true)
        val screen = MutableStateFlow(true)
        val visible = MutableStateFlow(false)
        val changes = MutableSharedFlow<Unit>(extraBufferCapacity = 100)
        var starts = 0
        var delivered = 0
        var pending = CompletableDeferred<Unit>()
        val job = refreshOnNetworkChanges(foreground, screen, visible, changes) {
            starts++
            pending.await()
            delivered++
        }
        runCurrent()
        repeat(30) { changes.emit(Unit) }
        runCurrent()
        assertEquals(1, starts)
        pending.complete(Unit)
        runCurrent()
        assertEquals(2, starts)
        assertEquals(2, delivered)
        pending = CompletableDeferred()
        changes.emit(Unit)
        runCurrent()
        foreground.value = false
        runCurrent()
        pending.complete(Unit)
        runCurrent()
        assertEquals(2, delivered)
        job.cancel()
    }
}
