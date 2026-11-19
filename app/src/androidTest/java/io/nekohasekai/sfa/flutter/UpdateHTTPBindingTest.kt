package io.nekohasekai.sfa.flutter

import androidx.test.ext.junit.runners.AndroidJUnit4
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.libbox.UIHTTPDownloadProgress
import io.nekohasekai.sfa.Application
import io.nekohasekai.sfa.utils.HTTPClient
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import java.io.Closeable
import java.io.File
import java.net.InetAddress
import java.net.ServerSocket
import java.util.UUID
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference
import kotlin.concurrent.thread

@RunWith(AndroidJUnit4::class)
class UpdateHTTPBindingTest {
    @Test fun apkBindingRejectsAnUnencryptedAddressWithoutCreatingACacheFile() = runBlocking(Dispatchers.IO) {
        Application.application.setupLibbox()
        val target = File(Application.application.cacheDir, "update-binding-${UUID.randomUUID()}.part")
        val task = Libbox.newUIHTTPTask()
        try {
            val observer = object : UIHTTPDownloadProgress {
                override fun update(downloaded: Long, total: Long) = error("Rejected downloads cannot report stored bytes")
            }
            assertTrue(runCatching { task.downloadFile("http://127.0.0.1:9/update.apk", target.absolutePath, 42, observer) }.isFailure)
            assertFalse(target.exists())
            assertTrue(runCatching { task.downloadFile("https://127.0.0.1:9/update.apk", target.absolutePath, 0, observer) }.isFailure)
            assertFalse(target.exists())
        } finally { task.close(); target.delete() }
    }

    @Test fun metadataCoroutineCancellationStopsTheBlockingBoundTaskAndAllowsRetry() = runBlocking(Dispatchers.IO) {
        Application.application.setupLibbox()
        for (dispatcher in listOf(Dispatchers.IO, Dispatchers.Default)) {
            MetadataServer(blockBody = true).use { server ->
                val request = async(dispatcher) { HTTPClient.withTask { it.getWithTimeout(server.url, 30_000) } }
                withTimeout(10_000) { while (server.requests.get() == 0) delay(10) }
                withTimeout(5_000) { request.cancelAndJoin() }
                assertTrue(request.isCancelled)
            }
        }
        MetadataServer(blockBody = false).use { server ->
            val response = withTimeout(5_000) { HTTPClient.withTask { it.getWithTimeout(server.url, 4_000) } }
            assertEquals(200, response.status)
            assertEquals(server.content, response.content.toString(Charsets.UTF_8))
        }
    }

    private class MetadataServer(blockBody: Boolean) : Closeable {
        private val socket = ServerSocket(0, 1, InetAddress.getByName("127.0.0.1"))
        val url = "http://127.0.0.1:${socket.localPort}/metadata.json"
        val content = "{\"version_name\":\"1.0.2\"}"
        val requests = AtomicInteger()
        val release = CountDownLatch(if (blockBody) 1 else 0)
        private val failure = AtomicReference<Throwable?>()
        @Volatile private var closing = false
        private val worker = thread(name = "update-metadata-fixture", isDaemon = true) {
            try {
                socket.accept().use { client ->
                    client.soTimeout = 10_000
                    val input = client.getInputStream().bufferedReader()
                    while (input.readLine()?.isNotEmpty() == true) { }
                    val bytes = content.toByteArray()
                    val output = client.getOutputStream()
                    output.write("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: ${bytes.size}\r\nConnection: close\r\n\r\n".toByteArray())
                    output.flush()
                    requests.incrementAndGet()
                    check(release.await(10, TimeUnit.SECONDS)) { "Timed out waiting to release metadata fixture" }
                    output.write(bytes)
                    output.flush()
                }
            } catch (error: Throwable) { if (!closing) failure.set(error) }
        }

        override fun close() {
            closing = true
            release.countDown()
            socket.close()
            worker.join(3_000)
            check(!worker.isAlive) { "Metadata fixture did not stop" }
            failure.get()?.let { throw AssertionError("Metadata fixture failed", it) }
        }
    }
}
