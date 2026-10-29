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
import io.nekohasekai.sfa.database.ProfileManager
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.vendor.PackageQueryManager
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
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
        } finally {
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

    @Test fun failedSelectionRestoresThePreviousSelectionAndService() = withRunningProfile { activity, profiles, id ->
        val previousMode = Settings.serviceMode
        ServerSocket(0, 1, InetAddress.getByName("127.0.0.1")).use { occupied ->
            val content = occupiedPortConfig(occupied.localPort)
            val candidate = profiles.save(mapOf("name" to "Broken selection", "content" to content))
            try {
                val failure = runCatching { profiles.select(candidate) }.exceptionOrNull()
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
                    profiles.select(other)
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
