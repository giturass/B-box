package io.nekohasekai.sfa.utils

import io.nekohasekai.libbox.Libbox
import io.nekohasekai.libbox.UIHTTPTask
import io.nekohasekai.sfa.ktx.unwrap
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.util.Locale

object HTTPClient {
    val userAgent by lazy {
        var userAgent = "SFA (sing-box "
        userAgent += Libbox.version()
        userAgent += "; language "
        userAgent += Locale.getDefault().toLanguageTag().replace("-", "_")
        userAgent += ")"
        userAgent
    }

    suspend fun getString(url: String): String = withTask { it.profileGet(url, userAgent).unwrap }

    suspend fun <T> withTask(block: (UIHTTPTask) -> T): T = coroutineScope {
        val task = Libbox.newUIHTTPTask()
        // Cancellation must reach Go while its blocking JNI call is still in
        // progress. This child closes the task as soon as the parent cancels.
        val cancellation = launch(Dispatchers.Unconfined, start = CoroutineStart.UNDISPATCHED) {
            try { awaitCancellation() } finally { task.close() }
        }
        try {
            withContext(Dispatchers.IO) {
                ensureActive()
                val result = try {
                    block(task)
                } catch (error: Exception) {
                    // The Go call reports cancellation as a bound error. Keep
                    // it a coroutine cancellation instead of failing its parent.
                    ensureActive()
                    throw error
                }
                ensureActive()
                result
            }
        } finally {
            task.close()
            cancellation.cancel()
        }
    }
}
