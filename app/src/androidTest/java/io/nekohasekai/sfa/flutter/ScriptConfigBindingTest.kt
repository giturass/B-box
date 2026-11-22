package io.nekohasekai.sfa.flutter

import androidx.test.ext.junit.runners.AndroidJUnit4
import io.nekohasekai.libbox.Libbox
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class ScriptConfigBindingTest {
    @Test fun eventAndGenericScriptsRoundTripWithoutLoadingOrExecutingTheirSources() {
        val content = """{"experimental":{"scripts":{
            "enabled":true,
            "event":[
                {"name":"same","event":"network-changed","source":"throw Error('must not execute');","storage_namespace":"native-events"},
                {"event":"engine-started","script_path":"unread.js","disabled":true},
                {"event":"profile-reloaded","source":"${'$'}done();"}
            ],
            "generic":[
                {"name":"same","disabled":true,"source":"${'$'}done();"},
                {"name":"same","source":"${'$'}done({ok:true});","timeout_ms":-1,"debug":true,"argument":"kept","http_client":{"allowed_origins":[]}}
            ]
        }}}"""
        Libbox.checkConfig(content)
        val formatted = Libbox.formatConfig(content).value
        Libbox.checkConfig(formatted)
        val scripts = JSONObject(formatted).getJSONObject("experimental").getJSONObject("scripts")
        val events = scripts.getJSONArray("event")
        assertEquals(listOf("network-changed", "engine-started", "profile-reloaded"), (0 until events.length()).map { events.getJSONObject(it).getString("event") })
        assertEquals("unread.js", events.getJSONObject(1).getString("script_path"))
        assertTrue(events.getJSONObject(1).getBoolean("disabled"))
        val generic = scripts.getJSONArray("generic")
        assertEquals(2, generic.length())
        assertTrue(generic.getJSONObject(0).getBoolean("disabled"))
        assertEquals(generic.getJSONObject(0).getString("name"), generic.getJSONObject(1).getString("name"))
        assertEquals(-1, generic.getJSONObject(1).getInt("timeout_ms"))
        assertEquals("kept", generic.getJSONObject(1).getString("argument"))
        assertEquals(0, generic.getJSONObject(1).getJSONObject("http_client").getJSONArray("allowed_origins").length())
        assertFalse(Libbox.needsUIMITMCA(formatted))
        val schema = Libbox.generateConfigSchema().value
        for (field in listOf("event", "generic", "disabled")) assertTrue(schema.contains("\"$field\""))
    }

    @Test fun invalidEventsAndDisabledSourceCombinationsFailTheActualAndroidBindings() {
        fun reject(section: String, task: JSONObject) {
            val content = JSONObject().put("experimental", JSONObject().put("scripts", JSONObject()
                .put(section, JSONArray().put(task)).put("resources", JSONObject().put("code", JSONObject().put("data", "${'$'}done();"))))).toString()
            assertTrue("checkConfig accepted $content", runCatching { Libbox.checkConfig(content) }.isFailure)
            assertTrue("formatConfig accepted $content", runCatching { Libbox.formatConfig(content) }.isFailure)
        }
        for (event in listOf("network-change", "engine-start", "config-reloaded", "manually")) {
            reject("event", JSONObject().put("event", event).put("disabled", true).put("source", "${'$'}done();"))
        }
        reject("event", JSONObject().put("source", "${'$'}done();"))
        reject("event", JSONObject().put("event", "network-changed").put("source", "const =").put("disabled", true))
        reject("generic", JSONObject().put("source", "const =").put("disabled", true))
        reject("generic", JSONObject().put("disabled", true))
        reject("generic", JSONObject().put("source", "${'$'}done();").put("resource", "code"))
        reject("generic", JSONObject().put("source", "${'$'}done();").put("event", "network-changed"))
        reject("generic", JSONObject().put("script_path", "../outside.js"))
    }
}
