package io.nekohasekai.sfa.flutter

import android.os.Build
import androidx.lifecycle.ViewModelProvider
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.Application
import io.nekohasekai.sfa.backend.log.LogLevel
import io.nekohasekai.sfa.backend.log.LogViewModel
import io.nekohasekai.sfa.bg.UpdateProfileWork
import io.nekohasekai.sfa.compose.MainActivity
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.database.ProfileContent
import io.nekohasekai.sfa.database.ProfileManager
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.vendor.PackageQueryManager
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import java.io.Closeable
import java.io.File
import java.net.InetAddress
import java.net.ServerSocket
import java.util.Date
import java.util.concurrent.atomic.AtomicInteger
import kotlin.concurrent.thread

@RunWith(AndroidJUnit4::class)
class ProfileRecoveryTest {
    @Test fun captureRulesFollowProfileRevisionAndRecoverFailedSaves() = runBlocking(Dispatchers.IO) {
        Application.application.setupLibbox()
        Settings.dataStore.initialize()
        val oldSelection = Settings.selectedProfile
        var failApply = false
        val profiles = BoxProfiles { recover -> if (failApply && !recover) error("Capture profile apply failed") }
        val original = """{"log":{"level":"warn"},"outbounds":[{"type":"direct","tag":"original-direct"}],"route":{"final":"original-direct"}}"""
        val first = profiles.save(mapOf("name" to "HTTP rewrite fixture", "content" to original))
        val second = profiles.save(mapOf("name" to "Other capture fixture", "content" to "{}"))
        try {
            Settings.selectedProfile = first
            val legacy = profiles.captureSettings("""{"https":true,"decryptHosts":["legacy.test"]}""")
            assertEquals(first, legacy["profileId"])
            assertTrue((legacy["config"] as Map<*, *>)["https"] == true)
            assertEquals(original, profiles.read(first)) // Reading legacy settings never rewrites the profile.
            val rules = """{"https":true,"decryptBypassHosts":["pinned.test"],"extractHosts":true,"rewriteEnabled":true,"scripts":[{"pattern":"^https://example.test/","source":"${'$'}done({headers: ${'$'}request.headers});"}]}"""
            profiles.setCaptureSettings(first, legacy["revision"] as String, rules)
            val saved = profiles.read(first)
            Libbox.checkConfig(saved)
            assertTrue(saved.contains("original-direct"))
            assertTrue(saved.contains("http_debug"))
            assertTrue(Libbox.readUICaptureConfig(saved).contains("pinned.test"))
            assertEquals("{}", profiles.read(second))
            assertTrue(runCatching { profiles.setCaptureSettings(first, legacy["revision"] as String, "{}") }.isFailure)
            assertEquals(saved, profiles.read(first))
            Settings.selectedProfile = second
            assertTrue(runCatching { profiles.setCaptureSettings(first, BoxProfiles.revision(saved), "{}") }.isFailure)
            val other = profiles.captureSettings("{}")
            assertFalse((other["config"] as Map<*, *>)["rewriteEnabled"] == true)
            Settings.selectedProfile = first
            val timestamp = profiles.get(first).typed.lastUpdated.time
            failApply = true
            assertTrue(runCatching { profiles.setCaptureSettings(first, BoxProfiles.revision(saved), "{}") }.isFailure)
            assertEquals(saved, profiles.read(first))
            assertEquals(timestamp, profiles.get(first).typed.lastUpdated.time)
            failApply = false
            assertTrue(runCatching { profiles.setCaptureSettings(first, BoxProfiles.revision(saved), """{"scripts":[{"pattern":".","source":"const = ;"}]}""") }.isFailure)
            assertEquals(saved, profiles.read(first))
        } finally {
            failApply = false
            Settings.selectedProfile = oldSelection
            profiles.delete(first)
            profiles.delete(second)
            Settings.dataStore.flush()
        }
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
            profiles.delete(id)
            Settings.selectedProfile = oldSelection
            Settings.dataStore.flush()
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
            profiles.delete(id)
            Settings.selectedProfile = oldSelection
            Settings.dataStore.flush()
        }
    }

    @Test fun queryModeIsPersisted() = runBlocking(Dispatchers.IO) {
        Settings.dataStore.initialize()
        val before = Settings.perAppProxyPackageQueryMode
        try {
            PackageQueryManager.setQueryMode(Settings.PACKAGE_QUERY_MODE_ROOT)
            Settings.dataStore.flush()
            assertEquals(Settings.PACKAGE_QUERY_MODE_ROOT, Settings.perAppProxyPackageQueryMode)
            assertEquals(Settings.PACKAGE_QUERY_MODE_ROOT, PackageQueryManager.queryMode.value)
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
                val failure = runCatching { profiles.select(candidate) { activity.serviceStatus.value } }.exceptionOrNull()
                assertNotNull("Selecting a configuration must report its reload failure", failure)
                assertEquals(id, Settings.selectedProfile)
                assertEquals(previousMode, Settings.serviceMode)
                assertEquals(content, profiles.read(candidate))
                withTimeout(15_000) { activity.serviceStatus.first { it == Status.Started } }
                assertEquals(0, failure!!.suppressed.size)
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
                val profiles = BoxProfiles { recover -> withContext(Dispatchers.Main) { activity.applyProfile(recover = recover) } }
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

    private class SubscriptionServer(@Volatile var content: String) : Closeable {
        private val server = ServerSocket(0, 1, InetAddress.getByName("127.0.0.1"))
        val url = "http://127.0.0.1:${server.localPort}/profile.json"
        val requests = AtomicInteger()
        private val worker = thread(isDaemon = true, name = "subscription-fixture") {
            while (!server.isClosed) {
                try {
                    server.accept().use { socket ->
                        socket.soTimeout = 15_000
                        val reader = socket.getInputStream().bufferedReader()
                        while (!reader.readLine().isNullOrEmpty()) { }
                        val bytes = content.toByteArray(Charsets.UTF_8)
                        requests.incrementAndGet()
                        val output = socket.getOutputStream()
                        output.write("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: ${bytes.size}\r\nConnection: close\r\n\r\n".toByteArray())
                        output.write(bytes)
                        output.flush()
                    }
                } catch (failure: Exception) {
                    if (!server.isClosed) throw failure
                }
            }
        }
        override fun close() {
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
}
