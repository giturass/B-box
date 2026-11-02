package io.nekohasekai.sfa.bg

import android.content.pm.PackageInfo

/** Resolve installed code before a target process starts, including Trichrome
 * shared APKs. UID selection matches the kernel filter, including shared UIDs. */
internal fun captureTargetPaths(packages: List<PackageInfo>, caller: Int, selectedUIDs: Set<Int>): List<String> =
    captureTargetPackages(packages, caller, selectedUIDs).keys.toList()

internal fun captureTargetPackages(packages: List<PackageInfo>, caller: Int, selectedUIDs: Set<Int>): Map<String, List<String>> {
    val paths = linkedMapOf<String, MutableSet<String>>()
    fun add(path: String?, packageName: String) {
        // sharedLibraryFiles can contain native sonames such as libOpenCL.so.
        // Send only filesystem paths accepted by the collector; process maps
        // can still locate libraries loaded by soname in their linker namespace.
        if (path == null || !path.startsWith('/') ||
            path.any { it == '\u0000' || it == '\r' || it == '\n' } ||
            path.toByteArray(Charsets.UTF_8).size > 4096) return
        paths.getOrPut(path) { linkedSetOf() }.add(packageName)
    }
    for (pkg in packages) {
        val app = pkg.applicationInfo ?: continue
        if (app.uid == caller || app.uid / 100000 != caller / 100000 ||
            selectedUIDs.isNotEmpty() && app.uid !in selectedUIDs) continue
        add(app.nativeLibraryDir, pkg.packageName)
        add(app.sourceDir, pkg.packageName)
        app.splitSourceDirs?.forEach { add(it, pkg.packageName) }
        app.sharedLibraryFiles?.filter { it.endsWith(".apk") || it.endsWith(".so") }?.forEach { add(it, pkg.packageName) }
    }
    require(paths.size <= 4096) { "Too many capture library paths; select fewer applications" }
    return paths.mapValues { it.value.toList() }
}
