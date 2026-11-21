package io.nekohasekai.sfa.terminal.fixture;

import android.os.ParcelFileDescriptor;

interface ITerminalRendererFixture {
    int getPid();
    void attach();
    void sendInput(in ParcelFileDescriptor data);
    String transcript();
    String screen();
    void detach();
    oneway void holdMain();
    boolean mainHeld();
    void releaseMain();
    oneway void killProcess();
    void attachWithInterruptedConnection(boolean bindingDied);
}
