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
import io.nekohasekai.sfa.flutter.BoxNodes
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.util.concurrent.TimeUnit

/** Reads the node subscriptions that update by themselves, as [UpdateProfileWork] does for remote profiles. */
class UpdateNodeWork {
    companion object {
        private const val WORK_NAME = "UpdateNodes"
        private const val TAG = "UpdateNodeWork"

        suspend fun reconfigureUpdater() {
            runCatching {
                withContext(Dispatchers.IO) { reconfigureUpdater0() }
            }.onFailure {
                Log.e(TAG, "reconfigureUpdater", it)
            }
        }

        private fun reconfigureUpdater0() {
            val subscriptions = BoxNodes.subscriptions().filter { it.autoUpdate }
            val manager = WorkManager.getInstance(Application.application)
            if (subscriptions.isEmpty()) {
                manager.cancelUniqueWork(WORK_NAME)
                return
            }
            val interval = subscriptions.minOf { it.interval }.coerceAtLeast(15).toLong()
            val now = System.currentTimeMillis()
            val initialDelay = subscriptions.minOf { it.interval * 60L - (now - it.updated) / 1000L }
            manager.enqueueUniquePeriodicWork(
                WORK_NAME,
                ExistingPeriodicWorkPolicy.UPDATE,
                PeriodicWorkRequest.Builder(UpdateTask::class.java, interval, TimeUnit.MINUTES)
                    .apply {
                        if (initialDelay > 0) setInitialDelay(initialDelay, TimeUnit.SECONDS)
                        setBackoffCriteria(BackoffPolicy.LINEAR, 15, TimeUnit.MINUTES)
                    }
                    .build(),
            )
        }

        internal suspend fun updateNodes(context: Context): Boolean {
            val listed = runCatching { BoxNodes.subscriptions() }.getOrElse {
                // Shared nodes that cannot be read are reported where they are used; another attempt would not read them either.
                Log.e(TAG, "read subscriptions", it)
                return true
            }
            var success = true
            val now = System.currentTimeMillis()
            for (due in listed.filter { it.due(now) }) {
                val service = BackgroundProfileService(context)
                try {
                    val nodes = BoxNodes(beforeApply = service::prepare, applySelected = service::apply)
                    // Read again under the lock: it may have been edited or updated since it was listed.
                    val subscription = nodes.subscription(due.id)
                    if (!subscription.due(System.currentTimeMillis())) continue
                    nodes.commit(subscription, nodes.fetch(subscription), refresh = true)
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    Log.e(TAG, "update nodes ${due.name}", e)
                    success = false
                } finally {
                    service.close()
                }
            }
            return success
        }
    }

    class UpdateTask(appContext: Context, params: WorkerParameters) : CoroutineWorker(appContext, params) {
        override suspend fun doWork(): Result = if (updateNodes(applicationContext)) Result.success() else Result.retry()
    }
}
