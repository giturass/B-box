package io.nekohasekai.sfa.database.preference

import android.util.Log
import androidx.preference.PreferenceDataStore
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

@Suppress("MemberVisibilityCanBePrivate", "unused")
open class RoomPreferenceDataStore(createDao: () -> KeyValueEntity.Dao) : PreferenceDataStore() {
    private val kvPairDao by lazy(createDao)
    private val writeAccess = Any()
    private val pendingWrites = Channel<() -> Unit>(Channel.UNLIMITED)
    private class Mutation(val values: Map<String, KeyValueEntity?>, val reset: Boolean, val optimistic: Boolean) {
        fun applyTo(target: MutableMap<String, KeyValueEntity>) {
            if (reset) target.clear()
            values.forEach { (key, value) -> if (value == null) target.remove(key) else target[key] = value }
        }
    }
    private class State(val committed: MutableMap<String, KeyValueEntity>) {
        val pending = mutableListOf<Mutation>()
        @Volatile var visible: Map<String, KeyValueEntity> = committed.toMap()
    }
    private val state by lazy {
        State(kvPairDao.all().associateByTo(mutableMapOf()) { it.key })
    }
    private val cache get() = state.visible
    // Only the serial writer accesses this; each flush reports failures since the last flush.
    private var writeError: Exception? = null

    init {
        CoroutineScope(SupervisorJob() + Dispatchers.IO).launch {
            for (write in pendingWrites) {
                write()
            }
        }
    }

    suspend fun initialize() = withContext(Dispatchers.IO) {
        cache.size
        Unit
    }

    suspend fun flush() {
        val completion = CompletableDeferred<Unit>()
        synchronized(writeAccess) {
            check(pendingWrites.trySend {
                val error = writeError
                writeError = null
                if (error == null) completion.complete(Unit) else completion.completeExceptionally(error)
            }.isSuccess)
        }
        completion.await()
    }

    /** Commit a related set in one Room insert transaction and report write failures. */
    suspend fun putBatchConfirmed(entities: List<KeyValueEntity>) {
        initialize()
        val copies = entities.map { entity ->
            KeyValueEntity(entity.key).apply { valueType = entity.valueType; value = entity.value.copyOf() }
        }
        val completion = CompletableDeferred<Unit>()
        enqueue(Mutation(copies.associateBy { it.key }, reset = false, optimistic = false), completion) {
            kvPairDao.putAll(copies)
        }
        // A caller rolling back after cancellation must not race an unobserved queued commit.
        withContext(NonCancellable) { completion.await() }
    }

    private fun put(entity: KeyValueEntity) {
        enqueue(Mutation(mapOf(entity.key to entity), reset = false, optimistic = true)) { kvPairDao.put(entity) }
    }

    private fun enqueue(mutation: Mutation, completion: CompletableDeferred<Unit>? = null, write: () -> Unit) {
        val current = state
        val changed: Set<String>
        synchronized(writeAccess) {
            current.pending.add(mutation)
            changed = publish(current)
            check(pendingWrites.trySend {
                val error = runCatching(write).exceptionOrNull()
                val committedChanges = synchronized(writeAccess) {
                    if (error == null) mutation.applyTo(current.committed)
                    current.pending.remove(mutation)
                    publish(current)
                }
                committedChanges.forEach(::fireChangeListener)
                if (error == null) completion?.complete(Unit)
                else if (completion != null) completion.completeExceptionally(error)
                else {
                    val failure = error as? Exception ?: IllegalStateException("Unable to save preferences", error)
                    if (writeError == null) writeError = failure
                    else if (writeError !== failure && writeError!!.suppressed.size < 16) writeError!!.addSuppressed(failure)
                    Log.e("RoomPreferenceDataStore", "write preferences", error)
                }
            }.isSuccess)
        }
        changed.forEach(::fireChangeListener)
    }

    /** Rebuild from durable values so a failed/confirmed write cannot erase newer optimistic writes. */
    private fun publish(current: State): Set<String> {
        val visible = current.committed.toMutableMap()
        current.pending.filter { it.optimistic }.forEach { it.applyTo(visible) }
        val changed = (current.visible.keys + visible.keys).filterTo(mutableSetOf()) { key ->
            val before = current.visible[key]
            val after = visible[key]
            before?.valueType != after?.valueType || (before != null && after != null && !before.value.contentEquals(after.value))
        }
        current.visible = visible
        return changed
    }

    fun getBoolean(key: String) = cache[key]?.boolean

    fun getFloat(key: String) = cache[key]?.float

    fun getInt(key: String) = cache[key]?.long?.toInt()

    fun getLong(key: String) = cache[key]?.long

    fun getString(key: String) = cache[key]?.string

    fun getStringSet(key: String) = cache[key]?.stringSet

    fun reset() {
        enqueue(Mutation(emptyMap(), reset = true, optimistic = true)) { kvPairDao.reset() }
    }

    override fun getBoolean(key: String, defValue: Boolean) = getBoolean(key) ?: defValue

    override fun getFloat(key: String, defValue: Float) = getFloat(key) ?: defValue

    override fun getInt(key: String, defValue: Int) = getInt(key) ?: defValue

    override fun getLong(key: String, defValue: Long) = getLong(key) ?: defValue

    override fun getString(key: String, defValue: String?) = getString(key) ?: defValue

    override fun getStringSet(key: String, defValue: MutableSet<String>?) = getStringSet(key) ?: defValue

    fun putBoolean(key: String, value: Boolean?) = if (value == null) remove(key) else putBoolean(key, value)

    fun putFloat(key: String, value: Float?) = if (value == null) remove(key) else putFloat(key, value)

    fun putInt(key: String, value: Int?) = if (value == null) remove(key) else putLong(key, value.toLong())

    fun putLong(key: String, value: Long?) = if (value == null) remove(key) else putLong(key, value)

    override fun putBoolean(key: String, value: Boolean) = put(KeyValueEntity(key).put(value))

    override fun putFloat(key: String, value: Float) = put(KeyValueEntity(key).put(value))

    override fun putInt(key: String, value: Int) = put(KeyValueEntity(key).put(value.toLong()))

    override fun putLong(key: String, value: Long) = put(KeyValueEntity(key).put(value))

    override fun putString(key: String, value: String?) = if (value == null) {
        remove(key)
    } else {
        put(KeyValueEntity(key).put(value))
    }

    override fun putStringSet(key: String, values: MutableSet<String>?) = if (values == null) {
        remove(key)
    } else {
        put(KeyValueEntity(key).put(values))
    }

    fun remove(key: String) {
        enqueue(Mutation(mapOf(key to null), reset = false, optimistic = true)) { kvPairDao.delete(key) }
    }

    private val listeners = HashSet<OnPreferenceDataStoreChangeListener>()

    private fun fireChangeListener(key: String) {
        val listeners =
            synchronized(listeners) {
                listeners.toList()
            }
        listeners.forEach { listener ->
            try {
                listener.onPreferenceDataStoreChanged(this, key)
            } catch (error: Exception) {
                Log.e("RoomPreferenceDataStore", "preference listener", error)
            }
        }
    }

    fun registerChangeListener(listener: OnPreferenceDataStoreChangeListener) {
        synchronized(listeners) {
            listeners.add(listener)
        }
    }

    fun unregisterChangeListener(listener: OnPreferenceDataStoreChangeListener) {
        synchronized(listeners) {
            listeners.remove(listener)
        }
    }
}
