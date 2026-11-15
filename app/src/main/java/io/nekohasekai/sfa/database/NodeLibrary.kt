package io.nekohasekai.sfa.database

import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.Application
import kotlinx.coroutines.sync.withLock
import java.io.File

/**
 * The proxy nodes every profile can use, stored as the document libbox reads and edits.
 * A read that has to match a profile file happens under [ProfileContent.snapshotMutex].
 */
internal object NodeLibrary {
    private val file get() = File(Application.application.filesDir, "nodes/library.json")

    fun read(): String = file.let { if (it.isFile) it.readText() else "" }

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
        val existed = file.isFile
        val previous = read()
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
        ProfileChange.apply(
            save = {
                ProfileContent.snapshotMutex.withLock {
                    if (apply) beforeSave()
                    write(library)
                }
            },
            restore = listOf<suspend () -> Unit>({
                if (existed) write(previous) else check(!file.exists() || file.delete()) { "Unable to restore shared nodes" }
            }),
            restoreBarrier = { work -> ProfileContent.snapshotMutex.withLock { work() } },
            applyService = { recover -> if (apply) applySelected(recover) },
        )
    }
}
