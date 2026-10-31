package io.nekohasekai.sfa.vendor

import android.content.pm.PackageManager
import rikka.shizuku.Shizuku

object ShizukuAccess {

    private const val SHIZUKU_PERMISSION_REQUEST_CODE = 1001

    fun isAvailable(): Boolean = try {
        Shizuku.pingBinder()
    } catch (e: Exception) {
        false
    }

    fun checkPermission(): Boolean = try {
        if (Shizuku.isPreV11()) {
            false
        } else {
            Shizuku.checkSelfPermission() == PackageManager.PERMISSION_GRANTED
        }
    } catch (e: Exception) {
        false
    }

    fun requestPermission() {
        if (!Shizuku.isPreV11()) {
            Shizuku.requestPermission(SHIZUKU_PERMISSION_REQUEST_CODE)
        }
    }

}
