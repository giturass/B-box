package io.nekohasekai.sfa.flutter

import android.util.Base64
import androidx.test.ext.junit.runners.AndroidJUnit4
import io.nekohasekai.libbox.Libbox
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class WarpWireGuardConfigTest {
    private fun configuration(account: JSONObject, section: String = "endpoints"): String = JSONObject()
        .put(section, JSONArray().put(account))
        .put("route", JSONObject().put("final", "WARP")).toString()

    @Test fun registeredWireGuardEndpointsRoundTripThroughTheAndroidCore() {
        val account = JSONObject(warpWireGuardRegistrationTestAccount())
        val content = configuration(account)
        Libbox.checkConfig(content)
        val formatted = Libbox.formatConfig(content).value
        Libbox.checkConfig(formatted)
        val root = JSONObject(formatted)
        val endpoint = root.getJSONArray("endpoints").getJSONObject(0)
        assertEquals("wireguard", endpoint.getString("type"))
        assertEquals("WARP", endpoint.getString("tag"))
        assertEquals(1280, endpoint.getInt("mtu"))
        assertEquals(account.getString("private_key"), endpoint.getString("private_key"))
        assertEquals(32, Base64.decode(endpoint.getString("private_key"), Base64.DEFAULT).size)
        assertEquals(account.getJSONArray("address").toString(), endpoint.getJSONArray("address").toString())
        assertEquals(1, endpoint.getJSONArray("peers").length())
        val peer = endpoint.getJSONArray("peers").getJSONObject(0)
        assertEquals("engage.cloudflareclient.com", peer.getString("address"))
        assertEquals(2408, peer.getInt("port"))
        assertEquals(account.getJSONArray("peers").getJSONObject(0).getString("public_key"), peer.getString("public_key"))
        assertEquals(32, Base64.decode(peer.getString("public_key"), Base64.DEFAULT).size)
        val reserved = peer.get("reserved")
        val reservedBytes = when (reserved) {
            is JSONArray -> (0 until reserved.length()).map { reserved.getInt(it) }
            is String -> Base64.decode(reserved, Base64.DEFAULT).map { it.toInt() and 255 }
            else -> error("Unexpected WireGuard reserved representation")
        }
        assertEquals(listOf(1, 2, 3), reservedBytes)
        val allowedIps = peer.getJSONArray("allowed_ips")
        assertEquals(listOf("0.0.0.0/0", "::/0"), (0 until allowedIps.length()).map { allowedIps.getString(it) })
        assertFalse(endpoint.optBoolean("system"))
        assertFalse(endpoint.has("server"))
        assertFalse(endpoint.has("tls"))
        assertFalse(endpoint.has("http_version"))
        val outbounds = root.optJSONArray("outbounds") ?: JSONArray()
        assertFalse((0 until outbounds.length()).any { outbounds.getJSONObject(it).optString("type") == "wireguard" })
    }

    @Test fun invalidWireGuardCredentialsAndLegacyOutboundShapeFailValidation() {
        val shortKey = Base64.encodeToString(ByteArray(31) { 1 }, Base64.NO_WRAP)
        for ((field, value) in listOf(
            "private_key" to "",
            "private_key" to "invalid",
            "address" to JSONArray().put("172.16.0.2/invalid"),
        )) {
            val account = JSONObject(warpWireGuardRegistrationTestAccount()).put(field, value)
            assertTrue("Invalid WireGuard $field was accepted by libbox", runCatching { Libbox.checkConfig(configuration(account)) }.isFailure)
        }
        for ((field, value) in listOf(
            "public_key" to "invalid",
            "public_key" to shortKey,
            "allowed_ips" to JSONArray(),
            "reserved" to JSONArray().put(1).put(2),
            "reserved" to JSONArray().put(1).put(2).put(256),
        )) {
            val account = JSONObject(warpWireGuardRegistrationTestAccount())
            account.getJSONArray("peers").getJSONObject(0).put(field, value)
            assertTrue("Invalid WireGuard peer $field was accepted by libbox", runCatching { Libbox.checkConfig(configuration(account)) }.isFailure)
        }
        assertTrue("Legacy WireGuard outbound was accepted by libbox", runCatching {
            Libbox.checkConfig(configuration(JSONObject(warpWireGuardRegistrationTestAccount()), "outbounds"))
        }.isFailure)
    }
}
