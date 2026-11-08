package io.nekohasekai.sfa.database.preference

import androidx.room.Room
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import io.nekohasekai.sfa.constant.SettingsKey
import io.nekohasekai.sfa.flutter.BoxSettings
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.async
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import java.io.IOException
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

@RunWith(AndroidJUnit4::class)
class PreferencePersistenceTest {
    private fun database() = Room.inMemoryDatabaseBuilder(
        InstrumentationRegistry.getInstrumentation().targetContext, KeyValueDatabase::class.java,
    ).build()

    @Test fun ordinarySettingsPersistTogetherAndFailedWritesRestoreTheCache() = runBlocking(Dispatchers.IO) {
        val database = database()
        try {
            val dao = database.keyValuePairDao()
            val store = RoomPreferenceDataStore { dao }
            val values = BoxSettings.entities(mapOf("allowBypass" to true, "queryMode" to "ROOT", "perAppList" to listOf("one.app")))
            store.putBatchConfirmed(values)
            val reopened = RoomPreferenceDataStore { dao }
            reopened.initialize()
            assertEquals(true, reopened.getBoolean(SettingsKey.ALLOW_BYPASS))
            assertEquals("ROOT", reopened.getString(SettingsKey.PER_APP_PROXY_PACKAGE_QUERY_MODE))
            assertEquals(setOf("one.app"), reopened.getStringSet(SettingsKey.PER_APP_PROXY_LIST))
            database.close()
            store.putBoolean(SettingsKey.ALLOW_BYPASS, false)
            store.remove(SettingsKey.PER_APP_PROXY_PACKAGE_QUERY_MODE)
            assertTrue(runCatching { store.flush() }.isFailure)
            assertEquals(true, store.getBoolean(SettingsKey.ALLOW_BYPASS))
            assertEquals("ROOT", store.getString(SettingsKey.PER_APP_PROXY_PACKAGE_QUERY_MODE))
            assertTrue(runCatching { store.putBatchConfirmed(BoxSettings.entities(mapOf("queryMode" to "SHIZUKU"))) }.isFailure)
            assertEquals("ROOT", store.getString(SettingsKey.PER_APP_PROXY_PACKAGE_QUERY_MODE))
            store.reset()
            assertTrue(runCatching { store.flush() }.isFailure)
            assertEquals(true, store.getBoolean(SettingsKey.ALLOW_BYPASS))
            store.flush() // Errors are reported once; the writer is still usable.
        } finally { database.close() }
    }

    @Test fun failedOlderWriteCannotReplaceANewerPendingValue() = runBlocking(Dispatchers.IO) {
        val database = database()
        val entered = CountDownLatch(1)
        val release = CountDownLatch(1)
        try {
            val dao = database.keyValuePairDao()
            val wrapped = object : KeyValueEntity.Dao by dao {
                override fun put(value: KeyValueEntity): Long {
                    if (value.string == "failed") {
                        entered.countDown()
                        check(release.await(10, TimeUnit.SECONDS))
                        throw IOException("Disk write failed")
                    }
                    return dao.put(value)
                }
            }
            val store = RoomPreferenceDataStore { wrapped }
            store.putBatchConfirmed(listOf(KeyValueEntity("name").put("old")))
            val observed = CopyOnWriteArrayList<String?>()
            store.registerChangeListener(object : OnPreferenceDataStoreChangeListener {
                override fun onPreferenceDataStoreChanged(data: androidx.preference.PreferenceDataStore, key: String) {
                    observed.add(store.getString("name"))
                }
            })
            store.putString("name", "failed")
            assertTrue(entered.await(10, TimeUnit.SECONDS))
            store.putString("name", "newest")
            assertEquals("newest", store.getString("name"))
            release.countDown()
            assertTrue(runCatching { store.flush() }.isFailure)
            assertEquals("newest", store.getString("name"))
            assertEquals("newest", dao["name"]?.string)
            assertFalse(observed.contains("old"))
        } finally { release.countDown(); database.close() }
    }

    @Test fun confirmedBatchDoesNotHideNewerPendingWrites() = runBlocking(Dispatchers.IO) {
        val database = database()
        val entered = CountDownLatch(1)
        val release = CountDownLatch(1)
        try {
            val dao = database.keyValuePairDao()
            val wrapped = object : KeyValueEntity.Dao by dao {
                override fun putAll(values: List<KeyValueEntity>) {
                    if (values.any { it.string == "confirmed" }) {
                        entered.countDown()
                        check(release.await(10, TimeUnit.SECONDS))
                    }
                    dao.putAll(values)
                }
            }
            val store = RoomPreferenceDataStore { wrapped }
            store.putBatchConfirmed(listOf(KeyValueEntity("name").put("old")))
            val saving = async { store.putBatchConfirmed(listOf(KeyValueEntity("name").put("confirmed"), KeyValueEntity("enabled").put(true))) }
            assertTrue(entered.await(10, TimeUnit.SECONDS))
            assertEquals("old", store.getString("name"))
            assertNull(store.getBoolean("enabled"))
            store.putString("name", "newest")
            release.countDown()
            saving.await()
            store.flush()
            assertEquals("newest", store.getString("name"))
            assertEquals(true, store.getBoolean("enabled"))
            assertEquals("newest", dao["name"]?.string)
        } finally { release.countDown(); database.close() }
    }

    @Test fun cancelledSaveCannotCommitAfterItsRollback() = runBlocking(Dispatchers.IO) {
        val database = database()
        val entered = CountDownLatch(1)
        val release = CountDownLatch(1)
        try {
            val dao = database.keyValuePairDao()
            val store = RoomPreferenceDataStore {
                object : KeyValueEntity.Dao by dao {
                    override fun putAll(values: List<KeyValueEntity>) {
                        if (values.single().string == "pending") {
                            entered.countDown()
                            check(release.await(10, TimeUnit.SECONDS))
                        }
                        dao.putAll(values)
                    }
                }
            }
            store.putBatchConfirmed(listOf(KeyValueEntity("name").put("old")))
            val saving = launch {
                try {
                    store.putBatchConfirmed(listOf(KeyValueEntity("name").put("pending")))
                    currentCoroutineContext().ensureActive()
                } finally {
                    withContext(NonCancellable) { store.putBatchConfirmed(listOf(KeyValueEntity("name").put("old"))) }
                }
            }
            assertTrue(entered.await(10, TimeUnit.SECONDS))
            saving.cancel()
            assertFalse(saving.isCompleted)
            release.countDown()
            saving.join()
            store.flush()
            assertEquals("old", store.getString("name"))
            assertEquals("old", dao["name"]?.string)
        } finally { release.countDown(); database.close() }
    }

    @Test fun stringSetsUseUtf8ByteLengths() = runBlocking(Dispatchers.IO) {
        val database = database()
        try {
            val dao = database.keyValuePairDao()
            val store = RoomPreferenceDataStore { dao }
            val names = mutableSetOf("中文", "café", "🚀", "plain", "")
            store.putStringSet("names", names)
            store.flush()
            assertEquals(names, RoomPreferenceDataStore { dao }.getStringSet("names"))
        } finally { database.close() }
    }
}
