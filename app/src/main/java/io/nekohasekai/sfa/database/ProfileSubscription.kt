package io.nekohasekai.sfa.database

import io.nekohasekai.libbox.Libbox
import java.io.File

/** Immutable download input. Capture and validate while holding ProfileContent.mutex. */
internal data class ProfileSubscription(
    val id: Long,
    val url: String,
    private val path: String,
    private val revision: String,
    private val updatedAt: Long,
    private val autoUpdate: Boolean,
    private val interval: Int,
) {
    suspend fun currentLocked(): Profile {
        val profile = ProfileManager.get(id) ?: error("Profile was deleted during download")
        require(profile.typed.type == TypedProfile.Type.Remote && profile.typed.remoteURL == url &&
            profile.typed.path == path && profile.typed.lastUpdated.time == updatedAt &&
            profile.typed.autoUpdate == autoUpdate && profile.typed.autoUpdateInterval == interval &&
            ProfileContent.revision(File(path).readText()) == revision) {
            "Profile changed during download; refresh and retry"
        }
        return profile
    }

    /** Use only after [currentLocked] accepts the download, under the same content lock. */
    fun mergedContentLocked(profile: Profile, incoming: String): String {
        require(profile.id == id && profile.typed.path == path) { "Profile changed during download; refresh and retry" }
        return Libbox.mergeUIConfigNodes(File(path).readText(), incoming).value
    }

    companion object {
        fun captureLocked(profile: Profile): ProfileSubscription {
            require(profile.typed.type == TypedProfile.Type.Remote) { "Only remote profiles can be updated" }
            return ProfileSubscription(profile.id, profile.typed.remoteURL, profile.typed.path,
                ProfileContent.revision(File(profile.typed.path).readText()), profile.typed.lastUpdated.time,
                profile.typed.autoUpdate, profile.typed.autoUpdateInterval)
        }
    }
}
