package io.nekohasekai.sfa.database

import android.util.AtomicFile
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.bg.BoxService
import io.nekohasekai.sfa.constant.Status
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import java.io.File
import java.io.OutputStream
import java.security.MessageDigest
import java.util.Date
import java.util.concurrent.atomic.AtomicLong

/** Serializes foreground edits with the background subscription updater. */
internal object ProfileContent {
    val mutex = Mutex()
    // Protect only persistence and startup reads. Never hold this while waiting for a restart.
    val snapshotMutex = Mutex()
    private val writeGeneration = AtomicLong()
    // File timestamps/length alone can miss equal-size writes and recovery within one clock tick.
    val generation: Long get() = writeGeneration.get()

    fun revision(content: String): String = MessageDigest.getInstance("SHA-256")
        .digest(content.toByteArray()).joinToString("") { "%02x".format(it.toInt() and 0xff) }

    fun validate(content: String) {
        require(content.toByteArray().size <= 16 * 1024 * 1024) { "Configuration exceeds 16 MiB" }
        Libbox.checkConfig(content)
    }

    fun requireStableService(status: Status = BoxService.currentStatus) {
        require(status in setOf(Status.Stopped, Status.Started) &&
            BoxService.currentStatus in setOf(Status.Stopped, Status.Started)) {
            "Wait for the service to finish starting or stopping"
        }
    }

    /** Caller holds [mutex] until the save, service application and any recovery finish. */
    suspend fun replaceLocked(
        profile: Profile,
        content: String,
        expectedRevision: String? = null,
        updatedAt: Date? = null,
        beforeSave: suspend () -> Unit = { requireStableService() },
        applySelected: suspend (recover: Boolean) -> Unit,
    ) {
        val file = File(profile.typed.path)
        val previous = if (file.exists()) file.readText() else null
        if (expectedRevision != null) {
            require(previous != null && revision(previous) == expectedRevision) { "Configuration changed; reload it before saving" }
        }
        val cleaned = Libbox.removeUIRetiredCaptureOptions(content)
        validate(cleaned)
        val changed = previous != cleaned
        if (!changed && updatedAt == null) return
        val savedAt = updatedAt ?: if (changed && profile.typed.type == TypedProfile.Type.Local) Date() else null

        val apply = changed && profile.id == Settings.selectedProfile
        if (apply) beforeSave()
        val previousUpdatedAt = Date(profile.typed.lastUpdated.time)
        val previousMode = Settings.serviceMode
        val restore = mutableListOf<suspend () -> Unit>()
        if (changed) restore.add {
            if (previous != null) write(file, previous)
            else check(!file.exists() || file.delete()) { "Unable to restore profile file" }
        }
        if (savedAt != null) restore.add {
            profile.typed.lastUpdated = previousUpdatedAt
            check(ProfileManager.update(profile) == 1) { "Profile no longer exists" }
        }
        if (apply) restore.add {
            if (Settings.serviceMode != previousMode) Settings.saveServiceMode(previousMode)
        }
        ProfileChange.apply(
            save = {
                snapshotMutex.withLock {
                    if (apply) beforeSave()
                    if (changed) write(file, cleaned)
                    if (savedAt != null) {
                        profile.typed.lastUpdated = savedAt
                        check(ProfileManager.update(profile) == 1) { "Profile no longer exists" }
                    }
                }
            },
            restore = restore,
            restoreBarrier = { work -> snapshotMutex.withLock { work() } },
            applyService = { recover -> if (apply) applySelected(recover) },
        )
    }

    fun write(file: File, content: String) = write(file) { it.write(content.toByteArray()) }

    fun write(file: File, writeContent: (OutputStream) -> Unit) {
        val atomic = AtomicFile(file)
        try {
            val stream = atomic.startWrite()
            try {
                writeContent(stream)
                atomic.finishWrite(stream)
            } catch (e: Exception) {
                atomic.failWrite(stream)
                throw e
            }
        } finally {
            writeGeneration.incrementAndGet()
        }
    }
}
