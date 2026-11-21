package io.nekohasekai.sfa.flutter.ipc;

import io.nekohasekai.sfa.flutter.ipc.IBoxUiCallback;
import io.nekohasekai.sfa.flutter.ipc.IBoxUiSession;

/** Separate from the stable VPN service Binder interface. */
interface IBoxUiService {
    IBoxUiSession openSession(in IBoxUiCallback callback);
}
