package io.nekohasekai.sfa.bg

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Deferred
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.getAndUpdate
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

internal data class DefaultInterface(val name: String, val index: Int)

/** Resolve and publish outside Android's callback thread; newer state cancels old work. */
internal class DefaultInterfaceMonitor<N : Any>(
    scope: CoroutineScope,
    private val resolve: suspend (N) -> DefaultInterface?,
    private val retryIntervalMillis: Long = 100,
    private val onFailure: (Exception) -> Unit = {},
) {
    private data class State<N>(
        val network: N? = null,
        val listener: (suspend (DefaultInterface?) -> Unit)? = null,
        val initialPublication: CompletableDeferred<Unit>? = null,
        val revision: Long = 0,
    )

    private val state = MutableStateFlow(State<N>())
    private val worker = scope.launch {
        state.collectLatest { current ->
            val listener = current.listener ?: return@collectLatest
            var missingPublished = false
            while (true) {
                val networkInterface = try {
                    current.network?.let { resolve(it) }
                } catch (error: CancellationException) {
                    if (state.value === current) current.initialPublication?.cancel(error)
                    throw error
                } catch (error: Exception) {
                    onFailure(error)
                    null
                }
                // JNI/native reads can finish after cancellation. Never publish their old result.
                currentCoroutineContext().ensureActive()
                if (state.value !== current) return@collectLatest
                try {
                    if (networkInterface != null || !missingPublished) {
                        listener(networkInterface)
                        currentCoroutineContext().ensureActive()
                        if (state.value !== current) return@collectLatest
                        current.initialPublication?.complete(Unit)
                        missingPublished = networkInterface == null
                    }
                    if (networkInterface != null || current.network == null) return@collectLatest
                } catch (error: CancellationException) {
                    if (state.value === current) current.initialPublication?.cancel(error)
                    throw error
                } catch (error: Exception) {
                    current.initialPublication?.completeExceptionally(error)
                    onFailure(error)
                }
                delay(retryIntervalMillis)
            }
        }
    }

    init {
        worker.invokeOnCompletion {
            state.value.initialPublication?.cancel(CancellationException("Default interface monitor stopped"))
        }
    }

    fun updateNetwork(network: N?) {
        state.update { it.copy(network = network, revision = it.revision + 1) }
    }

    fun setListener(listener: (suspend (DefaultInterface?) -> Unit)?): Deferred<Unit>? {
        val initialPublication = listener?.let { CompletableDeferred<Unit>() }
        val previous = state.getAndUpdate {
            it.copy(listener = listener, initialPublication = initialPublication, revision = it.revision + 1)
        }
        previous.initialPublication?.cancel(CancellationException("Default interface listener replaced or removed"))
        if (!worker.isActive) {
            initialPublication?.cancel(CancellationException("Default interface monitor stopped"))
        }
        return initialPublication
    }

    suspend fun close() {
        setListener(null)
        state.update { it.copy(network = null, revision = it.revision + 1) }
        withContext(NonCancellable) { worker.cancelAndJoin() }
    }
}
