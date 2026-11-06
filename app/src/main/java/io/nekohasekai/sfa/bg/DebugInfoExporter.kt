package io.nekohasekai.sfa.bg

import android.content.Context
import io.nekohasekai.sfa.utils.HookErrorClient
import java.io.BufferedInputStream
import java.io.BufferedOutputStream
import java.io.File
import java.io.FileInputStream
import java.io.OutputStream
import java.io.IOException
import java.util.Timer
import java.util.TimerTask
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.zip.Deflater
import java.util.zip.ZipEntry
import java.util.zip.ZipOutputStream

object DebugInfoExporter {
    private const val BUFFER_SIZE = 128 * 1024

    fun export(context: Context, output: OutputStream, packageName: String) {
        val warnings = mutableListOf<String>()
        BoundedZipOutputStream(BufferedOutputStream(output)).use { zip ->
            addTextEntry(zip, "system/export_info.txt", buildExportInfo(context, packageName))
            addFrameworkEntries(zip, warnings)
            addApexEntries(zip, warnings)
            addLogEntries(zip, warnings, context)
            addSystemEntries(zip, warnings, packageName)
            if (warnings.isNotEmpty()) addTextEntry(zip, "logs/debug_export.txt", warnings.joinToString("\n"))
        }
    }

    private class BoundedZipOutputStream(output: OutputStream) : ZipOutputStream(output) {
        private var written = 0L
        override fun write(bytes: ByteArray, offset: Int, length: Int) {
            if (written + length > 512L * 1024 * 1024) throw IOException("Diagnostic archive exceeds 512 MiB")
            super.write(bytes, offset, length)
            written += length
        }
    }

    private fun buildExportInfo(context: Context, packageName: String): String {
        val sb = StringBuilder()
        sb.append("package=").append(packageName).append('\n')
        sb.append("timestamp=").append(System.currentTimeMillis()).append('\n')
        sb.append("context_class=").append(context.javaClass.name).append('\n')
        return sb.toString()
    }

    private fun addFrameworkEntries(zip: ZipOutputStream, warnings: MutableList<String>): Int {
        var count = 0
        val root = File("/system/framework")
        if (!root.isDirectory) return 0
        val targetFiles = setOf("framework.jar", "services.jar")
        val files = root.listFiles() ?: emptyArray()
        for (file in files) {
            if (!file.isFile) continue
            if (file.name !in targetFiles) continue
            if (addFileEntry(zip, file, "framework/${file.name}", warnings, noCompression = true)) {
                count++
            }
        }
        return count
    }

    private fun addApexEntries(zip: ZipOutputStream, warnings: MutableList<String>): Int {
        val file = File("/apex/com.android.tethering/javalib/service-connectivity.jar")
        if (!file.isFile || file.length() > 128L * 1024 * 1024) {
            warnings.add("missing or oversized file: ${file.path}")
            return 0
        }
        return if (addFileEntry(zip, file, "framework/apex_com.android.tethering/service-connectivity.jar", warnings, noCompression = true)) 1 else 0
    }

    private fun addLogEntries(zip: ZipOutputStream, warnings: MutableList<String>, context: Context): Int {
        var count = 0
        if (streamCommandToZip(zip, "logs/logcat.txt", warnings, listOf("logcat", "-d", "-b", "all")) != null) count++
        if (streamCommandToZip(zip, "logs/dmesg.txt", warnings, listOf("dmesg")) != null) count++
        val serviceLogsResult = HookErrorClient.query(context)
        if (serviceLogsResult.logs.isNotEmpty()) {
            val formatted = formatLogEntries(serviceLogsResult.logs)
            addTextEntry(zip, "logs/service_logs.txt", formatted)
            count++
        } else if (serviceLogsResult.failure != null) {
            warnings.add("service logs: ${serviceLogsResult.failure}${serviceLogsResult.detail?.let { " ($it)" } ?: ""}")
        }
        val lspdDir = File("/data/adb/lspd/log")
        if (lspdDir.isDirectory) {
            val files = lspdDir.listFiles() ?: emptyArray()
            for (file in files.sortedByDescending { it.lastModified() }.take(8)) {
                if (!file.isFile) continue
                if (addFileEntry(zip, file, "logs/lspd/${file.name}", warnings)) count++
            }
        } else {
            warnings.add("lspd logs not found: /data/adb/lspd/log")
        }
        return count
    }

    private fun formatLogEntries(entries: List<LogEntry>): String {
        val dateFormat = SimpleDateFormat("yyyy-MM-dd HH:mm:ss", Locale.US)
        return entries.joinToString("\n---\n") { entry ->
            val levelName = when (entry.level) {
                LogEntry.LEVEL_DEBUG -> "DEBUG"
                LogEntry.LEVEL_INFO -> "INFO"
                LogEntry.LEVEL_WARN -> "WARN"
                LogEntry.LEVEL_ERROR -> "ERROR"
                else -> "UNKNOWN"
            }
            val timestamp = dateFormat.format(Date(entry.timestamp))
            buildString {
                append(levelName).append("[").append(timestamp).append("] ")
                append("[").append(entry.source).append("]: ")
                append(entry.message)
                if (!entry.stackTrace.isNullOrEmpty()) {
                    append("\n").append(entry.stackTrace)
                }
            }
        }
    }

    private fun addSystemEntries(zip: ZipOutputStream, warnings: MutableList<String>, packageName: String): Int {
        var count = 0
        if (streamCommandToZip(zip, "system/getprop.txt", warnings, listOf("getprop")) != null) count++
        if (streamCommandToZip(zip, "system/uname.txt", warnings, listOf("uname", "-a")) != null) count++
        if (streamCommandToZip(zip, "system/id.txt", warnings, listOf("id")) != null) count++
        if (addFileEntry(zip, File("/proc/version"), "system/proc_version.txt", warnings)) count++
        if (addFileEntry(zip, File("/proc/cpuinfo"), "system/cpuinfo.txt", warnings)) count++
        if (addFileEntry(zip, File("/proc/meminfo"), "system/meminfo.txt", warnings)) count++
        if (addFileEntry(zip, File("/proc/pressure/cpu"), "system/pressure_cpu.txt", warnings)) count++
        if (addFileEntry(zip, File("/proc/pressure/memory"), "system/pressure_memory.txt", warnings)) count++
        if (addFileEntry(zip, File("/proc/pressure/io"), "system/pressure_io.txt", warnings)) count++
        val cmdPackages =
            streamCommandToZip(
                zip,
                "system/packages_cmd.txt",
                warnings,
                listOf("cmd", "package", "list", "packages", "-f"),
            )
        if (cmdPackages != null) count++
        if ((cmdPackages == null || cmdPackages.bytes == 0L) && (cmdPackages?.exitCode ?: 1) != 0) {
            if (streamCommandToZip(
                    zip,
                    "system/packages_pm.txt",
                    warnings,
                    listOf("pm", "list", "packages", "-f"),
                ) != null
            ) {
                count++
            }
        }
        if (streamCommandToZip(
                zip,
                "system/dumpsys_package_$packageName.txt",
                warnings,
                listOf("dumpsys", "package", packageName),
            ) != null
        ) {
            count++
        }
        return count
    }

    private fun addFileEntry(
        zip: ZipOutputStream,
        file: File,
        entryName: String,
        warnings: MutableList<String>,
        noCompression: Boolean = false,
    ): Boolean {
        if (!file.isFile || file.length() > 128L * 1024 * 1024) {
            warnings.add("missing or oversized file: ${file.path}")
            return false
        }
        try {
            if (noCompression) zip.setLevel(Deflater.NO_COMPRESSION)
            zip.putNextEntry(ZipEntry(entryName))
            BufferedInputStream(FileInputStream(file)).use { input ->
                val buffer = ByteArray(BUFFER_SIZE)
                while (true) {
                    val read = input.read(buffer)
                    if (read <= 0) break
                    zip.write(buffer, 0, read)
                }
            }
            zip.closeEntry()
            if (noCompression) zip.setLevel(Deflater.DEFAULT_COMPRESSION)
            return true
        } catch (e: Throwable) {
            warnings.add("zip failed ${file.path}: ${e.message}")
            if (noCompression) zip.setLevel(Deflater.DEFAULT_COMPRESSION)
            return false
        }
    }

    private fun addTextEntry(zip: ZipOutputStream, entryName: String, content: String) {
        val entry = ZipEntry(entryName)
        zip.putNextEntry(entry)
        val bytes = content.toByteArray()
        zip.write(bytes)
        zip.closeEntry()
    }

    private data class CommandResult(val exitCode: Int, val bytes: Long)

    private fun streamCommandToZip(
        zip: ZipOutputStream,
        entryName: String,
        warnings: MutableList<String>,
        command: List<String>,
    ): CommandResult? {
        val process = try { ProcessBuilder(command).redirectErrorStream(true).start() } catch (error: Exception) {
            warnings.add("command failed (${command.joinToString(" ")}): ${error.message}")
            return null
        }
        val timer = Timer("hook-debug-timeout", true)
        timer.schedule(object : TimerTask() {
            override fun run() { process.destroy() }
        }, 15000)
        return try {
            zip.putNextEntry(ZipEntry(entryName))
            var bytes = 0L
            process.inputStream.use { input ->
                val buffer = ByteArray(BUFFER_SIZE)
                while (bytes < 8L * 1024 * 1024) {
                    val read = input.read(buffer)
                    if (read <= 0) break
                    zip.write(buffer, 0, read)
                    bytes += read
                }
            }
            process.destroy()
            val code = process.waitFor()
            zip.closeEntry()
            if (code != 0 || bytes >= 8L * 1024 * 1024) warnings.add("command incomplete (${command.joinToString(" ")}): exit=$code, bytes=$bytes")
            CommandResult(code, bytes)
        } catch (error: Exception) {
            warnings.add("command failed (${command.joinToString(" ")}): ${error.message}")
            runCatching { zip.closeEntry() }
            null
        } finally {
            timer.cancel()
            process.destroy()
        }
    }
}
