package io.nekohasekai.sfa.flutter

import android.content.Intent
import android.net.Uri
import android.os.Binder
import android.os.Bundle
import android.util.AtomicFile
import androidx.core.content.IntentCompat
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File
import java.util.UUID

@RunWith(AndroidJUnit4::class)
class BoxIntentStateTest {
    private val context get() = InstrumentationRegistry.getInstrumentation().targetContext

    private fun withState(test: suspend (String, BoxIntentState.State) -> Unit) = runBlocking {
        val token = UUID.randomUUID().toString()
        val state = BoxIntentState.attach(context, token)
        try {
            withContext(Dispatchers.Main.immediate) { test(token, state) }
        } finally {
            state.discard()
            BoxIntentState.attach(context, token).discard()
        }
    }

    private fun input(uri: String) = BoxIntentState.State.Input(UUID.randomUUID().toString(), uri)

    @Test fun liveAttachmentsShareStateAndExitReplaysInFlightOnlyOnce() = withState { token, state ->
        val first = state.enqueue("sing-box://import-remote-profile?url=first")
        val second = state.enqueue("second")
        assertEquals(first, state.takeIntent())
        assertSame(state, BoxIntentState.attach(context, token))
        assertEquals(listOf(second), state.pending.toList())
        state.persist()
        assertSame(state, BoxIntentState.attach(context, token))
        assertEquals(listOf(first, second), state.pending.toList())
        assertTrue(state.inFlight.isEmpty())
        BoxIntentState.attach(context, token)
        assertEquals(2, state.pending.size)
    }

    @Test fun enqueueAndDeliveryAreDurableWithoutAnExitBarrier() = withState { token, state ->
        val first = state.enqueue("first")
        val second = state.enqueue("second")
        assertEquals(first, state.takeIntent())
        BoxIntentState.evictCachedState(token)
        val recovered = BoxIntentState.attach(context, token)
        assertEquals(listOf(first, second), recovered.pending.toList())
        assertTrue(recovered.inFlight.isEmpty())
        assertEquals(first.toWire(), recovered.takeIntent()?.toWire())
    }

    @Test fun completionIsPersistedBeforeAColdAttachment() = withState { token, state ->
        val completed = state.enqueue("completed")
        val queued = state.enqueue("queued")
        assertEquals(completed, state.takeIntent())
        state.completeIntent(completed.id)
        BoxIntentState.evictCachedState(token)
        val recovered = BoxIntentState.attach(context, token)
        assertEquals(listOf(queued), recovered.pending.toList())
        assertTrue(recovered.inFlight.isEmpty())
    }

    @Test fun aSurvivingFormCanAcknowledgeRestoredPendingInputBeforeAnotherTake() = withState { token, state ->
        val completed = state.enqueue("completed")
        val queued = state.enqueue("queued")
        state.takeIntent()
        BoxIntentState.evictCachedState(token)
        val recovered = BoxIntentState.attach(context, token)
        assertEquals(listOf(completed, queued), recovered.pending.toList())
        recovered.completeIntent(completed.id)
        recovered.completeIntent(completed.id)
        BoxIntentState.evictCachedState(token)
        assertEquals(listOf(queued), BoxIntentState.attach(context, token).pending.toList())
    }

    @Test fun foregroundRecoveryReplaysUndeliveredAndOwnedInputWithTheSameIdentity() = withState { token, state ->
        val undelivered = state.enqueue("cancelled-ready")
        val owned = state.enqueue("open-form")
        val queued = state.enqueue("queued")
        // Model ready completing its durable take before its cancelled outer
        // coroutineScope delivers the result to Dart, then an existing form.
        state.takeIntent()
        state.takeIntent()
        state.requeueUnacknowledged()
        assertEquals(listOf(undelivered, owned, queued), state.pending.toList())
        assertTrue(state.inFlight.isEmpty())
        state.requeueUnacknowledged()
        assertEquals(3, state.pending.size)
        // The surviving form acknowledges its ID even though replay is pending.
        state.completeIntent(owned.id)
        BoxIntentState.evictCachedState(token)
        assertEquals(listOf(undelivered, queued), BoxIntentState.attach(context, token).pending.toList())
    }

    @Test fun identicalUrlsAreAcknowledgedAndRecoveredIndividually() = withState { token, state ->
        val value = "sing-box://import-remote-profile?url=same"
        val first = state.enqueue(value)
        val second = state.enqueue(value)
        assertNotEquals(first.id, second.id)
        assertEquals(first, state.takeIntent())
        assertEquals(second, state.takeIntent())
        assertEquals(listOf(first, second), state.inFlight.toList())
        state.completeIntent(second.id)
        assertEquals(listOf(first), state.inFlight.toList())
        BoxIntentState.evictCachedState(token)
        val recovered = BoxIntentState.attach(context, token)
        assertEquals(listOf(first), recovered.pending.toList())
        assertEquals(first, recovered.takeIntent())
        recovered.completeIntent(first.id)
        BoxIntentState.evictCachedState(token)
        assertTrue(BoxIntentState.attach(context, token).pending.isEmpty())
    }

    @Test fun failedEnqueueAndTakeKeepTheAcceptedIdentityForRetry() = runBlocking {
        val directory = File(context.cacheDir, "ui-intent-test-" + UUID.randomUUID())
        check(directory.mkdirs())
        val blockedParent = File(directory, "blocked").apply { writeText("not a directory") }
        val file = File(blockedParent, "snapshot")
        val token = UUID.randomUUID().toString()
        val state = withContext(Dispatchers.Main.immediate) { BoxIntentState.State(token, file) }
        try {
            val accepted = withContext(Dispatchers.Main.immediate) {
                assertTrue(runCatching { state.enqueue("kept") }.isFailure)
                val accepted = state.pending.single()
                assertTrue(runCatching { state.takeIntent() }.isFailure)
                assertEquals(listOf(accepted), state.pending.toList())
                assertTrue(state.inFlight.isEmpty())
                check(blockedParent.delete())
                assertEquals(accepted, state.takeIntent())
                accepted
            }
            val snapshot = withContext(Dispatchers.IO) { JSONObject(file.readText()) }
            val restored = withContext(Dispatchers.Main.immediate) {
                BoxIntentState.State(token, file).also { it.restore(snapshot) }
            }
            assertEquals(listOf(accepted), restored.pending.toList())
        } finally {
            state.discard()
            directory.deleteRecursively()
        }
    }

    @Test fun failedAcknowledgementKeepsInputAndSharedContentForRetry() = runBlocking {
        val directory = File(context.cacheDir, "ui-intent-test-" + UUID.randomUUID())
        check(directory.mkdirs())
        val blockedParent = File(directory, "blocked").apply { writeText("not a directory") }
        val file = File(blockedParent, "snapshot")
        val state = withContext(Dispatchers.Main.immediate) { BoxIntentState.State(UUID.randomUUID().toString(), file) }
        val shareToken = UUID.randomUUID().toString()
        val first = input("first")
        val shared = input("bbox-share:$shareToken")
        val last = input("last")
        val pendingFirst = input("queued-first")
        val pending = input("queued")
        val pendingLast = input("queued-last")
        try {
            withContext(Dispatchers.Main.immediate) {
                state.shares[shareToken] = Intent(Intent.ACTION_SEND).putExtra(Intent.EXTRA_TEXT, "kept for retry")
                state.inFlight.addAll(listOf(first, shared, last))
                state.pending.addAll(listOf(pendingFirst, pending, pendingLast))
                assertTrue(runCatching { state.completeIntent(shared.id) }.isFailure)
                assertEquals(listOf(first, shared, last), state.inFlight.toList())
                assertEquals("kept for retry", state.shares[shareToken]?.getStringExtra(Intent.EXTRA_TEXT))
                assertTrue(runCatching { state.completeIntent(pending.id) }.isFailure)
                assertEquals(listOf(pendingFirst, pending, pendingLast), state.pending.toList())
                check(blockedParent.delete())
                state.completeIntent(shared.id)
                assertEquals(listOf(first, last), state.inFlight.toList())
                assertFalse(state.shares.containsKey(shareToken))
                state.completeIntent(pending.id)
            }
            val saved = withContext(Dispatchers.IO) { JSONObject(file.readText()) }
            val savedInFlight = saved.getJSONArray("inFlight")
            assertEquals(listOf(first.id, last.id), (0 until savedInFlight.length()).map {
                savedInFlight.getJSONObject(it).getString("id")
            })
            assertEquals(0, saved.getJSONObject("shares").length())
        } finally {
            state.discard()
            directory.deleteRecursively()
        }
    }

    @Test fun coldSnapshotsRestoreTextAndMultipleContentUrisWithoutCallerBinderExtras() = withState { token, state ->
        val textToken = UUID.randomUUID().toString()
        val fileToken = UUID.randomUUID().toString()
        val uris = arrayListOf(Uri.parse("content://bbox-fixture/first"), Uri.parse("content://bbox-fixture/second"))
        state.shares[textToken] = Intent(Intent.ACTION_SEND).apply {
            type = "text/plain"
            putExtra(Intent.EXTRA_TEXT, "共享文字\nsecond line")
            putExtras(Bundle().apply { putBinder("unused-caller-binder", Binder()) })
        }
        state.shares[fileToken] = Intent(Intent.ACTION_SEND_MULTIPLE).apply {
            type = "application/octet-stream"
            flags = Intent.FLAG_GRANT_READ_URI_PERMISSION
            putParcelableArrayListExtra(Intent.EXTRA_STREAM, uris)
        }
        val text = state.enqueue("bbox-share:$textToken")
        val files = state.enqueue("bbox-share:$fileToken")
        assertEquals(text, state.takeIntent())
        BoxIntentState.evictCachedState(token)
        val recovered = BoxIntentState.attach(context, token)
        assertEquals(listOf(text, files), recovered.pending.toList())
        assertEquals("共享文字\nsecond line", recovered.shares[textToken]?.getCharSequenceExtra(Intent.EXTRA_TEXT))
        assertFalse(recovered.shares[textToken]!!.hasExtra("unused-caller-binder"))
        assertEquals(uris, IntentCompat.getParcelableArrayListExtra(recovered.shares[fileToken]!!, Intent.EXTRA_STREAM, Uri::class.java))
        assertEquals(Intent.FLAG_GRANT_READ_URI_PERMISSION, recovered.shares[fileToken]?.flags)
        recovered.completeIntent(text.id)
        assertFalse(recovered.shares.containsKey(textToken))
        BoxIntentState.evictCachedState(token)
        assertFalse(BoxIntentState.attach(context, token).shares.containsKey(textToken))
    }

    @Test fun atomicRecoveryKeepsTheLastCompleteSnapshot() = withState { token, state ->
        val preserved = state.enqueue("preserved")
        val atomic = AtomicFile(File(File(context.noBackupFilesDir, "ui-intents"), token))
        withContext(Dispatchers.IO) {
            // Close without finish/fail to model process death during an AtomicFile replacement.
            atomic.startWrite().use { it.write("{\"version\":".toByteArray()) }
        }
        BoxIntentState.evictCachedState(token)
        assertEquals(listOf(preserved), BoxIntentState.attach(context, token).pending.toList())
    }

    @Test fun concurrentEnqueueAndTakePersistEveryIdentityOnce() = withState { token, state ->
        val produced = coroutineScope {
            val start = CompletableDeferred<Unit>()
            val enqueues = (0 until 8).map { index ->
                async(Dispatchers.Default) { start.await(); state.enqueue("input-$index") }
            }
            val takes = (0 until 8).map {
                async(Dispatchers.Default) { start.await(); state.takeIntent() }
            }
            start.complete(Unit)
            val accepted = enqueues.awaitAll()
            takes.awaitAll()
            accepted
        }
        val expected = state.inFlight.toList() + state.pending.toList()
        assertEquals(produced.map { it.id }.toSet(), expected.map { it.id }.toSet())
        assertEquals(8, expected.size)
        BoxIntentState.evictCachedState(token)
        assertEquals(expected, BoxIntentState.attach(context, token).pending.toList())
    }

    @Test fun versionOneMigrationKeepsDuplicateOccurrencesAndPersistsStableIds() = withState { token, _ ->
        val value = "sing-box://import-remote-profile?url=same"
        val snapshot = JSONObject()
            .put("version", 1)
            .put("inFlight", JSONArray(listOf(value, value)))
            .put("pending", JSONArray(listOf("queued")))
            .put("shares", JSONObject())
        val file = File(File(context.noBackupFilesDir, "ui-intents"), token)
        withContext(Dispatchers.IO) {
            check(file.parentFile!!.isDirectory || file.parentFile!!.mkdirs() || file.parentFile!!.isDirectory)
            val atomic = AtomicFile(file)
            val stream = atomic.startWrite()
            stream.write(snapshot.toString().toByteArray())
            atomic.finishWrite(stream)
        }
        BoxIntentState.evictCachedState(token)
        val migrated = BoxIntentState.attach(context, token)
        val expected = migrated.pending.toList()
        assertEquals(listOf(value, value, "queued"), expected.map { it.uri })
        assertEquals(3, expected.map { it.id }.toSet().size)
        assertEquals(expected.first(), migrated.takeIntent())
        assertEquals(2, withContext(Dispatchers.IO) { JSONObject(file.readText()).getInt("version") })
        BoxIntentState.evictCachedState(token)
        assertEquals(expected, BoxIntentState.attach(context, token).pending.toList())
    }

    @Test fun consumedSharesAndFinishedActivitiesDoNotReturnAfterRestart() = withState { token, state ->
        val shareToken = UUID.randomUUID().toString()
        val shared = "bbox-share:$shareToken"
        state.shares[shareToken] = Intent(Intent.ACTION_SEND).putExtra(Intent.EXTRA_TEXT, "text")
        state.enqueue(shared)
        state.takeIntent()
        // Sending already consumed the source before Dart's final acknowledgement arrives.
        state.shares.remove(shareToken)
        state.persist()
        BoxIntentState.evictCachedState(token)
        val recovered = BoxIntentState.attach(context, token)
        assertTrue(recovered.pending.isEmpty())
        recovered.enqueue("discarded")
        recovered.persist()
        recovered.discard()
        recovered.persist()
        val file = File(File(context.noBackupFilesDir, "ui-intents"), token)
        assertFalse(file.exists())
        assertTrue(BoxIntentState.attach(context, token).pending.isEmpty())
        assertTrue(runCatching { BoxIntentState.attach(context, "../outside") }.isFailure)
    }
}
