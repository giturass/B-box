# B-box 项目约束

本文件适用于整个仓库，记录开发和维护约束。项目以 sing-box-for-android 为 Android 后端，以 FlClash 的 Flutter 组件构建界面；三个上游边界分别维护，不能将界面参考版本当作核心或应用的比较基线。

## 文档职责

| 文档 | 内容 |
| --- | --- |
| [README.md](README.md) | 项目介绍、下载入口和许可声明 |
| [AGENTS.md](AGENTS.md) | 项目约束、工作方式和验证要求 |
| [libbox.md](libbox.md) | 相对上游 sing-box/libbox 的补丁、扩展绑定及核心构建差异 |
| [flclash.md](flclash.md) | FlClash UI 的参考版本、组件使用、适配范围及视觉验证 |
| [app.md](app.md) | 相对 sing-box-for-android 的应用差异、兼容性及构建发布 |

开始任务先阅读适用的上级 `AGENTS.md`、本文件和所涉及领域的文档。修改跨越多个领域时同步更新对应文档；同一事实由所属文档维护，其他文档使用链接引用。文档描述当前源码，撤销或替代的实现不再列为当前要求；历史细节通过 Git 查询。

## 工作环境与改动范围

- 本机是 Android Termux，使用支持的命令和工具，例如 `pkg`；不要依赖 `systemctl` 等完整 Linux 系统服务。
- 临时文件统一放在 `$HOME/tmp`，同时遵守当前会话的文件系统权限；不要把本机 SDK、工具链路径或签名配置写入仓库和 CI。
- 未得到用户明确要求，不在本机执行编译构建。Go 测试、Flutter 分析/测试/AAR 构建、Android 编译和设备测试使用 GitHub Actions。文档、源码和差异的静态检查可在本机执行。
- 开始前检查工作树，保留用户未提交的改动。上游同步固定比较 SHA，区分已提交差异和工作树差异，核实远端地址后再按任务授权推送。
- 接口、构建方式和兼容性判断应有当前源码或对应版本的官方文档依据，不凭空增加接口，不将旧验证结果当作当前版本结果。
- 不提交密钥、密码、`local.properties`、`.local-signing/`、`.core/`、SDK、AAR 或生成的 APK。发布沿用既有签名密钥，不重新生成替代密钥。

## 架构与兼容性约束

1. Flutter 负责全部应用页面和导航。`compose.MainActivity` 与 `compose.ShareActivity` 仅保留稳定组件名，进入 `BoxActivity`；不要恢复旧 Compose 页面或导航作为备用入口。原生状态模型在 `backend/`，平台能力通过 `flutter/` 下的 Android 桥接提供。
2. Room 与 libbox 是配置、服务和会话数据的来源。配置为 sing-box JSON，保存前经过 `Libbox.checkConfig`；不能静默转换 Clash YAML，不能引入 Mihomo 核心、提供者、覆写脚本或不受 sing-box 支持的开关。
3. 模式、代理组、可选节点和延迟来自当前内核；自动组不能手动选择。保留本地/远程会话区分，不把本机配置、出口 IP 或演示数据当作远端状态。
4. 保留 Android VPN 权限、前台服务和服务状态确认流程。配置及备份应用须保留修订冲突检查、原子写入和失败恢复；不能在需要重启服务时把发出启动请求视为启动成功。
5. B-box 的安装身份为 `io.ericlee.bfa`，源码 namespace、类路径及 AIDL 描述符保留 `io.nekohasekai.sfa`。任务未涉及这些约定时保持兼容；不要重排或复用已退役的 Binder 方法编号，也不要清空无关设置和数据库。
6. 保留完整 sing-box 协议和 Tailscale/Taildrop、终端、普通 Root/Shizuku 能力。历史核心裁剪与 Tailscale/终端移除已回退；Android 5/6、legacy AAR、Xposed/LSPosed 和旧更新下载/静默安装链路则已移除。具体范围见 [libbox.md](libbox.md) 和 [app.md](app.md)。
7. 复用 FlClash 组件时保留来源和许可，按 [flclash-manifest.json](flutter/flclash-manifest.json) 区分直接导入与业务适配。共享源码复用、编译成功或预览声明都不能代替视觉和实机验收。

## 上游同步与验证

- Android 上游比较基线维护在 [app.md](app.md)，核心标签由 [version.properties](version.properties) 决定，FlClash 参考提交维护在 [flclash.md](flclash.md) 和来源清单。更新其中一个上游时，不自动改变另两个基线。
- 更新核心时核对 legacy 补丁、所有 `libbox/ui_*.go` 扩展及生成的绑定，并同步检查 Release、Flutter 验证和 Android smoke 工作流。官方原版 AAR 缺少 B-box 新增绑定，不能仅凭版本相同直接替换。
- 更新 FlClash 时检查源文件及哈希、适配差异、字体/着色器和许可。改动直接导入文件后，应如实更新来源清单及改动说明，不把业务改动伪装为机械包名替换。
- 按受影响范围验证。本机可运行 `git diff --check` 和 `python3 tools/verify_newui.py`；文档变更还应检查链接和旧引用。后者仅检查源码约束，不证明运行行为。
- [.github/workflows/newui.yml](.github/workflows/newui.yml) 负责 Go 扩展测试、Flutter 分析/交互/布局测试及 AAR 构建；[newui-android.yml](.github/workflows/newui-android.yml) 负责 JVM、Android 编译和 API 24/31/35 smoke；[build-release.yml](.github/workflows/build-release.yml) 负责签名 ARM64 Release 和产物校验。
- 验证记录应说明源码 SHA、实际运行的检查、Actions 链接及未覆盖范围。已有 Release、旧提交的成功构建或测试代码的存在，都不等于当前源码已通过验收。仅构建下载产物时注意 Release 工作流的 `publish_release` 默认开启，具体发布规则见 [app.md](app.md)。
