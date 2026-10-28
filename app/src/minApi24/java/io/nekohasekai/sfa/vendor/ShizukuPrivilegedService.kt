package io.nekohasekai.sfa.vendor

import android.content.pm.PackageInfo
import io.nekohasekai.sfa.bg.IShizukuService
import io.nekohasekai.sfa.bg.ParceledListSlice

class ShizukuPrivilegedService : IShizukuService.Stub() {

    override fun destroy() {
        System.exit(0)
    }

    override fun getInstalledPackages(flags: Int, userId: Int): ParceledListSlice<PackageInfo> {
        val allPackages = PrivilegedServiceUtils.getInstalledPackages(flags, userId)
        return ParceledListSlice(allPackages)
    }

}
