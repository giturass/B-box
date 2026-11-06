package io.nekohasekai.sfa.flutter

import android.os.Parcel
import androidx.room.Room
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import io.nekohasekai.sfa.bg.LogEntry
import io.nekohasekai.sfa.bg.PackageEntry
import io.nekohasekai.sfa.bg.ParceledListSlice
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.database.preference.KeyValueDatabase
import io.nekohasekai.sfa.database.preference.KeyValueEntity
import io.nekohasekai.sfa.database.preference.RoomPreferenceDataStore
import io.nekohasekai.sfa.utils.PrivilegeSettingsClient
import io.nekohasekai.sfa.utils.VpnDetectionTest
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runBlocking
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class PrivilegeBridgeTest {
    @Test fun absentModuleDoesNotChangeSettingsOrRequestRoot() = runBlocking(Dispatchers.IO) {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        Settings.dataStore.initialize()
        val before = PrivilegeSettingsClient.read()
        PrivilegeSettingsClient.register(context)
        val state = PrivilegeSettingsClient.refresh()
        assertEquals(false, state["active"])
        assertEquals(false, state["synced"])
        assertEquals(before.toWire(), state["settings"])
        val error = runCatching {
            PrivilegeSettingsClient.update(before.revision(), before.copy(enabled = !before.enabled), {}) { _, _, _ ->
                fail("An inactive module must not restart the VPN")
            }
        }.exceptionOrNull()
        assertNotNull(error)
        assertEquals(before, PrivilegeSettingsClient.read())
        // The ordinary Android API probe also works without loading framework-only API classes.
        assertNotNull(VpnDetectionTest.runDetection(context))
    }

    @Test fun confirmedBatchSurvivesReopenAndFailedCommitLeavesCachedValuesIntact() = runBlocking(Dispatchers.IO) {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val database = Room.inMemoryDatabaseBuilder(context, KeyValueDatabase::class.java).build()
        try {
            val dao = database.keyValuePairDao()
            val store = RoomPreferenceDataStore { dao }
            store.initialize()
            store.putBatchConfirmed(listOf(KeyValueEntity("hide").put(true), KeyValueEntity("apps").put(setOf("one.app", "two.app"))))
            val reopened = RoomPreferenceDataStore { dao }
            reopened.initialize()
            assertEquals(true, reopened.getBoolean("hide"))
            assertEquals(setOf("one.app", "two.app"), reopened.getStringSet("apps"))
            database.close()
            assertTrue(runCatching { store.putBatchConfirmed(listOf(KeyValueEntity("hide").put(false))) }.isFailure)
            assertEquals(true, store.getBoolean("hide"))
        } finally { database.close() }
    }

    @Test fun modulePayloadsRoundTripThroughTheExistingSliceProtocol() {
        val parcel = Parcel.obtain()
        try {
            ParceledListSlice(listOf(PackageEntry("io.ericlee.bfa"), PackageEntry("example.app"))).writeToParcel(parcel, 0)
            parcel.setDataPosition(0)
            val restored = ParceledListSlice.CREATOR.createFromParcel(parcel, PackageEntry::class.java.classLoader)
            assertEquals(listOf("io.ericlee.bfa", "example.app"), restored.list.map { (it as PackageEntry).packageName })
            parcel.setDataSize(0)
            parcel.setDataPosition(0)
            ParceledListSlice(listOf(LogEntry(LogEntry.LEVEL_WARN, 12L, "hook", "message", "trace"))).writeToParcel(parcel, 0)
            parcel.setDataPosition(0)
            val logs = ParceledListSlice.CREATOR.createFromParcel(parcel, LogEntry::class.java.classLoader).list
            assertEquals("trace", (logs.single() as LogEntry).stackTrace)
        } finally { parcel.recycle() }
    }
}
