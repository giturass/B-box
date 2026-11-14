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

Tailscale 端点/节点/出口节点/Ping、Taildrop 收发与分享、SSH/Root/Shizuku 终端、Ghostty 设置和导入字体、OpenConnect/OpenVPN 认证、USB/IP 和网络诊断通过 Flutter 页面使用现有后端。崩溃/OOM/电量报告的存储、读取和导出桥接仍保留，既有报告及设置数据不因界面入口调整而清理；当前入口范围见 [导航与交互约定](flclash.md#导航与交互约定)。

`ready` 保留 Settings/libbox 初始化、版本、仪表盘、配置、设置、界面偏好和待处理 Intent；配置、设置和偏好独立读取并发执行，不等待 LAN、工具与诊断快照。次要状态通过 EventChannel 同步，通道重连补发已有 LAN、工具、诊断、传输和终端状态；Flutter 不用迟到的启动快照覆盖已收到的新事件。

[BoxTools.kt](app/src/main/java/io/nekohasekai/sfa/flutter/BoxTools.kt) 按可见消费者订阅 USB/IP 状态和诊断出站列表。Tailscale、OpenConnect、OpenVPN 在前台且当前服务/远程会话可用时继续订阅，保留认证及来件状态；工具详情不可见时只编码 Tailscale 端点状态、认证链接和文件数量，进入详情后补发节点数据。工具事件去重，传输操作与终端生命周期沿用原后端；Flutter 通知范围见 [界面状态约定](flclash.md#导航与交互约定)。

Flutter 从 `ready` 和配置/设置变更事件取得配置列表与设置，工具快照由 EventChannel 同步。Ghostty 预览通过 [BoxTerminalFactory.kt](app/src/main/java/io/nekohasekai/sfa/flutter/BoxTerminalFactory.kt) 的原生视图使用当前终端配置；这些功能共用现有启动、事件和视图入口。

[PackageQueryManager.kt](app/src/main/java/io/nekohasekai/sfa/vendor/PackageQueryManager.kt) 保留已保存查询策略的状态流。Root 可用性缓存继续供平台桥接使用；Shizuku 死亡监听继续重置实际服务连接，权限检查与申请由 [ShizukuAccess.kt](app/src/main/java/io/nekohasekai/sfa/vendor/ShizukuAccess.kt) 提供。旧原生页面的安装、授权和连接展示状态已移除。

## 配置、服务应用与备份

[BoxProfiles.kt](app/src/main/java/io/nekohasekai/sfa/flutter/BoxProfiles.kt) 负责导入、编辑、手动订阅更新、配置切换及局域网代理保存；[ProfileContent.kt](app/src/main/java/io/nekohasekai/sfa/database/ProfileContent.kt) 的互斥锁和内容替换流程由前台编辑和后台订阅更新共用。

Cloudflare WARP MASQUE 使用新增的 sing-box `masque` 出站，通过既有 JSON 导入/编辑和 `Libbox.checkConfig` 校验。字段、凭据及示例由 [核心文档](libbox.md#cloudflare-warp-masque) 维护。

- 保存前检查内容修订和完整 `Libbox.checkConfig`，以原子写入替换内容。分项配置编辑原生 DNS、路由、规则集、入站、出站、端点和日志；原始 JSON 编辑保留配置元数据及订阅 URL。
- 普通应用设置、查询策略/托管扫描结果、终端设置、配置选择及服务模式通过 [RoomPreferenceDataStore.kt](app/src/main/java/io/nekohasekai/sfa/database/preference/RoomPreferenceDataStore.kt) 的确认事务保存；只有 Room 提交成功才发布缓存并向调用方返回成功。查询策略先用于扫描，成功后与其他设置一起提交。保留的同步属性写入在失败时恢复缓存，`flush()` 报告此前尚未报告的错误，较早写入的完成或失败不能覆盖后续排队的值；服务清理与广播结束不因刷盘失败中断。字符串集合按 UTF-8 字节长度编码，支持中文及 emoji。
- 本地配置创建时在 Room 记录时间，原始 JSON 编辑、分项配置编辑及局域网入站变更成功保存内容时更新 `lastUpdated`；无内容变化和仅编辑名称等元数据不刷新时间。保存或应用失败与内容一起恢复旧时间。远程配置的该字段仍表示订阅更新时间，手动 JSON 编辑不覆盖它；没有时间记录的旧配置保持未知。界面展示见 [flclash.md](flclash.md#配置页与内核差异)。
- 运行中修改选中配置需应用到服务：模式相同时重载，模式变化时停止并重新走权限/启动流程。需要重启时通过 [ServiceStartResult.kt](app/src/main/java/io/nekohasekai/sfa/flutter/ServiceStartResult.kt) 等待 Started 回调；失败或超时恢复旧文件/设置并尝试恢复原服务。停服时普通保存不会自动启动 VPN。
- [Settings.kt](app/src/main/java/io/nekohasekai/sfa/database/Settings.kt) 的 `detectServiceMode()` 在 `Dispatchers.IO` 内查询选中配置、读取文件并调用 libbox 判断 TUN 入站，避免主线程应用配置时阻塞界面。读取或解析失败沿用普通服务模式回退，协程取消继续向调用方传播。
- [BoxService.kt](app/src/main/java/io/nekohasekai/sfa/bg/BoxService.kt) 的启动/重载错误和主动停止共用主线程上的停止入口，由唯一的清理任务等待启动结束、释放核心与命令服务器，再报告 Stopped。失败 RPC 返回后触发的恢复不能另起一条错误清理路径，避免迟到的清理注销恢复后实例的接收器、遗留核心及缓存文件锁。错误和运行配置发布检查启动代次，旧请求不能停止或覆盖新实例；已进入停止阶段的请求不能再启动核心。
- 原始 JSON 编辑、分项配置编辑、手动/自动订阅更新和配置切换共用 [ProfileChange.kt](app/src/main/java/io/nekohasekai/sfa/database/ProfileChange.kt) 的失败恢复流程。订阅保存或应用失败恢复旧内容和更新时间；切换失败恢复原选择及服务模式。恢复在不可取消的协程上下文完成，保留原错误及恢复错误；持久数据恢复失败时不启动未恢复的配置。
- 配置切换、选中配置的内容写入及备份恢复在持久化前检查宿主与本机服务状态，启动中或停止中拒绝修改。配置事务另用短期互斥区保护文件、选择及元数据写入，启动/重载读取同一快照；等待服务重启前释放该锁，避免启动读旧内容或与配置操作互相等待。保存预检后才发生的启停请求等待最终状态再决定是否应用。界面禁用行为见 [flclash.md](flclash.md#配置页与内核差异)。
- 运行中重载缺少 Wi-Fi 规则所需定位权限时，RPC 返回失败并回滚旧配置，确认原服务恢复后结束操作；不会发送可能在回滚后再次启动服务的定位授权回调。初次启动仍保留系统定位授权流程，停服保存后再启动也沿用该流程。运行配置快照在权限检查成功后发布。
- 删除配置在同一互斥区内检查最新选择，禁止删除当前选中的配置，停服时也需先切换到其他配置。拒绝操作不改文件、数据库、选择或服务；不再通过删除操作自动选择其他配置。
- URL 导入、手动及后台订阅更新共用 [HTTPClient.kt](app/src/main/java/io/nekohasekai/sfa/utils/HTTPClient.kt) 和可取消的核心下载接口，不设固定下载容量或总时限；协程取消在阻塞的 gomobile 调用仍在运行时关闭 `UIHTTPTask`，等待网络退出，接口语义见 [HTTP 与 WebDAV](libbox.md#http-与-webdav)。网络下载在配置及桥接操作锁外执行，下载失败不进入保存或服务应用。更新提交时通过 [ProfileSubscription.kt](app/src/main/java/io/nekohasekai/sfa/database/ProfileSubscription.kt) 复核配置存在、URL、类型、路径、修订、更新时间和自动更新计划；编辑、删除或另一轮更新后的旧结果不能覆盖当前数据。桥接提交另检查本地会话身份，切换会话后的迟到下载需要重试。
- [UpdateProfileWork.kt](app/src/main/java/io/nekohasekai/sfa/bg/UpdateProfileWork.kt) 在同一互斥区内完成每个订阅的保存、应用和恢复，应用失败请求重试。[BackgroundProfileService.kt](app/src/main/java/io/nekohasekai/sfa/bg/BackgroundProfileService.kt) 通过本机服务连接确认状态，模式相同重载，模式变化及失败恢复时等待停止和 Started 回调；后台重启所需 VPN/通知权限未授予时更新失败并回滚。更新未选中配置或停服时不启动服务，远程会话不接收本机订阅的重载。
- 服务设置页的 VPN 绕过、自动重定向和动态通知开关保存后，运行中的本地服务自动停止并重新走权限/启动流程，确认 Started 后完成操作。[ServiceSettingsChange.kt](app/src/main/java/io/nekohasekai/sfa/flutter/ServiceSettingsChange.kt) 在保存或重启失败时恢复原设置，仅在设置恢复成功后尝试恢复原服务；恢复失败保留原错误与恢复错误，不启动未恢复的设置。停止状态只保存设置，远程会话不触发本机或远端重启。
- Root 自动重定向的系统 BPF map 兼容范围和核心补丁见 [libbox.md](libbox.md#android-自动重定向兼容)。Android 桥接继续沿用上游的启动、错误传递及 Root 服务死亡清理流程。
- 局域网代理使用 libbox 原生入站变换，不引入全局 Clash 端口设置。运行时端点来自已成功启动的配置快照，避免将保存失败或未应用的配置显示为正在监听。
- [DashboardConfigCache.kt](app/src/main/java/io/nekohasekai/sfa/flutter/DashboardConfigCache.kt) 仅缓存选中配置和已启动配置的内容、修订及最近一组接口地址的 LAN 解析结果，相同内容共用缓存。刷新时以配置 ID、路径、文件时间/大小及写入代次检查保存配置；未变化时不重读内容、计算哈希或调用 LAN 解析。地址变化复用内容和修订，重新调用 LAN 解析；运行配置按成功启动快照更新。普通写入及备份恢复/回滚共用 `ProfileContent.write` 推进代次，避免同大小、同时间戳的修改漏刷。解析错误也缓存，配置/地址变化或手动刷新时重试，取消不缓存。
- 二进制 `.bpf` 导入保留订阅 URL、更新间隔、自动更新及时间戳。动态 QRS 通过 [BoxQrScanner.kt](app/src/main/java/io/nekohasekai/sfa/flutter/BoxQrScanner.kt) 按相机会话收集有界帧、校验 CRC，并支持原压缩文件封装。

[BoxBackups.kt](app/src/main/java/io/nekohasekai/sfa/flutter/BoxBackups.kt) 提供 `bbox-1` 格式，包含稳定配置身份、界面/应用设置、终端设置与导入字体，以及可选 WebDAV 设置。仅恢复配置时不改其他设置。恢复先暂存文件，再使用 Room 事务；应用失败回滚文件、数据库和设置。任一步回滚失败仍继续其他回滚，但不再请求启动服务，保留暂存原件以及原始错误和回滚错误。兼容策略保留其他配置，覆盖策略在用户确认后删除备份中不存在的配置。

界面偏好的读取、保存及备份恢复共用 [BoxPreferences.kt](app/src/main/java/io/nekohasekai/sfa/flutter/BoxPreferences.kt) 校验，卡片布局接受 `System HTTP proxy` 键。旧设置及备份中的 `locale`、`fontFamily`、`fontScale`、`customFontScale` 被忽略，下次保存界面偏好时清理这些键，保留其余偏好；终端设置使用独立数据，不受此清理影响。外观及首页卡片交互见 [flclash.md](flclash.md#导航与交互约定)。

配置、备份、终端配置文本及导入字体不设固定容量配额；保留 JSON/字体有效性、文件路径、修订和原子提交校验。文档选择器读取完整内容，WebDAV 使用 [BoxWebDAV.kt](app/src/main/java/io/nekohasekai/sfa/flutter/BoxWebDAV.kt) 与可取消核心任务；订阅/WebDAV 取消会结束原生请求并按协程取消传播，正常网络错误继续报告。取消文件选择、传输失败和未确认上传不能报告成功。HTTP 行为见 [libbox.md](libbox.md#http-与-webdav)。报告文本读取同样不额外设置固定字节上限，二进制配置继续使用下载入口。

原生日志保留所有收到的等级供 Flutter 过滤，`CommandClient` 将当前会话内核的 `setDefaultLogLevel` 回调与日志记录一起传给界面。默认值来自内核实际使用的配置，不自行解析本地文件来推断远端状态；本地配置成功重载后，通过已启动配置快照的变化重新订阅日志，更新默认级别。界面筛选规则见 [flclash.md](flclash.md#导航与交互约定)。

日志原生缓冲保留最近 3000 条记录；[LogStream.kt](app/src/main/java/io/nekohasekai/sfa/flutter/LogStream.kt) 仅在前台日志页请求数据时生成桥接消息，首次订阅及恢复可见时发送快照，随后发送新增记录、最早保留 ID 和默认等级。清空或缓冲身份重置发送替换消息，单纯等级变化不重新编码记录。隐藏日志页不影响原生缓冲和当前会话的等级同步。

[ConnectionStore.kt](app/src/main/java/io/nekohasekai/sfa/backend/connections/ConnectionStore.kt) 按 ID 应用当前内核的新增、计数更新和关闭事件，复用未变化的记录及不可变快照。事件语义对应核心 [command_types.go](https://github.com/SagerNet/sing-box/blob/824dbcfb4dd0a95921840b1737b950cde590241e/experimental/libbox/command_types.go)：更新累加总量，关闭使用最终计数并归零速率，已关闭记录保留五分钟；DNS 出站继续过滤。[ConnectionStream.kt](app/src/main/java/io/nekohasekai/sfa/flutter/ConnectionStream.kt) 在前台连接页/详情请求数据时与上次实际发送的快照比较，发送变更记录和删除 ID；首次及恢复可见时完整同步，StateFlow 合并中间状态不会漏掉变更。

界面网络信息通过 [BoxDashboard.kt](app/src/main/java/io/nekohasekai/sfa/flutter/BoxDashboard.kt) 的 `ConnectivityManager.registerNetworkCallback` 被动监听网络、能力和地址变化，配置、服务、运行快照及远程会话变化也触发刷新。[NetworkRefresh.kt](app/src/main/java/io/nekohasekai/sfa/flutter/NetworkRefresh.kt) 合并突发通知并串行刷新；前台且亮屏时，内网 IP 卡片/详情可见每十秒、隐藏每六十秒补查接口，覆盖缺少回调的热点变化，后台或熄屏停止监听与补查。手动刷新立即检查并允许缓存错误重试。此监听只读取界面数据，不调用网络请求、VPN 重建或服务重连；服务侧 `DefaultNetworkListener` 保持原实现。

监听请求在 API 30+ 使用公开的 [clearCapabilities()](https://developer.android.com/reference/android/net/NetworkRequest.Builder#clearCapabilities())；API 24–29 通过 `removeCapability()` 移除 `NOT_RESTRICTED`、`TRUSTED` 和 `NOT_VPN` 三项默认能力，继续覆盖 VPN 和无互联网能力的网络变化。旧版本默认值依据 [Android 10 NetworkCapabilities 源码](https://android.googlesource.com/platform/frameworks/base/+/refs/tags/android-10.0.0_r1/core/java/android/net/NetworkCapabilities.java)，不调用旧系统的隐藏接口。

[BoxDashboard.kt](app/src/main/java/io/nekohasekai/sfa/flutter/BoxDashboard.kt) 的详细内存采样独立于网络/热点轮询，由 Flutter 内存卡片和详情页共享订阅。[VisiblePolling.kt](app/src/main/java/io/nekohasekai/sfa/flutter/VisiblePolling.kt) 仅在订阅可见、应用前台且屏幕亮起时每两秒采样；重新满足条件立即采样，隐藏或断开 EventChannel 时停止。手动释放仍单次执行 libbox 回收并更新统计，不增加自动强制 GC。

应用列表通过 [AppListCache.kt](app/src/main/java/io/nekohasekai/sfa/flutter/AppListCache.kt) 复用一份包查询快照，参考上游基线的 `PackageCache`，先按系统应用/联网权限过滤，再按需读取并缓存应用名称。进入页面和手动刷新重新查询，切换显示范围复用快照；查询策略或语言改变后的查询不复用旧快照，查询失败/取消允许重试。排除应用自身及无 `ApplicationInfo` 的条目。Flutter 的排序和图标使用方式见 [flclash.md](flclash.md#导航与交互约定)。

托管应用模式仍在激活前完成扫描，安装/卸载广播沿用自动更新；列表查询完成后同步最新的已保存设置，包含后台更新的托管结果，打开页面不触发 APK/DEX 扫描。手动重新扫描通过既有桥接更新设置。查询策略继续持久化；Android 12 精确定位同时申请 fine/coarse 权限；远程配置卡不使用本地配置名。

## 请求抓取与 MITM

MITM 配置、证书设置及 HTTP 脚本/Cron 的入口见 [配置编辑](flclash.md#配置页与内核差异)。先准备并安装 CA，再在选中配置的 MITM 中开启 HTTPS，按顺序填写域名及排除项，并按需开启 TCP 解密。空域名列表不解密。配置随本地服务生效；“工具 → 连接与请求”只控制记录，从新请求/流边界开始，未被接管的旧连接需要目标应用重新建立。HTTP 明文记录不需要 CA，协议选项、默认值及匹配规则见 [libbox.md](libbox.md#独立-mitm-配置)。

[BoxCapture.kt](app/src/main/java/io/nekohasekai/sfa/flutter/BoxCapture.kt) 提供本地请求启停、筛选、列表/详情、域名、HAR/cURL 导出以及 CA 管理。开始抓包需本地服务已 Started；记录设置准备完成后，在配置互斥区复核服务实例、运行配置和当前选择，再调用该服务的抓包绑定；解密设置与证书来自已启动配置。启动失败、返回期间取消或状态确认失败会回收本次已启动会话。切换远程会话停止本地抓包，远程会话拒绝读取或操作本机记录与证书。核心协议、路由、限额和停止行为见 [libbox.md](libbox.md#httphttps-请求调试)。

统一请求查看器和关联请求通过 `capture.query` 调用 `QueryUICapture`，将搜索、活动状态、排序、连接 ID、窗口及已知摘要版本交给核心；只在 Flutter 与当前连接订阅合并查询窗口，UI 行为见 [flclash.md](flclash.md#导航与交互约定)。查询附带当前连接的 ID 和搜索元数据，核心返回全部匹配请求所代表的当前连接 ID，避免分页后重复展示父连接。旧 `capture.snapshot` / `ListUICapture` 绑定继续保留兼容。

`capture.export` 的 HAR 操作接受 `selection` 查询条件，打开 Android 文件选择器前通过 `SelectUICaptureHAR` 固定所有匹配 HTTP ID，再用 `ExportUICaptureHARSelection` 流式导出；完整 ID 列表仅在原生层持有，未加载记录仍可导出，选择后清空的记录不以新记录替换。原有显式 `ids` 和 `query` 形式继续支持，不另设选择条数上限；正文、文件选择取消和导出失败语义保持不变。[BoxWire.kt](app/src/main/java/io/nekohasekai/sfa/flutter/BoxWire.kt) 在仪表盘消息中提供远程服务器 ID，Flutter 按目标变化清空连接视图并递增会话代次，隔离迟到抓取结果和旧详情操作，同名远程服务器也分别处理。远程页面只订阅远端连接，不能查看或操作本机抓取历史。

[CaptureSettings.kt](app/src/main/java/io/nekohasekai/sfa/flutter/CaptureSettings.kt) 将 version 3 的应用/端口、域名提取和正文限额保存到私有 `noBackupFilesDir/http-debug/settings.json`，正文可保存完整内容或使用用户填写的字节限制。多个 Activity 共用互斥锁，保存检查修订并使用 AtomicFile，读取先恢复中断写入，不另设设置或迁移文件容量配额。抓包期间不能改记录设置。version 0～2 原子迁移保留记录筛选，删除旧 HTTPS、解密包含/排除、QUIC、库路径、函数偏移及本机改写/脚本字段，新保存拒绝这些字段；不会将旧本机解密策略提升为配置。配置和备份不携带抓包设置、记录或 CA。其他旧扩展清理见 [核心兼容说明](libbox.md#旧抓包配置清理)。

[MitmCertificate.kt](app/src/main/java/io/nekohasekai/sfa/flutter/MitmCertificate.kt) 只在用户点击生成时创建 CA。P-256 根私钥使用随机独立 alias 保存到 Android Keystore，不可导出；Go 传入 SHA-256 摘要，由 `NONEwithECDSA` 签名，再由 Go 验证。API 24 起所需操作依据 [AOSP Android 7 Keystore 实现](https://android.googlesource.com/platform/frameworks/base/+/refs/tags/android-7.0.0_r1/keystore/java/android/security/keystore/AndroidKeyStoreECDSASignatureSpi.java)。公钥证书和 alias 保存于不参与备份的 `http-debug/mitm-authority.json`，AtomicFile 记录最多 32 KiB。重新生成检查修订、先创建新密钥并签发，原子写入成功后才删除旧 alias；失败保留旧记录，清理本次新密钥。手动抓包或配置 MITM 正在运行时不能重新生成；单独运行改写或脚本不占用 CA。生成本身不安装证书或启用解密。

证书快照分别报告有效期、私钥可用性、SHA-256、公钥身份及 AndroidCAStore 中按完整 DER 匹配的用户/系统信任。匹配遍历所有 alias，兼容 subject-hash 冲突；进程内信任库存在该证书不代表任意目标应用会信任。Android 7 起很多应用默认不信任用户 CA，pinning、自有证书库、工作资料和独立 namespace 也会限制抓取。界面安装/刷新流程见 [flclash.md](flclash.md#导航与交互约定)。

CA 导出仅包含公钥 CRT。Android 7–10 使用 KeyChain 安装入口，Android 11 及以后打开安全设置，由用户先导出、再在设置中选择 CA 文件；返回值仅表示设置已打开，不能作为安装成功证明。[MitmCertificateModule.kt](app/src/main/java/io/nekohasekai/sfa/flutter/MitmCertificateModule.kt) 可导出 `bbox_mitm_ca` ZIP，交给 Magisk、KernelSU 或 APatch 管理器安装并重启，应用不执行自动 Root 注入。

[模块脚本](app/src/main/assets/mitm-ca/inject.sh) 参考当前 FlClash 的 system/Conscrypt APEX namespace 处理，导出模块版本为 3。在各实际 namespace 内复制其原有证书集合到独立 tmpfs，补入公钥 CA、处理 hash.N 冲突并设权限/SELinux 标签，准备成功后才 bind 到目标；不先清空证书目录或写系统分区。进入 namespace 前打开并固定 namespace、进程根目录、解释器、脚本及公钥证书的描述符，在目标 `/dev` 临时暂存，避免目标视图隐藏模块路径后无法读取注入器和证书；切换后沿用目标进程的根目录。挂载后核对证书，准备失败保留原库。

post-fs-data 只做不等待的早期注入；[service.sh](app/src/main/assets/mitm-ca/service.sh) 以文件锁保证单实例，启动阶段每两秒重试，最多 60 轮快速检查，就绪后转为每 30 秒补查。每轮先注入已出现的主 Zygote，再枚举所有可访问的 `/proc/*/ns/mnt` 并按 namespace 标识去重，覆盖现有 app/WebView Zygote 后代及非直属进程。每轮重新核对进程实际视图中的证书，不跨轮仅凭 namespace inode 缓存成功，因此晚出现的 namespace、Zygote 重启及同一 namespace 内替换的 APEX 挂载会继续修复。禁用或移除标记出现后停止补查，已有挂载仍需重启移除。启动阶段语义依据 [Magisk](https://topjohnwu.github.io/Magisk/guides.html#boot-scripts) 和 [KernelSU](https://kernelsu.org/guide/module.html#boot-scripts) 文档，不能在阻塞的 post-fs-data 等待 Android 开机完成。

[Android 14 的 TrustedCertificateStore](https://android.googlesource.com/platform/external/conscrypt/+/refs/tags/android-14.0.0_r1/platform/src/main/java/org/conscrypt/TrustedCertificateStore.java) 默认优先读取非空的 `/apex/com.android.conscrypt/cacerts`；API 34+ 的预期证书目录缺失或为空返回 pending，旧系统没有 APEX CA 目录时允许只处理 system。单轮退出码 0 表示已检查的预期库就绪，1 表示失败，2 表示等待就绪/重试；没有 Android 证书路径的其他视图单独计为 skipped。`mount.status` 原子保存最近一轮的启动属性、退出码及统计，`mount.log` 保留最多 64 KiB 的变化记录，不能将进程退出或 nsenter 失败算作注入成功。

模块 ZIP 包含来源说明和 GPL-3.0 许可；逐文件参考见 [来源清单](flutter/flclash-manifest.json)。更新/删除模块后需重启，旧版或手工安装的其他模块由用户在原管理器中移除。轮询窗口内的首次请求仍可能早于补查，已缓存信任的应用可能需重启；Root 管理器隔离策略、不可访问的 namespace、自有证书库及 pinning 仍受各自限制。[Linux 挂载回归](tools/test_mitm_ca.py) 使用真实隔离 namespace 和临时系统目录；Android 仪器测试验证公钥、Keystore 及模块打包。两者均不代替实际 Magisk/KernelSU/APatch 安装、重启和第三方应用信任验收。

Root/eBPF 采集器、目标 TLS 库发现、FD 会话和手动函数偏移已移除。[IRootService](app/src/main/aidl/io/nekohasekai/sfa/bg/IRootService.aidl) 的事务 10 退役，不复用；原 2/3 空缺保持，普通 Root/Shizuku、VPN 授权、自动重定向和 Shell 能力保留。

正文详情只跨桥接传首个页，`capture.body` 按需读取指定方向、偏移和表示的后续页；协议及完整性字段见 [核心正文读取](libbox.md#httphttps-请求调试)。HAR/cURL、域名、CRT 和模块导出使用 Android 文档选择器，取消返回 false，写入失败不报告成功；证书在选择文件后再次检查修订。HAR 与 cURL 由原生绑定流式写入私有临时文件，再复制到用户选择的文档，用后删除临时文件，不经 JNI、JSONObject 或 Flutter 传递完整正文字符串。抓包日志页面、缓冲和 `capture.logs` / `capture.logs.clear` 平台方法已移除；运行错误及脚本 console 输出使用现有服务日志，Android 证书清理/抓包准备异常使用普通 Android 日志。

### 配置处理生命周期

`experimental.mitm`、`experimental.scripts` 和 `experimental.http_rewrite` 相互独立；字段、迁移及执行范围由 [libbox.md](libbox.md#独立-mitm-配置) 维护。规则与资源随原始 JSON 编辑、分项配置编辑、分享、订阅更新及配置备份保存，本机记录设置、CA 与记录继续留在设备私有目录。

[BoxService.kt](app/src/main/java/io/nekohasekai/sfa/bg/BoxService.kt) 启动/重载时调用 `NeedsUIMITMCA`，只有启用且非空的 MITM 域名配置要求 CA。OverrideOptions 的 ProcessingDirectory/MitmCertificate/MitmSigner 传入私有路径、公钥证书及 Keystore 回调，ScriptTimezone 传入设备时区，ProcessingProfileDirectory 传入当前实际配置文件的父目录，供相对文件资源解析。缺少所需 CA 时明确失败，不自动生成或安装。

脚本及 HTTP 改写的资源页通过 `profiles.importResource` 打开现有 Android 文档选择器，使用 ContentResolver 流式复制用户选择的文件。根据 [Android 应用专属存储](https://developer.android.com/training/data-storage/app-specific) 的访问边界，导入文件写入当前配置实际父目录，生成独立文件名并保留有效后缀，再向对应 `resources` 字典写入 `data_type: file` 和相对路径；重名资源加名称后缀，保留原条目。它不依赖 WorkingDirectoryProvider 暴露的外部文件目录，也不把 `content://` 当作 Go 文件路径。

复制在配置和桥接操作锁外完成，先写同目录暂存文件并同步；取消关闭正在读取的文档流并清理暂存。提交时重新核对本地会话、配置身份和内容修订，完整校验 JSON 后重命名文件，再沿用 ProfileContent 的原子保存、服务确认及失败恢复。恢复后确认资源未被配置引用才删除新文件；无法确认恢复状态时保留文件，避免再次破坏配置。导入只登记资源，规则通过资源名称引用；分享、配置导出及 B-box 应用内备份只保存引用，不打包本地资源文件，界面明确说明这一点。Android 系统云备份与设备迁移沿用 [backup_rules.xml](app/src/main/res/xml/backup_rules.xml) 和 [data_extraction_rules.xml](app/src/main/res/xml/data_extraction_rules.xml)，配置为包含整个 `configs` 目录（含导入资源），实际执行由系统决定。

libbox 先规范化旧配置、校验并准备三种扩展，再关闭旧处理、启动核心，成功后才激活 MITM/改写/脚本、Cron 和外部资源刷新。脚本及资源下载使用已启动实例的路由和证书库，`policy` 查找该实例的真实出站，网关模板读取该实例默认接口的网关；配置校验和预览不读取外部源码、不联网或执行脚本。重载/停止先关闭共享路由准入，再取消在途网络及定时器，等待调度/刷新、异步路由和显式出站拨号退出后才关闭核心；手动抓包启停不会重建调度。默认联网、可选限制及失败语义见 [脚本 HTTP 客户端](libbox.md#脚本-http-客户端)。

一元 gRPC 脚本的 `grpc_mode` 与空响应快捷动作使用同一 JSON 校验、格式化和保存通路，不新增平台绑定或模块导入入口。HTTP/2、消息与 trailers 处理及脚本宿主行为见 [一元 gRPC 正文脚本](libbox.md#一元-grpc-正文脚本)；脚本主动 abort 关闭当前 HTTP/1 连接或 HTTP/2 流，请求页仍保留实际错误记录。

脚本持久数据保存在私有 `noBackupFilesDir/http-debug/captures/script-storage/`，使用默认共享或显式命名空间；URL 资源缓存位于同级 `resource-cache/`。二者不设固定容量配额；清空记录、启停录制不删除它们，处理配置激活时只回收已无引用的资源缓存及未提交临时文件。脚本持久数据不会因移除规则或重载自动删除。配置/订阅/备份携带资源引用、命名空间和联网限制，实际存储值和下载缓存留在本机，不加入配置导出、应用备份或系统自动备份；应用内备份也不额外打包引用的本地文件。原子提交及失败保留旧缓存分别见 [存储](libbox.md#脚本持久存储)和[资源加载](libbox.md#文件和-url-资源加载)。

所有保存继续经过 ProfileContent 修订检查、完整 Libbox.checkConfig、原子写入及失败恢复。配置应用、恢复和后台启动使用同一入口；失败不能留下部分生效的处理或被报告为启动成功。旧嵌套配置的保守迁移见 [核心说明](libbox.md#旧抓包配置清理)。

`ReadUICaptureStatus` 的 active 仅表示手动记录，mitmActive/scriptsActive/rewriteActive 分别报告配置状态，scriptRules/cronJobs/rewriteRules 报告规则数量。录制策略在现有引擎中切换，启停不关闭已有连接；停止时封存记录，处理与转发继续，边界和资源状态字段见 [核心说明](libbox.md#httphttps-请求调试)。服务关闭结束全部处理；切换远程会话停止本地记录并禁止本机记录/证书操作，本地配置继续随本地服务运行，不作为远端状态展示。

CA 管理与服务启动共用 MitmCertificate.accessLock，加载签名器期间不能轮换密钥；活跃 MITM 或手动抓包拒绝重新生成。界面入口及状态展示见 [flclash.md](flclash.md#配置页与内核差异)。

这些流程的 Android 仪器测试范围见 [验证与历史记录](#android-仪器测试)，核心回归文件见 [libbox.md](libbox.md#回归文件覆盖)。

## 已移除能力与兼容入口

- **Xposed/LSPosed 特权增强：** 删除系统模块、VPN/HTTP 代理信息与 VPN 应用列表隐藏、接口重命名、Flutter 设置及自检/Hook 日志、Root 诊断导出，以及对应模块注册、Provider、API 依赖、许可资源和专用测试。应用查询恢复各 flavor 的普通路径：`other` 使用 PackageManager，`play` 使用保存的 Root/Shizuku 选择。普通 VPN、Root/Shizuku 查询、终端和 MITM CA 模块继续保留。从含模块的版本升级时，已注入系统进程的旧 Hook 需要在 LSPosed 中禁用 B-box 并重启 Android 后退出。
- **Android 5/6：** 删除 `otherLegacy`、`src/minApi21`、`src/otherLegacy`、legacy 核心依赖及 API 21 专用依赖版本。`play` 和 `other` 均使用 API 24+ 实现；网络监听、通知、WebView、托管扫描和 Termux 环境也已清理低于 API 24 的分支。Android 7/8 的网络回调选择及现有 Go 栈兼容处理继续保留。Gradle 的 `useLegacyPackaging` 是原生库压缩方式，不代表仍支持旧 Android 分支。
- **旧更新下载/静默安装：** 删除 GitHub/F-Droid/Play 旧更新器、APK 下载和安装工具、对应 Root/Shizuku Binder 方法、Manifest 权限、设置及资源。设置/启动器不再显示旧自动更新询问、弹窗和徽标。
- **旧任务兼容：** 启动取消 WorkManager 的 `AutoUpdate`；[UpdateWorker.kt](app/src/main/java/io/nekohasekai/sfa/vendor/UpdateWorker.kt) 保留类名并直接成功返回，不下载或安装。配置订阅的 `UpdateProfileWork` 仍正常工作。
- **Binder/设置兼容：** Root/Shizuku 已退役的安装事务编号 2 不复用；Root 的模块专用编号 3、采集器编号 10 与模块诊断导出编号 11 也保持空缺，其余编号不重排。删除功能的历史设置（包括 `hide_settings_*`）不再读取，不清空无关设置和数据库。

关于页仍提供独立 [BoxUpdates.kt](app/src/main/java/io/nekohasekai/sfa/flutter/BoxUpdates.kt) 检查：读取 B-box Releases（含预发布），以 `B-box-version-metadata.json` 的 `application_id`、`version_code`、`version_name` 核对安装身份和版本，再匹配设备 ABI 的 `B-box-<版本>-<ABI>.apk`。没有匹配 APK 时不提供下载资产。它不恢复原更新器的后台下载/静默安装链路。

[ReleaseChecker.kt](app/src/main/java/io/nekohasekai/sfa/flutter/ReleaseChecker.kt) 将最近 20 条发布的元数据检查限制为最多 3 个并发请求、每次请求最多 8 秒、整轮最多 20 秒，并将剩余时限传入核心 HTTP 请求。完整结果缓存 1 分钟；成功元数据按资产 URL、ID、更新时间和大小缓存 15 分钟，资产变化后重新读取。单项失败保留已确认的最高版本候选，列表失败可返回先前确认的候选，但明确标记检查未完成；没有完整检查结果时不显示“已是最新版本”。界面行为见 [flclash.md](flclash.md#导航与交互约定)。

## 构建与发布

[app/build.gradle.kts](app/build.gradle.kts) 从本地 `app/libs/libbox.aar` 和 Flutter 生成的 Maven 仓库消费依赖。核心必须包含 [libbox.md](libbox.md) 的当前扩展。[setup-flutter](.github/actions/setup-flutter/action.yml) 统一固定 Flutter 3.47.6，并以 `--enforce-lockfile` 读取已提交的依赖锁文件；该 SDK 满足 prism_ui 0.1.2 的 Flutter 3.47 最低要求，界面来源和本地依赖见 [flclash.md](flclash.md#参考版本与来源清单)。Flutter 验证在 Gradle 启动前显式安装该 SDK 使用的 NDK 28.2.13676358，避免 AAR 配置阶段触发隐式安装；它与 libbox 使用的 NDK 独立。CI 先构建 AAR 到 `flutter/build/host/outputs/repo`，再构建 Android；应用使用 AAR 时不需要再安装 Flutter SDK。

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

Release 独立并行构建 ARM64 libbox 和 Flutter Release AAR；Flutter 使用 `--no-debug --no-profile`，只生成打包所需的 Release 库。应用构建从**同一次运行**下载两个 AAR 产物，再准备既有签名密钥并执行保留 R8 的 `assembleOtherRelease`。它不调用或等待测试工作流，也不执行 Go、Flutter 或 JVM 测试；验证统一由 [newui.yml](.github/workflows/newui.yml) 在代码推送时运行。中间 AAR Artifact 保留一天。[verify_release.py](tools/verify_release.py) 在 APK 上传前校验唯一 APK、最低 API 24、ARM64、应用包名/版本/标签、启动 Activity、MITM 模块 assets、退役采集器及 Xposed 模块元数据未打包、签名。APK Artifact 名为 `B-box-<版本>-arm64-v8a-release`，保留 30 天。

`publish_release` 默认开启；设为 `false` 时仍构建、校验并上传 Actions Artifact，仅跳过发布。[publish_release.py](tools/publish_release.py) 要求 Release tag 严格等于 `VERSION_NAME`（不加 `v`）且指向本次源码 SHA；已有 tag 指向其他提交时失败，不移动版本标签。同一提交重跑可重传资产。发布 APK 及 `B-box-version-metadata.json`，alpha/beta/rc 标为预发布。

## 验证与历史记录

本节依次维护验证入口、已配置的检查、当前源码的实际结果和历史运行索引。已配置的检查不是通过声明；记录实际结果时附源码 SHA、Actions 链接和未验证范围，不能沿用旧裁剪 APK、旧 Compose 或其他提交的验证记录。核心回归文件的覆盖范围见 [libbox.md](libbox.md#回归文件覆盖)，Flutter 测试与视觉验证见 [flclash.md](flclash.md#验证与升级参考版本)。

### 验证入口

| 入口 | 配置的验证范围 |
| --- | --- |
| [verify_newui.py](tools/verify_newui.py) | Flutter 字形/导入/导出、Android 入口、旧 UI/更新依赖、退役抓包/特权增强实现残留及 Root 事务编号的静态检查 |
| [newui.yml](.github/workflows/newui.yml) | 各分支代码 push 自动触发，也支持目标为 `dev` / `newui` 的 PR 及手动触发；仅 Markdown 的 push/PR 跳过。统一运行 Root CA 挂载/补查、Go 扩展/HTTP/MITM/重定向布局、Flutter 分析/交互/布局、JVM、`play` 编译及 API 24/31/36 smoke，API 36 另运行 `google_apis_ps16k`；同时构建验证所需的 AAR 和 x86_64 模拟器 APK |
| [build-release.yml](.github/workflows/build-release.yml) | 仅手动触发；独立并行准备核心/Flutter Release 库，构建签名 ARM64 APK、校验安装身份/产物及可选发布，不运行测试 |

### JVM 回归

以下用例由统一工作流 Android job 的 `:app:testOtherDebugUnitTest` 运行。调用计数和虚拟时间断言不代表设备上的实测耗时、内存或帧率，也不代替真实 Wi-Fi/蜂窝切换、热点及性能验收。

- **服务与配置应用：** [ServiceStartResultTest.kt](app/src/test/java/io/nekohasekai/sfa/flutter/ServiceStartResultTest.kt)、[BoxSettingsTest.kt](app/src/test/java/io/nekohasekai/sfa/flutter/BoxSettingsTest.kt)、[LogBufferTest.kt](app/src/test/java/io/nekohasekai/sfa/backend/log/LogBufferTest.kt) 和 [BoxQrScannerTest.kt](app/src/test/java/io/nekohasekai/sfa/flutter/BoxQrScannerTest.kt) 覆盖服务启动确认/失败/超时、设置、日志等级/边界和 QRS；[ServiceSettingsChangeTest.kt](app/src/test/java/io/nekohasekai/sfa/flutter/ServiceSettingsChangeTest.kt) 覆盖服务开关保存后的启动确认、失败/超时/取消恢复及恢复错误保留；[ProfileChangeTest.kt](app/src/test/java/io/nekohasekai/sfa/database/ProfileChangeTest.kt) 覆盖配置持久化失败、应用超时/取消、恢复确认及恢复错误保留。
- **版本检查：** [ReleaseCheckerTest.kt](app/src/test/java/io/nekohasekai/sfa/flutter/ReleaseCheckerTest.kt) 用虚拟时间检查总时限、并发上限、失败候选保留、缓存失效及取消后重试。
- **仪表盘与应用列表缓存：** [DashboardConfigCacheTest.kt](app/src/test/java/io/nekohasekai/sfa/flutter/DashboardConfigCacheTest.kt) 通过调用计数覆盖重复刷新不再读取/哈希/解析、地址变化、配置切换、写入/回滚、运行快照更新及错误/取消重试；[AppListCacheTest.kt](app/src/test/java/io/nekohasekai/sfa/flutter/AppListCacheTest.kt) 通过查询/名称读取计数覆盖隐藏应用不读取名称、筛选切换复用、刷新及查询策略/语言失效、失败/取消重试和并发查询合并。
- **日志与可见性轮询：** [LogStreamTest.kt](app/src/test/java/io/nekohasekai/sfa/flutter/LogStreamTest.kt) 覆盖日志快照、增量、淘汰边界、等级更新、清空、重新订阅和缓冲重置，并检查 3000 条缓冲新增一条时只编码一条；[VisiblePollingTest.kt](app/src/test/java/io/nekohasekai/sfa/flutter/VisiblePollingTest.kt) 用虚拟时间检查不可见/后台/熄屏时零采样、恢复立即采样及取消在途任务。
- **连接与网络刷新：** [ConnectionStreamTest.kt](app/src/test/java/io/nekohasekai/sfa/flutter/ConnectionStreamTest.kt) 检查增量计数、最终关闭计数、历史淘汰、DNS 过滤、快照隔离及 3000 条记录经过状态合并和恢复订阅后的消息内容；[NetworkRefreshTest.kt](app/src/test/java/io/nekohasekai/sfa/flutter/NetworkRefreshTest.kt) 用虚拟时间覆盖网络通知、十秒/六十秒补查、前后台/熄屏取消和突发通知合并。

### Android 仪器测试

以下用例使用实际 libbox AAR，由 smoke 矩阵的原生组在每个环境运行。模拟器中的自有客户端和本地测试服务器不代表第三方目标应用信任、真实网络或 Root 模块已经实机通过。

- **配置应用与恢复：** [ProfileRecoveryTest.kt](app/src/androidTest/java/io/nekohasekai/sfa/flutter/ProfileRecoveryTest.kt) 覆盖实际 libbox 二进制导入、查询策略持久化、端口占用导致的 JSON 编辑/配置切换/手动与自动订阅/完整备份恢复失败，以及停服和未选中配置更新的服务状态；另覆盖启停期间拒绝内容/备份修改、缺定位权限的重载回滚、配置表单内容与元数据共同恢复、下载等待时编辑/关闭自动更新，以及 URL/删除/会话冲突。`failedSelectionRestoresThePreviousSelectionAndService` 连续五轮核对选配置失败恢复、停止释放核心及重新启动，防止服务清理竞态污染后续用例。
- **配置时间与日志级别：** `localProfileTimesFollowSuccessfulContentChanges` 覆盖本地创建/内容编辑的时间记录、无变化保存和重命名不刷新时间、应用失败恢复原内容与时间；二进制订阅测试另检查手动 JSON 编辑保留导入的订阅时间。`logLevelFollowsSuccessfulConfigurationReloads` 覆盖真实内核日志订阅的默认级别、运行中修改 `log.level` 后刷新，以及移除该字段后恢复当前核心的默认值。
- **订阅下载、备份回滚与删除：** 覆盖超过旧 16 MiB 阈值的订阅完成 URL 导入、手动及自动更新并保留服务状态，以及协程取消阻塞中的原生下载。取消用例分别从 IO/Default 调度器发起，共连续执行 20 次，再验证后续请求仍能成功；取消必须结束 JNI 调用且不能作为普通网络失败取消父任务。其他用例覆盖备份文件或设置回滚失败后保留暂存原件、继续其他回滚且不启动服务；运行/停止时拒绝删除选中配置、正常删除未选中配置，以及删除等待互斥锁期间选择变化的复核。下载的分块、gzip、大正文、取消和重定向循环由 [核心订阅测试](libbox/ui_profile_test.go) 覆盖。
- **设置持久化：** [PreferencePersistenceTest.kt](app/src/androidTest/java/io/nekohasekai/sfa/database/preference/PreferencePersistenceTest.kt) 使用实际 Room 检查普通设置重新读取、提交失败后的缓存恢复、后续排队值保留、取消与回滚顺序，以及 UTF-8 字符串集合。查询策略回归在每次切换 Root/Shizuku 并刷盘后重新打开 Room 数据库读取持久值，同时核对状态流，不以读取同一内存缓存代替持久化验证。
- **LAN 缓存：** `lanCacheTracksEqualSizeWritesSuccessfulReloadsAndRecovery` 使用实际文件和 libbox，覆盖同大小同时间戳写入、保存与运行配置分离、成功重载、失败恢复和停服后的 LAN 状态。
- **记录设置与 MITM CA：** `captureSettingsAreLocalAndCheckRevisions` 使用实际 libbox 和 AtomicFile，覆盖 version 3 设置迁移/保存/重新打开、修订冲突、本机 HTTPS 字段移除和退役选项拒绝。[MitmCertificateTest.kt](app/src/androidTest/java/io/nekohasekai/sfa/flutter/MitmCertificateTest.kt) 使用实际 Android Keystore 与 gomobile，验证 CA 自签名、预哈希签名、私钥不可导出、无自动生成、修订冲突/轮换、AtomicFile 恢复、密钥缺失、大小限制，以及 CRT/模块仅含公钥和完整脚本；不安装设备证书或 Root 模块。
- **路由 MITM：** `routedMITMUsesKeystoreAndTheOriginalOutbound` 将实际 Android TLS 客户端经 mixed CONNECT 入口送入本地服务，使用 Keystore CA 解密、原 selector/direct 出站与独立证书库验证测试服务器。覆盖普通 HTTPS、开启 TCP 解密但无 ALPN 的 HTTP/1.1，以及服务器先发数据的二进制 TLS/TCP；核对 URL、原路由及 TLS 元数据，HTTP 检查重复 Cookie/cURL，TCP 检查双向字节、完整性与 cURL 拒绝，并验证重载停抓。测试服务器证书由 smoke 脚本提供，见 [模拟器 smoke 矩阵](#模拟器-smoke-矩阵)；自有 CA 客户端测试不等于第三方信任库、CT/ECH 或 Root 模块的兼容性验收。
- **HTTP 改写、资源导入与 Cron：** `profileHTTPRewritingUsesKeystoreRoutingAndReloadRecovery` 使用实际 gomobile、Keystore、mixed CONNECT 和 HTTP 服务器，覆盖平级 MITM/脚本与改写、HTTPS 离线 Mock、跨域映射重新匹配路由、响应改写、停记录后继续处理及应用失败恢复；资源用例先通过实际 `content://` 文档流完成本地文件和脚本导入，再检查 999 状态、MIME、后查找/内部反向引用 → jq → 脚本的顺序及本地文件更新。`localResourceImportsKeepRevisionsRecoverFailuresAndCleanCancelledCopies` 覆盖重名资源、修订冲突、应用失败恢复、文档读取失败和阻塞读取期间取消后的清理。`independentCronUsesRoutingAndStopsWithTheService` 覆盖定时路由请求、抓包启停、非法 Cron 保存拒绝及移除配置后不再请求。本地订阅/定时请求服务器按连接处理对端断开，允许服务重载或停止时取消在途 Cron 请求后继续接受新请求；监听失败及其他异常仍使测试失败，响应内容、请求次数、配置恢复和停止后的不再调度继续由原有断言检查。
- **脚本：** `httpScriptsUseRoutedClientTimersAndPersistentNamespaces` 使用仅当前路由可解析、也匹配脚本范围的 HTTP 目标，检查异步回调及防递归、定时器、磁盘快照、规则改名/重载/清空记录后保留值、命名空间隔离、默认联网及显式空允许列表禁止联网。
- **WARP MASQUE：** [WarpMasqueConfigTest.kt](app/src/androidTest/java/io/nekohasekai/sfa/flutter/WarpMasqueConfigTest.kt) 使用临时 ECDSA 密钥，检查实际 libbox AAR 接受 HTTP/2、HTTP/3 WARP MASQUE 配置，格式化保留凭据及隧道地址，并拒绝非法账号配置；该用例不连接 Cloudflare。核心握手和真实 IP 栈传输的回归范围见 [libbox.md](libbox.md#cloudflare-warp-masque)。

### 模拟器 smoke 矩阵

模拟器镜像名称依据 [Google SDK 系统镜像清单](https://dl.google.com/android/repository/sys-img/google_apis/sys-img2-1.xml)；16 KiB 测试方式见 [Android 官方文档](https://developer.android.com/guide/practices/page-sizes)。smoke 读取设备实际页大小，普通镜像要求 4096、`ps16k` 要求 16384，不能仅凭镜像名称记录通过。页大小优先使用 `getconf PAGE_SIZE`；旧系统缺少该命令时从 `/proc/self/smaps` 的 `KernelPageSize` 读取，并在结果中记录采集来源，不按 Android 版本猜测。所有矩阵均为临时 x86_64 模拟器，不代表 ARM64 实机或系统 CA 模块安装已验收。

统一入口为 [android_smoke.py](tools/android_smoke.py)：安装应用和仪器测试 APK，分别重置应用数据后运行 UI smoke 与原生回归，前一组失败不跳过后一组。UI 覆盖首次启动、远程 JSON 导入、VPN 授权、启停、配置文件与实际内核分组同页、底栏设置入口及进程重启后配置保留；原生组运行全部仪器测试并拒绝零测试结果。脚本仅用于 CI 模拟器，不能对保有用户数据的设备执行。

- **结果与日志：** `build/android-smoke/` 保存截图、logcat、失败堆栈、仪器测试输出及带源码 SHA、实际 SDK/版本/构建指纹/页大小的 `results.json`，超时也保留日志。每组测试在应用启动前持续采集 main/system/crash/events 日志，结束后保存 Activity 和内存状态，API 30+ 另保存该应用的系统退出记录；`am start -W` 的输出单独保存，避免启动过程被末尾日志截断。
- **启动与导入：** 启动阶段允许截图后关闭一次 Google APIs 模拟器的 Pixel Launcher ANR 弹窗，再重新打开 B-box；不关闭 B-box 自身的 ANR 或其他错误。导入 smoke 等待弹层保存按钮在连续两次窗口观察中位置稳定后点击，记录实际点击边界；保存后须返回带“启动”按钮及导入配置名的仪表盘，不能以表单内的预填名称作为导入成功依据。
- **MITM 测试服务器：** 脚本在 CI 临时目录生成独立测试服务器证书，经 ADB reverse 提供 HTTP 和原始流两个 TLS 端点，只把公钥 CA 传给仪器测试；私钥不写入仓库、设备或上传产物，套件结束删除。单独运行路由 MITM 用例需要脚本提供的 `mitmOriginPort` / `mitmTCPOriginPort` / `mitmOriginCA` 参数。
- **存储与内存：** 模拟器数据分区统一设为 4 GiB，以容纳完整核心的测试 APK 及安装数据。API 36 / 16 KiB 另显式分配 4 GiB RAM：默认镜像曾在冷启动期间因内存水位不足杀掉前台 B-box，系统退出记录为 `LOW_MEMORY`；该矩阵用于页大小兼容验证，不是低内存压力测试。smoke job 不编译原生库，下载镜像前移除该临时 runner 上不使用的预装 NDK，为系统镜像和数据分区留空间；构建 job 的 NDK 不受影响。

### Root CA 回归

统一工作流的 `root-ca` job 在独立 Linux PID/mount namespace 中运行 [test_mitm_ca.py](tools/test_mitm_ca.py)，使用 BusyBox 和导出的原始脚本，覆盖 APEX 延迟就绪、旧系统缺少 APEX、hash 冲突和原证书保留、已有二级后代/隐藏模块路径、同一 namespace 的重新挂载、准备失败、service 重试和禁用退出。该测试只在 Actions 运行，不对本机 Android 做 Root 挂载，也不代表真实 Magisk/KernelSU/APatch 安装与重启已验收。

### 当前验证记录

2026-11-14，配置/代理合并、设置底栏与仪表盘铅笔入口的验证源码为 `2adc1b653bd98acc68929deec19f77dfa04f0022`，对应 [Unit tests and regression](https://github.com/giturass/B-box/actions/runs/43562639502)。core、Root CA、Flutter、Android 构建及四组 smoke 合计 8 个作业全部通过；后续仅补充文档的 Markdown 提交不改变上述验证源码。

- core 的匹配内核 Android ingress-discard、全部 `TestUI`、不含 QUIC 与 `with_quic` 两种 `^TestWARP` race、`with_quic` 的 `^TestUIMASQUE`、路由/HTTP/订阅 race、完整 `experimental/httpdebug` race 和路由接入回归步骤全部通过。
- Root CA namespace 回归的 6 项测试通过。
- Flutter 分析、259 项交互/布局及 ARM64 Debug/Release Flutter AAR 构建通过；分析提示、配置横向滑动与设置导航、铅笔入口的截图检查见 [界面验证记录](flclash.md#当前验证记录)。
- 已读取 [native-unit-tests](https://github.com/giturass/B-box/actions/runs/43562639502/artifacts/14100589285) 的 13 份 XML，56 项 JVM 测试通过，无失败、错误或跳过；`play` Kotlin 编译和 x86_64 应用/仪器测试 APK 构建通过。

已逐项核对以下 Artifact 的 `results.json` 与 `native-tests.txt`：源码 SHA 均与上述提交一致，每组 UI smoke 和全部 41 项原生测试通过，原生状态码全部成功，无失败或跳过；页大小为设备实际读取值。

| 环境与结果产物 | 实际页大小 | 采集来源 |
| --- | --- | --- |
| [API 24 / Android 7 / google_apis](https://github.com/giturass/B-box/actions/runs/43562639502/artifacts/14101675282) | 4096 字节 | `/proc/self/smaps` |
| [API 31 / Android 12 / google_apis](https://github.com/giturass/B-box/actions/runs/43562639502/artifacts/14100688625) | 4096 字节 | `getconf` |
| [API 36 / Android 16 / google_apis](https://github.com/giturass/B-box/actions/runs/43562639502/artifacts/14101935906) | 4096 字节 | `getconf` |
| [API 36 / Android 16 / google_apis_ps16k](https://github.com/giturass/B-box/actions/runs/43562639502/artifacts/14101143038) | 16384 字节 | `getconf` |

本机仅执行差异、源码约束、Python 脚本语法及文档链接等静态检查，编译及 Go/Flutter/JVM/Android 测试均在 Actions 运行。未覆盖范围：

- 本次未触发签名 ARM64 Release 工作流，也没有发布；Flutter AAR 和 x86_64 测试 APK 不等于可安装的 ARM64 Release。
- 尚未验收 ARM64 实机上的配置滑动、设置导航、仪表盘编辑、真实远程会话或性能；Flutter 截图使用测试桥接数据。
- 尚未验收 Cloudflare 真实注册/连接、真实网络下的传输兼容性、ARM64 实机网络切换及实际 Root 管理器的 CA 模块安装与重启。

### 历史验证记录

下表索引此前在 Actions 上记录过的运行，只用于查询，不是当前源码的通过声明。逐次的检查项、产物链接、失败原因和未覆盖范围保留在 Git 历史，可用 `git show 06eed8f5f830a90b9853995ced3b2e211deca628:app.md` 查看整理前的完整记录。未注明时 smoke 为该行所列环境的 UI smoke 加原生测试；Flutter 数量见 [flclash.md](flclash.md#历史验证记录)。

| 日期 | 主题 | 验证源码 | Actions | 范围与备注 |
| --- | --- | --- | --- | --- |
| 2026-11-14 | 代码整理与 MASQUE 网络重置修复 | `06eed8f5f830a90b9853995ced3b2e211deca628` | [43523435178](https://github.com/giturass/B-box/actions/runs/43523435178) | 8 个 job 通过；56 项 JVM；API 24/31/36 及 16 KiB 四组各 41 项原生测试；同源码 core 第二次重跑也通过，未触发 ARM64 Release |
| 2026-11-13 | Cloudflare WARP MASQUE | `e0d77c9a5507a3503c5942c1a0b38bd702dbfd5e` | [43434680819](https://github.com/giturass/B-box/actions/runs/43434680819) | 8 个 job 通过；56 项 JVM；API 24/31/36 及 16 KiB 四组各 41 项原生测试。同一源码的 [ARM64 Release](https://github.com/giturass/B-box/actions/runs/43435512552) 以 `publish_release=false` 构建并校验签名 APK，未发布 |
| 2026-11-13 | UI 性能优化 | `efe9028214cd52ef03f5a8b1e0143006be5ba6ad` | [43407927934](https://github.com/giturass/B-box/actions/runs/43407927934) | 8 个 job 通过；56 项 JVM；四组各 39 项原生测试 |
| 2026-11-11 | Android 协议、资源导入与正文读取修复 | `d3a1f178d6d2375291b698d7200ef9ab8ff98260` | [42914488877 第二次尝试](https://github.com/giturass/B-box/actions/runs/42914488877/attempts/2) | API 24 job 在同一源码重跑后通过；56 项 JVM；四组各 39 项原生测试 |
| 2026-11-11 | 路由、正文准入与资源缓存修复 | `6414cea3bfca092b8f5c146552f6c76738cb4bf1` | [42848220348](https://github.com/giturass/B-box/actions/runs/42848220348) | 56 项 JVM；四组各 38 项原生测试 |
| 2026-11-10 | 统一请求查看器 | `3922cd606f520f6581bd4fd0a7d211a68a24ca23` | [42760227283](https://github.com/giturass/B-box/actions/runs/42760227283) | 56 项 JVM；四组各 37 项原生测试 |
| 2026-11-09 | 特权增强移除 | `95a6f002170628911e2687af6e2dd39b3691bb3e` | [42570495902](https://github.com/giturass/B-box/actions/runs/42570495902) | 56 项 JVM；四组各 37 项原生测试。前序 [42563166489](https://github.com/giturass/B-box/actions/runs/42563166489) 与诊断运行 [42567022417](https://github.com/giturass/B-box/actions/runs/42567022417) 未通过，不计入 |
| 2026-11-08 | 持久化、配置恢复与后台检查优化 | `3ea124a980b0c8bdd205cc8ebdf79633a991210b` | [42477676213](https://github.com/giturass/B-box/actions/runs/42477676213) | 两个失败 job 在同一源码重试后通过；67 项 JVM；四组各 40 项原生测试。[ARM64 Release](https://github.com/giturass/B-box/actions/runs/42476210954) 在 `82ffed86f57891f68fd055d707124a1b1714ba14` 上以 `publish_release=false` 构建，未发布 |
| 2026-11-07 | 脚本修复合并 | `23fab54581bcbdfa06fd74fd3ca674c141386285` | [42357641137](https://github.com/giturass/B-box/actions/runs/42357641137) | 工作流结论为 `cancelled`，API 31 smoke 未完成，不计为 smoke 通过；61 项 JVM |
| 2026-11-07 | 一元 gRPC 与脚本 API 对齐 | `3f583b602fbde98814f90c5bbc0e4a1d41131890` | [42355749078](https://github.com/giturass/B-box/actions/runs/42355749078) | 61 项 JVM；仅 API 31，28 项原生测试 |
| 2026-11-07 | 订阅下载、备份回滚与配置删除修复 | `9eda9945d23ca6916844f2c294cfd314c1185cb0` | [42300191496](https://github.com/giturass/B-box/actions/runs/42300191496) | 50 项 JVM；仅 API 31，25 项原生测试。同一源码的 [ARM64 Release](https://github.com/giturass/B-box/actions/runs/42300615184) 以 `publish_release=false` 构建，未发布 |
| 2026-11-06 | 死代码清理 | `adb57d82be837bbe366dab0779ce0af3d5e2f57c` | [42242606570](https://github.com/giturass/B-box/actions/runs/42242606570) | 50 项 JVM；仅 API 31，20 项原生测试 |
| 2026-11-05 | Surge HTTP 处理对齐 | `75897bd75b15ead454e3fcfeac383f303de15108` | [42000793483](https://github.com/giturass/B-box/actions/runs/42000793483) | 50 项 JVM；仅 API 31，20 项原生测试 |
| 2026-11-05 | Prism UI 升级 | `d9254711ccf34cda547d2f2287b0e0a508e861a6` | [41982716632](https://github.com/giturass/B-box/actions/runs/41982716632) | 50 项 JVM；仅 API 31，20 项原生测试 |
| 2026-11-05 | MITM 与 Surge 对齐 | `5e40bd30a3695cb2e7ed84716955fc64e4a08495` | [41964904848](https://github.com/giturass/B-box/actions/runs/41964904848) | 50 项 JVM；仅 API 31，20 项原生测试 |
| 2026-11-05 | Root CA 与服务恢复修复 | `4d7dabb9b804706d3dd019b92be85fd4f68ba89c` | [41870624196](https://github.com/giturass/B-box/actions/runs/41870624196) | 50 项 JVM；仅 API 31，20 项原生测试。前序 [41857802322](https://github.com/giturass/B-box/actions/runs/41857802322) 的 API 31 回归失败，不计入 |
| 2026-11-04 | MITM 连接与正文处理优化 | `aa734faab8ae5edf76dba56b88aa5e8986fa47dc` | [41833768526](https://github.com/giturass/B-box/actions/runs/41833768526) | 50 项 JVM；仅 API 31，19 项原生测试。前序 [41818907770](https://github.com/giturass/B-box/actions/runs/41818907770)、[41820772969](https://github.com/giturass/B-box/actions/runs/41820772969) 失败，不计入 |
| 2026-11-04 | 界面刷新与存储优化 | `ef219625437f64eb7c6fc349d4617617bbea8dc8` | [41760772704 第二次尝试](https://github.com/giturass/B-box/actions/runs/41760772704/attempts/2) | API 24 job 在同一源码重跑后通过；50 项 JVM；API 24/31/35 各 19 项原生测试 |
| 2026-11-03 | 独立 MITM/TCP/Cron | `339ecfc8bf42c4a07d800a6b8024d2e02a466ae9`（核心与 Flutter）、`e1bbf0fcb1e3b06d4f4912943f3aa3487892b169`（Android） | [41637368901](https://github.com/giturass/B-box/actions/runs/41637368901)、[41636466736](https://github.com/giturass/B-box/actions/runs/41636466736) | 当时为两条独立工作流；44 项 JVM；API 24/31/35 各 19 项原生测试 |
| 2026-11-03 | JSON 配置改写 | `e62ddbe724a1b5f61e3bff96e48b1ba8e82c94fc` | [41575286785](https://github.com/giturass/B-box/actions/runs/41575286785)、[41575670538](https://github.com/giturass/B-box/actions/runs/41575670538) | 两条独立工作流；44 项 JVM；API 24/31/35 各 18 项原生测试 |
| 2026-11-03 | MITM 分支 | `b3dfe817c7838a7485497eced0aa1bcebe1ae6d6`（核心与 Flutter）、`8cc2ca6b64963891d479a671b4a6a58521a16ba1`（Android） | [41549420394](https://github.com/giturass/B-box/actions/runs/41549420394)、[41548479075](https://github.com/giturass/B-box/actions/runs/41548479075) | 两条独立工作流；44 项 JVM；API 24/31/35 各 17 项原生测试。[ARM64 Release](https://github.com/giturass/B-box/actions/runs/41546076303) 在 `8869e6992a59327c11d39ad98457a22f402a23cb` 上以 `publish_release=false` 构建，未发布 |

原 `MODIFICATIONS.md` 与 `NEWUI.md` 已按领域归入这四份文档。历史 MOD-001～004、007～008、012 的 Compose 界面由 Flutter 替代；MOD-005/006/009 的品牌、安装身份及发布约定在本文继续维护；MOD-011/015/016 的删除项以本次迁移后的现状为准；MOD-013/014 的核心与工具裁剪已回退；MOD-017 的 UI 适配见 [flclash.md](flclash.md)。旧实现、逐次构建日志与产物哈希可通过 `git log --all -- MODIFICATIONS.md NEWUI.md` 和对应提交的文件内容查询，不再混入当前项目约束。
