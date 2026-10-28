package io.nekohasekai.sfa.vendor

import android.content.Context
import androidx.work.CoroutineWorker
import androidx.work.WorkerParameters

// Older installations may still have queued AutoUpdate work.
class UpdateWorker(appContext: Context, params: WorkerParameters) : CoroutineWorker(appContext, params) {
    override suspend fun doWork(): Result = Result.success()
}
