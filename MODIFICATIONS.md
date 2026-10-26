# B-box：修改清单与维护指南

## 基线与范围

| 项目 | 当前值 |
| --- | --- |
| 应用名称 / APK 前缀 | `B-box` |
| Gradle 根项目名 | `B-box` |
| 本地项目目录 | `~/project/B-box` |
| 仓库 | https://github.com/giturass/B-box |
| 上游 | https://github.com/SagerNet/sing-box-for-android ，`dev` 分支 |
| 比较基线 | `a548a7903e96266a0046018562c73b192edbfc18` |
| 版本 / versionCode | `1.15.0-alpha.13` / `747` |
| 安装包名 / applicationId | `io.ericlee.bfa` |
| 源码 namespace | `io.nekohasekai.sfa` |
| 更新日期 | 2026-10-27 |

MOD-017 正在替代 MOD-001～004、MOD-007～008、MOD-012 的 Compose 界面；这些历史条目用于记录旧实现，不再定义当前启动器的界面。

本文描述当前有效改动，历史验证记录仅对应各自源码版本。路由、VPN、远程控制、配置导入导出、Tailscale/Taildrop、终端及普通 Root/Shizuku 能力继承上游；Xposed/LSPosed 模块与特权增强已按 MOD-011 移除。MOD-013、MOD-014 已回退，核心恢复上游完整功能；MOD-015、MOD-016 继续有效。核心 AAR 不在 Git 中，本地构建与 CI 核心来源应分别核验。

## 修改索引

路径前缀为 `app/src/main/java/io/nekohasekai/sfa/`。

| ID | 修改 | 主要文件 |
| --- | --- | --- |
| MOD-001 | 首页浮动控制入口 | `compose/MainActivity.kt`、`compose/component/ServiceStatusBar.kt`、`compose/screen/dashboard/DashboardScreen.kt` |
| MOD-002 | 配置卡片操作布局 | `compose/screen/dashboard/ProfilesCard.kt` |
| MOD-003 | 移除独立连接统计卡片 | `DashboardViewModel.kt`、`DashboardCardRenderer.kt`、`DashboardSettingsBottomSheet.kt` |
| MOD-004 | 1.12.23 样式的模式按钮 | `compose/screen/dashboard/ClashModeCard.kt` |
| MOD-005 | B-box 项目、应用与产物命名 | `settings.gradle.kts`、语言资源、Gradle、README、构建工作流 |
| MOD-006 | ARM64 Release 构建 | `.github/workflows/build-release.yml`、`app/build.gradle.kts` |
| MOD-007 | FlClash 风格服务按钮 | `compose/component/ServiceStartButton.kt`、`ServiceStatusBar.kt` |
| MOD-008 | 合并流量统计并与调试卡片配对 | `TrafficCard.kt`、`DashboardCardSettings.kt`、仪表盘渲染与设置 |
| MOD-009 | 独立安装包名与分支更新源 | `app/build.gradle.kts`、Manifest、服务/安装广播、`GitHubUpdateChecker.kt` |
| MOD-011 | 移除 Xposed/LSPosed 与特权增强 | Manifest、Gradle、设置与导航、模块源码/资源、安装与应用查询 |
| MOD-012 | 节点卡片尺寸与选中态优化 | `compose/screen/dashboard/GroupsCard.kt` |
| MOD-013 | 已回退：ARM64 核心组件裁剪 | 核心恢复上游完整功能，删除裁剪补丁与专用校验器 |
| MOD-014 | 已回退：移除 App 的 Tailscale 与终端 | 恢复导航、工具与设置页、Manifest、Root 后端、Ghostty 与语言资源 |
| MOD-015 | 移除 Android 5/6 与 legacy 核心 | `app/build.gradle.kts`、`src/minApi21`、`src/otherLegacy`、`patches/libbox-no-legacy.patch`、工作流 |
| MOD-017 | Flutter 全界面迁移与 sing-box 桥接 | `flutter/`、`flutter/*.kt`、`NEWUI.md`、`third_party/flclash/` |
| MOD-016 | 移除应用更新提示与设置入口 | `MainActivity.kt`、`AppSettingsScreen.kt`、`SettingsScreen.kt`、`UpdateWorker.kt`、设置与导航 |


## MOD-017：FlClash Flutter 界面与 sing-box 能力边界

参考版本固定为 FlClash v0.9.2，提交 `7fd417fb9a9a277c6a9339a8ac87f907df7c25f7`。直接使用该版本共享 Flutter 控件、字形、形状、字体、动效和导航；服务按钮与出站模式按钮由对应仪表盘实现适配。页面业务数据来自 B-box 原有 Room 与 libbox，不依赖 Mihomo。

`compose.MainActivity` 和 `compose.ShareActivity` 都继承 `BoxActivity`。旧 Compose 页面源码仍供后端 ViewModel/DTO 所在模块编译，但不提供导航入口。OpenConnect WebView 使用平台视图嵌入新页面；它复用原浏览器控制器，不调用旧 Compose 对话框。

配置保存与导入调用 `Libbox.checkConfig`；只显示内核返回的路由模式。不能手动选择自动测速组。新 UI 不提供 Clash YAML、覆写脚本、Mihomo 提供者或其他 Clash 专用开关。sing-box 扩展功能统一放在 Flutter 工具页。

Gradle 从 `flutter/build/host/outputs/repo` 消费 Flutter AAR。先用 Flutter 3.47.6 执行 `flutter pub get` 和 `flutter build aar --no-profile`，再构建 Android；CI 已包含这些步骤。本机不执行构建，按项目 AGENTS.md 在 Actions 验证。Flutter 无法发布 x86 AOT，因此通用 ABI 列表保留 ARMv7、ARM64、x86_64，发布工作流仍限定 ARM64。

当前验证记录见 NEWUI.md；不能把共享源码复用或编译成功表述为已经完成逐像素验收。


## MOD-001：仪表盘底部控制

**上游行为：** 运行时显示整条服务状态栏，连接/代理组按钮显示数量；停止时显示单独启动 FAB。状态栏可出现在多个一级页面。

**当前行为：**

- 手机底部布局改为三块浮动控件：连接、代理组、服务启动/停止。前两块用文字标签替代数字计数。
- 控件限制在真正的仪表盘 route；配置子页面、其他一级页不显示。远程会话状态条也限制在仪表盘。
- 活动连接仅在 `Status.Started` 时显示；代理组还要求 `hasGroups`。没有对应入口时保留占位，稳定服务按钮位置。
- 原先固定宽度的服务按钮已由 MOD-007 的 FlClash 风格按钮替代。停止时 56dp 方形悬浮按钮，运行时按计时内容展开，最多 220dp，并根据手机剩余空间收窄。高度 56dp、圆角 16dp。
- 停止时启动服务；其他状态调用现有服务切换逻辑；`Stopping` 时禁用按钮。已启动时显示暂停双竖线和 `HH:MM:SS`；图标与时间的内部位置不受当前秒数长度影响。实际点击行为仍为停止，不新增暂停核心功能。
- 大屏导航栏保留原有远程会话分支，本地启动/运行按钮改用同一 `ServiceStartButton`。不要将手机三个按钮描述成所有屏幕统一布局。
- 仪表盘为底部控件预留 88dp，避免列表尾部被遮挡。
- 新资源键：`dashboard_active_connections`、`dashboard_proxy_groups`；提供英文和简体中文，其他语言使用默认资源回退。

**维护约束：** 保留本地/远程会话分支；不要绕过原有 VPN 权限与服务启动流程；不要把 UI 数量显示移除误认为底层连接/代理组数据不再需要。

**回归：** 未选配置、已选配置且停止、启动中、已运行、停止中；有/无代理组；切到日志/连接/设置/配置子页；返回首页；远程会话；手机/平板布局；计时器与底部遮挡。

## MOD-002：配置卡片

**上游行为：** 标题栏只有添加按钮；编辑、更新、分享操作位于卡片底部。

**当前行为：** 标题栏右侧按分享、编辑、添加排列。有选中配置才显示分享与编辑，空配置列表仍可添加。标题单行省略，避免挤压操作区。远程配置更新按钮放在配置类型/更新时间的信息行右侧，保留更新中禁用、加载指示和成功勾选反馈。

**维护约束：** 分享、保存文件、JSON、URL、二维码等原有回调仍被保留；不要因布局移动丢失分支。更新按钮仅适用于 `TypedProfile.Type.Remote`。

**上游同步：** 自 `1.15.0-alpha.13` 起，在仪表盘更新当前选中的远程配置且内容发生变化时，沿用上游逻辑立即应用到运行中的服务：服务模式相同则重载，模式变化则停止后重新走启动流程。

**回归：** 空列表、本地/远程配置、超长名称、更新成功/失败、各类分享导出、配置选择和编辑；运行中更新选中配置时的重载、模式切换及 VPN 权限流程。

## MOD-003：连接统计卡片移除

**上游行为：** 仪表盘有独立入站/出站连接数量卡片，与 Debug 卡片组成 Statistics 双列分组。

**当前行为：** 删除独立 `ConnectionsCard`、`CardGroup.Connections` 及卡片专用 `connectionsIn`/`connectionsOut` UI 字段。同步移除渲染、默认顺序、可见性及仪表项设置入口。`CardPairGroup.Statistics` 当前用于 Debug 与合并后的流量统计配对，见 MOD-008。

**兼容性：** `DashboardCardSettings.kt` 只接受当前枚举名及两种旧流量卡片名，旧设置中的 `Connections` 在加载顺序和禁用项时被忽略；没有新增数据库迁移。活动连接页面/弹层、`connectionsCount` 和底层连接数据仍保留。

**回归：** 从包含 `Connections` 的旧卡片顺序/隐藏项升级；重置仪表项；拖动排序；显示/隐藏 Debug；流量卡片配对；打开活动连接列表。

## MOD-004：1.12.23 模式按钮

**核验基线：** sing-box `v1.12.23` 的 `clients/android` 子模块指向 `eb87216961321de1802e1355c470242f2ed5faa8`。按钮参考该提交的 `view_clash_mode_button.xml`、`bg_rounded_rectangle*.xml` 和 `OverviewFragment.kt`。

**当前行为：** 仅模式选择按钮（rule/global/direct）使用旧版独立矩形样式：4dp 圆角、1dp 描边、16dp 间距、选中项填充主题主色，无勾选图标。保留现有 Card 的背景、形状、Tune 图标、加粗 titleMedium 标题、16dp 内边距和标题下方 12dp 间距。模式仍由后端提供，保留每行最多三个按钮和长名称换行的兼容处理。系统 HTTP 代理已完整恢复修改前实现。

**回归：** 深浅主题；模式选中状态与后端同步；多模式及长名称换行；卡片标题和系统 HTTP 代理保持原样。

## MOD-005：项目命名、品牌和开源归属

- README 标题、应用所有现有语言资源的 `app_name` 使用 `B-box`。
- Gradle 根项目名（`settings.gradle.kts` 中的 `rootProject.name`）和 APK 名称前缀使用 `B-box`。
- 本地项目目录于 2026-10-08 从 `~/project/sing-box-for-android` 更名为 `~/project/B-box`，后续本机命令从新目录执行。此次更名沿用已有的 Gradle 项目名、应用名与包名，原有 Git 工作树和未提交修改完整保留。
- GitHub 远端仓库于 2026-10-08 从 `giturass/sing-box-mod` 更名为 `giturass/B-box`，与本地目录及 Gradle 项目名一致；`origin` 使用 `https://github.com/giturass/B-box.git`，GitHub About 简介同步使用 `B-box` 名称。
- README 下载链接、应用更新 API 及本文的 Release/Actions 链接均使用 `giturass/B-box`；上游保持 `SagerNet/sing-box-for-android`，来源链接保留其真实仓库名称。
- 应用图标使用用户提供的 `/sdcard/bbox.png`，原图保存为 `artwork/bbox.png`。执行 `java tools/GenerateLauncherIcons.java` 可重建五种密度的普通/圆形启动器图标、自适应前景、主题单色图标及通知图标；自适应前景使用 108dp 画布中央 72dp 区域、白色背景，避免图案被系统遮罩裁切。
- 安装包名按用户要求改为 `io.ericlee.bfa`；保留 `io.nekohasekai.sfa` namespace、上游版本号与源码包路径。新包可与原包并行安装，应用私有数据独立，旧包配置需通过导出/导入迁移，详见 MOD-009。
- 保留 `LICENSE` 和 README 原有版权/许可文本，明确非官方分支；维护者发布前应阅读其中名称与关联声明。本文不替换原许可，也不将其简化为另一份授权。

**回归：** 五个现有 locale 的应用标签一致；安装器/启动器显示新名称；APK 文件名使用新前缀；安装身份与组件标识按 MOD-009 核验。

## MOD-006：构建与签名

### GitHub Actions

入口为 `.github/workflows/build-release.yml`，仅手动触发，生成 Android 7.0+ 的 ARM64 `otherRelease` APK。Release 构建与发布统一在 GitHub Actions 执行。`-Parm64Only=true` 限定一个 ABI 并关闭 universal；不传该参数时保留原有多 ABI 行为。

手动参数 `publish_release` 默认开启；关闭时仍运行单元测试、签名 Release 构建、产物校验及 Actions Artifact 上传，仅跳过 GitHub Release 发布。同版本标签已指向其他提交时，可用此选项构建新的 APK 供下载。

- 核心取自 `SagerNet/sing-box` 的 `v${VERSION_NAME}` 标签，Go 版本来自 `version.properties`。先检查并应用 `patches/libbox-no-legacy.patch`，仅禁用 API 21 的 legacy AAR 生成分支；协议实现、上游构建标签及导出绑定保持完整。再执行上游 `make lib_install` 和 `build_libbox -target android -platform android/arm64`，确认未生成 legacy AAR 后复制 `libbox.aar`。详见 MOD-015。
- 核心使用 JDK 17，应用使用 JDK 21；SDK 36/37.1、Build Tools 36.0.0/37.0.0、NDK 28.0.13004108。
- Release 前运行 `testOtherDebugUnitTest`，保留 R8 和 Release Lint；上传前验证唯一 APK、ARM64 架构、签名、新包名、版本、启动 Activity 与全部应用标签。Artifact 为 `B-box-<版本>-arm64-v8a-release`，保留 30 天。
- 校验成功后发布到本仓库 GitHub Release：tag 严格等于 `VERSION_NAME`（例如 `1.15.0-alpha.10`，不加 `v`），指向构建的源码 SHA；alpha/beta/rc 标记为预发布。发布 APK 及 `B-box-version-metadata.json`，供应用内更新读取。已有 tag 指向不同提交时应报错，不移动既有版本标签；同一提交重跑可重新上传产物。
- 修改版本时确认对应核心标签已发布且接口兼容。检查运行的 head SHA，只有实际成功的运行才可作为验证依据。

仓库 Actions Secrets：

| Secret | 用途 |
| --- | --- |
| `KEYSTORE_BASE64` | 发布密钥文件的 Base64 内容 |
| `KEYSTORE_PASS` | 密钥文件密码 |
| `ALIAS_NAME` | 发布密钥 alias |
| `ALIAS_PASS` | 对应密钥密码 |

沿用既有发布密钥。工作流在 runner 上解码，并在结束时清理，不上传密钥。Gradle 签名配置优先读取直接环境变量，再回退 `LOCAL_PROPERTIES` 和 `local.properties`，默认文件为 `app/release.keystore`。

### 本地 Termux 检查

遵照本机 `~/project/AGENTS.md`，没有用户明确要求时不在本机执行编译构建；Release 交给上述 GitHub Actions。编译前准备版本匹配、包含上游完整功能的标准 `libbox.aar`；legacy 核心与 App 分支已按 MOD-015 移除。此前 MOD-013 的裁剪 AAR 缺少已恢复的 Tailscale/Taildrop/原生 Shell 绑定，不能继续用于当前源码。

历史本地构建使用 Termux 原生 aapt2/aidl、Build Tools 37.0.0、NDK 29.0.14206865；本地 init script 覆盖工具路径和签名，不能把本机路径写进 CI。2026-10-19 检查时，已有 OpenJDK 17.0.20；但项目 wrapper 要求的 Gradle 9.7.0 尚未缓存，`app/libs/libbox.aar` 缺失。本次仅完成原生 aapt2 资源编译与静态核验；完整 Debug 编译和单元测试前需补齐工具链、匹配的核心及依赖。旧日志中的成功记录仅对应当时环境。

`--offline` 仅适用于依赖已缓存的环境。本地密钥和密码保存在忽略的 `.local-signing/`，沿用原文件；不要重新生成或提交。新机器需自行配置等价环境。


## MOD-007：FlClash 风格启动按钮

**来源：** [chen08209/FlClash](https://github.com/chen08209/FlClash)，参考提交 `c7be7023d33615cb624148d41414f80a7d96cede` 的 [`lib/views/dashboard/widgets/start_button.dart`](https://github.com/chen08209/FlClash/blob/c7be7023d33615cb624148d41414f80a7d96cede/lib/views/dashboard/widgets/start_button.dart) 和 `test/widgets/start_button_test.dart`。参考项目采用 GPL-3.0，原许可见其 [LICENSE](https://github.com/chen08209/FlClash/blob/c7be7023d33615cb624148d41414f80a7d96cede/LICENSE)。本实现为原生 Compose 适配，不引入 Flutter 依赖或 FlClash 核心。

**移植内容与平台差异：**

- 新组件 `ServiceStartButton`：56dp 高、16dp 圆角，停止时 56dp 宽。图标起点固定在左侧 16dp；运行时图标槽从 56dp 收到 48dp，右边计时区在 200ms 内展开。
- 播放三角与暂停双竖线用 Compose Canvas 两个多边形插值形变。并非 Flutter `AnimatedIcons` 矢量资源的逐帧复制；Compose 使用平滑缓动，不照搬 Flutter 的回弹曲线。
- 计时采用 `HH:MM:SS`、`titleMedium` 中等字重和等宽数字特性，测量小时位数对应的数字样本保留空间，三位/更多小时可扩展，高位小时用当前主题 `primary` 强调。停止时保留最后计时直到 200ms 收起完成，防止收起中跳回零。
- 启动/停止中的 Android 服务状态显示 24dp 进度圈；停止中禁用重复点击。无障碍信息继续表达“启动/停止”和真实服务状态，不能因为暂停外观而虚构暂停能力。
- 颜色保留本项目 `primaryContainer/onPrimaryContainer`；快捷入口保留 `secondaryContainer/onSecondaryContainer`，均支持动态配色和深浅主题。没有导入 FlClash 的固定颜色。
- “连接”“代理组”保持 56dp 高和相同圆角，主按钮阴影 4dp、快捷入口 1dp，按钮间距 12dp。按最长标签和主按钮预留宽度统一判断：两个入口有足够宽度时都使用居中的横向图标/文字；窄屏时一起切为图标在上、文字在下，以保留入口标签，极端大字体允许省略。
- 手机根据快捷入口数量为主按钮限制最大宽度，保留快捷入口的最小空间；大屏本地服务按钮复用同一组件。远程会话仍使用原有远程控件。

**定位：** `ServiceStartButton` 负责动画/计时/服务按钮语义；`PlayPauseIcon` 负责形变；`ServiceStatusBar` 分配手机宽度；`DashboardShortcut` 负责横排/上下排自适应；`MainActivity` 负责服务回调和手机/大屏分支。

**回归矩阵：** 停止、启动中、运行、停止中；开始时间暂缺；59秒/1小时/100小时跨位；动画中状态反转；短时间重复点击；有无代理组；320/360dp 窄屏、大字体、平板；主题动态色和深浅模式；无障碍动作。已提供 Compose 预览入口（浅色、深色、窄屏大字体、停止），预览声明不等于已渲染或设备验证。

## MOD-008：流量统计卡片

**当前行为：**

- 上传、下载两张卡片合并为单张“流量统计”，只显示核心提供的累计上传量 `uplinkTotal`、累计下载量 `downlinkTotal`；不显示实时网速和折线图，删除仪表盘专用速率/历史序列状态及更新计算。数值沿用当前核心会话统计，未增加跨会话累计或计费周期。
- 沿用其他卡片的 Material 3 `Card`、主题颜色和形状；16dp 内边距、20dp 主题色图标、加粗 `titleMedium` 标题、标题下 12dp 间距，数据行使用 `bodyMedium` 标签和 `bodyLarge` 数值，两行间隔 8dp。
- `CardGroup.Traffic` 与 `Debug` 属于 `CardPairGroup.Statistics`，两张卡同时可见时固定同行并排、等宽等高，间隔 16dp，窄屏和大字体也保持双列。两者在保存的排序中不相邻时，合并到先出现的卡片位置，保留两者的左右顺序及其他卡片的相对顺序；仅显示一张时，该卡片占满整行。
- 仪表项设置只保留一个“流量统计”入口；默认顺序集中定义于 `DashboardCardSettings.kt`，重置与首次加载一致。

**设置兼容：** 旧 `UploadTraffic` / `DownloadTraffic` 在顺序中映射为 `Traffic`，取首次出现位置并去重，保留其他卡片顺序。旧两张流量卡片都隐藏时才隐藏新卡片；只隐藏其中一张时继续显示统计。新 `Traffic` 隐藏设置正常保存/恢复，未知卡片和已删除的 `Connections` 被忽略，`Profiles` 始终可见。无需数据库迁移。

**维护约束：** 保留本地/远程数据来源和服务停止时的原有清零行为；连接列表、通知及 Tailscale 工具的速率/图表不属于本项，不得误删共享 `LineChart`。

**回归：** 升级旧排序与隐藏项、重复/未知名称、重置、拖动、单独隐藏/显示、远程会话、停止/重启；普通宽度、窄屏及大字体下同时开启两卡均同行并排；深浅主题。`DashboardCardSettingsTest` 覆盖设置兼容、相邻/分隔排序的双向配对及仅显示一张时占满整行的规则。

## MOD-009：独立安装身份与更新

- `applicationId` 为 `io.ericlee.bfa`；`namespace`、Kotlin/Java 包、AIDL 描述符及 RootServer 反射类名保留 `io.nekohasekai.sfa`，确保已声明组件可加载。
- Provider authorities 使用 `${applicationId}`；服务控制、USB 权限、USBIP 停止、Taildrop 服务与安装回执广播改由 `BuildConfig.APPLICATION_ID` 派生，Manifest 回执 action 同步使用占位符。文件分享 URI、显式服务 Intent、自身包过滤继续使用应用运行时包名。
- GitHub 更新源使用 `giturass/B-box` 的 Releases，读取 `B-box-version-metadata.json` 中的 `version_code`，匹配当前版本的 B-box ARM64 APK。发布范围为 Android 7.0+ 的 `other` / ARM64；无兼容资产时不提供下载，避免把 Release 网页或官方 SFA 包当作更新。稳定/预发布通道沿用现有设置，F-Droid 源仍按当前包名查询。
- 新旧包可并行安装，但私有配置与数据不会自动迁移。

**回归：** 新旧包安装共存；启动 Activity、VPN 授权、通知停止、USB 授权、文件分享及安装回执；稳定/预发布检查、无匹配架构、版本元数据与升级安装。`GitHubUpdateCheckerTest` 覆盖 B-box 资产筛选、版本标签及架构/flavor/API 兼容性。

## MOD-011：移除 Xposed/LSPosed 与特权增强

- 删除 Xposed/LSPosed API、模块入口与 Hook、服务 Provider/AIDL、`META-INF/xposed` 注册资源及 vendored `libxposed-api`；删除对应 Gradle 子项目、仓库、依赖与专用 Lint 忽略规则。
- 设置页移除“特权增强”，删除管理、检测与 Hook 日志页面、三条导航路由、Manifest 模块入口、启动注册、状态轮询和更新重启通知。
- 删除四个专用设置属性/键及五种语言的专用字符串。原有日志空状态译文迁为通用 `log_no_logs`。旧 DataStore 键值不再读取，不新增数据库迁移或清空其他设置。
- 安装和应用查询移除模块激活时的强制 Root 分支，继续使用用户的普通安装、Root、Shizuku 设置。删除模块专用调试导出及 Root AIDL 的方法编号 3，其余方法编号保持不变。
- 保留普通 VPN、Root/Shizuku、应用查询、桥接/自动重定向、终端，以及崩溃、OOM、电量报告。

**回归：** 设置与返回导航、日志空状态、安装方式切换、应用列表查询、升级旧设置；各 flavor 的编译及资源合并；确认源码、模块注册和构建依赖中不再保留已移除组件。

## MOD-012：节点卡片选中态与浅色可读性

- 节点采用双列布局、12dp 圆角与分组底板；每张卡片最小高度 65dp，上下内边距 9dp，两行间距 6dp。名称与类型/延迟两行整体垂直居中，将最小高度产生的额外留白均分到上下两侧。名称使用 16sp 的 `bodyLarge`，类型与延迟使用 12sp 的 `labelMedium`；大字体下卡片可自然增高。
- 浅色选中背景由 `primaryContainer` 向 `primary` 混合 20%，深色使用 `primaryContainer`，选中卡片增加 1dp 主色描边。根据当前主题的 `surface` 亮度判断浅深色，使用主题色以适配动态配色。
- 浅色选中的测速文字向 `onPrimaryContainer` 混合 35%，加深文字并保留延迟等级的色相；类型文字使用 80% 不透明度的选中文字色。名称、类型与延迟均为单行省略，延迟最多占详情行可用宽度的 60%，为类型预留空间。

**回归：** 320/360dp 双列、深浅色、动态色、大字体、长名称/类型/延迟、奇数末项、点击选择及长按测速。具体动态配色的文字对比度与实机效果仍需设备验证。

## MOD-013：ARM64 核心组件裁剪（已回退）

2026-10-25 按用户要求回退。恢复上游 NaiveProxy、Tailscale、OpenVPN、Snell、OpenConnect、USBIP、Tor 实现及构建标签，恢复 Tailscale/Taildrop 客户端导出绑定和原生 Shell/PTY 会话。删除 `patches/libbox-trim.patch`、`tools/verify_libbox.py`、对应测试与 CI 裁剪检查；本地 `.core/` 同步撤销裁剪。仅保留 MOD-015 的 legacy AAR 构建排除补丁。

## MOD-014：移除 App 的 Tailscale 与终端（已回退）

2026-10-25 按用户要求回退。恢复 Tailscale 端点/节点/出口节点/Ping、Taildrop 收发与分享服务、SSH 终端及 10 条导航路由；恢复 Ghostty 主题/字体/配置页面、设置键、语言资源、API 24+ 编辑器语言模式、四项适用的 Ghostty 依赖及 snapshots 仓库。平台 Shell/SFTP、Root shell Binder 和用户解析恢复裁剪前实现，Root AIDL 方法编号 6、7 恢复。

MOD-015 的 Android 5/6 分支移除与 MOD-016 的更新界面/自动调度移除继续有效；不恢复 API 21 编辑器、legacy Ghostty 依赖或更新入口。节点卡片的居中布局及其余定制继续保留。历史设置未清空，恢复功能沿用原设置键。

**回归：** 本地/远程 Tailscale 状态、Taildrop 分享/收发/通知跳转、SSH 终端与 Root/Shizuku Shell、主题/字体/Ghostty 配置、JSON 编辑器；完整核心的协议配置检查与启动。回退后的实际 AAR/APK 仍需构建验证。

## MOD-015：移除 Android 5/6 与 legacy 核心

- 删除 `otherLegacy` flavor、对应源码集与产物命名分支、`libbox-legacy.aar` 依赖及所有 API 21 专用依赖版本/配置，包括旧版 Compose 和 EditorKit。保留 `play` / `other`，最低 Android 版本均为 API 24（Android 7.0）。
- 删除 `src/minApi21` 的七个兼容实现文件及 `src/otherLegacy` 的 Manifest、Vendor 和安装器，共十个文件；保留 API 24 的配置编辑器、应用查询、二维码扫描及 Root/Shizuku 安装实现。移除已无使用者的旧 Termux 模块 SDK/NDK 属性。
- `patches/libbox-no-legacy.patch` 仅删除 API 21 AAR 生成分支和该分支使用的标签过滤函数，不修改协议、构建标签或导出绑定。CI 只构建标准 `libbox.aar`，并要求构建后不存在 `libbox-legacy.aar`。`.core/` 继续忽略，`.gitattributes` 保留 unified diff 的空白检查例外。
- 更新检查测试仍保留历史 legacy APK 的拒绝用例，防止从旧 Release 附件误选不兼容安装包。`useLegacyPackaging` 是 APK 原生库压缩设置，继续保留，不代表旧版 Android 构建分支。

**回归：** `otherRelease` 的测试、编译、R8、资源合并与签名；API 24+ 编辑器、应用查询和安装；没有 `otherLegacy` 构建配置，核心只生成标准 AAR；最终 APK 的 ARM64 架构及最低 API 24 校验。

## MOD-016：移除应用更新提示与设置入口

- 删除首次启动的自动检查询问、启动时的新版本/下载弹窗和更新检查调用；删除应用设置的整个“更新设置”与“操作”分组，以及来源、通道、GitHub Token 和安装方式选择弹窗。通知、语言、缓存及版本信息/复制菜单继续保留。
- 删除应用设置的新版本入口、手动“检查更新”、版本行“New”徽标、设置首页和手机/平板导航的更新徽标。删除专用更新弹窗组件、F-Droid 镜像设置页面及导航路由，五种语言移除首次提示和分组标题共 15 项字符串。
- 删除四项自动检查/首次询问/自动更新/已提示版本的设置属性与键，不读取历史值。启动时取消唯一任务 `AutoUpdate`；`UpdateWorker` 保留原类名作为旧任务兼容入口，其执行方法直接完成，不检查、下载或安装更新。删除自动更新调度入口。
- 现有更新源/缓存数据类型和普通安装工具保留，Compose 界面不再调用更新检查或下载接口；不清空历史设置、配置或数据库。配置订阅的 `UpdateProfileWork`、普通 Root/Shizuku 能力、GitHub Actions 构建及 APK 手动下载继续保留。

**回归：** 首次启动及从旧设置升级无更新弹窗；无手动检查、更新分组或更新徽标；通知、语言、缓存、版本复制、配置订阅更新及普通 Root/Shizuku 功能；已排队 `AutoUpdate` 任务取消/兼容完成；Release 编译、资源合并和签名。

## 维护与上游同步

1. 先读 README、本文件及适用的 AGENTS.md，检查工作树并保留未提交改动。
2. 固定比较基线 SHA；区分已提交差异与工作树差异。同步上游后更新基线并检查 MOD-001 至 MOD-009、MOD-011、MOD-012、MOD-015、MOD-016；MOD-013、MOD-014 已回退，不重新引入裁剪。当前已合并上游 `dev` 的 `a548a79` / `1.15.0-alpha.13`。相对前一基线 `bc73504`，纳入核心初始化等待、服务模式检测与连接同步、仪表盘配置更新生效、分应用代理列表/扫描及旧版 Android 连接归属查询修复；版本与 Go 版本采用上游值。冲突处理保留 B-box 定制，构建配置和应用查询仍不包含 Xposed 仓库、模块或强制 Root 分支。
3. 特别核对 `CardGroup` 设置兼容性、本地/远程分支、VPN 权限流程和服务状态；未授权时不改协议、包名、签名或服务生命周期。
4. 完成必要编译、资源及受影响 UI 检查，记录实际验证范围。编译与预览声明不等于实机交互验证。
5. 提交前审查差异，不提交密钥、密码、机器配置、SDK 或 AAR。`origin` 为个人 fork，`upstream` 为 SagerNet；仅推送授权仓库，不强推上游。

## 验证记录

- 2026-10-25 MOD-013 / MOD-014 回退：41 个未与后续修改重叠的 App/构建文件与裁剪前 `9000805` 逐字节一致；交叉文件保留 MOD-015 / MOD-016，五种语言与设置仅保留后续更新功能删除。55 个 XML 的解析/资源重名检查、全部本地字符串/复数引用、10 条 Tailscale/终端路由、分享与服务注册、Root shell AIDL 编号检查通过。核心恢复完整协议标签/注册、11 个客户端方法及原生 Shell 导出；legacy 专用补丁实际应用与反向检查通过。8 个工作流 Bash 块、2 个嵌入 Python 块及 `git diff --check` 通过；节点卡片和其余定制保持原样。本次未执行本机编译、GitHub Actions 或实机验证，未生成新 AAR/APK。

以下历史构建记录仅适用于所列提交；裁剪版 APK 不包含本次 MOD-013 / MOD-014 回退。

- 2026-10-25 MOD-015 / MOD-016 最终构建：[Actions 40144809788](https://github.com/giturass/B-box/actions/runs/40144809788) 成功，提交 `6de8a299d3cc14052f26e8bbce2a93f3c5bfded2`。九项裁剪校验器测试、标准 AAR 的 Java 绑定/原生库校验、741 个包的 Android 依赖图检查、应用单元测试、签名 Release 编译、最低 API 24、包名/版本/89 个应用标签、ARM64 架构及 APK 签名检查全部通过；只生成标准 AAR。此次包含 legacy 核心/Android 5/6 分支、首次更新提示、更新设置、手动检查与更新徽标移除。`publish_release=false`，产物上传至 Actions，未替换现有同版本 Release。
- 同次产物复核：[APK Artifact](https://github.com/giturass/B-box/actions/runs/40144809788/artifacts/12580705391)。下载归档 SHA-256 为 `acf9bade3f2353dc86df8ef4cdae66c2580655228fdf4261ad064a87c2d8e0f7`，与 GitHub digest 一致；APK 为 22,474,086 字节，SHA-256 `0c2de4093dd4a21907ae58a21e1d581694079e659f913398d840c76dec2fb3d2`。直接读取 APK 内 Go buildinfo，确认 Go `1.26.10`、六项保留标签和 73 个链接模块，元数据及原生库裁剪检查通过。标准 AAR / APK 原生库为 45,907,296 / 45,907,288 字节，APK 内 ZIP 条目为 14,898,431 字节。未进行实机交互验证。
- 2026-10-25 首轮 MOD-015 / MOD-016 构建：[Actions 40143908210](https://github.com/giturass/B-box/actions/runs/40143908210)，提交 `e123f9d760a71b0845e80670fc3c9596aa197776`。标准核心构建/裁剪检查、应用单元测试及签名 Release 编译通过；最低 SDK 校验仍使用旧 `sdkVersion` 输出字段而失败，未上传 APK。核对 [AOSP `DumpManifest.cpp`](https://github.com/aosp-mirror/platform_frameworks_base/blob/main/tools/aapt2/dump/DumpManifest.cpp) 的 `UsesSdk::Print` 后，校验兼容新版 `minSdkVersion` 字段，并在失败时输出实际解析值；继续要求最低 API 24。
- 2026-10-25 MOD-015 / MOD-016 静态复核：标准核心补丁反向应用与 `git diff --check` 通过；确认只保留 `play` / `other`、最低 API 24，旧版源码与 API 21 核心构建分支已删除。更新界面/调度及已删除设置引用检查通过，Compose 状态委托导入已核对；资源 XML、工作流 shell 与嵌入 Python 语法检查通过。未在本机编译，实际 APK 以随后 GitHub Actions 构建为准。
- 2026-10-25 残留清理构建：[Actions 40136030352](https://github.com/giturass/B-box/actions/runs/40136030352) 成功，提交 `d6f179c992bbba46f1b908133e28dd7264465e23`。九项 Python 校验器测试、标准/legacy AAR 的 Java 绑定及原生库残留检查、标准核心 741 个包的依赖图检查、应用单元测试、签名 Release 构建及 APK v2 签名校验全部通过；`tailssh`、PTY 和未使用的 Tailscale/Taildrop 导出绑定不再进入产物。标准 AAR / APK 原生库为 45,907,296 / 45,907,288 字节，APK 内 ZIP 条目为 14,898,431 字节。此构建包含残留清理，尚不包含随后 MOD-015 / MOD-016 的修改。
- 2026-10-25 残留处理静态检查：12 个核心文件的补丁反向应用检查通过；与上游 `v1.15.0-alpha.13` 客户端源码逐函数比较，67 个函数体逐字节一致，56 个保留、11 个移至条件编译文件。Python 语法、导出类型条件编译引用、客户端导入使用及 `git diff --check` 检查通过；九项校验器测试通过，新增原生库检查能正确拒绝此前 `30ded20` APK 中的 `tailssh` 和 PTY 残留。未在本机编译，新的实际 AAR/APK 结果以随后 CI 验证为准。
- 2026-10-25 裁剪版 Release 构建：[Actions 40134109267](https://github.com/giturass/B-box/actions/runs/40134109267) 成功，构建提交 `30ded207a4242ff628123cf0d688403cfbede137`。标准及 legacy ARM64 AAR 的实际构建标签和模块校验、标准变体 Android/CGO 下 743 个包的依赖图检查、应用单元测试、签名 Release 构建、APK 包名/版本/89 个应用标签/ARM64 架构及 APK v2 签名校验全部通过。本次 `publish_release=false`，APK 上传至 Actions Artifact，现有同版本 GitHub Release 未替换。
- 同次产物复核：[APK Artifact](https://github.com/giturass/B-box/actions/runs/40134109267/artifacts/12576741966) 下载后的归档 SHA-256 与 GitHub digest 一致。APK 为 22,779,150 字节，SHA-256 `56a90b0314d32ac44bf4a8a86776fd6d1fbd951dedf4c6a736c17033c51082db`；较现有同版本官方完整核心的 B-box APK（36,109,526 字节）减少 13,330,376 字节 / 36.92%，包含核心与 App 两项裁剪的共同收益。APK 内 `libbox.so` 为 46,291,896 字节，ZIP 条目为 14,979,406 字节；标准/legacy AAR 原生库分别为 46,291,904 / 46,329,576 字节。
- 同次残留审查：按 Go 官方 buildinfo 格式直接读取下载 APK 内的原生库，确认 Go `1.26.10`、六项保留构建标签、74 个链接模块；七项被裁剪外部模块及任何名称含 `tailscale` 的模块均未链接。原生库中的包/函数名称仍包含 `tailssh` 的 `OpenPtyShell`、`StartPtyProcess`、`StartSocketpairProcess` 等通用 Shell/PTY 实现，`github.com/creack/pty` 也仍链接；另保留导出接口、gRPC 类型、配置结构及不可用报错占位。未发现其他被裁剪协议实现的包/函数名称。此检查未继续删除兼容接口或通用 Shell 实现，未进行设备交互验证。
- 2026-10-25 节点卡片上下留白：两行内容改用 `Arrangement.spacedBy(6.dp, Alignment.CenterVertically)`，使名称上方与类型/延迟下方的布局留白等距。已核对 AndroidX 官方 `Arrangement` 源码中的垂直对齐行为及本项目既有用法，变更范围审查与 `git diff --check` 通过。未执行本机编译构建或实机验证。
- 2026-10-25 App 裁剪：MOD-014 删除 29 个专用源码/AIDL 文件。静态检查通过：56 个 XML 文件可解析，五种语言删除资源无剩余引用，10 条 Tailscale/终端路由移除且其他路由保留，Root AIDL 事务编号和剩余实现一致，已删除类/设置/依赖无残留引用，libbox 平台方法参数签名与裁剪前一致。两种 API 版本的 JSON 编辑器仅有移除 Ghostty 模式的预期差异，配置编辑页/状态管理、JSON schema/语言实现及编辑器依赖与裁剪前一致。核心补丁反向应用检查、原生库校验脚本 Python 语法检查和 `git diff --check` 通过。未执行本机编译、GitHub Actions 或设备验证，未生成新 APK，尚无实际体积数据。
- 2026-10-25 核心裁剪：针对 `v1.15.0-alpha.13` / `824dbcfb4dd0a95921840b1737b950cde590241e` 准备并应用 MOD-013 补丁。标准 Git 补丁检查、实际应用及反向应用检查通过；四个受影响的核心文件与预期修改逐字节一致。按 Android/ARM64、CGO、Go 1.26 和保留标签进行源码导入静态分析，108 个可达本地包中不再包含被裁剪的实现，也没有直接引用七项相关外部模块；其余代理协议、gRPC 和普通终端辅助包仍可达。此分析未解析第三方模块内部的完整依赖，CI 的 `go list -deps` 将进一步核验。
- 同次验证：原生库元数据校验器的 11 项通过/拒绝用例通过，覆盖被裁剪标签、模块/子模块、模块替换、缺失元数据、保留项及错误目标平台/架构；源码依赖校验用例确认普通 shell 辅助包可保留、已裁剪实现会被拒绝。工作流 8 个 shell 块通过 Bash 语法检查，2 个内嵌 Python 块及校验脚本通过 Python 语法检查，`git diff --check` 通过。遵照本机 AGENTS.md，未执行本机 Go/Android 编译构建，未运行 GitHub Actions，未生成或发布新 AAR/APK；尚无实际体积收益数据。

- 既有 Release 工作流：[36887951093](https://github.com/giturass/B-box/actions/runs/36887951093)，2026-10-01 成功；该产物仍使用旧应用名。
- 当前模式按钮及系统代理回退已通过本地 Debug 构建和签名验证；Clash 文件定向 Spotless 检查通过。全库仍有既有格式问题，不代表全库检查通过。
- MyBox Release：[36955861455](https://github.com/giturass/B-box/actions/runs/36955861455)，2026-10-02 成功，构建源码提交 `ee3890d`。R8、Release Lint、ARM64 架构和签名检查通过；下载产物后确认所有应用标签均为 `MyBox`，版本 `1.15.0-alpha.9`（741）。
- 产物：`MyBox-1.15.0-alpha.9-arm64-v8a.apk`；SHA-256：`9bf47a9cbdc26ccd9f1b3e39edaa30ca09149f349b389773fd4f4d40ccbcb19a`。
- B-box 本地验证（2026-10-02）：`testOtherDebugUnitTest` 8 项设置兼容/配对测试全部通过；8 个受影响 Kotlin 文件定向 Spotless 检查均为 `IS CLEAN`；`assembleOtherRelease` 成功，R8 与 Release Lint Vital 通过。工作流 actionlint 通过（Termux 下禁用外部 shellcheck/pyflakes 调用），未运行 GitHub Actions。
- 2026-10-02 本地产物记录：`B-box-1.15.0-alpha.9-arm64-v8a.apk`，36,249,271 字节；SHA-256：`5cff43df19f9f1790501acac6e0da0886ff224e8d036dd704f55bf00d0bad09f`。当时包名为 `io.nekohasekai.sfa`、版本 `1.15.0-alpha.9`（741）、全部应用标签 `B-box`、仅 ARM64、APK 签名验证通过；沿用本机 `.local-signing` 密钥。
- 2026-10-02 核心 AAR SHA-256：`9aea3c1f5291c417e7e10782e3031cc7434f55b0facd5d6db1e4992c010193e1`，与当时 `1.15.0-alpha.9` 副本一致。图标原图 SHA-256：`51599719baf1cc226d948107412729247eb7621c8714235f9cb7865f887d60cd`，与 `/sdcard/bbox.png` 一致。
- 2026-10-03 上游同步与新包发布：[Actions 37099085549](https://github.com/giturass/B-box/actions/runs/37099085549) 成功，构建提交 `f3a60033d1bd904a7cef0bfb2fb5819e88c93c8a`。`testOtherDebugUnitTest`（设置兼容、卡片配对及 GitHub 更新资产筛选）、R8、Release Lint Vital、ARM64 Release 构建、产物身份与签名检查全部通过。Release 在 GitHub Actions 构建；本地仅完成 8 个修改 Kotlin 文件的定向 Spotless 检查（均 `IS CLEAN`）及工作流 actionlint 检查。
- 2026-10-03 历史发布记录：[Release `1.15.0-alpha.10`](https://github.com/giturass/B-box/releases/tag/1.15.0-alpha.10) 已发布为预发布，tag 与应用版本完全一致，当时指向上述 `f3a6003` 构建提交。附件包含 `B-box-1.15.0-alpha.10-arm64-v8a.apk` 和 `B-box-version-metadata.json`；元数据为包名 `io.ericlee.bfa`、版本 `1.15.0-alpha.10`、versionCode `742`。
- 上述历史产物复核通过：APK 36,171,175 字节，SHA-256 `9095e366693e27ffe24b4c9f0e133dfd42ba22f2dc313e21982bb5f07a1ad219`，与 Release 附件 digest 一致；包名 `io.ericlee.bfa`、90 个应用标签均为 `B-box`、仅 ARM64、内嵌核心 `1.15.0-alpha.10`、APK v2 签名验证通过。CI 核心从 `SagerNet/sing-box` 的 `v1.15.0-alpha.10`（`c992297988288565a24a6d36e2cf4d77cb835fcd`）构建。
- 2026-10-03 配对规则修正：流量统计与调试卡片同时可见时始终同行，包括窄屏、大字体及不相邻排序。Debug 编译和 `testOtherDebugUnitTest` 通过（9 项仪表盘测试、5 项更新检查测试），两个修改 Kotlin 文件定向 Spotless 检查均为 `IS CLEAN`。此修正未包含在上述 `f3a6003` 历史构建产物中；当前同名 Release 状态见下方 API 复核记录。
- 2026-10-08 GitHub API 复核：当前 `1.15.0-alpha.10` 标签指向 `81a1e79a44282ba07d5729588b10a422788cc376`，包含流量统计与调试卡片配对修正；对应 [Actions 37102809327](https://github.com/giturass/B-box/actions/runs/37102809327) 于 2026-10-03 完成且状态为 `success`。当前 Release ID 为 `402367667`，APK 为 36,171,027 字节，GitHub 返回的 SHA-256 为 `60974b9095db4e8707ea6aa6318f4ea521f4ea8f2d8457180ba2080c8d358d2f`。本次仅核对 API 元数据，未重新下载 APK 或验证签名。
- 2026-10-08 节点卡片样式回退：当时 `GroupsCard.kt` 恢复到样式调整前的 `81a1e79` 版本，文件内容完全一致，撤销节点的 1.12.23 样式定制；B-box 命名、远端地址、更新源及其他界面改动保留。此次回退仅核对范围与文件内容，未重新编译或进行实机验证。
- 2026-10-08 项目目录更名：已确认 Git 根目录为 `~/project/B-box`，`rootProject.name` 为 `B-box`。更名前后的工作树状态和未提交差异一致，`git diff --check` 通过；未发现需要调整的源码、脚本或 IDE 目录引用。
- 2026-10-08 远端仓库更名：GitHub 仓库及 `origin` 已统一为 `giturass/B-box`，About 简介使用 `B-box` 名称；仓库 ID `1397330945`、默认分支 `dev`、Release/附件 ID 与 digest、工作流 ID 均与更名前一致。新 Releases API 可读，旧 API 地址仍可访问同一仓库；README、本文和应用更新源均使用新地址。差异检查通过，本次未重新编译或发布 APK。
- 2026-10-08 双列节点与模块移除：当时完成 FlClash 风格双列节点与 MOD-011 的源码修改。18 个修改的 Kotlin/Gradle 文件通过可用 Kotlin 2.2.20 PSI 语法解析；6 个修改 XML 解析、资源重名、全部本地字符串引用、已删除类型引用及差异格式检查通过。构建命令 `sh gradlew :app:compileOtherDebugKotlin :app:testOtherDebugUnitTest -Parm64Only=true --offline --no-daemon --console=plain` 在启动时因未配置 `JAVA_HOME` 且 PATH 中没有 `java` 而失败；PSI 检查借用已有 JDK，仅验证语法，不等同项目 Kotlin 2.4.10 的 Android 编译。未生成 APK，完整编译、单元测试、预览渲染与实机交互尚未验证。
- 2026-10-08 FlClash 节点卡片样式回退：`GroupsCard.kt` 恢复为 `9cbde5b` 版本（与 `81a1e79` 文件内容完全一致），撤销 MOD-010；MOD-011 的 Xposed/LSPosed 移除继续有效。README 与修改索引已同步，文件内容与回退范围核对、`git diff --check` 通过。本次未重新编译或进行实机验证。
- 2026-10-08 节点尺寸与选中态：完成 MOD-012，回退后的节点卡片适度增大，并加深浅色选中背景。变更范围审查与 `git diff --check` 通过。尝试 `sh gradlew :app:compileOtherDebugKotlin -Parm64Only=true --offline --no-daemon --console=plain`，因未配置 `JAVA_HOME` 且 PATH 中没有 `java` 而无法启动；本次未完成 Android 编译或实机验证。
- 2026-10-08 节点卡片高度微调：最小高度调为 68dp，上下内边距调为 11dp，以匹配默认字号下的内容高度。`git diff --check` 通过，本次未重新编译或实机验证。
- 2026-10-09 上游同步：合并 `upstream/dev` 的 `2d43d54`，更新为 `1.15.0-alpha.11`（744）。解决版本与 Xposed 仓库配置冲突；4 个 Kotlin 文件的实际改动逐项与上游增量一致，B-box 定制及原有未提交的图标链接删除、`gradlew` 权限改动完整保留。差异格式、变更范围、5 种语言 XML、错误占位符与权限说明 HTML 检查通过。已确认上游核心 `v1.15.0-alpha.11` 标签存在，本机标准核心仍为 `alpha.10`，完整构建前需准备匹配 AAR。尝试 `sh gradlew :app:compileOtherDebugKotlin :app:testOtherDebugUnitTest -Parm64Only=true --offline --no-daemon --console=plain --max-workers=2`，因未配置 `JAVA_HOME` 且 PATH 中没有 `java` 而无法启动；本次未完成 Android 编译、单元测试或实机验证，未生成或发布 APK。
- 2026-10-19 上游同步：合并 `upstream/dev` 的 `bc73504fcb858400a939140ad785ed92f4c9f429`，更新为 `1.15.0-alpha.12`（745），Go 版本仍为 `go1.26.8`。仅 `version.properties` 产生冲突，按上游值解决。排除维护文档和版本文件后，新旧基线对应的全部 137 个 mod 文件差异（含二进制和删除项）逐字节一致；首页控件、配置操作、流量与调试配对、模式按钮、65dp 节点卡片、品牌图标、独立安装身份、更新源、ARM64 工作流及 Xposed 移除完整保留。
- 本次核验：`git diff --check`、56 个 XML 解析与资源重名检查、5 种语言的 `B-box` 应用名、全部本地字符串引用、已移除 Xposed 类型/依赖/注册文件检查，以及 Termux 原生 aapt2 对 `app/src/main/res` 的资源编译均通过。确认上游核心 `v1.15.0-alpha.12` 标签存在，指向 `25b0d5ea3e466d246c708ac0f20d0448110e566c`。本地缺少匹配 AAR 和 Gradle 9.7.0 缓存，未执行 Android 编译、单元测试、预览或实机验证；未生成或发布 APK。
- 2026-10-19 节点卡片微调：名称、类型和延迟统一使用常规字重；最小高度由 65dp 减为 63dp，上下内边距各由 9dp 减为 8dp，按 Android 布局单位缩小 2dp。变更范围审查与 `git diff --check` 通过；本次未重新编译或实机验证。
- 2026-10-19 节点卡片样式恢复：恢复 `7144b2f` 的 65dp 卡片样式，包括 9dp 上下内边距、6dp 两行间距、`bodyLarge` 名称与 `labelMedium` 类型/延迟，以及长文本单行省略和延迟宽度限制。
- 2026-10-24 上游同步：合并上游 `dev` 的 `a548a7903e96266a0046018562c73b192edbfc18`，更新为 `1.15.0-alpha.13`（747），Go 更新为 `go1.26.10`。解决初始化、平台接口导入、两个 API 级别的应用查询、Gradle 仓库及版本冲突；保留三个已删除的 Xposed 文件状态。纳入服务启动和配置应用、分应用代理列表与扫描、旧版 Android 连接归属查询修复。
- 本次静态核验：除维护文档外，128 个未受上游增量影响的 B-box 路径逐字节一致（含权限和删除项）；9 个非定制文件与上游完全一致，6 个定制文件的实际新增/删除行与上游增量一致（兼容 minApi21 已有的局部变量清理）。56 个 XML 解析、资源重名、5 种语言应用名、本地字符串引用及已移除 Xposed/旧重连事件引用检查通过；字符串检查排除原有 Material 库提供的 `appbar_scrolling_view_behavior`。确认核心标签 `v1.15.0-alpha.13` 存在，指向 `824dbcfb4dd0a95921840b1737b950cde590241e`。遵照本机 AGENTS.md，本次未运行编译构建、单元测试、预览或实机验证，未生成或发布 APK。
- MOD-008 配对修正尚待实机点击、深浅主题、大字体及截图回归。
