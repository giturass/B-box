package io.nekohasekai.sfa.terminal

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class TerminalReplyLedgerTest {
    private fun bytes(value: String) = value.toByteArray()
    private fun text(values: List<ByteArray>) = values.map { it.toString(Charsets.UTF_8) }

    @Test fun uiDeathReturnsOnlyUncommittedRepliesInOutputOrder() {
        val ledger = TerminalReplyLedger()
        val first = ledger.reserve(listOf(bytes("DA1"), bytes("size")))!!
        ledger.reserve(listOf(bytes("cursor")))
        ledger.reserve(emptyList())
        ledger.reserve(listOf(bytes("version")))

        // The GUI has committed its own replies for the first frame. Killing
        // it must never send the backend's duplicate replies for that frame.
        assertTrue(ledger.acknowledge(first))
        assertEquals(listOf("cursor", "version"), text(ledger.drain()))
        assertTrue(ledger.drain().isEmpty())
    }

    @Test fun duplicateAndOutOfOrderAcknowledgementsCannotDiscardReplies() {
        val ledger = TerminalReplyLedger()
        val first = ledger.reserve(listOf(bytes("first")))!!
        val second = ledger.reserve(listOf(bytes("second")))!!
        assertFalse(ledger.acknowledge(second))
        assertTrue(ledger.acknowledge(first))
        assertFalse(ledger.acknowledge(first))
        assertEquals(listOf("second"), text(ledger.drain()))
    }

    @Test fun aFullOrFailedOutputQueueKeepsCurrentRepliesWithTheBackend() {
        val ledger = TerminalReplyLedger(capacity = 2)
        ledger.reserve(listOf(bytes("pending")))
        val failed = ledger.reserve(listOf(bytes("not delivered")))!!
        assertNull(ledger.reserve(listOf(bytes("queue full"))))
        ledger.rollback(failed)
        assertEquals(listOf("pending"), text(ledger.drain()))
    }

    @Test fun acknowledgedFramesReleaseBacklogCapacity() {
        val ledger = TerminalReplyLedger(capacity = 1)
        val first = ledger.reserve(emptyList())!!
        assertNull(ledger.reserve(emptyList()))
        assertTrue(ledger.acknowledge(first))
        val second = ledger.reserve(listOf(bytes("next")))!!
        assertTrue(second > first)
        assertEquals(listOf("next"), text(ledger.drain()))
    }

    @Test fun timedOutRepliesStayOrderedAndLateAcknowledgementsCannotRepeatThem() {
        val ledger = TerminalReplyLedger()
        val uiOnly = ledger.reserve(emptyList())!!
        val first = ledger.reserve(listOf(bytes("DA1"), bytes("size")))!!
        val second = ledger.reserve(listOf(bytes("cursor")))!!
        ledger.reserve(listOf(bytes("still pending")))

        // A later deadline can run first. It commits all earlier automatic
        // replies, while an empty backend frame still accepts UI-only input.
        assertEquals(listOf("DA1", "size", "cursor"), text(ledger.commitThrough(second)))
        assertTrue(ledger.commitThrough(first).isEmpty())
        assertTrue(ledger.acknowledge(uiOnly))
        assertFalse(ledger.acknowledge(first))
        assertFalse(ledger.acknowledge(second))
        assertEquals(listOf("still pending"), text(ledger.drain()))
    }

    @Test fun aDeadlineKeepsBacklogBoundedUntilTheUiAcknowledgesItsFrame() {
        val ledger = TerminalReplyLedger(capacity = 1)
        val first = ledger.reserve(listOf(bytes("query")))!!
        assertEquals(listOf("query"), text(ledger.commitThrough(first)))
        assertNull(ledger.reserve(emptyList()))
        assertFalse(ledger.acknowledge(first))
        assertTrue(ledger.commitThrough(first).isEmpty())

        val second = ledger.reserve(listOf(bytes("next")))!!
        assertEquals(listOf("next"), text(ledger.commitThrough(second)))
        assertFalse(ledger.acknowledge(second))
        assertTrue(ledger.reserve(emptyList()) != null)
    }
}
