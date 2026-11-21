package io.nekohasekai.sfa.terminal.fixture;

import android.os.ParcelFileDescriptor;

interface ITerminalBackendFixture {
    int getPid();
    void reset();
    void feed(in ParcelFileDescriptor data);
    int inputSize();
    ParcelFileDescriptor takeInput();
    String transcript();
    String screen();
    boolean hasSurface();
    boolean transportClosed();
}
