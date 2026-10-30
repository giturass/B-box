package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class VisiblePollingTest {
    @Test fun samplesOnlyWithVisibleForegroundConsumersAndAnAwakeScreen() = runTest {
        val visible = MutableStateFlow(false)
        val foreground = MutableStateFlow(true)
        val screenOn = MutableStateFlow(true)
        var samples = 0
        val job = pollWhileVisible(visible, foreground, screenOn) { samples++ }
        runCurrent()
        advanceTimeBy(10_000)
        assertEquals(0, samples)
        visible.value = true
        runCurrent()
        assertEquals(1, samples)
        advanceTimeBy(2000)
        runCurrent()
        assertEquals(2, samples)

        for (condition in listOf(visible, foreground, screenOn)) {
            condition.value = false
            runCurrent()
            val previous = samples
            advanceTimeBy(10_000)
            runCurrent()
            assertEquals(previous, samples)
            condition.value = true
            runCurrent()
            assertEquals(previous + 1, samples)
        }
        job.cancel()
    }

    @Test fun hidingCancelsPendingWorkAndResumeStartsAFreshSample() = runTest {
        val visible = MutableStateFlow(true)
        val foreground = MutableStateFlow(true)
        val screenOn = MutableStateFlow(true)
        val pending = CompletableDeferred<Unit>()
        var starts = 0
        var delivered = 0
        val job = pollWhileVisible(visible, foreground, screenOn) {
            starts++
            pending.await()
            delivered++
        }
        runCurrent()
        assertEquals(1, starts)
        visible.value = false
        runCurrent()
        pending.complete(Unit)
        runCurrent()
        assertEquals(0, delivered)
        visible.value = true
        runCurrent()
        assertEquals(2, starts)
        assertEquals(1, delivered)
        job.cancel()
    }
}
