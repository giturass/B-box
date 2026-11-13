package io.nekohasekai.sfa.flutter

import io.nekohasekai.sfa.bg.CrashReport
import io.nekohasekai.sfa.bg.CrashReportFile
import io.nekohasekai.sfa.bg.CrashReportManager
import io.nekohasekai.sfa.bg.OOMReport
import io.nekohasekai.sfa.bg.OOMReportFile
import io.nekohasekai.sfa.bg.OOMReportManager
import io.nekohasekai.sfa.bg.PowerReport
import io.nekohasekai.sfa.bg.PowerReportFile
import io.nekohasekai.sfa.bg.PowerReportManager
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File
import java.util.Date

internal class BoxReports(private val activity: BoxActivity) {
    private val sources: Map<String, Source<*, *>> = mapOf(
        "crash" to Source<CrashReport, CrashReportFile>(
            refresh = CrashReportManager::refresh,
            reports = { CrashReportManager.reports.value },
            summary = { Summary(it.id, it.date, it.isRead) },
            files = CrashReportManager::availableFiles,
            attachment = { Attachment(it.file, it.displayName, it.kind.name) },
            content = CrashReportManager::loadFileContent,
            markAsRead = CrashReportManager::markAsRead,
            delete = CrashReportManager::delete,
            deleteAll = CrashReportManager::deleteAll,
            archive = CrashReportManager::createZipArchive,
        ),
        "oom" to Source<OOMReport, OOMReportFile>(
            refresh = OOMReportManager::refresh,
            reports = { OOMReportManager.reports.value },
            summary = { Summary(it.id, it.date, it.isRead) },
            files = OOMReportManager::availableFiles,
            attachment = { Attachment(it.file, it.displayName, it.kind.name) },
            content = OOMReportManager::loadFileContent,
            markAsRead = OOMReportManager::markAsRead,
            delete = OOMReportManager::delete,
            deleteAll = OOMReportManager::deleteAll,
            archive = OOMReportManager::createZipArchive,
        ),
        "power" to Source<PowerReport, PowerReportFile>(
            refresh = PowerReportManager::refresh,
            reports = { PowerReportManager.reports.value },
            summary = { Summary(it.id, it.date, it.isRead) },
            files = PowerReportManager::availableFiles,
            attachment = { Attachment(it.file, it.displayName, it.kind.name) },
            content = PowerReportManager::loadFileContent,
            markAsRead = PowerReportManager::markAsRead,
            delete = PowerReportManager::delete,
            deleteAll = PowerReportManager::deleteAll,
            archive = PowerReportManager::createZipArchive,
        ),
    )

    suspend fun invoke(method: String, args: Map<String, Any?>): Any? {
        val kind = args["kind"] as? String ?: error("Missing report kind")
        val source = requireNotNull(sources[kind]) { "Unknown report kind" }
        val result = withContext(Dispatchers.IO) { source.invoke(method, args) }
        return if (result is File) save(result) else result
    }

    private suspend fun save(file: File): Boolean {
        val destination = activity.pickDocument(file.name) ?: return false
        withContext(Dispatchers.IO) {
            activity.contentResolver.openOutputStream(destination, "wt")?.use { output -> file.inputStream().use { it.copyTo(output) } }
                ?: error("Unable to save report")
        }
        return true
    }

    private class Summary(val id: String, val date: Date, val read: Boolean) {
        fun toWire() = mapOf("id" to id, "date" to date.time, "read" to read)
    }

    private class Attachment(val file: File, val label: String, val kind: String) {
        fun toWire() = mapOf("name" to file.name, "label" to label, "kind" to kind, "size" to file.length())
    }

    // The crash, OOM and power report managers have the same shape but share no
    // interface, so each one is adapted to the operations the bridge exposes.
    private class Source<R, F>(
        private val refresh: suspend () -> Unit,
        private val reports: () -> List<R>,
        private val summary: (R) -> Summary,
        private val files: (R) -> List<F>,
        private val attachment: (F) -> Attachment,
        private val content: (F) -> String,
        private val markAsRead: (R) -> Unit,
        private val delete: suspend (R) -> Unit,
        private val deleteAll: suspend () -> Unit,
        private val archive: suspend (report: R, config: Boolean, logs: Boolean, encrypt: Boolean) -> File,
    ) {
        // A returned File is saved by the caller to a document chosen by the user.
        suspend fun invoke(method: String, args: Map<String, Any?>): Any? {
            refresh()
            when (method) {
                "reports.list" -> return reports().map { summary(it).toWire() }
                "reports.clear" -> {
                    deleteAll()
                    return null
                }
            }
            val report = reports().firstOrNull { summary(it).id == args["id"] } ?: error("Report no longer exists")
            return when (method) {
                "reports.delete" -> {
                    delete(report)
                    null
                }
                "reports.files" -> {
                    markAsRead(report)
                    files(report).map { attachment(it).toWire() }
                }
                "reports.read" -> {
                    val file = find(report, args["name"])
                    require(attachment(file).kind != "PROFILE") { "Download binary profiles to inspect them" }
                    content(file)
                }
                "reports.save" -> attachment(find(report, args["name"])).file
                "reports.export" -> archive(report, args["config"] == true, args["logs"] == true, args["encrypt"] == true)
                else -> error("Unknown report operation")
            }
        }

        private fun find(report: R, name: Any?): F =
            files(report).firstOrNull { attachment(it).file.name == name } ?: error("Report file no longer exists")
    }
}
