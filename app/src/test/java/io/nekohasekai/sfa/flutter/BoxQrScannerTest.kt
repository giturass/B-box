package io.nekohasekai.sfa.flutter

import io.nekohasekai.sfa.qrs.QRSDecoder
import java.io.ByteArrayOutputStream
import java.nio.ByteBuffer
import java.nio.ByteOrder
import java.util.Base64
import java.util.zip.CRC32
import java.util.zip.DeflaterOutputStream
import org.junit.Assert.*
import org.junit.Test

class BoxQrScannerTest {
    private fun frame(raw: ByteArray, encoded: ByteArray, slice: Int, vararg indices: Int): ByteArray {
        val total = (encoded.size + slice - 1) / slice
        val data = ByteArray(slice)
        for (index in indices) for (offset in data.indices) {
            val value = encoded.getOrElse(index * slice + offset) { 0 }
            data[offset] = (data[offset].toInt() xor value.toInt()).toByte()
        }
        val crc = CRC32().apply { update(raw) }.value xor total.toLong()
        return ByteBuffer.allocate(16 + indices.size * 4 + slice).order(ByteOrder.LITTLE_ENDIAN).apply {
            putInt(indices.size)
            indices.forEach { putInt(it) }
            putInt(total); putInt(encoded.size); putInt(crc.toInt()); put(data)
        }.array()
    }

    @Test fun decodesOutOfOrderXorFramesAndIgnoresDuplicates() {
        val raw = "abcdefghijklmno".toByteArray()
        val decoder = QRSDecoder()
        val combined = frame(raw, raw, 5, 0, 1)
        assertEquals(0, decoder.processFrame(combined)!!.decodedBlocks)
        assertEquals(0, decoder.processFrame(combined)!!.decodedBlocks)
        assertEquals(1, decoder.processFrame(frame(raw, raw, 5, 2))!!.decodedBlocks)
        assertArrayEquals(raw, decoder.processFrame(frame(raw, raw, 5, 0))!!.data)
    }

    @Test fun subsetReductionDoesNotContinueWithInvalidatedIndices() {
        val raw = "abcdefghijklmno".toByteArray()
        val decoder = QRSDecoder()
        decoder.processFrame(frame(raw, raw, 5, 0, 1))
        assertEquals(1, decoder.processFrame(frame(raw, raw, 5, 0, 1, 2))!!.decodedBlocks)
        assertArrayEquals(raw, decoder.processFrame(frame(raw, raw, 5, 1))!!.data)
    }

    @Test fun importsCompressedUrlFramesWithOfficialFileHeader() {
        val content = "{\"log\":{\"level\":\"trace\"}}".toByteArray()
        val metadata = "{\"name\":\"Profile.bpf\"}".toByteArray()
        val raw = ByteBuffer.allocate(8 + metadata.size + content.size).apply {
            putInt(metadata.size); put(metadata); putInt(content.size); put(content)
        }.array()
        val encoded = ByteArrayOutputStream().also { out -> DeflaterOutputStream(out).use { it.write(raw) } }.toByteArray()
        var imports = 0
        val scanner = BoxQrScanner { data, _ -> imports++; mapOf("content" to data.toString(Charsets.UTF_8)) }
        val total = (encoded.size + 9) / 10
        for (index in (0 until total).reversed()) {
            val value = Base64.getEncoder().encodeToString(frame(raw, encoded, 10, index))
            val result = scanner.scan("camera", "https://example.com/qrs#$value")
            if (index > 0) assertNull(result["profile"])
            else assertEquals(content.toString(Charsets.UTF_8), (result["profile"] as Map<*, *>)["content"])
        }
        assertEquals(1, imports)
    }

    @Test fun rejectsMalformedFramesAndDoesNotMixCameraSessions() {
        val raw = "abcdefghij".toByteArray()
        val decoder = QRSDecoder()
        assertNull(decoder.processFrame(frame(raw, raw, 5, 99)))
        assertNull(decoder.processFrame(frame(raw, raw, 5, 0, 0)))
        decoder.processFrame(frame(raw, raw, 5, 0))
        assertThrows(IllegalArgumentException::class.java) { decoder.processFrame(frame(raw, raw, 6, 1)) }
        val scanner = BoxQrScanner { _, _ -> mapOf("name" to "done") }
        fun encoded(index: Int) = Base64.getEncoder().encodeToString(frame(raw, raw, 5, index))
        scanner.scan("first", encoded(0))
        assertNull(scanner.scan("second", encoded(1))["profile"])
        scanner.close("first")
        assertNull(scanner.scan("first", encoded(1))["profile"])
        assertEquals("done", (scanner.scan("second", encoded(0))["profile"] as Map<*, *>)["name"])
        assertEquals("sing-box://import-remote-profile?url=https://example.com/a", scanner.scan("plain", "sing-box://import-remote-profile?url=https://example.com/a")["text"])
    }
}
