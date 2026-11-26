package io.nekohasekai.sfa.flutter

import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

class BoxPanelCacheTest {
    @get:Rule val directory = TemporaryFolder()
    private val list = """[{"moduleId":"first","name":"Rates"},{"moduleId":"second","name":"Rates"}]"""

    @Test fun onlySuccessfulFieldsSurviveReopening() {
        val write = BoxPanelCache(directory.root)
        val result = """{"title":"Rates","content":"8.00\n21.00","icon-color":"#EF8F1C","debug":"private"}"""
        write.save(1, "a", "first", "Rates", result)
        for (invalid in listOf("{}", "null", "[]", "{\"title\":27}")) {
            assertTrue(runCatching { write.save(1, "a", "first", "Rates", invalid) }.isFailure)
        }
        val read = BoxPanelCache(directory.root)
        val matching = JSONArray(read.withCached(1, "a", list))
        val cached = matching.getJSONObject(0).getJSONObject("cached")
        assertEquals("8.00\n21.00", cached.getString("content"))
        assertEquals("#EF8F1C", cached.getString("icon-color"))
        assertTrue(cached.getLong("updatedAt") > 0)
        assertFalse(cached.has("debug"))
        assertFalse(matching.getJSONObject(1).has("cached"))
        assertFalse(JSONArray(read.withCached(2, "a", list)).getJSONObject(0).has("cached"))
        assertFalse(JSONArray(read.withCached(1, "b", list)).getJSONObject(0).has("cached"))
    }

    @Test fun olderEntriesAreEvictedWithoutLosingLatestResult() {
        val cache = BoxPanelCache(directory.root)
        val result = JSONObject().put("title", "New").put("content", "字".repeat(200000)).toString()
        for (id in listOf("first", "second")) cache.save(1, "a", id, "Rates", result)
        val entries = JSONArray(cache.withCached(1, "a", list))
        assertFalse(entries.getJSONObject(0).has("cached"))
        assertTrue(entries.getJSONObject(1).has("cached"))
        assertTrue(directory.root.listFiles()!!.single().length() <= 1024 * 1024)
    }
}
