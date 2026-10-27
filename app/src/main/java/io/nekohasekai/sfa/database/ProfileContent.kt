package io.nekohasekai.sfa.database

import android.util.AtomicFile
import io.nekohasekai.libbox.Libbox
import kotlinx.coroutines.sync.Mutex
import java.io.File
import java.security.MessageDigest

/** Serializes foreground edits with the background subscription updater. */
internal object ProfileContent {
    val mutex = Mutex()

    fun revision(content: String): String = MessageDigest.getInstance("SHA-256")
        .digest(content.toByteArray()).joinToString("") { "%02x".format(it.toInt() and 0xff) }

    fun validate(content: String) {
        require(content.toByteArray().size <= 16 * 1024 * 1024) { "Configuration exceeds 16 MiB" }
        Libbox.checkConfig(content)
    }

    fun write(file: File, content: String) {
        val atomic = AtomicFile(file)
        val stream = atomic.startWrite()
        try {
            stream.write(content.toByteArray())
            atomic.finishWrite(stream)
        } catch (e: Exception) {
            atomic.failWrite(stream)
            throw e
        }
    }
}
