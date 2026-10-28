package io.nekohasekai.sfa.bg;

import io.nekohasekai.sfa.bg.ParceledListSlice;

interface IShizukuService {
    void destroy() = 16777114; // Destroy method defined by Shizuku server

    ParceledListSlice getInstalledPackages(int flags, int userId) = 1;

    // Transaction 2 was the retired APK installer; do not reuse it.
}
