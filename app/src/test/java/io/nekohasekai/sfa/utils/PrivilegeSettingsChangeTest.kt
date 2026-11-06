package io.nekohasekai.sfa.utils

import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class PrivilegeSettingsChangeTest {
    private val old = PrivilegeSettingsConfig(false, setOf("retained.app"), false, "wlan")
    private val next = old.copy(enabled = true, renameInterface = true)

    @Test fun restartFailureRestoresDiskAndModuleBeforeRestartingAgain() = runTest {
        var disk = old
        var module = old
        val starts = mutableListOf<Boolean>()
        val error = runCatching {
            PrivilegeSettingsChange.apply(old, next, { disk = it }, { module = it }) { recover ->
                starts += recover
                assertEquals(if (recover) old else next, disk)
                assertEquals(disk, module)
                if (!recover) error("VPN start failed")
            }
        }.exceptionOrNull()!!
        assertEquals("VPN start failed", error.message)
        assertEquals(listOf(false, true), starts)
        assertEquals(old, disk)
        assertEquals(old, module)
    }

    @Test fun unacknowledgedSyncRollsBackAndNeverRestarts() = runTest {
        var disk = old
        val syncs = mutableListOf<PrivilegeSettingsConfig>()
        val error = runCatching {
            PrivilegeSettingsChange.apply(old, next, { disk = it }, {
                syncs += it
                if (it == next) error("binder died after applying")
            }) { fail("Must not start with unconfirmed module settings") }
        }.exceptionOrNull()!!
        assertEquals("binder died after applying", error.message)
        assertEquals(listOf(next, old), syncs)
        assertEquals(old, disk)
    }

    @Test fun storageFailureDoesNotReachTheModule() = runTest {
        var disk = old
        val error = runCatching {
            PrivilegeSettingsChange.apply(old, next, {
                if (it == next) error("disk full")
                disk = it
            }, { fail("Module must not change before persistence") }) { fail("Unexpected restart") }
        }.exceptionOrNull()!!
        assertEquals("disk full", error.message)
        assertEquals(old, disk)
    }

    @Test fun failedRecoveryPreservesBothErrorsAndPreventsAnotherStart() = runTest {
        for (failDisk in listOf(false, true)) {
            val starts = mutableListOf<Boolean>()
            val error = runCatching {
                PrivilegeSettingsChange.apply(old, next, {
                    if (failDisk && it == old) error("restore disk failed")
                }, {
                    if (!failDisk && it == old) error("restore module failed")
                }) { recover ->
                    starts += recover
                    error("VPN failed")
                }
            }.exceptionOrNull()!!
            assertEquals("VPN failed", error.message)
            assertEquals(listOf(false), starts)
            assertEquals(listOf(if (failDisk) "restore disk failed" else "restore module failed"), error.suppressed.map { it.message })
        }
    }

    @Test fun cancellationFinishesRecovery() = runTest {
        var disk = old
        var module = old
        var recovered = false
        val job = launch {
            PrivilegeSettingsChange.apply(old, next, { disk = it }, { module = it }) { recover ->
                if (recover) {
                    currentCoroutineContext().ensureActive()
                    assertEquals(old, disk)
                    assertEquals(old, module)
                    recovered = true
                } else awaitCancellation()
            }
        }
        runCurrent()
        job.cancel()
        runCurrent()
        job.join()
        assertTrue(recovered)
    }

    @Test fun revisionsIgnorePackageOrderButRejectChangesAndUnsafePrefixes() {
        val a = old.copy(packages = linkedSetOf("first.app", "second.app"))
        assertEquals(a.revision(), a.copy(packages = linkedSetOf("second.app", "first.app")).revision())
        assertNotEquals(a.revision(), a.copy(renameInterface = true).revision())
        assertEquals(a, PrivilegeSettingsConfig.fromWire(a.toWire()))
        for (prefix in listOf("", "123", "tun\npackages=other.app", "网卡", "a".repeat(14), "eth-")) {
            assertTrue(prefix, runCatching { old.copy(interfacePrefix = prefix).validate() }.isFailure)
        }
        for (packages in listOf(setOf("bad\napp"), setOf("a,b"), setOf("a."), setOf("a".repeat(256)))) {
            assertTrue(runCatching { old.copy(packages = packages).validate() }.isFailure)
        }
        assertTrue(runCatching { PrivilegeSettingsConfig.fromWire(old.toWire() + ("unexpected" to true)) }.isFailure)
    }
}
