package io.nekohasekai.sfa.flutter

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.util.AtomicFile
import androidx.annotation.MainThread
import androidx.annotation.VisibleForTesting
import androidx.core.content.IntentCompat
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap

/** Pending Android input is private application state and is excluded from profile/system backups. */
internal object BoxIntentState {
    private const val SHARE_PREFIX = "bbox-share:"
    private val access = Mutex()
    private val states = mutableMapOf<String, State>()

    suspend fun attach(context: Context, token: String): State = withContext(Dispatchers.Main.immediate) {
        validateToken(token)
        access.withLock {
            states[token]?.let { state ->
                state.replayAfterExit()
                return@withLock state
            }
            val file = File(File(context.noBackupFilesDir, "ui-intents"), token)
            val snapshot = withContext(Dispatchers.IO) {
                val atomic = AtomicFile(file)
                try {
                    atomic.openRead().use { JSONObject(it.readBytes().toString(Charsets.UTF_8)) }
                } catch (missing: java.io.FileNotFoundException) {
                    null
                }
            }
            State(token, file).also { state ->
                if (snapshot != null) state.restore(snapshot)
                states[token] = state
            }
        }
    }

    private fun validateToken(token: String) {
        require(runCatching { UUID.fromString(token).toString() == token }.getOrDefault(false)) {
            "Invalid UI host token"
        }
    }

    @MainThread
    class State internal constructor(private val token: String, private val file: File) {
        data class Input(val id: String, val uri: String) {
            fun toWire(): Map<String, String> = mapOf("id" to id, "uri" to uri)
            fun toJson(): JSONObject = JSONObject().put("id", id).put("uri", uri)
        }

        val pending = ArrayDeque<Input>()
        val shares = ConcurrentHashMap<String, Intent>()
        val inFlight: MutableList<Input> = mutableListOf()
        private var replayOnAttach = false
        private var discarded = false

        /** Keep the delivery identity durable before exposing input to either Flutter session. */
        suspend fun enqueue(uri: String): Input = withContext(Dispatchers.Main.immediate) {
            access.withLock {
                check(!discarded) { "The Activity input state has been discarded" }
                val input = Input(UUID.randomUUID().toString(), uri)
                pending.addLast(input)
                // Persistence failure keeps accepted input in memory for a later retry/exit barrier.
                withContext(NonCancellable) { writeSnapshot() }
                input
            }
        }

        suspend fun takeIntent(): Input? = withContext(Dispatchers.Main.immediate) {
            access.withLock {
                val input = pending.removeFirstOrNull() ?: return@withLock null
                inFlight.add(input)
                try {
                    withContext(NonCancellable) { writeSnapshot() }
                    input
                } catch (error: Exception) {
                    inFlight.remove(input)
                    pending.addFirst(input)
                    throw error
                }
            }
        }

        /** Acknowledgement is durable before returning, including removal of consumed share content. */
        suspend fun completeIntent(id: String) = withContext(Dispatchers.Main.immediate) {
            validateToken(id)
            access.withLock {
                if (discarded) return@withLock
                withContext(NonCancellable) acknowledgement@{
                    // A live form can acknowledge input still pending after the backend restarted.
                    val queuedIndex = pending.indexOfFirst { it.id == id }
                    val queued = if (queuedIndex >= 0) pending.removeAt(queuedIndex) else null
                    val flightIndex = inFlight.indexOfFirst { it.id == id }
                    val flight = if (flightIndex >= 0) inFlight.removeAt(flightIndex) else null
                    val input = flight ?: queued ?: return@acknowledgement
                    val shareKey = input.uri.takeIf { it.startsWith(SHARE_PREFIX) }?.removePrefix(SHARE_PREFIX)
                    val removedShare = shareKey?.let(shares::remove)
                    try {
                        writeSnapshot()
                    } catch (error: Exception) {
                        if (queued != null) pending.add(queuedIndex, queued)
                        if (flight != null) inFlight.add(flightIndex, flight)
                        if (shareKey != null && removedShare != null) shares.putIfAbsent(shareKey, removedShare)
                        throw error
                    }
                }
            }
        }

        /** Called by the host's exit barrier; a replacement host can replay this round once. */
        suspend fun persist() = withContext(Dispatchers.Main.immediate) {
            access.withLock {
                if (discarded) return@withLock
                writeSnapshot()
                replayOnAttach = true
            }
        }

        /** Only an Activity actually finishing discards its input; process death keeps it recoverable. */
        suspend fun discard() = withContext(Dispatchers.Main.immediate) {
            access.withLock {
                if (discarded) return@withLock
                discarded = true
                pending.clear()
                inFlight.clear()
                shares.clear()
                if (states[token] === this@State) states.remove(token)
                withContext(NonCancellable + Dispatchers.IO) { AtomicFile(file).delete() }
            }
        }

        private fun replayable(input: Input): Boolean =
            !input.uri.startsWith(SHARE_PREFIX) || shares.containsKey(input.uri.removePrefix(SHARE_PREFIX))

        /** A cancelled ready response may have persisted input Dart never received. */
        suspend fun requeueUnacknowledged() = withContext(Dispatchers.Main.immediate) {
            access.withLock {
                if (discarded || inFlight.isEmpty()) return@withLock
                requeueInFlight()
                writeSnapshot()
                replayOnAttach = false
            }
        }

        internal fun replayAfterExit() {
            if (!replayOnAttach || discarded) return
            replayOnAttach = false
            requeueInFlight()
        }

        private fun requeueInFlight() {
            val retry = inFlight.filter(::replayable)
            inFlight.clear()
            for (value in retry.asReversed()) pending.addFirst(value)
            pending.removeAll { !replayable(it) }
        }

        internal fun restore(snapshot: JSONObject) {
            val version = snapshot.getInt("version")
            require(version == 1 || version == 2) { "Unsupported UI intent snapshot" }
            val shared = snapshot.getJSONObject("shares")
            for (key in shared.keys()) shares[key] = decodeShare(shared.getJSONObject(key))
            fun inputs(array: JSONArray): List<Input> = (0 until array.length()).map { index ->
                if (version == 1) Input(UUID.randomUUID().toString(), array.getString(index))
                else array.getJSONObject(index).let { value ->
                    Input(value.getString("id").also(::validateToken), value.getString("uri"))
                }
            }
            val retry = inputs(snapshot.getJSONArray("inFlight"))
            val queued = inputs(snapshot.getJSONArray("pending"))
            val seen = mutableSetOf<String>()
            for (input in retry + queued) {
                require(seen.add(input.id)) { "Duplicate UI input identity" }
                if (replayable(input)) pending.addLast(input)
            }
            // Recovered in-flight input is now pending, so repeated live attaches cannot duplicate it.
            inFlight.clear()
        }

        private suspend fun writeSnapshot() {
            val shared = JSONObject()
            for ((key, value) in shares) shared.put(key, encodeShare(value))
            val snapshot = JSONObject()
                .put("version", 2)
                .put("pending", JSONArray(pending.filter(::replayable).map(Input::toJson)))
                .put("inFlight", JSONArray(inFlight.filter(::replayable).map(Input::toJson)))
                .put("shares", shared)
                .toString().toByteArray(Charsets.UTF_8)
            withContext(NonCancellable + Dispatchers.IO) {
                val directory = file.parentFile!!
                check(directory.isDirectory || directory.mkdirs() || directory.isDirectory) { "Unable to create UI intent directory" }
                val atomic = AtomicFile(file)
                val stream = atomic.startWrite()
                try {
                    stream.write(snapshot)
                    atomic.finishWrite(stream)
                } catch (error: Exception) {
                    atomic.failWrite(stream)
                    throw error
                }
            }
        }
    }

    private fun strings(array: JSONArray): List<String> = (0 until array.length()).map(array::getString)

    /** Store only fields B-box reads. Parcel bytes and arbitrary caller extras cannot be persisted. */
    private fun encodeShare(intent: Intent): JSONObject {
        require(intent.action == Intent.ACTION_SEND || intent.action == Intent.ACTION_SEND_MULTIPLE) {
            "Unsupported shared input"
        }
        val uris = if (intent.action == Intent.ACTION_SEND_MULTIPLE) {
            IntentCompat.getParcelableArrayListExtra(intent, Intent.EXTRA_STREAM, Uri::class.java).orEmpty()
        } else listOfNotNull(IntentCompat.getParcelableExtra(intent, Intent.EXTRA_STREAM, Uri::class.java))
        return JSONObject().apply {
            put("action", intent.action)
            put("type", intent.type ?: JSONObject.NULL)
            put("flags", intent.flags)
            put("uris", JSONArray(uris.map(Uri::toString)))
            put("text", intent.getCharSequenceExtra(Intent.EXTRA_TEXT)?.toString() ?: JSONObject.NULL)
        }
    }

    private fun decodeShare(value: JSONObject): Intent {
        val action = value.getString("action")
        require(action == Intent.ACTION_SEND || action == Intent.ACTION_SEND_MULTIPLE) { "Invalid shared input snapshot" }
        return Intent(action).apply {
            if (!value.isNull("type")) type = value.getString("type")
            flags = value.getInt("flags")
            val uris = strings(value.getJSONArray("uris")).map(Uri::parse)
            if (action == Intent.ACTION_SEND_MULTIPLE) {
                putParcelableArrayListExtra(Intent.EXTRA_STREAM, ArrayList(uris))
            } else uris.firstOrNull()?.let { putExtra(Intent.EXTRA_STREAM, it) }
            if (!value.isNull("text")) putExtra(Intent.EXTRA_TEXT, value.getString("text"))
        }
    }

    @VisibleForTesting
    internal suspend fun evictCachedState(token: String) = withContext(Dispatchers.Main.immediate) {
        access.withLock { states.remove(token) }
        Unit
    }
}
