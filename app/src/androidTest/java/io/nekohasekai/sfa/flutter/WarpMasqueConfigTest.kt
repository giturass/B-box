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
import java.security.KeyPairGenerator
import java.security.spec.ECGenParameterSpec

@RunWith(AndroidJUnit4::class)
class WarpMasqueConfigTest {
    private fun account(): JSONObject {
        val generator = KeyPairGenerator.getInstance("EC").apply { initialize(ECGenParameterSpec("secp256r1")) }
        val client = generator.generateKeyPair()
        val peer = generator.generateKeyPair()
        return JSONObject().put("type", "masque").put("tag", "warp")
            .put("server", "127.0.0.1").put("server_port", 443)
            .put("private_key", Base64.encodeToString(client.private.encoded, Base64.NO_WRAP))
            .put("peer_public_key", Base64.encodeToString(peer.public.encoded, Base64.NO_WRAP))
            .put("address", JSONArray().put("172.16.0.2/32").put("2001:db8::2/128"))
    }

    private fun configuration(account: JSONObject): String = JSONObject()
        .put("outbounds", JSONArray().put(account))
        .put("route", JSONObject().put("final", "warp")).toString()

    @Test fun http2AndHttp3UseThePatchedAndroidCore() {
        val account = account()
        for (version in listOf(2, 3)) {
            account.put("version", version)
            val content = configuration(account)
            Libbox.checkConfig(content)
            val formatted = Libbox.formatConfig(content).value
            Libbox.checkConfig(formatted)
            val outbound = JSONObject(formatted).getJSONArray("outbounds").getJSONObject(0)
            assertEquals("masque", outbound.getString("type"))
            assertEquals(version, outbound.getInt("version"))
            assertEquals(account.getString("private_key"), outbound.getString("private_key"))
            assertEquals(account.getString("peer_public_key"), outbound.getString("peer_public_key"))
            assertEquals(account.getJSONArray("address").toString(), outbound.getJSONArray("address").toString())
        }
    }

    @Test fun malformedAccountDataFailsConfigurationValidation() {
        for ((field, value) in listOf(
            "private_key" to "invalid",
            "peer_public_key" to "invalid",
            "address" to JSONArray().put("172.16.0.2/24"),
            "address" to JSONArray().put("172.16.0.2/32").put("172.16.0.3/32"),
            "version" to 1,
            "tls" to JSONObject().put("insecure", true),
        )) {
            val account = account().put("version", 2).put(field, value)
            assertTrue("Invalid $field was accepted by libbox", runCatching { Libbox.checkConfig(configuration(account)) }.isFailure)
        }
    }
}
