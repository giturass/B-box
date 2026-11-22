package io.nekohasekai.sfa.flutter

import androidx.test.ext.junit.runners.AndroidJUnit4
import io.nekohasekai.libbox.Libbox
import kotlinx.serialization.json.Json
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class HTTPCompoundMatchConfigTest {
    @Test fun declarativeConditionsSurviveTheAndroidCoreFormatter() {
        val content = """{
            "experimental":{
                "http_rewrite":{
                    "enabled":true,
                    "header_rewrite":[{
                        "match":{"all":[
                            {"subject":"request.method","regex":"(?i)^post$"},
                            {"subject":"request.header","header":"X-Optional","equals":""}
                        ]},
                        "phase":"request","operation":"add","header":"X-Policy","value":"matched"
                    }],
                    "url_rewrite":[{
                        "pattern":"^https://api\\.test/(.*)$",
                        "match":{"subject":"request.header","header":"Host","equals":"api.test"},
                        "action":"header","replacement":"https://mapped.test/${'$'}1"
                    },{
                        "match":{"subject":"url","equals":"https://api.test/redirect"},
                        "action":"302","replacement":"https://mapped.test/redirect"
                    }],
                    "map_remote":[{
                        "match":{"subject":"url","regex":"(?i)^https://api\\.test/remote$"},
                        "replacement":"https://mapped.test/remote"
                    }],
                    "map_local":[{
                        "match":{"all":[
                            {"subject":"url","equals":"https://api.test/local"},
                            {"any":[
                                {"subject":"request.method","equals":"GET"},
                                {"subject":"request.header","header":"X-Mode","regex":"(?i)^preview$"}
                            ]}
                        ]},
                        "data":"local"
                    }],
                    "body_rewrite":[{
                        "pattern":"/response$","phase":"response","regex":"old","replacement":"new",
                        "match":{"all":[
                            {"subject":"response.status","equals":200},
                            {"subject":"response.header","header":"Content-Type","regex":"(?i)^application/json"},
                            {"subject":"request.method","equals":"GET"}
                        ]}
                    }]
                },
                "scripts":{"enabled":true,"http":[{
                    "phase":"response","source":"${'$'}done();",
                    "match":{"any":[
                        {"subject":"response.status","regex":"^20[0-9]$"},
                        {"subject":"request.header","header":"X-Token","equals":"中文 token"}
                    ]}
                }]}
            }
        }"""
        Libbox.checkConfig(content)
        val formatted = Libbox.formatConfig(content).value
        Libbox.checkConfig(formatted)
        val expected = JSONObject(content).getJSONObject("experimental")
        val saved = JSONObject(formatted).getJSONObject("experimental")
        for (section in listOf("header_rewrite", "url_rewrite", "map_remote", "map_local", "body_rewrite")) {
            val originals = expected.getJSONObject("http_rewrite").getJSONArray(section)
            val actuals = saved.getJSONObject("http_rewrite").getJSONArray(section)
            assertEquals(originals.length(), actuals.length())
            for (index in 0 until originals.length()) {
                val original = originals.getJSONObject(index)
                val actual = actuals.getJSONObject(index)
                assertEquals("Formatting lost $section[$index] conditions", Json.parseToJsonElement(original.getJSONObject("match").toString()),
                    Json.parseToJsonElement(actual.getJSONObject("match").toString()))
                if (original.has("pattern")) assertEquals(original.getString("pattern"), actual.getString("pattern"))
                else assertFalse("Formatting introduced a pattern in $section[$index]", actual.has("pattern"))
            }
        }
        assertEquals(Json.parseToJsonElement(expected.getJSONObject("scripts").getJSONArray("http").getJSONObject(0).getJSONObject("match").toString()),
            Json.parseToJsonElement(saved.getJSONObject("scripts").getJSONArray("http").getJSONObject(0).getJSONObject("match").toString()))
        assertFalse(saved.getJSONObject("scripts").getJSONArray("http").getJSONObject(0).has("pattern"))
    }

    @Test fun malformedConditionsFailAndroidValidationAndFormatting() {
        for (condition in listOf(
            "null", "{}", "{\"all\":[]}", "{\"any\":[]}",
            """{"all":[{"subject":"url","regex":"."}],"any":[{"subject":"url","regex":"."}]}""",
            """{"subject":"url","equals":"https://api.test/","regex":"."}""",
            """{"subject":"url","regex":"["}""",
            """{"subject":"request.header","equals":"token"}""",
            """{"subject":"request.header","header":"Bad Header","equals":"token"}""",
            """{"subject":"request.method","header":"X-Token","equals":"GET"}""",
            """{"subject":"request.method","equals":123}""",
            """{"subject":"response.status","equals":"200"}""",
            """{"subject":"response.status","equals":200.5}""",
            """{"subject":"response.body","regex":"."}""",
            """{"not":{"subject":"request.method","equals":"GET"}}""",
        )) {
            val rule = JSONObject().put("phase", "response").put("regex", "old").put("replacement", "new")
                .put("match", org.json.JSONTokener(condition).nextValue())
            reject(configuration("body_rewrite", rule), "Malformed compound match $condition")
        }
        reject(configuration("map_local", JSONObject().put("data", "unscoped")), "A rule without pattern or match")
        reject(configuration("map_local", JSONObject().put("pattern", ".").put("match", JSONObject.NULL).put("data", "local")),
            "An explicit null match with a valid legacy pattern")
    }

    @Test fun responseConditionsAreRejectedInEveryRequestPhaseIncludingDisabledRules() {
        for (disabled in listOf(false, true)) {
            for (subject in listOf("response.status", "response.header")) {
                val leaf = JSONObject().put("subject", subject)
                if (subject == "response.status") leaf.put("equals", 200) else leaf.put("header", "X-Origin").put("equals", "yes")
                val condition = JSONObject().put("any", JSONArray().put(JSONObject().put("all", JSONArray().put(leaf))))
                for ((section, template) in listOf(
                    "header_rewrite" to """{"phase":"request","operation":"add","header":"X-Test","value":"yes"}""",
                    "url_rewrite" to """{"action":"reject"}""",
                    "map_remote" to """{"replacement":"https://mapped.test/"}""",
                    "body_rewrite" to """{"phase":"request","regex":"old","replacement":"new"}""",
                    "map_local" to """{"data":"local"}""",
                    "http" to """{"phase":"request","source":"${'$'}done();"}""",
                )) {
                    val rule = JSONObject(template).put("pattern", ".").put("match", condition).put("disabled", disabled)
                    reject(configuration(section, rule), "$section admitted $subject in a request rule (disabled=$disabled)")
                }
            }
        }
    }

    private fun configuration(section: String, rule: JSONObject): String {
        val extension = if (section == "http") "scripts" else "http_rewrite"
        return JSONObject().put("experimental", JSONObject().put(extension,
            JSONObject().put("enabled", true).put(section, JSONArray().put(rule)))).toString()
    }

    private fun reject(content: String, description: String) {
        assertTrue("$description passed checkConfig", runCatching { Libbox.checkConfig(content) }.isFailure)
        assertTrue("$description passed formatConfig", runCatching { Libbox.formatConfig(content) }.isFailure)
    }
}
