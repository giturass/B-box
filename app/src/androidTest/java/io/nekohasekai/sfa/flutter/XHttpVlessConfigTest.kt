package io.nekohasekai.sfa.flutter

import android.util.Base64
import androidx.test.ext.junit.runners.AndroidJUnit4
import io.nekohasekai.libbox.Libbox
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.security.SecureRandom

internal object VlessEncryptionTestKeys {
    // The reference implementation accepts raw 32-byte X25519 keys. These
    // disposable values only exercise configuration validation, without dialing.
    fun rawX25519(): String = Base64.encodeToString(
        ByteArray(32).also { SecureRandom().nextBytes(it) },
        Base64.URL_SAFE or Base64.NO_PADDING or Base64.NO_WRAP,
    )
}

@RunWith(AndroidJUnit4::class)
class XHttpVlessConfigTest {
    private val uuid = "11111111-1111-4111-8111-111111111111"

    private fun transport(mode: String = "stream-up"): JSONObject = JSONObject()
        .put("type", "xhttp").put("mode", mode)
        .put("host", JSONArray().put("upload.example.test").put("alternate.example.test"))
        .put("path", "/xhttp?fixture=1")
        .put("headers", JSONObject().put("X-Fixture", "android"))
        .put("x_padding_bytes", "100-200").put("sc_max_each_post_bytes", "1024-2048")
        .put("no_grpc_header", true)
        .put("xmux", JSONObject().put("max_connections", 2))

    private fun outbound(transport: JSONObject = transport()): JSONObject = JSONObject()
        .put("type", "vless").put("tag", "fixture-out")
        .put("server", "127.0.0.1").put("server_port", 443).put("uuid", uuid)
        .put("transport", transport)

    private fun configuration(outbound: JSONObject, inbound: JSONObject? = null): String = JSONObject()
        .put("outbounds", JSONArray().put(outbound))
        .put("route", JSONObject().put("final", "fixture-out"))
        .apply { if (inbound != null) put("inbounds", JSONArray().put(inbound)) }.toString()

    private fun checked(content: String): JSONObject {
        Libbox.checkConfig(content)
        val formatted = Libbox.formatConfig(content).value
        Libbox.checkConfig(formatted)
        return JSONObject(formatted)
    }

    private fun textValues(value: Any): List<String> = if (value is JSONArray) {
        (0 until value.length()).map { value.getString(it) }
    } else {
        listOf(value as String)
    }

    @Test fun xhttpModesAndHTTPVersionsUseThePatchedAndroidCore() {
        for (mode in listOf("auto", "packet-up", "stream-up", "stream-one")) {
            for (alpn in listOf("http/1.1", "h2", "h3")) {
                val outbound = outbound(transport(mode)).put("tls", JSONObject()
                    .put("enabled", true).put("server_name", "upload.example.test")
                    .put("alpn", JSONArray().put(alpn)))
                val saved = checked(configuration(outbound)).getJSONArray("outbounds").getJSONObject(0)
                val transport = saved.getJSONObject("transport")
                assertEquals("xhttp", transport.getString("type"))
                assertEquals(mode, transport.getString("mode"))
                assertEquals(textValues(outbound.getJSONObject("transport").get("host")), textValues(transport.get("host")))
                assertEquals("/xhttp?fixture=1", transport.getString("path"))
                assertEquals("100-200", transport.getString("x_padding_bytes"))
                assertEquals("1024-2048", transport.getString("sc_max_each_post_bytes"))
                assertTrue(transport.getBoolean("no_grpc_header"))
                assertEquals(2, transport.getJSONObject("xmux").getInt("max_connections"))
                assertEquals(listOf(alpn), textValues(saved.getJSONObject("tls").get("alpn")))
            }
        }
    }

    @Test fun encryptionAndDecryptionSurviveAndroidBindingFormatting() {
        // Syntax and Vision transport combinations follow the fixed reference's
        // test/vless_encryption_test.go; no server credentials are stored here.
        for (mode in listOf("native", "xorpub", "random")) {
            for (rtt in listOf("1rtt", "0rtt")) {
                for (type in listOf("ws", "xhttp")) {
                    val transport = if (type == "ws") JSONObject().put("type", "ws").put("path", "/vless") else transport()
                    val padding = "100-111-1111.75-0-111.50-0-3333"
                    val encryption = "mlkem768x25519plus.$mode.$rtt.$padding.${VlessEncryptionTestKeys.rawX25519()}"
                    val decryption = "mlkem768x25519plus.$mode.300-600s.$padding.${VlessEncryptionTestKeys.rawX25519()}"
                    val outbound = outbound(transport).put("flow", "xtls-rprx-vision").put("encryption", encryption)
                    val inbound = JSONObject().put("type", "vless").put("tag", "fixture-in")
                        .put("listen", "127.0.0.1").put("listen_port", 0)
                        .put("users", JSONArray().put(JSONObject().put("uuid", uuid).put("flow", "xtls-rprx-vision")))
                        .put("decryption", decryption).put("transport", JSONObject(transport.toString()))
                    val saved = checked(configuration(outbound, inbound))
                    assertEquals(encryption, saved.getJSONArray("outbounds").getJSONObject(0).getString("encryption"))
                    assertEquals(decryption, saved.getJSONArray("inbounds").getJSONObject(0).getString("decryption"))
                    assertEquals(type, saved.getJSONArray("outbounds").getJSONObject(0).getJSONObject("transport").getString("type"))
                    assertEquals("xtls-rprx-vision", saved.getJSONArray("outbounds").getJSONObject(0).getString("flow"))
                }
            }
        }
    }

    @Test fun splitDownloadSettingsSurviveAndroidBindingFormatting() {
        val download = JSONObject().put("server", "127.0.0.2").put("server_port", 8443)
            .put("host", JSONArray().put("download.example.test"))
            .put("path", "/download").put("mode", "stream-up")
            .put("tls", JSONObject().put("enabled", true).put("server_name", "download.example.test"))
        val saved = checked(configuration(outbound(transport().put("download_settings", download))))
            .getJSONArray("outbounds").getJSONObject(0).getJSONObject("transport").getJSONObject("download_settings")
        assertEquals("127.0.0.2", saved.getString("server"))
        assertEquals(8443, saved.getInt("server_port"))
        assertEquals("/download", saved.getString("path"))
        assertEquals(listOf("download.example.test"), textValues(saved.get("host")))
        assertEquals("download.example.test", saved.getJSONObject("tls").getString("server_name"))
    }

    @Test fun invalidTransportAndEncryptionParametersFailAndroidValidation() {
        for (transport in listOf(
            transport("invalid-mode"),
            transport().put("xmux", JSONObject().put("max_connections", 2).put("max_concurrency", 3)),
            transport("stream-one").put("download_settings", JSONObject().put("path", "/download")),
        )) {
            assertTrue("Invalid XHTTP configuration was accepted", runCatching { Libbox.checkConfig(configuration(outbound(transport))) }.isFailure)
        }
        val key = VlessEncryptionTestKeys.rawX25519()
        for (encryption in listOf(
            "invalid", "mlkem768x25519plus.native.0rtt.invalid-key",
            "mlkem768x25519plus.native.unknown.$key",
            "mlkem768x25519plus.invalid-mode.0rtt.$key",
            "unsupported.native.0rtt.$key",
        )) {
            val failure = runCatching { Libbox.checkConfig(configuration(outbound().put("encryption", encryption))) }.exceptionOrNull()
            assertTrue("Invalid VLESS encryption was accepted", failure != null)
            assertTrue("VLESS encryption error exposed the key", !failure!!.message.orEmpty().contains(key))
        }
        for (decryption in listOf(
            "mlkem768x25519plus.native.invalid-ticket.$key",
            "mlkem768x25519plus.invalid-mode.600s.$key",
            "unsupported.native.600s.$key",
        )) {
            val inbound = JSONObject().put("type", "vless").put("listen", "127.0.0.1").put("listen_port", 0)
                .put("users", JSONArray().put(JSONObject().put("uuid", uuid))).put("decryption", decryption)
            val failure = runCatching { Libbox.checkConfig(configuration(outbound(), inbound)) }.exceptionOrNull()
            assertTrue("Invalid VLESS decryption was accepted", failure != null)
            assertTrue("VLESS decryption error exposed the key", !failure!!.message.orEmpty().contains(key))
        }
    }
}
