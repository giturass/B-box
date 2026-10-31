# B-box

基于 [SagerNet/sing-box-for-android](https://github.com/SagerNet/sing-box-for-android) 的非官方 Android 客户端，使用 [FlClash v0.9.2](https://github.com/chen08209/FlClash/tree/7fd417fb9a9a277c6a9339a8ac87f907df7c25f7) 的 Flutter 界面组件，后端保持 sing-box。项目独立维护，不代表两个上游的官方发布。

下载 APK：[GitHub Releases](https://github.com/giturass/B-box/releases)（Android 7.0+，ARM64）。`newui` 的新界面与功能仍按提交验证；已有 Release 不等同于当前源码。

- Flutter 负责仪表盘、代理、配置、工具及所有子页面；启动器和 Android 分享入口均进入新界面。
- 配置使用 sing-box JSON。代理组、可选节点、测速和路由模式来自 libbox，自动组由内核选择。
- 保留 VPN、分应用代理、Root/Shizuku 应用查询、远程控制、连接管理和日志。
- 工具中的“请求”支持本地 HTTP/HTTPS 调试、域名筛选、请求详情及 HAR/cURL 导出，可为本机独立 CA 下载 Magisk/KernelSU 刷入模块；[支持范围和限制](libbox.md#httphttps-请求调试)。
- Tailscale、Taildrop、SSH、OpenConnect/OpenVPN 认证、USB/IP、网络诊断和报告通过新页面使用现有后端。
- 支持配置和设置备份；B-box 既有包名、数据库及配置保持兼容。
- 旧 Compose 页面、导航、编辑器及更新下载/静默安装链路已移除。关于页使用独立的 B-box Release 检查；配置订阅更新继续保留。
- Xposed/LSPosed 和 Android 5/6 支持已移除。

项目约束：[AGENTS.md](AGENTS.md)。核心差异：[libbox.md](libbox.md)。FlClash UI 使用情况及来源：[flclash.md](flclash.md)。Android 应用差异与构建发布：[app.md](app.md)。组件许可证：[flutter/assets/licenses](flutter/assets/licenses)。

## Documentation

https://sing-box.sagernet.org/installation/clients/sfa/

## License

```
Copyright (C) 2022 by nekohasekai <contact-sagernet@sekai.icu>

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.

In addition, no derivative work may use the name or imply association
with this application without prior consent.
```

Under the license, that forks of the app are not allowed to be listed on F-Droid or other app stores
under the original name.
