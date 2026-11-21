# B-box

基于 [sing-box-for-android](https://github.com/SagerNet/sing-box-for-android) 的非官方 Android 客户端，以 [MiChongs sing-box](https://github.com/MiChongs/sing-box) 为主内核，复用 [Concord](https://github.com/chen08209/concord) 的 MITM、HTTP 改写、QuickJS 脚本和模块实现，界面使用 FlClash 与 Prism 的 Flutter 组件。sing-box、Concord 和 Prism 均通过 fork 的 Git 子模块引入。项目独立维护，不代表上游的官方发布。

下载 APK：[GitHub Releases](https://github.com/giturass/B-box/releases)（Android 7.0+，ARM64）。`newui` 的新界面与功能仍按提交验证；已有 Release 不等同于当前源码。

- Flutter 负责全部页面和导航，界面布局及组件来源见 [FlClash UI 使用说明](flclash.md)；启动器和 Android 分享入口均进入新界面。
- 配置使用 sing-box JSON。代理组、可选节点、测速和路由模式来自 libbox，自动组由内核选择。
- 保留 MiChongs 全部原生功能标签、协议、Provider、分组和实验配置入口；[主内核与构建范围](libbox.md#基线与集成方式)。
- 支持 XHTTP（SplitHTTP）传输和 VLESS Encryption；[配置、来源与验证范围](libbox.md#xhttp-与-vless-encryption)。
- 工具页支持 WARP MASQUE 注册，注册节点直接写入当前所选配置；[注册与协议说明](libbox.md#cloudflare-warp-masque)。
- 保留 VPN、分应用代理、Root/Shizuku 应用查询、远程控制、连接管理和日志。
- 工具中的“连接与请求”支持连接管理、HTTP 抓取、HTTPS MITM、域名筛选、请求详情及 HAR/cURL 导出；[支持范围和限制](libbox.md#httphttps-请求调试)。
- 配置支持 MITM、URL/Header/Body Rewrite、Map Local/API Mock、QuickJS 请求/响应与任务脚本，以及 Surge/Loon/QX/Stash 模块导入、预览和管理；[配置说明与示例](libbox.md#json-配置中的-http-改写)。Concord 负责 HTTP 处理，全部网络出站仍由 sing-box 管理。
- Tailscale、Taildrop、SSH、OpenConnect/OpenVPN 认证、USB/IP 和网络诊断通过新页面使用现有后端；[报告后端及入口范围](app.md#界面替换与-android-桥接)。
- 支持配置和设置备份；B-box 既有包名、数据库及配置保持兼容。
- 应用版本从 `v1.0.1` 独立维护；关于页支持检查更新、应用内下载和由 Android 确认安装，见 [应用更新说明](app.md#应用版本与应用内更新)。
- 旧 Compose 页面、导航、编辑器及旧更新器/静默安装链路已移除；配置订阅更新继续保留。
- Xposed/LSPosed 特权增强及 Android 5/6 支持已移除；[兼容说明](app.md#已移除能力与兼容入口)。

项目约束：[AGENTS.md](AGENTS.md)。核心差异：[libbox.md](libbox.md)。FlClash UI 使用情况及来源：[flclash.md](flclash.md)。Android 应用差异与构建发布：[app.md](app.md)。组件许可证：[flutter/assets/licenses](flutter/assets/licenses)。

检出源码时使用 `git clone --recurse-submodules https://github.com/giturass/B-box`；已有检出执行 `git submodule update --init --recursive`。

## Documentation

- 项目文档站：[B-box 文档](https://b-box-docs.vercel.app)
- 上游文档：[sing-box for Android](https://sing-box.sagernet.org/installation/clients/sfa/)

## 致谢

感谢以下开源项目提供源码、组件和实现参考：

- [SagerNet/sing-box-for-android](https://github.com/SagerNet/sing-box-for-android)：Android 服务、数据与平台后端基础。
- [SagerNet/sing-box](https://github.com/SagerNet/sing-box)：代理核心与 libbox 基础。
- [FlClash](https://github.com/chen08209/FlClash) 及 [prism_ui](https://github.com/chen08209/prism_ui)：Flutter 界面组件来源。
- [MiChongs/sing-box](https://github.com/MiChongs/sing-box)：主内核与原生协议、Provider、分组等功能来源。
- [Concord](https://github.com/chen08209/concord)：MITM、HTTP 改写、QuickJS 脚本及模块实现来源。
- [XTLS/Xray-core](https://github.com/XTLS/Xray-core)：XHTTP 与 VLESS Encryption 的协议实现参考。
- [Throne](https://github.com/throneproj/Throne)：订阅节点提取、出站/端点分类及 WARP MASQUE 注册和传输的行为参考。

感谢 [LINUX DO](https://linux.do) 社区的支持。

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
