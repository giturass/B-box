package io.nekohasekai.sfa.flutter

import android.app.job.JobInfo
import android.app.job.JobParameters
import android.app.job.JobScheduler
import android.app.job.JobService
import android.content.ComponentName
import android.content.Context
import android.os.Handler
import android.os.Looper
import android.os.PersistableBundle
import android.os.Process
import android.os.SystemClock
import android.util.Log
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlin.coroutines.resume

/** System binding thaws :ui and its bound native host for the existing safe shutdown barrier. */
class UiIdleJobService : JobService() {
    override fun onStartJob(params: JobParameters): Boolean {
        val started = UiProcessLifecycle.current()?.startIdleJob(
            params.extras.getString(TOKEN), params.extras.getLong(DEADLINE),
        ) { jobFinished(params, false) } ?: false
        if (started) Log.i(TAG, "UI idle deadline reached for PID ${Process.myPid()}")
        return started
    }

    override fun onStopJob(params: JobParameters): Boolean {
        UiProcessLifecycle.current()?.stopIdleJob(params.extras.getString(TOKEN), params.extras.getLong(DEADLINE))
        return false
    }

    companion object {
        private const val TAG = "UiIdleJobService"
        private const val JOB_ID = 0x42425549
        private const val TOKEN = "uiIdleToken"
        private const val DEADLINE = "uiIdleDeadline"
        // JobServiceEngine also uses a normal main Handler. An async coroutine Handler
        // could skip its jobFinished message while a ViewRoot sync barrier is present.
        private val completionHandler = Handler(Looper.getMainLooper())

        internal suspend fun awaitCompletionDispatch() = suspendCancellableCoroutine<Unit> { continuation ->
            val callback = Runnable { continuation.resume(Unit) }
            completionHandler.post(callback)
            continuation.invokeOnCancellation { completionHandler.removeCallbacks(callback) }
        }

        internal fun createTrigger(context: Context): UiIdleTrigger {
            val scheduler = checkNotNull(context.getSystemService(JobScheduler::class.java))
            return UiIdleTrigger(
                nowMillis = SystemClock::elapsedRealtime,
                schedule = { token, deadline, delay ->
                    val job = JobInfo.Builder(JOB_ID, ComponentName(context, UiIdleJobService::class.java))
                        .setMinimumLatency(delay)
                        .setOverrideDeadline(delay)
                        .setExtras(PersistableBundle().apply {
                            putString(TOKEN, token)
                            putLong(DEADLINE, deadline)
                        })
                        .build()
                    check(scheduler.schedule(job) == JobScheduler.RESULT_SUCCESS) { "Unable to schedule UI idle shutdown" }
                    Log.i(TAG, "UI idle shutdown scheduled for PID ${Process.myPid()} in ${delay}ms")
                },
                cancel = { scheduler.cancel(JOB_ID) },
            )
        }
    }
}
