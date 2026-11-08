package io.nekohasekai.sfa.flutter

import android.os.Build
import androidx.lifecycle.ViewModelProvider
import androidx.room.Room
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import io.nekohasekai.libbox.Libbox
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
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.database.preference.KeyValueDatabase
import io.nekohasekai.sfa.vendor.PackageQueryManager
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
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.net.SocketException
import java.security.KeyStore
import java.security.SecureRandom
import java.security.cert.X509Certificate
import java.util.Date
import java.util.UUID
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.CountDownLatch
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
            val profileDirectory = File(profiles.get(id).typed.path).parentFile!!
            val fileResource = File(profileDirectory, "rewrite-$id-${UUID.randomUUID()}.json")
            val scriptResource = File(profileDirectory, "rewrite-$id-${UUID.randomUUID()}.js")
            try {
                fileResource.writeText("""{"message":"word word"}""")
                scriptResource.writeText("${'$'}done({body:${'$'}response.body.replace('paired-jq','from-file-script')});")
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
                    extension.getJSONArray("map_local").put(JSONObject().put("pattern", "/file$")
                        .put("data_type", "file").put("data", fileResource.name).put("status", 999))
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
                            .put("script_path", scriptResource.name).put("debug", true)))
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
                fileResource.delete()
                scriptResource.delete()
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

            // A small base must not bypass the limit on the recovered backup.
            base.writeText("{}")
            backup.writeText(" ".repeat(4 * 1024 * 1024 + 1))
            val error = assertThrows(IllegalArgumentException::class.java) {
                CaptureSettings(directory, retiredKeyAlias = null).snapshot()
            }
            assertTrue(error.message.orEmpty().contains("exceed"))
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
        ServerSocket(0, 1, InetAddress.getByName("127.0.0.1")).use { occupied ->
            SubscriptionServer(occupiedPortConfig(occupied.localPort)).use { server ->
                withRunningProfile(server.url) { activity, profiles, id ->
                    val before = profiles.get(id).typed.lastUpdated.time
                    Libbox.checkConfig(server.content)
                    val failure = runCatching { profiles.update(id) }.exceptionOrNull()
                    assertNotNull("A failed subscription must report its reload failure", failure)
                    assertEquals(1, server.requests.get())
                    assertEquals("{}", profiles.read(id))
                    assertEquals(before, profiles.get(id).typed.lastUpdated.time)
                    withTimeout(15_000) { activity.serviceStatus.first { it == Status.Started } }
                    assertEquals(0, failure!!.suppressed.size)
                }
            }
        }
    }

    @Test fun oversizedSubscriptionsPreserveDataAndService() {
        SubscriptionServer("{}".padEnd(16 * 1024 * 1024 + 1)).use { server ->
            withRunningProfile(server.url) { activity, profiles, id ->
                val before = profiles.get(id).typed.lastUpdated.time
                val running = BoxService.dashboardServer
                val failure = runCatching { profiles.update(id) }.exceptionOrNull()
                assertTrue(failure?.message.orEmpty().contains("Subscription exceeds 16 MiB"))
                assertEquals("{}", profiles.read(id))
                assertEquals(before, profiles.get(id).typed.lastUpdated.time)

                val profile = profiles.get(id)
                profile.typed.autoUpdate = true
                profile.typed.lastUpdated = Date(0)
                ProfileManager.update(profile)
                assertFalse(UpdateProfileWork.updateProfiles(activity.applicationContext))
                assertEquals("{}", profiles.read(id))
                assertEquals(0L, profiles.get(id).typed.lastUpdated.time)

                val ids = ProfileManager.list().map { it.id }
                val importFailure = runCatching {
                    profiles.save(mapOf("name" to "Oversized subscription", "url" to server.url))
                }.exceptionOrNull()
                assertTrue(importFailure?.message.orEmpty().contains("Subscription exceeds 16 MiB"))
                assertEquals(ids, ProfileManager.list().map { it.id })
                assertEquals(id, Settings.selectedProfile)
                assertEquals(Status.Started, activity.serviceStatus.value)
                assertSame(running, BoxService.dashboardServer)
                assertEquals(3, server.requests.get())
            }
        }
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

    @Test fun failedAutomaticUpdateRestoresDataAndRequestsRetry() {
        ServerSocket(0, 1, InetAddress.getByName("127.0.0.1")).use { occupied ->
            SubscriptionServer(occupiedPortConfig(occupied.localPort)).use { server ->
                withRunningProfile(server.url) { activity, profiles, id ->
                    Libbox.checkConfig(server.content)
                    val profile = profiles.get(id)
                    profile.typed.autoUpdate = true
                    profile.typed.lastUpdated = Date(0)
                    ProfileManager.update(profile)
                    assertFalse(UpdateProfileWork.updateProfiles(activity.applicationContext))
                    assertEquals(1, server.requests.get())
                    assertEquals("{}", profiles.read(id))
                    assertEquals(0L, profiles.get(id).typed.lastUpdated.time)
                    assertEquals(id, Settings.selectedProfile)
                    withTimeout(15_000) { activity.serviceStatus.first { it == Status.Started } }
                }
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
        SubscriptionServer("""{"log":{"level":"debug"}}""", release).use { server ->
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

    @Test fun pendingAutomaticDownloadDoesNotBlockDisablingTheSubscription() {
        val release = CountDownLatch(1)
        SubscriptionServer("""{"log":{"level":"debug"}}""", release).use { server ->
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
        SubscriptionServer("{}", release).use { server ->
            withRunningProfile { _, profiles, id -> coroutineScope {
                val imported = async { profiles.save(mapOf("name" to "Pending import", "url" to server.url)) }
                try {
                    withTimeout(10_000) { while (server.requests.get() == 0) delay(10) }
                    withTimeout(10_000) { profiles.save(mapOf("id" to id, "name" to "Edited during import")) }
                    assertFalse(imported.isCompleted)
                } finally { release.countDown() }
                val importedId = imported.await()
                try {
                    assertEquals("Edited during import", profiles.get(id).name)
                    assertEquals(id, Settings.selectedProfile)
                    assertEquals("{}", profiles.read(importedId))
                } finally { profiles.delete(importedId) }
            } }
        }
    }

    @Test fun subscriptionCommitRechecksUrlDeletionAndSessionIdentity() = withRunningProfile { _, profiles, _ ->
        val id = profiles.save(mapOf("name" to "Unselected download", "url" to "https://example.test/old", "content" to "{}"))
        val snapshot = profiles.prepareUpdate(id)
        profiles.save(mapOf("id" to id, "name" to "Changed URL", "url" to "https://example.test/new"))
        assertTrue(runCatching { profiles.commitUpdate(snapshot, "{}") }.isFailure)
        assertEquals("https://example.test/new", profiles.get(id).typed.remoteURL)
        profiles.delete(id)
        assertTrue(runCatching { profiles.commitUpdate(snapshot, "{}") }.isFailure)
        val session = io.nekohasekai.sfa.utils.CommandTarget.localSession()
        io.nekohasekai.sfa.utils.CommandTarget.setRemoteServer(null)
        assertTrue(runCatching { io.nekohasekai.sfa.utils.CommandTarget.requireLocalSession(session) }.isFailure)
    }

    @Test fun stoppedAndUnselectedUpdatesPreserveServiceState() {
        SubscriptionServer("""{"log":{"disabled":true}}""").use { server ->
            withRunningProfile(server.url) { activity, profiles, id ->
                val other = profiles.save(mapOf("name" to "Unselected subscription", "content" to "{}", "url" to server.url))
                try {
                    profiles.update(other)
                    assertEquals(server.content, profiles.read(other))
                    assertEquals(id, Settings.selectedProfile)
                    assertEquals(Status.Started, activity.serviceStatus.value)

                    server.content = """{"log":{"level":"warn"}}"""
                    val unselected = profiles.get(other)
                    unselected.typed.autoUpdate = true
                    unselected.typed.lastUpdated = Date(0)
                    ProfileManager.update(unselected)
                    assertTrue(UpdateProfileWork.updateProfiles(activity.applicationContext))
                    assertEquals(server.content, profiles.read(other))
                    assertEquals(id, Settings.selectedProfile)
                    assertEquals(Status.Started, activity.serviceStatus.value)
                    val updatedUnselected = profiles.get(other)
                    updatedUnselected.typed.autoUpdate = false
                    ProfileManager.update(updatedUnselected)

                    withContext(Dispatchers.Main) { activity.stopService() }
                    withTimeout(15_000) { activity.serviceStatus.first { it == Status.Stopped } }
                    profiles.update(id)
                    assertEquals(server.content, profiles.read(id))
                    assertEquals(Status.Stopped, activity.serviceStatus.value)

                    server.content = "{}"
                    val profile = profiles.get(id)
                    profile.typed.autoUpdate = true
                    profile.typed.lastUpdated = Date(0)
                    ProfileManager.update(profile)
                    assertTrue(UpdateProfileWork.updateProfiles(activity.applicationContext))
                    assertEquals("{}", profiles.read(id))
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
