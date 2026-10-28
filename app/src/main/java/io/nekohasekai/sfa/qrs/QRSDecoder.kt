package io.nekohasekai.sfa.qrs

import java.io.ByteArrayOutputStream
import java.util.zip.Inflater

class QRSDecoder {
    private var codec: LubyCodec? = null
    private var state: LubyCodec.DecodingState? = null
    private val processedHashes = mutableSetOf<Int>()
    private var sliceSize = 0
    private var frameBudget = 0L

    companion object {
        const val MAX_BYTES = 16 * 1024 * 1024
    }

    data class DecodeProgress(
        val decodedBlocks: Int,
        val totalBlocks: Int,
        val isComplete: Boolean,
        val data: ByteArray? = null,
        val error: String? = null,
    ) {
        override fun equals(other: Any?): Boolean {
            if (this === other) return true
            if (javaClass != other?.javaClass) return false
            other as DecodeProgress
            return decodedBlocks == other.decodedBlocks &&
                totalBlocks == other.totalBlocks &&
                isComplete == other.isComplete &&
                data.contentEquals(other.data) &&
                error == other.error
        }

        override fun hashCode(): Int {
            var result = decodedBlocks
            result = 31 * result + totalBlocks
            result = 31 * result + isComplete.hashCode()
            result = 31 * result + (data?.contentHashCode() ?: 0)
            result = 31 * result + (error?.hashCode() ?: 0)
            return result
        }
    }

    @Synchronized
    fun processFrame(payload: ByteArray): DecodeProgress? {
        val block = parsePayload(payload) ?: return null
        if (state != null && state!!.checksum != block.checksum) reset()
        val current = state
        require(current == null || current.totalBlocks == block.totalBlocks && current.compressedSize == block.compressedSize && sliceSize == block.data.size) { "Inconsistent QRS frames" }
        val hash = payload.contentHashCode()
        if (!processedHashes.add(hash)) return progress
        // Subset registrations cost O(degree²); bound them as well as payloads.
        frameBudget += payload.size + block.degree.toLong() * block.degree * 16
        require(frameBudget <= 64L * 1024 * 1024) { "Too many QRS frames" }

        if (codec == null) {
            sliceSize = block.data.size
            codec = LubyCodec(sliceSize = block.data.size)
            state = codec!!.createDecodingState(block)
        }

        val currentState = state!!
        val complete = codec!!.processBlock(currentState, block)

        return if (complete) {
            val assembledData = codec!!.assembleData(currentState)
            val compressedData = assembledData.copyOf(currentState.compressedSize)

            val decompressedData = try {
                decompress(compressedData)
            } catch (e: Exception) {
                null
            }

            if (decompressedData != null) {
                val checksumValid = codec!!.verifyChecksum(
                    decompressedData,
                    currentState.checksum,
                    currentState.totalBlocks,
                )
                if (checksumValid) {
                    return DecodeProgress(
                        currentState.decodedCount,
                        currentState.totalBlocks,
                        true,
                        decompressedData,
                    )
                }
            }

            val rawChecksumValid = codec!!.verifyChecksum(
                compressedData,
                currentState.checksum,
                currentState.totalBlocks,
            )
            if (rawChecksumValid) {
                DecodeProgress(currentState.decodedCount, currentState.totalBlocks, true, compressedData)
            } else {
                DecodeProgress(
                    currentState.decodedCount,
                    currentState.totalBlocks,
                    true,
                    error = "Checksum verification failed",
                )
            }
        } else {
            DecodeProgress(currentState.decodedCount, currentState.totalBlocks, false)
        }
    }

    @Synchronized
    fun reset() {
        codec = null
        state = null
        processedHashes.clear()
        sliceSize = 0
        frameBudget = 0
    }

    val progress: DecodeProgress?
        @Synchronized get() = state?.let {
            DecodeProgress(it.decodedCount, it.totalBlocks, it.decodedCount == it.totalBlocks)
        }

    private fun parsePayload(payload: ByteArray): LubyCodec.EncodedBlock? {
        if (payload.size !in 20..8192) return null

        var offset = 0
        val degree = payload.readIntLE(offset)
        offset += 4

        if (degree !in 1..1000 || payload.size <= 4 + 4 * degree + 12) return null

        val indices = IntArray(degree) {
            val idx = payload.readIntLE(offset)
            offset += 4
            idx
        }

        val totalBlocks = payload.readIntLE(offset)
        offset += 4

        val compressedSize = payload.readIntLE(offset)
        offset += 4

        val checksum = payload.readIntLE(offset).toLong() and 0xFFFFFFFFL
        offset += 4

        if (offset > payload.size) return null

        val data = payload.copyOfRange(offset, payload.size)

        if (totalBlocks !in 1..32768 || compressedSize !in 1..MAX_BYTES ||
            totalBlocks.toLong() * data.size > MAX_BYTES || compressedSize.toLong() > totalBlocks.toLong() * data.size ||
            degree > totalBlocks || indices.any { it !in 0 until totalBlocks } || indices.toSet().size != degree) return null
        return LubyCodec.EncodedBlock(degree, indices, totalBlocks, compressedSize, checksum, data)
    }

    private fun decompress(data: ByteArray): ByteArray {
        val inflater = Inflater()
        try {
            inflater.setInput(data)
            val output = ByteArrayOutputStream()
            val buffer = ByteArray(8192)
            while (!inflater.finished()) {
                val count = inflater.inflate(buffer)
                check(count > 0 || inflater.finished()) { "Incomplete compressed QRS data" }
                require(output.size() + count <= MAX_BYTES) { "QRS data exceeds 16 MiB" }
                output.write(buffer, 0, count)
            }
            return output.toByteArray()
        } finally { inflater.end() }
    }
}
