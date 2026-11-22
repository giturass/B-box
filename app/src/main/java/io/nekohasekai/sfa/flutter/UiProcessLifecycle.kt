package io.nekohasekai.sfa.flutter

import android.annotation.SuppressLint
import android.app.Activity
import android.app.Application
import android.content.Intent
import android.os.Bundle
import android.os.Process
import android.util.Log
import androidx.annotation.MainThread
import androidx.core.app.ActivityCompat
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.withContext
import java.io.Closeable
import java.util.IdentityHashMap
import java.util.UUID

/** Installed only in :ui. Services and native operations live in the application's default process. */
class UiProcessLifecycle private constructor(application: Application, beforeExit: suspend () -> Unit) {
    private val idleTrigger = UiIdleJobService.createTrigger(application)
    private val policy = UiExitPolicy(
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate),
        isUiProcess = { UiProcess.isUiProcess(application) },
        beforeExit = { withContext(NonCancellable) { beforeExit() } },
        exit = { Process.killProcess(Process.myPid()) },
        onFailure = { Log.w("UiProcessLifecycle", "UI shutdown deferred", it) },
        awaitIdle = idleTrigger::awaitIdle,
        awaitExitDispatch = UiIdleJobService::awaitCompletionDispatch,
    )
    private val activityIdentities = IdentityHashMap<Activity, String>()
    private val permissionLeases = IdentityHashMap<Activity, MutableMap<Int, Closeable>>()
    private val forwardingPermissions = IdentityHashMap<Activity, MutableSet<Int>>()

    // The getter is public but restricted by AndroidX. Preserve a previously installed delegate.
    @SuppressLint("RestrictedApi")
    private val previousPermissionDelegate = ActivityCompat.getPermissionCompatDelegate()

    init {
        application.registerActivityLifecycleCallbacks(object : Application.ActivityLifecycleCallbacks {
            override fun onActivityCreated(activity: Activity, savedInstanceState: Bundle?) {
                activityIdentities[activity] = savedInstanceState?.getString(ACTIVITY_IDENTITY)
                    ?: UUID.randomUUID().toString()
            }

            override fun onActivityStarted(activity: Activity) {
                policy.activityStarted(activity, identityFor(activity))
            }

            override fun onActivityStopped(activity: Activity) {
                policy.activityStopped(activity, activityIdentities[activity].takeIf { activity.isChangingConfigurations })
            }

            override fun onActivityDestroyed(activity: Activity) {
                permissionLeases.remove(activity)?.values?.forEach(Closeable::close)
                forwardingPermissions.remove(activity)
                val identity = activityIdentities.remove(activity)
                policy.activityStopped(activity, identity.takeIf { activity.isChangingConfigurations })
            }

            override fun onActivityResumed(activity: Activity) = Unit
            override fun onActivityPaused(activity: Activity) = Unit
            override fun onActivitySaveInstanceState(activity: Activity, outState: Bundle) {
                outState.putString(ACTIVITY_IDENTITY, identityFor(activity))
            }
        })

        ActivityCompat.setPermissionCompatDelegate(object : ActivityCompat.PermissionCompatDelegate {
            override fun requestPermissions(activity: Activity, permissions: Array<out String>, requestCode: Int): Boolean {
                if (forwardingPermissions[activity]?.contains(requestCode) == true) {
                    return previousPermissionDelegate?.requestPermissions(activity, permissions, requestCode) ?: false
                }
                val lease = acquireLease()
                permissionLeases.getOrPut(activity) { mutableMapOf() }.put(requestCode, lease)?.close()
                forwardingPermissions.getOrPut(activity) { mutableSetOf() }.add(requestCode)
                try {
                    // Re-enter once to preserve AndroidX validation/filtering and any earlier delegate.
                    // The outer request is handled, so it cannot launch a second permission dialog.
                    ActivityCompat.requestPermissions(activity, permissions, requestCode)
                    return true
                } catch (error: Exception) {
                    takePermissionLease(activity, requestCode)?.close()
                    throw error
                } finally {
                    forwardingPermissions[activity]?.let { requests ->
                        requests.remove(requestCode)
                        if (requests.isEmpty()) forwardingPermissions.remove(activity)
                    }
                }
            }

            @Suppress("DEPRECATION", "OVERRIDE_DEPRECATION")
            override fun onActivityResult(activity: Activity, requestCode: Int, resultCode: Int, data: Intent?): Boolean =
                previousPermissionDelegate?.onActivityResult(activity, requestCode, resultCode, data) ?: false
        })
    }

    private fun identityFor(activity: Activity): String = activityIdentities.getOrPut(activity) { UUID.randomUUID().toString() }

    /** Hold across an external document, permission or installer result; close in the caller's finally. */
    fun acquireLease(): Closeable = policy.acquireLease()

    internal fun startIdleJob(token: String?, deadlineMillis: Long, finish: () -> Unit): Boolean =
        idleTrigger.start(token, deadlineMillis, finish)

    internal fun stopIdleJob(token: String?, deadlineMillis: Long) = idleTrigger.stop(token, deadlineMillis)

    /** Take before dispatching the permission result, then close after callbacks finish. */
    @MainThread
    fun takePermissionLease(activity: Activity, requestCode: Int): Closeable? =
        permissionLeases[activity]?.let { requests ->
            requests.remove(requestCode).also {
                if (requests.isEmpty()) permissionLeases.remove(activity)
            }
        }

    companion object {
        private const val ACTIVITY_IDENTITY = "bbox.uiActivityIdentity"
        @Volatile private var instance: UiProcessLifecycle? = null

        @MainThread
        fun install(application: Application, beforeExit: suspend () -> Unit): UiProcessLifecycle? {
            if (!UiProcess.isUiProcess(application)) return null
            return instance ?: UiProcessLifecycle(application, beforeExit).also { instance = it }
        }

        fun current(): UiProcessLifecycle? = instance
    }
}
