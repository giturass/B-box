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
| MOD-001 | 已实现 | 仪表盘底部浮动控制按钮，仅首页显示 | `compose/MainActivity.kt`：`isDashboardRoute`、`showStatusBar`、`showStartFab`；`compose/component/ServiceStatusBar.kt`；`dashboard/DashboardScreen.kt` |
| MOD-002 | 已实现 | 配置卡片操作重新布局 | `dashboard/ProfilesCard.kt`：`ProfilesCard`、`ProfileHeaderActions` |
| MOD-003 | 已实现 | 移除独立连接统计卡片 | `dashboard/DashboardViewModel.kt`、`DashboardCardRenderer.kt`、`DashboardSettingsBottomSheet.kt`、`DashboardScreen.kt`；删除 `ConnectionsCard.kt` |
| MOD-004 | 已实现 | Clash 模式独立矩形按钮 | `dashboard/ClashModeCard.kt`：`ClashModeCard` |
| MOD-005 | 已实现 | 项目命名与 MOD 文档 | `README.md`、`MODIFICATIONS.md`、`settings.gradle.kts`、`app/build.gradle.kts`、五个语言目录的 `strings.xml` |
| MOD-006 | 本地构建配置 | 独立签名与 Termux 低内存构建 | `.gitignore`；未提交的 `.local-signing/`、`.gradle/sfa-termux-release.init.gradle` |

## 2. MOD-001：仪表盘底部控制

**上游行为：** 运行时显示整条服务状态栏，连接/代理组按钮显示数量；停止时显示单独启动 FAB。状态栏可出现在多个一级页面。

**当前行为：**

- 手机底部布局改为三块浮动控件：活动连接、代理组、服务启动/停止。前两块用文字标签替代数字计数。
- 控件限制在真正的仪表盘 route；配置子页面、其他一级页不显示。远程会话状态条也限制在仪表盘。
- 活动连接仅在 `Status.Started` 时显示；代理组还要求 `hasGroups`。没有对应入口时保留占位，稳定服务按钮位置。
- 服务按钮停止时宽 56dp，其余状态宽 112dp；宽度动画 220ms。按钮高 56dp、圆角 16dp。
- 停止时启动服务；其他状态调用现有服务切换逻辑；`Stopping` 时禁用按钮。运行时显示运行时长。
- 大屏导航栏布局保留原有独立控件分支，限制首页显示，运行状态文字改为“停止”。不要将手机三按钮布局误描述成所有屏幕统一布局。
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

## 5. MOD-004：Clash 模式按钮

**上游行为：** `SingleChoiceSegmentedButtonRow` 分段胶囊按钮，根据文字宽度判断是否切换下拉菜单，选中项有勾选图标。

**当前行为：**

- 使用 `Column` + 等宽 `Row`，每行最多三个；不足三个的末行用占位保持列宽。
- 按钮圆角 4dp、边框 1dp，横/纵间距 16dp，文字上下内边距 8dp。
- 选中：`primary` 背景、`onPrimary` 文字；未选中：透明背景、`onPrimaryContainer` 边框和文字。
- 保留当前卡片标题；取消分段胶囊、勾选图标和宽度触发下拉菜单；长文本可以换行。
- 使用 `selectableGroup` / `Role.RadioButton` 表达单选语义。点击已选模式不再次发送切换回调，选中状态仍由传入的 `selectedMode` 驱动。

**历史证据边界：** 用户目标为“1.12.25 时期样式”。未找到可直接核验的 `1.12.25` 标签；实际参考的是 Compose 重构前 `f3763ba71da7fe1e61fa9b6b5d1f4ab42969d606`（当时 version.properties 为 1.12.13）的 `view_clash_mode_button.xml`、`bg_rounded_rectangle*.xml` 及 `OverviewFragment` 三列布局。不要声称已像素级对照 1.12.25 完成验证。

**回归：** 0/1/2/3/4+ 模式、长文本、中文/英文、窄屏、大字体、深浅主题、重复点击选中项、后端变更模式与切换失败后的实际状态。是否展示卡片仍由现有调用层控制。

## 6. MOD-005：品牌和开源归属

- README 标题、应用所有现有语言资源的 `app_name` 使用 `sing-box mod`。
- Gradle 根项目名和 APK 名称前缀使用不带空格的 `sing-box-mod`。
- 保留 `io.nekohasekai.sfa` applicationId/namespace、上游版本号、上游代码包路径，避免无关迁移。包名相同意味着不同签名的安装包不能直接覆盖，也不能并行安装。
- 保留 `LICENSE` 和 README 原有版权/许可文本，明确非官方分支；维护者发布前应阅读其中名称与关联声明。本文不替换原许可，也不将其简化为另一份授权。

**回归：** 五个现有 locale 的应用标签一致；安装器/启动器显示新名称；APK 文件名使用新前缀；不修改数据库、ContentProvider、VPN 或 Xposed 标识。

## 7. MOD-006：构建、签名和二进制边界

### 通用构建

以仓库 Gradle 文件为准安装 JDK/Android SDK/NDK。当前 compileSdk 37（minor 1）、JVM 17 字节码，本机使用 JDK 21。先准备与版本及接口匹配的 `app/libs/libbox.aar`；legacy flavor 另需 `libbox-legacy.aar`。这些文件被忽略，单独 clone 此仓库不能立即构建。上游资料入口见 README 的 Documentation。

标准 Gradle release 签名读取 `local.properties` 或环境变量 `LOCAL_PROPERTIES` 中的 `KEYSTORE_PASS`、`ALIAS_NAME`、`ALIAS_PASS`，默认 keystore 路径为 `app/release.keystore`。仓库中的该文件继承自上游，不是本 MOD 新生成的签名密钥；本机使用下面的独立签名覆盖，不复用上游凭据。

```sh
./gradlew :app:assembleOtherRelease
```

该命令以 SDK、核心 AAR 和签名配置均已准备好为前提。首次解析依赖需联网。不要将密码提交到 Git 或打印到构建日志。

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
- Clash 文件经过定向 Spotless 检查，差异经过 `git diff --check`。这不表示所有上游文件均通过全库格式检查。
- 改名后已通过 `:app:processOtherReleaseResources`，五个 locale 的 `app_name` XML 检查一致；尚未重新生成改名后的 APK。此前生成的 `SFA-1.15.0-alpha.9-arm64-v8a.apk` 不包含本次名称调整，不能作为已改名版本发布。
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
