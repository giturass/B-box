package io.nekohasekai.sfa.terminal;

import android.os.ParcelFileDescriptor;
import io.nekohasekai.sfa.terminal.IBoxTerminalSession;

/** The default process owns sessions; a UI owns only these pipes. */
interface IBoxTerminalService {
    IBoxTerminalSession attach(String id, in ParcelFileDescriptor output,
        in ParcelFileDescriptor input, IBinder clientToken);
}
