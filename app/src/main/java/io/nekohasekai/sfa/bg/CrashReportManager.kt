package io.nekohasekai.sfa.bg

import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.Application
import io.nekohasekai.sfa.BuildConfig
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.json.JSONObject
import java.io.File
import java.io.FileOutputStream
import java.io.PrintWriter
import java.io.StringWriter
import java.text.ParseException
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.TimeZone
import java.util.UUID

private const val METADATA_FILE_NAME = "metadata.json"
private const val JVM_LOG_FILE_NAME = "jvm.log"
private const val CRASH_REPORTS_DIR_NAME = "crash_reports"

data class CrashReport(
    val id: String,
    val date: Date,
    val directory: File,
    val isRead: Boolean,
)

data class CrashReportFile(
    val kind: Kind,
    val displayName: String,
    val file: File,
) {
    enum class Kind {
        METADATA,
        GO_LOG,
        JVM_LOG,
        CONFIG,
    }
}

object CrashReportManager {
    private const val GO_LOG_FILE_NAME = "go.log"
    private const val CONFIG_FILE_NAME = "configuration.json"
    private const val READ_MARKER_FILE_NAME = ".read"
    private const val PENDING_JVM_CRASH_FILE_NAME = "CrashReport-JVM.log"
    private const val PENDING_JVM_METADATA_FILE_NAME = "CrashReport-JVM-metadata.json"

    private val timestampFormat = SimpleDateFormat("yyyy-MM-dd'T'HH-mm-ss", Locale.US).apply {
        timeZone = TimeZone.getTimeZone("UTC")
    }

    private lateinit var workingDir: File
    private lateinit var baseDir: File
    private val archiveAccess = Any()

    private val _reports = MutableStateFlow<List<CrashReport>>(emptyList())
    val reports: StateFlow<List<CrashReport>> = _reports
    private val _unreadCount = MutableStateFlow(0)
    val unreadCount: StateFlow<Int> = _unreadCount

    fun install(workingDir: File, baseDir: File) {
        this.workingDir = workingDir
        this.baseDir = baseDir
        synchronized(archiveAccess) {
            archivePendingJvmCrashReport()
            archivePendingUiJvmCrashReports()
        }
        val previous = Thread.getDefaultUncaughtExceptionHandler()
        Thread.setDefaultUncaughtExceptionHandler { thread, throwable ->
            writePendingJvmCrashReport(thread, throwable)
            previous?.uncaughtException(thread, throwable)
        }
    }

    /** The Flutter process records only JVM failures and never initializes libbox. */
    fun installUi(baseDir: File, processName: String) {
        val pending = UiJvmCrashReports(baseDir)
        val previous = Thread.getDefaultUncaughtExceptionHandler()
        Thread.setDefaultUncaughtExceptionHandler { thread, throwable ->
            runCatching {
                pending.write(thread, throwable, processName,
                    BuildConfig.VERSION_CODE.toString(), BuildConfig.VERSION_NAME)
            }
            previous?.uncaughtException(thread, throwable)
        }
    }

    private fun writePendingJvmCrashReport(thread: Thread, throwable: Throwable) {
        try {
            val writer = StringWriter()
            throwable.printStackTrace(PrintWriter(writer))
            File(workingDir, PENDING_JVM_CRASH_FILE_NAME).writeText(writer.toString())
            val metadata = JSONObject().apply {
                put("source", "Application")
                put("crashedAt", formatTimestampISO8601(Date()))
                put("exceptionName", throwable.javaClass.name)
                put("exceptionReason", throwable.message ?: "")
                put("processName", Application.application.packageName)
                put("appVersion", BuildConfig.VERSION_CODE.toString())
                put("appMarketingVersion", BuildConfig.VERSION_NAME)
                runCatching {
                    put("coreVersion", Libbox.version())
                    put("goVersion", Libbox.goVersion())
                }
            }
            File(workingDir, PENDING_JVM_METADATA_FILE_NAME).writeText(metadata.toString())
        } catch (_: Throwable) {
        }
    }

    suspend fun refresh() = withContext(Dispatchers.IO) {
        synchronized(archiveAccess) {
            archivePendingUiJvmCrashReports()
            val reports = scanCrashReports()
            _reports.value = reports
            _unreadCount.value = reports.count { !it.isRead }
        }
    }

    private fun archivePendingUiJvmCrashReports() {
        // Leave a failed archive queued for the next refresh. A UI failure must
        // not interrupt installation of the default process's report handler.
        runCatching { UiJvmCrashReports(baseDir).archiveTo(File(workingDir, CRASH_REPORTS_DIR_NAME)) }
    }

    private fun archivePendingJvmCrashReport() {
        val crashFile = File(workingDir, PENDING_JVM_CRASH_FILE_NAME)
        val metadataFile = File(workingDir, PENDING_JVM_METADATA_FILE_NAME)
        val configFile = File(baseDir, CONFIG_FILE_NAME)
        if (!crashFile.exists()) return
        val content = crashFile.readText().trim()
        if (content.isEmpty()) {
            crashFile.delete()
            metadataFile.delete()
            configFile.delete()
            return
        }
        val crashDate = Date(crashFile.lastModified())
        val reportDir = nextAvailableReportDir(crashDate)
        reportDir.mkdirs()
        crashFile.copyTo(File(reportDir, JVM_LOG_FILE_NAME), overwrite = true)
        crashFile.delete()
        if (metadataFile.exists()) {
            metadataFile.copyTo(File(reportDir, METADATA_FILE_NAME), overwrite = true)
            metadataFile.delete()
        }
        if (configFile.exists()) {
            val configContent = runCatching { configFile.readText() }.getOrNull()?.trim()
            if (!configContent.isNullOrEmpty()) {
                configFile.copyTo(File(reportDir, CONFIG_FILE_NAME), overwrite = true)
            }
            configFile.delete()
        }
    }

    private fun scanCrashReports(): List<CrashReport> {
        val crashReportsDir = File(workingDir, CRASH_REPORTS_DIR_NAME)
        if (!crashReportsDir.isDirectory) return emptyList()
        val directories = crashReportsDir.listFiles { file -> file.isDirectory } ?: return emptyList()
        return directories.mapNotNull { dir ->
            val date = parseTimestamp(dir.name) ?: return@mapNotNull null
            CrashReport(
                id = dir.name,
                date = date,
                directory = dir,
                isRead = File(dir, READ_MARKER_FILE_NAME).exists(),
            )
        }.sortedByDescending { it.date }
    }

    fun availableFiles(report: CrashReport): List<CrashReportFile> {
        val files = mutableListOf<CrashReportFile>()
        val metadataFile = File(report.directory, METADATA_FILE_NAME)
        if (metadataFile.exists()) {
            files.add(CrashReportFile(CrashReportFile.Kind.METADATA, "Metadata", metadataFile))
        }
        val goLogFile = File(report.directory, GO_LOG_FILE_NAME)
        if (goLogFile.exists()) {
            files.add(CrashReportFile(CrashReportFile.Kind.GO_LOG, "Go Crash Log", goLogFile))
        }
        val jvmLogFile = File(report.directory, JVM_LOG_FILE_NAME)
        if (jvmLogFile.exists()) {
            files.add(CrashReportFile(CrashReportFile.Kind.JVM_LOG, "JVM Crash Log", jvmLogFile))
        }
        val configFile = File(report.directory, CONFIG_FILE_NAME)
        if (configFile.exists()) {
            files.add(CrashReportFile(CrashReportFile.Kind.CONFIG, "Configuration", configFile))
        }
        return files
    }

    fun loadFileContent(file: CrashReportFile): String {
        if (!file.file.exists()) return ""
        val content = file.file.readText()
        if (file.kind == CrashReportFile.Kind.METADATA) {
            return runCatching {
                JSONObject(content).toString(2)
            }.getOrDefault(content)
        }
        return content
    }

    fun markAsRead(report: CrashReport) {
        File(report.directory, READ_MARKER_FILE_NAME).createNewFile()
        val updated = _reports.value.map {
            if (it.id == report.id) it.copy(isRead = true) else it
        }
        _reports.value = updated
        _unreadCount.value = updated.count { !it.isRead }
    }

    suspend fun delete(report: CrashReport) = withContext(Dispatchers.IO) {
        report.directory.deleteRecursively()
        val updated = _reports.value.filter { it.id != report.id }
        _reports.value = updated
        _unreadCount.value = updated.count { !it.isRead }
    }

    suspend fun deleteAll() = withContext(Dispatchers.IO) {
        File(workingDir, CRASH_REPORTS_DIR_NAME).deleteRecursively()
        _reports.value = emptyList()
        _unreadCount.value = 0
    }

    suspend fun createZipArchive(report: CrashReport, includeConfig: Boolean, includeLog: Boolean, useAgeEncryption: Boolean): File = withContext(Dispatchers.IO) {
        val cacheDir = File(Application.application.cacheDir, CRASH_REPORTS_DIR_NAME)
        cacheDir.mkdirs()
        val zipFile = File(cacheDir, if (useAgeEncryption) "${report.id}.zip.age" else "${report.id}.zip")
        zipFile.delete()
        val strippedDir = File(cacheDir, report.id)
        strippedDir.deleteRecursively()
        report.directory.copyRecursively(strippedDir, overwrite = true)
        File(strippedDir, READ_MARKER_FILE_NAME).delete()
        if (!includeConfig) {
            File(strippedDir, CONFIG_FILE_NAME).delete()
        }
        if (!includeLog) {
            File(strippedDir, GO_LOG_FILE_NAME).delete()
            File(strippedDir, JVM_LOG_FILE_NAME).delete()
        }
        Libbox.createZipArchive(strippedDir.path, zipFile.path, useAgeEncryption)
        zipFile
    }

    private fun nextAvailableReportDir(date: Date): File {
        val crashReportsDir = File(workingDir, CRASH_REPORTS_DIR_NAME)
        val baseName = timestampFormat.format(date)
        var index = 0
        while (true) {
            val suffix = if (index == 0) "" else "-$index"
            val dir = File(crashReportsDir, baseName + suffix)
            if (!dir.exists()) return dir
            index++
        }
    }

    private fun parseTimestamp(name: String): Date? {
        val components = name.split("-")
        val baseName = if (components.size > 5 && components.last().toIntOrNull() != null) {
            components.dropLast(1).joinToString("-")
        } else {
            name
        }
        return try {
            timestampFormat.parse(baseName)
        } catch (_: ParseException) {
            null
        }
    }

    private fun formatTimestampISO8601(date: Date): String {
        val format = SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ssZ", Locale.US).apply {
            timeZone = TimeZone.getTimeZone("UTC")
        }
        return format.format(date)
    }
}

/** Pure JVM storage: immutable UI entries share neither the main pending files nor its config. */
internal class UiJvmCrashReports(baseDir: File) {
    private val pendingDir = File(baseDir, PENDING_DIRECTORY_NAME)

    fun write(thread: Thread, throwable: Throwable, processName: String, appVersion: String, marketingVersion: String) {
        check(pendingDir.isDirectory || pendingDir.mkdirs() || pendingDir.isDirectory) { "Unable to create UI crash report directory" }
        val id = UUID.randomUUID().toString()
        val staged = File(pendingDir, ".$id")
        val ready = File(staged, READY_FILE_NAME)
        check(staged.mkdir()) { "Unable to stage UI crash report" }
        try {
            val stack = StringWriter().also { throwable.printStackTrace(PrintWriter(it)) }.toString()
            writeSynced(File(staged, JVM_LOG_FILE_NAME), stack)
            val metadataFile = File(staged, METADATA_FILE_NAME)
            val metadataDraft = File(staged, ".$METADATA_FILE_NAME")
            runCatching {
                val metadata = buildJsonObject {
                    put("source", "Application")
                    put("processName", processName)
                    put("threadName", thread.name)
                    put("crashedAt", utcFormat("yyyy-MM-dd'T'HH:mm:ssZ").format(Date()))
                    put("exceptionName", throwable.javaClass.name)
                    put("exceptionReason", throwable.message ?: "")
                    put("appVersion", appVersion)
                    put("appMarketingVersion", marketingVersion)
                }
                writeSynced(metadataDraft, metadata.toString())
                check(metadataDraft.renameTo(metadataFile)) { "Unable to finish UI crash metadata" }
            }.onFailure {
                // Metadata is optional for existing reports too. Preserve a
                // complete stack if metadata allocation or writing fails.
                metadataDraft.delete()
            }
            // Ready is created only after all available data files close. A
            // failed directory rename remains safe for the backend to retry.
            writeSynced(ready, "")
            staged.renameTo(File(pendingDir, id))
        } catch (error: Throwable) {
            if (!ready.isFile) staged.deleteRecursively()
            throw error
        }
    }

    fun archiveTo(reportsDir: File) {
        val entries = pendingDir.listFiles { file ->
            file.isDirectory && entryId(file) != null && File(file, READY_FILE_NAME).isFile
        }?.sortedBy { File(it, JVM_LOG_FILE_NAME).lastModified() }.orEmpty()
        if (entries.isEmpty()) return
        check(reportsDir.isDirectory || reportsDir.mkdirs() || reportsDir.isDirectory) { "Unable to create crash report archive directory" }
        val archived = reportsDir.listFiles { file -> file.isDirectory }.orEmpty().mapNotNull { report ->
            runCatching { File(report, ORIGIN_FILE_NAME).readText() }.getOrNull()
        }.toMutableSet()
        for (entry in entries) {
            val id = checkNotNull(entryId(entry))
            val claimed = File(pendingDir, ".archiving-$id")
            // Claim a stable path before copying: the UI may still be trying
            // to rename its ready directory, and refresh may follow a crash.
            if (entry != claimed && !entry.renameTo(claimed)) continue
            val log = File(claimed, JVM_LOG_FILE_NAME)
            if (id in archived || !log.isFile || log.length() == 0L) {
                check(claimed.deleteRecursively()) { "Unable to remove consumed UI crash report" }
                continue
            }
            val report = reserveReportDir(reportsDir, Date(log.lastModified()))
            try {
                copySynced(log, File(report, JVM_LOG_FILE_NAME))
                File(claimed, METADATA_FILE_NAME).takeIf { it.isFile }?.let { copySynced(it, File(report, METADATA_FILE_NAME)) }
                writeSynced(File(report, ORIGIN_FILE_NAME), id)
            } catch (error: Throwable) {
                report.deleteRecursively()
                throw error
            }
            // Retain a stable origin so a crash after copying, before deleting
            // the pending entry, does not produce a second archived report.
            archived.add(id)
            check(claimed.deleteRecursively()) { "Unable to remove archived UI crash report" }
        }
    }

    private fun entryId(file: File): String? {
        val id = file.name.removePrefix(".archiving-").removePrefix(".")
        return runCatching { id.takeIf { UUID.fromString(it).toString() == it } }.getOrNull()
    }

    private fun reserveReportDir(reportsDir: File, date: Date): File {
        val timestamp = utcFormat("yyyy-MM-dd'T'HH-mm-ss").format(date)
        var index = 0
        while (true) {
            val directory = File(reportsDir, timestamp + if (index == 0) "" else "-$index")
            if (directory.mkdir()) return directory
            check(directory.exists()) { "Unable to reserve UI crash report archive" }
            index++
        }
    }

    private fun writeSynced(file: File, content: String) = FileOutputStream(file).use { output ->
        output.write(content.toByteArray(Charsets.UTF_8))
        output.fd.sync()
    }

    private fun copySynced(source: File, target: File) {
        check(target.createNewFile()) { "Unable to create UI crash report archive file" }
        source.inputStream().use { input ->
            FileOutputStream(target).use { output ->
                input.copyTo(output)
                output.fd.sync()
            }
        }
    }

    private fun utcFormat(pattern: String) = SimpleDateFormat(pattern, Locale.US).apply {
        timeZone = TimeZone.getTimeZone("UTC")
    }

    companion object {
        const val PENDING_DIRECTORY_NAME = "crash_reports_ui_pending"
        private const val READY_FILE_NAME = ".ready"
        private const val ORIGIN_FILE_NAME = ".ui-origin"
    }
}
