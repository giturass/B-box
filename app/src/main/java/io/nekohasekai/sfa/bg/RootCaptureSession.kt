package io.nekohasekai.sfa.bg

import android.os.Build
import android.os.IBinder
import android.os.ParcelFileDescriptor
import org.json.JSONObject
import java.io.BufferedInputStream
import java.io.ByteArrayOutputStream
import java.io.File
import java.io.IOException
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

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
            val startup = try {
                executor.submit<Pair<ByteArray, IOException?>> {
                    val sendError = try {
                        child.outputStream.write((config + "\n").toByteArray(Charsets.UTF_8))
                        child.outputStream.flush()
                        null
                    } catch (error: IOException) {
                        error
                    }
                    // Root/kernel checks may exit before reading stdin; preserve
                    // their structured error even if sending settings hit EPIPE.
                    readCaptureLine(input) to sendError
                }.get(startupTimeoutMillis, TimeUnit.MILLISECONDS)
            } finally {
                executor.shutdownNow()
            }
            val first = startup.first
            val event = try { JSONObject(first.toString(Charsets.UTF_8)) } catch (_: Exception) {
                throw IOException("eBPF collector failed: ${first.toString(Charsets.UTF_8).take(4096)}")
            }
            if (event.optString("type") == "error") throw IOException(event.optString("error"))
            startup.second?.let { throw IOException("Unable to send eBPF capture settings", it) }
            check(event.optString("type") == "ready" && event.optInt("version") == 1 && event.optInt("libraries") > 0) {
                "The eBPF collector did not attach compatible TLS probes"
            }
            synchronized(lock) {
                check(!closed) { "Capture owner disconnected" }
                Thread({
                    try {
                        ParcelFileDescriptor.AutoCloseOutputStream(pipe[1]).use { output ->
                            output.write(first)
                            output.write('\n'.code)
                            input.copyTo(output, 8192)
                        }
                    } catch (_: IOException) {
                        // Closing the core reader or owner session intentionally breaks the pipe.
                    } finally {
                        close()
                    }
                }, "capture-events").apply { isDaemon = true }.start()
            }
        } catch (error: Exception) {
            close()
            val cause = error.cause ?: error
            val reason = cause.message ?: "eBPF capture startup failed"
            val detail = synchronized(diagnostics) { diagnostics.toString(Charsets.UTF_8.name()).trim() }
            throw IllegalStateException(if (detail.isEmpty()) reason else "$reason\n$detail", cause)
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
