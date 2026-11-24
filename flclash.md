# FlClash UI 使用情况

本文记录界面来源、复用和适配范围。Android 后端差异见 [app.md](app.md)，核心扩展见 [libbox.md](libbox.md)，维护约束见 [AGENTS.md](AGENTS.md)。

## 参考版本与来源清单

FlClash 应用层固定参考 [chen08209/FlClash v0.9.3](https://github.com/chen08209/FlClash/tree/be84736605527034689c95bf27828ff27d912c5e)，提交 `be84736605527034689c95bf27828ff27d912c5e`（2026-11-04）。共享组件独立同步到 [prism_ui 0.1.3](https://github.com/chen08209/prism_ui/tree/7831ade349ede896aedb8a5757dc9c3fe88081d5)，提交 `7831ade349ede896aedb8a5757dc9c3fe88081d5`（2026-11-19，上游 `main` 与 `v0.1.3` 标签指向同一提交）。FlClash 参考提交中的 `plugins/prism_ui` 仍为 `63450c0ac0e8e1b2c0f3ed4ad7926dc94e083185`，由清单的 `flclashSubmoduleCommit` 单独记录。当前使用实际 Flutter 组件，历史 Compose 仿制按钮和卡片不再定义界面。

[flutter/flclash-manifest.json](flutter/flclash-manifest.json) 是逐文件来源清单：

| 清单字段 | 使用方式 |
| --- | --- |
| `files` | 直接导入的 14 个 FlClash 应用层 UI 文件及两个字体；哈希对应将 Dart 包引用从 `fl_clash` 机械改名为 `b_box_ui` 后的文件 |
| `adaptedFiles` | 仪表盘、配置/代理、外观、备份、选项列表行、IP 质量查询及参考测试等适配文件；记录原始来源哈希和实际改动说明 |
| `adaptedNativeFiles` | `libbox/ui_egress.go` 对 FlClash IP 查询的适配来源；独立保留 v0.9.2 的 `7fd417fb9a9a277c6a9339a8ac87f907df7c25f7` 参考，不随界面升级改写原生查询 |
| `prismUi` | 独立组件提交与 FlClash 子模块参考、189 个原样导入文件、两个 SDK 导入兼容补丁、两个授权列表宽度适配、B-box 导航栏与日志流适配及应用内许可资源；共 193 个本地包文件，包含源码、生成模型、两个 shader、包配置、许可、说明和 39 个上游测试及辅助文件 |
| `supplementalReferences` | 独立于整体 UI 基线的 MITM dev、正文查看、声明式复合匹配及脚本事件/调试补充参考，按原样导入、改写或设计参考记录源文件哈希；本地路径相对仓库根目录 |

清单中 `files` 和 `adaptedFiles` 的本地路径相对 `flutter/`，`adaptedNativeFiles` 的本地路径相对仓库根目录；`source`/`sources` 指向各自固定参考提交内的上游路径。跨仓库的 `supportingSources` 单独标注 `repository` 和 `commit`，未标注的沿用所属清单参考。`prismUi.files` 相对其 `localPath`，记录实际本地文件哈希；其中的适配文件和许可资源相对 `flutter/`，适配条目单独保留原始上游 `sourceSHA256`。清单记录逐文件哈希和改动来源，本文维护使用说明，两者一并保留。

请求/MITM 功能独立参考 FlClash `dev` 的 [`63aa150e23288558074d877f745a6d3b5f6418f6`](https://github.com/chen08209/FlClash/tree/63aa150e23288558074d877f745a6d3b5f6418f6)，相关提交为 [`f54486102172566e7342d7f5c138663c3c5dec0b`](https://github.com/chen08209/FlClash/commit/f54486102172566e7342d7f5c138663c3c5dec0b) 的 MITM 应用侧设计和 [`1a7556d30a38707f39abfba4b7d6a6b8f57774ed`](https://github.com/chen08209/FlClash/commit/1a7556d30a38707f39abfba4b7d6a6b8f57774ed) 的请求/连接整合。该 dev 源码的 `FEATURE_MITM` 默认关闭，公开 core 不提供完整 MITM 实现；B-box 自行实现 sing-box 路由上的 TLS 接管，不能把上游页面当成可用核心。采用证书信任、报文展示和 CA 模块设计；统一请求查看器的交互参考 Surge [近期请求说明](https://kb.nssurge.com/surge-knowledge-base/zh/guidelines/troubleshooting)及[近期/活动请求 API](https://manual.nssurge.com/tools/http-api.html)，以 B-box 的真实连接和抓取记录实现。该补充参考与 v0.9.3 UI、Android 上游和核心基线分别维护。

声明式复合匹配独立参考 FlClash [`5e579c4987e50e5c68e6023cc31ff4db55d480bb` 的 loon_rewrite.dart](https://github.com/chen08209/FlClash/blob/5e579c4987e50e5c68e6023cc31ff4db55d480bb/lib/common/loon_rewrite.dart#L247) 中的 URL、方法、请求/响应头及状态条件。清单单独记录源文件哈希和设计参考用途；B-box 在自身 HTTP 处理引擎中实现结构化 JSON 条件和嵌套 all/any，不导入整套 Loon 解析器。语法及运行语义见 [HTTP 改写配置](libbox.md#json-配置中的-http-改写)，整体 UI 参考提交不变。

脚本事件与调试独立参考同一固定提交的 [mitm_module.dart](https://github.com/chen08209/FlClash/blob/5e579c4987e50e5c68e6023cc31ff4db55d480bb/lib/common/mitm_module.dart#L505) 及 [mitm_modules.dart](https://github.com/chen08209/FlClash/blob/5e579c4987e50e5c68e6023cc31ff4db55d480bb/lib/views/config/mitm_modules.dart#L38)：采用三个事件名字、Cron/event/generic 的立即运行入口、防重复执行和结果/错误/console 展示。来源清单记录两份原文件哈希及设计参考用途；B-box 自行实现 sing-box JSON、内核事件、配置修订及数组下标选择、可取消 Goja 执行和本地/远程隔离。该补充参考不更新整体 UI、Android 或核心基线，也不导入模块解析器。

正文查看独立参考同一固定提交的 [readMitmBody](https://github.com/chen08209/FlClash/blob/5e579c4987e50e5c68e6023cc31ff4db55d480bb/lib/common/mitm_body.dart#L64)、[mitm_protobuf.dart](https://github.com/chen08209/FlClash/blob/5e579c4987e50e5c68e6023cc31ff4db55d480bb/lib/common/mitm_protobuf.dart) 及[正文查看组件](https://github.com/chen08209/FlClash/blob/5e579c4987e50e5c68e6023cc31ff4db55d480bb/lib/features/connection/mitm_body.dart)。采用文本/图片/二进制分类、无类型定义的 Protobuf/gRPC 展示、十六进制行、折行和缩放交互，完整正文查看与操作直接放在请求/响应详情中；清单记录改写与交互参考的原始哈希，并列出 Go 完整文件解析的适配路径。B-box 使用 sing-box 保存的正文、原生流式解码与派生文件、原文保真 JSON 格式化和 Prism 控件；Flutter 按需读取字节页或派生文本页，字符集由自身原生读取接口提供，页面没有上游 Rust 字符集或编辑器依赖。整体 UI、Android 和核心基线分别维护。

FlClash 应用层组件位于 [flutter/lib/ui](flutter/lib/ui)，共享控件以本地包 [flutter/packages/prism_ui](flutter/packages/prism_ui) 使用，保留原始源码与 shader，不再依赖旧 `material_ui`/`cupertino_ui`。原始许可见 [FlClash GPL](flutter/assets/licenses/LICENSE)、[prism_ui LICENSE](flutter/packages/prism_ui/LICENSE) 和 [NOTICE](flutter/packages/prism_ui/NOTICE)。Prism 内置的 shapes、sliver list、resizable navigator 的 MIT 声明另从源文件头提取至 [应用许可资源](flutter/assets/licenses)，与 HCT/Flutter 的 Apache/BSD 及 SDK、插件许可一起在 [许可证页](flutter/lib/pages/licenses.dart) 展示。该页面按包名列出、搜索并打开可选中的完整正文。

[组件 pubspec](flutter/packages/prism_ui/pubspec.yaml) 要求 Dart 3.10+、Flutter 3.47+；0.1.3 的 SDK 要求和依赖与此前固定版本相同。应用继续使用 [setup-flutter](.github/actions/setup-flutter/action.yml) 固定的 Flutter 3.47.6，[锁文件](flutter/pubspec.lock) 仅更新本地 Prism 包的版本。

0.1.3 删除了 `prism_list.dart` 与 `route_motion_hold.dart` 的显式 rendering 导入，但 Flutter 3.47.6 的 [`widgets/viewport.dart`](https://github.com/flutter/flutter/blob/3.47.6/packages/flutter/lib/src/widgets/viewport.dart#L19) 不导出这两个文件使用的 [`ScrollCacheExtent`](https://github.com/flutter/flutter/blob/3.47.6/packages/flutter/lib/src/rendering/viewport.dart#L42)。这两份文件仅恢复 `import 'package:flutter/rendering.dart' show ScrollCacheExtent;`。另在 `list_selected.dart` 与 `list_row.dart` 增加缺省开启的 `constrainTrailing` 参数；仅[忽略电池优化项](flutter/lib/components/battery_optimization.dart)关闭它，恢复 FlClash 参考子模块的尾部完整宽度与文字继承规则，避免英文及大字号授权按钮被 0.1.3 的长值限宽压缩。四份适配的原始来源哈希列入 `prismUi.adaptedFiles`，其余 189 个包文件原样保留。

字体归属见 [JetBrainsMono-OFL.txt](flutter/assets/licenses/JetBrainsMono-OFL.txt) 和 [Twemoji-LICENSE.md](flutter/assets/licenses/Twemoji-LICENSE.md)。Twemoji 原始图形来自 Twitter，字体转换由 Mozilla 提供，图形未修改；字体许可同样用于应用内许可证页。

应用内手工登记的许可由 [main.dart](flutter/lib/main.dart) 统一注册，与 [pubspec.yaml](flutter/pubspec.yaml) 打包的 [许可资源](flutter/assets/licenses) 同步维护，覆盖当前 UI、字体和核心扩展依赖。核心正文解码及 jq 的许可包括 [brotli-LICENSE](flutter/assets/licenses/brotli-LICENSE)、[klauspost-compress-LICENSE](flutter/assets/licenses/klauspost-compress-LICENSE)、[gojq-LICENSE](flutter/assets/licenses/gojq-LICENSE) 和 [timefmt-go-LICENSE](flutter/assets/licenses/timefmt-go-LICENSE)；JavaScript 与 Cron 的许可也由同一入口登记。依赖版本及执行范围见 [libbox.md](libbox.md#独立脚本与-cron)。

应用打包的字体均有实际用途：[JetBrainsMono-Regular.ttf](flutter/assets/fonts/JetBrainsMono-Regular.ttf) 用于代码编辑、请求报文、记录详情及终端预览，[Twemoji.Mozilla.ttf](flutter/assets/fonts/Twemoji.Mozilla.ttf) 用于代理名称等 Emoji 文本。[NotoSansSC-status.ttf](flutter/test/support/fonts/NotoSansSC-status.ttf) 仅供状态卡像素比较测试使用，不进入应用资源。

## 界面复用与业务适配

Flutter 承担仪表盘、配置、工具、设置及所有子页面的导航；配置页合并配置文件与代理分组。复用 FlClash 的控件、字形、形状、排版、字体、动效、弹层及导航；页面状态和操作经 Android 桥接接入 B-box 的 Room 与 libbox。没有引入 FlClash/Mihomo 原生库、Clash 完整配置模型、Mihomo 配置覆写脚本或 Clash 专用服务。节点订阅解析由 libbox 提供，范围见 [节点导入与配置合并](libbox.md#节点导入与配置合并)。B-box 请求/响应、定时、事件与 Generic 脚本的范围见 [独立脚本与 Cron](libbox.md#独立脚本与-cron)。

v0.9.3 使用 `PrismApp`、Prism 主题、表面、按压反馈、输入、弹窗和返回保护；原生操作消息交给 `BannerHost`，采用 Prism 的工具栏避让间距，复制和保存失败反馈期间仍可使用顶部按钮。B-box 表单在输入框上方保留字段标签，脏草稿确认及保存期间禁止退出继续由 `BoxEditGuard` 处理。搜索使用新版回调，连接/日志的可删除关键词由页面维护，并与输入文字共同过滤；排序列表使用 Prism 的最终索引回调，代理标签页显式管理控制器并按组名保留选择。网络速度卡片跟进新版 1.45 最小比例、数值下方的背景曲线及独立重绘区域；外观预览改用逐形状模糊，调色和分组标签按新版测量接口适配。状态参考渲染器同步到 v0.9.3，采用上游普通状态，不引入其 Root 服务边框含义。

Prism UI 0.1.3 原样同步列表长尾值截断、折叠分组前置内容的动态间距、反向列表初始位置、嵌套弹层返回及按压取消反馈，新增分组标题说明、成功/警告填充前景色、弹窗缩放起点和正文局部加粗接口。成功/警告容器使用新版低彩度色板；字体、生成模型、shader 及许可正文未变。列表尾部锚定改为显式选择，[日志页](flutter/lib/pages/logs.dart) 设置 `anchorAtEnd: true`，继续由应用管理跟随、滚动暂停、冻结记录和恢复；普通列表沿用新版默认行为。[选项列表行](flutter/lib/ui/list_item.dart) 的值按钮增加弹性宽度约束，长出口标签在新版尾部限宽下保留标题和选择图标，显示截断不改变实际传给后端的值；该文件由直接导入转为明确适配。配置卡的更新时间也接收尾部剩余宽度，保留既有单行省略、完整时间提示、加载动画和固定对号。固定导航栏的 64 dp 高度、主背景色和底部安全区适配继续保留。

| 区域 | 主要实现 | 当前适配 |
| --- | --- | --- |
| 首页与状态 | [dashboard.dart](flutter/lib/pages/dashboard.dart)、[status_panel.dart](flutter/lib/components/status_panel.dart)、[start_toggle.dart](flutter/lib/components/start_toggle.dart) | 复用状态布局、国家徽章、服务按钮形变及网络速率展示；启停调用原生服务流程 |
| 配置卡与分组概览 | [profile_panel.dart](flutter/lib/components/profile_panel.dart)、[proxy_groups_panel.dart](flutter/lib/components/proxy_groups_panel.dart) | 使用真实配置元数据和内核分组；配置切换按钮固定在右侧，与进入配置的箭头同列居中对齐，按钮名称位于其独立 32 dp 点击语义中。代理组、出站、路由规则数字及加载占位与对应标签逐列居中对齐，远端未知数量保持未知 |
| 配置页代理区域 | [proxies.dart](flutter/lib/pages/proxies.dart)、[proxy_card.dart](flutter/lib/components/proxy_card.dart)、[proxy_list.dart](flutter/lib/components/proxy_list.dart) | 在配置文件下方显示 libbox 分组，保留卡片/列表、搜索、测速与节点定位；只有可选择的组显示选择操作。本机停服时由 libbox 读取所选配置的 selector/urltest 并允许选择手动组，启停前后复用同一分组界面；编辑从节点菜单进入 |
| 配置文件管理 | [profiles.dart](flutter/lib/pages/profiles.dart)、[profile_card.dart](flutter/lib/components/profile_card.dart)、[profile_details.dart](flutter/lib/pages/profile_details.dart)、[subscriptions.dart](flutter/lib/pages/subscriptions.dart) | 顶部横向配置卡片复用行高、对号选中态与相对更新时间，卡片外边框与代理区域使用相同的 16 dp 左右间距；默认文件显示为“配置文件”并与已有配置一同显示，保留添加、更新、排序、编辑和导入 sing-box JSON；URL 节点导入当前文件并保留独立来源，编辑页可管理来源，切换弹层使用自适应网格 |
| 首页编辑 | [dashboard_editor.dart](flutter/lib/components/dashboard_editor.dart) | 右上角 FlClash 铅笔按钮或长按卡片进入原地编辑，保留摇摆、长按排序、移除徽章及添加列表 |
| 内存与局域网 | [memory_panel.dart](flutter/lib/components/memory_panel.dart)、[intranet_panel.dart](flutter/lib/components/intranet_panel.dart) | 分别使用 Go/进程内存和 sing-box 监听配置，不虚构 Mihomo 内存或全局代理端口 |
| 系统 HTTP 代理 | [system_proxy_panel.dart](flutter/lib/components/system_proxy_panel.dart) | 采用参考版 VPN 卡片的布局、状态徽章和紧凑开关，使用 B-box 自有的大陆轮廓地球字形；开关接入内核上报状态及既有 Android 桥接 |
| 外观与关于 | [appearance.dart](flutter/lib/pages/appearance.dart)、[about.dart](flutter/lib/pages/about.dart) | 复用主题预览、调色和版本/许可布局，保持 B-box 品牌及其 Release 来源；关于页使用与 Android 启动器同源的[品牌图标](app.md#比较基线与应用身份) |
| 备份 | [backup.dart](flutter/lib/pages/backup.dart)、[form.dart](flutter/lib/components/form.dart) | 复用本地/远程分区、WebDAV 表单、连通状态、恢复范围及策略，后端使用 `bbox-1` 格式 |
| 连接与请求 | [requests.dart](flutter/lib/pages/requests.dart)、[connection_record.dart](flutter/lib/components/connection_record.dart)、[request_settings.dart](flutter/lib/pages/request_settings.dart)、[mitm_certificate.dart](flutter/lib/pages/mitm_certificate.dart)、[capture_domains.dart](flutter/lib/pages/capture_domains.dart) | 统一网络活动列表与详情、连接管理、本机录制、独立 MITM 与证书、TCP 流、域名选择及 HAR/cURL 导出；复用共享列表、表单和导航 |
| WARP 注册 | [warp.dart](flutter/lib/pages/warp.dart)、[tools.dart](flutter/lib/pages/tools.dart) | 复用列表、表单、按钮和返回保护，可选 MASQUE / WireGuard，分别追加到所选配置的 `outbounds` / `endpoints`；页面为 B-box 业务代码 |

记录组件使用 [record.dart](flutter/lib/components/record.dart) 中 `RecordTone` 的标签前景色和容器色；首页拖动浮层使用 [dashboard_editor.dart](flutter/lib/components/dashboard_editor.dart) 的 `DashboardShadows.e2` 阴影。应用层已移除未调用的强调色和一级阴影辅助实现，适配差异记录在来源清单；像素比较的独立状态参考渲染器继续保留自己使用的阴影实现。

### 导航与交互约定

Android UI 生命周期、后台退出及原生状态恢复的约定见 [UI 与后台进程](app.md#ui-与后台进程)。

关于页单独显示 B-box 应用版本与实际内核版本，“检查更新”打开独立的 [更新弹窗](flutter/lib/components/app_update_dialog.dart)。弹窗区分完整检查、未完成检查和缺少元数据；部分发布暂时不可访问时保留确认过的候选，没有完整结果或元数据时不显示“已是最新版本”。有可用安装包时在应用内显示下载进度、取消及失败重试；关闭弹窗或离开页面后，关于页的“查看更新”可再次进入当前下载或已验证 APK 的状态。安装由系统确认，需要未知来源授权时给出说明，返回后由用户再次点击安装；启动请求不会改写当前应用版本或显示安装成功。未提供完整性元数据或设备匹配 APK 的发布仍可打开发布页面。检查、版本和安装边界见 [应用版本与应用内更新](app.md#应用版本与应用内更新)。

关于页品牌图标为 96×96 dp，图片铺满单层 `AppShape` 圆角裁切区域，沿用整体占位与标题间距；图标外围的额外表面和内边距已移除。

- 首次启动直接进入仪表盘，没有免责声明接受门槛。Android 默认显示状态、网络速度、出站模式、配置和代理组五张卡；内存与内网 IP 可选。连接从工具页进入，不提供首页连接卡，旧布局/备份忽略该卡，下一次布局保存清理其键。
- 手机底栏依次为仪表盘、配置、工具、设置，可选固定或悬浮样式；宽屏侧栏及外观页预览使用相同顺序。固定样式由 [main.dart](flutter/lib/main.dart) 在页面下方承载 [BoxNavigationBar](flutter/lib/components/navigation_bar.dart)，保留 Prism 的目的地、选中指示和按压反馈；栏体高度为 64 dp，仅追加系统底部安全区。栏体及底部安全区使用主界面背景色，无阴影和表面叠色，随浅色、深色及纯黑主题同步。悬浮样式使用 `NavigationDock`，宽屏使用 `NavigationSidebar`；外观页预览使用相同底色和紧凑比例。
- [PageSwitcher](flutter/lib/components/page_switcher.dart) 按参考版使用 `PageView` 和保活缓存，滑动或淡入时使用页面快照，跨页切换不会绘制中间页。隐藏页不参与工具栏浮层定位，焦点、语义和原生订阅仍按活动页隔离；宽屏立即切页。
- 外观页提供主题、纯黑模式、主题色、导航栏样式、标签页动画及液态玻璃，不提供字体选择或自定义字体缩放。应用正文使用默认字体，字号随系统变化并沿用 0.8～1.4 倍的缩放范围；旧偏好的兼容处理见 [app.md](app.md#配置服务应用与备份)。
- 出站模式只显示内核提供的值。规则、全局、直连使用参考版字形，名称和选中态忽略大小写，排列和切换参数保留内核原值。系统 HTTP 代理卡片按内核能力显示，支持首页编辑中的排序、移除和重新添加；能力暂不可用时保留顺序和隐藏偏好。卡片沿用参考版 VPN 卡片的 48 像素徽章、加粗标题、启用/禁用副标题及紧凑开关；启用时徽章为主题色六瓣形，禁用时为低强调圆形。卡片和首页编辑列表共用 [system_proxy_glyph.dart](flutter/lib/components/system_proxy_glyph.dart) 中的大陆轮廓地球字形，使用共享 Glyph 的线宽与圆角；全局出站模式保留参考版经纬线球体字形。
- 服务按钮的暂停图形表示停止服务，不新增暂停核心能力。远程会话、启停过渡和不可用操作必须反映真实状态。
- 仪表盘右上角恢复 FlClash 的 `AppGlyphs.edit` 铅笔，点击进入现有卡片编辑模式，完成后返回仪表盘。设置从底栏的 [设置页](flutter/lib/pages/settings.dart) 进入，依次显示外观、服务、内核、访问控制、备份与恢复和关于。服务与内核的具体选项各自保留在二级页面，不再经过“常规”；内核页不显示电量报告开关。“关闭弃用警告”在设置快照缺少该键时显示开启，收到明确关闭值后显示关闭，持久化默认值见 [Android 设置处理](app.md#配置服务应用与备份)。
- 工具页依次显示连接与请求、日志、终端、WARP 注册、网络诊断、远程控制及按能力显示的端点；网络诊断二级页显示网络质量与 STUN 测试，终端设置仍在终端内。工具页不再展示设置、关于或崩溃/内存/电量报告入口。配置相关入口统一在配置的“编辑”页，具体排列见 [配置页与内核差异](#配置页与内核差异)。
- 工具页以“连接与请求”作为连接和抓取记录的唯一入口，入口只显示标题，页面标题与入口一致，默认显示全部网络活动。顶部可筛选全部、活动、抓取记录及已结束；活动 HTTP 请求按记录是否完成判断，已结束包括完成的请求和关闭的连接。已完成请求不会把仍存活的 keep-alive 连接标为关闭：活动筛选中没有进行中请求代表它时，仍显示连接行。远程会话展示远端连接及管理操作，隐藏抓取筛选和本机操作，说明 HTTP 调试仅支持本地。
- 未开启 raw TCP 记录的普通 TCP/UDP、未解密 HTTPS 以连接信息展示；已记录 HTTP 请求、TCP/TLS 流、WebSocket 消息及仅域名记录分别成行。同一连接的 HTTP/1.1 请求、HTTP/2/3 流、TCP 数据块和 WebSocket 消息保留独立行，以精确 `connectionId` 关联；当前筛选结果已有记录代表该连接时省略连接行，不以目标地址或旧抓包 ID 猜测关联。缺少父连接和旧版无关联记录仍可搜索、查看；HTTP 导出范围由 [核心文档](libbox.md#httphttps-请求调试) 维护。

TCP/WebSocket 详情按 `direction` 显示发送或接收正文，复用文本、Hex、Base64、JSON 和图片预览；消息不显示 HTTP 头、Cookie 或 cURL。`parentId` 可跳转握手/父流记录，`streamId` 与实时 `streamActive` 决定是否可注入，消息记录完成后仍轮询活跃流状态。“注入消息”可选择模拟客户端或服务器方向，并输入文本、Hex 或 Base64；WebSocket 可选文本/二进制。发送完成后才显示结果，等待期间可定向取消；远程会话和关闭的流不提供本机注入。脚本编辑页增加 Stream scripts 列表，使用 JSON 表单编辑匹配、方向和消息处理，禁用示例默认不运行。核心消息处理合同见 [libbox.md](libbox.md#tcpwebsocket-消息脚本与注入)，平台任务取消见 [app.md](app.md#请求抓取与-mitm)。
- 搜索覆盖 URL、方法、状态、协议、应用、路由及来源/目标等已观测字段，支持可删除关键词；默认按创建时间排序，也保留速度、上传和下载排序。请求行显示自身正文计数，连接行显示核心连接流量及速率；请求速度未知，不复制父连接速率或相加统计。核心对全部保留历史筛选和排序，初始读取 50 条摘要，“显示更多”每次扩大 50 条窗口，轮询只传回窗口内变化的摘要。Flutter 与连接快照合并后展示前 N 行，未加载的匹配请求仍参与父连接去重；全历史搜索和导出不受窗口限制。连接数量沿用当前内核快照。
- 关闭录制仍可查看内核的近期连接元数据，HTTP 报文及解密流的保存由显式录制控制，MITM 由配置单独控制。抓取开关等待原生确认，停止抓取或停服后保留请求历史。空态说明从新 HTTP 请求/TCP 流开始记录；需要重新连接的范围及停止时的记录冻结语义由 [核心录制生命周期](libbox.md#httphttps-请求调试) 维护，中英文文案同步。
- 请求详情使用同一点击复制行及反馈，长 URL、头部与 Cookie 分行显示；右上角菜单切换请求、响应和 Cookies，请求与响应 Cookie 分组展示，保留重复 `Set-Cookie`、空值、值中的等号及属性，点击 Cookie 复制该条原文。
- 抓包设置只提供记录应用、原始目标端口、正文保留方式和可见域名提取。正文可选择“保存完整正文”，或填写每方向的正整数字节限制；既有自定义值原样保留，不限定为 KiB 预设。记录保留到清空，页面不显示固定磁盘总配额。保存检查本机修订，失败保留草稿。MITM 独立提供配置及证书设置，默认关闭；域名、排除项及协议选项遵循 [MITM 配置](libbox.md#独立-mitm-配置)。证书页显示有效期、公钥 SHA-256、用户/系统信任和私钥可用性，提供 CRT/系统模块导出与 Android 设置入口；打开页面不生成证书，生成是明确操作，重新生成需确认，返回前台可刷新信任。远程会话隔离与平台边界见 [app.md](app.md#请求抓取与-mitm)。
- 请求菜单提供排序、关闭全部活动连接、抓包设置、当前筛选结果的 HAR 导出和清空抓取记录。HAR 将当前筛选条件交给原生层，在打开文件选择器前固定所有匹配 HTTP 记录的 ID，包含尚未展开的分页；这些 ID 不传回 Flutter。普通连接、仅域名或 TLS/TCP 不作为 HTTP 导出。关闭连接、停止录制和清空抓取记录分别调用各自操作，清空不关闭连接。页面只显示录制状态；运行错误和脚本 console 输出进入应用现有日志，见 [libbox.md](libbox.md#httphttps-请求调试)。
- 请求详情保留重复头部、Cookie、正文状态及 trailers，显示出站/规则、应用及来源/目标地址，字段支持复制。已有记录中保存过的 PID/socket 字段仍可读，缺失值保持未知。域名观察与握手失败标为“仅域名”，不能导出 cURL；请求正文/头部不完整、截断或含 trailers 也不能导出 cURL。TLS/TCP 详情分别显示两个方向的原始字节，不显示 HTTP 状态、头部和 Cookies，禁用 cURL；正常完成的流显示“已完成”。页面复用现有共享组件，没有修改 FlClash 直接导入文件。
- 服务设置页的 VPN 绕过、自动重定向和动态通知开关即时应用，保存和重启期间显示进度并禁用这些开关，底部没有“应用服务设置”按钮；启动确认及失败恢复见 [app.md](app.md#配置服务应用与备份)。动态通知与“忽略电池优化”之间显示“剪贴板节点导入提示”，沿用 `clipboardImport` 界面偏好，缺省开启并保留已保存的关闭值；切换只保存偏好，不重启服务。前台剪贴板提示及手动节点导入仍使用现有流程。
- [忽略电池优化项](flutter/lib/components/battery_optimization.dart)采用固定参考版 [on_demand.dart](https://github.com/chen08209/FlClash/blob/be84736605527034689c95bf27828ff27d912c5e/lib/views/config/on_demand.dart#L255) 的前置条件行：`DecorationListItem`、两行上限的 `TooltipLabel` 和右侧授权按钮，没有图标、副标题或整行点击。未授权按钮使用错误容器色及“点击授权”，已授权使用缺省主题色及“已授权”；保留 `MinFilledButtonTheme`、`ElasticButton`、12 dp 水平内边距、名义 80×40 dp 最小尺寸，以及不可见反态文字保证两种状态宽度相同。密度及文字测量由原组件处理，不将名义尺寸强制为实际尺寸。首次状态读取隐藏按钮但保留占位，在尾部右侧显示 24 dp 指示器；授权请求只防重入，继续显示原按钮，已授权点击无操作。进入页面和返回前台读取本机真实系统状态，完成、取消及失败不乐观改为已授权；Android 交互见 [应用文档](app.md#配置服务应用与备份)。该项继续位于 B-box 服务页原有分组末行，分组位置决定圆角，不引入参考版按需运行的其他选项。
- 日志页默认选择“跟随配置”，显示内核上报的日志级别及更严重的记录；菜单同时显示当前级别，仍可手动选择“全部”或单一级别，手动筛选不会被后续状态更新覆盖。搜索、暂停和导出沿用同一筛选结果；默认级别尚未收到时保留已接收记录。日志来源及本地重载同步见 [app.md](app.md#配置服务应用与备份)。
- 应用始终跟随系统语言并响应运行时变化；旧偏好的兼容处理见 [app.md](app.md#配置服务应用与备份)。
- 代理列表保留圆角吸顶分组标题、固定高度的懒加载行、折叠动效及填充/普通/隐藏分组图标。折叠状态按选中配置保存，定位节点通过行偏移实现，不要求节点预先进入视口。
- 访问控制按上游 SFA 的列表/扫描分离方式加载，进入页面直接显示应用与已保存的托管选择；“重新扫描托管应用”为独立操作，扫描期间保留列表。筛选/搜索/排序结果在列表及相关条件未变化时复用，流量等无关状态更新不重新排序。图标按可见行请求，[app_icon_cache.dart](flutter/lib/common/app_icon_cache.dart) 合并同包的在途请求，按最近使用顺序将编码数据限制在 4 MiB、256 项以内，缺失图标也计入项数。淘汰同时释放 Flutter 图像缓存中的非活动副本，系统内存压力或手动刷新清空缓存；清空前的迟到结果不会重新填入缓存。切换显示范围或无修改地关闭查询设置不清空图标；查询策略变更或申请权限后关闭设置只刷新一次。原生查询与托管更新约定见 [app.md](app.md#配置服务应用与备份)。
- 长按任意仪表盘卡片进入编辑，使用标准长按手势；卡片中的更新时间和查询错误提示改为点击显示，按钮继续通过点击执行原操作。IP 地址点击打开详情，不提供长按提示或截断地址的全文浮层。编辑期间右上角显示完成按钮。首页编辑保留卡片的全局 key，防止重复出口请求及丢失状态。改动立即保存；保存失败保留草稿，点击完成再次尝试。
- sing-box 专属 Tailscale、Taildrop、终端、认证、USB/IP 和诊断使用统一 Flutter 页面，复用原生后端；报告后端及其他能力边界见 [app.md](app.md)。

[box.dart](flutter/lib/box.dart) 按应用状态、启动/外观、代理组、流量、内存、连接、日志、工具、诊断、传输和终端分开通知；高频数据只更新对应消费者，不触发应用根部或无关列表重建。代理组按 tag 复用未变化的节点、节点列表和组；[proxy_nodes.dart](flutter/lib/common/proxy_nodes.dart) 缓存每组的搜索及排序投影，排序所依赖的名称、延迟和超时状态未变化时直接替换变化的节点引用。标签页复用未变化组的网格，列表继续复用其懒加载行。出口查询仅在组的选择映射改变时接收路由通知，延迟和顺序变化不触发；显式批量通知仍刷新全部依赖。

[main.dart](flutter/lib/main.dart) 每种亮度仅保留当前主题，共浅色、深色两份，比较完整配色对象后复用。系统语言变化和内存压力清空主题缓存，主题色反复切换不会累积旧主题对象。

[data_subscription.dart](flutter/lib/components/data_subscription.dart) 按页面活动状态、TickerMode 和应用生命周期合并内存、日志、连接、工具详情、诊断出站及内网 IP 可见性请求；页面与详情共享一个原生请求，最后一个可见消费者退出时释放。内网 IP 请求决定热点补查频率，认证及来件通知保留独立订阅，具体边界见 [Android 桥接](app.md#界面替换与-android-桥接)和[采样规则](app.md#配置服务应用与备份)。日志增量保留既有记录对象，暂停快照独立于后续追加和淘汰，恢复可见时重新同步原生缓冲。

连接增量按 ID 合并；[network_activity.dart](flutter/lib/common/network_activity.dart) 将连接和抓取 ID 放在不同的行身份空间，缓存搜索元数据、记录对象和筛选结果。集合及排序值未变化时复用顺序，计数变化仍更新行内容，相同排序值按行 ID 保持稳定。列表与统一详情共用连接可见性订阅，恢复时接收完整快照；会话切换处理见 [Android 桥接](app.md#请求抓取与-mitm)。

[capture_poll.dart](flutter/lib/components/capture_poll.dart) 仅在本地请求列表、关联记录或未完成详情活动、TickerMode 开启且应用前台时每两秒查询；调用不重叠，忽略旧会话的迟到结果，跨会话变化合并在同一帧时仍通过会话代次失效旧读取。搜索或筛选改变时立即清除旧结果，完成前的旧请求不覆盖新条件；[capture_page.dart](flutter/lib/common/capture_page.dart) 依据核心返回的窗口 ID 顺序合并增量，复用未变化对象并释放窗口外的摘要。关联列表按原始连接 ID 在核心筛选，避免扫描整份 Flutter 历史。已完成详情保留当前页面快照并停止定时查询，手动刷新或重新可见/返回前台时重新读取一次。隐藏页面停止轮询，后台录制继续随服务运行。请求列表和统一详情在共享页面内部读取间距，把滚动视口放在标题栏下方，避免滚动文字穿过标题；末尾“显示更多”按钮可滚动到固定搜索栏上方。页面为 B-box 业务代码，没有修改 FlClash 直接导入文件。

普通连接和抓取记录使用同一个详情页。普通连接显示状态、来源/目标、规则、出站、应用及流量，并列出关联抓取记录；应用行展示当前会话内核提供的包名，缺失时显示“—”，详情复制沿用同一字段。请求详情在有原始连接 ID 时显示可复制 ID 和“查看连接”，菜单可切换连接信息与请求、响应、Cookies。连接信息下的关联记录以请求自身 ID 打开详情；仅域名记录不显示 HTTP 报文菜单。连接已淘汰时保留本页最后观测信息或请求中保存的元数据，标明当前连接不可用并禁用关闭操作。请求菜单的“关闭底层连接”需明确确认会中断共享连接上的所有请求。详情和 cURL 始终使用请求自身 ID；两个 ID 的来源、关联及保留规则见 [核心请求调试](libbox.md#httphttps-请求调试)。

[captured_body.dart](flutter/lib/components/captured_body.dart) 在请求/响应详情的同一正文区域根据内容自动展示文本、完整图片、JSON 或 Protobuf/gRPC，保留十六进制和 Base64 切换，展示内容类型、字符集、保存/总字节数、截断和未完成状态。正文标题行按当前内容提供 JSON 格式化、文本自动折行/横向滚动、图片缩放重置和原页复制；“更多”菜单提供显示内容复制。图片在正文区域直接缩放。每个正文组件按请求 ID、方向和本机会话持有自己的原生读取器。Flutter 只保留当前原字节页及当前派生页，上一页/下一页按需替换它们，原生读取器在临时文件中复用解码进度；正文组件保活，滚动离开列表缓存窗口后返回仍保留当前原页和派生视图。字节页大小、字符边界及原始保存/HTTP 解码字节的语义见 [核心正文读取](libbox.md#httphttps-请求调试)。

正文状态由父详情的 `CapturePoll` 统一刷新：前台、详情页活动且 TickerMode 开启时每两秒读取未完成记录，完成后停轮询；重新可见或返回前台重新读取一次。计数、完成和截断状态在所有原字节页同步，当前末页增长时按原偏移补读，页码历史不增加。原页保留自己的快照元数据，迟到的详情首页不能覆盖新页，分页读取期间收到更新后也会补读落后的末页；发出读取请求不会把旧页误判为完整正文。写入在途造成的截断状态可在保存字节追平后清除；`hasMore` 只决定翻页，不替代保存或解码失败的状态。原字节分页失败保留当前页，完整派生视图失败保留原页并提供重试。

原始保存字节首页回退后，父详情提供健康且计数不落后的 HTTP 解码首页时，正文取消旧原生读取器及在途请求，再替换为解码首页。当前若已翻到原始保存字节的后续页，则保留该页的来源、偏移和页码历史，另从健康解码首页识别完整视图；回到原始保存字节首页时再重新绑定解码来源。完整视图从正文开头独立解码，两套分页不会混用偏移。

[body_content.dart](flutter/lib/common/body_content.dart) 基于当前页原字节判断文本/图片/二进制显示，并仅从方向的首个 HTTP 解码页提供完整视图的格式线索。文本优先采用原生读取器提供的字符集及独立 `displayText`。原生从正文开头的 BOM、Content-Type 和前 1024 字节 HTML/XML 声明识别字符集，并在后续页保留相同解码方式；受支持字符集的纯 ASCII 页也保留可读显示。没有声明时默认 UTF-8，有明确但不支持的字符集时保留字节。文本严格检查有效字符，密集控制字符或 NUL 保留为二进制；稀疏控制字符只在显示中替换。字符集范围和原始页偏移由 [核心读取](libbox.md#httphttps-请求调试) 维护。十六进制行的地址从当前原字节页的实际偏移开始，按宽度和系统字号选择列数并懒加载行；原始保存字节回退仍保留来源和解码错误。

自动识别优先检查首页的 PNG/JPEG/GIF/WebP/BMP/ICO 文件头；gRPC 声明按帧处理，Protobuf 声明进入对应协议解析，其余内容按实际可读文本判断。完整单页的合法 JSON 对象/数组自动识别为 JSON，明确声明 `application/json`、`text/json` 或 `+json` 时也支持合法标量；无效单页 JSON 保留普通文本。JSON 校验使用替换显示控制字符前的原解码文本。跨页正文依据可识别的对象/数组起始结构提供候选，JSON 声明还接受字符串、数字、关键字或空白前缀，明显 HTML/普通文本仍按实际内容显示；完整语法由原生解析验证。非 SVG 图片 MIME 的首页若实际为可读文本/JSON，使用真实文本形式，误标的长文本在后续可读页继续显示文本；不能作为文本读取时仍可作为完整图片候选。无字符集声明、使用缺省 UTF-8 的未知或 `application/octet-stream` 二进制，仅在完整首个页通过保守的字段结构校验后推测为 Protobuf；未知二进制默认显示 Hex。格式线索按正文方向保留，后续原字节页不重新猜测，派生分页及返回前台恢复沿用同一方向的识别结果。

单页快速解析保留原有预算：完整落在一页的 JSON 对象/数组可切换格式化与原文；[http_message.dart](flutter/lib/common/http_message.dart) 只调整字符串外的空白，保留数值字面量、重复键及转义，缩进输出超过 64 KiB 或内容无效时保留原文。Dart 的 Protobuf/gRPC 快速解析最多进入 16 层，节点与派生输出分别限制为 4096 和 64 KiB；结构无效或超预算时使用原字节查看。这些只限制 Dart 的单页快速显示，详情内的完整原生视图通过流式文件解析处理跨页正文和超过这些节点、输出预算的内容。正文保留范围及每方向默认值由 [核心抓取设置](libbox.md#httphttps-请求调试) 维护。

正文完整保存、非空、未截断且该方向有健康 HTTP 解码首页提供的格式线索时，详情正文区域按自动识别结果直接展示完整图片、JSON、Protobuf 或 gRPC 视图。首页只提供候选线索，完整 HTTP 解码及 JSON/Protobuf/gRPC 格式仍由原生从头验证，失败保留原页并提供重试。当前原页仍为较早的原始保存字节或错误页时，只有计数不落后的健康解码首页线索才能启动完整视图，原页仍保留供复制和字节查看。没有有效解码线索的回退或读取错误，以及空正文、未完成或截断状态保留原页和状态提示。完整落在当前页的图片可直接预览，跨页图片由同一正文组件读取完整 HTTP 解码文件；SVG 默认使用文本。图片按视口限制解码尺寸，每边最多 2048 像素并保留比例，关闭或更换内容时释放图像缓存，无效或平台不支持的图片可切回十六进制。

完整 JSON、Protobuf/gRPC 视图在原生层生成 UTF-8 临时文件，Flutter 每次只读取一个派生页，派生页以该文件的 UTF-8 字节偏移翻页，并维护独立页码历史。JSON 继续保留数值字面量、重复键及转义。Protobuf 按字段编号保留重复字段、无符号 64 位整数和固定宽度十六进制值，长度字段的文本或嵌套结构仍是无类型定义的推测；完整路径保留 16 层的嵌套推测边界，更深或不透明字段显示带长度的字节摘要，原字节页继续可读。gRPC 支持多帧、gRPC-Web 二进制/独立填充 Base64 块及 trailers：单页快速路径标记 `grpc-encoding` 并保留压缩帧；完整路径按帧声明解码受支持的消息压缩，失败或不支持时保留帧字节摘要，与外层 HTTP 压缩分别处理。gRPC 的 `+json` 声明按 JSON 验证每条解码消息，无效消息显示 Hex，外层保留帧结构，正文不显示 Protobuf 字段提示。原生完整解码、帧和文件生命周期的边界见 [核心正文读取](libbox.md#httphttps-请求调试)。

派生显示始终保留当前原字节页：“复制本页正文”复制桥接的原 `text`，文本页保留原字符，二进制页保留原 Base64；字符集显示、JSON 格式化、图片及 Protobuf/gRPC 解读都不替换它。正文操作区的“复制显示内容”复制当前文本/格式化派生页或十六进制；切回原文、Hex 或 Base64 时恢复原字节页和原页码历史。原方向的保存/总字节数不被派生文件长度覆盖，两套偏移也分别维护；整份 HTTP 导出继续使用原生流式导出。

退出详情、切换正文方向、详情被覆盖或进入后台时取消当前读取并清理读取器及临时文件；详情再次可见时保留原页并按需使用新读取器，已打开的派生格式和页码历史按需重新生成并恢复。切换本地/远程会话后立即失效旧读取，正文区域不再显示或复制旧会话内容；旧会话的迟到正文、详情或派生结果不能恢复数据。实际读取、清理及 Android 桥接边界见 [应用请求抓取](app.md#请求抓取与-mitm)。

### 配置页与内核差异

配置页顶部仅显示可横向滑动的两行卡片，每张卡片宽度为当前页面宽度减去两侧各 16 dp，与下方代理区域外边框等宽对齐；下方为当前内核的代理分组。默认文件中文显示为“配置文件”，由 Android 持续维护，即使已有其他配置也保留在列表中；默认文件不能删除或转为远程类型，可修改内容和名称。配置文件滚动和分组切换使用各自的控制器，滑动卡片不选择配置或切换底栏。卡片保留参考版固定行高和更新动画，进入页面及已选配置变化时定位当前选择；顶部当前配置的卡片与切换弹层均使用 `SelectionBadge` 对号表示选中，编辑从卡片菜单进入。切换配置的弹层仍使用最多三列的自适应网格，并从顶部打开。远程会话顶部显示远程配置摘要，下方使用远端分组，隐藏本机配置卡片及管理操作。

页面右上角依次为搜索、当前分组测速、添加和收纳菜单；配置卡片外不再显示“配置文件”标题及独立管理按钮行。卡片样式下测速按钮固定在工具栏、不参与收纳，列表样式由各分组标题测速；节点定位沿用工具栏收纳逻辑。添加按钮和空状态按钮打开同一菜单，分为三段：扫描二维码、文件和剪贴板按读到的内容区分配置与节点，“配置”段提供从 URL 导入和“新建配置”，“节点”段提供新建节点。收纳菜单首段为全部更新和配置排序，其后是样式、节点排序、布局、尺寸等显示选项；全部更新依次更新各文件的订阅来源及已有远程配置，没有可更新来源或正在更新时禁用，配置文件不足两个时禁用排序。配置排序使用分组背景、拖动手柄及拖动浮起效果。长按菜单依次提供编辑、更新（有订阅来源或远程类型）、复制链接和二维码（远程配置）、分享、导出及删除，删除标为危险操作。当前选中配置的删除项禁用，停服时也需先切换到其他配置；配置页和切换弹层均在打开确认框前及确认后复核最新选择，原生保护见 [app.md](app.md#配置服务应用与备份)。编辑页显示配置摘要，提供元数据分组表单、节点订阅管理、分项配置和原始 JSON 编辑入口。

本机服务未启动时，由 libbox 读取所选配置中的 selector/urltest 分组和手动组的已保存选择，配置页与首页分组卡使用该快照；来自其他配置的迟到快照不会显示。启动前后使用相同的标签页、列表、节点卡片、选中对号和定位入口，点按手动组节点即选择，自动组始终禁止手选。停服及启停过渡期间保留测速控件并禁用，过渡期间同时禁用节点选择；运行后数据由当前内核上报。停服选择由原生确认保存后刷新，不关闭连接或启动服务；选择持久化见 [应用说明](app.md#配置服务应用与备份)，核心读取及自动组未知状态见 [配置分组与停服选择](libbox.md#配置分组与停服选择)。所选配置中定义的节点从长按菜单编辑、多选和删除，多选时工具栏显示数量、全选、分享和删除；普通点按不进入编辑。切换配置或会话清理多选状态。没有分组时，空状态提供“添加节点”按钮。远程会话仅显示远端分组，断开时也不使用本机快照。

“扫描二维码”页底部的“从图片识别”可以选择本机截图或照片。识别出的文本和相机读数共用导入流程和动态 QRS 进度，打开首个可导入结果的预览。取消选择会留在扫码页，图片无二维码或读取失败时可换图重试。[Throne 图片导入参考](https://github.com/throneproj/Throne/blob/48273be2870fd2c81059628ff2cc439ac711808e/src/ui/mainWindow/mainwindow_deeplink.cpp#L80-L146) · [Android 桥接](app.md#界面替换与-android-桥接)

[节点导入与编辑](flutter/lib/pages/nodes.dart) 将新建节点、仅含节点的文件/二维码/剪贴板导入和前台剪贴板提示的导入结果直接追加到当前选中配置文件。预览显示目标配置名称，保存和删除同时传递打开操作时的配置 ID 与内容修订，配置切换或内容冲突由原生拒绝，失败保留预览或草稿。独立共享节点页面及节点库状态已移除。

XHTTP 与 VLESS Encryption 使用现有节点链接、订阅及 sing-box JSON 编辑入口，字段和支持范围由 [核心文档](libbox.md#xhttp-与-vless-encryption) 维护。旧核心保存的跳过报告继续按原记录显示，成功更新来源后以新解析结果替换；这类历史报告的中文翻译保留。新增核心源码的许可通过现有许可证页展示，界面参考版本不变。

“从链接导入”和剪贴板/二维码中的 HTTP(S) 订阅链接打开同一个[订阅表单](flutter/lib/pages/subscriptions.dart)，显示目标配置名称，保存来源名称、URL、自动更新及间隔；URL 导入只向当前选中配置文件添加节点和来源，不创建配置文件。表单打开前固定目标文件 ID 和内容修订，异步读取修订或下载期间切换配置不会改写目标；保存向 Android 传递固定目标、链接及元数据，下载、解析或冲突失败保留输入。表单列出 Clash、sing-box JSON、节点分享链接和 Base64 等主要来源，明确提示仅解析代理节点。每个来源拥有独立的节点归属，更新仅替换该来源的节点，保留其他来源、本地节点和配置设置。明确不支持的节点只跳过对应项，导入及手动更新的结果提示显示导入数量、跳过数量和原因；来源列表持久展示报告，自动更新后也可查看。原 URL 保留，全部不可用或非法更新仍失败，具体判断由核心维护。

配置编辑页的“节点订阅”入口列出该文件的来源、URL、节点数量、自动更新状态及更新时间，提供单独更新、编辑和移除操作。编辑来源只保存元数据，之后更新时使用新链接；移除前确认节点会作为本地节点保留。同一管理页的来源写操作串行执行，订阅表单通过 `BoxEditGuard` 保护草稿并禁止保存期间退出；来源列表按请求序号与会话版本拒绝迟到响应。详情页更新按钮在批次执行期间禁用，单文件和全部更新在切换会话后停止后续请求；页面销毁后延迟菜单回调不再发起操作。只有选中此配置时才能在管理页添加订阅，远程会话隐藏本机管理操作。新建配置保留本地 sing-box JSON 表单，已有远程配置继续使用元数据表单，二进制 `.bpf` 导入继续携带已保存的完整配置内容及订阅元数据。完整解析范围由 [核心节点解析](libbox.md#节点导入与配置合并) 维护，持久化及 DNS、路由、入站、分组等设置的更新规则由 [Android 配置处理](app.md#配置服务应用与备份) 维护。

分项配置编辑器只去除格式化内容开头的 `// b-box-local-nodes: [...]` 注释后交给 Dart JSON 解析器，不处理字符串中的 `//` 或其他注释；本地节点归属由原生保存时回填，原始配置编辑保留该注释。

服务启动中或停止中，配置列表和已打开的切换弹层禁用配置选择，首页切换配置按钮同步禁用；服务进入运行或停止状态后恢复。原生状态复核见 [app.md](app.md#配置服务应用与备份)。

| 参考版能力 | B-box 的适配边界 |
| --- | --- |
| 链接、文件、二维码导入 | 远程链接只解析节点订阅并追加到当前配置，保存多个独立来源；文件/二维码/剪贴板保留 sing-box JSON、节点分享链接、`.bpf` 及动态 QRS。Clash 订阅仅提取 `proxies` 节点，不导入完整配置 |
| 自定义配置、提供者与覆写 | “新建配置”创建本地 sing-box JSON；通过原始 JSON 及分项配置编辑器修改，不引入 Mihomo 配置生成、提供者或覆写脚本 |
| 配置预览与文件编辑 | 从编辑页的“打开配置内容”入口读取保存的 JSON，不将其称为运行时配置 |
| 自动更新 | 每个订阅来源独立保存开关和间隔，仅更新自己的代理节点，保留其他来源和当前配置的其他设置；保留 Android 后端至少 15 分钟的限制，关闭后隐藏间隔输入，无效草稿不阻止关闭，保留原有效间隔及导入元数据；批量更新包含本地及默认文件中的来源，并按原生互斥写入流程串行执行 |
| 流量用量与到期徽标 | 当前 Room/桥接没有订阅额度和到期字段，卡片显示真实本地/远程类型，不用会话流量代替额度；这是当前元数据能力的差异，不表示 sing-box 无法支持订阅信息展示 |
| 上次更新时间 | 首页和配置列表均显示 Room 时间戳，采用参考版紧凑相对时间并提供完整时间提示；首页显示“更新于 …”，点击时间显示提示，配置列表保留原交互。本地配置显示创建/内容保存时间，远程配置显示订阅更新时间。没有历史记录时显示未知，时间记录和失败恢复见 [app.md](app.md#配置服务应用与备份) |
| 分享与导出 | 复制链接得到原始订阅 URL，分享/二维码保留 sing-box 导入链接和名称，导出仍为 JSON |

已有配置的编辑保留 B-box 的本地/远程类型切换；新建配置创建本地文件，URL 通过订阅表单导入。不会因视觉对齐加入参考版的 age 密钥等未接入能力。保存、修订冲突、配置应用及失败恢复继续由既有桥接处理，见 [app.md](app.md#配置服务应用与备份)。

“工具 → WARP 注册”用 Prism 列表分组显示当前配置、节点名称与 WARP 协议，可选 MASQUE 或 WireGuard。默认 MASQUE，其独立传输选项默认 HTTP/3，可选择 HTTP/2，界面不显示回退提示。切换协议保留此前选择的 MASQUE 传输。WireGuard 隐藏 MASQUE 传输项，并提示保存到当前配置的端点。点击“注册并保存”会将 MASQUE 追加到当前配置的 `outbounds`，将 WireGuard 追加到 `endpoints`；按钮下方显示“注册将在 Cloudflare 创建设备账号，并写入当前配置文件。”，待保存重试时不显示新注册说明。Cloudflare 服务条款作为页面底部独立外链列表项，注册说明不附在条款下。注册进度、错误和保存结果使用列表状态行，节点名称为空时在输入框内提示。注册或保存期间禁用重复提交与协议修改并保护返回，远程会话及服务启停期间禁用本机操作。待保存列表显示原配置、节点名称和实际注册协议，仅 MASQUE 显示 HTTP 传输，重进页面可找回进程内的待保存项。“重试保存”复用账号，目标变化时提示先选择原配置。已保存 WireGuard 可通过“网络 → 端点”修改，MASQUE 通过“网络 → 出站”修改，两者均可使用原始 JSON 编辑器。字段及示例见 [核心文档](libbox.md#cloudflare-warp-masque)，注册与配置写入保障见 [应用说明](app.md#配置服务应用与备份)。

原始 JSON 编辑器使用 [code_editor.dart](flutter/lib/components/code_editor.dart) 显示从 1 开始的逻辑行号，空行和末尾换行保留编号；长行折行不增加行号，行号随正文滚动。编辑器只保存逻辑行起始偏移，在同一帧先布局正文，再复用 `RenderEditable` 的排版及未取整选区端点计算可见行号，输入、替换内容和调整尺寸时无需等待下一帧，也不为行号排版第二份完整文本。正文滚动及视口尺寸变化后同步选区端点，使手柄显示位置与拖动计算使用同一组正文坐标，避免按旧位置选中其他行；搜索、格式化、撤销及修订校验沿用原编辑流程。

[JsonEditorController](flutter/lib/pages/profile_editor.dart) 只缓存当前内容、样式和配色的高亮，纯选区变化复用结果；输入法组合输入及超过既有 150000 字符阈值时使用普通文本并释放高亮缓存。原始编辑器和分项 JSON 表单不因选区变化重建整页。`ProfileConfigStore.set` 只复制修改路径上的 Map，序列化读取其他分支；失败时原配置和修订保持不变。

选区同步补偿 [固定 Flutter SDK](.github/actions/setup-flutter/action.yml) 的 Android 滚动处理，通过已有的 `EditableTextState.selectionOverlay` 取得选择覆盖层并刷新端点。该 getter 标为测试可见，调用处仅对它豁免可见性分析；SDK 升级时需核对这个入口及滚动后的手柄回归。

分项配置的 JSON 表单使用固定 16 dp 圆角、正文顶部对齐的多行编辑框。[BoxField](flutter/lib/widgets.dart) 将圆角传给 Prism 的填充输入主题，使背景及聚焦边框使用同一轮廓，避免多行编辑区域随高度变成胶囊形。

配置“编辑”页依次直接提供“日志”、“网络”、“DNS”、“路由规则”、“规则集”、“MITM”、“HTTP 改写”和“脚本”，末尾单独保留一个“打开配置内容”入口，元数据表单仅保留元数据字段和更新时间。各分项打开时读取该配置的最新内容，由 [configuration.dart](flutter/lib/pages/configuration.dart) 复用共享分组、规则列表、排序及 JSON 表单；原始 JSON 入口独立于分项加载。内网详情的“编辑”入口复用同一配置编辑页。

MITM 页面首先提供单一证书入口，再显示 HTTPS、域名、HTTP/2、HTTP/3、服务器证书校验、“解密TCP”和 QUIC 回退设置；证书入口独立于配置加载。页面顶部不显示配置名或解密说明，TCP 和 QUIC 开关仅显示标题。域名入口只显示标题，完整域名及排除项在二级编辑器查看和修改，保留输入顺序及通配符、端口后缀，新增抓取域名追加在列表末尾。端口、应用等额外筛选通过同页 JSON 编辑入口修改，见 [MITM 配置](libbox.md#独立-mitm-配置)。HTTP 改写页面依次显示 URL Rewrite、Header Rewrite、Body Rewrite（含 jq）、Map Local / API Mock、Map Remote，下方提供资源及完整 JSON 编辑入口；脚本独立编辑启用状态、HTTP、Stream、Cron、事件、Generic 和资源。改写规则和 HTTP 脚本的 JSON 表单提供复合匹配字段说明及可选中的示例，条件由原生完整配置校验，不在界面重复实现语义校验。脚本及改写资源页提供“导入本地资源”、已有资源的 JSON 编辑及整个资源字典编辑；导入期间禁用重复提交，取消保留原配置，成功后显示可供规则引用的资源名，失败显示原因。文件位置、修订检查和备份边界见 [Android 导入流程](app.md#配置处理生命周期)。复合匹配、前后查找/反向引用、URL 资源及独立正文限额通过 JSON 表单配置，字段及运行行为见 [libbox.md](libbox.md#json-配置中的-http-改写)。新建规则/任务示例默认禁用。

字段修改通过 ProfileConfigStore.set，本地资源导入通过独立桥接；二者均保留其他字段、修订及原生校验/失败恢复；关闭失败表单前保留草稿。CA 不写入配置，活跃 MITM 或手动记录时禁用重新生成，独立脚本/改写不占用证书。映射请求详情继续把连接出站/规则标为原连接。

Cron、事件和 Generic 的已保存任务菜单提供“立即运行”，事件表单可选择网络变化、引擎启动及配置重载。运行要求当前配置已在本地服务中成功生效、脚本及任务已启用，编辑/保存/加载或原生应用配置期间禁用；HTTP 规则仍由实际请求触发。任务按原数组下标及修订发送，允许同名和未命名条目，运行中显示进度并提供“取消脚本运行”，等待该次请求退出后才允许重试。结果通过共享 Prism 弹层显示可选中、可复制的任务名、错误、结果和 console 文本，字符串及 panel 的 title/content 可读显示，空值显示“无输出”，长文本置于有界滚动区。

页面退出、进入编辑表单、配置/服务/会话变化时按本次 requestId 取消，并用累计状态代次丢弃迟到结果；相同 JSON 重载也依赖原生处理代次区分。旧菜单保留所显示任务的修订及请求身份，不能运行重排后的同下标任务或取消下一次运行。已显示的结果在上下文失效后关闭自己的弹层，不关闭其上方的编辑器；远程会话不显示本机任务运行或调试输出。字段及上下文由 [核心脚本文档](libbox.md#事件与手动调试) 维护，取消和实际运行配置的复核见 [Android 生命周期](app.md#配置处理生命周期)。

#### 节点分享

配置页的卡片和列表共用节点菜单。长按本机配置中的节点，选“分享”可查看二维码、复制链接或打开 Android 系统分享。长按选“多选”后，可从工具栏分享已选节点，多选使用文本分享。部分节点只能用 sing-box JSON 表达时，默认以 JSON 分享全部所选节点，也可切换查看可用标准链接与数量。单个节点超过二维码容量时，仍可复制或分享文本。切换会话、配置或更新其内容后，请重新打开分享。核心格式及范围见 [libbox 节点分享](libbox.md#节点分享)。交互参考 [Throne 的链接复制和二维码入口](https://github.com/throneproj/Throne/blob/48273be2870fd2c81059628ff2cc439ac711808e/src/ui/mainWindow/mainwindow_profiles.cpp#L198-L359)。

[nodes.dart](flutter/lib/pages/nodes.dart) 保留现有格式切换，标准链接与 JSON 均以单块 Prism 圆角主题卡片展示，使用 16 dp 内边距、JetBrainsMono 字体和 1.5 倍行高，可选中正文并自然折行。JSON 缓存 [formatCapturedJson](flutter/lib/common/http_message.dart) 生成的缩进预览，仅调整字符串外空白；无法格式化时显示原文。顶栏复制、系统分享和二维码保留核心原始内容，多选标准链接按换行拼接。长内容在工具栏下方滚动。

单选二维码、多选换行文本及配置修订变化后的预览失效已有 [Flutter 回归覆盖](flutter/test/profiles_test.dart)。

### 状态卡片与出口查询

查询来源、字段解析及质量判定参考上述固定版本。IP 竞速由 [ui_egress.go](libbox/ui_egress.go) 负责：Cloudflare、IP.SB、IPinfo、ipwho.is、GeoJS、country.is、IPQuery、ident.me 共八源，B-box 的完整解析与失败回退、限额及传输约束见 [libbox.md](libbox.md#出口-ip-和质量查询传输)。

质量查询在 [ip_quality.dart](flutter/lib/common/ip_quality.dart) 中依次分批使用 ident.me/ip-api.com/IPQuery、IPLocate、ipapi.is、proxycheck.io；等待一秒或当前请求全部失败后进入后续批次。默认不增加单请求期限，调用方可显式指定，用户取消和会话失效继续关闭在途请求。保留参考版组织/ASN 解析、显式类型优先、住宅类型推断及 Tor/abuser 质量规则，模型见 [ip_quality_models.dart](flutter/lib/common/ip_quality_models.dart)。

[ip_quality_cache.dart](flutter/lib/common/ip_quality_cache.dart) 将成功质量结果按 IP 持久缓存三天，失败仅在内存冷却五分钟，手动重试可跳过失败缓存。先显示 IP，再补充质量；质量失败保留 IP，详情展示来源和错误。

刷新与会话约定：

- 停止的本地服务通过设备当前网络查询，即使没有配置也可查询和手动重试；运行中通过 sing-box 路由查询。启停时取消旧探测，等待稳定服务状态再查询。远程会话保留占位，不展示本机出口。
- 返回前台保留未变路由的成功 IP 和质量；IP 查询失败才自动重试，间隔至少五秒，手动刷新可跳过。质量缓存到期只刷新质量。
- 短暂失焦不取消请求；已经发出的请求允许在后台完成，新查询等待前台。UI 在后台断开连接时保留本地分组快照，用于返回时比较。
- 启停、运行中的配置/模式/节点及网络变化取消旧请求、忽略迟到结果，并合并 300 毫秒内的变化后查询新出口，不受旧路由冷却限制。分组排序、延迟更新和停服时选择配置不触发无关的出口失效。

### 内网 IP 与备份展示

内网 IP 卡片复用参考版共享开关、端口副标题、Wi-Fi 徽章及复制操作。复制按钮沿用 `DashboardIconButton` 的 32 像素正方形点击区、18 像素图标、低强调前景色及向右 8 像素偏移，不受标题栏高度压缩。共享开关修改选中配置的 mixed/http/socks listen；无代理入站时先显示端口表单，明确新增 mixed 入站。运行时复制已成功启动配置的端点，停服/远程会话复制设备 IP，详情保留多入站/多端口。配置变换和保存约束分别见 [libbox.md](libbox.md) 与 [app.md](app.md)。

备份页沿用参考版的本地/远程布局、文件名、范围和兼容/覆盖策略，实际格式是 B-box `bbox-1`。WebDAV 地址、凭据和文件名不额外限制字符数，保留地址协议、端口及文件名路径校验。覆盖会在页面确认后移除备份中不存在的配置；取消选择、写入失败或上传未确认时不显示成功。事务、文件恢复和可取消传输由 Android/libbox 实现，见 [app.md](app.md)。

## 验证与升级参考版本

本节依次维护测试入口、测试文件的覆盖范围、当前源码的界面验证结果、历史运行索引和升级步骤。测试的存在不是通过声明；这些测试只覆盖声明的页面和情景，不等于全应用逐像素一致或已经完成实机验收。核心、JVM 与 Android 的结果和未覆盖范围见 [app.md](app.md#验证与历史记录)。

### 测试入口与产物

[newui.yml](.github/workflows/newui.yml) 在各分支代码推送时自动运行，统一包含核心、Flutter、JVM 和 Android smoke；[setup-flutter](.github/actions/setup-flutter/action.yml) 固定 SDK 和依赖锁文件。Flutter job 先运行 [verify_newui.py](tools/verify_newui.py) 核验 FlClash/prism_ui 文件哈希、许可、适配目标和退役依赖等源码约束，再依次执行 `flutter analyze --no-fatal-infos`、`flutter test --reporter expanded test packages/prism_ui/test` 和 `flutter build aar --no-profile --target-platform android-arm64`。上游 Prism 测试与 B-box 测试共用应用锁文件，不另行解析组件的开发依赖。截图和像素比较由同一测试入口生成，截图及失败对比图上传为 `newui-validation`，验证用 AAR 上传为 `flutter-aar-arm64`。手动 Release 构建独立准备所需的 Flutter Release AAR，不依赖测试运行，见 [构建与发布](app.md#构建与发布)。

### 测试文件覆盖

[intent_lifecycle_test.dart](flutter/test/intent_lifecycle_test.dart) 检查导入弹层关闭/保存后的完成确认，以及解码或修订查询中断时保留待恢复 Intent；[state_updates_test.dart](flutter/test/state_updates_test.dart) 检查后端重连后重新取得快照、恢复可见订阅并拒绝迟到状态。

下列测试使用桥接替身和测试数据，验证交互、通知、调用和几何断言。它们不代替实际 TLS、系统证书安装、第三方应用或实机视觉验收；实际 PSS/RSS、CPU、帧耗时和加载耗时仍需在同设备、同配置下测量。原生服务重启、配置重载和内核日志级别由 [Android 仪器测试](app.md#android-仪器测试) 覆盖。

[Prism 上游测试](flutter/packages/prism_ui/test) 原样固定到上述组件提交，包含 32 个 widget 测试文件、两个公共测量/字体测试及配置和辅助文件；覆盖长尾值与双向布局、折叠分组、可选尾部锚定、反向列表、弹层和按压反馈等组件行为。[logs_test.dart](flutter/test/logs_test.dart) 另覆盖 B-box 日志流的动态行高、跟随、滚动暂停和恢复，使用实际滚动位置与可见记录断言。 导航测试另在 320 宽、1.4 倍字号下实际选择长出口标签，核对标题、选项值与选择图标的边界，并确认诊断启动仍携带完整标签；运行截图写入 `build/screenshots/navigation/diagnostics-long-outbound-320-light-1.4.png`。

**导航、外观与关于**

- [navigation_test.dart](flutter/test/navigation_test.dart)：固定导航在 320 宽、底部安全区、浅色/深色/纯黑及大字号下的高度、底色、页面边界、四个目的地顺序、标签切换和选中语义，并输出固定栏截图。窄屏实际横向滑动配置文件并核对底栏及选择不变，输出合并页的滑动前后截图。设置从底栏进入，二级页面返回设置；宽屏通过侧栏实际按钮切页，并在截图前核对当前页。外观与 MITM 页直接检查工具栏操作区域像素，防止隐藏首页的按钮残留；复制反馈和保存失败回归另检查顶部按钮仍可点击。另输出配置编辑与排序弹层在窄屏、大字号和宽屏下的截图，这些是布局回归产物，配置页尚未声明与上游逐像素一致。
- [appearance_test.dart](flutter/test/appearance_test.dart)：主题选择与重开页面后的偏好，旧字体偏好不覆盖默认字体，系统字号运行时变化和既有缩放上下限。导航、状态、内网 IP、出站模式及请求页面的字号场景通过系统缩放模拟，不依赖已退役的应用偏好。
- [licenses_test.dart](flutter/test/licenses_test.dart)：异步加载、共享许可按包分组、搜索及完整可选中正文。
- [about_test.dart](flutter/test/about_test.dart)：应用与核心版本分别展示，版本检查完整/未完成/无元数据、等待时禁止重复请求、下载条件及发布页面入口；应用内进度、取消/重新下载、未知来源授权返回后的明确安装点击、保留 APK 的安装错误重试，以及中英文窄屏/大字体布局。
- [warp_test.dart](flutter/test/warp_test.dart)：工具入口顺序、HTTP/2 与 WireGuard 注册保存、默认 HTTP/3 和协议切换时的传输保留、重复提交/返回保护与取消、读取修订时目标/会话变化、MASQUE / WireGuard 待保存协议重进与原配置重试、放弃确认，以及远程/启停/无配置禁用和中文 320 宽/1.4 倍字号下两种协议、按钮说明及底部条款的几何布局。导航测试另生成 WARP 页布局截图；桥接替身不验证真实注册网络及内核节点写入。
- [dashboard_editor_test.dart](flutter/test/dashboard_editor_test.dart)、[outbound_mode_test.dart](flutter/test/outbound_mode_test.dart) 和 [terminal_settings_test.dart](flutter/test/terminal_settings_test.dart)：铅笔点击及卡片长按进入首页原位编辑，保留卡片状态并检查排序、移除、添加及保存失败；出站模式图形与 Android 一致并保留内核顺序和当前选择；停服时从终端页打开 Ghostty 设置并保存原生配置。

**配置、编辑与备份**

扫码页回归模拟相机权限被拒绝、取消选图、图片无二维码和选图成功，验证订阅预览接收识别结果并等待用户确认。测试使用识别结果替身。[profiles_test.dart](flutter/test/profiles_test.dart)

- [profiles_test.dart](flutter/test/profiles_test.dart)：新建与二进制导入的内容/元数据、URL 节点导入当前选中文件而不创建配置、失败后保留输入重试、读取修订期间切换配置及冲突后的原目标保留、来源单独编辑/更新/移除、原生元数据事件刷新来源列表及远程会话隔离；覆盖迟到列表响应、来源写操作串行化、退出管理页后的迟到更新、详情页连续更新点击、会话切换后停止单文件/全部更新、草稿取消不写入及保存期间禁止退出。添加菜单与取消导入、菜单能力、原始订阅链接、相对时间边界、本地时间未知占位、自动更新校验和选中配置定位继续覆盖。横向滑动不切页或选择配置，卡片与列表两种代理样式仍能测速并通过原生确认切换配置，卡片样式从工具栏按钮测速。停服时的“添加节点”空状态、当前配置的对号选中态、所选配置分组、迟到快照隔离和菜单节点编辑，以及收纳菜单的全部更新同时处理本地/默认文件来源和已有远程配置均有覆盖。新增节点和仅节点导入携带目标配置修订，冲突保留预览，删除确认期间切换配置保留原目标；默认文件在已有配置旁显示、不可删除或转远端，以及手机/大字号/宽屏下配置卡片边框与代理区域对齐均有用例。列表及弹层在启停期间禁止切换与状态稳定后恢复、切换失败、通过右上角收纳菜单进入配置排序后的冲突恢复，以及远程分组选择及本机配置隔离继续覆盖。
- [configuration_test.dart](flutter/test/configuration_test.dart)：MITM、HTTP 改写、脚本三种配置及 Cron 草稿通过独立路径保存，HTTP/2、HTTP/3、证书校验及 QUIC 的显式选项正确保存，关闭改写保留 MITM/脚本，校验失败保留内容，原始编辑按修订写入内容且不改动配置元数据。HTTP 改写和 HTTP 脚本经实际规则表单提交复合条件，原生校验拒绝后保留草稿、原配置及修订，修正后保存嵌套 all/any 和原有其他扩展；桥接替身只验证编辑及传输流程。带本地节点注释的格式化内容可加载和保存后回读，原始编辑保留注释，JSON 字符串中的 URL/注释样式文字及其他注释不被统一剥除。
- [script_configuration_test.dart](flutter/test/script_configuration_test.dart)：事件/Generic/Stream 草稿和触发器编辑，校验失败后保留草稿；Cron/event/generic 手动运行、结果/错误/日志与复制、同名/未命名原下标和独立并行、取消期间保留忙状态、旧菜单及已显示弹层失效。另覆盖配置/服务/会话/可见性变化后的取消与迟到结果隔离，以及中英文窄屏/大字号下输出滚动、固定操作和事件编辑器布局；使用桥接替身。两项 Linux 字体用例将中文 320 宽、1.8 倍字号的输出滚动前后、脚本入口、事件编辑器、三个触发器菜单及 JSON 截图写入 `build/screenshots/scripts/`，字体来源及测试边界见 [测试字体说明](flutter/test/support/fonts/README.md)。
- [code_editor_test.dart](flutter/test/code_editor_test.dart)：加载应用实际的等宽字体，对照 `RenderEditable` 检查行号在折行、宽度/字号变化及滚动后的对齐，覆盖空行、末尾换行和替换内容。选区用例在 0.8、1.0、1.4 倍字号下实际拖动两端手柄，检查跨行选择、折行文档滚动后的定位，以及通过复制菜单取得的原文范围。
- [backup_test.dart](flutter/test/backup_test.dart)：取消本地选择器不报告成功也不恢复数据，导出使用紧凑 JSON；恢复传递范围与策略，失败后可重试；WebDAV 上传等待服务器确认且不打开本地选择器；WebDAV 表单校验目录 URL，保存失败和取消关闭后保留长输入。

**代理、状态与出口查询**

- [proxies_test.dart](flutter/test/proxies_test.dart)、[proxy_nodes_test.dart](flutter/test/proxy_nodes_test.dart) 与 [profiles_test.dart](flutter/test/profiles_test.dart)：自动组禁止手选、延迟测试目标、分组切换、列表折叠及视口外节点定位；停服手动选择在卡片/列表两种样式下携带配置 ID 与修订，失败保留选中项，确认后刷新，启停前后保留分组与节点布局；停服自动组、启停过渡及断开的远程会话禁止选择，远程会话不使用本机快照；节点筛选复用未变化的结果，超时、改名、相同延迟重排和移除使排序结果失效。
- [status_panel_test.dart](flutter/test/status_panel_test.dart)：状态卡片与出口查询的刷新和会话约定，并将启动按钮的独立无障碍点击入口与配置可用性、实际桥接调用一起验证。
- [status_panel_parity_test.dart](flutter/test/status_panel_parity_test.dart)：与冻结的 [flclash_status_reference.dart](flutter/test/support/flclash_status_reference.dart) 比较像素，覆盖中英文、深浅主题、0.8/1.0/1.4 字号比例、IPv6、查询等待/失败、组织/ASN 回退及质量等级；停止按钮另行比较，停服状态包含无配置首次启动和设备网络查询的成功/等待/失败情况。参考渲染器独立保留原版标题、chips、徽章、形变、卡片表面和排版，不导入 B-box 的适配仪表盘组件；比较使用相同视口、字号、语言、主题及 Roboto/Noto Sans SC 字体，按卡片自然高度截图并断言高度，双方图像上传为 CI Artifact。
- [ip_quality_test.dart](flutter/test/ip_quality_test.dart) 与 [ip_quality_cache_test.dart](flutter/test/ip_quality_cache_test.dart)：各来源的类型和标志解析、查询目标地址、不增加固定期限、用户取消不启动后续批次、首个声明类型的来源胜出并取消其余、前序失败或静默时进入后续批次、全部应答后回退推断类型及失败原因；成功结果的内存共享与按 IP 持久化、三天到期边界、无效记录忽略、失败五分钟内存冷却且不持久化、手动重试和刷新跳过缓存、缓存读写失败不丢弃成功结果、取消不返回旧数据也不产生冷却，以及同一 IP 的并发查询共享结果。
- [intranet_panel_test.dart](flutter/test/intranet_panel_test.dart)：大字号下复制按钮的尺寸与位置，以及运行/停服/远程会话的复制内容。

**服务设置与日志**

- [service_settings_test.dart](flutter/test/service_settings_test.dart) 与 [box_test.dart](flutter/test/box_test.dart)：三个服务开关的即时应用、等待原生确认期间禁用开关及移除手动应用按钮；重复编辑、停止/远程会话、启停过渡及失败后的原生设置恢复。
- [battery_optimization_test.dart](flutter/test/battery_optimization_test.dart)：授权按钮独立点击与语义、初始加载占位、取消/失败后的重试、确认授权及固定宽度、已授权无操作、重复请求、设置事件与后端重连的迟到结果保护、授权等待中较早快照后的真实状态回读，以及返回前台刷新。与冻结的 [flclash_battery_reference.dart](flutter/test/support/flclash_battery_reference.dart) 在中英文、320/360/412 dp、0.8/1.0/1.4 字号比例、浅色/深色/纯黑及未授权/已授权/首次加载/请求等待的 48 个组合和两种原版分组首行情景比较完整 RGBA，允许差异像素数为零；参考独立保留原 FlClash 子模块的行渲染器、装饰及分隔线，双方 PNG 与差异计数生成在 `battery-optimization/`。两项完整服务页测试另生成中文浅色及窄屏大字号深色截图。中文采用有许可的 [测试字体](flutter/test/support/fonts/README.md)，加载器保留原实现并固定双方相同的动画输入；桥接替身及受控字体不证明系统授权页或 ARM64 实机视觉一致。
- [logs_test.dart](flutter/test/logs_test.dart)：本地/远程日志状态的默认阈值、保留更严重的记录、手动筛选及恢复跟随配置，以及暂停、搜索、导出和清空。

**数据订阅、刷新与缓存**

- [state_updates_test.dart](flutter/test/state_updates_test.dart)：高频数据只重建对应订阅者、服务状态仍通知、应用根部不随遥测/工具进度变化重建且外观设置继续生效，并覆盖日志增量淘汰、暂停快照、连接增量对象复用/重置、启动响应不覆盖早到事件，以及更新状态的独立通知和迟到的命令响应不覆盖进度事件。
- [data_subscription_test.dart](flutter/test/data_subscription_test.dart)：各类数据的卡片/详情共享订阅，以及隐藏页面、TickerMode、前后台和销毁时的订阅切换。
- [connection_list_test.dart](flutter/test/connection_list_test.dart) 与 [network_activity_test.dart](flutter/test/network_activity_test.dart)：搜索、过滤、排序、增量复用、一对多关联、无关联/淘汰历史、分页前去重及连接与请求计量隔离。
- [connections_test.dart](flutter/test/connections_test.dart)、[capture_poll_test.dart](flutter/test/capture_poll_test.dart) 和 [capture_page_test.dart](flutter/test/capture_page_test.dart)：统一请求页关闭后释放连接订阅并停止抓取轮询；完成详情停轮询、活跃 TCP/WebSocket 消息继续轮询、手动刷新、前台恢复和同帧会话切换；查询增量复用已保留的摘要并释放窗口外记录，畸形增量页被拒绝且不产生空历史。
- [app_icon_cache_test.dart](flutter/test/app_icon_cache_test.dart)：图标共享在途加载并按最近最少使用淘汰，未命中和超大图标不使缓存无界增长，加载期间清空不会重新填充或覆盖新条目。
- [access_test.dart](flutter/test/access_test.dart)：进入托管页面不扫描、加载期间接收最新托管选择、显示范围与隐藏选择保留、保存失败后重试、图标复用、扫描等待/成功/失败及查询设置返回后的刷新。

**请求与 MITM**

- [requests_test.dart](flutter/test/requests_test.dart)：统一工具入口、混合列表和活动筛选、关联详情、底层连接关闭确认、历史保留、同名远端的会话隔离、HAR 选中 ID、本机设置修订、旧记录设置清理、无改写横幅/抓包日志入口、端口、启动确认及后台轮询/迟到结果。MITM 域名覆盖空域名、有序域名/排除项选择；证书测试检查无配置时从抓包设置经 MITM 页进入证书、明确生成、取消导出、活跃 MITM 禁止重生成、远程切换和信任刷新。其他回归包括停止抓取时的历史分页与搜索重置、TCP/WebSocket 详情、方向正文、父记录跳转、活跃流 Text/Hex/Base64 注入及定向取消、失败草稿保留、流完成/导出限制、raw TCP 设置、既有 socket 元数据、报文菜单、字段/Cookie 复制、正文格式及 Unicode 分页、完整正文/任意字节限制和超过旧条数范围的搜索导出；历史列表检查“显示更多”可实际点击，正文检查原生翻页、页内复制、失败重试及会话切换。资源页另检查导入、取消、修订失败和远程会话禁用。详情的滚动查找限定在当前详情页内，目标尚未由列表构建时先滚动，不对空查找调用 `.last`，以保留关联请求、连接过期及滚动后标题边界的实际断言。
- 滚动边界：配置页面和共享表单将滚动视口放在共享标题下方，测试核对配置页 320 宽、1.3 倍字号深色主题，以及抓包设置 360 宽浅色/1.4 倍深色、800 宽浅色的滚动前后几何位置。窄屏、大字号深色及宽屏测试运行后将截图写入 `build/screenshots/requests/`，包含独立配置（如 `independent-mitm-config.png`、`independent-scripts-cron.png`）、请求设置、证书、统一列表、关联记录、分页按钮及报文详情。
- [注入布局用例](flutter/test/requests_test.dart) 使用英文与桥接替身，覆盖 TCP/WebSocket 在 320×568、1.8 倍系统字号深色和 800×1000、1.0 倍浅色下的选项触达、编辑及固定发送/取消动作，并核对组件实际字号遵循 1.4 上限；同一入口生成四张方向消息详情截图及十二张注入控件、草稿和进行中截图，保存于 `build/screenshots/requests/`。
- [http_message_test.dart](flutter/test/http_message_test.dart)：JSON 数值/重复键/转义保真、无效内容回退、正文页缩进输出过大时回退原文，以及重复 Cookie、空值、等号和 Expires 逗号。请求正文不完整或截断时不提供 cURL 导出。
- [body_content_test.dart](flutter/test/body_content_test.dart)：首页图片签名、无/误标 MIME、合法 JSON 对象/数组与声明标量、跨页候选，以及错误页/非首页不提供格式线索；文本控制字符与无效 UTF-8、截断尾字符、声明字符集/BOM、原生跨页 `displayText` 与原字节分离、非 UTF-8 字符集纯 ASCII 页、原生默认 UTF-8 下的无类型 Protobuf 推测及最新方向完成元数据；另检查图片完整性、十六进制绝对偏移、Protobuf 无符号整数与重复字段、单页无效或超预算结构、gRPC 多帧/压缩标记及 gRPC-Web 二进制和独立填充 Base64 块。
- [captured_body_test.dart](flutter/test/captured_body_test.dart)：详情内完整正文查看、自动内容识别、显示方式与原文/派生复制、原页历史和字节来源、二进制行懒加载、图片缩放/失败回退、文本横向滚动与折行；活动正文的末页追加、读取器复用、完成停轮询、迟到详情/分页以及写入追平后清除截断。完整跨页图片检查文件读取和解码尺寸；完整 JSON/Protobuf/gRPC 检查派生 UTF-8 页、独立偏移/页码、原计数和原页复制不变，以及首页识别在分页中保持、实际内容与 MIME 不一致时的显示和未知二进制回退。取消用例覆盖后台/返回前台的派生页恢复、覆盖页面、销毁、直接远程切换和同帧远程往返，确认关闭发生在迟到结果前且恢复使用新读取器；失败保留原页并可重试。窄屏大字号布局和图片交互截图由统一测试工作流生成，使用保存正文的桥接替身，不作为实机验收。

### 当前验证记录

2026-11-23 详情页原位完整正文显示、自动内容识别及正文恢复的功能/测试源码 `267d692eae2c4f81755146fcf227ba91ed6a15b9` 已通过 [本次统一测试运行](https://github.com/giturass/B-box/actions/runs/45174354097)中的 [Flutter 作业](https://github.com/giturass/B-box/actions/runs/45174354097/job/137691694530)：分析为 154 项 info、0 warning/error，920 项测试全部成功；日志确认 ARM64 Debug/Release Flutter AAR 均构建成功。[AAR 产物](https://github.com/giturass/B-box/actions/runs/45174354097/artifacts/14818029846) 与 [截图产物](https://github.com/giturass/B-box/actions/runs/45174354097/artifacts/14819515840) 的 `workflow_run.head_sha` 均已核对与该源码一致；本机只下载截图产物。

实际逐张查看本次 `build/screenshots/body/` 的九张截图：320×568 浅色的 `320-1.8-light-hex.png`、`320-1.8-light-hex-end.png`、`320-1.8-light-base64.png`，360×568 深色的 `360-1.4-dark-hex.png`、`360-1.4-dark-hex-end.png`、`360-1.4-dark-base64.png`，以及 360×800 的 `360-light-image.png`、`360-light-image-zoomed.png`、`360-light-large-image.png`。320 的系统字号输入为 1.8，组件实际缩放上限为 1.4；360 的系统及组件字号均为 1.4，实际缩放值由同轮布局断言核对。

两组窄屏的正文标题、复制/更多和 Hex/Base64 切换均可见，Hex 分别使用每行 2/4 字节，地址、字节和 ASCII 列可读，Base64 自然折行，未见横向溢出或工具栏遮挡。两张 Hex 尾部截图仅显示当前内部列表与外层页面的交叠视口，末字节 `ff` 由通过的断言验证。有效 PNG、缩放后的图片和 72.3 KB 跨页大图都在详情内显示，重置/复制/更多可见；大图截图仅覆盖当前视口。完整文件读取、原字节/派生分页、自动内容识别、仅保留“复制显示内容”的更多菜单及后台恢复由同轮测试断言覆盖，这九张图未另拍 JSON/Protobuf/gRPC 派生文本视图。

另实际查看 `build/screenshots/requests/` 中的 `requests-pagination.png`、`body-first-page.png`、`body-last-page.png`，以及 `360-1.0-light`、`360-1.4-dark`、`800-1.0-light` 三组的 `details`、`body` 和 `message-menu`，共十二张截图。列表“Show more”位于底部搜索条上方，正文前/后页按钮、页码及复制工具栏可见；详情和报文菜单位于页面或右侧详情弹层内，窄屏标题按既有省略规则显示，未见菜单行越出视口。`body-last-page.png` 中的 emoji 在 Linux 测试字体下呈缺字方框，Unicode 字符串和 UTF-8 分页保真由通过的断言验证，Android 字体的实际字形仍需实机核对。

本次截图采用英文、SDK Roboto、JetBrainsMono 与 Android 桥接替身，未覆盖中文正文界面、全部图片格式、真实远程数据或 ARM64 实机视觉，也没有上游逐像素一致的验收结论。核心、JVM 与 Android smoke 的结果及边界由 [应用验证记录](app.md#当前验证记录) 维护。本机仅执行静态检查并读取本次 Actions 日志、元数据和截图；后续验证记录提交仅更新文档。

以下记录均仅适用于各自列出的此前源码 SHA；其中独立查看页的截图保留当时实现的历史身份，原位布局与自动内容识别的当前结论以上述 `267d692` 记录为准。

2026-11-23 大正文完整文件读取、字符集分页、活动尾页刷新及派生视图取消恢复的功能/测试源码 `60c56dc182e1a655928aec6c7d3934a1eff34462` 已通过 [Flutter 作业](https://github.com/giturass/B-box/actions/runs/45141151663/job/137579236128)：分析为 153 项 info、0 warning/error，880 项测试全部成功；ARM64 Debug/Release Flutter AAR 均构建成功。[AAR 产物](https://github.com/giturass/B-box/actions/runs/45141151663/artifacts/14801733797) 与 [截图产物](https://github.com/giturass/B-box/actions/runs/45141151663/artifacts/14802910862) 的 `workflow_run.head_sha` 均已核对与该源码一致。后台恢复用例在异步操作完成后校验原/新读取器及相同派生页偏移，正文、页码历史、会话及取消断言全部保留。

实际逐张查看本轮 `build/screenshots/body/` 的九张截图：320×568 浅色的 `320-1.8-light-hex.png`、`320-1.8-light-hex-end.png`、`320-1.8-light-base64.png`，360×568 深色的 `360-1.4-dark-hex.png`、`360-1.4-dark-hex-end.png`、`360-1.4-dark-base64.png`，以及 360×800 的 `360-light-image-inline.png`、`360-light-image-viewer.png`、`360-light-large-image-viewer.png`。Hex 在两组宽度下分别使用每行 2/4 字节，地址、字节和 ASCII 列可读，Base64 正常换行，未见横向溢出；尾页图仅代表已滚至当前内部列表末端的视口。

两张蓝图展示完整 32×24 PNG；大图展示保存/总量均为 72.3 KB、超过 64 KiB 的完整 128×192 PNG，全宽、全高按比例可见，无解码错误。大图用例的原页只含前 16 KiB，查看页按完整文件路径使用 `FileImage`/`ResizeImage`；解码成功、尺寸限制、原页复制、切回 Hex 后再显示图片的断言均通过。完整 JSON/Protobuf/gRPC 的原字节与派生分页、字符集、活动末页和取消行为由同轮自动断言覆盖，这九张图未另拍派生文本视图。

截图采用英文、SDK Roboto、JetBrainsMono 与 Android 桥接替身；实际 Android 绑定、模拟器覆盖和未覆盖的 ARM64 实机行为由 [应用验证记录](app.md#当前验证记录) 维护。本机仅执行源码/来源约束、`git diff --check` 和文档本地链接检查，并读取本轮 Actions 日志及产物。后续验证记录提交仅更新文档，以下既有记录只适用于各自列出的此前源码 SHA。

2026-11-23 正文查看的功能及测试源码 `14639a98899f14e4e4613311558e0c66ff004efa` 已通过 [Flutter 作业](https://github.com/giturass/B-box/actions/runs/45059030530/job/137320599646)：分析为 139 项 info、0 warning/error，858 项测试全部成功，包括新增 19 项正文解读及 17 项查看交互测试；ARM64 Debug/Release Flutter AAR 均构建成功。[AAR 产物](https://github.com/giturass/B-box/actions/runs/45059030530/artifacts/14768743191) 与 [截图产物](https://github.com/giturass/B-box/actions/runs/45059030530/artifacts/14768758084) 的 `workflow_run.head_sha` 均已核对与该源码一致。核心、脚本取消及 Android 结果由 [应用验证记录](app.md#当前验证记录) 维护。

实际逐张查看本次 `build/screenshots/body/` 的八张截图：320×568 浅色的 `320-1.8-light-hex.png`、`320-1.8-light-hex-end.png`、`320-1.8-light-base64.png`，360×568 深色的 `360-1.4-dark-hex.png`、`360-1.4-dark-hex-end.png`、`360-1.4-dark-base64.png`，以及 360×800 的 `360-light-image-inline.png` / `360-light-image-viewer.png`。320 的系统字号输入为 1.8，组件使用应用上限 1.4；360 窄屏的系统及组件字号均为 1.4，布局测试另核对了实际缩放值。Hex 按剩余宽度使用每行 2/4 字节，地址、字节和 ASCII 列没有水平溢出；Base64 自然折行，工具栏固定于顶部。Hex 尾页截图只表示内部列表已滚至末端，卡片下部仍参与页面纵向滚动，不将未出现在截图视口内的尾字节作为视觉结论。

两张图片截图中，完整 32×24 PNG 已成功解码并按比例展示，详情入口及查看页的重置、复制和显示方式可见；图片解码失败不能通过该有效图片用例。缩放/重置、坏图回退、页码/原字节复制、迟到会话结果及横向滚动由同轮 widget 断言覆盖。截图采用英文、SDK Roboto、实际 JetBrainsMono 与 Android 桥接替身，未覆盖中文正文界面、真实远程数据、所有图片格式或 ARM64 实机视觉；本轮没有上游逐像素比较结论。本机仅运行静态检查并读取本次 Actions 日志、报告和截图，后续验证记录提交仅补充文档。下列记录只适用于各自列出的此前源码 SHA。

2026-11-22 “忽略电池优化”授权行的功能及测试源码 `fa694fb01444a3f6309c353fadef054b4402306a` 已通过 [Flutter 作业](https://github.com/giturass/B-box/actions/runs/45013769449/job/137181589422)：分析为 125 项 info、0 warning/error，822 项测试全部成功，其中新增 65 项电池授权交互、状态、布局及参考比较测试；ARM64 Debug/Release Flutter AAR 均构建成功。[AAR 产物](https://github.com/giturass/B-box/actions/runs/45013769449/artifacts/14751108134) 和 [截图产物](https://github.com/giturass/B-box/actions/runs/45013769449/artifacts/14751336977) 的 `workflow_run.head_sha` 均与该源码一致。核心、JVM 与 Android 验证由 [应用记录](app.md#当前验证记录)维护。

逐份核对 `build/screenshots/battery-optimization/` 下的 50 个比较 JSON 和 102 张 PNG，并独立解码全部 50 对图片：共 1,734,944 个 RGBA 像素，差异像素数及允许差异数均为 0，图片尺寸与各自报告一致。该结果覆盖测试文件声明的中英文、字号、主题、四种状态及原版分组首行情景；对照保留固定 FlClash 子模块的原始行渲染器，字体、主题与动画输入相同。

实际查看两张完整服务页 `zh-360-light-1.0-service-unauthorized.png`、`zh-320-dark-1.4-service-authorized.png`，以及中文首次加载、请求等待、英文窄屏未授权与纯黑已授权行：按钮文字、颜色、宽度及占位符合参考；320 dp / 1.4 倍字号的中文标题自然换行，英文长标题遵循原版两行省略规则，授权按钮完整展示。桥接替身、受控字体及固定指示器输入不覆盖系统授权页面、完整动画过程或 ARM64 实机视觉；原生适配差异见 [应用文档](app.md#配置服务应用与备份)。本机仅执行静态检查并读取本次 Actions 日志和产物；后续验证记录提交仅补充文档，下列记录只适用于各自列出的此前源码 SHA。

2026-11-22 事件与手动调试的功能源码 `390abf9eadcf795af84d2a6f5203cc3708fb0671` 已通过 [Flutter 作业](https://github.com/giturass/B-box/actions/runs/44999778401/job/137138547132)：分析为 105 项 info、0 warning/error，757 项测试全部成功，包含 36 项脚本回归（2 项普通测试、34 项 widget 测试）；ARM64 Debug/Release Flutter AAR 均构建成功。[AAR 产物](https://github.com/giturass/B-box/actions/runs/44999778401/artifacts/14745337923) 和 [截图产物](https://github.com/giturass/B-box/actions/runs/44999778401/artifacts/14745507431) 的 `workflow_run.head_sha` 均与上述源码一致。核心、JVM 和 Android 结果由 [应用验证记录](app.md#当前验证记录) 维护。

实际逐张查看本次 `build/screenshots/scripts/` 下中文 320×568、1.8 倍字号的六张截图：`configuration-zh-320-1.8.png`、`event-editor-zh-320-1.8.png`、`event-triggers-zh-320-1.8.png`、`event-json-zh-320-1.8.png` 及 `output-zh-320-1.8-before-scroll.png` / `output-zh-320-1.8-after-scroll.png`。四种脚本入口、三个事件选项、保存的 `profile-reloaded` JSON、长结果/日志的滚动内容和固定复制/关闭按钮可读。

这些是 Linux widget 截图，使用 [测试字体](flutter/test/support/fonts/README.md) 与 Android 桥接替身；不作为 ARM64 实机视觉或与上游逐像素一致的验收。本机仅静态检查并读取 Actions 日志、报告和截图，后续验证记录提交仅补充文档。下列记录只适用于其中明确列出的此前源码 SHA。

2026-11-22 声明式复合匹配及 smoke 长等待移除后的源码 `baa5acd1bebe58b71f0f651dc4ceeb5ab0a662ac` 已通过 [Flutter 作业](https://github.com/giturass/B-box/actions/runs/44987095303/job/137099941409)：分析为 98 项 info、0 warning/error，721 项交互/布局测试及 ARM64 Debug/Release Flutter AAR 均成功。两项新增用例实际经过 HTTP 改写和 HTTP 脚本规则表单，验证复合条件被拒绝后保留草稿、配置及修订，修正后重试保存；原生桥接使用替身，核心及 Android 的实际校验和执行结果见 [应用验证记录](app.md#当前验证记录)。

该次未审阅新增规则表单说明的截图，未完成 ARM64 实机视觉验收；原有截图不能作为该次表单的视觉通过依据。本机只执行静态检查及读取 Actions 结果，验证记录提交仅补充文档。

UI 进程拆分、状态重连及后台回收时机调整的验证结果与覆盖范围由 [应用文档](app.md#当前验证记录) 维护。下列截图与 Actions 结果对应各自列出的此前源码 SHA。

2026-11-20 本轮在 `6df59c595fe0ff86ca4c96c884a14070b61620f9` 基础上，将 Prism UI 从 `63450c0ac0e8e1b2c0f3ed4ad7926dc94e083185` 同步至 `7831ade349ede896aedb8a5757dc9c3fe88081d5`。193 个本地包文件中 191 个逐字节对应新提交，两个仅恢复固定 SDK 所需的 rendering 导入；来源清单区分原样导入、兼容补丁与 FlClash 子模块参考。日志流显式启用尾部锚定，上游测试已接入统一工作流。

本轮功能源码 `5931339c26bd4e4791b9fec3a68d4713f6780032` 的 [统一测试](https://github.com/giturass/B-box/actions/runs/44692039380)（[Flutter 作业](https://github.com/giturass/B-box/actions/runs/44692039380/job/136139917060)）完成源码约束检查、Flutter 分析（95 项 info，0 warning/error）、全部 697 项交互/布局测试（B-box 323 项、Prism 374 项）及 ARM64 Debug/Release Flutter AAR 构建。[AAR 产物](https://github.com/giturass/B-box/actions/runs/44692039380/artifacts/14608958430) 和 [newui-validation 截图](https://github.com/giturass/B-box/actions/runs/44692039380/artifacts/14608874032) 的 `workflow_run.head_sha` 均已核对与该源码一致。

实际查看下列五张本轮截图，文件名相对截图产物的 `build/screenshots/`：

| 截图 | SHA-256 | 可观察结论 |
| --- | --- | --- |
| `navigation/diagnostics-long-outbound-320-light-1.4.png` | `fdaed69efa5f274797c55b1e9ba51475366bd22f54e157b2f87dfb9474fe785c` | 320 宽、1.4 倍字号下，Outbound 标题完整，长值省略，选择图标与文字分离；返回、开关及 Start 按钮无可见遮挡或溢出 |
| `360-dark-1.4-Profiles-populated.png` | `6068b88e70d4eae96a24381b7d410d1a2a69475ee693a6cea488eabe7c54e0bb` | 配置名称、更新时间和选中对号互不遮挡；节点卡片、顶部工具栏及底部导航无可见溢出或重叠 |
| `800-light-1.0-Profiles-populated.png` | `aa60e4a9cdbd6187eab86fc29261886f75e7e5ceed09955d5e423b75b42e6002` | 宽屏配置标题完整，时间与对号位于右侧；侧栏、工具栏和节点卡片边界清晰 |
| `360-dark-1.4-Profile-edit.png` | `e7d3db2264941a5e4acab5fda8ceb55fd69b22561d684a441920384313912862` | 弹层关闭、标题和保存按钮分离，可见字段与说明无水平溢出；底部更新间隔行仅露出滚动边界的一部分 |
| `requests/360-1.4-dark-message-menu.png` | `be3c5cf4451c533f36e88d175ab56bae4fe211e02b87a6cea4191eb5df825632` | 菜单标题、Request/Response/Cookies 和选中对号位于弹层内，菜单行无可见重叠或越出视口 |

这些是 Linux widget 测试截图，采用 SDK Roboto、报文区域的 JetBrainsMono 与 Android 桥接替身；未覆盖中文、键盘弹出、完整表单滚动、加载及弹层动画、真实远程数据或 ARM64 实机视觉。本机仅执行来源哈希、源码约束、`git diff --check` 及文档本地链接/标题片段检查；后续验证记录提交仅补充文档，结论固定于上述功能源码 SHA。核心及 Android 验证由 [应用记录](app.md#当前验证记录) 维护。

### 历史验证记录

下表索引此前记录过的 Flutter 运行，只用于查询，不是当前源码的通过声明；成功行均包含 Flutter 分析、交互/布局测试和 ARM64 Debug/Release Flutter AAR 构建，分析结果无 warning/error；失败或取消的运行按表注明，不能作为通过依据。截图路径相对各次 `newui-validation` 的 `build/screenshots/`，逐张的核对结论和未覆盖范围保留在 Git 历史，可用 `git show 06eed8f5f830a90b9853995ced3b2e211deca628:flclash.md` 查看整理前的完整记录。对应的核心与 Android 结果见 [app.md](app.md#历史验证记录)。

| 日期 | 主题 | 验证源码 | Actions | 测试 / info | 当时实际查看的截图 |
| --- | --- | --- | --- | --- | --- |
| 2026-11-23 | 大正文恢复生命周期修正前 | `174fa0dfdec9f1bc35bcc46a61dddada9b7531c0` | [45133726683](https://github.com/giturass/B-box/actions/runs/45133726683) | 879 通过、1 失败 / 152 | 九张正文截图已预检查，跨页 PNG 文件解码成功；恢复测试非法生命周期过渡，AAR 跳过，后续推送取消未完成作业，不能作为整套通过依据 |
| 2026-11-23 | 大正文恢复失败定位前 | `9a812b7896fc33e071dccdd73a3f5165336389e3` | [45136164614](https://github.com/giturass/B-box/actions/runs/45136164614) | 879 通过、1 失败 / 152 | 恢复后无正文文本元素，AAR 跳过；本轮未完成独立视觉验收，核心和模拟器结果见 [应用历史](app.md#历史验证记录) |
| 2026-11-23 | 大正文恢复异步断言修正前 | `4056b906b9518c665a7039cc626ed4bff860c070` | [45139786594](https://github.com/giturass/B-box/actions/runs/45139786594) | 879 通过、1 失败 / 153 | 诊断确认新读取器及恢复页偏移正确，异步桥接回调中的 expect 与 pumpAndSettle 的测试保护冲突；AAR 跳过，后续推送取消未完成作业，无最终视觉结论 |
| 2026-11-23 | 正文查看 SDK 接口修正前 | `3b2df5f9ebac0bdb1a2942ce979673f2bdbb8786` | [45055280397](https://github.com/giturass/B-box/actions/runs/45055280397) | 未执行 / 139（4 error） | Prism SelectableText 参数及重复本地化键不兼容；后续修正推送取消未完成作业，无通过或视觉结论 |
| 2026-11-23 | 正文查看图片等待修正前 | `5fab9392dd924eed8a64c4bd016e1aa50fe9cbab` | [45055750463](https://github.com/giturass/B-box/actions/runs/45055750463) | 857 通过、1 超时 / 139 | 有效图片用例在 fakeAsync/real zone 之间等待解码而超时；ARM64 AAR 跳过，窄屏 Hex/Base64 仅作预检查，Android 同轮失败见 [应用历史](app.md#历史验证记录) |
| 2026-11-23 | 正文查看取消窗口补齐前 | `7cd74db2e86f892cc1e2d96f060c0419a3bd45bd` | [45057968879](https://github.com/giturass/B-box/actions/runs/45057968879) | 858 / 139 | Flutter 与 ARM64 Debug/Release AAR 通过；关闭期间才附着任务的取消回归及图片成功断言补齐后推送，未完成 smoke 被取消，整套为 cancelled，不作为最终源码通过依据 |
| 2026-11-22 | 电池授权测试清理修复前 | `2d092f7bd5459862a97f43a4ee475f481c9664a8` | [45012453322](https://github.com/giturass/B-box/actions/runs/45012453322) | 757 通过、65 失败 / 124 | 50 对共 1,734,944 像素零差异、102 PNG 齐全；64 项测试结束时未恢复平台变量、1 项未释放语义句柄，AAR 跳过，后续修正推送取消未完成的作业；该轮不能作为测试通过记录 |
| 2026-11-22 | 电池授权参考测试首轮 | `01fce644e524363c8d07e4123a07b07aa7261b0a` | [45011527017](https://github.com/giturass/B-box/actions/runs/45011527017) | 未执行 / 124 | 分析为 1 error、12 warning：消息控制器订阅接口错误及冻结参考私有参数告警；测试和 AAR 跳过，无截图产物，后续修正推送取消未完成的作业 |
| 2026-11-22 | 脚本调试首轮 | `3669e7f86bd877a8c2b1827f375c8e49b2bbdc73` | [44992556782](https://github.com/giturass/B-box/actions/runs/44992556782) | 751 通过、4 失败 / 105 | 脚本交互/布局回归失败，不能作为通过依据；整套结果见 [应用历史](app.md#历史验证记录) |
| 2026-11-22 | 脚本截图字体与查找 | `7af1019e74e6d881bc3481076acc4433f9f27aca` | [44994399341](https://github.com/giturass/B-box/actions/runs/44994399341) | 756 通过、1 失败 / 105 | 截图用例在滚动前访问尚未构建的 JSON 字段，后续修正推送取消未完成 smoke |
| 2026-11-22 | 脚本截图字段定位 | `94f122ced99f0c54e77b6da0043ca26937f945d9` | [44995309984](https://github.com/giturass/B-box/actions/runs/44995309984) | 756 通过、1 失败 / 105 | 用例误用不存在的 `http-scripts` Key，后续修正推送取消未完成 smoke |
| 2026-11-22 | 脚本截图回归修正 | `a4d6e9132a6bd02e1cbbd8fdf8f29efd90198550` | [44996187416](https://github.com/giturass/B-box/actions/runs/44996187416) | 757 / 105 | Flutter 与 ARM64 AAR 成功，实际查看上述六种脚本截图；Android 三组暴露探测与脚本准入锁冲突，整套失败原因见 [应用历史](app.md#历史验证记录) |
| 2026-11-20 | Prism UI SDK 导入修复 | `6d9fac605557ff27067b03a01b47a4765954300c` | [44690773881](https://github.com/giturass/B-box/actions/runs/44690773881) | 695 通过、2 失败 / 95 | Prism 374 项全部通过；配置卡更新时间在大字号下溢出约 1.1 px，日志测试 Key 类型与生产组件不匹配；ARM64 AAR 跳过，修正推送取消运行，截图不作为当前通过依据 |
| 2026-11-20 | Prism UI 0.1.3 首轮同步 | `b5927256036a156b918063adcf873a49122aed2a` | [44689944833](https://github.com/giturass/B-box/actions/runs/44689944833) | 未运行 / 95（4 error） | ScrollCacheExtent 显式导入删除与固定 SDK 导出不兼容；测试和 AAR 跳过，无本轮截图结论；修复原始来源及实际补丁已记录 |
| 2026-11-19 | 应用独立版本与更新弹窗 | `556a8716e0f7152ae2c3b012145127bcd1fea6d2` | [44489197911](https://github.com/giturass/B-box/actions/runs/44489197911) | 320 / 95 | ARM64 AAR 通过；实际查看中英文 320×568、1.4 倍字号下载、未知来源授权和签名错误六张更新弹窗截图，无重叠/遮挡/溢出；采用测试字体与桥接替身，早于本轮品牌图标及组件更新；设备安装与实机视觉未验收 |
| 2026-11-19 | 首次通知回归断言修正 | `3d175de0dae76909a2a136ad5a40ae143628f6b2` | [44462310150](https://github.com/giturass/B-box/actions/runs/44462310150) | 306 / 93 | 分析/测试通过，ARM64 AAR 仓库依赖解析失败；x64 Debug AAR 已通过。无本轮分享页视觉验收；整套结果见 [应用历史](app.md#历史验证记录) |
| 2026-11-19 | 节点分享页标准链接与 JSON 简化 | `41834548e78196aec04f597995c6a7b724bf1809` | [44460811251](https://github.com/giturass/B-box/actions/runs/44460811251) | 306 / 93 | Flutter 分析/测试及 ARM64 AAR 通过，分享页视觉未验收；整套结果见 [应用历史](app.md#历史验证记录) |
| 2026-11-19 | WARP WireGuard 选项与注册说明 | `bd720bcdf5742e6cc3f8697b16ab12e6569350a1` | [44455949442](https://github.com/giturass/B-box/actions/runs/44455949442) | 306 / 93 | Flutter 分析/测试及 ARM64 AAR 通过，早于分享页展示调整；无本轮分享页视觉验收，整套结果见 [应用历史](app.md#历史验证记录) |
| 2026-11-18 | WARP 注册与停服代理组选择 | `5017c42c890fa3cdaebd5edfa7323c7473107005` | [44123007908](https://github.com/giturass/B-box/actions/runs/44123007908) | 302 / 92 | 三张英文初始页：`360-light-1.0-WARP-registration.png`、`360-dark-1.4-WARP-registration.png`、`800-light-1.0-WARP-registration.png`；当时核对无溢出/遮挡，不含 WireGuard、本轮分享页、中文、注册中、待保存或 ARM64 实机；逐张哈希与原核对结论见 Git `bd720bc:flclash.md` |
| 2026-11-17 | XHTTP/VLESS Encryption 支持及许可登记 | `8c56b623b46c7ab4c5c4632b1db428ebc49dcb25` | [44086051274](https://github.com/giturass/B-box/actions/runs/44086051274) | 287 / 84 | 无新增界面几何变更或实机视觉记录 |
| 2026-11-17 | 配置内订阅来源及跳过报告 | `5937367671729a0c726182500288732851aa969a` | [44070105954](https://github.com/giturass/B-box/actions/runs/44070105954) | 287 / 84 | 旧核心的 API 24/31/36 及 16 KiB 来源卡片、SOCKS 节点与原分组；当时 XHTTP 为跳过项，不包含本轮协议支持；API 24 默认联网启动失败，未做 ARM64 实机验收 |
| 2026-11-17 | 默认名称、选中对号及弃用警告默认值 | `a4607728af065cb46a8b0c021480988bd9531442` | [43917329871](https://github.com/giturass/B-box/actions/runs/43917329871)（[Flutter 作业](https://github.com/giturass/B-box/actions/runs/43917329871/job/133474906780)） | 271 / 84 | 配置卡片与代理区域等宽，选中对号、大字号和宽屏文字无重叠；查看 360 浅色、360 深色 1.4 倍、横向滚动、800 浅色配置页及内核设置截图，未覆盖本轮订阅来源 |
| 2026-11-16 | 配置内节点、等宽布局及首页切换语义修复 | `041325d4824cfae8f115f25021f0f7a8160c68aa` | [43875039054](https://github.com/giturass/B-box/actions/runs/43875039054)（[Flutter 作业](https://github.com/giturass/B-box/actions/runs/43875039054/job/133325897659)） | 271 / 84 | Flutter 分析、测试及两种 AAR 通过；实际抽样查看 360 浅色、360 深色 1.4 倍、横向滚动和 800 浅色配置页，边框与代理区域等宽且文字、更新时间、操作及底栏没有重叠；当时配置卡片使用铅笔，未覆盖本轮恢复对号 |
| 2026-11-16 | 首页切换语义测试句柄清理 | `eb81cc5820d6073e0b0a004d71ea912d0056aff6` | [43874301204](https://github.com/giturass/B-box/actions/runs/43874301204)（[Flutter 作业](https://github.com/giturass/B-box/actions/runs/43874301204/job/133323252873)） | 269 通过、2 失败 / 84 | 两项新增语义测试的功能断言完成，结束验证时句柄仍 active；Flutter AAR 跳过，修正推送取消整次运行 |
| 2026-11-16 | 首页切换语义测试查找修正 | `cc8a786cd131c3cf44e239154e61f444987aaa08` | [43873540377](https://github.com/giturass/B-box/actions/runs/43873540377)（[Flutter 作业](https://github.com/giturass/B-box/actions/runs/43873540377/job/133320593495)） | 269 通过、2 失败 / 84 | 两项新增用例用 `byTooltip` 查找已排除提示语义的 Prism 控件，在进入语义断言前失败；Flutter AAR 跳过，修正推送取消整次运行 |
| 2026-11-16 | 配置内节点与等宽布局 | `662f6eedd569759c3ee1cc7cad0707cee3f19827` | [43869161640](https://github.com/giturass/B-box/actions/runs/43869161640)（[Flutter 作业](https://github.com/giturass/B-box/actions/runs/43869161640/job/133305587339)） | 269 / 84 | Flutter 分析、测试及两种 AAR 通过；抽样查看 360 浅色、360 深色 1.4 倍、横向滚动和 800 浅色配置页，边框分别为 16/344、64/784 dp，与代理区域等宽，文字/铅笔/底栏无重叠；Android API 24 smoke 随后发现首页切换提示语义合并到整张卡片，不能作为当前验收 |
| 2026-11-16 | 配置内节点与等宽布局首轮 | `359ef06801f42f36b9207cda1786ce6de92280e7` | [43868300388](https://github.com/giturass/B-box/actions/runs/43868300388)（[Flutter 作业](https://github.com/giturass/B-box/actions/runs/43868300388/job/133302439776)） | 267 通过、2 失败 / 84 | 不计为通过：新建节点菜单项未进入可点击区域，800 宽用例误点非导航文字；Flutter AAR 构建跳过，整次运行因修正推送取消。截图为失败源码的预检查产物，不作为最终验收 |
| 2026-11-15 | 配置页重新设计 | `d1282569f242832437824865a0f6e1cf84b8599e` | [43723480439](https://github.com/giturass/B-box/actions/runs/43723480439)（[Flutter 作业](https://github.com/giturass/B-box/actions/runs/43723480439/job/132840276393)） | 260 / 84 | 360 宽深浅主题与大字号配置页、配置滑动、空状态、固定黑色底栏、800 宽配置页和 API 24 smoke `04-core-proxies.png`；当时共享节点仅有交互覆盖，抽样截图结论与未覆盖范围详见 Git `d128256:flclash.md` |
| 2026-11-14 | 配置卡片顶部精简及工具栏收纳调整 | `2be09f60fcf6244f9f30c7899c5da382359701e0` | [43570946186](https://github.com/giturass/B-box/actions/runs/43570946186)（[Flutter 作业](https://github.com/giturass/B-box/actions/runs/43570946186/job/132374987419)） | 259 / 84 | `360-light-1.0-Profiles-populated.png`、`360-dark-1.4-Profiles-populated.png`、`360-dark-1.4-Profiles-scrolled.png`、`navigation/fixed-black.png`、`800-light-1.0-Profiles-populated.png`；另查看 API 24 smoke 的 `04-core-proxies.png`，详见 Git `d128256:flclash.md` |
| 2026-11-14 | 配置/代理合并、设置底栏与仪表盘铅笔 | `2adc1b653bd98acc68929deec19f77dfa04f0022` | [43562639502](https://github.com/giturass/B-box/actions/runs/43562639502) | 259 / 84 | 配置滑动前后、深浅主题/大字号、固定底栏、宽屏、设置与仪表盘共七张 Flutter 截图；API 24 启动、分组、设置三张模拟器截图，详见 Git `ab20ad1:flclash.md` |
| 2026-11-14 | 配置合并首轮测试定位修正 | `6dcc6ed17e49834b6bae4c201ed6d21032f813cd` | [43562160382](https://github.com/giturass/B-box/actions/runs/43562160382) | 259（7 失败） / 87 | 不计为通过：底栏 Settings 提示被误算为仪表盘按钮、折叠分组查找测速按钮，共七项用例失败；修正推送取消尚未完成的作业，截图不作为当前验收 |
| 2026-11-14 | 代码整理与 MASQUE 网络重置修复 | `06eed8f5f830a90b9853995ced3b2e211deca628` | [43523435178](https://github.com/giturass/B-box/actions/runs/43523435178)（[Flutter 作业](https://github.com/giturass/B-box/actions/runs/43523435178/job/132223173515)） | 257 / 84 | `requests/unified-related-requests.png`、`requests/requests-pagination.png`、`requests/independent-mitm-config.png`、`navigation/fixed-light.png`、`360-light-1.0-Dashboard-populated.png` |
| 2026-11-13 | WARP MASQUE 集成 | `e0d77c9a5507a3503c5942c1a0b38bd702dbfd5e` | [43434680819](https://github.com/giturass/B-box/actions/runs/43434680819)（[Flutter 作业](https://github.com/giturass/B-box/actions/runs/43434680819/job/131916654215)） | 257 / 83 | `requests/unified-related-requests.png` |
| 2026-11-13 | 图标/主题缓存、代理组通知、JSON 编辑和请求历史分页优化 | `efe9028214cd52ef03f5a8b1e0143006be5ba6ad` | [43407927934](https://github.com/giturass/B-box/actions/runs/43407927934)（[Flutter 作业](https://github.com/giturass/B-box/actions/runs/43407927934/job/131826877757)） | 257 / 83 | `requests/requests-pagination.png`、`requests/unified-http2-and-connections.png`、`requests/unified-related-requests.png`、`requests/360-1.4-dark-list.png`、`requests/800-1.0-light-list.png`、`360-light-1.0-Proxies-populated.png`、`360-dark-1.4-Proxies-populated.png` |
| 2026-11-13 | 原始 JSON 选区修复 | `74bcbfc9d76b994d5bfb91f3401eee5f1109a9fc` | [43331463838](https://github.com/giturass/B-box/actions/runs/43331463838)（[Flutter 作业](https://github.com/giturass/B-box/actions/runs/43331463838/job/131576932628)） | 246 / 79 | 未记录截图。修复前的 `208c5675d7c5582925a02c2002a6e1b3c62a2ed9`（[43328760156](https://github.com/giturass/B-box/actions/runs/43328760156)）有六个滚动后的选区用例失败，不计入 |
| 2026-11-11 | 本地资源导入与正文分页 | `d3a1f178d6d2375291b698d7200ef9ab8ff98260` | [42914488877](https://github.com/giturass/B-box/actions/runs/42914488877) | 234 / 79 | `requests/resource-import-scripts.png`、`requests/resource-import-http_rewrite.png`、`requests/body-first-page.png`、`requests/body-last-page.png` |
| 2026-11-10 | 正文保留方式、WebDAV 长输入及表单滚动边界 | `6414cea3bfca092b8f5c146552f6c76738cb4bf1` | [42848220348](https://github.com/giturass/B-box/actions/runs/42848220348) | 232 / 80 | `requests/360-1.4-dark-settings-ports.png`、`requests/360-1.0-light-settings-ports.png`、`requests/800-1.0-light-settings.png`、`360-dark-1.4-Profile-edit.png`、`800-light-1.0-Profile-edit.png` |
| 2026-11-10 | 统一请求查看器 | `3922cd606f520f6581bd4fd0a7d211a68a24ca23` | [42760227283](https://github.com/giturass/B-box/actions/runs/42760227283) | 229 / 80 | `requests/unified-http2-and-connections.png`、`requests/unified-related-requests.png`、`requests/requests-pagination.png`、`requests/360-1.4-dark-list.png`、`requests/360-1.4-dark-details.png`、`requests/360-1.4-dark-body.png`、`requests/800-1.0-light-list.png`、`requests/800-1.0-light-details.png` |
| 2026-11-05 | FlClash v0.9.3 / Prism UI 升级 | `d9254711ccf34cda547d2f2287b0e0a508e861a6` | [41982716632](https://github.com/giturass/B-box/actions/runs/41982716632) | 215 / 78 | 800 宽浅色的代理、配置、工具和配置编辑页，360 宽的首页、外观和配置编辑页，`requests/independent-mitm-config.png`、`requests/independent-mitm-tcp-quic.png`、`requests/360-1.4-dark-message-menu.png`、`requests/800-1.0-light-settings.png`，三种 `navigation/fixed-*.png`，`status-card/zh-dark-large-bbox.png`；另查看 `android-smoke-api-31` 的启动、分组和配置恢复截图 |

### 升级参考版本

升级参考版本时同步审查 [来源清单](#参考版本与来源清单)、适配文件、字体/着色器、许可和独立参考渲染器，并核对 [固定 Flutter SDK](.github/actions/setup-flutter/action.yml) 下的选区覆盖层入口。结果按实际 Actions 运行写入 [当前验证记录](#当前验证记录)，原记录压缩为历史表中的一行；旧截图和失败运行不作为新版本的通过依据。
