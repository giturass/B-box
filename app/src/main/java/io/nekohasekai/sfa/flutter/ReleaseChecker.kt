package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.sync.withPermit
import kotlinx.coroutines.withTimeoutOrNull
import java.util.concurrent.ConcurrentHashMap

/** Bounded lookup with per-asset failures. A partial lookup can never mean "up to date". */
internal class ReleaseChecker(
    private val applicationId: String,
    private val loadReleases: suspend (timeoutMillis: Long) -> List<Release>,
    private val loadMetadata: suspend (Asset, timeoutMillis: Long) -> Metadata,
    private val clock: () -> Long = { System.nanoTime() / 1_000_000 },
    private val totalTimeoutMillis: Long = 20_000,
    private val requestTimeoutMillis: Long = 8_000,
    private val parallelism: Int = 3,
    private val resultCacheMillis: Long = 60_000,
    private val metadataCacheMillis: Long = 15 * 60_000,
) {
    data class Asset(val name: String, val url: String, val cacheKey: String = url)
    data class Release(val url: String, val notes: String, val assets: List<Asset>)
    data class Metadata(val applicationId: String, val versionCode: Long, val version: String)
    data class Candidate(val release: Release, val metadata: Metadata)
    data class Result(val candidate: Candidate?, val complete: Boolean)
    private data class Cached<T>(val at: Long, val value: T)
    private val mutex = Mutex()
    private val metadataCache = ConcurrentHashMap<String, Cached<Metadata>>()
    private var resultCache: Cached<Result>? = null

    suspend fun check(): Result = mutex.withLock {
        resultCache?.takeIf { clock() - it.at < resultCacheMillis }?.let { return@withLock it.value }
        val deadline = clock() + totalTimeoutMillis
        fun remaining() = (deadline - clock()).coerceAtMost(requestTimeoutMillis)
        val releases = attempt(remaining(), loadReleases)?.take(20)
            ?: return@withLock Result(resultCache?.value?.candidate, complete = false)
        val inputs = releases.mapNotNull { release ->
            release.assets.firstOrNull { it.name == "B-box-version-metadata.json" }?.let { release to it }
        }
        metadataCache.keys.retainAll(inputs.map { it.second.cacheKey }.toSet())
        val semaphore = Semaphore(parallelism)
        val outcomes = coroutineScope {
            inputs.map { (release, asset) -> async {
                semaphore.withPermit {
                    val cached = metadataCache[asset.cacheKey]?.takeIf { clock() - it.at < metadataCacheMillis }
                    val metadata = cached?.value ?: attempt(remaining()) { timeout -> loadMetadata(asset, timeout) }
                        ?.also { metadataCache[asset.cacheKey] = Cached(clock(), it) }
                    metadata?.let { Candidate(release, it) }
                }
            } }.awaitAll()
        }
        val candidate = outcomes.filterNotNull().filter { it.metadata.applicationId == applicationId }
            .maxByOrNull { it.metadata.versionCode }
        val result = Result(candidate, complete = outcomes.all { it != null })
        if (result.complete) resultCache = Cached(clock(), result)
        result
    }

    private suspend fun <T> attempt(timeout: Long, load: suspend (Long) -> T): T? {
        if (timeout <= 0L) return null
        return try {
            withTimeoutOrNull(timeout) { load(timeout) }
        } catch (error: CancellationException) {
            throw error
        } catch (_: Exception) {
            null
        }
    }
}
