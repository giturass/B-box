package io.nekohasekai.sfa.bg

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File
import java.io.PrintWriter
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference
import kotlin.concurrent.thread

/** Exercises the actual UI writer without Android stubs or loading libbox. */
class UiJvmCrashReportsTest {
    @get:Rule val temporary = TemporaryFolder()

    @Test fun uiReportsKeepTheirProcessIdentityAndNeverConsumeTheMainPendingFilesOrConfiguration() {
        val files = mainFiles()
        val store = UiJvmCrashReports(files.base)
        val previous = File(files.reports, "2026-11-21T00-00-00").apply { mkdirs() }
        File(previous, "go.log").writeText("main Go failure")
        store.write(Thread("Flutter platform worker"), IllegalArgumentException("UI failure 中文"),
            "io.ericlee.bfa:ui", "749", "1.0.1")
        files.assertMainUntouched()
        store.archiveTo(files.reports)
        files.assertMainUntouched()

        assertEquals("main Go failure", File(previous, "go.log").readText())
        val report = files.archives().single { it != previous }
        assertTrue(File(report, "jvm.log").readText().contains("UI failure 中文"))
        assertFalse(File(report, "configuration.json").exists())
        assertFalse(File(report, "go.log").exists())
        val metadata = Json.parseToJsonElement(File(report, "metadata.json").readText()).jsonObject
        assertEquals("Application", metadata["source"]!!.jsonPrimitive.content)
        assertEquals("io.ericlee.bfa:ui", metadata["processName"]!!.jsonPrimitive.content)
        assertEquals("Flutter platform worker", metadata["threadName"]!!.jsonPrimitive.content)
        assertEquals("java.lang.IllegalArgumentException", metadata["exceptionName"]!!.jsonPrimitive.content)
        assertEquals("749", metadata["appVersion"]!!.jsonPrimitive.content)
        assertEquals("1.0.1", metadata["appMarketingVersion"]!!.jsonPrimitive.content)
        assertFalse(metadata.containsKey("coreVersion"))
        assertFalse(metadata.containsKey("goVersion"))
        assertTrue(files.pendingEntries().isEmpty())
    }

    @Test fun simultaneousUiFailuresPublishSeparateReportsWithoutOverwritingTheMainFailure() {
        val files = mainFiles()
        val store = UiJvmCrashReports(files.base)
        val start = CountDownLatch(1)
        val failure = AtomicReference<Throwable?>()
        val writers = (0 until 2).map { worker ->
            thread(name = "UI-crash-writer-$worker", isDaemon = true) {
                try {
                    check(start.await(5, TimeUnit.SECONDS))
                    repeat(3) { index ->
                        store.write(Thread.currentThread(), IllegalStateException("UI $worker/$index"),
                            "io.ericlee.bfa:ui", "749", "1.0.1")
                    }
                } catch (error: Throwable) { failure.set(error) }
            }
        }
        start.countDown()
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
        while (writers.any { it.isAlive } && System.nanoTime() < deadline) {
            store.archiveTo(files.reports)
            Thread.yield()
        }
        writers.forEach { it.join(5_000) }
        assertFalse("UI crash writers did not finish", writers.any { it.isAlive })
        failure.get()?.let { throw AssertionError("UI crash writer failed", it) }
        store.archiveTo(files.reports)
        assertEquals(6, files.archives().size)
        val reasons = files.archives().map {
            Json.parseToJsonElement(File(it, "metadata.json").readText())
                .jsonObject["exceptionReason"]!!.jsonPrimitive.content
        }.toSet()
        assertEquals((0 until 2).flatMap { worker -> (0 until 3).map { "UI $worker/$it" } }.toSet(), reasons)
        assertTrue(files.pendingEntries().isEmpty())
        files.assertMainUntouched()
    }

    @Test fun anArchiveFailureRetainsTheUiReportForRetryAndLeavesTheMainConfigurationAlone() {
        val files = mainFiles()
        val store = UiJvmCrashReports(files.base)
        store.write(Thread.currentThread(), IllegalStateException("recover this UI report"),
            "io.ericlee.bfa:ui", "749", "1.0.1")
        val blocked = File(files.working, "blocked-archive").apply { writeText("occupied") }
        assertThrows(IllegalStateException::class.java) { store.archiveTo(blocked) }
        assertEquals(1, files.pendingEntries().size)
        assertTrue(File(files.pendingEntries().single(), "jvm.log").readText().contains("recover this UI report"))
        files.assertMainUntouched()

        assertTrue(blocked.delete())
        store.archiveTo(blocked)
        assertEquals(1, blocked.listFiles { file -> file.isDirectory }!!.size)
        assertTrue(files.pendingEntries().isEmpty())
        files.assertMainUntouched()
    }

    @Test fun aRefreshCannotArchiveALogUntilItsUiMetadataHasBeenPublished() {
        val files = mainFiles()
        val store = UiJvmCrashReports(files.base)
        val metadataReached = CountDownLatch(1)
        val release = CountDownLatch(1)
        val failure = AtomicReference<Throwable?>()
        val paused = object : RuntimeException() {
            override fun printStackTrace(writer: PrintWriter) { writer.println("staged UI stack") }
            override val message: String get() {
                metadataReached.countDown()
                check(release.await(5, TimeUnit.SECONDS))
                return "complete UI metadata"
            }
        }
        val writer = thread(name = "UI-staged-crash", isDaemon = true) {
            try { store.write(Thread.currentThread(), paused, "io.ericlee.bfa:ui", "749", "1.0.1") }
            catch (error: Throwable) { failure.set(error) }
        }
        try {
            assertTrue(metadataReached.await(5, TimeUnit.SECONDS))
            val staged = files.pendingEntries().single()
            assertTrue(staged.name.startsWith('.'))
            assertEquals("staged UI stack", File(staged, "jvm.log").readText().trimEnd())
            assertFalse(File(staged, "metadata.json").exists())
            store.archiveTo(files.reports)
            assertTrue(files.archives().isEmpty())
            assertEquals(1, files.pendingEntries().size)
            files.assertMainUntouched()
        } finally {
            release.countDown()
            writer.join(5_000)
        }
        assertFalse("Staged UI writer did not finish", writer.isAlive)
        failure.get()?.let { throw AssertionError("Staged UI writer failed", it) }
        store.archiveTo(files.reports)
        assertEquals(1, files.archives().size)
        assertTrue(files.pendingEntries().isEmpty())
        files.assertMainUntouched()
    }

    @Test fun retryingAfterCopyingBeforePendingCleanupDoesNotDuplicateTheArchivedUiReport() {
        val files = mainFiles()
        val store = UiJvmCrashReports(files.base)
        store.write(Thread.currentThread(), IllegalStateException("already archived"),
            "io.ericlee.bfa:ui", "749", "1.0.1")
        val entry = files.pendingEntries().single()
        val snapshot = File(temporary.newFolder("recovery"), "entry")
        assertTrue(entry.copyRecursively(snapshot))
        store.archiveTo(files.reports)
        // Recreate the source left by a process dying after the archive copy.
        assertTrue(snapshot.copyRecursively(entry))
        store.archiveTo(files.reports)
        assertEquals(1, files.archives().size)
        assertTrue(files.pendingEntries().isEmpty())
        files.assertMainUntouched()
    }

    @Test fun metadataFailureKeepsTheCompleteUiStackWithoutAttachingTheMainConfiguration() {
        val files = mainFiles()
        val store = UiJvmCrashReports(files.base)
        val failure = object : RuntimeException() {
            override fun printStackTrace(writer: PrintWriter) { writer.println("UI stack survives metadata failure") }
            override val message: String get() = error("Metadata unavailable")
        }
        store.write(Thread.currentThread(), failure, "io.ericlee.bfa:ui", "749", "1.0.1")
        store.archiveTo(files.reports)
        val report = files.archives().single()
        assertEquals("UI stack survives metadata failure", File(report, "jvm.log").readText().trimEnd())
        assertFalse(File(report, "metadata.json").exists())
        assertFalse(File(report, "configuration.json").exists())
        assertTrue(files.pendingEntries().isEmpty())
        files.assertMainUntouched()
    }

    @Test fun aFailedPublicationRenameRetainsAReadyUiReportForTheBackendToArchive() {
        val files = mainFiles()
        val store = UiJvmCrashReports(files.base)
        var blocked: File? = null
        val failure = object : RuntimeException() {
            override fun printStackTrace(writer: PrintWriter) { writer.println("UI publication retry") }
            override val message: String get() {
                val staged = files.pendingEntries().single()
                blocked = File(staged.parentFile, staged.name.removePrefix(".")).apply {
                    assertTrue(mkdir())
                    File(this, "occupied").writeText("do not overwrite")
                }
                return "publication rename will fail"
            }
        }
        store.write(Thread.currentThread(), failure, "io.ericlee.bfa:ui", "749", "1.0.1")
        val staged = files.pendingEntries().single { it.name.startsWith('.') }
        assertTrue(File(staged, ".ready").isFile)
        assertTrue(File(staged, "jvm.log").isFile)
        assertTrue(File(staged, "metadata.json").isFile)
        store.archiveTo(files.reports)
        val report = files.archives().single()
        assertEquals("UI publication retry", File(report, "jvm.log").readText().trimEnd())
        assertEquals("publication rename will fail", Json.parseToJsonElement(File(report, "metadata.json").readText())
            .jsonObject["exceptionReason"]!!.jsonPrimitive.content)
        assertFalse(staged.exists())
        assertEquals("do not overwrite", File(checkNotNull(blocked), "occupied").readText())
        files.assertMainUntouched()
    }

    private fun mainFiles(): MainFiles {
        val files = MainFiles(temporary.newFolder("base"), temporary.newFolder("working"))
        File(files.base, "configuration.json").writeText(MAIN_CONFIG)
        File(files.working, "CrashReport-JVM.log").writeText(MAIN_LOG)
        File(files.working, "CrashReport-JVM-metadata.json").writeText(MAIN_METADATA)
        return files
    }

    private class MainFiles(val base: File, val working: File) {
        val reports = File(working, "crash_reports")
        fun pendingEntries() = File(base, UiJvmCrashReports.PENDING_DIRECTORY_NAME).listFiles().orEmpty().toList()
        fun archives() = reports.listFiles { file -> file.isDirectory }.orEmpty().toList()
        fun assertMainUntouched() {
            assertEquals(MAIN_CONFIG, File(base, "configuration.json").readText())
            assertEquals(MAIN_LOG, File(working, "CrashReport-JVM.log").readText())
            assertEquals(MAIN_METADATA, File(working, "CrashReport-JVM-metadata.json").readText())
        }
    }

    companion object {
        private const val MAIN_CONFIG = "{\"outbounds\":[{\"type\":\"direct\",\"tag\":\"main\"}]}"
        private const val MAIN_LOG = "main JVM failure awaiting restart"
        private const val MAIN_METADATA = "{\"processName\":\"io.ericlee.bfa\",\"source\":\"Application\"}"
    }
}
