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
import java.util.concurrent.atomic.AtomicInteger

@RunWith(AndroidJUnit4::class)
class CaptureSessionTest {
    @Test fun packagedCollectorExecutesOnAndroid() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val executable = File(context.applicationInfo.nativeLibraryDir, "libbbox_capture.so")
        assertTrue("Collector must be packaged and executable", executable.canExecute())
        val process = ProcessBuilder(executable.absolutePath).redirectErrorStream(true).start()
        try {
            val event = JSONObject(readCaptureLine(process.inputStream).toString(Charsets.UTF_8))
            assertEquals("error", event.getString("type"))
            assertTrue(event.getString("error").contains("requires Root"))
            assertNotEquals(0, process.waitFor())
        } finally { process.destroy() }
    }

    @Test fun collectorReadyPipeAndOwnerDeath() {
        val closed = AtomicInteger()
        lateinit var child: Process
        val session = RootCaptureSession(File("/system/bin/sh"), Binder(), { closed.incrementAndGet() },
            spawn = {
                ProcessBuilder("/system/bin/sh", "-c",
                    """read config; printf '%s\n' '{"type":"ready","version":1,"libraries":1}'; cat >/dev/null""")
                    .start().also { child = it }
            })
        try {
            session.start("{}")
            ParcelFileDescriptor.AutoCloseInputStream(session.getFileDescriptor()).use { input ->
                assertTrue(readCaptureLine(input).toString(Charsets.UTF_8).contains("\"ready\""))
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
                    """read config; printf '%s\n' '{"type":"error","error":"unsupported kernel"}'""").start()
            })
        val error = assertThrows(IllegalStateException::class.java) { session.start("{}") }
        assertTrue(error.message.orEmpty().contains("unsupported kernel"))
        assertThrows(IllegalStateException::class.java) { session.getFileDescriptor() }
        assertEquals(1, closed.get())
    }
}

