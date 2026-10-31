package io.nekohasekai.sfa.bg

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertThrows
import org.junit.Test
import java.io.ByteArrayInputStream
import java.io.IOException

class CaptureProtocolTest {
    @Test fun startupLineIsBoundedAndLeavesStreamData() {
        val input = ByteArrayInputStream("ready\nnext".toByteArray())
        assertArrayEquals("ready".toByteArray(), readCaptureLine(input))
        assertArrayEquals("next".toByteArray(), input.readBytes())
        assertThrows(IOException::class.java) { readCaptureLine(ByteArrayInputStream(ByteArray(65536) { 65 })) }
        assertThrows(IOException::class.java) { readCaptureLine(ByteArrayInputStream("partial".toByteArray())) }
    }
}

