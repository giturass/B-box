package io.nekohasekai.sfa.database

import android.util.AtomicFile
import io.nekohasekai.libbox.Libbox
import kotlinx.coroutines.sync.Mutex
import java.io.File
import java.security.MessageDigest
import java.util.Date

/** Serializes foreground edits with the background subscription updater. */
internal object ProfileContent {
    val mutex = Mutex()

    fun revision(content: String): String = MessageDigest.getInstance("SHA-256")
        .digest(content.toByteArray()).joinToString("") { "%02x".format(it.toInt() and 0xff) }

    fun validate(content: String) {
        require(content.toByteArray().size <= 16 * 1024 * 1024) { "Configuration exceeds 16 MiB" }
        Libbox.checkConfig(content)
    }

    /** Caller holds [mutex] until the save, service application and any recovery finish. */
    suspend fun replaceLocked(
        profile: Profile,
        content: String,
        expectedRevision: String? = null,
        updatedAt: Date? = null,
        applySelected: suspend (recover: Boolean) -> Unit,
    ) {
        val file = File(profile.typed.path)
        val previous = if (file.exists()) file.readText() else null
        if (expectedRevision != null) {
            require(previous != null && revision(previous) == expectedRevision) { "Configuration changed; reload it before saving" }
        }
        validate(content)
        val changed = previous != content
        if (!changed && updatedAt == null) return

        val apply = changed && profile.id == Settings.selectedProfile
        val previousUpdatedAt = Date(profile.typed.lastUpdated.time)
        val previousMode = Settings.serviceMode
        val restore = mutableListOf<suspend () -> Unit>()
        if (changed) restore.add {
            if (previous != null) write(file, previous)
            else check(!file.exists() || file.delete()) { "Unable to restore profile file" }
        }
        if (updatedAt != null) restore.add {
            profile.typed.lastUpdated = previousUpdatedAt
            check(ProfileManager.update(profile) == 1) { "Profile no longer exists" }
        }
        if (apply) restore.add {
            if (Settings.serviceMode != previousMode) Settings.serviceMode = previousMode
            Settings.dataStore.flush()
        }
        ProfileChange.apply(
            save = {
                if (changed) write(file, content)
                if (updatedAt != null) {
                    profile.typed.lastUpdated = updatedAt
                    check(ProfileManager.update(profile) == 1) { "Profile no longer exists" }
                }
            },
            restore = restore,
            applyService = { recover -> if (apply) applySelected(recover) },
        )
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
