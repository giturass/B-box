package io.nekohasekai.sfa.bg

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.collectLatest
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
                        missingPublished = networkInterface == null
                    }
                    if (networkInterface != null || current.network == null) return@collectLatest
                } catch (error: CancellationException) {
                    throw error
                } catch (error: Exception) {
                    onFailure(error)
                }
                delay(retryIntervalMillis)
            }
        }
    }

    fun updateNetwork(network: N?) {
        state.update { it.copy(network = network, revision = it.revision + 1) }
    }

    fun setListener(listener: (suspend (DefaultInterface?) -> Unit)?) {
        state.update { it.copy(listener = listener, revision = it.revision + 1) }
    }

    suspend fun close() {
        state.update { it.copy(network = null, listener = null, revision = it.revision + 1) }
        withContext(NonCancellable) { worker.cancelAndJoin() }
    }
}
