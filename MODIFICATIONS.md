# sing-box mod：差异清单与 AI 维护指南

## 0. 文档范围与比较基线

| 字段 | 值 |
| --- | --- |
| 项目显示名称 | `sing-box mod` |
| GitHub 仓库 | `https://github.com/giturass/sing-box-mod` |
| 上游 | `https://github.com/SagerNet/sing-box-for-android` |
| 上游分支 | `dev` |
| 本次比较基线 | `8e42c63c4771de10b20dd2562704850c604518d8`（Remove remote control retries） |
| 上游应用版本 | `1.15.0-alpha.9`，versionCode `741` |
| 核对日期 | 2026-09-30 |
| 差异来源 | 已拉取的上游基线与本地工作树逐文件比较，包含此前未提交的 UI 修改 |

这是维护规范和实际改动索引，不是功能愿望清单。未标为已实现的内容不能当作已有功能。后续修改每项行为时，同步更新对应 MOD 编号；同步上游后更新基线 SHA，并重新检查所有 MOD 的约束。

**继承关系：** VPN/代理核心、路由、协议支持、远程控制、配置导入导出、Xposed/Shizuku/Root 能力等来自上游，本次没有为它们增加新功能。不能将这些上游能力列为本 MOD 独创功能。`app/libs/libbox.aar` 是本机构建输入，不在 Git 中；应用源码差异不代表已审计或可复现核心二进制差异。

## 1. 修改索引

下表中的 `dashboard/` 指 `app/src/main/java/io/nekohasekai/sfa/compose/screen/dashboard/`，`compose/` 指 `app/src/main/java/io/nekohasekai/sfa/compose/`。

| ID | 状态 | 修改 | 主要文件/符号 |
| --- | --- | --- | --- |
| MOD-001 | 已实现 | 仪表盘底部浮动控制按钮，仅首页显示 | `compose/MainActivity.kt`：`isDashboardRoute`、`showStatusBar`、`showStartFab`；`compose/component/ServiceStatusBar.kt`、`ServiceStartButton.kt`；`dashboard/DashboardScreen.kt` |
| MOD-002 | 已实现 | 配置卡片操作重新布局 | `dashboard/ProfilesCard.kt`：`ProfilesCard`、`ProfileHeaderActions` |
| MOD-003 | 已实现 | 移除独立连接统计卡片 | `dashboard/DashboardViewModel.kt`、`DashboardCardRenderer.kt`、`DashboardSettingsBottomSheet.kt`、`DashboardScreen.kt`；删除 `ConnectionsCard.kt` |
| MOD-004 | 已回退 | Clash 模式恢复上游分段按钮/下拉菜单 | `dashboard/ClashModeCard.kt`：`ClashModeCard` |
| MOD-005 | 已实现 | 项目命名与 MOD 文档 | `README.md`、`MODIFICATIONS.md`、`settings.gradle.kts`、`app/build.gradle.kts`、五个语言目录的 `strings.xml` |
| MOD-007 | 已实现，debug/release 构建通过 | FlClash 风格启动按钮与配套快捷入口 | `compose/component/ServiceStartButton.kt`、`ServiceStatusBar.kt`、`compose/MainActivity.kt` |
| MOD-006 | 本地及 CI 构建配置 | 独立签名、Termux 低内存构建、手动 ARM64 Release 工作流 | `.github/workflows/build-release.yml`、`app/build.gradle.kts`、`.gitignore`；未提交的 `.local-signing/`、`.gradle/sfa-termux-release.init.gradle` |

## 2. MOD-001：仪表盘底部控制

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

## 3. MOD-002：配置卡片

**上游行为：** 标题栏只有添加按钮；编辑、更新、分享操作位于卡片底部。

**当前行为：** 标题栏右侧按分享、编辑、添加排列。有选中配置才显示分享与编辑，空配置列表仍可添加。标题单行省略，避免挤压操作区。远程配置更新按钮放在配置类型/更新时间的信息行右侧，保留更新中禁用、加载指示和成功勾选反馈。

**维护约束：** 分享、保存文件、JSON、URL、二维码等原有回调仍被保留；不要因布局移动丢失分支。更新按钮仅适用于 `TypedProfile.Type.Remote`。

**回归：** 空列表、本地/远程配置、超长名称、更新成功/失败、各类分享导出、配置选择和编辑。

## 4. MOD-003：连接统计卡片移除

**上游行为：** 仪表盘有独立入站/出站连接数量卡片，与 Debug 卡片组成 Statistics 双列分组。

**当前行为：** 删除独立 `ConnectionsCard`、`CardGroup.Connections`、`CardPairGroup.Statistics` 及卡片专用 `connectionsIn`/`connectionsOut` UI 字段。同步移除渲染、默认顺序、可见性及仪表项设置入口。Debug 不再属于 Statistics 配对。

**兼容性：** 现有 `stringToCardGroup()` 捕获未知枚举名，旧设置中的 `Connections` 在加载顺序和禁用项时被忽略；没有新增数据库迁移。活动连接页面/弹层、`connectionsCount` 和底层连接数据仍保留。

**回归：** 从包含 `Connections` 的旧卡片顺序/隐藏项升级；重置仪表项；拖动排序；显示/隐藏 Debug；流量卡片配对；打开活动连接列表。

## 5. MOD-004：Clash 模式按钮（已回退）

**当前行为：** 按用户要求撤回旧版独立矩形按钮，`ClashModeCard.kt` 完整恢复为比较基线 `8e42c63` 的上游实现：`SingleChoiceSegmentedButtonRow` 分段按钮，文字宽度超出时自动显示下拉菜单，选中项显示勾选图标。此文件当前相对上游无差异。

**历史：** `118c2fe` 曾引入 4dp 圆角、1dp 描边、每行最多三个的旧式按钮。该修改已撤回，后续同步不得按旧文档重新引入。历史参考来自 1.12.13 时期源码，并未直接核验 1.12.25。

**回归：** 普通模式名称显示分段按钮；窄屏/长模式名切换下拉菜单；选中模式与后端状态一致；深浅主题正常。

## 6. MOD-005：品牌和开源归属

- README 标题、应用所有现有语言资源的 `app_name` 使用 `sing-box mod`。
- Gradle 根项目名和 APK 名称前缀使用不带空格的 `sing-box-mod`。
- 保留 `io.nekohasekai.sfa` applicationId/namespace、上游版本号、上游代码包路径，避免无关迁移。包名相同意味着不同签名的安装包不能直接覆盖，也不能并行安装。
- 保留 `LICENSE` 和 README 原有版权/许可文本，明确非官方分支；维护者发布前应阅读其中名称与关联声明。本文不替换原许可，也不将其简化为另一份授权。

**回归：** 五个现有 locale 的应用标签一致；安装器/启动器显示新名称；APK 文件名使用新前缀；不修改数据库、ContentProvider、VPN 或 Xposed 标识。

## 7. MOD-006：构建、签名和二进制边界

### 通用构建

以仓库 Gradle 文件为准安装 JDK/Android SDK/NDK。当前 compileSdk 37（minor 1）、JVM 17 字节码，本机使用 JDK 21。先准备与版本及接口匹配的 `app/libs/libbox.aar`；legacy flavor 另需 `libbox-legacy.aar`。这些文件被忽略，单独 clone 此仓库不能立即构建。上游资料入口见 README 的 Documentation。

标准 Gradle release 签名依次读取直接环境变量、Base64 环境变量 `LOCAL_PROPERTIES`、`local.properties` 中的 `KEYSTORE_PASS`、`ALIAS_NAME`、`ALIAS_PASS`，默认 keystore 路径为 `app/release.keystore`。仓库中的该文件继承自上游，不是本 MOD 新生成的签名密钥；本机使用下面的独立签名覆盖，不复用上游凭据。

```sh
./gradlew :app:assembleOtherRelease
```

该命令以 SDK、核心 AAR 和签名配置均已准备好为前提。首次解析依赖需联网。不要将密码提交到 Git 或打印到构建日志。

### GitHub Actions 手动 ARM64 Release

- 入口为 `.github/workflows/build-release.yml`，仅 `workflow_dispatch` 触发；Secret 配置与下载步骤见 README。运行前需配置自有签名文件及密码，缺失时立即失败。
- 核心来源为 `SagerNet/sing-box` 的 `v${VERSION_NAME}` 标签，Go 版本取自 `version.properties`；按上游 `make lib_install` 和 `build_libbox -target android -platform android/arm64` 构建。上游脚本会额外生成 legacy AAR，但 CI 只使用标准 `libbox.aar`，仅生成 `otherRelease` APK。此 CI 核心来源不等同于此前本地 AAR 的来源或二进制可复现性证明。
- 上游 libbox 脚本明确要求 JDK 17，应用 Gradle 阶段使用 JDK 21；安装 SDK 36、37.1，Build Tools 36.0.0、37.0.0 和 NDK 28.0.13004108。升级构建配置时同步核对工作流。
- 新增 `-Parm64Only=true` 开关，启用时只拆分 `arm64-v8a` 并关闭 universal；未设置时保留原有多 ABI 与 universal 行为。直接环境变量优先用于签名，原有 `LOCAL_PROPERTIES`/本地配置继续支持。
- 保留 R8 和 release Lint；上传前检查 APK 数量、名称、原生 ABI 集合及签名，只上传一个 ARM64 APK 到 Actions Artifacts（30 天），不自动创建 GitHub Release。密钥仅在 runner 解码并于结束时清理，不上传密钥或核心 AAR。
- 回归：默认/ARM64 开关的 ABI 配置、签名 Secret 缺失时的失败、核心版本匹配、单个 APK 及签名验证；未实际运行 GitHub 工作流前不能声明云端构建通过。

### 已使用的 Termux 构建方式

- 本机 Build Tools 37.0.0，NDK 29.0.14206865；Termux 原生 aapt2/aidl，不能直接执行 SDK 自带的 Linux x86_64 二进制。
- 本地 init script 覆盖 Build Tools/NDK 路径、仅构建 ARM64、关闭 universal APK，并给 release 指定独立签名配置。
- 密钥位于 `.local-signing/sfa-release.p12`，alias `sfa-release`，密码文件 `.local-signing/release-password.txt`；目录已忽略。后续更新必须保留同一密钥，不能每次构建重新生成。
- `.gradle/sfa-termux-release.init.gradle` 和机器 SDK 路径不会上传；新机器需自行建立等价本地配置。本文记录环境，不承诺一键跨设备复现。

```sh
gradle -I .gradle/sfa-termux-release.init.gradle :app:assembleOtherRelease \
  --offline --console=plain --max-workers=1 --no-daemon \
  '-Dorg.gradle.jvmargs=-Xmx2048m -XX:ActiveProcessorCount=2 -Dfile.encoding=UTF-8' \
  -Pkotlin.compiler.execution.strategy=in-process
```

`--offline` 仅在依赖已完整缓存后使用。曾先后遇到：默认 Build Tools 36.0.0 缺失；构建进程无明确异常退出；release/Lint 依赖离线缺失；Lint 在线版本查询等待。处理方式分别为使用本机 SDK 覆盖、2GB 堆/单 worker、联网补齐依赖、缓存完整后离线检查。不要直接禁用压缩或跳过 Lint 来掩盖错误。

### 验证记录与限制

- 改名前的 UI 修改已完成 `assembleOtherRelease`（包含 R8 和 release Lint），并通过 `apksigner verify`：RSA 3072、自有签名、v2 校验成功，ARM64 APK 约 35MB。
- 历史 Clash 修改经过定向 Spotless 检查，差异经过 `git diff --check`。这不表示所有上游文件均通过全库格式检查。
- 改名后已通过 `:app:processOtherReleaseResources`，五个 locale 的 `app_name` XML 检查一致；已在后续维护中生成包含 MOD-007 的 debug/release APK（见第 10 节）。此前生成的 `SFA-1.15.0-alpha.9-arm64-v8a.apk` 不包含本次名称调整，不能作为已改名版本发布。
- 尚无本次 UI 的实机安装/点击回归或截图证据；编译成功不能替代上述各 MOD 的人工回归。
- 此次 GitHub 发布范围是源码和维护文档；密钥、密码、本地配置、构建日志、SDK 和 APK 不随源码提交。

## 8. AI 接手流程与上游同步

1. 先读此文档、README、适用的 AGENTS.md，运行 `git status --short`；保留用户未提交改动，不执行清理式 reset。
2. 以固定 SHA 对比，不能把“上游最新版本”当作稳定基线。使用 `git diff <baseline>..HEAD`，未提交改动另看 `git diff`。
3. 修改前定位 MOD ID 和具体符号；没有需求时，不改核心协议、服务生命周期、包名、签名或其他上游功能。
4. 同步时获取上游 `dev`，在单独分支合并、解决冲突并检查 MOD-001 至 MOD-006，尤其是 `CardGroup` 枚举/设置序列化和 `MainActivity` 本地/远程分支。
5. 完成必要编译、资源检查和受影响 UI 回归；更新文档基线与验证范围，保留“未验证”事实。
6. 发布前审查 staged diff，确认不含 `.local-signing/`、密码、`local.properties` 或核心 AAR；仅推送自己的 fork，不能 force-push 上游。

本仓库发布后的 remote 约定：`origin` 指向 MOD fork，`upstream` 指向 SagerNet。可参考：

```sh
git fetch upstream
git switch -c maintenance/sync-upstream
git merge upstream/dev
# 解决冲突，验证各 MOD，更新本文件基线后，再按项目流程发布。
```

后续每次行为变更至少补充：MOD ID、上游前后差异、入口文件/符号、兼容性约束、实际验证结果、剩余限制。不要把假设、计划或仅成功编译的结果写成已经过设备验证的事实。

## 9. MOD-007：FlClash 风格启动按钮

**来源：** [chen08209/FlClash](https://github.com/chen08209/FlClash)，参考提交 `c7be7023d33615cb624148d41414f80a7d96cede` 的 [`lib/views/dashboard/widgets/start_button.dart`](https://github.com/chen08209/FlClash/blob/c7be7023d33615cb624148d41414f80a7d96cede/lib/views/dashboard/widgets/start_button.dart) 和 `test/widgets/start_button_test.dart`。参考项目采用 GPL-3.0，原许可见其 [LICENSE](https://github.com/chen08209/FlClash/blob/c7be7023d33615cb624148d41414f80a7d96cede/LICENSE)。本实现为原生 Compose 适配，不引入 Flutter 依赖或 FlClash 核心。

**取代历史：** 用户明确选择“跟随 flclash”，因此暂停双竖线取代此前要求的正方形运行图标。MOD-004 的 Clash 模式回退仍然保留；不要混淆 Clash 模式选择和 FlClash 风格服务按钮。

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

## 10. 后续维护记录

- 2026-10-01：扩展 MOD-006，新增手动 ARM64 Release 工作流及签名 Secret 文档。actionlint 1.7.12、Bash 语法、ShellCheck、`git diff --check` 通过；签名缺失处理、Base64 解码、版本读取及既有 APK 的 ABI 校验通过。本机 Gradle 9.7.1 的 Release `--dry-run` 通过；独立配置检查确认 ARM64 模式仅 1 个输出、关闭开关时保留 4 个 ABI 加 universal 共 5 个输出，直接环境变量签名配置生效。未运行 GitHub 云端工作流，未重新编译核心或生成新 APK。

- 2026-09-30：回退 MOD-004 至上游；MOD-001 的已启动按钮改为左侧已启动图标、右侧时间（手机与大屏），保留点击停止行为。已通过 `:app:compileOtherReleaseKotlin` 与 `git diff --check`，并核对 Clash 文件与上游基线完全一致；后续已完成 debug 打包及签名验证；未执行实机 UI 回归。

- 2026-09-30：`assembleOtherDebug` 成功（7m 7s），产物 `sing-box-mod-1.15.0-alpha.9-arm64-v8a-debug.apk` 约 64MB，包含改名、Clash 回退及已启动图标。使用 `.gradle/sfa-termux-debug.init.gradle` 将 debug 签名指向既有本地密钥；单 worker、禁用并行、2GB 堆、`ActiveProcessorCount=2`、Kotlin 进程内编译。`apksigner verify` 通过，证书与此前本地 release 一致。

- 2026-09-30：运行图标恢复原正方形 `Stop`；手机三个已运行控件统一左侧 8dp 内边距、20dp 图标及 4dp 图文间距，避免短计时文本将图标推向中间。未启动时的启动图标仍居中。按钮标签简化为“连接”/“Connections”，资源键保持不变。单 worker 的 `compileOtherDebugKotlin` 与 `processOtherDebugResources` 检查通过；随后已完成 `assembleOtherDebug` 打包（1m 1s），ARM64 debug APK 约 66MB，签名校验通过；未进行实机布局验证。

- 2026-09-30：新增 MOD-007，用户选择播放/暂停图标，替代此前正方形方案；保留现有配色并调整快捷入口。低并发 `assembleOtherDebug` 通过（最终增量构建 1m 35s），APK 签名校验通过；当前无连接的 ADB 设备，未做实机交互或截图验证。

- 2026-09-30：包含 MOD-007 的 `assembleOtherRelease` 成功（11m 37s），完成 R8 压缩及 release Lint；产物 `sing-box-mod-1.15.0-alpha.9-arm64-v8a.apk` 约 35MB。使用既有本地签名、单 worker、禁用并行、2GB 堆、`ActiveProcessorCount=2`、Serial GC、`CICompilerCount=2` 和 Kotlin 进程内编译，未发生线程或内存崩溃。`apksigner verify` 通过（v2、RSA 3072）；核对应用标签 `sing-box mod`、版本 `1.15.0-alpha.9`（741）及 ARM64 架构。未执行实机 UI 回归。
