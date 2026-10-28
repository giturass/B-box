package io.nekohasekai.sfa.flutter

import android.os.Build
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.Application
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
import java.io.File
import java.net.InetAddress
import java.net.ServerSocket

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
