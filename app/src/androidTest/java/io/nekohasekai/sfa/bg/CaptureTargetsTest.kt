package io.nekohasekai.sfa.bg

import android.content.pm.ApplicationInfo
import android.content.pm.PackageInfo
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class CaptureTargetsTest {
    private fun app(name: String, uid: Int) = PackageInfo().apply {
        packageName = name
        applicationInfo = ApplicationInfo().apply {
            this.uid = uid
            sourceDir = "/data/app/$name/base.apk"
            splitSourceDirs = arrayOf("/data/app/$name/split_config.arm64_v8a.apk")
            nativeLibraryDir = "/data/app/$name/lib/arm64"
            sharedLibraryFiles = arrayOf("/data/app/trichrome/base.apk", "/system/framework/framework.jar")
        }
    }

    @Test
    fun installedCodeIncludesSharedAndSplitAPKsWithoutRunningProcesses() {
        val packages = listOf(app("one", 10001), app("two", 10001), app("other", 10002),
            app("bbox", 10003), app("work", 110001))
        val paths = captureTargetPaths(packages, 10003, setOf(10001))
        assertTrue(paths.contains("/data/app/one/base.apk"))
        assertTrue(paths.contains("/data/app/two/lib/arm64"))
        assertTrue(paths.contains("/data/app/one/split_config.arm64_v8a.apk"))
        assertEquals(1, paths.count { it == "/data/app/trichrome/base.apk" })
        assertFalse(paths.any { it.contains("/bbox/") || it.contains("/other/") || it.contains("/work/") || it.endsWith(".jar") })
        val all = captureTargetPaths(packages, 10003, emptySet())
        assertTrue(all.contains("/data/app/other/base.apk"))
        assertFalse(all.any { it.contains("/bbox/") || it.contains("/work/") })
    }

    @Test
    fun nativeSonamesDoNotInvalidateInstalledCaptureTargets() {
        val target = app("one", 10001).apply {
            applicationInfo!!.sharedLibraryFiles = arrayOf(
                "libOpenCL.so", "libGLESv2_adreno.so", "relative/libtls.so",
                "/vendor/lib64/libtls.so", "/data/app/trichrome/base.apk",
                "/system/framework/framework.jar",
            )
        }
        val expected = listOf(
            "/data/app/one/lib/arm64", "/data/app/one/base.apk",
            "/data/app/one/split_config.arm64_v8a.apk",
            "/vendor/lib64/libtls.so", "/data/app/trichrome/base.apk",
        )
        for (selected in listOf(emptySet(), setOf(10001))) {
            assertEquals(expected, captureTargetPaths(listOf(target), 10003, selected))
        }
    }

    @Test
    fun automaticCandidatesRespectCollectorPathBoundaries() {
        val asciiLimit = "/" + "a".repeat(4092) + ".so"
        val utf8Limit = "/" + "é".repeat(2046) + ".so"
        val target = app("one", 10001).apply {
            applicationInfo!!.apply {
                nativeLibraryDir = "lib/arm64"
                splitSourceDirs = arrayOf("", "relative.apk", "/data/app/line\nbreak.apk")
                sharedLibraryFiles = arrayOf(
                    "/vendor/lib64/null\u0000.so", "/vendor/lib64/line\rbreak.so",
                    "/" + "a".repeat(4093) + ".so", "/" + "é".repeat(2047) + ".so",
                    asciiLimit, utf8Limit, "/vendor/lib64/libtls.so",
                )
            }
        }
        assertEquals(
            listOf("/data/app/one/base.apk", asciiLimit, utf8Limit, "/vendor/lib64/libtls.so"),
            captureTargetPaths(listOf(target), 10003, emptySet()),
        )
    }
}
