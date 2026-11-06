package io.nekohasekai.sfa.utils

import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.ktx.unwrap
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ensureActive
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

    suspend fun getString(url: String): String = withContext(Dispatchers.IO) {
        // The Go request owns its client/body and enforces the deadline and
        // decoded-byte limit before content crosses the gomobile boundary.
        val content = Libbox.uiProfileGet(url, userAgent).unwrap
        ensureActive()
        content
    }
}
