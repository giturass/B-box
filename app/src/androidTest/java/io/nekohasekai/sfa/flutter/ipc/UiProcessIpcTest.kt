package io.nekohasekai.sfa.flutter.ipc

import android.content.ComponentName
import android.content.ClipData
import android.content.Context
import android.content.ContextWrapper
import android.content.Intent
import android.content.ServiceConnection
import android.net.Uri
import android.os.Binder
import android.os.Bundle
import android.os.ParcelFileDescriptor
import android.security.KeyChain
import android.system.Os
import androidx.core.content.IntentCompat
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import io.flutter.plugin.common.BinaryMessenger
import io.flutter.plugin.common.EventChannel
import io.flutter.plugin.common.FlutterException
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.MethodChannel
import io.flutter.plugin.common.StandardMethodCodec
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import java.io.Closeable
import java.io.File
import java.nio.ByteBuffer
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicInteger

/** Uses Android's actual anonymous file descriptors and Flutter codecs, without starting a core. */
@RunWith(AndroidJUnit4::class)
class UiProcessIpcTest {
    private val context get() = InstrumentationRegistry.getInstrumentation().targetContext
    private val codec get() = StandardMethodCodec.INSTANCE

    @Test fun largePayloadsKeepBufferPositionsNullsAndErrorDetails() {
        val body = ByteArray(2 * 1024 * 1024 + 17) { (it * 31).toByte() }
        val encoded = codec.encodeMethodCall(MethodCall("节点🔒", mapOf("body" to body, "name" to "中文😀")))
        val bytes = IpcPayload.writtenBytes(encoded)!!
        val descriptor = IpcPayload.write(context, bytes)!!
        assertTrue(descriptor.statSize > 1024 * 1024)
        assertTrue(Os.readlink("/proc/self/fd/${descriptor.fd}").endsWith(" (deleted)"))
        val retained = IpcPayload.retain(descriptor)
        assertFalse(descriptor.fileDescriptor.valid())
        val call = codec.decodeMethodCall(IpcPayload.readableBuffer(IpcPayload.read(retained))!!)
        assertEquals("节点🔒", call.method)
        val args = call.arguments as Map<*, *>
        assertEquals("中文😀", args["name"])
        assertArrayEquals(body, args["body"] as ByteArray)
        val written = IpcPayload.writtenBuffer(bytes)!!
        assertEquals(bytes.size, written.position())
        assertArrayEquals(bytes, IpcPayload.writtenBytes(written))
        val padded = ByteBuffer.allocateDirect(128).apply { put(byteArrayOf(1, 2, 3)) }
        assertArrayEquals(byteArrayOf(1, 2, 3), IpcPayload.writtenBytes(padded))
        assertNull(IpcPayload.write(context, null))
        assertNull(IpcPayload.read(null))
        assertArrayEquals(byteArrayOf(), IpcPayload.read(IpcPayload.write(context, byteArrayOf())))
        val failure = runCatching {
            codec.decodeEnvelope(IpcPayload.readableBuffer(IpcPayload.errorBytes(
                FlutterException("revision", "配置已变更", mapOf("revision" to 73)),
            ))!!)
        }.exceptionOrNull() as FlutterException
        assertEquals("revision", failure.code)
        assertEquals("配置已变更", failure.message)
        assertEquals(73, (failure.details as Map<*, *>)["revision"])
    }

    @Test fun concurrentLargeMessagesAndReverseUiRequestsKeepTheirOwnReplies() = runBlocking {
        withTimeout(30_000) {
            val fixture = FixtureContext(context)
            val engine = EngineFixture()
            val client = withContext(Dispatchers.Main) { UiBridgeClient(fixture, engine, { _, args -> args }) }
            try {
                client.call("echo", mapOf("index" to -1))
                val native = fixture.server.hosts.single()
                val expected = List(5) { id -> ByteArray(1024 * 1024 + 193) { (it + id * 7).toByte() } }
                val requests = expected.mapIndexed { id, bytes -> async {
                    client.call("echo", mapOf("index" to id, "body" to bytes)) as Map<*, *>
                } }
                val reversed = async {
                    native.session.requestUi("ui.echo", mapOf("body" to expected.last())) as Map<*, *>
                }
                requests.awaitAll().forEachIndexed { id, reply ->
                    assertEquals(id, (reply["index"] as Number).toInt())
                    assertArrayEquals(expected[id], reply["body"] as ByteArray)
                }
                assertArrayEquals(expected.last(), reversed.await()["body"] as ByteArray)
                assertEquals(6, native.echoes.size)
            } finally {
                withContext(Dispatchers.Main) { client.close(); fixture.server.close() }
            }
        }
    }

    @Test fun intentMarshallingDropsForeignBindersAndDescriptorsAndKeepsShareFields() {
        val pipe = ParcelFileDescriptor.createPipe()
        try {
            val uri = Uri.parse("content://io.ericlee.bfa.cache/shared.txt")
            val source = Intent(Intent.ACTION_SEND).apply {
                type = "text/plain"
                flags = Intent.FLAG_GRANT_READ_URI_PERMISSION
                clipData = ClipData.newRawUri("Shared file", uri)
                putExtra(Intent.EXTRA_STREAM, uri)
                putExtra(Intent.EXTRA_TEXT, "节点😀")
                putExtras(Bundle().apply { putBinder("foreignBinder", Binder()); putParcelable("foreignFd", pipe[0]) })
            }
            val restored = IpcPayload.unmarshalIntent(IpcPayload.marshalIntent(source))
            assertEquals(Intent.ACTION_SEND, restored.action)
            assertEquals("text/plain", restored.type)
            assertEquals("节点😀", restored.getStringExtra(Intent.EXTRA_TEXT))
            assertEquals(uri, IntentCompat.getParcelableExtra(restored, Intent.EXTRA_STREAM, Uri::class.java))
            assertEquals(uri, restored.clipData!!.getItemAt(0).uri)
            assertEquals(Intent.FLAG_GRANT_READ_URI_PERMISSION, restored.flags)
            assertFalse(restored.hasExtra("foreignBinder"))
            assertFalse(restored.hasExtra("foreignFd"))
            val chooser = IpcPayload.unmarshalIntent(IpcPayload.marshalIntent(Intent.createChooser(source, "分享")))
            val target = IntentCompat.getParcelableExtra(chooser, Intent.EXTRA_INTENT, Intent::class.java)!!
            assertEquals("分享", chooser.getStringExtra(Intent.EXTRA_TITLE))
            assertEquals("节点😀", target.getStringExtra(Intent.EXTRA_TEXT))
            assertFalse(target.hasExtra("foreignBinder"))
            val certificate = byteArrayOf(0x30, 0x01, 0x00)
            val install = IpcPayload.unmarshalIntent(IpcPayload.marshalIntent(KeyChain.createInstallIntent().apply {
                putExtra(KeyChain.EXTRA_CERTIFICATE, certificate)
                putExtra(KeyChain.EXTRA_NAME, "B-box 测试 CA")
            }))
            assertArrayEquals(certificate, install.getByteArrayExtra(KeyChain.EXTRA_CERTIFICATE))
            assertEquals("B-box 测试 CA", install.getStringExtra(KeyChain.EXTRA_NAME))
        } finally {
            pipe.forEach { it.close() }
        }
    }

    @Test fun rebindRestoresForegroundAndEventsAndRejectsMessagesFromTheOldSession() = runBlocking {
        withTimeout(30_000) {
            val fixture = FixtureContext(context)
            val engine = EngineFixture()
            val client = withContext(Dispatchers.Main) {
                UiBridgeClient(fixture, engine, { _, args -> args }).also {
                    it.sendControl("ui.attach", "same-activity")
                    it.updateForeground(true)
                }
            }
            try {
                val first = (client.call("generation") as Number).toInt()
                engine.invoke(EVENTS_CHANNEL, "listen")
                val old = fixture.server.hosts.single()
                while (old.foreground.lastOrNull() != true) delay(1)
                withContext(Dispatchers.Main) { fixture.restart() }
                val second = (client.call("generation") as Number).toInt()
                assertNotEquals(first, second)
                val current = fixture.server.hosts.last()
                while (current.foreground.lastOrNull() != true || current.listens == 0 ||
                    engine.events.none { it["type"] == "fixture" && (it["value"] as Number).toInt() == second }) delay(1)
                assertEquals("ui.attach" to "same-activity", current.controls.first())
                assertTrue(engine.events.any { it["type"] == "backendReconnected" })
                assertEquals(1, current.listens)
                val oldEvent = IpcPayload.writtenBytes(codec.encodeSuccessEnvelope(mapOf("type" to "obsolete")))
                val barrier = IpcPayload.writtenBytes(codec.encodeSuccessEnvelope(mapOf("type" to "barrier")))
                withContext(Dispatchers.IO) {
                    IpcPayload.write(context, oldEvent).use { old.callback.onMessage(EVENTS_CHANNEL, it, 0L) }
                    IpcPayload.write(context, barrier).use { current.callback.onMessage(EVENTS_CHANNEL, it, 0L) }
                }
                while (engine.events.none { it["type"] == "barrier" }) delay(1)
                assertFalse(engine.events.any { it["type"] == "obsolete" })
                assertEquals(second, (engine.events.last { it["type"] == "fixture" }["value"] as Number).toInt())
            } finally {
                withContext(Dispatchers.Main) { client.close(); fixture.server.close() }
            }
        }
    }

    @Test fun prepareExitWaitsForTheHostAndUiDeathReleasesReverseRequests() = runBlocking {
        withTimeout(30_000) {
            val fixture = FixtureContext(context)
            val waiting = CompletableDeferred<Unit>()
            val never = CompletableDeferred<Any?>()
            val client = withContext(Dispatchers.Main) {
                UiBridgeClient(fixture, EngineFixture(), { _, _ -> waiting.complete(Unit); never.await() })
            }
            try {
                client.call("generation")
                val native = fixture.server.hosts.single()
                val prepared = async { client.prepareForExit() }
                native.prepareEntered.await()
                assertFalse(prepared.isCompleted)
                assertTrue(client.connected.value)
                assertEquals(1, fixture.connections.size)
                native.prepareRelease.complete(Unit)
                prepared.await()
                // Returning to the foreground can use this same session after the drain.
                assertEquals(native.id, (client.call("generation") as Number).toInt())
                val reverse = async { runCatching { native.session.requestUi("ui.pending") } }
                waiting.await()
                native.session.binderDied()
                assertTrue(reverse.await().isFailure)
                assertFalse(native.session.isConnected)
                client.connected.first { !it }
                native.session.supervisorJob.join()
                assertTrue(runCatching { client.call("generation") }.isFailure)
            } finally {
                withContext(Dispatchers.Main) { client.close(); fixture.server.close() }
            }
        }
    }

    @Test fun delayedBackgroundExitKeepsAReturnedUiUsableButEngineCleanupStillDrains() = runBlocking {
        withTimeout(30_000) {
            val fixture = FixtureContext(context)
            val client = withContext(Dispatchers.Main) { UiBridgeClient(fixture, EngineFixture(), { _, args -> args }) }
            try {
                client.call("generation")
                client.callControl("ui.foreground", true)
                val native = fixture.server.hosts.single()
                assertTrue(native.session.foreground.value)

                // A cancelled timer may already have queued its control call.
                // The latest foreground state must prevent it from quiescing.
                client.prepareForExit(backgroundOnly = true)
                assertFalse(native.prepareEntered.isCompleted)
                assertEquals(native.id, (client.call("generation") as Number).toInt())

                // Finishing an engine still needs recovery even if Android has
                // not yet delivered the Activity's stopped callback.
                val cleanup = async { client.prepareForExit() }
                native.prepareEntered.await()
                assertFalse(cleanup.isCompleted)
                native.prepareRelease.complete(Unit)
                cleanup.await()
            } finally {
                fixture.server.hosts.forEach { it.prepareRelease.complete(Unit) }
                withContext(Dispatchers.Main) { client.close(); fixture.server.close() }
            }
        }
    }

    @Test fun separateUiSessionsKeepTheirOwnCallbacksAndClosingOnePreservesTheOther() = runBlocking {
        withTimeout(30_000) {
            val fixture = FixtureContext(context)
            val mainEngine = EngineFixture()
            val shareEngine = EngineFixture()
            val mainClient = withContext(Dispatchers.Main) {
                UiBridgeClient(fixture, mainEngine, { _, args -> mapOf("activity" to "main", "value" to args) })
            }
            val shareClient = withContext(Dispatchers.Main) {
                UiBridgeClient(fixture, shareEngine, { _, args -> mapOf("activity" to "share", "value" to args) })
            }
            try {
                mainClient.callControl("ui.attach", "main-activity")
                shareClient.callControl("ui.attach", "share-activity")
                val mainId = (mainClient.call("generation") as Number).toInt()
                val shareId = (shareClient.call("generation") as Number).toInt()
                assertNotEquals(mainId, shareId)
                val mainHost = fixture.server.hosts.single { it.id == mainId }
                val shareHost = fixture.server.hosts.single { it.id == shareId }
                mainEngine.invoke(EVENTS_CHANNEL, "listen")
                shareEngine.invoke(EVENTS_CHANNEL, "listen")
                while (mainEngine.events.none { it["type"] == "fixture" } ||
                    shareEngine.events.none { it["type"] == "fixture" }) delay(1)
                assertTrue(mainEngine.events.all { (it["value"] as Number).toInt() == mainId })
                assertTrue(shareEngine.events.all { (it["value"] as Number).toInt() == shareId })
                val callbacks = listOf(mainHost, shareHost).map { host -> async {
                    host.session.requestUi("ui.echo", host.id) as Map<*, *>
                } }.awaitAll()
                assertEquals("main", callbacks[0]["activity"])
                assertEquals(mainId, (callbacks[0]["value"] as Number).toInt())
                assertEquals("share", callbacks[1]["activity"])
                assertEquals(shareId, (callbacks[1]["value"] as Number).toInt())

                val prepared = async { mainClient.prepareForExit() }
                mainHost.prepareEntered.await()
                assertFalse(prepared.isCompleted)
                assertEquals(shareId, (shareClient.call("generation") as Number).toInt())
                mainHost.prepareRelease.complete(Unit)
                prepared.await()
                withContext(Dispatchers.Main) { mainClient.close() }
                mainHost.session.supervisorJob.join()
                assertFalse(mainHost.session.isConnected)
                assertEquals(1, fixture.connections.size)
                assertTrue(shareClient.connected.value)
                val reply = shareClient.call("echo", mapOf("index" to 42)) as Map<*, *>
                assertEquals(42, (reply["index"] as Number).toInt())
                assertEquals("share", (shareHost.session.requestUi("ui.echo") as Map<*, *>)["activity"])
            } finally {
                withContext(Dispatchers.Main) { mainClient.close(); shareClient.close(); fixture.server.close() }
            }
        }
    }

    @Test fun hostCompletionFailsPendingCallsWithoutReattachingTheOldActivity() = runBlocking {
        withTimeout(30_000) {
            val fixture = FixtureContext(context)
            val client = withContext(Dispatchers.Main) { UiBridgeClient(fixture, EngineFixture(), { _, args -> args }) }
            try {
                client.call("generation")
                val native = fixture.server.hosts.single()
                val prepared = async { runCatching { client.prepareForExit() } }
                native.prepareEntered.await()
                native.session.completeClose()
                assertTrue(prepared.await().isFailure)
                client.connected.first { !it }
                native.session.supervisorJob.join()
                withContext(Dispatchers.Main) {
                    assertFalse(native.session.isConnected)
                    assertTrue(fixture.connections.isEmpty())
                }
                assertTrue(runCatching { client.call("generation") }.isFailure)
                assertEquals(1, fixture.server.hosts.size)
            } finally {
                withContext(Dispatchers.Main) { client.close(); fixture.server.close() }
            }
        }
    }

    @Test fun recoverableClosureFailsOldCallsAndRestoresTheReplacementSession() = runBlocking {
        withTimeout(30_000) {
            val fixture = FixtureContext(context)
            val engine = EngineFixture()
            val client = withContext(Dispatchers.Main) { UiBridgeClient(fixture, engine, { _, args -> args }) }
            try {
                client.callControl("ui.attach", "recovering-activity")
                val original = (client.call("generation") as Number).toInt()
                engine.invoke(EVENTS_CHANNEL, "listen")
                val native = fixture.server.hosts.single()
                val prepared = async { runCatching { client.prepareForExit() } }
                native.prepareEntered.await()
                native.session.disconnect("Fixture transport failed", recoverable = true)
                assertTrue(prepared.await().isFailure)
                val replacement = (client.call("generation") as Number).toInt()
                assertNotEquals(original, replacement)
                val current = fixture.server.hosts.last()
                while (current.listens == 0 || engine.events.none { it["type"] == "backendReconnected" }) delay(1)
                assertEquals("ui.attach" to "recovering-activity", current.controls.first())
                assertEquals(1, fixture.connections.size)
                assertTrue(client.connected.value)
                native.session.supervisorJob.join()
            } finally {
                withContext(Dispatchers.Main) { client.close(); fixture.server.close() }
            }
        }
    }

    @Test fun payloadWriteFailureFailsTheCallAndRebindsAfterStorageRecovers() = runBlocking {
        withTimeout(30_000) {
            val fixture = FixtureContext(context)
            val blocker = File.createTempFile("ipc-blocker-", ".tmp", context.cacheDir)
            val client = withContext(Dispatchers.Main) { UiBridgeClient(fixture, EngineFixture(), { _, args -> args }) }
            try {
                val original = (client.call("generation") as Number).toInt()
                fixture.unavailableCache = File(blocker, "cache")
                assertTrue(runCatching { client.call("echo", mapOf("index" to 1)) }.isFailure)
                fixture.unavailableCache = null
                val replacement = (client.call("generation") as Number).toInt()
                assertNotEquals(original, replacement)
                assertEquals(1, fixture.connections.size)
                assertTrue(client.connected.value)
            } finally {
                fixture.unavailableCache = null
                blocker.delete()
                withContext(Dispatchers.Main) { client.close(); fixture.server.close() }
            }
        }
    }

    @Test fun unreadableReceivedDescriptorRebindsInsteadOfLeavingReadinessPending() = runBlocking {
        withTimeout(30_000) {
            val fixture = FixtureContext(context)
            val client = withContext(Dispatchers.Main) { UiBridgeClient(fixture, EngineFixture(), { _, args -> args }) }
            val pipe = ParcelFileDescriptor.createPipe()
            try {
                val original = (client.call("generation") as Number).toInt()
                val native = fixture.server.hosts.single()
                val disconnected = async(start = CoroutineStart.UNDISPATCHED) { client.connected.first { !it } }
                // Reading a pipe's write-only descriptor fails with EBADF, after actual FD retention.
                withContext(Dispatchers.IO) { native.callback.onMessage(EVENTS_CHANNEL, pipe[1], 0L) }
                disconnected.await()
                val replacement = (client.call("generation") as Number).toInt()
                assertNotEquals(original, replacement)
                assertEquals(1, fixture.connections.size)
                assertTrue(client.connected.value)
            } finally {
                pipe.forEach { it.close() }
                withContext(Dispatchers.Main) { client.close(); fixture.server.close() }
            }
        }
    }

    private class FixtureContext(base: Context) : ContextWrapper(base) {
        @Volatile var unavailableCache: File? = null
        override fun getCacheDir(): File = unavailableCache ?: super.getCacheDir()
        val server = NativeFixture(base)
        val connections = CopyOnWriteArrayList<ServiceConnection>()
        private val component = ComponentName(base, BoxUiService::class.java)

        override fun bindService(service: Intent, conn: ServiceConnection, flags: Int): Boolean {
            connections.add(conn)
            conn.onServiceConnected(component, server)
            return true
        }

        override fun unbindService(conn: ServiceConnection) { connections.remove(conn) }

        fun restart() {
            connections.forEach { it.onServiceDisconnected(component) }
            server.hosts.toList().forEach { it.session.disconnect("Fixture process died") }
            connections.forEach { it.onServiceConnected(component, server) }
        }
    }

    private class NativeFixture(private val context: Context) : IBoxUiService.Stub(), Closeable {
        data class Host(val id: Int, val session: BackendSession, val callback: IBoxUiCallback) {
            val foreground = CopyOnWriteArrayList<Boolean>()
            val controls = CopyOnWriteArrayList<Pair<String, Any?>>()
            val echoes = CopyOnWriteArrayList<Int>()
            @Volatile var listens = 0
            val prepareEntered = CompletableDeferred<Unit>()
            val prepareRelease = CompletableDeferred<Unit>()
        }
        private val nextHost = AtomicInteger()
        val hosts = CopyOnWriteArrayList<Host>()

        override fun openSession(callback: IBoxUiCallback): IBoxUiSession {
            val session = BackendSession(context, callback) {}
            val host = Host(nextHost.incrementAndGet(), session, callback)
            hosts.add(host)
            session.watchUiProcess()
            session.scope.launch {
                MethodChannel(session.messenger, BOX_CHANNEL).setMethodCallHandler { call, result ->
                    when (call.method) {
                        "generation" -> result.success(host.id)
                        "echo" -> session.scope.launch {
                            val index = ((call.arguments as? Map<*, *>)?.get("index") as? Number)?.toInt() ?: 0
                            delay((index.coerceAtLeast(0) % 3 * 7).toLong())
                            host.echoes.add(index)
                            result.success(call.arguments)
                        }
                        else -> result.notImplemented()
                    }
                }
                EventChannel(session.messenger, EVENTS_CHANNEL).setStreamHandler(object : EventChannel.StreamHandler {
                    override fun onListen(arguments: Any?, events: EventChannel.EventSink) {
                        host.listens++
                        events.success(mapOf("type" to "fixture", "value" to host.id))
                    }
                    override fun onCancel(arguments: Any?) = Unit
                })
                session.controlHandler = { method, args ->
                    host.controls.add(method to args)
                    if (method == "ui.foreground") host.foreground.add(args == true)
                    null
                }
                session.prepareExitHandler = { host.prepareEntered.complete(Unit); host.prepareRelease.await() }
                session.attachBackend(Closeable { session.completeClose() })
            }
            return session.binder
        }

        override fun close() { hosts.forEach { it.session.close() } }
    }

    private class EngineFixture : BinaryMessenger {
        private val handlers = ConcurrentHashMap<String, BinaryMessenger.BinaryMessageHandler>()
        val events = CopyOnWriteArrayList<Map<*, *>>()

        override fun send(channel: String, message: ByteBuffer?) = send(channel, message, null)

        override fun send(channel: String, message: ByteBuffer?, callback: BinaryMessenger.BinaryReply?) {
            val bytes = IpcPayload.writtenBytes(message)
            if (channel == EVENTS_CHANNEL && bytes != null) {
                val event = StandardMethodCodec.INSTANCE.decodeEnvelope(IpcPayload.readableBuffer(bytes)!!)
                if (event is Map<*, *>) events.add(event)
            }
            callback?.reply(null)
        }

        override fun setMessageHandler(channel: String, handler: BinaryMessenger.BinaryMessageHandler?) {
            if (handler == null) handlers.remove(channel) else handlers[channel] = handler
        }

        suspend fun invoke(channel: String, method: String, args: Any? = null): Any? {
            val result = CompletableDeferred<Any?>()
            withContext(Dispatchers.Main) {
                val codec = StandardMethodCodec.INSTANCE
                val request = IpcPayload.readableBuffer(IpcPayload.writtenBytes(codec.encodeMethodCall(MethodCall(method, args))))
                handlers.getValue(channel).onMessage(request) { reply ->
                    runCatching {
                        codec.decodeEnvelope(IpcPayload.readableBuffer(IpcPayload.writtenBytes(reply))!!)
                    }.fold(result::complete, result::completeExceptionally)
                }
            }
            return result.await()
        }
    }
}
