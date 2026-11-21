package io.nekohasekai.sfa.flutter.ipc;

import android.os.ParcelFileDescriptor;

interface IBoxUiSession {
    oneway void submit(String channel, in ParcelFileDescriptor message, long requestId);
    oneway void reply(long requestId, in ParcelFileDescriptor message);
    oneway void replyUi(long requestId, in ParcelFileDescriptor message);
    oneway void close();
}
