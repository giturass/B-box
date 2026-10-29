package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.CancellationException

/** Bounded to the selected file and the successfully started configuration. Caller serializes access. */
internal class DashboardConfigCache(
    private val revision: (String) -> String,
    private val parseListeners: (String, String) -> List<Map<String, Any?>>,
) {
    data class FileKey(
        val profileId: Long,
        val path: String,
        val generation: Long,
        val modified: Long,
        val size: Long,
        val exists: Boolean,
    )

    class Configuration internal constructor(
        internal val content: String,
        val revision: String,
        private val parseListeners: (String, String) -> List<Map<String, Any?>>,
    ) {
        private var addresses: String? = null
        private var parsed: Result<List<Map<String, Any?>>>? = null

        fun listeners(interfaceAddresses: String): List<Map<String, Any?>> {
            if (addresses != interfaceAddresses || parsed == null) {
                val result: Result<List<Map<String, Any?>>> = try {
                    Result.success(parseListeners(content, interfaceAddresses))
                } catch (error: CancellationException) {
                    throw error
                } catch (error: Exception) {
                    Result.failure(error)
                }
                addresses = interfaceAddresses
                parsed = result
            }
            return parsed!!.getOrThrow()
        }

        internal fun invalidateListeners() {
            parsed = null
        }
    }

    private var savedKey: FileKey? = null
    private var saved: Configuration? = null
    private var activeToken: Any? = null
    private var active: Configuration? = null

    fun saved(key: FileKey, read: () -> String): Configuration {
        if (savedKey != key) {
            // Publish the key only after a successful read; failures must remain retryable.
            val value = configuration(read())
            saved = value
            savedKey = key
        }
        return saved!!
    }

    fun active(token: Any, read: () -> String): Configuration {
        // DashboardConfig is immutable. Identity avoids scanning/hashing its content on every poll.
        if (activeToken !== token) {
            val value = configuration(read())
            active = value
            activeToken = token
        }
        return active!!
    }

    private fun configuration(content: String): Configuration =
        saved?.takeIf { it.content == content } ?: active?.takeIf { it.content == content }
            ?: Configuration(content, revision(content), parseListeners)

    fun clearSaved() {
        savedKey = null
        saved = null
    }

    fun clearActive() {
        activeToken = null
        active = null
    }

    /** Explicit refresh also retries cached parser failures without discarding reusable revisions. */
    fun refresh() {
        savedKey = null
        saved?.invalidateListeners()
        active?.invalidateListeners()
    }
}
