# B-box：修改清单与维护指南

## 基线与范围

| 项目 | 当前值 |
| --- | --- |
| 应用名称 / APK 前缀 | `B-box` |
| 仓库 | https://github.com/giturass/sing-box-mod |
| 上游 | https://github.com/SagerNet/sing-box-for-android ，`dev` 分支 |
| 比较基线 | `8e42c63c4771de10b20dd2562704850c604518d8` |
| 版本 / versionCode | `1.15.0-alpha.9` / `741` |
| 包名 | `io.nekohasekai.sfa` |
| 更新日期 | 2026-10-02 |

本文只描述当前有效改动；已撤回的外观方案和逐次构建流水记录已移除。协议、路由、VPN、远程控制、配置导入导出及特权能力继承上游。核心 AAR 不在 Git 中，本地构建与 CI 核心来源应分别核验。

## 修改索引

路径前缀为 `app/src/main/java/io/nekohasekai/sfa/`。

| ID | 修改 | 主要文件 |
| --- | --- | --- |
| MOD-001 | 首页浮动控制入口 | `compose/MainActivity.kt`、`compose/component/ServiceStatusBar.kt`、`compose/screen/dashboard/DashboardScreen.kt` |
| MOD-002 | 配置卡片操作布局 | `compose/screen/dashboard/ProfilesCard.kt` |
| MOD-003 | 移除独立连接统计卡片 | `DashboardViewModel.kt`、`DashboardCardRenderer.kt`、`DashboardSettingsBottomSheet.kt` |
| MOD-004 | 1.12.23 样式的模式按钮 | `compose/screen/dashboard/ClashModeCard.kt` |
| MOD-005 | B-box 名称与产物命名 | 语言资源、Gradle、README、构建工作流 |
| MOD-006 | ARM64 Release 构建 | `.github/workflows/build-release.yml`、`app/build.gradle.kts` |
| MOD-007 | FlClash 风格服务按钮 | `compose/component/ServiceStartButton.kt`、`ServiceStatusBar.kt` |
| MOD-008 | 合并流量统计并与调试卡片配对 | `TrafficCard.kt`、`DashboardCardSettings.kt`、仪表盘渲染与设置 |


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

**回归：** 空列表、本地/远程配置、超长名称、更新成功/失败、各类分享导出、配置选择和编辑。

## MOD-003：连接统计卡片移除

**上游行为：** 仪表盘有独立入站/出站连接数量卡片，与 Debug 卡片组成 Statistics 双列分组。

**当前行为：** 删除独立 `ConnectionsCard`、`CardGroup.Connections` 及卡片专用 `connectionsIn`/`connectionsOut` UI 字段。同步移除渲染、默认顺序、可见性及仪表项设置入口。`CardPairGroup.Statistics` 当前用于 Debug 与合并后的流量统计配对，见 MOD-008。

**兼容性：** `DashboardCardSettings.kt` 只接受当前枚举名及两种旧流量卡片名，旧设置中的 `Connections` 在加载顺序和禁用项时被忽略；没有新增数据库迁移。活动连接页面/弹层、`connectionsCount` 和底层连接数据仍保留。

**回归：** 从包含 `Connections` 的旧卡片顺序/隐藏项升级；重置仪表项；拖动排序；显示/隐藏 Debug；流量卡片配对；打开活动连接列表。

## MOD-004：1.12.23 模式按钮

**核验基线：** sing-box `v1.12.23` 的 `clients/android` 子模块指向 `eb87216961321de1802e1355c470242f2ed5faa8`。按钮参考该提交的 `view_clash_mode_button.xml`、`bg_rounded_rectangle*.xml` 和 `OverviewFragment.kt`。

**当前行为：** 仅模式选择按钮（rule/global/direct）使用旧版独立矩形样式：4dp 圆角、1dp 描边、16dp 间距、选中项填充主题主色，无勾选图标。保留现有 Card 的背景、形状、Tune 图标、加粗 titleMedium 标题、16dp 内边距和标题下方 12dp 间距。模式仍由后端提供，保留每行最多三个按钮和长名称换行的兼容处理。系统 HTTP 代理已完整恢复修改前实现。

**回归：** 深浅主题；模式选中状态与后端同步；多模式及长名称换行；卡片标题和系统 HTTP 代理保持原样。

## MOD-005：品牌和开源归属

- README 标题、应用所有现有语言资源的 `app_name` 使用 `B-box`。
- Gradle 根项目名和 APK 名称前缀使用 `B-box`。
- 应用图标使用用户提供的 `/sdcard/bbox.png`，原图保存为 `artwork/bbox.png`。执行 `java tools/GenerateLauncherIcons.java` 可重建五种密度的普通/圆形启动器图标、自适应前景、主题单色图标及通知图标；自适应前景使用 108dp 画布中央 72dp 区域、白色背景，避免图案被系统遮罩裁切。
- 保留 `io.nekohasekai.sfa` applicationId/namespace、上游版本号、上游代码包路径，避免无关迁移。包名相同意味着不同签名的安装包不能直接覆盖，也不能并行安装。
- 保留 `LICENSE` 和 README 原有版权/许可文本，明确非官方分支；维护者发布前应阅读其中名称与关联声明。本文不替换原许可，也不将其简化为另一份授权。

**回归：** 五个现有 locale 的应用标签一致；安装器/启动器显示新名称；APK 文件名使用新前缀；不修改数据库、ContentProvider、VPN 或 Xposed 标识。

## MOD-006：构建与签名

### GitHub Actions

入口为 `.github/workflows/build-release.yml`，仅手动触发，生成 Android 7.0+ 的 ARM64 `otherRelease` APK。`-Parm64Only=true` 限定一个 ABI 并关闭 universal；不传该参数时保留原有多 ABI 行为。

- 核心取自 `SagerNet/sing-box` 的 `v${VERSION_NAME}` 标签，Go 版本来自 `version.properties`。执行上游 `make lib_install` 和 `build_libbox -target android -platform android/arm64`，复制标准 `libbox.aar`。
- 核心使用 JDK 17，应用使用 JDK 21；SDK 36/37.1、Build Tools 36.0.0/37.0.0、NDK 28.0.13004108。
- 保留 R8 和 Release Lint；上传前验证唯一 APK、ARM64 架构和签名。Artifact 为 `B-box-<版本>-arm64-v8a-release`，保留 30 天，不自动创建 GitHub Release。
- 修改版本时确认对应核心标签已发布且接口兼容。检查运行的 head SHA，只有实际成功的运行才可作为验证依据。

仓库 Actions Secrets：

| Secret | 用途 |
| --- | --- |
| `KEYSTORE_BASE64` | 发布密钥文件的 Base64 内容 |
| `KEYSTORE_PASS` | 密钥文件密码 |
| `ALIAS_NAME` | 发布密钥 alias |
| `ALIAS_PASS` | 对应密钥密码 |

沿用既有发布密钥。工作流在 runner 上解码，并在结束时清理，不上传密钥。Gradle 签名配置优先读取直接环境变量，再回退 `LOCAL_PROPERTIES` 和 `local.properties`，默认文件为 `app/release.keystore`。

### 本地 Termux

先准备匹配的核心 AAR；legacy flavor 另需 `libbox-legacy.aar`。本机使用 Termux 原生 aapt2/aidl，Build Tools 37.0.0、NDK 29.0.14206865；本地 init script 覆盖工具路径和签名，不能把本机路径写进 CI。

```sh
gradle -I .gradle/sfa-termux-release.init.gradle :app:assembleOtherRelease \
  --offline --console=plain --max-workers=1 --no-daemon \
  '-Dorg.gradle.jvmargs=-Xmx2048m -XX:ActiveProcessorCount=2 -Dfile.encoding=UTF-8' \
  -Pkotlin.compiler.execution.strategy=in-process
```

`--offline` 仅适用于依赖已缓存的环境。本地密钥和密码保存在忽略的 `.local-signing/`，沿用原文件；不要重新生成或提交。新机器需自行配置等价环境，不能靠关闭 R8/Lint 掩盖构建问题。


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
- `CardGroup.Traffic` 与 `Debug` 属于 `CardPairGroup.Statistics`，默认相邻并排、等宽等高，间隔 16dp。遵循原有排序：相邻可配对，隐藏一张或移到不相邻位置时单张占满宽度。可用宽度不足 360dp 或字体缩放超过 1.3 时使用单列，避免文字拥挤。
- 仪表项设置只保留一个“流量统计”入口；默认顺序集中定义于 `DashboardCardSettings.kt`，重置与首次加载一致。

**设置兼容：** 旧 `UploadTraffic` / `DownloadTraffic` 在顺序中映射为 `Traffic`，取首次出现位置并去重，保留其他卡片顺序。旧两张流量卡片都隐藏时才隐藏新卡片；只隐藏其中一张时继续显示统计。新 `Traffic` 隐藏设置正常保存/恢复，未知卡片和已删除的 `Connections` 被忽略，`Profiles` 始终可见。无需数据库迁移。

**维护约束：** 保留本地/远程数据来源和服务停止时的原有清零行为；连接列表、通知及 Tailscale 工具的速率/图表不属于本项，不得误删共享 `LineChart`。

**回归：** 升级旧排序与隐藏项、重复/未知名称、重置、拖动、单独隐藏/显示、远程会话、停止/重启；普通宽度并排、窄屏/大字体单列、深浅主题。`DashboardCardSettingsTest` 覆盖设置兼容与配对规则。

## 维护与上游同步

1. 先读 README、本文件及适用的 AGENTS.md，检查工作树并保留未提交改动。
2. 固定比较基线 SHA；区分已提交差异与工作树差异。同步上游后更新基线并检查 MOD-001 至 MOD-008。本次按用户要求暂不合并上游，继续保持 `8e42c63` / `1.15.0-alpha.9`；已撤回临时合并，不包含 `1.15.0-alpha.10`。
3. 特别核对 `CardGroup` 设置兼容性、本地/远程分支、VPN 权限流程和服务状态；未授权时不改协议、包名、签名或服务生命周期。
4. 完成必要编译、资源及受影响 UI 检查，记录实际验证范围。编译与预览声明不等于实机交互验证。
5. 提交前审查差异，不提交密钥、密码、机器配置、SDK 或 AAR。`origin` 为个人 fork，`upstream` 为 SagerNet；仅推送授权仓库，不强推上游。

## 验证记录

- 既有 Release 工作流：[36887951093](https://github.com/giturass/sing-box-mod/actions/runs/36887951093)，2026-10-01 成功；该产物仍使用旧应用名。
- 当前模式按钮及系统代理回退已通过本地 Debug 构建和签名验证；Clash 文件定向 Spotless 检查通过。全库仍有既有格式问题，不代表全库检查通过。
- MyBox Release：[36955861455](https://github.com/giturass/sing-box-mod/actions/runs/36955861455)，2026-10-02 成功，构建源码提交 `ee3890d`。R8、Release Lint、ARM64 架构和签名检查通过；下载产物后确认所有应用标签均为 `MyBox`，版本 `1.15.0-alpha.9`（741）。
- 产物：`MyBox-1.15.0-alpha.9-arm64-v8a.apk`；SHA-256：`9bf47a9cbdc26ccd9f1b3e39edaa30ca09149f349b389773fd4f4d40ccbcb19a`。
- B-box 本地验证（2026-10-02）：`testOtherDebugUnitTest` 8 项设置兼容/配对测试全部通过；8 个受影响 Kotlin 文件定向 Spotless 检查均为 `IS CLEAN`；`assembleOtherRelease` 成功，R8 与 Release Lint Vital 通过。工作流 actionlint 通过（Termux 下禁用外部 shellcheck/pyflakes 调用），未运行 GitHub Actions。
- 本地 APK：`app/build/outputs/apk/other/release/B-box-1.15.0-alpha.9-arm64-v8a.apk`，36,249,271 字节；SHA-256：`5cff43df19f9f1790501acac6e0da0886ff224e8d036dd704f55bf00d0bad09f`。确认包名 `io.nekohasekai.sfa`、版本 `1.15.0-alpha.9`（741）、全部应用标签 `B-box`、仅 ARM64、APK 签名验证通过；沿用本机 `.local-signing` 密钥。
- 本地核心 AAR SHA-256：`9aea3c1f5291c417e7e10782e3031cc7434f55b0facd5d6db1e4992c010193e1`，与已有 `1.15.0-alpha.9` 副本一致。图标原图 SHA-256：`51599719baf1cc226d948107412729247eb7621c8714235f9cb7865f887d60cd`，与 `/sdcard/bbox.png` 一致。
- 尚未完成本次 UI 的实机点击、深浅主题、大字体或截图回归。
