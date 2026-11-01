package io.nekohasekai.sfa.bg

import android.content.pm.PackageInfo

/** Resolve installed code before a target process starts, including Trichrome
 * shared APKs. UID selection matches the kernel filter, including shared UIDs. */
internal fun captureTargetPaths(packages: List<PackageInfo>, caller: Int, selectedUIDs: Set<Int>): List<String> {
    val paths = linkedSetOf<String>()
    fun add(path: String?) {
        if (!path.isNullOrBlank()) paths.add(path)
    }
    for (pkg in packages) {
        val app = pkg.applicationInfo ?: continue
        if (app.uid == caller || app.uid / 100000 != caller / 100000 ||
            selectedUIDs.isNotEmpty() && app.uid !in selectedUIDs) continue
        add(app.nativeLibraryDir)
        add(app.sourceDir)
        app.splitSourceDirs?.forEach(::add)
        app.sharedLibraryFiles?.filter { it.endsWith(".apk") || it.endsWith(".so") }?.forEach(::add)
    }
    require(paths.size <= 4096) { "Too many capture library paths; select fewer applications" }
    return paths.toList()
}
