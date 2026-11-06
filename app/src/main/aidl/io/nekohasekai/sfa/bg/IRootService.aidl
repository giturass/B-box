package io.nekohasekai.sfa.bg;

import android.os.ParcelFileDescriptor;
import io.nekohasekai.sfa.bg.IAutoRedirectHandler;
import io.nekohasekai.sfa.bg.IAutoRedirectSession;
import io.nekohasekai.sfa.bg.IBridgeSession;
import io.nekohasekai.sfa.bg.INeighborTableCallback;
import io.nekohasekai.sfa.bg.IRootShellSession;
import io.nekohasekai.sfa.bg.ParceledListSlice;

interface IRootService {
    void destroy() = 16777114; // Destroy method defined by Shizuku server

    ParceledListSlice getInstalledPackages(int flags, int userId) = 1;

    // Transactions 2 and 3 are retired; do not reuse them.

    void registerNeighborTableCallback(in INeighborTableCallback callback) = 4;

    oneway void unregisterNeighborTableCallback(in INeighborTableCallback callback) = 5;

    IRootShellSession openShellSession(String user, String command, in String[] env, String term, int rows, int cols) = 6;

    String lookupSFTPServer() = 7;

    IBridgeSession openBridge(String bridgeName, int mtu, String inet4Port, String inet6Port, int ruleIndex, int routeTable) = 8;

    IAutoRedirectSession startAutoRedirect(in byte[] options, IAutoRedirectHandler handler) = 9;

    // Transaction 10 (retired eBPF capture) must not be reused.

    void exportHookDebugInfo(in ParcelFileDescriptor output) = 11;
}
