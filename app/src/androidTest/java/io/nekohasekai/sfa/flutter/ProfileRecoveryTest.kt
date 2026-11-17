package io.nekohasekai.sfa.flutter

import android.annotation.SuppressLint
import android.content.Context
import android.os.Build
import androidx.concurrent.futures.CallbackToFutureAdapter
import androidx.core.content.FileProvider
import androidx.lifecycle.ViewModelProvider
import androidx.room.Room
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.work.Configuration
import androidx.work.ListenableWorker
import androidx.work.WorkerFactory
import androidx.work.WorkerParameters
import androidx.work.impl.ExecutionListener
import androidx.work.impl.WorkManagerImpl
import androidx.work.impl.model.WorkGenerationalId
import androidx.work.impl.model.generationalId
import androidx.work.testing.SynchronousExecutor
import androidx.work.testing.WorkManagerTestInitHelper
import com.google.common.util.concurrent.ListenableFuture
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.libbox.OutboundGroup
import io.nekohasekai.sfa.Application
import io.nekohasekai.sfa.backend.log.LogLevel
import io.nekohasekai.sfa.backend.log.LogViewModel
import io.nekohasekai.sfa.bg.UpdateProfileWork
import io.nekohasekai.sfa.bg.BoxService
import io.nekohasekai.sfa.compose.MainActivity
import io.nekohasekai.sfa.constant.Path
import io.nekohasekai.sfa.constant.SettingsKey
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.database.ProfileContent
import io.nekohasekai.sfa.database.ProfileManager
import io.nekohasekai.sfa.database.ProfileNodeSubscription
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.database.TypedProfile
import io.nekohasekai.sfa.database.preference.KeyValueDatabase
import io.nekohasekai.sfa.vendor.PackageQueryManager
import io.nekohasekai.sfa.utils.CommandClient
import io.nekohasekai.sfa.utils.CommandTarget
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.json.JSONArray
import org.json.JSONObject
import java.io.Closeable
import java.io.DataInputStream
import java.io.File
import java.io.IOException
import java.io.InputStream
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.net.SocketException
import java.net.URLEncoder
import java.security.KeyStore
import java.security.SecureRandom
import java.security.cert.X509Certificate
import java.util.Date
import java.util.UUID
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.CountDownLatch
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLContext
import javax.net.ssl.SNIHostName
import javax.net.ssl.SSLSocket
import javax.net.ssl.TrustManagerFactory
import kotlin.concurrent.thread

@RunWith(AndroidJUnit4::class)
class ProfileRecoveryTest {
    @Test fun profileHTTPRewritingUsesKeystoreRoutingAndReloadRecovery() {
        withRunningProfile { activity, profiles, id ->
            val authority = MitmCertificate(activity)
            val createdCA = authority.snapshot()["exists"] != true
            val generated = if (createdCA) authority.generate(authority.snapshot()["revision"] as String) else authority.snapshot()
            val caFile = File(activity.noBackupFilesDir, "http-debug/mitm-authority.json")
            val createdAlias = if (createdCA) JSONObject(caFile.readText()).getString("alias") else null
            val importedFiles = mutableListOf<File>()
            try {
                val (fileName, fileResource) = importResourceDocument(activity, profiles, id, "http_rewrite", ".json",
                    """{"message":"word word"}""").also { importedFiles.add(it.second) }
                val (scriptName, _) = importResourceDocument(activity, profiles, id, "scripts", ".js",
                    "${'$'}done({body:${'$'}response.body.replace('paired-jq','from-file-script')});").also { importedFiles.add(it.second) }
                val imported = JSONObject(profiles.read(id)).getJSONObject("experimental")
                val certificate = authority.publicCertificate(generated["revision"] as String)
                val trustStore = KeyStore.getInstance(KeyStore.getDefaultType()).apply { load(null); setCertificateEntry("mitm", certificate) }
                val managers = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm()).apply { init(trustStore) }
                val ssl = SSLContext.getInstance("TLS").apply { init(null, managers.trustManagers, SecureRandom()) }
                val proxyPort = ServerSocket(0, 1, InetAddress.getByName("127.0.0.1")).use { it.localPort }
                fun request(host: String, port: Int, path: String, secure: Boolean): String = Socket("127.0.0.1", proxyPort).use { tunnel ->
                    tunnel.soTimeout = 10_000
                    tunnel.getOutputStream().write("CONNECT $host:$port HTTP/1.1\r\nHost: $host:$port\r\n\r\n".toByteArray())
                    val reply = StringBuilder()
                    while (!reply.endsWith("\r\n\r\n")) {
                        val value = tunnel.getInputStream().read()
                        check(value >= 0 && reply.length < 8192) { "Invalid rewrite CONNECT response: $reply" }
                        reply.append(value.toChar())
                    }
                    assertTrue(reply.toString().startsWith("HTTP/1.1 200"))
                    val client = if (secure) (ssl.socketFactory.createSocket(tunnel, host, port, true) as SSLSocket).apply {
                        soTimeout = 10_000
                        sslParameters = sslParameters.apply { endpointIdentificationAlgorithm = "HTTPS"; serverNames = listOf(SNIHostName(host)) }
                        startHandshake()
                        (session.peerCertificates[0] as X509Certificate).verify(certificate.publicKey)
                    } else tunnel
                    client.use {
                        it.getOutputStream().write("GET $path HTTP/1.1\r\nHost: $host:$port\r\nConnection: close\r\n\r\n".toByteArray())
                        it.getInputStream().readBytes().toString(Charsets.UTF_8)
                    }
                }
                SubscriptionServer("""{"message":"old"}""").use { origin ->
                    val originPort = java.net.URI(origin.url).port
                    val extension = JSONObject("""{
                        "enabled":true,"ports":[80,443],
                        "map_local":[{"pattern":"/static$","data":"local-response","status":201}],
                        "map_remote":[{"pattern":"^http://source\\.test:80/remote$","replacement":"http://mapped.test:$originPort/profile.json"}],
                        "body_rewrite":[{"pattern":"/remote$","phase":"response","regex":"old","replacement":"static"}]
                    }""")
                    extension.put("resources", imported.getJSONObject("http_rewrite").getJSONObject("resources"))
                    extension.getJSONArray("map_local").put(JSONObject().put("pattern", "/file$")
                        .put("resource", fileName).put("status", 999))
                    extension.getJSONArray("body_rewrite")
                        .put(JSONObject().put("pattern", "/file$").put("phase", "response")
                            .put("regex", "(?<=:\")([a-z]+) \\1(?=\")").put("replacement", "paired"))
                        .put(JSONObject().put("pattern", "/file$").put("phase", "response").put("jq", ".message += \"-jq\""))
                    val scripts = JSONObject().put("enabled", true).put("http", JSONArray()
                        .put(JSONObject().put("pattern", "/dynamic$").put("phase", "request")
                            .put("source", "${'$'}done({response:{status:202,body:'script-mock'}});"))
                        .put(JSONObject().put("pattern", "/remote$").put("phase", "response").put("requires_body", true)
                            .put("source", "${'$'}done({body:${'$'}response.body.replace('static','script')});"))
                        .put(JSONObject().put("pattern", "/file$").put("phase", "response").put("requires_body", true)
                            .put("resource", scriptName).put("debug", true)))
                        .put("resources", imported.getJSONObject("scripts").getJSONObject("resources"))
                    val config = JSONObject("""{
                        "inbounds":[{"type":"mixed","tag":"rewrite-in","listen":"127.0.0.1","listen_port":$proxyPort}],
                        "outbounds":[{"type":"direct","tag":"original"},{"type":"direct","tag":"mapped"}],
                        "route":{"rules":[
                            {"domain":["offline.test","source.test"],"action":"route","outbound":"original","override_address":"127.0.0.1"},
                            {"inbound":["rewrite-in"],"domain":["mapped.test"],"action":"route","outbound":"mapped","override_address":"127.0.0.1"}
                        ]}
                    }""").put("experimental", JSONObject().put("http_rewrite", extension).put("scripts", scripts)
                        .put("mitm", JSONObject().put("enabled", true).put("hosts", JSONArray().put("offline.test")))).toString()
                    profiles.writeContent(id, config, BoxProfiles.revision(profiles.read(id)))
                    assertEquals(Status.Started, activity.serviceStatus.value)
                    assertTrue(JSONObject(Libbox.readUICaptureStatus()).getBoolean("rewriteActive"))
                    assertFalse(JSONObject(Libbox.readUICaptureStatus()).getBoolean("active"))
                    val static = request("offline.test", 443, "/static", true)
                    assertTrue(static.startsWith("HTTP/1.1 201"))
                    assertTrue(static.endsWith("local-response"))
                    assertTrue(request("offline.test", 443, "/dynamic", true).endsWith("script-mock"))
                    val fileMock = request("offline.test", 443, "/file", true)
                    assertTrue(fileMock.startsWith("HTTP/1.1 999"))
                    assertTrue(fileMock.contains("Content-Type: application/json", ignoreCase = true))
                    assertTrue(fileMock.endsWith("""{"message":"from-file-script"}"""))
                    fileResource.writeText("""{"message":"fresh"}""")
                    assertTrue(request("offline.test", 443, "/file", true).endsWith("""{"message":"fresh-jq"}"""))
                    assertEquals(0, origin.requests.get())

                    val captures = File(activity.noBackupFilesDir, "http-debug/captures")
                    Libbox.openUICapture(captures.absolutePath)
                    Libbox.clearUICapture()
                    BoxService.dashboardServer!!.startUICapture(captures.absolutePath, "{}", "", null)
                    assertTrue(request("source.test", 80, "/remote", false).contains("\"message\":\"script\""))
                    assertEquals(1, origin.requests.get())
                    Libbox.stopUICapture()
                    assertTrue(JSONObject(Libbox.readUICaptureStatus()).getBoolean("rewriteActive"))
                    assertFalse(JSONObject(Libbox.readUICaptureStatus()).getBoolean("active"))
                    assertTrue(request("offline.test", 443, "/static", true).endsWith("local-response"))
                    assertTrue(runCatching { authority.generate(authority.snapshot()["revision"] as String) }.isFailure)

                    // A failed new listener must restore the profile, CA-backed
                    // processing and confirmed service state, not just the JSON.
                    ServerSocket(0, 1, InetAddress.getByName("127.0.0.1")).use { occupied ->
                        val invalid = JSONObject(config).apply { getJSONArray("inbounds").getJSONObject(0).put("listen_port", occupied.localPort) }.toString()
                        assertTrue(runCatching { profiles.writeContent(id, invalid, BoxProfiles.revision(profiles.read(id))) }.isFailure)
                        assertEquals(config, profiles.read(id))
                        assertEquals(Status.Started, activity.serviceStatus.value)
                        assertTrue(JSONObject(Libbox.readUICaptureStatus()).getBoolean("rewriteActive"))
                        assertTrue(request("offline.test", 443, "/dynamic", true).endsWith("script-mock"))
                        assertTrue(request("offline.test", 443, "/file", true).endsWith("""{"message":"fresh-jq"}"""))
                    }
                    profiles.writeContent(id, "{}", BoxProfiles.revision(profiles.read(id)))
                    assertFalse(JSONObject(Libbox.readUICaptureStatus()).getBoolean("rewriteActive"))
                }
            } catch (error: Exception) {
                throw AssertionError("Profile HTTP rewrite failed: ${Libbox.readUICaptureStatus()}", error)
            } finally {
                withContext(Dispatchers.Main) { activity.stopService() }
                withTimeout(15_000) { activity.serviceStatus.first { it == Status.Stopped } }
                createdAlias?.let { KeyStore.getInstance("AndroidKeyStore").apply { load(null); deleteEntry(it) }; caFile.delete() }
                importedFiles.forEach { it.delete() }
            }
        }
    }

    @Test fun routedMITMUsesKeystoreAndTheOriginalOutbound() {
        val arguments = InstrumentationRegistry.getArguments()
        val originPort = arguments.getString("mitmOriginPort")?.toInt() ?: error("Run with tools/android_smoke.py TLS fixture")
        val tcpOriginPort = arguments.getString("mitmTCPOriginPort")?.toInt() ?: error("Run with tools/android_smoke.py TCP fixture")
        val originCA = android.util.Base64.decode(arguments.getString("mitmOriginCA"), android.util.Base64.DEFAULT).toString(Charsets.US_ASCII)
        withRunningProfile { activity, profiles, id ->
            val directory = File(activity.noBackupFilesDir, "http-debug")
            val authority = MitmCertificate(activity, directory)
            var alias: String? = null
            try {
                val old = authority.snapshot()
                val generated = if (old["exists"] == true) old else authority.generate(old["revision"] as String).also {
                    alias = JSONObject(File(directory, "mitm-authority.json").readText()).getString("alias")
                }
                val certificate = authority.publicCertificate(generated["revision"] as String)
                val proxyPort = ServerSocket(0, 1, InetAddress.getByName("127.0.0.1")).use { it.localPort }
                val config = JSONObject("""{
                    "experimental":{"mitm":{"enabled":true,"hosts":["-excluded.test","origin.test"],"ports":[$originPort,$tcpOriginPort]}},
                    "inbounds":[{"type":"mixed","tag":"mitm-in","listen":"127.0.0.1","listen_port":$proxyPort}],
                    "outbounds":[{"type":"selector","tag":"mitm-route","outbounds":["fixture-direct"]},{"type":"direct","tag":"fixture-direct"}],
                    "route":{"rules":[{"domain":["origin.test"],"action":"route","outbound":"mitm-route","override_address":"127.0.0.1"}]}
                }""").put("certificate", JSONObject().put("store", "none").put("certificate", JSONArray().put(originCA))).toString()
                val greeting = byteArrayOf(0) + "BBOX".toByteArray() + byteArrayOf(-1)
                val payload = byteArrayOf(0) + "client".toByteArray() + byteArrayOf(-1)
                val rawResponse = byteArrayOf(0) + "through-original-route".toByteArray() + byteArrayOf(-1)
                for (mode in listOf("http", "http-no-alpn", "tcp")) {
                    val tcp = mode != "http"
                    val raw = mode == "tcp"
                    val port = if (raw) tcpOriginPort else originPort
                    val candidate = JSONObject(config).apply { getJSONObject("experimental").getJSONObject("mitm").put("tcp", tcp) }.toString()
                    val path = if (tcp) "/http-no-alpn" else "/probe?raw=1;2"
                    profiles.writeContent(id, candidate, BoxProfiles.revision(profiles.read(id)))
                    assertEquals(Status.Started, activity.serviceStatus.value)
                    if (raw) {
                        // This mode reuses the previous profile. A no-op write
                        // keeps recording active, so end that session explicitly.
                        assertTrue(JSONObject(Libbox.readUICaptureStatus()).getBoolean("active"))
                        Libbox.stopUICapture()
                    }
                    assertFalse(JSONObject(Libbox.readUICaptureStatus()).getBoolean("active"))
                    val captures = File(activity.noBackupFilesDir, "http-debug/captures")
                    Libbox.openUICapture(captures.absolutePath)
                    Libbox.clearUICapture()
                    BoxService.dashboardServer!!.startUICapture(captures.absolutePath,
                        """{"version":3,"ports":[$originPort,$tcpOriginPort]}""", "", null)
                    assertTrue(JSONObject(Libbox.readUICaptureStatus()).getBoolean("active"))

                    val trustStore = KeyStore.getInstance(KeyStore.getDefaultType()).apply { load(null); setCertificateEntry("mitm", certificate) }
                    val managers = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm()).apply { init(trustStore) }
                    val ssl = SSLContext.getInstance("TLS").apply { init(null, managers.trustManagers, SecureRandom()) }
                    Socket("127.0.0.1", proxyPort).use { tunnel ->
                        tunnel.soTimeout = 10_000
                        tunnel.getOutputStream().write("CONNECT origin.test:$port HTTP/1.1\r\nHost: origin.test:$port\r\n\r\n".toByteArray())
                        val reply = StringBuilder()
                        while (!reply.endsWith("\r\n\r\n")) {
                            val value = tunnel.getInputStream().read()
                            check(value >= 0 && reply.length < 8192) { "Invalid CONNECT response: $reply" }
                            reply.append(value.toChar())
                        }
                        assertTrue(reply.toString().startsWith("HTTP/1.1 200"))
                        (ssl.socketFactory.createSocket(tunnel, "origin.test", port, true) as SSLSocket).use { client ->
                            client.soTimeout = 10_000
                            client.sslParameters = client.sslParameters.apply {
                                endpointIdentificationAlgorithm = "HTTPS"
                                serverNames = listOf(SNIHostName("origin.test"))
                            }
                            client.startHandshake()
                            // Conscrypt may discard peer certificates after receiving
                            // close_notify. Verify the live session before reading EOF.
                            val peer = client.session.peerCertificates[0] as X509Certificate
                            assertFalse(peer.encoded.contentEquals(certificate.encoded))
                            peer.verify(certificate.publicKey)
                            if (raw) {
                                val receivedGreeting = ByteArray(greeting.size)
                                DataInputStream(client.inputStream).readFully(receivedGreeting)
                                assertArrayEquals(greeting, receivedGreeting)
                                client.outputStream.write(payload)
                                assertArrayEquals(rawResponse, client.inputStream.readBytes())
                            } else {
                                client.outputStream.write("GET $path HTTP/1.1\r\nHost: origin.test:$port\r\nConnection: close\r\n\r\n".toByteArray())
                                val response = client.inputStream.readBytes().toString(Charsets.UTF_8)
                                assertTrue(response.startsWith("HTTP/1.1 200"))
                                assertTrue(response.contains("through-original-route"))
                            }
                        }
                    }
                    val detail = withTimeout(5_000) {
                        var result: JSONObject? = null
                        while (result == null) {
                            val items = JSONObject(Libbox.listUICapture(10, "origin.test")).getJSONArray("items")
                            if (items.length() == 1 && items.getJSONObject(0).getBoolean("complete")) {
                                result = JSONObject(Libbox.readUICapture(items.getJSONObject(0).getString("id")))
                            } else delay(20)
                        }
                        checkNotNull(result)
                    }
                    val metadata = detail.getJSONObject("metadata")
                    val bodyPage = JSONObject(Libbox.readUICaptureBody(detail.getString("id"), true, 0, false))
                    assertEquals(detail.getJSONObject("responsePreview").getString("text"), bodyPage.getString("text"))
                    assertFalse(bodyPage.getBoolean("hasMore"))
                    assertFalse(detail.has("requestBody"))
                    assertFalse(detail.has("responseBody"))
                    val summary = JSONObject(Libbox.listUICapture(10, "origin.test")).getJSONArray("items").getJSONObject(0)
                    assertEquals(metadata.optString("connectionId"), summary.optString("connectionId"))
                    assertEquals(metadata.getString("outbound"), summary.getString("outbound"))
                    assertEquals(detail.getJSONObject("request").getLong("bodyBytes"), summary.getLong("requestBytes"))
                    assertEquals(detail.getJSONObject("response").getLong("bodyBytes"), summary.getLong("responseBytes"))
                    val pageQuery = JSONObject().put("limit", 1).put("query", "origin.test")
                    val firstPage = JSONObject(Libbox.queryUICapture(pageQuery.toString()))
                    assertEquals(detail.getString("id"), firstPage.getJSONArray("ids").getString(0))
                    assertEquals(1, firstPage.getJSONArray("items").length())
                    assertFalse(firstPage.getBoolean("hasMore"))
                    pageQuery.put("known", firstPage.getJSONObject("versions"))
                    assertEquals(0, JSONObject(Libbox.queryUICapture(pageQuery.toString())).getJSONArray("items").length())
                    val querySelection = Libbox.selectUICaptureHAR(pageQuery.toString())
                    assertEquals(if (raw) 0 else 1, JSONArray(querySelection).length())
                    val har = File.createTempFile("activity-selection-", ".har", activity.cacheDir)
                    try {
                        Libbox.exportUICaptureHARSelection(har.absolutePath, querySelection)
                        assertEquals(if (raw) 0 else 1, JSONObject(har.readText()).getJSONObject("log").getJSONArray("entries").length())
                        Libbox.exportUICaptureHARSelection(har.absolutePath, JSONArray().put(detail.getString("id")).toString())
                        assertEquals(if (raw) 0 else 1, JSONObject(har.readText()).getJSONObject("log").getJSONArray("entries").length())
                        Libbox.exportUICaptureHARSelection(har.absolutePath, "[]")
                        assertEquals(0, JSONObject(har.readText()).getJSONObject("log").getJSONArray("entries").length())
                        if (raw) {
                            assertTrue(runCatching { Libbox.exportUICaptureCurlFile(har.absolutePath, detail.getString("id")) }.isFailure)
                        } else {
                            Libbox.exportUICaptureCurlFile(har.absolutePath, detail.getString("id"))
                            assertEquals(Libbox.exportUICaptureCurl(detail.getString("id")), har.readText())
                        }
                    } finally { har.delete() }
                    assertEquals("MITM", metadata.getString("captureSource"))
                    assertEquals("mitm-in", metadata.getString("inbound"))
                    assertEquals("mitm-route", metadata.getString("outbound"))
                    assertEquals("origin.test", metadata.getJSONObject("tls").getString("serverName"))
                    assertEquals("${if (raw) "tls" else "https"}://origin.test:$port${if (raw) "" else path}", detail.getJSONObject("request").getString("url"))
                    assertTrue(detail.getJSONObject("response").getBoolean("bodyComplete"))
                    assertEquals(raw, metadata.optBoolean("rawTCP"))
                    assertEquals("", metadata.getJSONObject("tls").getString("alpn"))
                    if (raw) {
                        assertEquals("TLS/TCP", detail.getJSONObject("request").getString("protocol"))
                        for ((key, expected) in listOf("requestPreview" to payload, "responsePreview" to (greeting + rawResponse))) {
                            val preview = detail.getJSONObject(key)
                            assertEquals("base64", preview.getString("encoding"))
                            assertArrayEquals(expected, android.util.Base64.decode(preview.getString("text"), android.util.Base64.DEFAULT))
                        }
                        assertTrue(runCatching { Libbox.exportUICaptureCurl(detail.getString("id")) }.isFailure)
                    } else {
                        assertTrue(detail.getJSONObject("responsePreview").getString("text").contains("through-original-route"))
                        val headers = detail.getJSONObject("response").getJSONArray("headers")
                        assertEquals(2, (0 until headers.length()).count { headers.getJSONObject(it).getString("name").equals("Set-Cookie", true) })
                        assertTrue(Libbox.exportUICaptureCurl(detail.getString("id")).contains("origin.test:$port"))
                    }
                }
                profiles.writeContent(id, "{}", BoxProfiles.revision(profiles.read(id)))
                assertFalse(JSONObject(Libbox.readUICaptureStatus()).getBoolean("active"))
            } catch (error: Exception) {
                throw AssertionError("Android routed MITM failed: ${Libbox.readUICaptureStatus()}", error)
            } finally {
                Libbox.stopUICapture()
                withContext(Dispatchers.Main) { activity.stopService() }
                withTimeout(15_000) { activity.serviceStatus.first { it == Status.Stopped } }
                alias?.let { KeyStore.getInstance("AndroidKeyStore").apply { load(null); deleteEntry(it) }; File(directory, "mitm-authority.json").delete() }
            }
        }
    }

    @Test fun independentCronUsesRoutingAndStopsWithTheService() {
        withRunningProfile { activity, profiles, id ->
            SubscriptionServer("cron-ok").use { origin ->
                val originPort = java.net.URI(origin.url).port
                val script = "${'$'}httpClient.get(${'$'}argument, function(error,response,data) { if(error || response.status !== 200 || data !== 'cron-ok') throw Error(error || data); console.log('android-cron-success'); ${'$'}done(); });"
                val config = JSONObject("""{
                    "outbounds":[{"type":"direct","tag":"cron-direct"}],
                    "route":{"rules":[{"inbound":["b-box-scripts"],"domain":["cron.test"],"action":"route","outbound":"cron-direct","override_address":"127.0.0.1"}]}
                }""").put("experimental", JSONObject().put("scripts", JSONObject().put("enabled", true).put("cron", JSONArray().put(
                    JSONObject().put("cron", "* * * * * *").put("timeout_ms", 5000)
                        .put("argument", "http://cron.test:$originPort/profile.json").put("source", script)
                )))).toString()
                profiles.writeContent(id, config, BoxProfiles.revision(profiles.read(id)))
                val state = JSONObject(Libbox.readUICaptureStatus())
                assertTrue(state.getBoolean("scriptsActive"))
                assertFalse(state.getBoolean("rewriteActive"))
                assertFalse(state.getBoolean("mitmActive"))
                assertEquals(1, state.getInt("cronJobs"))
                withTimeout(10_000) { while (origin.requests.get() == 0) delay(50) }
                val before = origin.requests.get()
                assertTrue(before > 0)
                BoxService.dashboardServer!!.startUICapture(File(activity.noBackupFilesDir, "http-debug/captures").absolutePath, "{}", "", null)
                Libbox.stopUICapture()
                assertTrue(JSONObject(Libbox.readUICaptureStatus()).getBoolean("scriptsActive"))
                withTimeout(5_000) { while (origin.requests.get() <= before) delay(50) }
                val invalid = JSONObject(config).apply {
                    getJSONObject("experimental").getJSONObject("scripts").getJSONArray("cron").getJSONObject(0).put("cron", "invalid")
                }.toString()
                assertTrue(runCatching { profiles.writeContent(id, invalid, BoxProfiles.revision(profiles.read(id))) }.isFailure)
                assertEquals(Status.Started, activity.serviceStatus.value)
                assertEquals(config, profiles.read(id))
                profiles.writeContent(id, "{}", BoxProfiles.revision(profiles.read(id)))
                assertFalse(JSONObject(Libbox.readUICaptureStatus()).getBoolean("scriptsActive"))
                val stopped = origin.requests.get()
                delay(1500)
                assertEquals(stopped, origin.requests.get())
            }
        }
    }

    @Test fun httpScriptsUseRoutedClientTimersAndPersistentNamespaces() {
        withRunningProfile { activity, profiles, id ->
            SubscriptionServer("api-value").use { origin ->
                val originPort = java.net.URI(origin.url).port
                val proxyPort = ServerSocket(0, 1, InetAddress.getByName("127.0.0.1")).use { it.localPort }
                val namespace = "android-api-${UUID.randomUUID()}"
                val script = """
                    const cached=${'$'}persistentStore.read('value');
                    if(cached!==null) ${'$'}done({response:{body:cached}});
                    ${'$'}httpClient.get(${'$'}argument,function(error,response,data){
                        if(error || response.status!==200) throw Error(error || 'status');
                        if(!${'$'}persistentStore.write(data,'value')) throw Error('storage');
                        setTimeout(()=>${'$'}done({response:{body:data}}),1);
                    });
                """.trimIndent()
                val rule = JSONObject().put("pattern", ".").put("phase", "request").put("timeout_ms", 2000)
                    .put("storage_namespace", namespace).put("argument", "http://api.test:$originPort/profile.json").put("source", script)
                    .put("http_client", JSONObject().put("allowed_origins", JSONArray().put("http://api.test:$originPort")).put("max_requests", 1))
                val config = JSONObject("""
                    {
                        "inbounds":[{"type":"mixed","tag":"script-in","listen":"127.0.0.1","listen_port":$proxyPort}],
                        "outbounds":[{"type":"direct","tag":"original"},{"type":"direct","tag":"script-route"}],
                        "route":{"rules":[
                            {"domain":["intercepted.test"],"action":"route","outbound":"original","override_address":"127.0.0.1"},
                            {"inbound":["b-box-scripts"],"domain":["api.test"],"action":"route","outbound":"script-route","override_address":"127.0.0.1"}
                        ]}
                    }
                """).put("experimental", JSONObject().put("scripts", JSONObject().put("enabled", true)
                    .put("ports", JSONArray().put(80).put(originPort)).put("http", JSONArray().put(rule))))
                fun request(): String = Socket("127.0.0.1", proxyPort).use { tunnel ->
                    tunnel.soTimeout = 10_000
                    tunnel.getOutputStream().write("CONNECT intercepted.test:80 HTTP/1.1\r\nHost: intercepted.test:80\r\n\r\n".toByteArray())
                    val reply = StringBuilder()
                    while (!reply.endsWith("\r\n\r\n")) {
                        val value = tunnel.getInputStream().read()
                        check(value >= 0 && reply.length < 8192) { "Invalid script CONNECT response: $reply" }
                        reply.append(value.toChar())
                    }
                    assertTrue(reply.toString().startsWith("HTTP/1.1 200"))
                    tunnel.getOutputStream().write("GET /cache HTTP/1.1\r\nHost: intercepted.test:80\r\nConnection: close\r\n\r\n".toByteArray())
                    tunnel.getInputStream().readBytes().toString(Charsets.UTF_8)
                }
                profiles.writeContent(id, config.toString(), BoxProfiles.revision(profiles.read(id)))
                // The auxiliary origin also matches the script's ports/pattern.
                // Without the service's recursion guard this request cannot finish.
                assertTrue(request().endsWith("api-value"))
                assertEquals(1, origin.requests.get())
                val snapshot = File(activity.noBackupFilesDir, "http-debug/captures/script-storage/$namespace.json")
                assertEquals("api-value", JSONObject(snapshot.readText()).getJSONObject("values").getString("value"))

                // Rename/reload the rule and clear capture records; the explicit
                // namespace must still serve the cached value without networking.
                rule.put("name", "renamed rule")
                profiles.writeContent(id, config.toString(), BoxProfiles.revision(profiles.read(id)))
                Libbox.clearUICapture()
                assertTrue(request().endsWith("api-value"))
                assertEquals(1, origin.requests.get())

                rule.put("storage_namespace", "isolated-${UUID.randomUUID()}").remove("http_client")
                profiles.writeContent(id, config.toString(), BoxProfiles.revision(profiles.read(id)))
                assertTrue(request().endsWith("api-value"))
                assertEquals(2, origin.requests.get())

                rule.put("http_client", JSONObject().put("allowed_origins", JSONArray()))
                    .put("source", "${'$'}httpClient.get(${'$'}argument,(error)=>{if(!error) throw Error('restriction bypass'); ${'$'}done({response:{status:299,body:'denied'}});});")
                profiles.writeContent(id, config.toString(), BoxProfiles.revision(profiles.read(id)))
                assertTrue(request().endsWith("denied"))
                assertEquals(2, origin.requests.get())

                rule.put("storage_namespace", namespace).put("source", script)
                profiles.writeContent(id, config.toString(), BoxProfiles.revision(profiles.read(id)))
                assertTrue(request().endsWith("api-value"))
                assertEquals(2, origin.requests.get())
                rule.put("source", "if(!${'$'}persistentStore.write(null,'value')) throw Error('delete'); ${'$'}done({response:{body:'cleared'}});")
                profiles.writeContent(id, config.toString(), BoxProfiles.revision(profiles.read(id)))
                assertTrue(request().endsWith("cleared"))
                assertFalse(snapshot.exists())
            }
        }
    }

    @Test fun captureSettingsAreLocalAndCheckRevisions() = runBlocking(Dispatchers.IO) {
        Application.application.setupLibbox()
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val directory = File(context.cacheDir, "capture-settings-test").apply { deleteRecursively(); mkdirs() }
        try {
            File(directory, "settings.json").writeText("""{"https":true,"rewriteEnabled":true,"scripts":[],"blockQUIC":true,"captureNetwork":true,"captureTlsKeys":true,"includeHosts":["old-include.test"],"excludeHosts":["old-exclude.test"],"decryptHosts":["api.test"],"decryptBypassHosts":["private.test"],"packages":["com.test"],"ports":[8443],"bodyLimit":131072}""")
            File(directory, "ca.json").writeText("retired material")
            val settings = CaptureSettings(directory, retiredKeyAlias = null)
            val initial = settings.snapshot()
            assertFalse((initial["config"] as Map<*, *>).containsKey("https"))
            assertFalse((initial["config"] as Map<*, *>).containsKey("rewriteEnabled"))
            assertFalse((initial["config"] as Map<*, *>).containsKey("captureNetwork"))
            assertFalse((initial["config"] as Map<*, *>).containsKey("captureTlsKeys"))
            assertFalse((initial["config"] as Map<*, *>).containsKey("includeHosts"))
            assertFalse((initial["config"] as Map<*, *>).containsKey("excludeHosts"))
            assertFalse((initial["config"] as Map<*, *>).containsKey("decryptHosts"))
            assertFalse((initial["config"] as Map<*, *>).containsKey("decryptBypassHosts"))
            assertEquals(listOf("com.test"), (initial["config"] as Map<*, *>)["packages"])
            assertEquals(listOf(8443), (initial["config"] as Map<*, *>)["ports"])
            assertEquals(131072, ((initial["config"] as Map<*, *>)["bodyLimit"] as Number).toInt())
            assertFalse(File(directory, "settings.json").readText().contains("captureNetwork"))
            assertFalse(File(directory, "settings.json").readText().contains("captureTlsKeys"))
            assertFalse(File(directory, "settings.json").readText().contains("includeHosts"))
            assertFalse(File(directory, "settings.json").readText().contains("excludeHosts"))
            assertFalse(File(directory, "ca.json").exists())
            assertEquals(initial, CaptureSettings(directory, retiredKeyAlias = null).snapshot())
            val revision = initial["revision"] as String
            val saved = settings.save(revision, """{"version":3,"extractHosts":true,"ports":[443]}""")
            assertNotEquals(revision, saved["revision"])
            assertTrue(runCatching { settings.save(revision, "{}") }.isFailure)
            val previous = File(directory, "settings.json").readText()
            for (retired in listOf("""{"https":true}""", """{"decryptBypassHosts":[]}""", """{"scripts":[]}""", """{"captureNetwork":true}""", """{"captureTlsKeys":true}""",
                """{"includeHosts":["api.test"]}""", """{"excludeHosts":["private.test"]}""")) {
                assertTrue(runCatching { settings.save(saved["revision"] as String, retired) }.isFailure)
                assertEquals(previous, File(directory, "settings.json").readText())
            }
            assertTrue(runCatching { settings.save(saved["revision"] as String, """{"tlsOffsets":[]}""") }.isFailure)
            assertEquals(previous, File(directory, "settings.json").readText())
            val reloaded = CaptureSettings(directory, retiredKeyAlias = null).snapshot()
            assertEquals(saved, reloaded)
            assertFalse((reloaded["config"] as Map<*, *>).containsKey("https"))
            assertEquals(true, (reloaded["config"] as Map<*, *>)["extractHosts"])
            val profile = """{"log":{"level":"warn"},"experimental":{"http_debug":{"scripts":[]},"cache_file":{"enabled":true}}}"""
            val cleaned = Libbox.removeUIRetiredCaptureOptions(profile)
            assertFalse(cleaned.contains("http_debug"))
            assertTrue(cleaned.contains("cache_file"))
            Libbox.checkConfig(cleaned)
        } finally { directory.deleteRecursively() }
    }

    @Test fun captureSettingsRecoverInterruptedAtomicWrites() = runBlocking(Dispatchers.IO) {
        Application.application.setupLibbox()
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val directory = File(context.cacheDir, "capture-settings-recovery-test").apply { deleteRecursively(); mkdirs() }
        try {
            val base = File(directory, "settings.json")
            val backup = File(directory, "settings.json.bak")
            backup.writeText("""{"https":true,"ports":[8443],"scripts":[],"captureNetwork":true,"captureTlsKeys":true,"includeHosts":["api.test"],"excludeHosts":["private.test"]}""")
            val settings = CaptureSettings(directory, retiredKeyAlias = null)
            val recovered = settings.snapshot()
            val config = recovered["config"] as Map<*, *>
            assertFalse(config.containsKey("https"))
            assertEquals(listOf(8443), config["ports"])
            assertFalse(config.containsKey("scripts"))
            assertFalse(config.containsKey("captureNetwork"))
            assertFalse(config.containsKey("captureTlsKeys"))
            assertFalse(config.containsKey("includeHosts"))
            assertFalse(config.containsKey("excludeHosts"))
            assertFalse(backup.exists())
            val content = base.readText()
            assertTrue(base.renameTo(backup))
            assertEquals(recovered, settings.snapshot())
            assertEquals(content, base.readText())
            assertFalse(backup.exists())

            // Recovery must read the complete committed backup even beyond the
            // former migration/settings size caps.
            base.writeText("{}")
            backup.writeText(content.padEnd(4 * 1024 * 1024 + 1))
            assertEquals(recovered["config"], CaptureSettings(directory, retiredKeyAlias = null).snapshot()["config"])
            assertFalse(backup.exists())
        } finally { directory.deleteRecursively() }
    }

    @Test fun binarySubscriptionsKeepTheirMetadata() = runBlocking(Dispatchers.IO) {
        Application.application.setupLibbox()
        val binary = io.nekohasekai.libbox.ProfileContent().apply {
            name = "Imported subscription"
            type = Libbox.ProfileTypeRemote
            config = "{}"
            remotePath = "https://example.com/profile.json"
            autoUpdate = true
            autoUpdateInterval = 120
            lastUpdated = 1_700_000_000_000L
        }
        val profiles = BoxProfiles {}
        val decoded = profiles.decode(binary.encode(), "import.bpf")
        assertEquals(true, decoded["remote"])
        assertEquals(binary.remotePath, decoded["url"])
        assertEquals(true, decoded["autoUpdate"])
        assertEquals(120, (decoded["interval"] as Number).toInt())
        assertEquals(binary.lastUpdated, decoded["updatedAt"])
        Settings.dataStore.initialize()
        val oldSelection = Settings.selectedProfile
        val id = profiles.save(decoded)
        try {
            val saved = profiles.get(id)
            assertEquals(binary.remotePath, saved.typed.remoteURL)
            assertEquals(binary.lastUpdated, saved.typed.lastUpdated.time)
            assertTrue(saved.typed.autoUpdate)
            profiles.writeContent(id, """{"log":{"level":"warn"}}""", BoxProfiles.revision("{}"))
            assertEquals(binary.lastUpdated, profiles.get(id).typed.lastUpdated.time)
        } finally {
            Settings.selectedProfile = oldSelection
            Settings.dataStore.flush()
            profiles.delete(id)
        }
    }

    @Test fun localProfileTimesFollowSuccessfulContentChanges() = runBlocking(Dispatchers.IO) {
        Application.application.setupLibbox()
        Settings.dataStore.initialize()
        val oldSelection = Settings.selectedProfile
        var failApply = false
        val profiles = BoxProfiles { recover -> if (failApply && !recover) error("Apply failed") }
        val createdAfter = System.currentTimeMillis()
        val id = profiles.save(mapOf("name" to "Local timestamp fixture", "content" to "{}"))
        try {
            Settings.selectedProfile = id
            assertTrue(profiles.get(id).typed.lastUpdated.time >= createdAfter)
            val profile = profiles.get(id)
            profile.typed.lastUpdated = Date(0) // A local profile created before timestamps were recorded.
            ProfileManager.update(profile)
            assertEquals(0L, profiles.list().single { it["id"] == id }["updatedAt"])
            val content = """{"log":{"level":"warn"}}"""
            profiles.writeContent(id, content, BoxProfiles.revision("{}"))
            val savedAt = profiles.get(id).typed.lastUpdated.time
            assertTrue(savedAt >= createdAfter)
            assertEquals(savedAt, profiles.list().single { it["id"] == id }["updatedAt"])
            profiles.writeContent(id, content, BoxProfiles.revision(content))
            profiles.save(mapOf("id" to id, "name" to "Renamed local profile"))
            assertEquals(savedAt, profiles.get(id).typed.lastUpdated.time)
            val beforeFailure = profiles.get(id)
            beforeFailure.typed.lastUpdated = Date(1_700_000_000_000L)
            ProfileManager.update(beforeFailure)
            failApply = true
            assertNotNull(runCatching {
                profiles.writeContent(id, "{}", BoxProfiles.revision(content))
            }.exceptionOrNull())
            assertEquals(content, profiles.read(id))
            assertEquals(beforeFailure.typed.lastUpdated.time, profiles.get(id).typed.lastUpdated.time)
        } finally {
            failApply = false
            Settings.selectedProfile = oldSelection
            Settings.dataStore.flush()
            profiles.delete(id)
        }
    }

    @Test fun defaultProfileStaysAvailableAlongsideExistingProfiles() = runBlocking(Dispatchers.IO) {
        Application.application.setupLibbox()
        Settings.dataStore.initialize()
        val oldSelection = Settings.selectedProfile
        val profiles = BoxProfiles {}
        val id = profiles.save(mapOf("name" to "Existing profile", "content" to "{}"))
        try {
            Settings.saveProfileSelection(id)
            val default = profiles.ensureDefault()
            val listed = profiles.list()
            assertTrue(listed.any { it["id"] == id })
            assertEquals(default, listed.single { it["default"] == true }["id"])
            assertEquals(default, profiles.ensureDefault())
            assertTrue(profiles.get(default).typed.isDefault)
            assertEquals("", profiles.get(default).typed.remoteURL)
            val content = profiles.read(default)
            Libbox.checkConfig(content)
            val original = profiles.get(default)
            val originalName = original.name
            val updatedAt = original.typed.lastUpdated.time
            try {
                original.name = "Default profile"
                ProfileManager.update(original)
                assertEquals(default, profiles.ensureDefault())
                assertEquals("Profile file", profiles.get(default).name)
                assertEquals(content, profiles.read(default))
                assertEquals(updatedAt, profiles.get(default).typed.lastUpdated.time)
                original.name = "My profile"
                ProfileManager.update(original)
                assertEquals(default, profiles.ensureDefault())
                assertEquals("My profile", profiles.get(default).name)
            } finally {
                original.name = originalName
                ProfileManager.update(original)
            }
            assertEquals(id, Settings.selectedProfile)
            assertTrue(runCatching { profiles.delete(default) }.exceptionOrNull() is IllegalArgumentException)
            assertTrue(profiles.get(default).typed.isDefault)
        } finally {
            Settings.saveProfileSelection(oldSelection)
            profiles.delete(id)
        }
    }

    @Test fun addedNodesBelongToTheSelectedProfileAndRecoverFailedWrites() = withRunningProfile { activity, profiles, id ->
        val nodes = BoxNodes(beforeApply = { ProfileContent.requireStableService(activity.serviceStatus.value) }) { recover ->
            withContext(Dispatchers.Main) { activity.applyProfile(recover = recover) }
        }
        val other = profiles.save(mapOf("name" to "Separate node configuration", "content" to "{}"))
        try {
            val added = nodes.add("""[{"type":"socks","tag":"local-node","server":"127.0.0.1","server_port":1080}]""", id, BoxProfiles.revision(profiles.read(id)))
            assertEquals(1, (added["added"] as Number).toInt())
            assertTrue(configurationNodes(profiles.read(id)).any { it.optString("tag") == "local-node" })
            assertEquals("{}", profiles.read(other))
            val saved = profiles.read(id)
            assertEquals(saved, profiles.decode(saved.toByteArray(), "nodes.json")["content"])
            val savedAt = profiles.get(id).typed.lastUpdated.time
            val revision = BoxProfiles.revision(saved)
            profiles.select(other) { activity.serviceStatus.value }
            assertTrue(runCatching { nodes.add("""[{"type":"socks","tag":"stale-node","server":"127.0.0.1","server_port":1081}]""", id, revision) }.exceptionOrNull() is IllegalArgumentException)
            assertEquals(saved, profiles.read(id))
            assertEquals("{}", profiles.read(other))
            assertEquals(other, nodes.state()["profileId"])
            assertTrue((nodes.state()["nodes"] as List<*>).isEmpty())
            profiles.select(id) { activity.serviceStatus.value }

            val applications = mutableListOf<Boolean>()
            val failing = BoxNodes(beforeApply = { ProfileContent.requireStableService(activity.serviceStatus.value) }) { recover ->
                applications.add(recover)
                if (!recover) error("Node application failed")
                withContext(Dispatchers.Main) { activity.applyProfile(recover = true) }
            }
            assertNotNull(runCatching {
                failing.add("""[{"type":"socks","tag":"failed-node","server":"127.0.0.1","server_port":1082}]""", id, revision)
            }.exceptionOrNull())
            assertEquals(listOf(false, true), applications)
            assertEquals(saved, profiles.read(id))
            assertEquals(savedAt, profiles.get(id).typed.lastUpdated.time)
            assertEquals(Status.Started, activity.serviceStatus.value)

            val backups = BoxBackups(activity, { emptyMap() }, {}, { _, _ -> }, { successful ->
                assertTrue(successful)
                // Other profile callbacks may briefly own the lock after restore releases it.
                // Publication must be able to await them and read the final snapshots.
                withTimeout(10_000) {
                    profiles.list()
                    nodes.state()
                }
            }, { _, _ -> }, BoxWebDAV(activity))
            val backup = backups.create()
            assertFalse(backup.containsKey("nodes"))
            val entries = rows(backup["profiles"])
            val default = entries.single { it["default"] == true }["id"]
            val conflicting = entries.map { if (it["id"] == id) it + mapOf("default" to true) else it }
            assertTrue(runCatching { backups.read(backups.encode(backup + mapOf("profiles" to conflicting)), "profiles") }.exceptionOrNull() is IllegalArgumentException)
            val renamedIdentity = entries.map { if (it["default"] == true) it + mapOf("key" to UUID.randomUUID().toString()) else it }
            backups.restore(backup + mapOf("profiles" to renamedIdentity), "profiles", "Compatible")
            assertEquals(default, ProfileManager.list().single { it.typed.isDefault }.id)
            assertEquals(saved, profiles.read(id))
            assertEquals(id, Settings.selectedProfile)
        } finally {
            if (Settings.selectedProfile == other) profiles.select(id) { activity.serviceStatus.value }
            profiles.delete(other)
        }
    }

    @Test fun stoppedSelectorChoicesUseTheCoreCacheWithoutEditingOrStarting() = withRunningProfile { activity, profiles, id ->
        val nodes = BoxNodes(beforeApply = { ProfileContent.requireStableService(activity.serviceStatus.value) }) {
            error("Selecting a stopped proxy must not apply or start the configuration")
        }
        val cache = File(activity.cacheDir, "offline-selector-${UUID.randomUUID()}.db")
        val other = profiles.save(mapOf("name" to "Separate selector configuration", "content" to "{}"))
        try {
            withContext(Dispatchers.Main) { activity.stopService() }
            withTimeout(15_000) { activity.serviceStatus.first { it == Status.Stopped } }
            val content = JSONObject("""{
                "outbounds":[
                    {"type":"selector","tag":"proxy","outbounds":["a","auto","b"],"default":"a"},
                    {"type":"urltest","tag":"auto","outbounds":["a","b"]},
                    {"type":"direct","tag":"a"},{"type":"direct","tag":"b"}
                ],
                "route":{"final":"proxy"},
                "experimental":{"cache_file":{"enabled":false,"cache_id":"offline-selection-fixture"}}
            }""").apply { getJSONObject("experimental").getJSONObject("cache_file").put("path", cache.absolutePath) }.toString()
            profiles.writeContent(id, content, BoxProfiles.revision(profiles.read(id)))
            val revision = BoxProfiles.revision(profiles.read(id))
            val updatedAt = profiles.get(id).typed.lastUpdated.time
            val initialGroups = rows(nodes.state()["groups"])
            assertEquals(listOf("proxy", "auto"), initialGroups.map { it["tag"] })
            assertEquals("a", initialGroups.first()["selected"])
            assertEquals(true, initialGroups.first()["selectable"])
            assertEquals(false, initialGroups.last()["selectable"])
            assertFalse("Reading stopped groups must not create a cache", cache.exists())

            nodes.select("proxy", "b", id, revision)
            assertTrue(cache.isFile)
            assertEquals("b", rows(nodes.state()["groups"]).first()["selected"])
            assertEquals(content, profiles.read(id))
            assertEquals(updatedAt, profiles.get(id).typed.lastUpdated.time)
            assertEquals(Status.Stopped, activity.serviceStatus.value)
            assertNull(BoxService.dashboardServer)
            for ((group, tag) in listOf("auto" to "a", "missing" to "a", "proxy" to "missing")) {
                assertNotNull(runCatching { nodes.select(group, tag, id, revision) }.exceptionOrNull())
            }
            assertEquals("b", rows(nodes.state()["groups"]).first()["selected"])

            profiles.select(other) { activity.serviceStatus.value }
            assertTrue(runCatching { nodes.select("proxy", "a", id, revision) }.exceptionOrNull() is IllegalArgumentException)
            profiles.select(id) { activity.serviceStatus.value }
            val edited = JSONObject(content).put("log", JSONObject().put("level", "warn")).toString()
            profiles.writeContent(id, edited, revision)
            assertTrue(runCatching { nodes.select("proxy", "a", id, revision) }.exceptionOrNull() is IllegalArgumentException)
            val currentRevision = BoxProfiles.revision(edited)
            val session = CommandTarget.localSession()
            CommandTarget.setRemoteServer(null)
            assertNotNull(runCatching {
                nodes.select("proxy", "a", id, currentRevision) { CommandTarget.requireLocalSession(session) }
            }.exceptionOrNull())
            assertEquals("b", rows(nodes.state()["groups"]).first()["selected"])

            withContext(Dispatchers.Main) { activity.applyProfile(recover = true) }
            coroutineScope {
                val selected = CompletableDeferred<String>()
                val client = CommandClient(this, CommandClient.ConnectionType.Groups, object : CommandClient.Handler {
                    override fun updateGroups(newGroups: MutableList<OutboundGroup>) {
                        newGroups.firstOrNull { it.tag == "proxy" }?.let { selected.complete(it.selected) }
                    }
                }, localOnly = true)
                try {
                    client.connect()
                    assertEquals("b", withTimeout(15_000) { selected.await() })
                } finally {
                    client.disconnect()
                }
            }
            assertTrue(rows(nodes.state()["groups"]).isEmpty())
            assertTrue(runCatching { nodes.select("proxy", "a", id, currentRevision) }.exceptionOrNull() is IllegalArgumentException)
            Libbox.newStandaloneCommandClient().selectOutbound("proxy", "auto")
            withContext(Dispatchers.Main) { activity.stopService() }
            withTimeout(15_000) { activity.serviceStatus.first { it == Status.Stopped } }
            assertEquals("auto", rows(nodes.state()["groups"]).first()["selected"])
            assertEquals(edited, profiles.read(id))
        } finally {
            withContext(Dispatchers.Main) { activity.stopService() }
            withTimeout(15_000) { activity.serviceStatus.first { it == Status.Stopped } }
            if (Settings.selectedProfile == other) profiles.select(id) { activity.serviceStatus.value }
            profiles.delete(other)
            cache.delete()
        }
    }

    @Test fun remoteRefreshesKeepAddedNodesAndRollBackFailedApplications() = withPausedScheduledUpdates {
        SubscriptionServer(socksSubscription()).use { server ->
            withRunningProfile(server.url) { activity, profiles, id ->
                val original = """{
                    "log":{"level":"warn"},
                    "inbounds":[{"type":"mixed","tag":"local-in","listen":"127.0.0.1","listen_port":0}],
                    "outbounds":[{"type":"socks","tag":"old-upstream","server":"127.0.0.1","server_port":1080},
                        {"type":"selector","tag":"proxy","outbounds":["old-upstream"]},
                        {"type":"direct","tag":"local-direct"}],
                    "route":{"final":"proxy"}
                }"""
                profiles.writeContent(id, original, BoxProfiles.revision(profiles.read(id)))
                val nodes = BoxNodes(beforeApply = { ProfileContent.requireStableService(activity.serviceStatus.value) }) { recover ->
                    withContext(Dispatchers.Main) { activity.applyProfile(recover = recover) }
                }
                val stale = profiles.prepareUpdate(id)
                nodes.add("""[{"type":"socks","tag":"local-node","server":"127.0.0.1","server_port":1080}]""", id, BoxProfiles.revision(profiles.read(id)))
                val added = profiles.read(id)
                assertTrue(runCatching { profiles.commitUpdate(stale, server.content) }.exceptionOrNull() is IllegalArgumentException)
                assertEquals(added, profiles.read(id))
                assertEquals(0L, profiles.get(id).typed.lastUpdated.time)
                // The structured editor uses formatted JSON without the ownership comment.
                // Saving it must still keep added nodes during the next subscription refresh.
                profiles.writeContent(id, JSONObject(Libbox.formatConfig(added).value).toString(), BoxProfiles.revision(added))

                profiles.update(id)
                var content = profiles.read(id)
                assertTrue(configurationNodes(content).any { it.optString("tag") == "local-node" })
                assertTrue(configurationNodes(content).any { it.optString("tag") == "upstream" })
                assertFalse(configurationNodes(content).any { it.optString("tag") == "old-upstream" })
                assertEquals(configuration(original).getJSONObject("log").toString(), configuration(content).getJSONObject("log").toString())
                assertEquals(configuration(original).getJSONArray("inbounds").toString(), configuration(content).getJSONArray("inbounds").toString())
                assertEquals(configuration(original).getJSONObject("route").toString(), configuration(content).getJSONObject("route").toString())
                assertTrue(configurationNodes(content).any { it.optString("tag") == "local-direct" })
                val selector = configurationNodes(content).single { it.optString("tag") == "proxy" }
                assertEquals(setOf("upstream", "local-node"), (0 until selector.getJSONArray("outbounds").length()).map { selector.getJSONArray("outbounds").getString(it) }.toSet())
                assertTrue(profiles.get(id).typed.lastUpdated.time > 0L)

                server.content = JSONObject(socksSubscription("updated-upstream"))
                    .put("log", JSONObject().put("level", "debug"))
                    .put("inbounds", JSONArray().put(JSONObject().put("type", "mixed").put("listen", "0.0.0.0").put("listen_port", 1)))
                    .put("route", JSONObject().put("final", "subscription-group"))
                    .toString()
                val scheduled = profiles.get(id)
                scheduled.typed.autoUpdate = true
                scheduled.typed.lastUpdated = Date(0)
                ProfileManager.update(scheduled)
                assertTrue(UpdateProfileWork.updateProfiles(activity.applicationContext))
                content = profiles.read(id)
                assertTrue(configurationNodes(content).any { it.optString("tag") == "local-node" })
                assertTrue(configurationNodes(content).any { it.optString("tag") == "updated-upstream" })
                assertFalse(configurationNodes(content).any { it.optString("tag") == "upstream" })
                assertEquals(configuration(original).getJSONObject("log").toString(), configuration(content).getJSONObject("log").toString())
                assertEquals(configuration(original).getJSONArray("inbounds").toString(), configuration(content).getJSONArray("inbounds").toString())
                assertEquals(configuration(original).getJSONObject("route").toString(), configuration(content).getJSONObject("route").toString())
                val updatedSelector = configurationNodes(content).single { it.optString("tag") == "proxy" }
                assertEquals(setOf("updated-upstream", "local-node"), (0 until updatedSelector.getJSONArray("outbounds").length()).map { updatedSelector.getJSONArray("outbounds").getString(it) }.toSet())
                val updatedAt = profiles.get(id).typed.lastUpdated.time
                assertTrue(updatedAt > 0L)
                assertEquals(2, server.requests.get())

                val failingProfiles = BoxProfiles(beforeApply = { ProfileContent.requireStableService(activity.serviceStatus.value) }) { recover ->
                    check(recover) { "Injected subscription application failure" }
                    withContext(Dispatchers.Main) { activity.applyProfile(recover = true) }
                }
                server.content = socksSubscription("failed-upstream")
                assertNotNull(runCatching { failingProfiles.update(id) }.exceptionOrNull())
                assertEquals(content, profiles.read(id))
                assertEquals(updatedAt, profiles.get(id).typed.lastUpdated.time)
                assertEquals(id, Settings.selectedProfile)
                withTimeout(15_000) { activity.serviceStatus.first { it == Status.Started } }
                SubscriptionServer(mixedVlessSubscription("separate-source-node", 1)).use { separate ->
                    val subscriptions = nodeSubscriptions(activity)
                    subscriptions.importSubscription(subscriptionArgs(profiles, id, "Separate source", separate.url))
                    val beforeUnsupported = profiles.read(id)
                    val metadata = profiles.get(id)
                    assertEquals(1, metadata.typed.subscriptions.single().skipped)
                    val generation = ProfileContent.generation
                    server.content = mixedVlessSubscription("rejected-legacy-node", 1)
                    val failure = runCatching { profiles.update(id) }.exceptionOrNull()
                    assertTrue(failure is IllegalArgumentException)
                    assertTrue(failure!!.message.orEmpty().contains("1 unsupported nodes"))
                    assertTrue(failure.message.orEmpty().contains("transport of this node"))
                    assertFalse(failure.message.orEmpty().contains("ignored-kcp-node"))
                    assertEquals(generation, ProfileContent.generation)
                    assertEquals(beforeUnsupported, profiles.read(id))
                    val preserved = profiles.get(id)
                    assertEquals(metadata.typed.remoteURL, preserved.typed.remoteURL)
                    assertEquals(metadata.typed.subscriptions, preserved.typed.subscriptions)
                    assertEquals(metadata.typed.lastUpdated.time, preserved.typed.lastUpdated.time)
                    assertEquals(id, Settings.selectedProfile)
                    assertEquals(Status.Started, activity.serviceStatus.value)
                }
            }
        }
    }

    @Test fun queryModesSurviveReopeningTheSettingsDatabase() = runBlocking(Dispatchers.IO) {
        Settings.dataStore.initialize()
        val before = Settings.perAppProxyPackageQueryMode
        try {
            for (mode in listOf(Settings.PACKAGE_QUERY_MODE_ROOT, Settings.PACKAGE_QUERY_MODE_SHIZUKU)) {
                PackageQueryManager.setQueryMode(mode)
                Settings.dataStore.flush()
                // Read a new Room connection, not the data store's in-memory cache.
                val stored = Room.databaseBuilder(
                    Application.application, KeyValueDatabase::class.java, Path.SETTINGS_DATABASE_PATH,
                ).build()
                try {
                    assertEquals(mode, stored.keyValuePairDao()[SettingsKey.PER_APP_PROXY_PACKAGE_QUERY_MODE]?.string)
                } finally {
                    stored.close()
                }
                assertEquals(mode, PackageQueryManager.queryMode.value)
            }
        } finally {
            PackageQueryManager.setQueryMode(before)
            Settings.dataStore.flush()
        }
    }

    @Test fun logLevelFollowsSuccessfulConfigurationReloads() = withRunningProfile { activity, profiles, id ->
        val logs = withContext(Dispatchers.Main) { ViewModelProvider(activity)[LogViewModel::class.java] }
        // The matching core defaults to trace when log.level is omitted.
        withTimeout(15_000) { logs.uiState.first { it.defaultLogLevel == LogLevel.TRACE } }
        for ((configured, expected) in listOf("warn" to LogLevel.WARNING, "debug" to LogLevel.DEBUG, "info" to LogLevel.INFO)) {
            val previous = profiles.read(id)
            profiles.writeContent(id, """{"log":{"level":"$configured"}}""", BoxProfiles.revision(previous))
            assertEquals(Status.Started, activity.serviceStatus.value)
            withTimeout(15_000) { logs.uiState.first { it.defaultLogLevel == expected } }
        }
        profiles.writeContent(id, "{}", BoxProfiles.revision(profiles.read(id)))
        withTimeout(15_000) { logs.uiState.first { it.defaultLogLevel == LogLevel.TRACE } }
    }

    @Test fun lanCacheTracksEqualSizeWritesSuccessfulReloadsAndRecovery() = withRunningProfile { activity, profiles, id ->
        val original = occupiedPortConfig(0)
        val shared = original.replace("127.0.0.1", "0.0.0.0").padEnd(original.length)
        profiles.writeContent(id, original, BoxProfiles.revision("{}"))
        val details = withContext(Dispatchers.Main) { BoxDashboard(activity) { _, _ -> } }
        try {
            val initial = details.refreshNetwork()
            assertEquals("127.0.0.1", rows(initial["activeBindings"]).single()["listen"])
            val file = File(profiles.get(id).typed.path)
            val modified = file.lastModified()
            ProfileContent.mutex.withLock {
                // Model saved but unapplied content with identical file size and timestamp.
                ProfileContent.write(file, shared)
                assertTrue(file.setLastModified(modified))
                assertEquals(modified, file.lastModified())
                assertEquals(original.length.toLong(), file.length())
            }
            val unapplied = details.refreshNetwork()
            assertEquals(BoxProfiles.revision(shared), unapplied["revision"])
            assertEquals("0.0.0.0", rows(unapplied["bindings"]).single()["listen"])
            assertEquals("127.0.0.1", rows(unapplied["activeBindings"]).single()["listen"])
            assertEquals(false, unapplied["enabled"])

            profiles.writeContent(id, original, BoxProfiles.revision(shared))
            profiles.writeContent(id, shared, BoxProfiles.revision(original))
            val applied = details.refreshNetwork()
            assertEquals(true, applied["running"])
            assertEquals(true, applied["enabled"])
            assertEquals("0.0.0.0", rows(applied["activeBindings"]).single()["listen"])
            ServerSocket(0, 1, InetAddress.getByName("127.0.0.1")).use { occupied ->
                assertNotNull(runCatching {
                    profiles.writeContent(id, occupiedPortConfig(occupied.localPort), BoxProfiles.revision(shared))
                }.exceptionOrNull())
            }
            val recovered = details.refreshNetwork()
            assertEquals(BoxProfiles.revision(shared), recovered["revision"])
            assertEquals(true, recovered["running"])
            assertEquals(applied["bindings"], recovered["bindings"])
            assertEquals(applied["activeBindings"], recovered["activeBindings"])

            withContext(Dispatchers.Main) { activity.stopService() }
            withTimeout(15_000) { activity.serviceStatus.first { it == Status.Stopped } }
            val stopped = details.refreshNetwork()
            assertEquals(false, stopped["running"])
            assertTrue(rows(stopped["activeBindings"]).isEmpty())
            assertEquals(applied["bindings"], stopped["bindings"])
        } finally {
            withContext(Dispatchers.Main) { details.close() }
        }
    }

    @Test fun failedSelectionRestoresThePreviousSelectionAndService() = withRunningProfile { activity, profiles, id ->
        val previousMode = Settings.serviceMode
        ServerSocket(0, 1, InetAddress.getByName("127.0.0.1")).use { occupied ->
            val content = occupiedPortConfig(occupied.localPort)
            val candidate = profiles.save(mapOf("name" to "Broken selection", "content" to content))
            try {
                repeat(5) {
                    val failure = runCatching { profiles.select(candidate) { activity.serviceStatus.value } }.exceptionOrNull()
                    assertNotNull("Selecting a configuration must report its reload failure", failure)
                    assertEquals(id, Settings.selectedProfile)
                    assertEquals(previousMode, Settings.serviceMode)
                    assertEquals(content, profiles.read(candidate))
                    withTimeout(15_000) { activity.serviceStatus.first { it == Status.Started } }
                    assertEquals(0, failure!!.suppressed.size)
                    // The RPC error and recovery's stop request must share one
                    // teardown. A late second teardown used to unregister the
                    // recovered service's receiver and leak its cache-file lock.
                    withContext(Dispatchers.Main) { activity.stopService() }
                    withTimeout(15_000) { activity.serviceStatus.first { it == Status.Stopped } }
                    assertNull(BoxService.dashboardServer)
                    withContext(Dispatchers.Main) { activity.applyProfile(recover = true) }
                    assertEquals(Status.Started, activity.serviceStatus.value)
                }
            } finally {
                profiles.delete(candidate)
            }
        }
    }

    @Test fun failedManualUpdateRestoresContentTimestampAndService() {
        SubscriptionServer(socksSubscription()).use { server ->
            withRunningProfile(server.url) { activity, profiles, id ->
                val before = profiles.get(id).typed.lastUpdated.time
                val configFile = File(profiles.get(id).typed.path)
                var appliedNewNodes = false
                val failingProfiles = BoxProfiles(beforeApply = { ProfileContent.requireStableService(activity.serviceStatus.value) }) { recover ->
                    if (!recover) {
                        // The apply callback already owns ProfileContent.mutex.
                        appliedNewNodes = configurationNodes(configFile.readText()).any { it.optString("tag") == "upstream" }
                        error("Injected subscription application failure")
                    }
                    withContext(Dispatchers.Main) { activity.applyProfile(recover = true) }
                }
                val failure = runCatching { failingProfiles.update(id) }.exceptionOrNull()
                assertNotNull("A failed subscription must report its application failure", failure)
                assertTrue("The new nodes must have been persisted before application failed", appliedNewNodes)
                assertTrue(failure!!.message.orEmpty().contains("Injected subscription application failure"))
                assertEquals(1, server.requests.get())
                assertEquals("{}", profiles.read(id))
                assertEquals(before, profiles.get(id).typed.lastUpdated.time)
                withTimeout(15_000) { activity.serviceStatus.first { it == Status.Started } }
                assertEquals(0, failure.suppressed.size)
            }
        }
    }

    @Test fun largeSubscriptionsUpdateImportAndPreserveServiceState() = withPausedScheduledUpdates {
        SubscriptionServer(socksSubscription().padEnd(16 * 1024 * 1024 + 1)).use { server ->
            withRunningProfile(server.url) { activity, profiles, id ->
                profiles.update(id)
                assertEquals(listOf("upstream"), configurationNodes(profiles.read(id)).map { it.optString("tag") })
                assertTrue(profiles.get(id).typed.lastUpdated.time > 0L)

                val profile = profiles.get(id)
                profile.typed.autoUpdate = true
                profile.typed.lastUpdated = Date(0)
                ProfileManager.update(profile)
                assertTrue(UpdateProfileWork.updateProfiles(activity.applicationContext))
                assertEquals(listOf("upstream"), configurationNodes(profiles.read(id)).map { it.optString("tag") })
                assertTrue(profiles.get(id).typed.lastUpdated.time > 0L)

                val profileIds = ProfileManager.list().map { it.id }.toSet()
                val subscriptions = nodeSubscriptions(activity)
                subscriptions.importSubscription(subscriptionArgs(profiles, id, "Large subscription", server.url))
                val imported = profiles.get(id).typed.subscriptions.single()
                assertEquals(server.url, imported.url)
                assertTrue(imported.tags.isNotEmpty())
                assertTrue(configurationNodes(profiles.read(id)).any { it.optString("tag") in imported.tags && it.optString("server") == "127.0.0.1" })
                assertEquals(profileIds, ProfileManager.list().map { it.id }.toSet())
                assertEquals(id, Settings.selectedProfile)
                assertEquals(Status.Started, activity.serviceStatus.value)
                assertEquals(3, server.requests.get())
            }
        }
    }

    @Test fun urlSubscriptionsImportOnlyNodesIntoTheSelectedDefaultConfiguration() = withRunningProfile { activity, profiles, id ->
        val upstream = """{
            "log":{"level":"debug"},
            "dns":{"servers":[{"type":"invalid-upstream-dns","tag":"remote-dns"}]},
            "inbounds":[{"type":"mixed","tag":"remote-in","listen":"0.0.0.0","listen_port":1}],
            "route":{"final":"remote-group"},
            "experimental":{"scripts":{"enabled":true,"http":[{"pattern":".*","source":"remote-script"}]}},
            "proxy-providers":{"remote-provider":{"url":"https://example.test/provider"}},
            "outbounds":[
                {"type":"socks","tag":"subscription-node","server":"127.0.0.1","server_port":1080},
                {"type":"direct","tag":"remote-direct"},
                {"type":"selector","tag":"remote-group","outbounds":["subscription-node"]},
                {"type":"urltest","tag":"remote-auto","outbounds":["subscription-node"]}
            ]
        }"""
        SubscriptionServer(upstream).use { server ->
            val defaultId = profiles.ensureDefault()
            val before = profiles.get(defaultId)
            val original = profiles.read(defaultId)
            val profileIds = ProfileManager.list().map { it.id }.toSet()
            withContext(Dispatchers.Main) { activity.stopService() }
            withTimeout(15_000) { activity.serviceStatus.first { it == Status.Stopped } }
            profiles.select(defaultId) { activity.serviceStatus.value }
            try {
                val subscriptions = nodeSubscriptions(activity)
                subscriptions.importSubscription(subscriptionArgs(profiles, defaultId, "Nodes-only URL subscription", server.url))
                val saved = configuration(profiles.read(defaultId))
                val defaults = configuration(original)
                for (key in listOf("dns", "inbounds", "route", "log", "experimental")) {
                    assertEquals("Subscription replaced local $key", defaults.opt(key)?.toString(), saved.opt(key)?.toString())
                }
                val outbounds = configurationNodes(profiles.read(defaultId))
                assertEquals(listOf("subscription-node"), outbounds.filter { it.optString("type") == "socks" }.map { it.getString("tag") })
                assertFalse(outbounds.any { it.optString("tag") in setOf("remote-direct", "remote-group", "remote-auto") })
                assertFalse(saved.has("proxy-providers"))
                assertEquals(defaultId, Settings.selectedProfile)
                assertEquals(profileIds, ProfileManager.list().map { it.id }.toSet())
                val savedProfile = profiles.get(defaultId)
                assertTrue(savedProfile.typed.isDefault)
                assertEquals(TypedProfile.Type.Local, savedProfile.typed.type)
                assertEquals("", savedProfile.typed.remoteURL)
                val source = savedProfile.typed.subscriptions.single { it.name == "Nodes-only URL subscription" }
                assertEquals(server.url, source.url)
                assertEquals(listOf("subscription-node"), source.tags)
                assertTrue(source.updatedAt > 0L)
                assertEquals(Status.Stopped, activity.serviceStatus.value)
                assertEquals(1, server.requests.get())
            } finally {
                ProfileContent.mutex.withLock {
                    ProfileContent.write(File(before.typed.path), original)
                    ProfileManager.update(before)
                }
                profiles.select(id) { activity.serviceStatus.value }
            }
        }
    }

    @Test fun base64UrlSubscriptionsImportAndUpdateProxyNodes() = withRunningProfile { activity, profiles, id ->
        fun encoded(tag: String): String = android.util.Base64.encodeToString(
            "socks5://127.0.0.1:1080#$tag".toByteArray(Charsets.UTF_8), android.util.Base64.NO_WRAP)
        SubscriptionServer(encoded("encoded-node")).use { server ->
            val subscriptions = nodeSubscriptions(activity)
            subscriptions.importSubscription(subscriptionArgs(profiles, id, "Base64 subscription", server.url))
            val source = profiles.get(id).typed.subscriptions.single()
            assertTrue(configurationNodes(profiles.read(id)).any { it.optString("tag") == "encoded-node" && it.optString("type") == "socks" })
            server.content = encoded("updated-encoded-node")
            subscriptions.update(id, source.id)
            val outbounds = configurationNodes(profiles.read(id))
            assertTrue(outbounds.any { it.optString("tag") == "updated-encoded-node" && it.optString("type") == "socks" })
            assertFalse(outbounds.any { it.optString("tag") == "encoded-node" })
            assertEquals(listOf("updated-encoded-node"), profiles.get(id).typed.subscriptions.single().tags)
            assertEquals(id, Settings.selectedProfile)
            assertEquals(Status.Started, activity.serviceStatus.value)
            assertEquals(2, server.requests.get())
        }
    }

    @Test fun mixedSubscriptionReportsPersistWhileUpdatesProtectLocalAndOtherSources() = withRunningProfile { activity, profiles, id ->
        val subscriptions = nodeSubscriptions(activity)
        val nodes = BoxNodes(beforeApply = { ProfileContent.requireStableService(activity.serviceStatus.value) }) { recover ->
            withContext(Dispatchers.Main) { activity.applyProfile(recover = recover) }
        }
        nodes.add(JSONObject(socksSubscription("local-report-node")).getJSONArray("outbounds").toString(), id, BoxProfiles.revision(profiles.read(id)))
        SubscriptionServer(socksSubscription("other-report-node")).use { other ->
            subscriptions.importSubscription(subscriptionArgs(profiles, id, "Other report source", other.url))
            val otherSource = profiles.get(id).typed.subscriptions.single()
            val keptNodes = configurationNodes(profiles.read(id)).associate { it.getString("tag") to it.toString() }
            SubscriptionServer(mixedVlessSubscription("supported-tcp-node", 1)).use { server ->
                val imported = subscriptions.importSubscription(subscriptionArgs(profiles, id, "Mixed TCP and KCP source", server.url))
                val source = profiles.get(id).typed.subscriptions.single { it.url == server.url }
                assertEquals(1, imported["added"])
                assertEquals(1, imported["skipped"])
                assertEquals(source.warnings.map { it.toWire() }, imported["warnings"])
                assertEquals(listOf("supported-tcp-node"), source.tags)
                assertEquals(1, source.skipped)
                assertEquals(1, source.warnings.single().count)
                assertTrue(source.warnings.single().reason.contains("transport of this node"))
                for (secret in listOf("ignored-kcp-node", "11111111-1111-4111-8111-111111111111", "127.0.0.1")) {
                    assertFalse("Warning leaked subscription node data", source.warnings.single().reason.contains(secret))
                }
                assertEquals(server.url, source.url)
                assertTrue(source.updatedAt > 0L)
                subscriptions.save(source.toWire() + mapOf("profileId" to id, "name" to "Renamed mixed source"))
                assertEquals(source.copy(name = "Renamed mixed source"), profiles.get(id).typed.subscriptions.single { it.id == source.id })

                server.content = mixedVlessSubscription("updated-tcp-node", 2)
                val updated = subscriptions.update(id, source.id)
                val saved = profiles.get(id).typed.subscriptions.single { it.id == source.id }
                assertEquals(1, updated["added"])
                assertEquals(2, updated["skipped"])
                assertEquals(saved.warnings.map { it.toWire() }, updated["warnings"])
                assertEquals(listOf("updated-tcp-node"), saved.tags)
                assertEquals(2, saved.skipped)
                assertEquals(2, saved.warnings.single().count)
                assertEquals(server.url, saved.url)
                assertEquals(otherSource, profiles.get(id).typed.subscriptions.single { it.id == otherSource.id })
                val updatedNodes = configurationNodes(profiles.read(id)).associate { it.getString("tag") to it.toString() }
                assertEquals(setOf("local-report-node", "other-report-node", "updated-tcp-node"), updatedNodes.keys)
                keptNodes.forEach { (tag, content) -> assertEquals(content, updatedNodes[tag]) }

                val beforeInvalid = profiles.read(id)
                val beforeSources = profiles.get(id).typed.subscriptions
                val generation = ProfileContent.generation
                server.content = mixedVlessSubscription("must-not-commit", 1) + "\nunknown://127.0.0.1:443#bad-node"
                assertTrue(runCatching { subscriptions.update(id, source.id) }.isFailure)
                assertEquals(generation, ProfileContent.generation)
                assertEquals(beforeInvalid, profiles.read(id))
                assertEquals(beforeSources, profiles.get(id).typed.subscriptions)

                server.content = mixedVlessSubscription("supported-again", 0)
                val cleared = subscriptions.update(id, source.id)
                assertEquals(0, cleared["skipped"])
                assertEquals(emptyList<Map<String, Any>>(), cleared["warnings"])
                val clearSource = profiles.get(id).typed.subscriptions.single { it.id == source.id }
                assertEquals(0, clearSource.skipped)
                assertTrue(clearSource.warnings.isEmpty())
                assertEquals(otherSource, profiles.get(id).typed.subscriptions.single { it.id == otherSource.id })
                assertEquals(server.url, clearSource.url)
                assertEquals(id, Settings.selectedProfile)
                assertEquals(Status.Started, activity.serviceStatus.value)
                assertEquals(4, server.requests.get())
            }
        }
    }

    @Test fun xhttpEncryptedSubscriptionsKeepFieldsOwnershipAndConfigurationSettings() = withRunningProfile { activity, profiles, id ->
        val original = """{
            "log":{"level":"warn"},
            "dns":{"servers":[{"type":"local","tag":"fixture-dns"}]},
            "inbounds":[{"type":"mixed","tag":"fixture-in","listen":"127.0.0.1","listen_port":0}],
            "outbounds":[{"type":"socks","tag":"local-encryption-node","server":"127.0.0.1","server_port":1080},
                {"type":"selector","tag":"proxy","outbounds":["local-encryption-node"]},
                {"type":"direct","tag":"local-direct"}],
            "route":{"final":"local-direct"}
        }"""
        profiles.writeContent(id, original, BoxProfiles.revision(profiles.read(id)))
        val subscriptions = nodeSubscriptions(activity)
        fun link(tag: String, mode: String, encryption: String): String {
            val query = linkedMapOf(
                "security" to "tls", "sni" to "upload.example.test", "type" to "xhttp",
                "mode" to mode, "host" to "upload.example.test", "path" to "/encrypted?fixture=1",
                "encryption" to encryption,
                "extra" to JSONObject().put("no_grpc_header", true).put("sc_max_each_post_bytes", "1024-2048").toString(),
            ).entries.joinToString("&") { (key, value) -> "$key=${URLEncoder.encode(value, "UTF-8")}" }
            return "vless://11111111-1111-4111-8111-111111111111@127.0.0.1:443?$query#$tag"
        }
        SubscriptionServer(socksSubscription("other-encryption-node")).use { other ->
            subscriptions.importSubscription(subscriptionArgs(profiles, id, "Other source", other.url))
            val otherSource = profiles.get(id).typed.subscriptions.single()
            val base = configuration(profiles.read(id))
            val localNode = configurationNodes(profiles.read(id)).single { it.optString("tag") == "local-encryption-node" }.toString()
            val encryption = "mlkem768x25519plus.native.0rtt.${VlessEncryptionTestKeys.rawX25519()}"
            SubscriptionServer(link("encrypted-xhttp-node", "stream-up", encryption)).use { server ->
                val profileIds = ProfileManager.list().map { it.id }.toSet()
                val imported = subscriptions.importSubscription(subscriptionArgs(profiles, id, "XHTTP Encryption source", server.url))
                assertEquals(1, imported["added"])
                assertEquals(0, imported["skipped"])
                assertEquals(emptyList<Map<String, Any>>(), imported["warnings"])
                var source = profiles.get(id).typed.subscriptions.single { it.url == server.url }
                assertEquals(listOf("encrypted-xhttp-node"), source.tags)
                assertEquals(0, source.skipped)
                assertTrue(source.warnings.isEmpty())
                assertTrue(source.updatedAt > 0L)
                assertEquals(profileIds, ProfileManager.list().map { it.id }.toSet())
                assertEquals(id, Settings.selectedProfile)

                suspend fun assertNode(tag: String, mode: String) {
                    val saved = configuration(profiles.read(id))
                    for (section in listOf("log", "dns", "route", "inbounds")) {
                        assertEquals("Subscription replaced $section", base.get(section).toString(), saved.get(section).toString())
                    }
                    val nodes = configurationNodes(profiles.read(id))
                    assertEquals(localNode, nodes.single { it.optString("tag") == "local-encryption-node" }.toString())
                    assertTrue(nodes.any { it.optString("tag") == "other-encryption-node" })
                    val node = nodes.single { it.optString("tag") == tag }
                    assertEquals(encryption, node.getString("encryption"))
                    val transport = node.getJSONObject("transport")
                    assertEquals("xhttp", transport.getString("type"))
                    assertEquals(mode, transport.getString("mode"))
                    assertEquals("/encrypted?fixture=1", transport.getString("path"))
                    val host = transport.get("host")
                    assertEquals("upload.example.test", if (host is JSONArray) host.getString(0) else host as String)
                    assertTrue(transport.getBoolean("no_grpc_header"))
                    assertEquals("1024-2048", transport.getString("sc_max_each_post_bytes"))
                    val group = nodes.single { it.optString("tag") == "proxy" }.getJSONArray("outbounds")
                    assertEquals(setOf("local-encryption-node", "other-encryption-node", tag), (0 until group.length()).map { group.getString(it) }.toSet())
                    assertEquals(otherSource, profiles.get(id).typed.subscriptions.single { it.id == otherSource.id })
                    assertEquals(Status.Started, activity.serviceStatus.value)
                }
                assertNode("encrypted-xhttp-node", "stream-up")

                server.content = link("updated-encrypted-xhttp-node", "packet-up", encryption)
                val updated = subscriptions.update(id, source.id)
                assertEquals(1, updated["added"])
                assertEquals(0, updated["skipped"])
                source = profiles.get(id).typed.subscriptions.single { it.id == source.id }
                assertEquals(listOf("updated-encrypted-xhttp-node"), source.tags)
                assertEquals(server.url, source.url)
                assertFalse(configurationNodes(profiles.read(id)).any { it.optString("tag") == "encrypted-xhttp-node" })
                assertNode("updated-encrypted-xhttp-node", "packet-up")

                val beforeFailure = profiles.read(id)
                val beforeSources = profiles.get(id).typed.subscriptions
                server.content = link("invalid-encryption-node", "packet-up", "mlkem768x25519plus.native.0rtt.invalid-key")
                assertTrue(runCatching { subscriptions.update(id, source.id) }.isFailure)
                assertEquals(beforeFailure, profiles.read(id))
                assertEquals(beforeSources, profiles.get(id).typed.subscriptions)
                assertEquals(id, Settings.selectedProfile)
                assertEquals(Status.Started, activity.serviceStatus.value)
            }
        }
    }

    @Test fun subscriptionsWithoutValidNodesDoNotCreateOrReplaceProfiles() = withRunningProfile { activity, profiles, id ->
        val profileIds = ProfileManager.list().map { it.id }.toSet()
        val rejected = listOf(
            "{}",
            """{"log":{"level":"debug"},"outbounds":[{"type":"direct","tag":"direct"}]}""",
            """{"outbounds":[{"type":"socks","tag":"invalid-node","server":"127.0.0.1","server_port":-1}]}""",
            """{"outbounds":[{"type":"socks","tag":"valid-node","server":"127.0.0.1","server_port":1080},
                {"type":"socks","tag":"invalid-node","server":"127.0.0.1","server_port":-1}]}""",
        )
        SubscriptionServer(socksSubscription("kept-source-node")).use { server ->
            val subscriptions = nodeSubscriptions(activity)
            assertTrue(runCatching { profiles.save(mapOf("name" to "Rejected remote-file import", "url" to server.url)) }.isFailure)
            assertEquals(0, server.requests.get())
            assertEquals(profileIds, ProfileManager.list().map { it.id }.toSet())
            server.content = "{}"
            val emptyGeneration = ProfileContent.generation
            assertTrue(runCatching { subscriptions.importSubscription(subscriptionArgs(profiles, id, "Empty subscription", server.url)) }.isFailure)
            assertTrue(profiles.get(id).typed.subscriptions.isEmpty())
            assertEquals(emptyGeneration, ProfileContent.generation)
            server.content = socksSubscription("kept-source-node")
            subscriptions.importSubscription(subscriptionArgs(profiles, id, "Kept subscription", server.url))
            val before = profiles.read(id)
            val timestamp = profiles.get(id).typed.lastUpdated.time
            val sources = profiles.get(id).typed.subscriptions
            for (content in rejected) {
                server.content = content
                val generation = ProfileContent.generation
                assertTrue(runCatching { subscriptions.importSubscription(subscriptionArgs(profiles, id, "Rejected subscription", server.url)) }.isFailure)
                assertTrue(runCatching { subscriptions.update(id, sources.single().id) }.isFailure)
                assertEquals(profileIds, ProfileManager.list().map { it.id }.toSet())
                assertEquals(generation, ProfileContent.generation)
                assertEquals(sources, profiles.get(id).typed.subscriptions)
                assertEquals(before, profiles.read(id))
                assertEquals(timestamp, profiles.get(id).typed.lastUpdated.time)
                assertEquals(id, Settings.selectedProfile)
                assertEquals(Status.Started, activity.serviceStatus.value)
            }
        }
    }

    @Test fun subscriptionCoroutineCancellationStopsTheNativeDownload() = runBlocking(Dispatchers.IO) {
        Application.application.setupLibbox()
        repeat(20) { attempt ->
            val gate = CountDownLatch(1)
            SubscriptionServer(socksSubscription(), gate).use { server ->
                try {
                    val dispatcher = if (attempt % 2 == 0) Dispatchers.IO else Dispatchers.Default
                    val request = async(dispatcher) { io.nekohasekai.sfa.utils.HTTPClient.getString(server.url) }
                    withTimeout(10_000) { while (server.requests.get() == 0) delay(10) }
                    request.cancel()
                    withTimeout(5_000) { request.join() }
                    assertTrue(request.isCancelled)
                } finally { gate.countDown() }
            }
        }
        SubscriptionServer(socksSubscription()).use { server ->
            assertEquals(server.content, withTimeout(5_000) { io.nekohasekai.sfa.utils.HTTPClient.getString(server.url) })
        }
    }

    @Test fun subscriptionSourcesUpdateOnlyTheirOwnNodesAndRetainRemovedSourcesNodes() = withRunningProfile { activity, profiles, id ->
        val subscriptions = nodeSubscriptions(activity)
        val nodes = BoxNodes(beforeApply = { ProfileContent.requireStableService(activity.serviceStatus.value) }) { recover ->
            withContext(Dispatchers.Main) { activity.applyProfile(recover = recover) }
        }
        nodes.add(JSONObject(socksSubscription("local-node")).getJSONArray("outbounds").toString(), id, BoxProfiles.revision(profiles.read(id)))
        val profileIds = ProfileManager.list().map { it.id }.toSet()
        SubscriptionServer(socksSubscription("first-source-node")).use { first ->
            SubscriptionServer(socksSubscription("second-source-node")).use { second ->
                subscriptions.importSubscription(subscriptionArgs(profiles, id, "First source", first.url))
                subscriptions.importSubscription(subscriptionArgs(profiles, id, "Second source", second.url))
                var sources = profiles.get(id).typed.subscriptions
                val firstId = sources.single { it.url == first.url }.id
                val secondId = sources.single { it.url == second.url }.id
                assertEquals(2, subscriptions.list(id).size)
                assertEquals(setOf("local-node", "first-source-node", "second-source-node"), configurationNodes(profiles.read(id)).map { it.getString("tag") }.toSet())
                profiles.save(mapOf("id" to id, "name" to "Profile with two sources"))
                assertEquals(sources, profiles.get(id).typed.subscriptions)

                val stale = subscriptions.prepareUpdate(id, firstId)
                subscriptions.save(mapOf("profileId" to id, "id" to firstId, "name" to "Renamed source",
                    "url" to first.url, "autoUpdate" to false, "interval" to 15))
                val beforeStale = profiles.read(id)
                assertTrue(runCatching { subscriptions.commitUpdate(stale, socksSubscription("stale-node")) }.isFailure)
                assertEquals(beforeStale, profiles.read(id))
                assertEquals("Renamed source", profiles.get(id).typed.subscriptions.single { it.id == firstId }.name)

                first.content = socksSubscription("updated-first-source-node")
                subscriptions.update(id, firstId)
                sources = profiles.get(id).typed.subscriptions
                assertEquals(listOf("updated-first-source-node"), sources.single { it.id == firstId }.tags)
                assertEquals(listOf("second-source-node"), sources.single { it.id == secondId }.tags)
                assertEquals(setOf("local-node", "updated-first-source-node", "second-source-node"), configurationNodes(profiles.read(id)).map { it.getString("tag") }.toSet())

                val owned = nodes.get("updated-first-source-node", id)
                val renamed = JSONObject(owned["content"] as String).put("tag", "renamed-source-node").toString()
                nodes.set("updated-first-source-node", renamed, owned["revision"] as String, id, BoxProfiles.revision(profiles.read(id)))
                assertEquals(listOf("renamed-source-node"), profiles.get(id).typed.subscriptions.single { it.id == firstId }.tags)
                subscriptions.update(id, firstId)
                assertEquals(listOf("updated-first-source-node"), profiles.get(id).typed.subscriptions.single { it.id == firstId }.tags)
                assertFalse(configurationNodes(profiles.read(id)).any { it.optString("tag") == "renamed-source-node" })

                val beforeDelete = configuration(profiles.read(id)).toString()
                subscriptions.delete(id, firstId)
                assertEquals(beforeDelete, configuration(profiles.read(id)).toString())
                assertEquals(listOf(secondId), profiles.get(id).typed.subscriptions.map { it.id })
                nodes.remove(listOf("second-source-node"), id, BoxProfiles.revision(profiles.read(id)))
                assertTrue(profiles.get(id).typed.subscriptions.single().tags.isEmpty())
                assertEquals(second.url, profiles.get(id).typed.subscriptions.single().url)
                assertFalse(configurationNodes(profiles.read(id)).any { it.optString("tag") == "second-source-node" })
                second.content = socksSubscription("updated-second-source-node")
                subscriptions.update(id, secondId)
                assertEquals(setOf("local-node", "updated-first-source-node", "updated-second-source-node"), configurationNodes(profiles.read(id)).map { it.getString("tag") }.toSet())
                assertEquals(profileIds, ProfileManager.list().map { it.id }.toSet())
                assertEquals(id, Settings.selectedProfile)
                assertEquals(Status.Started, activity.serviceStatus.value)
            }
        }
    }

    @Test fun pendingNodeSubscriptionImportRejectsSelectionAndContentChanges() {
        for (change in listOf("selection", "content")) {
            val release = CountDownLatch(1)
            SubscriptionServer(socksSubscription("pending-source-node"), release).use { server ->
                withRunningProfile { activity, profiles, id -> coroutineScope {
                    val subscriptions = nodeSubscriptions(activity)
                    val other = profiles.save(mapOf("name" to "Other subscription target", "content" to "{}"))
                    try {
                        val prepared = subscriptions.prepareImport(subscriptionArgs(profiles, id, "Pending source", server.url))
                        val imported = async { runCatching { subscriptions.commitImport(prepared, subscriptions.downloadImport(prepared)) } }
                        val edited = """{"log":{"level":"warn"}}"""
                        try {
                            withTimeout(10_000) { while (server.requests.get() == 0) delay(10) }
                            withTimeout(10_000) {
                                if (change == "selection") profiles.select(other) { activity.serviceStatus.value }
                                else profiles.writeContent(id, edited, BoxProfiles.revision("{}"))
                            }
                            assertFalse(imported.isCompleted)
                        } finally { release.countDown() }
                        assertTrue(imported.await().isFailure)
                        assertEquals(if (change == "content") edited else "{}", profiles.read(id))
                        assertEquals("{}", profiles.read(other))
                        assertTrue(profiles.get(id).typed.subscriptions.isEmpty())
                        assertTrue(profiles.get(other).typed.subscriptions.isEmpty())
                        assertEquals(Status.Started, activity.serviceStatus.value)
                    } finally {
                        if (Settings.selectedProfile == other) profiles.select(id) { activity.serviceStatus.value }
                        profiles.delete(other)
                    }
                } }
            }
        }
    }

    @Test fun subscriptionSourcesSurviveParcelProfileMetadataAndBackupRestore() = withRunningProfile { activity, profiles, id ->
        SubscriptionServer(mixedVlessSubscription("backed-up-source-node", 1)).use { server ->
            val subscriptions = nodeSubscriptions(activity)
            subscriptions.importSubscription(subscriptionArgs(profiles, id, "中文 source 🌐", server.url))
            val saved = profiles.get(id)
            val content = profiles.read(id)
            val converter = TypedProfile.Convertor()
            val decoded = converter.unmarshall(converter.marshall(saved.typed))
            assertEquals(saved.typed.subscriptions, decoded.subscriptions)
            assertEquals(1, decoded.subscriptions.single().skipped)
            assertEquals(1, decoded.subscriptions.single().warnings.single().count)
            assertEquals(saved.typed.path, decoded.path)
            assertEquals(saved.typed.type, decoded.type)
            profiles.save(mapOf("id" to id, "name" to "Renamed source profile"))
            assertEquals(saved.typed.subscriptions, profiles.get(id).typed.subscriptions)

            val backups = BoxBackups(activity, { emptyMap() }, {}, { _, _ -> }, {}, { _, _ -> }, BoxWebDAV(activity))
            val backup = backups.create()
            val entry = rows(backup["profiles"]).single { (it["id"] as Number).toLong() == id }
            val source = rows(entry["subscriptions"]).single()
            assertEquals(saved.typed.subscriptions.single().id, source["id"])
            assertEquals("中文 source 🌐", source["name"])
            assertEquals(server.url, source["url"])
            assertEquals(listOf("backed-up-source-node"), source["tags"])
            assertEquals(1, source["skipped"])
            assertEquals(1, rows(source["warnings"]).single()["count"])
            val read = backups.read(backups.encode(backup), "profiles")
            subscriptions.delete(id, saved.typed.subscriptions.single().id)
            assertTrue(profiles.get(id).typed.subscriptions.isEmpty())
            assertTrue(configurationNodes(profiles.read(id)).any { it.optString("tag") == "backed-up-source-node" })
            backups.restore(read, "profiles", "Compatible")
            assertEquals(saved.typed.subscriptions, profiles.get(id).typed.subscriptions)
            assertEquals(content, profiles.read(id))
            val legacySources = rows(entry["subscriptions"]).map { it.filterKeys { key -> key !in setOf("skipped", "warnings") } }
            val legacyExpected = saved.typed.subscriptions.map { it.copy(skipped = 0, warnings = emptyList()) }
            assertEquals(legacyExpected, ProfileNodeSubscription.decode(JSONArray(legacySources).toString()))
            val legacyEntries = rows(backup["profiles"]).map { profile ->
                if ((profile["id"] as Number).toLong() == id) profile + mapOf("subscriptions" to legacySources) else profile
            }
            val legacy = backups.read(backups.encode(backup + mapOf("profiles" to legacyEntries)), "profiles")
            subscriptions.delete(id, saved.typed.subscriptions.single().id)
            backups.restore(legacy, "profiles", "Compatible")
            assertEquals(legacyExpected, profiles.get(id).typed.subscriptions)
            assertEquals(content, profiles.read(id))
            assertEquals(id, Settings.selectedProfile)
            assertEquals(Status.Started, activity.serviceStatus.value)
        }
    }

    @Test fun subscriptionSourcesImportEndpointsAndReplaceTheirOwnedNodesWhileStopped() = withRunningProfile { activity, profiles, id ->
        withContext(Dispatchers.Main) { activity.stopService() }
        withTimeout(15_000) { activity.serviceStatus.first { it == Status.Stopped } }
        val key = android.util.Base64.encodeToString(ByteArray(32) { 1 }, android.util.Base64.NO_WRAP)
        val publicKey = android.util.Base64.encodeToString(ByteArray(32) { 2 }, android.util.Base64.NO_WRAP)
        SubscriptionServer("""{"endpoints":[{"type":"wireguard","tag":"source-endpoint","address":["10.0.0.2/32"],"private_key":"$key",
            "peers":[{"address":"127.0.0.1","port":51820,"public_key":"$publicKey","allowed_ips":["0.0.0.0/0"]}]}]}""").use { server ->
            SubscriptionServer(socksSubscription("other-source-outbound")).use { other ->
                val subscriptions = nodeSubscriptions(activity)
                subscriptions.importSubscription(subscriptionArgs(profiles, id, "Other outbound source", other.url))
                val otherSource = profiles.get(id).typed.subscriptions.single()
                subscriptions.importSubscription(subscriptionArgs(profiles, id, "Endpoint source", server.url))
                val source = profiles.get(id).typed.subscriptions.single { it.url == server.url }
                val endpoint = configuration(profiles.read(id)).getJSONArray("endpoints").getJSONObject(0)
                assertEquals("wireguard", endpoint.getString("type"))
                assertEquals(listOf("source-endpoint"), source.tags)
                assertFalse(configurationNodes(profiles.read(id)).any { it.optString("type") == "wireguard" })
                assertEquals(otherSource, profiles.get(id).typed.subscriptions.single { it.id == otherSource.id })
                server.content = socksSubscription("replacement-outbound")
                subscriptions.update(id, source.id)
                val endpoints = configuration(profiles.read(id)).optJSONArray("endpoints")
                assertTrue(endpoints == null || endpoints.length() == 0)
                assertEquals(listOf("replacement-outbound"), profiles.get(id).typed.subscriptions.single { it.id == source.id }.tags)
                assertEquals(otherSource, profiles.get(id).typed.subscriptions.single { it.id == otherSource.id })
                assertEquals(setOf("replacement-outbound", "other-source-outbound"), configurationNodes(profiles.read(id)).map { it.getString("tag") }.toSet())
                assertEquals(Status.Stopped, activity.serviceStatus.value)
            }
        }
    }

    @Test fun failedSubscriptionSourceApplicationRestoresContentAndSourceMetadata() = withRunningProfile { activity, profiles, id ->
        SubscriptionServer(mixedVlessSubscription("original-source-node", 1)).use { original ->
            val subscriptions = nodeSubscriptions(activity)
            subscriptions.importSubscription(subscriptionArgs(profiles, id, "Original source", original.url))
            val before = profiles.read(id)
            val profileBefore = profiles.get(id)
            assertEquals(1, profileBefore.typed.subscriptions.single().skipped)
            val file = File(profileBefore.typed.path)
            var persistedTags = emptyList<String>()
            var persistedSources = emptyList<ProfileNodeSubscription>()
            val applications = mutableListOf<Boolean>()
            val failing = BoxNodeSubscriptions(beforeApply = { ProfileContent.requireStableService(activity.serviceStatus.value) }) { recover ->
                applications.add(recover)
                if (!recover) {
                    persistedSources = profiles.get(id).typed.subscriptions
                    persistedTags = persistedSources.flatMap { it.tags }
                    assertTrue(configurationNodes(file.readText()).any { it.optString("tag").startsWith("failed-") })
                    error("Injected source application failure")
                }
                withContext(Dispatchers.Main) { activity.applyProfile(recover = true) }
            }
            SubscriptionServer(mixedVlessSubscription("failed-import-node", 3)).use { imported ->
                assertTrue(runCatching { failing.importSubscription(subscriptionArgs(profiles, id, "Failed source", imported.url)) }.isFailure)
                assertTrue("The new source metadata must be committed before application", "failed-import-node" in persistedTags)
                assertEquals(3, persistedSources.single { it.url == imported.url }.skipped)
                assertEquals(3, persistedSources.single { it.url == imported.url }.warnings.single().count)
                assertEquals(before, profiles.read(id))
                assertEquals(profileBefore.typed.subscriptions, profiles.get(id).typed.subscriptions)
                assertEquals(profileBefore.typed.lastUpdated.time, profiles.get(id).typed.lastUpdated.time)
            }
            original.content = mixedVlessSubscription("failed-update-node", 2)
            assertTrue(runCatching { failing.update(id, profileBefore.typed.subscriptions.single().id) }.isFailure)
            assertTrue("The updated source metadata must be committed before application", "failed-update-node" in persistedTags)
            assertEquals(2, persistedSources.single().skipped)
            assertEquals(2, persistedSources.single().warnings.single().count)
            assertEquals(before, profiles.read(id))
            assertEquals(profileBefore.typed.subscriptions, profiles.get(id).typed.subscriptions)
            assertEquals(profileBefore.typed.lastUpdated.time, profiles.get(id).typed.lastUpdated.time)
            assertEquals(listOf(false, true, false, true), applications)
            assertEquals(id, Settings.selectedProfile)
            assertEquals(Status.Started, activity.serviceStatus.value)
        }
    }

    @Test fun automaticUpdatesIncludeLocalAndDefaultConfigurationSources() = withPausedScheduledUpdates {
        withRunningProfile { activity, profiles, id ->
            val defaultId = profiles.ensureDefault()
            val defaultBefore = profiles.get(defaultId)
            val defaultContent = profiles.read(defaultId)
            val subscriptions = nodeSubscriptions(activity)
            SubscriptionServer(mixedVlessSubscription("local-source-node", 1)).use { local ->
                SubscriptionServer(socksSubscription("default-source-node")).use { default ->
                    try {
                        subscriptions.importSubscription(subscriptionArgs(profiles, id, "Local automatic source", local.url, autoUpdate = true))
                        withContext(Dispatchers.Main) { activity.stopService() }
                        withTimeout(15_000) { activity.serviceStatus.first { it == Status.Stopped } }
                        profiles.select(defaultId) { activity.serviceStatus.value }
                        subscriptions.importSubscription(subscriptionArgs(profiles, defaultId, "Default automatic source", default.url, autoUpdate = true))
                        profiles.select(id) { activity.serviceStatus.value }
                        withContext(Dispatchers.Main) { activity.applyProfile(recover = true) }
                        assertEquals(Status.Started, activity.serviceStatus.value)
                        ProfileContent.mutex.withLock {
                            for (profileId in listOf(id, defaultId)) {
                                val profile = profiles.get(profileId)
                                profile.typed.subscriptions = profile.typed.subscriptions.map { it.copy(updatedAt = 0L) }
                                ProfileManager.update(profile)
                            }
                        }
                        local.content = mixedVlessSubscription("updated-local-source-node", 2)
                        default.content = socksSubscription("updated-default-source-node")
                        assertTrue(UpdateProfileWork.updateProfiles(activity.applicationContext))
                        assertEquals(listOf("updated-local-source-node"), profiles.get(id).typed.subscriptions.single().tags)
                        assertEquals(2, profiles.get(id).typed.subscriptions.single().skipped)
                        assertEquals(2, profiles.get(id).typed.subscriptions.single().warnings.single().count)
                        assertEquals(listOf("updated-default-source-node"), profiles.get(defaultId).typed.subscriptions.single { it.url == default.url }.tags)
                        assertTrue(profiles.get(id).typed.subscriptions.single().updatedAt > 0L)
                        assertTrue(profiles.get(defaultId).typed.subscriptions.single { it.url == default.url }.updatedAt > 0L)
                        assertFalse(configurationNodes(profiles.read(id)).any { it.optString("tag") == "local-source-node" })
                        assertFalse(configurationNodes(profiles.read(defaultId)).any { it.optString("tag") == "default-source-node" })
                        assertEquals(TypedProfile.Type.Local, profiles.get(id).typed.type)
                        assertTrue(profiles.get(defaultId).typed.isDefault)
                        assertEquals(2, local.requests.get())
                        assertEquals(2, default.requests.get())
                        assertEquals(id, Settings.selectedProfile)
                        assertEquals(Status.Started, activity.serviceStatus.value)
                        val updated = profiles.get(id)
                        updated.typed.subscriptions = updated.typed.subscriptions.map { it.copy(updatedAt = 0L) }
                        ProfileManager.update(updated)
                        val beforeFailure = profiles.read(id)
                        val generation = ProfileContent.generation
                        local.content = "{}"
                        assertFalse(UpdateProfileWork.updateProfiles(activity.applicationContext))
                        assertEquals(beforeFailure, profiles.read(id))
                        assertEquals(updated.typed.subscriptions, profiles.get(id).typed.subscriptions)
                        assertEquals(updated.typed.lastUpdated.time, profiles.get(id).typed.lastUpdated.time)
                        assertEquals(generation, ProfileContent.generation)
                        assertEquals(3, local.requests.get())
                        assertEquals(2, default.requests.get())
                        assertEquals(Status.Started, activity.serviceStatus.value)
                    } finally {
                        withContext(Dispatchers.Main) { activity.stopService() }
                        withTimeout(15_000) { activity.serviceStatus.first { it == Status.Stopped } }
                        ProfileContent.mutex.withLock {
                            ProfileContent.write(File(defaultBefore.typed.path), defaultContent)
                            ProfileManager.update(defaultBefore)
                        }
                        profiles.select(id) { activity.serviceStatus.value }
                    }
                }
            }
        }
    }

    /** Direct updateProfiles calls must not race the periodic worker they reschedule. */
    @SuppressLint("RestrictedApi")
    private fun withPausedScheduledUpdates(block: () -> Unit) {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val original = WorkManagerImpl.getInstance(context)
        runBlocking(Dispatchers.IO) { cancelScheduledUpdates(original) }
        val configuration = Configuration.Builder()
            .setExecutor(SynchronousExecutor())
            .setTaskExecutor(SynchronousExecutor())
            .setWorkerFactory(object : WorkerFactory() {
                override fun createWorker(appContext: Context, workerClassName: String, workerParameters: WorkerParameters): ListenableWorker? =
                    if (workerClassName == UpdateProfileWork.UpdateTask::class.java.name)
                        PausedScheduledUpdate(appContext, workerParameters) else null
            }).build()
        WorkManagerTestInitHelper.initializeTestWorkManager(context, configuration)
        try {
            block()
        } finally {
            try {
                runBlocking(Dispatchers.IO) { cancelScheduledUpdates(WorkManagerImpl.getInstance(context)) }
                WorkManagerTestInitHelper.closeWorkDatabase()
            } finally {
                WorkManagerImpl.setDelegate(original)
                runBlocking(Dispatchers.IO) { UpdateProfileWork.reconfigureUpdater() }
            }
        }
    }

    @SuppressLint("RestrictedApi")
    private suspend fun cancelScheduledUpdates(manager: WorkManagerImpl) {
        val completed = ConcurrentHashMap.newKeySet<WorkGenerationalId>()
        val listener = ExecutionListener { id, _ -> completed.add(id) }
        manager.processor.addExecutionListener(listener)
        try {
            val running = manager.getWorkInfosForUniqueWork("UpdateProfile").get(15, TimeUnit.SECONDS)
                .mapNotNull { manager.processor.getRunningWorkSpec(it.id.toString())?.generationalId() }
            manager.cancelUniqueWork("UpdateProfile").result.get(15, TimeUnit.SECONDS)
            // Cancellation removes processor entries before coroutine cleanup
            // finishes. Wait for its completion callback as well as the request.
            withTimeout(15_000) {
                while (running.any { it !in completed }) delay(10)
            }
        } finally {
            manager.processor.removeExecutionListener(listener)
        }
    }

    private class PausedScheduledUpdate(context: Context, parameters: WorkerParameters) : ListenableWorker(context, parameters) {
        @Volatile private var completion: CallbackToFutureAdapter.Completer<Result>? = null

        override fun startWork(): ListenableFuture<Result> = CallbackToFutureAdapter.getFuture { completer ->
            completion = completer
            if (isStopped) completer.setCancelled()
            "Paused subscription scheduler in direct updater test"
        }

        override fun onStopped() { completion?.setCancelled() }
    }

    @Test fun selectedProfilesCannotBeDeletedWhileRunningOrStopped() = withRunningProfile { activity, profiles, id ->
        val file = File(profiles.get(id).typed.path)
        val running = BoxService.dashboardServer
        val failure = runCatching { profiles.delete(id) }.exceptionOrNull()
        assertTrue(failure is IllegalArgumentException)
        assertEquals("{}", file.readText())
        assertNotNull(ProfileManager.get(id))
        assertEquals(id, Settings.selectedProfile)
        assertEquals(Status.Started, activity.serviceStatus.value)
        assertSame(running, BoxService.dashboardServer)

        val other = profiles.save(mapOf("name" to "Unused profile", "content" to "{}"))
        val otherFile = File(profiles.get(other).typed.path)
        profiles.delete(other)
        assertNull(ProfileManager.get(other))
        assertFalse(otherFile.exists())
        assertEquals(id, Settings.selectedProfile)
        assertSame(running, BoxService.dashboardServer)

        withContext(Dispatchers.Main) { activity.stopService() }
        withTimeout(15_000) { activity.serviceStatus.first { it == Status.Stopped } }
        assertTrue(runCatching { profiles.delete(id) }.exceptionOrNull() is IllegalArgumentException)
        assertEquals("{}", file.readText())
        assertNotNull(ProfileManager.get(id))
        assertEquals(id, Settings.selectedProfile)
        assertEquals(Status.Stopped, activity.serviceStatus.value)
    }

    @Test fun deletionRechecksSelectionAfterWaitingForProfileChanges() = runBlocking(Dispatchers.IO) {
        Application.application.setupLibbox()
        Settings.dataStore.initialize()
        val oldSelection = Settings.selectedProfile
        val profiles = BoxProfiles {}
        val id = profiles.save(mapOf("name" to "Deletion race fixture", "content" to "{}"))
        try {
            Settings.selectedProfile = oldSelection
            val deletion = ProfileContent.mutex.withLock {
                val pending = async(start = CoroutineStart.UNDISPATCHED) { runCatching { profiles.delete(id) } }
                Settings.selectedProfile = id
                pending
            }
            assertTrue(deletion.await().exceptionOrNull() is IllegalArgumentException)
            assertEquals("{}", profiles.read(id))
            assertEquals(id, Settings.selectedProfile)
        } finally {
            Settings.selectedProfile = oldSelection
            Settings.dataStore.flush()
            profiles.delete(id)
        }
    }

    @Test fun failedAutomaticNodeParsingPreservesDataAndRequestsRetry() = withPausedScheduledUpdates {
        SubscriptionServer("""{"outbounds":[{"type":"socks","tag":"invalid-node","server":"127.0.0.1","server_port":-1}]}""").use { server ->
            withRunningProfile(server.url) { activity, profiles, id ->
                val profile = profiles.get(id)
                profile.typed.autoUpdate = true
                profile.typed.lastUpdated = Date(0)
                ProfileManager.update(profile)
                val generation = ProfileContent.generation
                assertFalse(UpdateProfileWork.updateProfiles(activity.applicationContext))
                assertEquals(1, server.requests.get())
                assertEquals("{}", profiles.read(id))
                assertEquals(0L, profiles.get(id).typed.lastUpdated.time)
                assertEquals(generation, ProfileContent.generation)
                assertEquals(id, Settings.selectedProfile)
                assertEquals(Status.Started, activity.serviceStatus.value)
            }
        }
    }

    @Test fun contentAndBackupWritesRejectServiceTransitionsBeforeChangingData() = withRunningProfile { activity, profiles, id ->
        val before = profiles.get(id)
        val running = BoxService.dashboardServer
        val backups = BoxBackups(activity, { emptyMap() }, {}, { _, _ -> fail("A rejected restore must not restart") }, {}, { _, _ -> }, BoxWebDAV(activity))
        val backup = backups.create()
        val generation = ProfileContent.generation
        try {
            for (status in listOf(Status.Starting, Status.Stopping)) {
                withContext(Dispatchers.Main) { activity.serviceStatus.value = status }
                assertTrue(runCatching { profiles.writeContent(id, """{"log":{"level":"debug"}}""", BoxProfiles.revision("{}")) }.exceptionOrNull() is IllegalArgumentException)
                assertTrue(runCatching { profiles.save(mapOf("id" to id, "name" to "Rejected edit", "content" to """{"log":{"level":"debug"}}""", "revision" to BoxProfiles.revision("{}"))) }.exceptionOrNull() is IllegalArgumentException)
                assertTrue(runCatching { backups.restore(backup, "all", "Compatible") }.exceptionOrNull() is IllegalArgumentException)
                assertEquals("{}", profiles.read(id))
                assertEquals(before.name, profiles.get(id).name)
                assertEquals(before.typed.lastUpdated.time, profiles.get(id).typed.lastUpdated.time)
                assertEquals(generation, ProfileContent.generation)
                assertSame(running, BoxService.dashboardServer)
            }
        } finally { withContext(Dispatchers.Main) { activity.serviceStatus.value = Status.Started } }
    }

    @Test fun reloadWithoutLocationPermissionRollsBackAndRecovers() = withRunningProfile { activity, profiles, id ->
        val permission = if (Build.VERSION.SDK_INT < 29) android.Manifest.permission.ACCESS_FINE_LOCATION else android.Manifest.permission.ACCESS_BACKGROUND_LOCATION
        assertEquals(android.content.pm.PackageManager.PERMISSION_DENIED, activity.checkSelfPermission(permission))
        val before = profiles.get(id).typed.lastUpdated.time
        val config = """{"outbounds":[{"type":"direct","tag":"direct"}],"route":{"rules":[{"wifi_ssid":["B-box test network"],"action":"route","outbound":"direct"}]}}"""
        Libbox.checkConfig(config)
        val failure = runCatching { profiles.writeContent(id, config, BoxProfiles.revision("{}")) }.exceptionOrNull()
        assertNotNull("Missing location permission must fail a reload", failure)
        assertTrue(failure!!.message.orEmpty().contains("Location permission"))
        assertEquals("{}", profiles.read(id))
        assertEquals(before, profiles.get(id).typed.lastUpdated.time)
        assertEquals(Status.Started, activity.serviceStatus.value)
        assertEquals("{}", BoxService.dashboardConfig.value?.content)
        assertEquals(0, failure.suppressed.size)
    }

    @Test fun failedProfileFormSaveRestoresBothMetadataAndContent() = withRunningProfile { activity, profiles, id ->
        val before = profiles.get(id)
        ServerSocket(0, 1, InetAddress.getByName("127.0.0.1")).use { occupied ->
            val failure = runCatching { profiles.save(mapOf("id" to id, "name" to "Failed edit",
                "content" to occupiedPortConfig(occupied.localPort), "revision" to BoxProfiles.revision("{}"))) }.exceptionOrNull()
            assertNotNull(failure)
            assertEquals(before.name, profiles.get(id).name)
            assertEquals(before.typed.lastUpdated.time, profiles.get(id).typed.lastUpdated.time)
            assertEquals("{}", profiles.read(id))
            assertEquals(Status.Started, activity.serviceStatus.value)
        }
    }

    @Test fun pendingSubscriptionDownloadAllowsEditingAndRejectsItsStaleResult() {
        val release = CountDownLatch(1)
        SubscriptionServer(socksSubscription(), release).use { server ->
            withRunningProfile(server.url) { activity, profiles, id -> coroutineScope {
                val update = async { runCatching { profiles.update(id) } }
                val edited = """{"log":{"level":"warn"}}"""
                try {
                    withTimeout(10_000) { while (server.requests.get() == 0) delay(10) }
                    withTimeout(10_000) {
                        assertEquals("{}", profiles.read(id))
                        profiles.writeContent(id, edited, BoxProfiles.revision("{}"))
                    }
                    assertFalse(update.isCompleted)
                } finally { release.countDown() }
                assertTrue(update.await().exceptionOrNull()?.message.orEmpty().contains("changed during download"))
                assertEquals(edited, profiles.read(id))
                assertEquals(0L, profiles.get(id).typed.lastUpdated.time)
                assertEquals(Status.Started, activity.serviceStatus.value)
            } }
        }
    }

    @Test fun pendingAutomaticDownloadDoesNotBlockDisablingTheSubscription() = withPausedScheduledUpdates {
        val release = CountDownLatch(1)
        SubscriptionServer(socksSubscription(), release).use { server ->
            withRunningProfile(server.url) { activity, profiles, id -> coroutineScope {
                val profile = profiles.get(id)
                profile.typed.autoUpdate = true
                ProfileManager.update(profile)
                val update = async { UpdateProfileWork.updateProfiles(activity.applicationContext) }
                try {
                    withTimeout(10_000) { while (server.requests.get() == 0) delay(10) }
                    withTimeout(10_000) { profiles.save(mapOf("id" to id, "name" to "Disabled update", "url" to server.url, "autoUpdate" to false)) }
                    assertFalse(update.isCompleted)
                } finally { release.countDown() }
                assertFalse(update.await())
                assertEquals("{}", profiles.read(id))
                assertFalse(profiles.get(id).typed.autoUpdate)
                assertEquals(0L, profiles.get(id).typed.lastUpdated.time)
            } }
        }
    }

    @Test fun urlImportDownloadsWithoutHoldingTheConfigurationLock() {
        val release = CountDownLatch(1)
        SubscriptionServer(socksSubscription(), release).use { server ->
            withRunningProfile { activity, profiles, id -> coroutineScope {
                val subscriptions = nodeSubscriptions(activity)
                val profileIds = ProfileManager.list().map { it.id }.toSet()
                val prepared = subscriptions.prepareImport(subscriptionArgs(profiles, id, "Pending import", server.url))
                val imported = async {
                    subscriptions.commitImport(prepared, subscriptions.downloadImport(prepared))
                }
                try {
                    withTimeout(10_000) { while (server.requests.get() == 0) delay(10) }
                    withTimeout(10_000) { profiles.save(mapOf("id" to id, "name" to "Edited during import")) }
                    assertFalse(imported.isCompleted)
                } finally { release.countDown() }
                val result = imported.await()
                assertTrue((result["added"] as Number).toLong() > 0)
                assertEquals("Edited during import", profiles.get(id).name)
                assertEquals(id, Settings.selectedProfile)
                assertEquals(profileIds, ProfileManager.list().map { it.id }.toSet())
                assertEquals(server.url, profiles.get(id).typed.subscriptions.single().url)
                assertTrue(configurationNodes(profiles.read(id)).any { it.optString("tag") == "upstream" && it.optString("type") == "socks" })
            } }
        }
    }

    @Test fun subscriptionCommitRechecksUrlDeletionAndSessionIdentity() = withRunningProfile { _, profiles, _ ->
        val id = profiles.save(mapOf("name" to "Unselected download", "url" to "https://example.test/old", "content" to "{}"))
        val snapshot = profiles.prepareUpdate(id)
        profiles.save(mapOf("id" to id, "name" to "Changed URL", "url" to "https://example.test/new"))
        assertTrue(runCatching { profiles.commitUpdate(snapshot, socksSubscription()) }.isFailure)
        assertEquals("https://example.test/new", profiles.get(id).typed.remoteURL)
        profiles.delete(id)
        assertTrue(runCatching { profiles.commitUpdate(snapshot, socksSubscription()) }.isFailure)
        val session = io.nekohasekai.sfa.utils.CommandTarget.localSession()
        io.nekohasekai.sfa.utils.CommandTarget.setRemoteServer(null)
        assertTrue(runCatching { io.nekohasekai.sfa.utils.CommandTarget.requireLocalSession(session) }.isFailure)
    }

    @Test fun stoppedAndUnselectedUpdatesPreserveServiceState() = withPausedScheduledUpdates {
        SubscriptionServer(JSONObject(socksSubscription()).put("log", JSONObject().put("disabled", true)).toString()).use { server ->
            withRunningProfile(server.url) { activity, profiles, id ->
                val other = profiles.save(mapOf("name" to "Unselected subscription", "content" to "{}", "url" to server.url))
                try {
                    profiles.update(other)
                    assertEquals(listOf("upstream"), configurationNodes(profiles.read(other)).map { it.optString("tag") })
                    assertFalse(configuration(profiles.read(other)).has("log"))
                    assertEquals(id, Settings.selectedProfile)
                    assertEquals(Status.Started, activity.serviceStatus.value)

                    server.content = JSONObject(socksSubscription("background-node")).put("log", JSONObject().put("level", "warn")).toString()
                    val unselected = profiles.get(other)
                    unselected.typed.autoUpdate = true
                    unselected.typed.lastUpdated = Date(0)
                    ProfileManager.update(unselected)
                    assertTrue(UpdateProfileWork.updateProfiles(activity.applicationContext))
                    assertEquals(listOf("background-node"), configurationNodes(profiles.read(other)).map { it.optString("tag") })
                    assertFalse(configuration(profiles.read(other)).has("log"))
                    assertEquals(id, Settings.selectedProfile)
                    assertEquals(Status.Started, activity.serviceStatus.value)
                    val updatedUnselected = profiles.get(other)
                    updatedUnselected.typed.autoUpdate = false
                    ProfileManager.update(updatedUnselected)

                    withContext(Dispatchers.Main) { activity.stopService() }
                    withTimeout(15_000) { activity.serviceStatus.first { it == Status.Stopped } }
                    profiles.update(id)
                    assertEquals(listOf("background-node"), configurationNodes(profiles.read(id)).map { it.optString("tag") })
                    assertEquals(Status.Stopped, activity.serviceStatus.value)

                    server.content = socksSubscription("stopped-background-node")
                    val profile = profiles.get(id)
                    profile.typed.autoUpdate = true
                    profile.typed.lastUpdated = Date(0)
                    ProfileManager.update(profile)
                    assertTrue(UpdateProfileWork.updateProfiles(activity.applicationContext))
                    assertEquals(listOf("stopped-background-node"), configurationNodes(profiles.read(id)).map { it.optString("tag") })
                    assertTrue(profiles.get(id).typed.lastUpdated.time > 0L)
                    assertEquals(Status.Stopped, activity.serviceStatus.value)
                    profiles.select(other) { activity.serviceStatus.value }
                    assertEquals(other, Settings.selectedProfile)
                    assertEquals(Status.Stopped, activity.serviceStatus.value)
                } finally {
                    Settings.selectedProfile = id
                    Settings.dataStore.flush()
                    profiles.delete(other)
                }
            }
        }
    }

    private suspend fun importResourceDocument(
        activity: BoxActivity, profiles: BoxProfiles, id: Long, section: String, suffix: String, content: String,
    ): Pair<String, File> {
        val document = File.createTempFile("resource-document-", suffix, activity.cacheDir)
        try {
            document.writeText(content)
            val uri = FileProvider.getUriForFile(activity, "${activity.packageName}.cache", document)
            assertEquals("content", uri.scheme)
            val name = activity.documentName(uri)
            assertEquals(document.name, name)
            val prepared = activity.contentResolver.openInputStream(uri)!!.use {
                profiles.prepareResource(id, section, BoxProfiles.revision(profiles.read(id)), name, it)
            }
            return prepared.use {
                val result = profiles.commitResource(it)
                assertFalse(it.pending.exists())
                assertEquals(content, it.destination.readText())
                val imported = result["importedResource"] as Map<*, *>
                assertEquals(it.destination.name, imported["path"])
                imported["name"] as String to it.destination
            }
        } finally { document.delete() }
    }

    @Test fun localResourceImportsKeepRevisionsRecoverFailuresAndCleanCancelledCopies() = withRunningProfile { activity, profiles, id ->
        val directory = File(profiles.get(id).typed.path).parentFile!!
        val importedFiles = mutableListOf<File>()
        suspend fun prepare(name: String = "same.js", owner: BoxProfiles = profiles): BoxProfiles.PreparedResource =
            "new resource".byteInputStream().use {
                owner.prepareResource(id, "scripts", BoxProfiles.revision(profiles.read(id)), name, it)
            }
        try {
            profiles.writeContent(id, """{"outbounds":[{"type":"direct","tag":"kept"}],"experimental":{"scripts":{"resources":{"same.js":{"data":"old"}}}}}""",
                BoxProfiles.revision(profiles.read(id)))
            prepare().use { resource ->
                val saved = profiles.commitResource(resource)
                importedFiles.add(resource.destination)
                assertEquals("same.js (2)", (saved["importedResource"] as Map<*, *>)["name"])
                val content = JSONObject(profiles.read(id))
                assertEquals("kept", content.getJSONArray("outbounds").getJSONObject(0).getString("tag"))
                assertEquals("old", content.getJSONObject("experimental").getJSONObject("scripts")
                    .getJSONObject("resources").getJSONObject("same.js").getString("data"))
            }
            prepare("stale.js").use { resource ->
                val newer = JSONObject(profiles.read(id)).put("log", JSONObject().put("level", "warn")).toString()
                profiles.writeContent(id, newer, BoxProfiles.revision(profiles.read(id)))
                assertTrue(runCatching { profiles.commitResource(resource) }.isFailure)
                assertEquals(newer, profiles.read(id))
                assertFalse(resource.destination.exists())
            }
            val previous = profiles.read(id)
            val applications = mutableListOf<Boolean>()
            val failing = BoxProfiles { recover ->
                applications.add(recover)
                if (!recover) error("Injected resource apply failure")
                withContext(Dispatchers.Main) { activity.applyProfile(recover = true) }
            }
            prepare("failed.js", failing).use { resource ->
                assertTrue(runCatching { failing.commitResource(resource) }.isFailure)
                assertFalse(resource.destination.exists())
                assertEquals(previous, profiles.read(id))
                assertEquals(listOf(false, true), applications)
                assertEquals(Status.Started, activity.serviceStatus.value)
            }
            fun files() = directory.listFiles().orEmpty().map { it.name }.toSet()
            val beforeCopy = files()
            val unreadable = object : InputStream() {
                override fun read(): Int = throw IOException("Document read failed")
            }
            assertTrue(runCatching {
                unreadable.use { profiles.prepareResource(id, "scripts", BoxProfiles.revision(previous), "unreadable.js", it) }
            }.isFailure)
            assertEquals(beforeCopy, files())

            val reading = CountDownLatch(1)
            val closed = CountDownLatch(1)
            val blocked = object : InputStream() {
                override fun read(): Int {
                    reading.countDown()
                    check(closed.await(10, TimeUnit.SECONDS)) { "Resource stream was not closed on cancellation" }
                    throw IOException("Document closed")
                }
                override fun close() { closed.countDown() }
            }
            coroutineScope {
                val copy = async(Dispatchers.IO) {
                    blocked.use { profiles.prepareResource(id, "scripts", BoxProfiles.revision(previous), "cancelled.js", it).close() }
                }
                try {
                    assertTrue(reading.await(5, TimeUnit.SECONDS))
                    copy.cancel()
                    withTimeout(5_000) { copy.join() }
                } finally { blocked.close() }
            }
            assertEquals(beforeCopy, files())
            assertEquals(previous, profiles.read(id))
        } finally { importedFiles.forEach { it.delete() } }
    }

    private fun withRunningProfile(
        url: String = "",
        block: suspend (MainActivity, BoxProfiles, Long) -> Unit,
    ) {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        if (Build.VERSION.SDK_INT >= 33) {
            android.os.ParcelFileDescriptor.AutoCloseInputStream(
                instrumentation.uiAutomation.executeShellCommand("pm grant ${instrumentation.targetContext.packageName} android.permission.POST_NOTIFICATIONS"),
            ).use { it.readBytes() }
        }
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            lateinit var activity: MainActivity
            scenario.onActivity { activity = it }
            runBlocking(Dispatchers.IO) {
                Settings.dataStore.initialize()
                val oldSelection = Settings.selectedProfile
                val oldMode = Settings.serviceMode
                val profiles = BoxProfiles(beforeApply = { ProfileContent.requireStableService(activity.serviceStatus.value) }) {
                    recover -> withContext(Dispatchers.Main) { activity.applyProfile(recover = recover) }
                }
                val id = profiles.save(mapOf("name" to "Subscription recovery fixture", "content" to "{}", "url" to url, "updatedAt" to 0L))
                try {
                    Settings.selectedProfile = id
                    Settings.dataStore.flush()
                    withContext(Dispatchers.Main) { activity.applyProfile(recover = true) }
                    assertEquals(Status.Started, activity.serviceStatus.value)
                    block(activity, profiles, id)
                } finally {
                    withContext(Dispatchers.Main) { activity.stopService() }
                    withTimeout(15_000) { activity.serviceStatus.first { it == Status.Stopped } }
                    ProfileManager.get(id)?.let { ProfileManager.delete(it); File(it.typed.path).delete() }
                    Settings.selectedProfile = oldSelection
                    Settings.serviceMode = oldMode
                    Settings.dataStore.flush()
                    UpdateProfileWork.reconfigureUpdater()
                }
            }
        }
    }

    private fun occupiedPortConfig(port: Int): String =
        """{"inbounds":[{"type":"mixed","listen":"127.0.0.1","listen_port":$port}]}"""

    private fun socksSubscription(tag: String = "upstream"): String =
        JSONObject().put("outbounds", JSONArray().put(JSONObject()
            .put("type", "socks").put("tag", tag).put("server", "127.0.0.1").put("server_port", 1080))).toString()

    private fun mixedVlessSubscription(tag: String, skipped: Int): String {
        val address = "vless://11111111-1111-4111-8111-111111111111@127.0.0.1:443"
        return (listOf("$address?security=tls&sni=example.test&type=tcp#$tag") +
            (1..skipped).map { "$address?security=tls&sni=example.test&type=kcp#ignored-kcp-node-$it" }).joinToString("\n")
    }

    private fun nodeSubscriptions(activity: BoxActivity) = BoxNodeSubscriptions(
        beforeApply = { ProfileContent.requireStableService(activity.serviceStatus.value) },
    ) { recover -> withContext(Dispatchers.Main) { activity.applyProfile(recover = recover) } }

    private suspend fun subscriptionArgs(profiles: BoxProfiles, id: Long, name: String, url: String, autoUpdate: Boolean = false): Map<String, Any?> =
        mapOf("profileId" to id, "profileRevision" to BoxProfiles.revision(profiles.read(id)),
            "name" to name, "url" to url, "autoUpdate" to autoUpdate, "interval" to 15)

    private fun configuration(content: String): JSONObject = JSONObject(Libbox.formatConfig(content).value)

    private fun configurationNodes(content: String): List<JSONObject> {
        val nodes = configuration(content).optJSONArray("outbounds") ?: return emptyList()
        return (0 until nodes.length()).map { nodes.getJSONObject(it) }
    }

    private fun rows(value: Any?): List<Map<*, *>> = (value as List<*>).map { it as Map<*, *> }

    private class SubscriptionServer(@Volatile var content: String, private val responseGate: CountDownLatch? = null) : Closeable {
        private val server = ServerSocket(0, 1, InetAddress.getByName("127.0.0.1"))
        val url = "http://127.0.0.1:${server.localPort}/profile.json"
        val requests = AtomicInteger()
        private val worker = thread(isDaemon = true, name = "subscription-fixture") {
            while (!server.isClosed) {
                try {
                    server.accept().use { socket ->
                        socket.soTimeout = 15_000
                        try {
                            val reader = socket.getInputStream().bufferedReader()
                            while (!reader.readLine().isNullOrEmpty()) { }
                            val bytes = content.toByteArray(Charsets.UTF_8)
                            requests.incrementAndGet()
                            check(responseGate?.await(30, TimeUnit.SECONDS) != false) { "Subscription test response was not released" }
                            val output = socket.getOutputStream()
                            output.write("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: ${bytes.size}\r\nConnection: close\r\n\r\n".toByteArray())
                            output.write(bytes)
                            output.flush()
                        } catch (_: SocketException) {
                            // Reloading/stopping the service cancels in-flight
                            // cron requests. The fixture must survive that peer
                            // disconnect and continue accepting later requests.
                        }
                    }
                } catch (failure: Exception) {
                    if (!server.isClosed) throw failure
                }
            }
        }
        override fun close() {
            responseGate?.countDown()
            server.close()
            worker.join(5_000)
        }
    }

    @Test fun failedRestartRollsBackConfigurationAndFullBackup() {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        if (Build.VERSION.SDK_INT >= 33) {
            android.os.ParcelFileDescriptor.AutoCloseInputStream(
                instrumentation.uiAutomation.executeShellCommand("pm grant ${instrumentation.targetContext.packageName} android.permission.POST_NOTIFICATIONS"),
            ).use { it.readBytes() }
        }
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            lateinit var activity: MainActivity
            scenario.onActivity { activity = it }
            runBlocking(Dispatchers.IO) {
                Settings.dataStore.initialize()
                val oldSelection = Settings.selectedProfile
                val profiles = BoxProfiles { recover -> withContext(Dispatchers.Main) { activity.applyProfile(restart = true, recover = recover) } }
                val id = profiles.save(mapOf("name" to "Recovery fixture", "content" to "{}"))
                val preferences = activity.getSharedPreferences("flclash_ui", 0)
                val oldPreferences = preferences.getString("value", "{}")
                try {
                    Settings.selectedProfile = id
                    Settings.dataStore.flush()
                    withContext(Dispatchers.Main) { activity.applyProfile(restart = true, recover = true) }
                    assertEquals(Status.Started, activity.serviceStatus.value)
                    ServerSocket(0, 1, InetAddress.getByName("127.0.0.1")).use { occupied ->
                        val invalidAtRuntime = """{"inbounds":[{"type":"mixed","listen":"127.0.0.1","listen_port":${occupied.localPort}}]}"""
                        val failure = runCatching { profiles.writeContent(id, invalidAtRuntime, BoxProfiles.revision("{}")) }.exceptionOrNull()
                        assertNotNull("Applying a configuration must wait for startup failure", failure)
                        assertEquals("{}", profiles.read(id))
                        assertEquals(Status.Started, activity.serviceStatus.value)
                        assertEquals(0, failure!!.suppressed.size)

                        val backups = BoxBackups(activity, { emptyMap() }, {},
                            { restart, recover -> withContext(Dispatchers.Main) { activity.applyProfile(restart, recover) } }, {}, { _, _ -> }, BoxWebDAV(activity))
                        val backup = backups.create()
                        @Suppress("UNCHECKED_CAST")
                        val entries = (backup["profiles"] as List<Map<String, Any?>>).map {
                            if ((it["id"] as Number).toLong() == id) it + mapOf("name" to "Broken restored profile", "content" to invalidAtRuntime) else it
                        }
                        val restoreFailure = runCatching {
                            backups.restore(backup + mapOf("profiles" to entries, "preferences" to mapOf("theme" to "dark")), "all", "Override")
                        }.exceptionOrNull()
                        assertNotNull("A failed restored service must fail the restore", restoreFailure)
                        assertEquals("Recovery fixture", profiles.get(id).name)
                        assertEquals("{}", profiles.read(id))
                        assertEquals(id, Settings.selectedProfile)
                        assertEquals(oldPreferences, preferences.getString("value", "{}"))
                        assertEquals(Status.Started, activity.serviceStatus.value)
                        assertEquals(0, restoreFailure!!.suppressed.size)
                    }
                } finally {
                    withContext(Dispatchers.Main) { activity.stopService() }
                    withTimeout(15000) { activity.serviceStatus.first { it == Status.Stopped } }
                    val fixture = ProfileManager.get(id)
                    if (fixture != null) { ProfileManager.delete(fixture); File(fixture.typed.path).delete() }
                    Settings.selectedProfile = oldSelection
                    preferences.edit().putString("value", oldPreferences).commit()
                    Settings.dataStore.flush()
                }
            }
        }
    }

    @Test fun failedBackupFileRollbackDoesNotRestartService() = checkBackupRollbackFailure(failFileRestore = true)

    @Test fun failedBackupSettingsRollbackDoesNotRestartService() = checkBackupRollbackFailure(failFileRestore = false)

    private fun checkBackupRollbackFailure(failFileRestore: Boolean) = withRunningProfile { activity, profiles, id ->
        val file = File(profiles.get(id).typed.path)
        val previous = file.readText()
        val previousName = profiles.get(id).name
        val preferences = activity.getSharedPreferences("flclash_ui", 0)
        val previousPreferences = preferences.getString("value", "{}")
        val blockers = listOf(file, File(file.path + ".new"), File(file.path + ".bak"))
        fun stages() = activity.cacheDir.listFiles().orEmpty().filter { it.isDirectory && it.name.startsWith("restore-") }.toSet()
        val previousStages = stages()
        val original = IllegalStateException("Restored service failed")
        val rollback = IOException("Settings rollback failed")
        val applications = mutableListOf<Boolean>()
        var settingsWrites = 0
        val backups = BoxBackups(activity, { emptyMap() }, {
            settingsWrites++
            if (!failFileRestore && settingsWrites == 2) throw rollback
        }, { restart, recover ->
            applications.add(recover)
            if (recover) {
                withContext(Dispatchers.Main) { activity.applyProfile(restart, recover) }
            } else {
                withContext(Dispatchers.Main) { activity.stopService() }
                withTimeout(15_000) { activity.serviceStatus.first { it == Status.Stopped } }
                if (failFileRestore) {
                    // API 24 writes the base file after a .bak rename; newer
                    // AtomicFile writes .new. Nonempty directories force a real
                    // I/O failure in both implementations without losing staging.
                    check(file.delete())
                    blockers.forEach { blocker ->
                        check(blocker.mkdir())
                        File(blocker, "occupied").writeText("rollback failure fixture")
                    }
                }
                throw original
            }
        }, {}, { _, _ -> }, BoxWebDAV(activity))
        try {
            val backup = backups.create()
            @Suppress("UNCHECKED_CAST")
            val entries = (backup["profiles"] as List<Map<String, Any?>>).map {
                if ((it["id"] as Number).toLong() == id) it + mapOf("name" to "Changed backup name", "content" to """{"log":{"level":"warn"}}""") else it
            }
            val failure = runCatching {
                backups.restore(backup + mapOf("profiles" to entries, "preferences" to mapOf("theme" to "dark")), "all", "Override")
            }.exceptionOrNull()!!
            assertSame(original, failure)
            assertEquals(1, failure.suppressed.size)
            if (failFileRestore) assertTrue(failure.suppressed.single() is IOException) else assertSame(rollback, failure.suppressed.single())
            assertEquals(listOf(false), applications)
            assertEquals(Status.Stopped, activity.serviceStatus.value)
            assertNull(BoxService.dashboardServer)
            assertEquals(id, Settings.selectedProfile)
            assertEquals(previousName, profiles.get(id).name)
            assertEquals(previousPreferences, preferences.getString("value", "{}"))
            assertEquals(2, settingsWrites)
            if (!failFileRestore) assertEquals(previous, profiles.read(id))
            val retained = stages() - previousStages
            assertEquals(1, retained.size)
            assertTrue(retained.single().listFiles().orEmpty().any { it.isFile && it.readText() == previous })
        } finally {
            if (failFileRestore) blockers.forEach { if (it.isDirectory) check(it.deleteRecursively()) }
            ProfileContent.mutex.withLock { ProfileContent.write(file, previous) }
            (stages() - previousStages).forEach { it.deleteRecursively() }
        }
    }
}
