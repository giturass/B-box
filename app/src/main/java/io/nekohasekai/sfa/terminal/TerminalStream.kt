package io.nekohasekai.sfa.terminal

import java.io.DataInputStream
import java.io.DataOutputStream

/** Private, same-version IPC. Large snapshots and pasted text never enter Binder parcels. */
internal object TerminalStream {
    const val MAGIC = 0x42425453
    const val VERSION = 1
    const val SNAPSHOT = 1
    const val OUTPUT = 2
    const val FINISHED = 3
    const val INPUT = 1
    const val ACKNOWLEDGED = 2

    fun DataOutputStream.writeBytesFrame(data: ByteArray) {
        writeInt(data.size)
        write(data)
    }

    fun DataInputStream.readBytesFrame(): ByteArray {
        val length = readInt()
        require(length >= 0) { "Invalid terminal frame" }
        return ByteArray(length).also(::readFully)
    }
}
