package io.nekohasekai.sfa.bg

import android.content.Context
import android.util.Log
import androidx.work.BackoffPolicy
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.PeriodicWorkRequest
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import io.nekohasekai.sfa.Application
import io.nekohasekai.sfa.database.ProfileContent
import io.nekohasekai.sfa.database.ProfileManager
import io.nekohasekai.sfa.database.ProfileSubscription
import io.nekohasekai.sfa.database.TypedProfile
import io.nekohasekai.sfa.flutter.BoxNodeSubscriptions
import io.nekohasekai.sfa.utils.HTTPClient
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.sync.withLock
import java.util.Date
import java.util.concurrent.TimeUnit

class UpdateProfileWork {
    companion object {
        private const val WORK_NAME = "UpdateProfile"
        private const val TAG = "UpdateProfileWork"
        private data class Schedule(val interval: Int, val updatedAt: Long)

        suspend fun reconfigureUpdater() {
            runCatching {
                reconfigureUpdater0()
            }.onFailure {
                Log.e(TAG, "reconfigureUpdater", it)
            }
        }

        private suspend fun reconfigureUpdater0() {
            val schedules = ProfileManager.list().flatMap { profile ->
                val legacy = if (profile.typed.type == TypedProfile.Type.Remote && profile.typed.autoUpdate)
                    listOf(Schedule(profile.typed.autoUpdateInterval, profile.typed.lastUpdated.time)) else emptyList()
                legacy + profile.typed.subscriptions.filter { it.autoUpdate }.map { Schedule(it.interval, it.updatedAt) }
            }
            if (schedules.isEmpty()) {
                WorkManager.getInstance(Application.application).cancelUniqueWork(WORK_NAME)
                return
            }

            var minDelay = schedules.minOf { it.interval }.toLong()
            val nowSeconds = System.currentTimeMillis() / 1000L
            val minInitDelay =
                schedules.minOf { (it.interval.toLong() * 60L) - (nowSeconds - (it.updatedAt / 1000L)) }
            if (minDelay < 15) minDelay = 15
            WorkManager.getInstance(Application.application).enqueueUniquePeriodicWork(
                WORK_NAME,
                ExistingPeriodicWorkPolicy.UPDATE,
                PeriodicWorkRequest.Builder(UpdateTask::class.java, minDelay, TimeUnit.MINUTES)
                    .apply {
                        if (minInitDelay > 0) setInitialDelay(minInitDelay, TimeUnit.SECONDS)
                        setBackoffCriteria(BackoffPolicy.LINEAR, 15, TimeUnit.MINUTES)
                    }
                    .build(),
            )
        }

        internal suspend fun updateProfiles(context: Context): Boolean {
            val profiles = ProfileManager.list()
            val remoteProfiles = profiles
                    .filter { it.typed.type == TypedProfile.Type.Remote && it.typed.autoUpdate }
            var success = true
            for (profile in remoteProfiles) {
                val lastSeconds =
                    (System.currentTimeMillis() - profile.typed.lastUpdated.time) / 1000L
                if (lastSeconds < profile.typed.autoUpdateInterval.toLong() * 60L) {
                    continue
                }
                try {
                    val snapshot = ProfileContent.mutex.withLock {
                        val current = ProfileManager.get(profile.id) ?: return@withLock null
                        if (current.typed.type != TypedProfile.Type.Remote || !current.typed.autoUpdate) return@withLock null
                        val age = (System.currentTimeMillis() - current.typed.lastUpdated.time) / 1000L
                        if (age < current.typed.autoUpdateInterval.toLong() * 60L) return@withLock null
                        ProfileSubscription.captureLocked(current)
                    } ?: continue
                    val content = HTTPClient.getString(snapshot.url)
                    ProfileContent.mutex.withLock {
                        val current = snapshot.currentLocked()
                        val service = BackgroundProfileService(context)
                        try {
                            ProfileContent.replaceLocked(current, snapshot.mergedContentLocked(current, content), updatedAt = Date(),
                                beforeSave = service::prepare, applySelected = service::apply)
                        } finally {
                            service.close()
                        }
                    }
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    Log.e(TAG, "update profile ${profile.name}", e)
                    success = false
                }
            }
            for (profile in profiles) {
                for (source in profile.typed.subscriptions.filter { it.autoUpdate }) {
                    val service = BackgroundProfileService(context)
                    val subscriptions = BoxNodeSubscriptions(beforeApply = service::prepare, applySelected = service::apply)
                    try {
                        val snapshot = subscriptions.prepareUpdate(profile.id, source.id)
                        if (!snapshot.source.autoUpdate ||
                            (System.currentTimeMillis() - snapshot.source.updatedAt) / 1000L < snapshot.source.interval.toLong() * 60L) continue
                        val content = subscriptions.downloadUpdate(snapshot)
                        subscriptions.commitUpdate(snapshot, content)
                    } catch (e: CancellationException) {
                        throw e
                    } catch (e: Exception) {
                        Log.e(TAG, "update subscription ${source.name} in profile ${profile.name}", e)
                        success = false
                    } finally {
                        service.close()
                    }
                }
            }
            return success
        }
    }

    class UpdateTask(appContext: Context, params: WorkerParameters) : CoroutineWorker(appContext, params) {
        override suspend fun doWork(): Result = if (updateProfiles(applicationContext)) Result.success() else Result.retry()
    }
}
