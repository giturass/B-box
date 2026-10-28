package io.nekohasekai.sfa.flutter

import io.nekohasekai.sfa.qrs.QRSDecoder
import java.nio.ByteBuffer
import kotlin.io.encoding.Base64

/** One decoder per visible scanner; frames never cross camera sessions. */
internal class BoxQrScanner(private val decode: (ByteArray, String) -> Map<String, Any?>) {
    private val sessions = mutableMapOf<String, QRSDecoder>()

    @Synchronized
    fun scan(id: String, text: String): Map<String, Any?> {
        require(id.length in 1..128 && text.length <= 16384) { "Invalid QR frame" }
        val encoded = if (text.startsWith("http://") || text.startsWith("https://")) text.substringAfter('#', "") else text
        val payload = runCatching { Base64.Default.decode(encoded.filterNot(Char::isWhitespace)) }.getOrNull()
        if (payload == null) return mapOf("text" to text)
        require(id in sessions || sessions.size < 4) { "Too many QR scanners" }
        val decoder = sessions.getOrPut(id) { QRSDecoder() }
        val progress = try {
            decoder.processFrame(payload)
        } catch (error: Exception) {
            sessions.remove(id)
            throw error
        } ?: return mapOf("text" to text)
        if (progress.error != null) {
            sessions.remove(id)
            error(progress.error)
        }
        val profile = progress.data?.let { decode(unwrap(it), "Profile.bpf") }
        if (profile != null) sessions.remove(id)
        return mapOf("decoded" to progress.decodedBlocks, "total" to progress.totalBlocks, "profile" to profile)
    }

    @Synchronized
    fun close(id: String) { sessions.remove(id) }

    @Synchronized
    fun close() { sessions.clear() }

    private fun unwrap(data: ByteArray): ByteArray {
        if (data.size < 8) return data
        val buffer = ByteBuffer.wrap(data)
        val metadataLength = buffer.int
        if (metadataLength < 0 || metadataLength > data.size - 8) return data
        buffer.position(4 + metadataLength)
        val contentLength = buffer.int
        if (contentLength < 0 || contentLength != buffer.remaining()) return data
        return ByteArray(contentLength).also { buffer.get(it) }
    }
}
