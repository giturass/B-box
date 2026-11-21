package io.nekohasekai.sfa.backend.tools

import android.os.ParcelFileDescriptor
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import kotlinx.coroutines.CancellationException
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File
import java.io.FileDescriptor
import java.util.UUID

@RunWith(AndroidJUnit4::class)
class TaildropSendManagerTest {
    private fun withFiles(test: (List<TaildropSendFile>, List<FileDescriptor>) -> Unit) {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val directory = File(context.cacheDir, "taildrop-send-test-${UUID.randomUUID()}")
        check(directory.mkdirs())
        val opened = mutableListOf<TaildropSendFile>()
        val descriptors = mutableListOf<FileDescriptor>()
        try {
            repeat(2) { index ->
                val file = File(directory, "$index.txt").apply { writeText("fixture $index") }
                val descriptor = ParcelFileDescriptor.open(file, ParcelFileDescriptor.MODE_READ_ONLY)
                descriptors.add(descriptor.fileDescriptor)
                opened.add(TaildropSendFile(file.name, descriptor))
            }
            test(opened, descriptors)
        } finally {
            opened.forEach(TaildropSendFile::close)
            directory.deleteRecursively()
        }
    }

    @Test fun rejectedForegroundStartupRemovesTheSessionAndClosesEveryFile() = withFiles { files, descriptors ->
        val previous = TaildropSendManager.sessions.value
        val failure = IllegalStateException("foreground startup rejected")
        var attemptedID: Long? = null
        val result = runCatching {
            TaildropSendManager.send("endpoint", "peer", "Peer", files) {
                attemptedID = TaildropSendManager.sessions.value.single { it !in previous }.id
                throw failure
            }
        }
        assertTrue(result.isFailure)
        assertEquals(failure, result.exceptionOrNull())
        assertNotNull(attemptedID)
        assertEquals(previous, TaildropSendManager.sessions.value)
        assertTrue(descriptors.none(FileDescriptor::valid))
    }

    @Test fun cancellingBeforeTheLazyWorkerStartsReleasesFilesAndDoesNotReportHandoff() = withFiles { files, descriptors ->
        val previous = TaildropSendManager.sessions.value
        val result = runCatching {
            TaildropSendManager.send("endpoint", "peer", "Peer", files) {
                val id = TaildropSendManager.sessions.value.single { it !in previous }.id
                TaildropSendManager.cancel(id)
            }
        }
        assertTrue(result.exceptionOrNull() is CancellationException)
        assertEquals(previous, TaildropSendManager.sessions.value)
        assertTrue(descriptors.none(FileDescriptor::valid))
    }
}
