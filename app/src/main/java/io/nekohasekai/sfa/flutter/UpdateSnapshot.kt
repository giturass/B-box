package io.nekohasekai.sfa.flutter

/** A discarded/failed older download must not hide a freshly confirmed newer release. */
internal fun updateSnapshot(checked: Map<String, Any?>, download: UpdateDownloadSession.State, installedVersionCode: Long): Map<String, Any?> {
    val result = checked.toMutableMap()
    result.putAll(download.toWire().filterKeys { it != "version" && it != "versionCode" })
    val checkedCode = (checked["versionCode"] as? Number)?.toLong()
    download.artifact?.takeIf { artifact ->
        download.status in setOf("downloading", "verifying", "ready", "permissionRequired", "installing") || download.canInstall ||
            checkedCode == null || checkedCode == artifact.versionCode
    }?.let { artifact ->
        result.putAll(mapOf(
            "available" to (artifact.versionCode > installedVersionCode), "hasMetadata" to true,
            "version" to artifact.version, "versionCode" to artifact.versionCode,
            "url" to artifact.releaseUrl, "notes" to artifact.notes,
            "downloadUrl" to artifact.url, "assetName" to artifact.name, "size" to artifact.size,
            "canDownload" to (artifact.versionCode > installedVersionCode),
        ))
    }
    return result
}
