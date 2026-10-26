package io.nekohasekai.sfa.flutter

import io.nekohasekai.sfa.bg.CrashReportManager
import io.nekohasekai.sfa.bg.OOMReportManager
import io.nekohasekai.sfa.bg.PowerReportManager
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File

internal class BoxReports(private val activity: BoxActivity) {
    suspend fun invoke(method: String, args: Map<String, Any?>): Any? {
        val kind = args["kind"] as? String ?: error("Missing report kind")
        require(kind in listOf("crash", "oom", "power")) { "Unknown report kind" }
        val result = withContext(Dispatchers.IO) {
            when (kind) {
                "crash" -> {
                    CrashReportManager.refresh()
                    if (method == "reports.list") return@withContext CrashReportManager.reports.value.map {
                        mapOf("id" to it.id, "date" to it.date.time, "read" to it.isRead)
                    }
                    if (method == "reports.clear") { CrashReportManager.deleteAll(); return@withContext null }
                    val report = CrashReportManager.reports.value.firstOrNull { it.id == args["id"] } ?: error("Report no longer exists")
                    when (method) {
                        "reports.delete" -> { CrashReportManager.delete(report); null }
                        "reports.files" -> {
                            CrashReportManager.markAsRead(report)
                            CrashReportManager.availableFiles(report).map { mapOf("name" to it.file.name, "label" to it.displayName, "kind" to it.kind.name, "size" to it.file.length()) }
                        }
                        "reports.read", "reports.save" -> {
                            val file = CrashReportManager.availableFiles(report).firstOrNull { it.file.name == args["name"] } ?: error("Report file no longer exists")
                            if (method == "reports.save") file.file else {
                                require(file.kind.name != "PROFILE") { "Download binary profiles to inspect them" }
                                require(file.file.length() <= 16 * 1024 * 1024) { "Report is too large to view; download it" }
                                CrashReportManager.loadFileContent(file)
                            }
                        }
                        "reports.export" -> CrashReportManager.createZipArchive(report, args["config"] == true, args["logs"] == true, args["encrypt"] == true)
                        else -> error("Unknown report operation")
                    }
                }
                "oom" -> {
                    OOMReportManager.refresh()
                    if (method == "reports.list") return@withContext OOMReportManager.reports.value.map {
                        mapOf("id" to it.id, "date" to it.date.time, "read" to it.isRead)
                    }
                    if (method == "reports.clear") { OOMReportManager.deleteAll(); return@withContext null }
                    val report = OOMReportManager.reports.value.firstOrNull { it.id == args["id"] } ?: error("Report no longer exists")
                    when (method) {
                        "reports.delete" -> { OOMReportManager.delete(report); null }
                        "reports.files" -> {
                            OOMReportManager.markAsRead(report)
                            OOMReportManager.availableFiles(report).map { mapOf("name" to it.file.name, "label" to it.displayName, "kind" to it.kind.name, "size" to it.file.length()) }
                        }
                        "reports.read", "reports.save" -> {
                            val file = OOMReportManager.availableFiles(report).firstOrNull { it.file.name == args["name"] } ?: error("Report file no longer exists")
                            if (method == "reports.save") file.file else {
                                require(file.kind.name != "PROFILE") { "Download binary profiles to inspect them" }
                                require(file.file.length() <= 16 * 1024 * 1024) { "Report is too large to view; download it" }
                                OOMReportManager.loadFileContent(file)
                            }
                        }
                        "reports.export" -> OOMReportManager.createZipArchive(report, args["config"] == true, args["logs"] == true, args["encrypt"] == true)
                        else -> error("Unknown report operation")
                    }
                }
                "power" -> {
                    PowerReportManager.refresh()
                    if (method == "reports.list") return@withContext PowerReportManager.reports.value.map {
                        mapOf("id" to it.id, "date" to it.date.time, "read" to it.isRead)
                    }
                    if (method == "reports.clear") { PowerReportManager.deleteAll(); return@withContext null }
                    val report = PowerReportManager.reports.value.firstOrNull { it.id == args["id"] } ?: error("Report no longer exists")
                    when (method) {
                        "reports.delete" -> { PowerReportManager.delete(report); null }
                        "reports.files" -> {
                            PowerReportManager.markAsRead(report)
                            PowerReportManager.availableFiles(report).map { mapOf("name" to it.file.name, "label" to it.displayName, "kind" to it.kind.name, "size" to it.file.length()) }
                        }
                        "reports.read", "reports.save" -> {
                            val file = PowerReportManager.availableFiles(report).firstOrNull { it.file.name == args["name"] } ?: error("Report file no longer exists")
                            if (method == "reports.save") file.file else {
                                require(file.kind.name != "PROFILE") { "Download binary profiles to inspect them" }
                                require(file.file.length() <= 16 * 1024 * 1024) { "Report is too large to view; download it" }
                                PowerReportManager.loadFileContent(file)
                            }
                        }
                        "reports.export" -> PowerReportManager.createZipArchive(report, args["config"] == true, args["logs"] == true, args["encrypt"] == true)
                        else -> error("Unknown report operation")
                    }
                }
                else -> error("Unknown report kind")
            }
        }
        if (result is File) {
            val destination = activity.pickDocument(result.name) ?: return false
            withContext(Dispatchers.IO) {
                activity.contentResolver.openOutputStream(destination, "wt")?.use { output -> result.inputStream().use { it.copyTo(output) } }
                    ?: error("Unable to save report")
            }
            return true
        }
        return result
    }
}
