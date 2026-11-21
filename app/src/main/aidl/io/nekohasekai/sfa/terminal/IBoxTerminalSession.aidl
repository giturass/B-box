package io.nekohasekai.sfa.terminal;

interface IBoxTerminalSession {
    oneway void resize(int columns, int rows, int widthPixels, int heightPixels);
    oneway void setAppearance(boolean dark);
    oneway void detach();
}
