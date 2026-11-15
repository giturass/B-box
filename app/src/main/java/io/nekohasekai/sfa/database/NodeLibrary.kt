package io.nekohasekai.sfa.database

import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.Application
import kotlinx.coroutines.sync.withLock
import java.io.File
import java.util.concurrent.CopyOnWriteArraySet

/**
 * The proxy nodes every profile can use, stored as the document libbox reads and edits.
 * A read that has to match a profile file happens under [ProfileContent.snapshotMutex].
 */
internal object NodeLibrary {
    private val file get() = File(Application.application.filesDir, "nodes/library.json")
    private val callbacks = CopyOnWriteArraySet<() -> Unit>()

    /** Called after the shared nodes were replaced, also by the worker that updates subscriptions. */
    fun registerCallback(callback: () -> Unit) { callbacks += callback }
    fun unregisterCallback(callback: () -> Unit) { callbacks -= callback }

    fun read(): String = snapshot().orEmpty()

    /** The stored document, or null before the first one is written. [restore] returns to it. */
    fun snapshot(): String? = file.takeIf { it.isFile }?.readText()

    fun restore(snapshot: String?) {
        if (snapshot != null) write(snapshot) else check(!file.exists() || file.delete()) { "Unable to restore shared nodes" }
    }

    /** The shared nodes with those of a backup: alone when it replaces them, otherwise in place of the ones it names. */
    fun merge(library: String, backup: String): String = Libbox.mergeUINodes(library, backup).value

    /** The configuration the core checks and runs: the profile with the shared nodes it takes. */
    fun expand(config: String, library: String = read()): String = Libbox.expandUINodes(config, library).value

    fun write(library: String) {
        val target = file
        check(target.parentFile!!.isDirectory || target.parentFile!!.mkdirs()) { "Unable to create node directory" }
        ProfileContent.write(target, library)
    }

    /**
     * Replaces the shared nodes. The selected profile has to stay valid with them, and a running
     * service follows the change or returns to the previous nodes.
     * Caller holds [ProfileContent.mutex] until the save, service application and any recovery finish.
     */
    suspend fun replaceLocked(
        library: String,
        beforeSave: suspend () -> Unit = { ProfileContent.requireStableService() },
        applySelected: suspend (recover: Boolean) -> Unit,
    ) {
        val stored = snapshot()
        val previous = stored.orEmpty()
        if (library == previous) return
        val selected = ProfileManager.get(Settings.selectedProfile)?.let { File(it.typed.path) }
            ?.takeIf { it.isFile }?.readText().orEmpty()
        var apply = false
        if (selected.isNotBlank()) {
            val expanded = expand(selected, library)
            Libbox.checkConfig(expanded)
            apply = expanded != runCatching { expand(selected, previous) }.getOrNull()
        }
        if (apply) beforeSave()
        try {
            ProfileChange.apply(
                save = {
                    ProfileContent.snapshotMutex.withLock {
                        if (apply) beforeSave()
                        write(library)
                    }
                },
                restore = listOf<suspend () -> Unit>({ restore(stored) }),
                restoreBarrier = { work -> ProfileContent.snapshotMutex.withLock { work() } },
                applyService = { recover -> if (apply) applySelected(recover) },
            )
        } finally {
            // Listeners read the nodes that remain, whether the change was kept or taken back.
            callbacks.forEach { it() }
        }
    }
}
