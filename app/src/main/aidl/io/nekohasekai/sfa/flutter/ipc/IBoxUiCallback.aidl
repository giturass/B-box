package io.nekohasekai.sfa.flutter.ipc;

import android.os.ParcelFileDescriptor;

interface IBoxUiCallback {
    oneway void onMessage(String channel, in ParcelFileDescriptor message, long requestId);
    oneway void onReply(long requestId, in ParcelFileDescriptor message);
    oneway void onUiRequest(long requestId, in ParcelFileDescriptor message);
    oneway void onClosed(String reason, boolean recoverable);
}
