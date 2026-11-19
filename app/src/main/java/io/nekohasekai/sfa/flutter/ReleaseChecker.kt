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
    data class Asset(val name: String, val url: String, val cacheKey: String = url, val size: Long? = null)
    data class Release(val url: String, val notes: String, val assets: List<Asset>, val tag: String? = null)
    data class Artifact(val name: String, val sha256: String, val size: Long)
    data class Metadata(val applicationId: String, val versionCode: Long, val version: String, val artifacts: List<Artifact> = emptyList())
    data class Candidate(val release: Release, val metadata: Metadata)
    data class Result(val candidate: Candidate?, val complete: Boolean)
    private data class Cached<T>(val at: Long, val value: T)
    private val mutex = Mutex()
    private val metadataCache = ConcurrentHashMap<String, Cached<Metadata>>()
    private var resultCache: Cached<Result>? = null
    private var confirmedCandidate: Candidate? = null

    suspend fun check(): Result = mutex.withLock {
        resultCache?.takeIf { clock() - it.at < resultCacheMillis }?.let { return@withLock it.value }
        val deadline = clock() + totalTimeoutMillis
        fun remaining() = (deadline - clock()).coerceAtMost(requestTimeoutMillis)
        val releases = attempt(remaining(), loadReleases)?.take(20)
            ?: return@withLock Result(confirmedCandidate, complete = false)
        val inputs = releases.mapNotNull { release ->
            release.assets.singleOrNull { it.name == "B-box-version-metadata.json" }?.let { release to it }
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
        val found = outcomes.filterNotNull().filter { it.metadata.applicationId == applicationId }
            .maxByOrNull { it.metadata.versionCode }
        val complete = inputs.isNotEmpty() && inputs.size == releases.size && outcomes.all { it?.metadata?.applicationId == applicationId }
        val candidate = if (complete) found else listOfNotNull(found, confirmedCandidate).maxByOrNull { it.metadata.versionCode }
        confirmedCandidate = candidate
        val result = Result(candidate, complete = complete)
        if (result.complete) resultCache = Cached(clock(), result)
        result
    }

    companion object {
        /** Metadata from older releases can be shown, but cannot authorize an unverified download. */
        fun artifact(candidate: Candidate, supportedAbis: List<String>): Pair<Asset, Artifact>? = supportedAbis.firstNotNullOfOrNull { abi ->
            val name = "B-box-${candidate.metadata.version}-$abi.apk"
            val assets = candidate.release.assets.filter { it.name == name }
            val artifacts = candidate.metadata.artifacts.filter { it.name == name }
            val asset = assets.singleOrNull() ?: return@firstNotNullOfOrNull null
            val artifact = artifacts.singleOrNull() ?: return@firstNotNullOfOrNull null
            if (artifact.size <= 0L || artifact.size == Long.MAX_VALUE || asset.size != artifact.size ||
                !artifact.sha256.matches(Regex("[0-9a-fA-F]{64}"))) return@firstNotNullOfOrNull null
            asset to artifact
        }
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
