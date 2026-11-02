# 与 sing-box-for-android 的差异

本文记录 Android 应用相对上游的当前差异。项目约束见 [AGENTS.md](AGENTS.md)，核心补丁见 [libbox.md](libbox.md)，Flutter 界面的来源及使用情况见 [flclash.md](flclash.md)。

## 比较基线与应用身份

| 项目 | 当前值 |
| --- | --- |
| 仓库 | [giturass/B-box](https://github.com/giturass/B-box) |
| Android 上游 | [SagerNet/sing-box-for-android](https://github.com/SagerNet/sing-box-for-android)，`dev` 分支 |
| 已合并的比较基线 | [`a548a7903e96266a0046018562c73b192edbfc18`](https://github.com/SagerNet/sing-box-for-android/commit/a548a7903e96266a0046018562c73b192edbfc18)，`1.15.0-alpha.13` |
| B-box 版本 / versionCode | `1.15.0-alpha.13` / `748`，以 [version.properties](version.properties) 为准 |
| 应用名 / Gradle 根项目名 / APK 前缀 | `B-box` |
| 安装包名 / applicationId | `io.ericlee.bfa` |
| 源码 namespace / 包路径 | `io.nekohasekai.sfa` |
| Android / flavor | API 24（Android 7.0）起，`play` / `other` |

应用基线与核心标签、FlClash 参考提交分别维护；B-box 的 versionCode 不必等于上游基线的值。以上描述当前源码，不表示已有 Release 包含全部改动。

品牌、安装身份及组件约定：

- 应用标签、Gradle 根项目名和 APK 前缀统一使用 B-box。图标原图为 [artwork/bbox.png](artwork/bbox.png)，[GenerateLauncherIcons.java](tools/GenerateLauncherIcons.java) 生成普通/圆形、自适应、主题单色及通知图标。保留上游许可和非官方分支说明，不改写真实来源名称。
- namespace、Kotlin/Java 类路径、AIDL 描述符及 RootServer 反射类名继续使用 `io.nekohasekai.sfa`。Provider authorities 使用 `${applicationId}`，服务控制和 USB/Taildrop 等广播使用 `BuildConfig.APPLICATION_ID` 或运行时包名。
- 可与原版 SFA 并行安装，私有配置和数据相互独立；原版数据需导出/导入。Flutter 迁移沿用 B-box 既有安装身份、数据库及配置，不以换 UI 为由清空数据。

## 界面替换与 Android 桥接

上游 Compose 页面、导航、编辑器、CameraX/MLKit 原生扫码及相关依赖和无引用资源已移除，所有页面由 Flutter 提供。`compose/` 只保留 [MainActivity.kt](app/src/main/java/io/nekohasekai/sfa/compose/MainActivity.kt) 和 [ShareActivity.kt](app/src/main/java/io/nekohasekai/sfa/compose/ShareActivity.kt) 两个稳定 Android 组件名，均继承 `BoxActivity`。

下表的原生路径以 `app/src/main/java/io/nekohasekai/sfa/` 为前缀。

| 区域 | 路径 | 当前职责 |
| --- | --- | --- |
| Flutter 宿主 | `flutter/BoxActivity.kt` | 启动器/分享入口、文档选择、权限、服务状态及配置应用 |
| 通道与命令 | `flutter/BoxBridge.kt` | `io.ericlee.bfa/box` MethodChannel 与 `io.ericlee.bfa/events` EventChannel，对接配置、工具和设置 |
| 原生状态 | `backend/` | 仪表盘、分组、连接、日志、Tailscale/Taildrop、诊断和认证状态模型 |
| 平台视图 | `flutter/BoxBrowserFactory.kt`、`flutter/BoxTerminalFactory.kt` | OpenConnect WebView 与 Ghostty 终端嵌入 Flutter，不回到旧 Compose 对话框 |
| 服务与数据 | `bg/`、`database/`、`utils/` | 保留 sing-box 服务、Room、订阅更新及本地/远程命令目标 |
| 应用查询 | [vendor/](app/src/main/java/io/nekohasekai/sfa/vendor/) | 普通、Root/Shizuku 应用查询和托管列表扫描；API 24+ 实现及依赖由 `play` / `other` 共用 |

Flutter 配置是 sing-box JSON，分组、可选节点、延迟和模式由 libbox 提供；自动组不可手动选择。VPN 授权、前台服务、连接管理、日志、流量、本地/远程会话及路由保持原有能力边界。具体布局、语言、卡片与工具入口见 [flclash.md](flclash.md)。

Tailscale 端点/节点/出口节点/Ping、Taildrop 收发与分享、SSH/Root/Shizuku 终端、Ghostty 设置和导入字体、OpenConnect/OpenVPN 认证、USB/IP、网络诊断、崩溃/OOM/电量报告均通过新页面使用现有后端。历史 MOD-014 的功能移除已回退，不能按旧裁剪记录删除这些能力。

## 配置、服务应用与备份

[BoxProfiles.kt](app/src/main/java/io/nekohasekai/sfa/flutter/BoxProfiles.kt) 负责导入、编辑、手动订阅更新、配置切换及局域网代理保存；[ProfileContent.kt](app/src/main/java/io/nekohasekai/sfa/database/ProfileContent.kt) 的互斥锁和内容替换流程由前台编辑和后台订阅更新共用。

- 保存前检查内容修订和完整 `Libbox.checkConfig`，以原子写入替换内容。高级配置编辑原生 DNS、路由、规则集、入站、出站、端点和日志；原始 JSON 编辑保留配置元数据及订阅 URL。
- 本地配置创建时在 Room 记录时间，原始/高级编辑及局域网入站变更成功保存内容时更新 `lastUpdated`；无内容变化和仅编辑名称等元数据不刷新时间。保存或应用失败与内容一起恢复旧时间。远程配置的该字段仍表示订阅更新时间，手动 JSON 编辑不覆盖它；没有时间记录的旧配置保持未知。界面展示见 [flclash.md](flclash.md#配置页与内核差异)。
- 运行中修改选中配置需应用到服务：模式相同时重载，模式变化时停止并重新走权限/启动流程。需要重启时通过 [ServiceStartResult.kt](app/src/main/java/io/nekohasekai/sfa/flutter/ServiceStartResult.kt) 等待 Started 回调；失败或超时恢复旧文件/设置并尝试恢复原服务。停服时普通保存不会自动启动 VPN。
- 原始/高级 JSON 编辑、手动/自动订阅更新和配置切换共用 [ProfileChange.kt](app/src/main/java/io/nekohasekai/sfa/database/ProfileChange.kt) 的失败恢复流程。订阅保存或应用失败恢复旧内容和更新时间；切换失败恢复原选择及服务模式。恢复在不可取消的协程上下文完成，保留原错误及恢复错误；持久数据恢复失败时不启动未恢复的配置。
- 配置切换在配置互斥区内、修改选中配置前读取最新服务状态；启动中或停止中拒绝切换，避免界面状态延迟导致启动配置与保存的选择不一致。界面禁用行为见 [flclash.md](flclash.md#配置页与内核差异)。
- [UpdateProfileWork.kt](app/src/main/java/io/nekohasekai/sfa/bg/UpdateProfileWork.kt) 在同一互斥区内完成每个订阅的保存、应用和恢复，应用失败请求重试。[BackgroundProfileService.kt](app/src/main/java/io/nekohasekai/sfa/bg/BackgroundProfileService.kt) 通过本机服务连接确认状态，模式相同重载，模式变化及失败恢复时等待停止和 Started 回调；后台重启所需 VPN/通知权限未授予时更新失败并回滚。更新未选中配置或停服时不启动服务，远程会话不接收本机订阅的重载。
- 常规服务页的 VPN 绕过、自动重定向和动态通知开关保存后，运行中的本地服务自动停止并重新走权限/启动流程，确认 Started 后完成操作。[ServiceSettingsChange.kt](app/src/main/java/io/nekohasekai/sfa/flutter/ServiceSettingsChange.kt) 在保存或重启失败时恢复原设置，重启失败时再尝试恢复原服务；停止状态只保存设置，远程会话不触发本机或远端重启。
- Root 自动重定向的系统 BPF map 兼容范围和核心补丁见 [libbox.md](libbox.md#android-自动重定向兼容)。Android 桥接继续沿用上游的启动、错误传递及 Root 服务死亡清理流程。
- 局域网代理使用 libbox 原生入站变换，不引入全局 Clash 端口设置。运行时端点来自已成功启动的配置快照，避免将保存失败或未应用的配置显示为正在监听。
- [DashboardConfigCache.kt](app/src/main/java/io/nekohasekai/sfa/flutter/DashboardConfigCache.kt) 仅缓存选中配置和已启动配置的内容、修订及最近一组接口地址的 LAN 解析结果，相同内容共用缓存。前台两秒轮询保留网络/热点检测，以配置 ID、路径、文件时间/大小及写入代次检查保存配置；未变化时不重读内容、计算哈希或调用 LAN 解析。地址变化复用内容和修订，重新调用 LAN 解析；运行配置按成功启动快照更新。普通写入及备份恢复/回滚共用 `ProfileContent.write` 推进代次，避免同大小、同时间戳的修改漏刷。解析错误也缓存，配置/地址变化或手动刷新时重试，取消不缓存。
- 二进制 `.bpf` 导入保留订阅 URL、更新间隔、自动更新及时间戳。动态 QRS 通过 [BoxQrScanner.kt](app/src/main/java/io/nekohasekai/sfa/flutter/BoxQrScanner.kt) 按相机会话收集有界帧、校验 CRC，并支持原压缩文件封装。

[BoxBackups.kt](app/src/main/java/io/nekohasekai/sfa/flutter/BoxBackups.kt) 提供 `bbox-1` 格式，包含稳定配置身份、界面/应用设置、终端设置与导入字体，以及可选 WebDAV 设置。仅恢复配置时不改其他设置。恢复先暂存文件，再使用 Room 事务；应用失败回滚文件、数据库和设置。兼容策略保留其他配置，覆盖策略在用户确认后删除备份中不存在的配置。

界面偏好的读取、保存及备份恢复共用 [BoxPreferences.kt](app/src/main/java/io/nekohasekai/sfa/flutter/BoxPreferences.kt) 校验，卡片布局接受 `System HTTP proxy` 键。首页卡片交互见 [flclash.md](flclash.md#导航与交互约定)。

备份读取/传输上限为 64 MiB，WebDAV 使用 [BoxWebDAV.kt](app/src/main/java/io/nekohasekai/sfa/flutter/BoxWebDAV.kt) 与核心扩展；取消文件选择和未确认上传不能报告成功。HTTP 行为见 [libbox.md](libbox.md)。

原生日志保留所有收到的等级供 Flutter 过滤，`CommandClient` 将当前会话内核的 `setDefaultLogLevel` 回调与日志记录一起传给界面。默认值来自内核实际使用的配置，不自行解析本地文件来推断远端状态；本地配置成功重载后，通过已启动配置快照的变化重新订阅日志，更新默认级别。界面筛选规则见 [flclash.md](flclash.md#导航与交互约定)。

日志原生缓冲保留最近 3000 条记录；[LogStream.kt](app/src/main/java/io/nekohasekai/sfa/flutter/LogStream.kt) 仅在前台日志页请求数据时生成桥接消息，首次订阅及恢复可见时发送快照，随后发送新增记录、最早保留 ID 和默认等级。清空或缓冲身份重置发送替换消息，单纯等级变化不重新编码记录。隐藏日志页不影响原生缓冲和当前会话的等级同步。

[BoxDashboard.kt](app/src/main/java/io/nekohasekai/sfa/flutter/BoxDashboard.kt) 的详细内存采样独立于网络/热点轮询，由 Flutter 内存卡片和详情页共享订阅。[VisiblePolling.kt](app/src/main/java/io/nekohasekai/sfa/flutter/VisiblePolling.kt) 仅在订阅可见、应用前台且屏幕亮起时每两秒采样；重新满足条件立即采样，隐藏或断开 EventChannel 时停止。手动释放仍单次执行 libbox 回收并更新统计，不增加自动强制 GC。

应用列表通过 [AppListCache.kt](app/src/main/java/io/nekohasekai/sfa/flutter/AppListCache.kt) 复用一份包查询快照，参考上游基线的 `PackageCache`，先按系统应用/联网权限过滤，再按需读取并缓存应用名称。进入页面和手动刷新重新查询，切换显示范围复用快照；查询策略或语言改变后的查询不复用旧快照，查询失败/取消允许重试。排除应用自身及无 `ApplicationInfo` 的条目。Flutter 的排序和图标使用方式见 [flclash.md](flclash.md#导航与交互约定)。

托管应用模式仍在激活前完成扫描，安装/卸载广播沿用自动更新；列表查询完成后同步最新的已保存设置，包含后台更新的托管结果，打开页面不触发 APK/DEX 扫描。手动重新扫描通过既有桥接更新设置。查询策略继续持久化；Android 12 精确定位同时申请 fine/coarse 权限；远程配置卡不使用本地配置名。

## 请求抓取与 Root/eBPF

[BoxCapture.kt](app/src/main/java/io/nekohasekai/sfa/flutter/BoxCapture.kt) 提供本地抓包启停、筛选、列表/详情、域名及 HAR/cURL、pcapng、TLS key-log 导出。启动先确认本地服务 Started，HTTPS 等待 Root 采集器挂载成功，再核对服务实例和选中配置未变化；失败释放源和管道。切到远程会话时停止本地抓包，远程会话拒绝读取本机记录。普通 Root/Shizuku、VPN 权限及服务确认流程保留。

[CaptureSettings.kt](app/src/main/java/io/nekohasekai/sfa/flutter/CaptureSettings.kt) 将筛选、HTTPS、独立的网络包/TLS 密钥开关、手动 TLS 函数偏移和正文限额保存到私有 `noBackupFilesDir/http-debug/settings.json`，最大 64 KiB。两个原始数据开关默认关闭，手动偏移默认空；`RootServer` 将已校验的 `tlsOffsets` 随其他设置传入固定采集器，字段与 ABI 校验边界见 [核心说明](libbox.md#httphttps-请求调试)。多个 Activity 共用操作互斥锁，保存检查内容修订并使用 AtomicFile；读取先恢复中断写入的备份，再检查实际文件大小和修订。抓包期间禁止改设置，保存不会开启抓包或重载配置。这些设置和记录不随配置导出/备份。旧本机设置中的改写/脚本字段删除，同时清理本应用旧 CA 文件及专用 Keystore alias；旧配置扩展的解析/保存兼容由 [核心清理](libbox.md#旧抓包配置清理) 维护。

[RootCaptureSession.kt](app/src/main/java/io/nekohasekai/sfa/bg/RootCaptureSession.kt) 只启动 APK 原生库目录中的固定采集器，通过 stdin 传递 UID/库设置，读取有界启动进度及 ready 消息。等待总时限为 25 秒，进度不延长时限；超时报告最后的内核加载、perf 缓冲或 TLS 库检查/挂载阶段，失败清理会话。进度不能代替包含有效版本及挂载库数的 ready。启动历史最多保留 64 条、64 KiB，保留时间及级别，在 ready 之后转换为日志事件交给 libbox；启动失败将阶段历史及原错误一并交回应用记录。标准错误独立排空并最多保留 4 KiB 诊断，就绪后及退出前转换为日志，链接器警告不作为采集数据解析。输出由私有 pipe 交给 libbox，没有网络监听或外部可执行文件下载。[IRootService](app/src/main/aidl/io/nekohasekai/sfa/bg/IRootService.aidl) 使用事务 10，旧事务 2/3 保持空缺；独立 `ICaptureSession` 提供 FD 和关闭方法。会话在启动前登记，Binder owner 死亡、Root 服务销毁、核心重载/停止均关闭 stdin 和探针，避免遗留采集进程。

[RootServer.kt](app/src/main/java/io/nekohasekai/sfa/bg/RootServer.kt) 查询当前用户的安装包和 `GET_SHARED_LIBRARY_FILES`，通过 [CaptureTargets.kt](app/src/main/java/io/nekohasekai/sfa/bg/CaptureTargets.kt) 按所选 UID 收集 `nativeLibraryDir`、base/split APK 及共享 APK/原生库路径，包含 Trichrome 共享库。共享 UID 的包使用同一选择，排除 B-box 自身和其他 Android 用户；目标应用尚未运行也能准备探针。候选路径连同来源包名集合经 `targetPackages` 私有启动字段传给采集器，由后者自动解析函数偏移并补充运行中进程的实际映射，不维护内置版本偏移表。目录中的库继承目录来源，共享 APK 保留全部相关包；诊断日志和状态错误显示包名、完整路径及条目，进程映射可由实际 UID 补充来源，无法确认时标为未知。库来源说明安装/共享关系，不替代具体连接的 UID/socket 归属。手动偏移和来源字段沿用现有 JSON 管道，没有增加或重排 Binder 事务。

自动候选只传递不含 NUL/换行且 UTF-8 长度不超过 4096 字节的绝对路径。Android 的共享库列表也可能返回 `libOpenCL.so` 等原生库名称：依据 [AOSP SystemConfig](https://android.googlesource.com/platform/frameworks/base/+/refs/tags/android-15.0.0_r1/services/core/java/com/android/server/SystemConfig.java#2017) 和 [SharedLibrariesImpl](https://android.googlesource.com/platform/frameworks/base/+/refs/tags/android-15.0.0_r1/services/core/java/com/android/server/pm/SharedLibrariesImpl.java#503)，这类条目使用 soname 而非绝对路径。发现阶段跳过这些条目，保留有效的 APK/库路径，并继续由进程映射补充实际加载位置，避免一个非路径条目使整个抓取启动失败。

采集器 ABI、内核/TLS 支持范围、无版本表自动识别、HTTP 解析、详细诊断日志及资源限额统一见 [libbox.md](libbox.md#httphttps-请求调试)。`BoxCapture` 通过独立的 `capture.logs` / `capture.logs.clear` 桥接读取和清空日志；启动 Root 失败也写入日志，操作失败提示引导用户查看详情。读取日志不依赖请求存储能否打开，清日志不删除请求。状态和日志的界面入口见 [flclash.md](flclash.md#导航与交互约定)。部分库的兼容性诊断保留活动状态与已挂载数量，不把库挂载成功视为所有应用均可抓取。CA 管理、证书模块生成、信任检查、HTTP 改写、脚本导入及相应页面/桥接均已移除。此前自行刷入的系统证书模块仍需在对应 Root 管理器中卸载。

HAR/cURL、pcapng 和 TLS key-log 导出使用 Android 文档选择器；取消或写入失败不报告成功，私有临时文件用后删除。pcapng 的实际网络包、密钥支持范围、按整条 TLS 连接导出及独立存储限额见 [核心实现](libbox.md#httphttps-请求调试)。清请求同时清除原始包和密钥；eBPF 日志通过已有的 `document.save` 导出当前筛选结果，清日志保持独立。域名查询继续用于抓包设置的筛选选择；页面与视觉验证见 [flclash.md](flclash.md#导航与交互约定)。

## 已移除能力与兼容入口

- **Xposed/LSPosed：** 删除模块 API、Hook、Provider/AIDL、模块注册、vendored `libxposed-api`、Gradle 子项目/依赖及特权增强页面/设置。普通 VPN、Root/Shizuku 查询、桥接/自动重定向、Shell 和报告继续保留。
- **Android 5/6：** 删除 `otherLegacy`、`src/minApi21`、`src/otherLegacy`、legacy 核心依赖及 API 21 专用依赖版本。`play` 和 `other` 均使用 API 24+ 实现。Gradle 的 `useLegacyPackaging` 是原生库压缩方式，不代表仍支持旧 Android 分支。
- **旧更新下载/静默安装：** 删除 GitHub/F-Droid/Play 旧更新器、APK 下载和安装工具、对应 Root/Shizuku Binder 方法、Manifest 权限、设置及资源。设置/启动器不再显示旧自动更新询问、弹窗和徽标。
- **旧任务兼容：** 启动取消 WorkManager 的 `AutoUpdate`；[UpdateWorker.kt](app/src/main/java/io/nekohasekai/sfa/vendor/UpdateWorker.kt) 保留类名并直接成功返回，不下载或安装。配置订阅的 `UpdateProfileWork` 仍正常工作。
- **Binder/设置兼容：** Root/Shizuku 已退役的安装事务编号 2 不复用；Root 的模块专用编号 3 也保持空缺，其余编号不重排。删除功能的历史设置不再读取，不清空无关设置和数据库。

关于页仍提供独立 [BoxUpdates.kt](app/src/main/java/io/nekohasekai/sfa/flutter/BoxUpdates.kt) 检查：读取 B-box Releases（含预发布），以 `B-box-version-metadata.json` 的 `application_id`、`version_code`、`version_name` 核对安装身份和版本，再匹配设备 ABI 的 `B-box-<版本>-<ABI>.apk`。没有匹配 APK 时不提供下载资产。它不恢复原更新器的后台下载/静默安装链路。

## 构建与发布

[app/build.gradle.kts](app/build.gradle.kts) 从本地 `app/libs/libbox.aar` 和 Flutter 生成的 Maven 仓库消费依赖。核心必须包含 [libbox.md](libbox.md) 的当前扩展。[setup-flutter](.github/actions/setup-flutter/action.yml) 统一固定 Flutter 3.47.6，并以 `--enforce-lockfile` 读取已提交的依赖锁文件。CI 先构建 AAR 到 `flutter/build/host/outputs/repo`，再构建 Android；应用使用 AAR 时不需要再安装 Flutter SDK。

默认 ABI 为 ARMv7、ARM64、x86_64 并生成 universal；Flutter 不提供 x86 AOT，因此没有 x86。`-Parm64Only=true` 只生成 ARM64，`-PemulatorOnly=true` 只生成 x86_64，二者都关闭 universal；核心及 Flutter AAR 必须包含目标 ABI。

[build-release.yml](.github/workflows/build-release.yml) 仅手动触发，发布 Android 7.0+ 的 ARM64 `otherRelease`。Release 与模拟器构建共用 [build-libbox](.github/actions/build-libbox/action.yml)，核心使用 JDK 17，应用使用 JDK 21；具体 SDK/Build Tools/NDK 版本以共享 Action 和 Gradle 为准。本机未经明确要求不构建，见 [AGENTS.md](AGENTS.md)。

发布签名使用以下仓库 Actions Secrets：

| Secret | 用途 |
| --- | --- |
| `KEYSTORE_BASE64` | 发布密钥文件的 Base64 内容 |
| `KEYSTORE_PASS` | 密钥文件密码 |
| `ALIAS_NAME` | 发布密钥 alias |
| `ALIAS_PASS` | 对应密钥密码 |

工作流将已有密钥解码至 `app/release.keystore`，结束时清理，不上传密钥。Gradle 属性优先读取直接环境变量，再回退 `LOCAL_PROPERTIES` 和 `local.properties`。本地签名文件不入库，不重新生成发布密钥。

Release 调用 [newui.yml](.github/workflows/newui.yml) 完成核心、eBPF 采集器及 Flutter 验证，直接下载**同一次运行**的 `flutter-aar-arm64` Artifact，不再次分析、测试或构建 Flutter。随后运行 `testOtherDebugUnitTest` 及保留 R8 的签名 Release 构建；单元测试成功或失败后均上传 HTML/XML 报告至 `release-unit-tests` Artifact（报告已生成时），保留 30 天。签名密钥在单元测试通过后才准备。[verify_release.py](tools/verify_release.py) 在 APK 上传前校验唯一 APK、最低 API 24、ARM64、应用包名/版本/标签、启动 Activity 和签名。APK Artifact 名为 `B-box-<版本>-arm64-v8a-release`，保留 30 天。

`publish_release` 默认开启；设为 `false` 时仍构建、校验并上传 Actions Artifact，仅跳过发布。[publish_release.py](tools/publish_release.py) 要求 Release tag 严格等于 `VERSION_NAME`（不加 `v`）且指向本次源码 SHA；已有 tag 指向其他提交时失败，不移动版本标签。同一提交重跑可重传资产。发布 APK 及 `B-box-version-metadata.json`，alpha/beta/rc 标为预发布。

## 验证与历史记录

| 入口 | 配置的验证范围 |
| --- | --- |
| [verify_newui.py](tools/verify_newui.py) | Flutter 字形/导入、Android 入口、旧 UI/更新依赖及退役抓包实现残留的静态检查 |
| [newui.yml](.github/workflows/newui.yml) | `dev` / `newui` push、目标为这两个分支的 PR、手动触发或 Release 复用；仅 Markdown 的 push/PR 跳过。检查 Go 扩展、HTTP/eBPF 抓取及自动重定向布局，执行 Linux ARM64/x86_64 Root SSL/Go TLS 与 x86_64 APK smoke，独立解密同次样本的正式 pcapng 导出，运行 Flutter 分析/交互/布局测试并构建 ARM64 Flutter AAR |
| [newui-android.yml](.github/workflows/newui-android.yml) | 手动触发；JVM 回归、`play` Kotlin 编译、`other` APK/仪器测试构建及 API 24/31/35 模拟器 smoke |
| [build-release.yml](.github/workflows/build-release.yml) | 手动触发；复用完整核心/Flutter 验证，运行 JVM 测试、签名 ARM64 Release、安装身份/产物校验及可选发布 |

JVM 回归覆盖服务启动确认/失败/超时、设置、日志等级/边界和 QRS；[ServiceSettingsChangeTest.kt](app/src/test/java/io/nekohasekai/sfa/flutter/ServiceSettingsChangeTest.kt) 覆盖服务开关保存后的启动确认、失败/超时/取消恢复及恢复错误保留；[ProfileChangeTest.kt](app/src/test/java/io/nekohasekai/sfa/database/ProfileChangeTest.kt) 覆盖配置持久化失败、应用超时/取消、恢复确认及恢复错误保留。[ProfileRecoveryTest.kt](app/src/androidTest/java/io/nekohasekai/sfa/flutter/ProfileRecoveryTest.kt) 覆盖实际 libbox 二进制导入、查询策略持久化、端口占用导致的 JSON 编辑/配置切换/手动与自动订阅/完整备份恢复失败，以及停服和未选中配置更新的服务状态。Flutter 视觉验证见 [flclash.md](flclash.md)。

模拟器统一入口为 [android_smoke.py](tools/android_smoke.py)：安装应用和仪器测试 APK，分别重置应用数据后运行 UI smoke、原生回归及采集器平台回归，前一组失败不跳过后一组。UI 覆盖首次启动、远程 JSON 导入、VPN 授权、启动/停止、实际内核分组及进程重启后的配置保留；原生组运行全部仪器测试并拒绝零测试结果。采集器组执行同次构建生成的 Android Go 测试二进制，检查 [eBPF 加载器平台路径](libbox.md#httphttps-请求调试)，不要求模拟器具备 Root 抓取权限。`build/android-smoke/` 保存分组截图、logcat、失败堆栈、仪器测试与采集器测试输出及带源码 SHA 的 `results.json`；测试超时也保留已写入的日志。启动阶段允许截图后关闭一次 Google APIs 模拟器的 Pixel Launcher ANR 弹窗，再重新打开 B-box；不关闭 B-box 自身的 ANR 或其他错误。脚本仅用于 CI 模拟器，不能对保有用户数据的设备执行。

查询策略回归在每次切换 Root/Shizuku 并刷盘后重新打开 Room 数据库读取持久值，同时核对状态流，不以读取同一内存缓存代替持久化验证。

`ProfileRecoveryTest.logLevelFollowsSuccessfulConfigurationReloads` 另覆盖真实内核日志订阅的默认级别、运行中修改 `log.level` 后刷新，以及移除该字段后恢复当前核心的默认值。

`ProfileRecoveryTest.localProfileTimesFollowSuccessfulContentChanges` 覆盖本地创建/内容编辑的时间记录、无变化保存和重命名不刷新时间、应用失败恢复原内容与时间；二进制订阅测试另检查手动 JSON 编辑保留导入的订阅时间。

`ProfileRecoveryTest.captureSettingsAreLocalAndCheckRevisions` 使用实际 libbox 和 AtomicFile，覆盖本机设置迁移/保存/重新打开、修订冲突、退役选项拒绝及旧配置清理。[CaptureProtocolTest.kt](app/src/test/java/io/nekohasekai/sfa/bg/CaptureProtocolTest.kt) 检查启动消息边界；[CaptureSessionTest.kt](app/src/androidTest/java/io/nekohasekai/sfa/bg/CaptureSessionTest.kt) 用确定性的子进程检查 ready 始终先于启动日志、日志时间/级别/细节、历史淘汰、标准错误分离、ready/错误、超时阶段及清理、原始操作错误保留、FD 单次领取、owner 死亡及重复关闭，并运行实际打包的 Android 采集器检查加载及无 Root 错误。模拟器没有代表真实 Root eBPF 设备的权限环境，采集效果仍需实机验证。

[CaptureTargetsTest.kt](app/src/androidTest/java/io/nekohasekai/sfa/bg/CaptureTargetsTest.kt) 检查安装文件发现包含原生库目录、split 和共享 APK、共享 UID 选择及去重，并排除自身/其他 Android 用户；另覆盖全部/选定应用中的原生 soname 过滤、非法候选跳过及 UTF-8 路径长度边界，无需启动目标进程。实际 ELF/APK 偏移和挂载验证入口见 [libbox.md](libbox.md#构建同步和验证)。

[DashboardConfigCacheTest.kt](app/src/test/java/io/nekohasekai/sfa/flutter/DashboardConfigCacheTest.kt) 通过调用计数覆盖重复轮询不再读取/哈希/解析、地址变化、配置切换、写入/回滚、运行快照更新及错误/取消重试；`ProfileRecoveryTest.lanCacheTracksEqualSizeWritesSuccessfulReloadsAndRecovery` 使用实际文件和 libbox，覆盖同大小同时间戳写入、保存与运行配置分离、成功重载、失败恢复和停服后的 LAN 状态。它们由 Android smoke 的 JVM/仪器测试步骤运行，Release 运行其中的 JVM 测试。

[AppListCacheTest.kt](app/src/test/java/io/nekohasekai/sfa/flutter/AppListCacheTest.kt) 通过查询/名称读取计数覆盖隐藏应用不读取名称、筛选切换复用、刷新及查询策略/语言失效、失败/取消重试和并发查询合并，由 Android smoke 和 Release 的 JVM 测试运行。它不衡量设备上的实际加载耗时。

[LogStreamTest.kt](app/src/test/java/io/nekohasekai/sfa/flutter/LogStreamTest.kt) 覆盖日志快照、增量、淘汰边界、等级更新、清空、重新订阅和缓冲重置，并检查 3000 条缓冲新增一条时只编码一条。[VisiblePollingTest.kt](app/src/test/java/io/nekohasekai/sfa/flutter/VisiblePollingTest.kt) 用虚拟时间检查不可见/后台/熄屏时零采样、恢复立即采样及取消在途任务；两者由 Android smoke 和 Release 的 JVM 测试运行，Flutter 通知与订阅测试见 [flclash.md](flclash.md#验证与升级参考版本)。这些调用计数不代表实测内存或帧率收益。

上述为已配置的检查，不是当前提交的通过声明。记录实际结果时附源码 SHA、Actions 链接和未验证范围，不能沿用旧裁剪 APK 或旧 Compose 验证记录。

原 `MODIFICATIONS.md` 与 `NEWUI.md` 已按领域归入这四份文档。历史 MOD-001～004、007～008、012 的 Compose 界面由 Flutter 替代；MOD-005/006/009 的品牌、安装身份及发布约定在本文继续维护；MOD-011/015/016 的删除项以本次迁移后的现状为准；MOD-013/014 的核心与工具裁剪已回退；MOD-017 的 UI 适配见 [flclash.md](flclash.md)。旧实现、逐次构建日志与产物哈希可通过 `git log --all -- MODIFICATIONS.md NEWUI.md` 和对应提交的文件内容查询，不再混入当前项目约束。
