package io.nekohasekai.sfa.bg

import android.os.Build
import android.os.IBinder
import android.os.ParcelFileDescriptor
import org.json.JSONObject
import java.io.BufferedInputStream
import java.io.ByteArrayOutputStream
import java.io.File
import java.io.IOException
import java.io.OutputStream
import java.util.ArrayDeque
import java.util.concurrent.ExecutionException
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.TimeoutException
import java.util.concurrent.atomic.AtomicReference

/** Owns one fixed, packaged collector. No shell, listener or downloaded executable. */
internal class RootCaptureSession(
    private val executable: File,
    private val owner: IBinder,
    private val onClosed: (RootCaptureSession) -> Unit,
    private val spawn: (File) -> Process = { ProcessBuilder(it.absolutePath).start() },
    private val startupTimeoutMillis: Long = 25000,
) : ICaptureSession.Stub(), IBinder.DeathRecipient {
    private val lock = Any()
    private var closed = false
    private var process: Process? = null
    private val pipe = ParcelFileDescriptor.createPipe()
    private var claimed = false
    private val diagnostics = ByteArrayOutputStream()
    private var errorReader: Thread? = null

    private fun drainErrors(child: Process): Thread = Thread({
        try {
            child.errorStream.use { input ->
                val buffer = ByteArray(4096)
                while (true) {
                    val count = input.read(buffer)
                    if (count < 0) break
                    synchronized(diagnostics) {
                        diagnostics.write(buffer, 0, minOf(count, 4096 - diagnostics.size()))
                    }
                }
            }
        } catch (_: IOException) {
            // Session shutdown closes the child streams.
        }
    }, "capture-errors").apply { isDaemon = true; start() }

    fun start(config: String) {
        val stage = AtomicReference("launching the collector")
        val history = CaptureStartupHistory()
        fun progress(value: String) {
            stage.set(value)
            history.record(value)
        }
        progress(stage.get())
        try {
            val child = synchronized(lock) {
                check(!closed) { "Capture owner disconnected" }
                owner.linkToDeath(this, 0)
                check(executable.canExecute()) { "The packaged eBPF collector is unavailable" }
                spawn(executable).also {
                    process = it
                    errorReader = drainErrors(it)
                }
            }
            val input = BufferedInputStream(child.inputStream)
            val executor = Executors.newSingleThreadExecutor { task ->
                Thread(task, "capture-start").apply { isDaemon = true }
            }
            val first = try {
                executor.submit<ByteArray> {
                    progress("sending capture settings")
                    val sendError = try {
                        child.outputStream.write((config + "\n").toByteArray(Charsets.UTF_8))
                        child.outputStream.flush()
                        null
                    } catch (error: IOException) {
                        error
                    }
                    // Root/kernel checks may exit before reading stdin; preserve
                    // their structured error even if sending settings hit EPIPE.
                    progress("waiting for the collector's startup response")
                    readCaptureStartup(input, sendError) { value, event ->
                        stage.set(value)
                        history.record(event.optString("message").ifBlank { value },
                            event.optString("level", "debug"), event.optLong("time"))
                    }
                }.get(startupTimeoutMillis, TimeUnit.MILLISECONDS)
            } finally {
                executor.shutdownNow()
            }
            synchronized(lock) {
                check(!closed) { "Capture owner disconnected" }
                Thread({
                    try {
                        ParcelFileDescriptor.AutoCloseOutputStream(pipe[1]).use { output ->
                            output.write(first)
                            output.write('\n'.code)
                            // Ready remains first. Replay the bounded startup
                            // history afterward so libbox can retain it off-page.
                            history.writeTo(output)
                            val initialErrors = synchronized(diagnostics) { diagnostics.toByteArray() }
                            writeCaptureDiagnostic(output, initialErrors.toString(Charsets.UTF_8))
                            input.copyTo(output, 8192)
                            val finalErrors = synchronized(diagnostics) { diagnostics.toByteArray() }
                            if (finalErrors.size > initialErrors.size) {
                                writeCaptureDiagnostic(output, String(finalErrors, initialErrors.size,
                                    finalErrors.size - initialErrors.size, Charsets.UTF_8))
                            }
                        }
                    } catch (_: IOException) {
                        // Closing the core reader or owner session intentionally breaks the pipe.
                    } finally {
                        close()
                    }
                }, "capture-events").apply { isDaemon = true }.start()
            }
        } catch (error: Exception) {
            val cause = if (error is ExecutionException) error.cause ?: error else error
            // Only unwrap the Future wrapper; an IOException's own message
            // describes the failed operation and must not be lost to its cause.
            val reason = when (cause) {
                is TimeoutException -> "eBPF capture startup timed out after $startupTimeoutMillis ms while ${stage.get()}"
                is InterruptedException -> "eBPF capture startup was interrupted while ${stage.get()}"
                else -> cause.message?.takeIf { it.isNotBlank() }
                    ?: "eBPF capture startup failed (${cause.javaClass.simpleName}) while ${stage.get()}"
            }
            runCatching { close() }.onFailure { cause.addSuppressed(it) }
            if (cause is InterruptedException) Thread.currentThread().interrupt()
            val detail = synchronized(diagnostics) { diagnostics.toString(Charsets.UTF_8.name()).trim() }
            throw IllegalStateException(listOf(history.text(), reason, detail).filter { it.isNotBlank() }.joinToString("\n"), cause)
        }
    }

    override fun getFileDescriptor(): ParcelFileDescriptor = synchronized(lock) {
        check(!closed && !claimed) { "Capture session is closed or already connected" }
        claimed = true
        ParcelFileDescriptor.dup(pipe[0].fileDescriptor)
    }

    override fun binderDied() = close()

    override fun close() {
        val child = synchronized(lock) {
            if (closed) return
            closed = true
            process.also { process = null }
        }
        try {
            runCatching { owner.unlinkToDeath(this, 0) }
            runCatching { pipe[0].close() }
            runCatching { pipe[1].close() }
            if (child != null) {
                // EOF is the normal shutdown signal and lets Go close every link/map.
                runCatching { child.outputStream.close() }
                val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(2)
                while (System.nanoTime() < deadline) {
                    if (runCatching { child.exitValue() }.isSuccess) break
                    Thread.sleep(20)
                }
                if (runCatching { child.exitValue() }.isFailure) {
                    child.destroy()
                    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) child.destroyForcibly()
                }
                runCatching { child.inputStream.close() }
                runCatching { errorReader?.join(250) }
                runCatching { child.errorStream.close() }
            }
        } finally {
            onClosed(this)
        }
    }
}

private fun readCaptureStartup(
    input: java.io.InputStream,
    sendError: IOException?,
    onProgress: (String, JSONObject) -> Unit,
): ByteArray {
    while (true) {
        val line = readCaptureLine(input)
        val text = line.toString(Charsets.UTF_8)
        val event = try { JSONObject(text) } catch (_: Exception) {
            throw IOException("eBPF collector failed: ${text.take(4096)}")
        }
        when (event.optString("type")) {
            "progress" -> {
                val stage = event.optString("stage").take(512).trim()
                check(event.optInt("version") == 1 && stage.isNotEmpty()) { "Invalid eBPF startup progress" }
                onProgress(stage, event)
            }
            "error" -> throw IOException(event.optString("error").ifBlank { "The eBPF collector reported a startup error" })
            else -> {
                sendError?.let { throw IOException("Unable to send eBPF capture settings", it) }
                check(event.optString("type") == "ready" && event.optInt("version") == 1 && event.optInt("libraries") > 0) {
                    "The eBPF collector did not attach compatible TLS probes"
                }
                // Progress cannot confirm startup; only this ready event can.
                return line
            }
        }
    }
}

private class CaptureStartupHistory {
    private val lines = ArrayDeque<Pair<String, ByteArray>>()
    private var bytes = 0

    @Synchronized fun record(message: String, level: String = "info", at: Long = 0) {
        val value = message.take(4096)
        val event = JSONObject().put("type", "log").put("version", 1)
            .put("time", at.takeIf { it > 0 } ?: System.currentTimeMillis())
            .put("level", level).put("message", value).toString().toByteArray(Charsets.UTF_8)
        while (lines.isNotEmpty() && (lines.size >= 64 || bytes + event.size > 64 * 1024)) {
            bytes -= lines.removeFirst().second.size
        }
        lines.addLast("[$level] $value" to event)
        bytes += event.size
    }

    @Synchronized fun writeTo(output: OutputStream) {
        for ((_, event) in lines) {
            output.write(event)
            output.write('\n'.code)
        }
    }

    @Synchronized fun text(): String = lines.joinToString("\n") { it.first }
}

private fun writeCaptureDiagnostic(output: OutputStream, message: String) {
    if (message.isBlank()) return
    val history = CaptureStartupHistory()
    history.record(message.trim(), "warn")
    history.writeTo(output)
}

internal fun readCaptureLine(input: java.io.InputStream): ByteArray {
    val line = ByteArrayOutputStream()
    while (line.size() < 64 * 1024) {
        val byte = input.read()
        if (byte == -1) throw IOException("eBPF collector exited before startup")
        if (byte == '\n'.code) return line.toByteArray()
        line.write(byte)
    }
    throw IOException("eBPF startup response exceeds 64 KiB")
}
