package io.nekohasekai.sfa.flutter

import android.util.Base64
import androidx.test.ext.junit.runners.AndroidJUnit4
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.Application
import io.nekohasekai.sfa.bg.BoxService
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.database.ProfileContent
import io.nekohasekai.sfa.database.ProfileManager
import io.nekohasekai.sfa.database.RemoteServer
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.utils.CommandTarget
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.runBlocking
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File
import java.security.KeyPairGenerator
import java.security.spec.ECGenParameterSpec

@RunWith(AndroidJUnit4::class)
class WarpRegistrationTest {
    private fun outbounds(content: String): List<JSONObject> {
        val rows = JSONObject(Libbox.formatConfig(content).value).getJSONArray("outbounds")
        return (0 until rows.length()).map { rows.getJSONObject(it) }
    }

    @Test fun registrationWritesTheSelectedFileAndSubscriptionUpdatesPreserveItsCredentials() = withProfiles { profiles, id, other ->
        val original = """{"dns":{"servers":[{"type":"local","tag":"local"}]},"outbounds":[{"type":"direct","tag":"WARP"}],"route":{"final":"WARP"}}"""
        profiles.writeContent(id, original, ProfileContent.revision(profiles.read(id)))
        val persistence = BoxWarpProfiles { assertEquals(Status.Stopped, BoxService.currentStatus) }
        val node = warpRegistrationTestAccount()
        var registrations = 0
        val warp = BoxWarp(persistence::prepare, persistence::commit,
            registerNode = { _, _, _, registered -> registrations++; registered(node) })
        try {
            val result = warp.register("selected-file", id, ProfileContent.revision(profiles.read(id)), "WARP", "http3")
            assertEquals("WARP 2", result["tag"])
            assertEquals(id, result["profileId"])
            assertEquals(1, (result["added"] as Number).toInt())
            assertEquals(1, registrations)
            assertEquals("{}", profiles.read(other))
            val saved = profiles.read(id)
            Libbox.checkConfig(saved)
            val current = JSONObject(Libbox.formatConfig(saved).value)
            val previous = JSONObject(Libbox.formatConfig(original).value)
            assertEquals(previous.getJSONObject("dns").toString(), current.getJSONObject("dns").toString())
            assertEquals(previous.getJSONObject("route").toString(), current.getJSONObject("route").toString())
            val credentials = JSONObject(node).getString("private_key")
            assertEquals(credentials, outbounds(saved).single { it.getString("type") == "masque" }.getString("private_key"))
            var subscription = """[{"type":"socks","tag":"upstream","server":"127.0.0.1","server_port":1080}]"""
            val sources = BoxNodeSubscriptions(download = { subscription }) { assertEquals(Status.Stopped, BoxService.currentStatus) }
            val imported = sources.importSubscription(mapOf("profileId" to id, "profileRevision" to ProfileContent.revision(saved),
                "name" to "WARP preservation fixture", "url" to "https://fixture.invalid/nodes", "autoUpdate" to false, "interval" to 60))
            val sourceId = (imported["source"] as Map<*, *>)["id"] as String
            subscription = """[{"type":"socks","tag":"upstream","server":"127.0.0.1","server_port":1081}]"""
            sources.update(id, sourceId)
            val updated = profiles.read(id)
            Libbox.checkConfig(updated)
            val retained = outbounds(updated).single { it.getString("type") == "masque" }
            assertEquals("WARP 2", retained.getString("tag"))
            assertEquals(credentials, retained.getString("private_key"))
            assertEquals("{}", profiles.read(other))
            assertEquals(Status.Stopped, BoxService.currentStatus)
        } finally { warp.close() }
    }

    @Test fun changedRevisionSelectionSessionAndServiceTransitionPreventCredentialWrites() = withProfiles { profiles, id, other -> coroutineScope {
        var hostStatus = Status.Stopped
        val persistence = BoxWarpProfiles(beforeApply = { ProfileContent.requireStableService(hostStatus) }) {}
        val entered = CompletableDeferred<Unit>()
        val response = CompletableDeferred<String>()
        var registrations = 0
        val warp = BoxWarp(persistence::prepare, persistence::commit, registerNode = { _, _, _, registered ->
            registrations++; entered.complete(Unit); registered(response.await())
        })
        try {
            hostStatus = Status.Starting
            assertTrue(runCatching { warp.register("transition", id, ProfileContent.revision(profiles.read(id)), "WARP", "http3") }.isFailure)
            assertEquals(0, registrations)
            hostStatus = Status.Stopped
            CommandTarget.setRemoteServer(RemoteServer())
            assertTrue(runCatching { warp.register("remote", id, ProfileContent.revision(profiles.read(id)), "WARP", "http3") }.isFailure)
            assertEquals(0, registrations)
            CommandTarget.setRemoteServer(null)
            val operation = async { runCatching { warp.register("late", id, ProfileContent.revision(profiles.read(id)), "WARP", "http3") } }
            entered.await()
            profiles.writeContent(id, """{"log":{"level":"warn"}}""", ProfileContent.revision(profiles.read(id)))
            Settings.saveProfileSelection(other)
            CommandTarget.setRemoteServer(RemoteServer())
            CommandTarget.setRemoteServer(null)
            response.complete(warpRegistrationTestAccount())
            val error = operation.await().exceptionOrNull() as BoxWarpException
            assertEquals("warp_pending", error.code)
            assertFalse(outboundsOrEmpty(profiles.read(id)).any { it.optString("type") == "masque" })
            assertEquals("{}", profiles.read(other))
            val pendingId = error.details!!["pendingId"] as String
            assertEquals("warp_pending", (runCatching { warp.retry(pendingId, id, ProfileContent.revision(profiles.read(id))) }.exceptionOrNull() as BoxWarpException).code)
            Settings.saveProfileSelection(id)
            warp.retry(pendingId, id, ProfileContent.revision(profiles.read(id)))
            assertEquals(1, registrations)
            assertEquals(1, outbounds(profiles.read(id)).count { it.optString("type") == "masque" })
            assertEquals("warn", JSONObject(Libbox.formatConfig(profiles.read(id)).value).getJSONObject("log").getString("level"))
            assertEquals("{}", profiles.read(other))
        } finally { warp.close(); CommandTarget.setRemoteServer(null) }
    } }

    private fun outboundsOrEmpty(content: String): List<JSONObject> = if (JSONObject(Libbox.formatConfig(content).value).has("outbounds")) outbounds(content) else emptyList()

    private fun withProfiles(block: suspend (BoxProfiles, Long, Long) -> Unit) = runBlocking(Dispatchers.IO) {
        Application.application.setupLibbox()
        Settings.dataStore.initialize()
        assertEquals(Status.Stopped, BoxService.currentStatus)
        val oldSelection = Settings.selectedProfile
        val oldRemote = CommandTarget.remoteServer
        CommandTarget.setRemoteServer(null)
        val profiles = BoxProfiles {}
        val id = profiles.save(mapOf("name" to "WARP registration fixture", "content" to "{}"))
        val other = profiles.save(mapOf("name" to "WARP separate fixture", "content" to "{}"))
        try {
            Settings.saveProfileSelection(id)
            block(profiles, id, other)
        } finally {
            Settings.saveProfileSelection(oldSelection)
            for (profileId in listOf(id, other)) ProfileManager.get(profileId)?.let { ProfileManager.delete(it); File(it.typed.path).delete() }
            CommandTarget.setRemoteServer(oldRemote)
        }
    }
}

internal fun warpRegistrationTestAccount(): String {
    val generator = KeyPairGenerator.getInstance("EC").apply { initialize(ECGenParameterSpec("secp256r1")) }
    return JSONObject().put("type", "masque").put("tag", "WARP").put("server", "127.0.0.1").put("server_port", 443)
        .put("private_key", Base64.encodeToString(generator.generateKeyPair().private.encoded, Base64.NO_WRAP))
        .put("peer_public_key", Base64.encodeToString(generator.generateKeyPair().public.encoded, Base64.NO_WRAP))
        .put("address", JSONArray().put("172.16.0.2/32").put("2001:db8::2/128"))
        .put("http_version", 3).put("disable_version_fallback", true)
        .put("tls", JSONObject().put("enabled", true).put("server_name", "consumer-api.cloudflareclient.com")).toString()
}
