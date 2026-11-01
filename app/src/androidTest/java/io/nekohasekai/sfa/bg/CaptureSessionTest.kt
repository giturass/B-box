package io.nekohasekai.sfa.bg

import android.os.Binder
import android.os.ParcelFileDescriptor
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File
import java.io.IOException
import java.util.concurrent.TimeoutException
import java.util.concurrent.atomic.AtomicInteger

@RunWith(AndroidJUnit4::class)
class CaptureSessionTest {
    @Test fun packagedCollectorExecutesOnAndroid() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val executable = File(context.applicationInfo.nativeLibraryDir, "libbbox_capture.so")
        assertTrue("Collector must be packaged and executable", executable.canExecute())
        val closed = AtomicInteger()
        val session = RootCaptureSession(executable, Binder(), { closed.incrementAndGet() })
        try {
            val error = assertThrows(IllegalStateException::class.java) { session.start("{}") }
            assertTrue(error.message, error.message.orEmpty().contains("requires Root"))
            assertEquals(1, closed.get())
        } finally { session.close() }
    }

    @Test fun collectorReadyPipeAndOwnerDeath() {
        val closed = AtomicInteger()
        lateinit var child: Process
        val session = RootCaptureSession(File("/system/bin/sh"), Binder(), { closed.incrementAndGet() },
            spawn = {
                ProcessBuilder("/system/bin/sh", "-c",
                    """read config; printf 'linker warning\n' >&2; printf '%131072s' warning >&2; printf '%s\n' '{"type":"progress","version":1,"stage":"loading BPF programs"}' '{"type":"progress","version":1,"stage":"attaching TLS probes","message":"libssl.so SSL_read=0x1020","level":"debug","time":1700000000000}' '{"type":"ready","version":1,"libraries":1}'; cat >/dev/null""")
                    .start().also { child = it }
            })
        try {
            session.start("{}")
            ParcelFileDescriptor.AutoCloseInputStream(session.getFileDescriptor()).use { input ->
                assertTrue(readCaptureLine(input).toString(Charsets.UTF_8).contains("\"ready\""))
                val logs = List(6) { JSONObject(readCaptureLine(input).toString(Charsets.UTF_8)) }
                assertTrue(logs.all { it.getString("type") == "log" && it.getInt("version") == 1 })
                assertTrue(logs.any { it.getString("message") == "loading BPF programs" })
                val attached = logs.single { it.getString("message") == "libssl.so SSL_read=0x1020" }
                assertEquals(1700000000000L, attached.getLong("time"))
                assertEquals("debug", attached.getString("level"))
                assertEquals("linker warning", logs.last().getString("message"))
                assertEquals("warn", logs.last().getString("level"))
                assertThrows(IllegalStateException::class.java) { session.getFileDescriptor() }
                session.binderDied()
                assertEquals(-1, input.read())
            }
            assertEquals(0, child.exitValue())
            assertEquals(1, closed.get())
        } finally { session.close() }
        assertEquals(1, closed.get())
    }

    @Test fun collectorFailureNeverReturnsAnActiveSession() {
        val closed = AtomicInteger()
        val session = RootCaptureSession(File("/system/bin/sh"), Binder(), { closed.incrementAndGet() },
            spawn = {
                ProcessBuilder("/system/bin/sh", "-c",
                    """read config; printf '%s\n' '{"type":"progress","version":1,"stage":"loading BPF programs"}' '{"type":"error","error":"unsupported kernel"}'""").start()
            })
        val error = assertThrows(IllegalStateException::class.java) { session.start("{}") }
        assertTrue(error.message.orEmpty().contains("unsupported kernel"))
        assertTrue(error.message.orEmpty().contains("loading BPF programs"))
        assertThrows(IllegalStateException::class.java) { session.getFileDescriptor() }
        assertEquals(1, closed.get())
    }

    @Test fun collectorStartupTimeoutIncludesStageAndClosesSession() {
        val closed = AtomicInteger()
        lateinit var child: Process
        val session = RootCaptureSession(File("/system/bin/sh"), Binder(), { closed.incrementAndGet() },
            spawn = {
                ProcessBuilder("/system/bin/sh", "-c",
                    """read config; printf '%s\n' '{"type":"progress","version":1,"stage":"loading BPF programs"}' '{"type":"progress","version":1,"stage":"inspecting TLS symbols in libslow.so"}'; cat >/dev/null""")
                    .start().also { child = it }
            }, startupTimeoutMillis = 2000)
        try {
            val error = assertThrows(IllegalStateException::class.java) { session.start("{}") }
            assertTrue(error.message, error.message.orEmpty().contains("timed out after 2000 ms"))
            assertTrue(error.message, error.message.orEmpty().contains("inspecting TLS symbols in libslow.so"))
            assertTrue(error.cause is TimeoutException)
            assertThrows(IllegalStateException::class.java) { session.getFileDescriptor() }
            assertEquals(0, child.exitValue())
            assertEquals(1, closed.get())
        } finally { session.close() }
        assertEquals(1, closed.get())
    }

    @Test fun startupProgressCannotReplaceValidReady() {
        for (ready in listOf(
            """{"type":"ready","version":1,"libraries":0}""",
            """{"type":"ready","version":2,"libraries":1}""",
        )) {
            val session = RootCaptureSession(File("/system/bin/sh"), Binder(), {},
                spawn = {
                    ProcessBuilder("/system/bin/sh", "-c",
                        """read config; printf '%s\n' '{"type":"progress","version":1,"stage":"attaching TLS probes"}' '$ready'; cat >/dev/null""").start()
                })
            try {
                val error = assertThrows(IllegalStateException::class.java) { session.start("{}") }
                assertTrue(error.message, error.message.orEmpty().contains("did not attach compatible TLS probes"))
                assertThrows(IllegalStateException::class.java) { session.getFileDescriptor() }
            } finally { session.close() }
        }
    }

    @Test fun startupFailureKeepsTheFailedOperation() {
        val closed = AtomicInteger()
        val failure = IOException("Unable to launch the packaged collector", IOException("permission denied"))
        val session = RootCaptureSession(File("/system/bin/sh"), Binder(), { closed.incrementAndGet() },
            spawn = { throw failure })
        val error = assertThrows(IllegalStateException::class.java) { session.start("{}") }
        assertTrue(error.message, error.message.orEmpty().endsWith(failure.message!!))
        assertSame(failure, error.cause)
        assertEquals(1, closed.get())
    }

    @Test fun collectorStartupKeepsStderrDiagnosticsBounded() {
        val session = RootCaptureSession(File("/system/bin/sh"), Binder(), {},
            spawn = {
                ProcessBuilder("/system/bin/sh", "-c",
                    """read config; printf 'loader failure\n' >&2; printf '%131072s' warning >&2; exit 1""").start()
            })
        val error = assertThrows(IllegalStateException::class.java) { session.start("{}") }
        assertTrue(error.message.orEmpty().contains("loader failure"))
        assertTrue(error.message.orEmpty().length < 8192)
        assertThrows(IllegalStateException::class.java) { session.getFileDescriptor() }
    }

    @Test fun startupHistoryKeepsOnlyRecentProgressAndReadyStillComesFirst() {
        val session = RootCaptureSession(File("/system/bin/sh"), Binder(), {}, spawn = {
            ProcessBuilder("/system/bin/sh", "-c",
                """read config; i=0; while [ "${'$'}i" -lt 80 ]; do printf '{"type":"progress","version":1,"stage":"step-%s"}\n' "${'$'}i"; i=${'$'}((i+1)); done; printf '%s\n' '{"type":"ready","version":1,"libraries":1}'; cat >/dev/null""").start()
        })
        try {
            session.start("{}")
            ParcelFileDescriptor.AutoCloseInputStream(session.getFileDescriptor()).use { input ->
                assertEquals("ready", JSONObject(readCaptureLine(input).toString(Charsets.UTF_8)).getString("type"))
                val logs = List(64) { JSONObject(readCaptureLine(input).toString(Charsets.UTF_8)) }
                assertEquals("step-16", logs.first().getString("message"))
                assertEquals("step-79", logs.last().getString("message"))
                assertTrue(logs.all { it.getString("type") == "log" })
                session.close()
                assertEquals(-1, input.read())
            }
        } finally { session.close() }
    }
}
