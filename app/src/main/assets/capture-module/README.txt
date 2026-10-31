B-box 本机调试 CA / Local debugging CA

1. 在 Magisk 或 KernelSU 中安装此 ZIP，然后完整重启设备。
2. 打开 B-box > 工具 > 请求 > CA 证书，点击“检查系统信任”。
3. 在 B-box 中重新生成 CA 后，下载并刷入新模块，然后重启。
4. 移除信任时，在 Magisk / KernelSU 中卸载 B-box HTTP Debug CA 并重启。

此 ZIP 只包含生成它的 B-box 安装的公开 CA。私钥保留在该应用内。
使用证书锁定或独立信任库的应用可能仍不接受该 CA。

Install this ZIP in Magisk or KernelSU and reboot. Check system trust in
B-box > Tools > Requests > CA certificate. After regenerating the CA, export
and install the new module and reboot. Uninstall B-box HTTP Debug CA from
your root manager and reboot to remove its trust.

This ZIP contains only the public CA of the B-box installation that exported
it. Its private key stays in that app. Certificate pinning and separate trust
stores can still prevent individual applications from accepting this CA.
