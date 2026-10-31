package io.nekohasekai.sfa.bg;

import android.os.ParcelFileDescriptor;

interface ICaptureSession {
    ParcelFileDescriptor getFileDescriptor();
    void close();
}

