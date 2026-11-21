package io.nekohasekai.sfa.terminal

/** Protected by ManagedSession's output lock. Entries also bound an unresponsive UI's backlog. */
internal class TerminalReplyLedger(private val capacity: Int = 64) {
    private data class Pending(val replies: List<ByteArray>, var committed: Boolean = false)

    private var sequence = 0L
    private val pending = linkedMapOf<Long, Pending>()

    fun reserve(replies: List<ByteArray>): Long? {
        if (pending.size >= capacity) return null
        val next = ++sequence
        pending[next] = Pending(replies)
        return next
    }

    fun rollback(sequence: Long) {
        pending.remove(sequence)
    }

    /** A late ACK consumes its frame but must not repeat an already sent reply. */
    fun acknowledge(sequence: Long): Boolean {
        if (pending.keys.firstOrNull() != sequence) return false
        return !checkNotNull(pending.remove(sequence)).committed
    }

    /** Keep automatic replies in output order even when UI parsing is delayed. */
    fun commitThrough(sequence: Long): List<ByteArray> {
        if (sequence !in pending) return emptyList()
        return pending.asSequence().takeWhile { it.key <= sequence }.flatMap { (_, entry) ->
            if (entry.committed || entry.replies.isEmpty()) emptySequence()
            else {
                entry.committed = true
                entry.replies.asSequence()
            }
        }.toList()
    }

    fun drain(): List<ByteArray> {
        val replies = pending.values.filterNot { it.committed }.flatMap { it.replies }
        pending.clear()
        return replies
    }
}
