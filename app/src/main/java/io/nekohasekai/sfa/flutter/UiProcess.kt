package io.nekohasekai.sfa.flutter

import android.app.ActivityManager
import android.app.Application
import android.content.Context
import android.os.Build
import android.os.Process
import androidx.core.content.getSystemService
import java.io.File

/** Never infer the process to kill from a package prefix or a possibly stale service PID. */
object UiProcess {
    fun isUiProcess(context: Context): Boolean = isUiProcessName(context.packageName, currentProcessName(context))

    private fun currentProcessName(context: Context): String? {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            runCatching { Application.getProcessName() }.getOrNull()?.let { return it }
        }
        runCatching {
            File("/proc/self/cmdline").readText().substringBefore('\u0000').trim()
        }.getOrNull()?.takeIf { it.isNotEmpty() }?.let { return it }
        return runCatching {
            context.getSystemService<ActivityManager>()?.runningAppProcesses
                ?.firstOrNull { it.pid == Process.myPid() }?.processName
        }.getOrNull()
    }
}
