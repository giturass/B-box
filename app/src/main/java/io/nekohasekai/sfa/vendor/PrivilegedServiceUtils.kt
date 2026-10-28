package io.nekohasekai.sfa.vendor

import android.content.pm.PackageInfo
import android.os.Build
import android.os.IBinder
import java.lang.reflect.InvocationTargetException

object PrivilegedServiceUtils {

    private val iPackageManagerStubClass by lazy { Class.forName("android.content.pm.IPackageManager\$Stub") }
    private val asInterfaceMethod by lazy { iPackageManagerStubClass.getMethod("asInterface", IBinder::class.java) }
    private val iPackageManagerClass by lazy { Class.forName("android.content.pm.IPackageManager") }

    private val getInstalledPackagesMethodLong by lazy {
        iPackageManagerClass.getMethod(
            "getInstalledPackages",
            Long::class.javaPrimitiveType,
            Int::class.javaPrimitiveType,
        )
    }
    private val getInstalledPackagesMethodInt by lazy {
        iPackageManagerClass.getMethod(
            "getInstalledPackages",
            Int::class.javaPrimitiveType,
            Int::class.javaPrimitiveType,
        )
    }
    private val getListMethod by lazy {
        Class.forName("android.content.pm.ParceledListSlice").getMethod("getList")
    }

    private fun getPackageManager(): Any {
        val binder = SystemServiceHelperCompat.getSystemService("package")
            ?: throw IllegalStateException("package service not available")
        return asInterfaceMethod.invoke(null, binder)
            ?: throw IllegalStateException("IPackageManager is null")
    }

    fun getInstalledPackages(flags: Int, userId: Int): List<PackageInfo> {
        val iPackageManager = getPackageManager()
        val result = try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                getInstalledPackagesMethodLong.invoke(iPackageManager, flags.toLong(), userId)
            } else {
                getInstalledPackagesMethodInt.invoke(iPackageManager, flags, userId)
            }
        } catch (e: InvocationTargetException) {
            throw e.cause ?: e
        }
        return extractPackageList(result)
    }

    @Suppress("UNCHECKED_CAST")
    private fun extractPackageList(parceledListSlice: Any?): List<PackageInfo> {
        if (parceledListSlice == null) return emptyList()
        val list = getListMethod.invoke(parceledListSlice) as? List<*>
        return list?.filterIsInstance<PackageInfo>() ?: emptyList()
    }
}
