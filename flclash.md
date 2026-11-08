# FlClash UI 使用情况

本文记录界面来源、复用和适配范围。Android 后端差异见 [app.md](app.md)，核心扩展见 [libbox.md](libbox.md)，维护约束见 [AGENTS.md](AGENTS.md)。

## 参考版本与来源清单

固定参考 [chen08209/FlClash v0.9.3](https://github.com/chen08209/FlClash/tree/be84736605527034689c95bf27828ff27d912c5e)，提交 `be84736605527034689c95bf27828ff27d912c5e`（2026-11-04）。其 `plugins/prism_ui` 子模块固定为 [prism_ui 0.1.2](https://github.com/chen08209/prism_ui/tree/63450c0ac0e8e1b2c0f3ed4ad7926dc94e083185)，提交 `63450c0ac0e8e1b2c0f3ed4ad7926dc94e083185`。当前使用实际 Flutter 组件，历史 Compose 仿制按钮和卡片不再定义界面。

[flutter/flclash-manifest.json](flutter/flclash-manifest.json) 是逐文件来源清单：

| 清单字段 | 使用方式 |
| --- | --- |
| `files` | 直接导入的 15 个 FlClash 应用层 UI 文件及两个字体；哈希对应将 Dart 包引用从 `fl_clash` 机械改名为 `b_box_ui` 后的文件 |
| `adaptedFiles` | 仪表盘、配置/代理、外观、备份、IP 质量查询及参考测试等适配文件；记录原始来源哈希和实际改动说明 |
| `adaptedNativeFiles` | `libbox/ui_egress.go` 对 FlClash IP 查询的适配来源；独立保留 v0.9.2 的 `7fd417fb9a9a277c6a9339a8ac87f907df7c25f7` 参考，不随界面升级改写原生查询 |
| `prismUi` | 固定子模块提交、原样导入的 153 个库文件、B-box 固定导航栏适配及应用内许可资源；包含源码、生成模型、两个 shader、包配置、许可和说明 |
| `supplementalReferences` | 独立于整体 UI 基线的 MITM dev 补充参考，按原样导入、改写或设计参考记录源文件哈希；本地路径相对仓库根目录 |

清单中 `files` 和 `adaptedFiles` 的本地路径相对 `flutter/`，`adaptedNativeFiles` 的本地路径相对仓库根目录；`source`/`sources` 指向各自固定参考提交内的上游路径。`prismUi.files` 相对其 `localPath`，其中的适配文件和许可资源相对 `flutter/`。清单记录逐文件哈希和改动来源，本文维护使用说明，两者一并保留。

请求/MITM 功能独立参考 FlClash `dev` 的 [`63aa150e23288558074d877f745a6d3b5f6418f6`](https://github.com/chen08209/FlClash/tree/63aa150e23288558074d877f745a6d3b5f6418f6)，相关提交为 [`f54486102172566e7342d7f5c138663c3c5dec0b`](https://github.com/chen08209/FlClash/commit/f54486102172566e7342d7f5c138663c3c5dec0b) 的 MITM 应用侧设计和 [`1a7556d30a38707f39abfba4b7d6a6b8f57774ed`](https://github.com/chen08209/FlClash/commit/1a7556d30a38707f39abfba4b7d6a6b8f57774ed) 的请求/连接整合。该 dev 源码的 `FEATURE_MITM` 默认关闭，公开 core 不提供完整 MITM 实现；B-box 自行实现 sing-box 路由上的 TLS 接管，不能把上游页面当成可用核心。采用证书信任、报文展示和 CA 模块设计，保留 B-box 的独立连接页。该补充参考与 v0.9.3 UI、Android 上游和核心基线分别维护。

FlClash 应用层组件位于 [flutter/lib/ui](flutter/lib/ui)，共享控件以本地包 [flutter/packages/prism_ui](flutter/packages/prism_ui) 使用，保留原始源码与 shader，不再依赖旧 `material_ui`/`cupertino_ui`。原始许可见 [FlClash GPL](flutter/assets/licenses/LICENSE)、[prism_ui LICENSE](flutter/packages/prism_ui/LICENSE) 和 [NOTICE](flutter/packages/prism_ui/NOTICE)。Prism 内置的 shapes、sliver list、resizable navigator 的 MIT 声明另从源文件头提取至 [应用许可资源](flutter/assets/licenses)，与 HCT/Flutter 的 Apache/BSD 及 SDK、插件许可一起在 [许可证页](flutter/lib/pages/licenses.dart) 展示。该页面按包名列出、搜索并打开可选中的完整正文。

字体归属见 [JetBrainsMono-OFL.txt](flutter/assets/licenses/JetBrainsMono-OFL.txt) 和 [Twemoji-LICENSE.md](flutter/assets/licenses/Twemoji-LICENSE.md)。Twemoji 原始图形来自 Twitter，字体转换由 Mozilla 提供，图形未修改；字体许可同样用于应用内许可证页。

应用内手工登记的许可由 [main.dart](flutter/lib/main.dart) 统一注册，与 [pubspec.yaml](flutter/pubspec.yaml) 打包的 [许可资源](flutter/assets/licenses) 同步维护，覆盖当前 UI、字体和核心扩展依赖。核心正文解码及 jq 的许可包括 [brotli-LICENSE](flutter/assets/licenses/brotli-LICENSE)、[klauspost-compress-LICENSE](flutter/assets/licenses/klauspost-compress-LICENSE)、[gojq-LICENSE](flutter/assets/licenses/gojq-LICENSE) 和 [timefmt-go-LICENSE](flutter/assets/licenses/timefmt-go-LICENSE)；JavaScript 与 Cron 的许可也由同一入口登记。依赖版本及执行范围见 [libbox.md](libbox.md#独立脚本与-cron)。

应用打包的字体均有实际用途：[JetBrainsMono-Regular.ttf](flutter/assets/fonts/JetBrainsMono-Regular.ttf) 用于代码编辑、请求报文、记录详情及终端预览，[Twemoji.Mozilla.ttf](flutter/assets/fonts/Twemoji.Mozilla.ttf) 用于代理名称等 Emoji 文本。[NotoSansSC-status.ttf](flutter/test/support/fonts/NotoSansSC-status.ttf) 仅供状态卡像素比较测试使用，不进入应用资源。

## 界面复用与业务适配

Flutter 承担仪表盘、代理、配置、工具及所有子页面的导航。复用 FlClash 的控件、字形、形状、排版、字体、动效、弹层及导航；页面状态和操作经 Android 桥接接入 B-box 的 Room 与 libbox。没有引入 FlClash/Mihomo 原生库、Clash 配置模型、Mihomo 配置覆写脚本、订阅转换器或 Clash 专用服务。B-box 请求/响应脚本与定时任务的范围见 [独立脚本与 Cron](libbox.md#独立脚本与-cron)。

v0.9.3 使用 `PrismApp`、Prism 主题、表面、按压反馈、输入、弹窗和返回保护；原生操作消息交给 `BannerHost`，采用 Prism 的工具栏避让间距，复制和保存失败反馈期间仍可使用顶部按钮。B-box 表单在输入框上方保留字段标签，脏草稿确认及保存期间禁止退出继续由 `BoxEditGuard` 处理。搜索使用新版回调，连接/日志的可删除关键词由页面维护，并与输入文字共同过滤；排序列表使用 Prism 的最终索引回调，代理标签页显式管理控制器并按组名保留选择。网络速度卡片跟进新版 1.45 最小比例、数值下方的背景曲线及独立重绘区域；外观预览改用逐形状模糊，调色和分组标签按新版测量接口适配。状态参考渲染器同步到 v0.9.3，采用上游普通状态，不引入其 Root 服务边框含义。

| 区域 | 主要实现 | 当前适配 |
| --- | --- | --- |
| 首页与状态 | [dashboard.dart](flutter/lib/pages/dashboard.dart)、[status_panel.dart](flutter/lib/components/status_panel.dart)、[start_toggle.dart](flutter/lib/components/start_toggle.dart) | 复用状态布局、国家徽章、服务按钮形变及网络速率展示；启停调用原生服务流程 |
| 配置卡与分组概览 | [profile_panel.dart](flutter/lib/components/profile_panel.dart)、[proxy_groups_panel.dart](flutter/lib/components/proxy_groups_panel.dart) | 使用真实配置元数据和内核分组；配置卡的代理组、出站、路由规则数字及加载占位与对应标签逐列居中对齐，远端未知数量保持未知 |
| 代理 | [proxies.dart](flutter/lib/pages/proxies.dart)、[proxy_card.dart](flutter/lib/components/proxy_card.dart)、[proxy_list.dart](flutter/lib/components/proxy_list.dart) | 卡片与列表都使用 libbox 分组；只有可选择的组显示选择操作 |
| 配置管理 | [profiles.dart](flutter/lib/pages/profiles.dart)、[profile_card.dart](flutter/lib/components/profile_card.dart)、[profile_details.dart](flutter/lib/pages/profile_details.dart) | 复用行高、选中态、相对更新时间、添加菜单、分组表单及排序样式，编辑和导入 sing-box JSON |
| 首页编辑 | [dashboard_editor.dart](flutter/lib/components/dashboard_editor.dart) | 原地编辑卡片、摇摆、长按排序、移除徽章及添加列表 |
| 内存与局域网 | [memory_panel.dart](flutter/lib/components/memory_panel.dart)、[intranet_panel.dart](flutter/lib/components/intranet_panel.dart) | 分别使用 Go/进程内存和 sing-box 监听配置，不虚构 Mihomo 内存或全局代理端口 |
| 系统 HTTP 代理 | [system_proxy_panel.dart](flutter/lib/components/system_proxy_panel.dart) | 采用参考版 VPN 卡片的布局、状态徽章和紧凑开关，使用 B-box 自有的大陆轮廓地球字形；开关接入内核上报状态及既有 Android 桥接 |
| 外观与关于 | [appearance.dart](flutter/lib/pages/appearance.dart)、[about.dart](flutter/lib/pages/about.dart) | 复用主题预览、调色和版本/许可布局，保持 B-box 品牌及其 Release 来源 |
| 备份 | [backup.dart](flutter/lib/pages/backup.dart)、[form.dart](flutter/lib/components/form.dart) | 复用本地/远程分区、WebDAV 表单、连通状态、恢复范围及策略，后端使用 `bbox-1` 格式 |
| 请求调试 | [requests.dart](flutter/lib/pages/requests.dart)、[request_settings.dart](flutter/lib/pages/request_settings.dart)、[mitm_certificate.dart](flutter/lib/pages/mitm_certificate.dart)、[capture_domains.dart](flutter/lib/pages/capture_domains.dart) | 请求列表/详情、本机筛选、独立 MITM 与证书、TCP 流、域名选择及 HAR/cURL 导出；复用共享列表、表单和导航 |

记录组件使用 [record.dart](flutter/lib/components/record.dart) 中 `RecordTone` 的标签前景色和容器色；首页拖动浮层使用 [dashboard_editor.dart](flutter/lib/components/dashboard_editor.dart) 的 `DashboardShadows.e2` 阴影。应用层已移除未调用的强调色和一级阴影辅助实现，适配差异记录在来源清单；像素比较的独立状态参考渲染器继续保留自己使用的阴影实现。

### 导航与交互约定

关于页区分完整与未完成的版本检查。部分发布暂时不可访问时显示重试提示，保留已验证候选的下载入口；没有完整结果时不显示“已是最新版本”，也不把旧候选的 APK 作为升级下载。检查范围及缓存策略见 [应用版本检查](app.md#已移除能力与兼容入口)。

- 首次启动直接进入仪表盘，没有免责声明接受门槛。Android 默认显示状态、网络速度、出站模式、配置和代理组五张卡；内存与内网 IP 可选。连接从工具页进入，不提供首页连接卡，旧布局/备份忽略该卡，下一次布局保存清理其键。
- 手机可选固定或悬浮导航栏。固定样式由 [main.dart](flutter/lib/main.dart) 在页面下方承载 [BoxNavigationBar](flutter/lib/components/navigation_bar.dart)，保留 Prism 的目的地、选中指示和按压反馈；栏体高度为 64 dp，仅追加系统底部安全区。栏体及底部安全区使用主界面背景色，无阴影和表面叠色，随浅色、深色及纯黑主题同步。悬浮样式使用 `NavigationDock`，宽屏使用 `NavigationSidebar`；外观页预览使用相同底色和紧凑比例。
- [PageSwitcher](flutter/lib/components/page_switcher.dart) 按参考版使用 `PageView` 和保活缓存，滑动或淡入时使用页面快照，跨页切换不会绘制中间页。隐藏页不参与工具栏浮层定位，焦点、语义和原生订阅仍按活动页隔离；宽屏立即切页。
- 外观页提供主题、纯黑模式、主题色、导航栏样式、标签页动画及液态玻璃，不提供字体选择或自定义字体缩放。应用正文使用默认字体，字号随系统变化并沿用 0.8～1.4 倍的缩放范围；旧偏好的兼容处理见 [app.md](app.md#配置服务应用与备份)。
- 出站模式只显示内核提供的值。规则、全局、直连使用参考版字形，名称和选中态忽略大小写，排列和切换参数保留内核原值。系统 HTTP 代理卡片按内核能力显示，支持首页编辑中的排序、移除和重新添加；能力暂不可用时保留顺序和隐藏偏好。卡片沿用参考版 VPN 卡片的 48 像素徽章、加粗标题、启用/禁用副标题及紧凑开关；启用时徽章为主题色六瓣形，禁用时为低强调圆形。卡片和首页编辑列表共用 [system_proxy_glyph.dart](flutter/lib/components/system_proxy_glyph.dart) 中的大陆轮廓地球字形，使用共享 Glyph 的线宽与圆角；全局出站模式保留参考版经纬线球体字形。
- 服务按钮的暂停图形表示停止服务，不新增暂停核心能力。远程会话、启停过渡和不可用操作必须反映真实状态。
- 工具页远程控制紧跟终端；终端设置在终端内，仪表盘编辑在首页工具栏。设置依次为外观、常规、访问控制、特权增强、备份与恢复；配置相关入口统一在配置的“编辑”页，具体排列见 [配置页与内核差异](#配置页与内核差异)。
- 工具页依次提供独立的“请求”和“连接”入口。连接页默认显示活动连接，可切换已关闭/全部，并按速度、创建时间及上下行总量排序；连接订阅不查询请求记录。请求列表沿用连接页的卡片、左右边距、行间距和字形，支持搜索，每批加载 50 条，最多显示内核保留的 300 条。抓取开关等待原生确认，停止抓取或停服后保留请求历史；远程会话的请求页显示不可用，连接页继续展示远端连接。
- 请求页空态说明从新 HTTP 请求/TCP 流开始记录，启停不关闭已有连接；需要重新连接的范围及停止时的记录冻结语义由 [核心录制生命周期](libbox.md#httphttps-请求调试) 维护，中英文文案同步。
- 请求详情使用同一点击复制行及反馈，长 URL、头部与 Cookie 分行显示；右上角菜单切换请求、响应和 Cookies，请求与响应 Cookie 分组展示，保留重复 `Set-Cookie`、空值、值中的等号及属性，点击 Cookie 复制该条原文。
- 抓包设置只提供记录应用、原始目标端口、正文限额和可见域名提取，保存检查本机修订，失败保留草稿。MITM 独立提供配置及证书设置，默认关闭；域名、排除项及协议选项遵循 [MITM 配置](libbox.md#独立-mitm-配置)。证书页显示有效期、公钥 SHA-256、用户/系统信任和私钥可用性，提供 CRT/系统模块导出与 Android 设置入口；打开页面不生成证书，生成是明确操作，重新生成需确认，返回前台可刷新信任。远程会话隔离与平台边界见 [app.md](app.md#请求抓取与-mitm)。
- 请求菜单提供抓包设置、当前搜索的 HAR 导出和清空请求；只显示请求录制状态，移除“配置中的 HTTP 改写已生效”提示及抓包日志功能。运行错误和脚本 console 输出进入应用现有日志，见 [libbox.md](libbox.md#httphttps-请求调试)。
- 请求详情保留重复头部、Cookie、正文状态及 trailers，显示出站/规则、应用及来源/目标地址，字段支持复制。已有记录中保存过的 PID/socket 字段仍可读，缺失值保持未知。域名观察与握手失败标为“仅域名”，不能导出 cURL；请求正文/头部不完整、截断或含 trailers 也不能导出 cURL。TLS/TCP 详情分别显示两个方向的原始字节，不显示 HTTP 状态、头部和 Cookies，禁用 cURL；正常完成的流显示“已完成”。页面复用现有共享组件，没有修改 FlClash 直接导入文件。
- 常规服务页使用即时应用的开关，保存和重启期间显示进度并禁用开关，底部没有“应用服务设置”按钮；启动确认及失败恢复见 [app.md](app.md#配置服务应用与备份)。
- 日志页默认选择“跟随配置”，显示内核上报的日志级别及更严重的记录；菜单同时显示当前级别，仍可手动选择“全部”或单一级别，手动筛选不会被后续状态更新覆盖。搜索、暂停和导出沿用同一筛选结果；默认级别尚未收到时保留已接收记录。日志来源及本地重载同步见 [app.md](app.md#配置服务应用与备份)。
- 应用始终跟随系统语言并响应运行时变化；旧偏好的兼容处理见 [app.md](app.md#配置服务应用与备份)。
- 代理列表保留圆角吸顶分组标题、固定高度的懒加载行、折叠动效及填充/普通/隐藏分组图标。折叠状态按选中配置保存，定位节点通过行偏移实现，不要求节点预先进入视口。
- 访问控制按上游 SFA 的列表/扫描分离方式加载，进入页面直接显示应用与已保存的托管选择；“重新扫描托管应用”为独立操作，扫描期间保留列表。筛选/搜索/排序结果在列表及相关条件未变化时复用，流量等无关状态更新不重新排序。图标保持按可见行请求和缓存，切换显示范围或无修改地关闭查询设置不清空图标；查询策略变更或申请权限后关闭设置只刷新一次。原生查询与托管更新约定见 [app.md](app.md#配置服务应用与备份)。
- 首页编辑保留卡片的全局 key，防止重复出口请求及丢失状态。改动立即保存；保存失败保留草稿，点击完成再次尝试。
- sing-box 专属 Tailscale、Taildrop、终端、认证、USB/IP、诊断和报告使用统一 Flutter 页面，复用原生后端；能力边界见 [app.md](app.md)。
- [privilege.dart](flutter/lib/pages/privilege.dart) 提供 LSPosed 系统状态、VPN 隐藏、应用选择、接口名称、自检、Hook 日志及 Root 诊断导出，沿用共享分区、列表、输入和导航。设置使用显式保存，等待原生确认期间禁止重复提交和退出；失败保留草稿，刷新丢弃草稿需确认。模块未激活或版本不匹配时禁用编辑，远程会话显示本机功能不可用；从 LSPosed 打开模块设置直接进入本页。系统作用域、恢复和兼容边界见 [LSPosed 特权增强](app.md#lsposed-特权增强)。

[box.dart](flutter/lib/box.dart) 按应用状态、启动/外观、流量、内存、连接、日志、工具、诊断、传输和终端分开通知；高频数据只更新对应消费者，不触发应用根部、代理或配置列表重建。出口查询监听应用状态变化，不因遥测或工具进度重新计算路由标识。显式批量通知仍刷新全部依赖。

[data_subscription.dart](flutter/lib/components/data_subscription.dart) 按页面活动状态、TickerMode 和应用生命周期合并内存、日志、连接、工具详情、诊断出站及内网 IP 可见性请求；页面与详情共享一个原生请求，最后一个可见消费者退出时释放。内网 IP 请求决定热点补查频率，认证及来件通知保留独立订阅，具体边界见 [Android 桥接](app.md#界面替换与-android-桥接)和[采样规则](app.md#配置服务应用与备份)。日志增量保留既有记录对象，暂停快照独立于后续追加和淘汰，恢复可见时重新同步原生缓冲。

连接增量按 ID 合并，未变化的行保留原对象；[connection_list.dart](flutter/lib/common/connection_list.dart) 缓存搜索元数据和筛选结果，集合及排序值未变化时复用顺序，计数变化仍更新行内容。列表与详情共用可见性订阅，恢复时接收完整快照；相同排序值按 ID 保持稳定顺序。

[capture_poll.dart](flutter/lib/components/capture_poll.dart) 仅在本地请求列表/未完成详情活动、TickerMode 开启且应用前台时每两秒查询；调用不重叠，筛选变化合并刷新，忽略旧筛选或会话的迟到结果。已完成详情保留当前页面快照并停止定时查询，手动刷新或重新可见/返回前台时重新读取一次。隐藏页面停止轮询，后台录制继续随服务运行。请求列表在共享页面内部读取上下间距，末尾“显示更多”按钮可滚动到固定搜索栏上方。页面为 B-box 业务代码，没有修改 FlClash 直接导入文件。

请求详情在记录带有原始连接 ID 时显示可复制的“连接 ID”，缺省时不显示该行；详情及导出继续使用请求自身 ID。两个 ID 的来源、关联及保留规则见 [核心请求调试](libbox.md#httphttps-请求调试)。

[captured_body.dart](flutter/lib/components/captured_body.dart) 将正文预览放在等宽、自动折行、有界滚动的卡片中，展示内容类型、保存/总字节数及 Base64、截断、未完成状态。完整 JSON 可切换格式化与原文；[http_message.dart](flutter/lib/common/http_message.dart) 仅调整 JSON 字符串外的空白，保留数值字面量、重复键及转义，无法解析或超过格式化限额时显示原文。长预览分批展开，边界不拆开 UTF-16 代理对；复制始终使用内核返回的原始预览，不受当前格式或已展开长度影响，也不代表未保存的完整正文。

### 配置页与内核差异

配置列表按参考版使用两行卡片、固定行高、选中徽标、更新动画及最多三列的自适应网格。普通配置页首次显示时定位已选配置，切换配置的弹层从顶部打开。添加按钮和空状态按钮均打开菜单，新建入口标为“新建配置”；排序使用分组背景、拖动手柄及拖动浮起效果。长按菜单依次提供编辑、更新（远程配置）、复制链接和二维码（远程配置）、分享、导出及删除，删除标为危险操作。当前选中配置的删除项禁用，停服时也需先切换到其他配置；配置页和切换弹层均在打开确认框前及确认后复核最新选择，原生保护见 [app.md](app.md#配置服务应用与备份)。编辑页显示配置摘要，提供元数据分组表单、分项配置和原始 JSON 编辑入口。

服务启动中或停止中，配置列表和已打开的切换弹层禁用配置选择，首页切换配置按钮同步禁用；服务进入运行或停止状态后恢复。原生状态复核见 [app.md](app.md#配置服务应用与备份)。

| 参考版能力 | B-box 的适配边界 |
| --- | --- |
| 链接、文件、二维码导入 | 使用 sing-box JSON / 分享链接，保留 `.bpf`、动态 QRS 和剪贴板入口；不转换 Clash YAML |
| 自定义配置、提供者与覆写 | “新建配置”创建本地 sing-box JSON；通过原始 JSON 及分项配置编辑器修改，不引入 Mihomo 配置生成、提供者或覆写脚本 |
| 配置预览与文件编辑 | 从编辑页的“打开配置内容”入口读取保存的 JSON，不将其称为运行时配置 |
| 自动更新 | 保留 Android 后端至少 15 分钟的限制；关闭后隐藏间隔输入，无效草稿不阻止关闭，保留原有效间隔及导入元数据；批量更新按原生互斥写入流程串行执行 |
| 流量用量与到期徽标 | 当前 Room/桥接没有订阅额度和到期字段，卡片显示真实本地/远程类型，不用会话流量代替额度；这是当前元数据能力的差异，不表示 sing-box 无法支持订阅信息展示 |
| 上次更新时间 | 首页和配置列表均显示 Room 时间戳，采用参考版紧凑相对时间并提供完整时间提示；本地配置显示创建/内容保存时间，远程配置显示订阅更新时间。没有历史记录时显示未知，时间记录和失败恢复见 [app.md](app.md#配置服务应用与备份) |
| 分享与导出 | 复制链接得到原始订阅 URL，分享/二维码保留 sing-box 导入链接和名称，导出仍为 JSON |

编辑保留 B-box 已有的本地/远程类型切换；不会因视觉对齐加入参考版的 age 密钥等未接入能力。保存、修订冲突、配置应用及失败恢复继续由既有桥接处理，见 [app.md](app.md#配置服务应用与备份)。

原始 JSON 编辑器使用 [code_editor.dart](flutter/lib/components/code_editor.dart) 显示从 1 开始的逻辑行号，空行和末尾换行保留编号；长行折行不增加行号，行号随正文滚动。行号布局与正文使用相同的字形、字号比例和换行宽度，仅创建可见编号；搜索、格式化、撤销及修订校验沿用原编辑流程。

分项配置的 JSON 表单使用固定 16 dp 圆角、正文顶部对齐的多行编辑框。[BoxField](flutter/lib/widgets.dart) 将圆角传给 Prism 的填充输入主题，使背景及聚焦边框使用同一轮廓，避免多行编辑区域随高度变成胶囊形。

配置“编辑”页依次直接提供“日志”、“网络”、“DNS”、“路由规则”、“规则集”、“MITM”、“HTTP 改写”和“脚本”，末尾单独保留一个“打开配置内容”入口，元数据表单仅保留元数据字段和更新时间。各分项打开时读取该配置的最新内容，由 [configuration.dart](flutter/lib/pages/configuration.dart) 复用共享分组、规则列表、排序及 JSON 表单；原始 JSON 入口独立于分项加载。内网详情的“编辑”入口复用同一配置编辑页。

MITM 页面直接编辑 HTTPS、域名及排除项、HTTP/2、服务器证书校验、TCP 和 QUIC 回退，并提供单一证书入口；证书入口独立于配置加载。域名编辑器保留输入顺序及通配符、端口后缀，新增抓取域名追加在列表末尾。端口、应用等额外筛选通过同页 JSON 编辑入口修改，见 [MITM 配置](libbox.md#独立-mitm-配置)。HTTP 改写页面依次显示 URL Rewrite、Header Rewrite、Body Rewrite（含 jq）、Map Local / API Mock、Map Remote，下方提供资源及完整 JSON 编辑入口；脚本独立编辑启用状态、HTTP、Cron 和资源。前后查找/反向引用、文件/URL 资源及独立正文限额通过完整 JSON 表单配置，字段及运行行为见 [libbox.md](libbox.md#json-配置中的-http-改写)。新建规则/任务示例默认禁用。

所有修改通过 ProfileConfigStore.set 保留其他字段、修订及原生校验/失败恢复；关闭失败表单前保留草稿。CA 不写入配置，活跃 MITM 或手动记录时禁用重新生成，独立脚本/改写不占用证书。映射请求详情继续把连接出站/规则标为原连接。

[configuration_test.dart](flutter/test/configuration_test.dart) 验证三种配置及 Cron 草稿通过独立路径保存，HTTP/2、证书校验及 QUIC 显式关闭选项正确保存，关闭改写保留 MITM/脚本，校验失败保留内容；[requests_test.dart](flutter/test/requests_test.dart) 覆盖空域名、有序域名/排除项选择、旧记录设置清理、无改写横幅/抓包日志入口及 TCP 详情。生成的独立配置截图为 requests/independent-mitm-config.png、requests/independent-scripts-cron.png；结果按实际源码 SHA 记录。

### 状态卡片与出口查询

查询来源、字段解析及质量判定参考上述固定版本。IP 竞速由 [ui_egress.go](libbox/ui_egress.go) 负责：Cloudflare、IP.SB、IPinfo、ipwho.is、GeoJS、country.is、IPQuery、ident.me 共八源，B-box 的完整解析与失败回退、限额及传输约束见 [libbox.md](libbox.md#出口-ip-和质量查询传输)。

质量查询在 [ip_quality.dart](flutter/lib/common/ip_quality.dart) 中依次分批使用 ident.me/ip-api.com/IPQuery、IPLocate、ipapi.is、proxycheck.io；等待一秒或当前请求全部失败后进入后续批次，单请求最多八秒。保留参考版组织/ASN 解析、显式类型优先、住宅类型推断及 Tor/abuser 质量规则，模型见 [ip_quality_models.dart](flutter/lib/common/ip_quality_models.dart)。

[ip_quality_cache.dart](flutter/lib/common/ip_quality_cache.dart) 将成功质量结果按 IP 持久缓存三天，失败仅在内存冷却五分钟，手动重试可跳过失败缓存。先显示 IP，再补充质量；质量失败保留 IP，详情展示来源和错误。

刷新与会话约定：

- 停止的本地服务通过设备当前网络查询，即使没有配置也可查询和手动重试；运行中通过 sing-box 路由查询。启停时取消旧探测，等待稳定服务状态再查询。远程会话保留占位，不展示本机出口。
- 返回前台保留未变路由的成功 IP 和质量；IP 查询失败才自动重试，间隔至少五秒，手动刷新可跳过。质量缓存到期只刷新质量。
- 短暂失焦不取消请求；已经发出的请求允许在后台完成，新查询等待前台。UI 在后台断开连接时保留本地分组快照，用于返回时比较。
- 启停、运行中的配置/模式/节点及网络变化取消旧请求、忽略迟到结果，并合并 300 毫秒内的变化后查询新出口，不受旧路由冷却限制。分组排序、延迟更新和停服时选择配置不触发无关的出口失效。

### 内网 IP 与备份展示

内网 IP 卡片复用参考版共享开关、端口副标题、Wi-Fi 徽章及复制操作。复制按钮沿用 `DashboardIconButton` 的 32 像素正方形点击区、18 像素图标、低强调前景色及向右 8 像素偏移，不受标题栏高度压缩。共享开关修改选中配置的 mixed/http/socks listen；无代理入站时先显示端口表单，明确新增 mixed 入站。运行时复制已成功启动配置的端点，停服/远程会话复制设备 IP，详情保留多入站/多端口。配置变换和保存约束分别见 [libbox.md](libbox.md) 与 [app.md](app.md)。

备份页沿用参考版的本地/远程布局、文件名、范围和兼容/覆盖策略，实际格式是 B-box `bbox-1`。覆盖会在页面确认后移除备份中不存在的配置；取消选择、写入失败或上传未确认时不显示成功。事务、文件恢复和传输限额由 Android/libbox 实现，见 [app.md](app.md)。

## 验证与升级参考版本

2026-11-05，FlClash v0.9.3 / Prism UI 升级的验证源码为 `d9254711ccf34cda547d2f2287b0e0a508e861a6`。对应 [Actions 41982716632](https://github.com/giturass/B-box/actions/runs/41982716632) 的 Flutter 分析、215 项交互/布局测试及 ARM64 Debug/Release Flutter AAR 构建通过；分析包含 78 条 info、0 条 warning/error。后续只记录结果的 Markdown 提交不改变验证源码，Android 与核心复核见 [应用验证记录](app.md#prism-ui-升级验证)。

[newui.yml](.github/workflows/newui.yml) 在各分支代码推送时自动运行，统一包含核心、Flutter、JVM 和 Android smoke；[setup-flutter](.github/actions/setup-flutter/action.yml) 固定 SDK 和依赖锁文件。Flutter 测试按功能组织，覆盖导航、配置编辑、首页编辑、代理列表、备份、外观、关于、终端设置、模式、内网 IP 和 IP 查询/缓存；[verify_newui.py](tools/verify_newui.py) 核验 FlClash/prism_ui 文件哈希、许可、适配目标和退役依赖等源码约束。截图/像素比较仍由同一测试入口运行，截图与验证 AAR 分别上传到 `newui-validation` 和 `flutter-aar-arm64`。手动 Release 构建独立准备所需的 Flutter Release AAR，不依赖测试运行，见 [构建与发布](app.md#构建与发布)。

[navigation_test.dart](flutter/test/navigation_test.dart) 另检查固定导航在 320 宽、底部安全区、浅色/深色/纯黑及大字号下的高度、底色、页面边界、标签切换和选中语义，并输出固定栏截图。宽屏通过侧栏实际按钮切页，并在截图前核对当前页；外观与 MITM 页直接检查工具栏操作区域像素，防止隐藏首页的按钮残留。复制反馈和保存失败回归另检查顶部按钮仍可点击。[licenses_test.dart](flutter/test/licenses_test.dart) 检查异步加载、共享许可按包分组、搜索及完整可选中正文。

[appearance_test.dart](flutter/test/appearance_test.dart) 检查主题选择与重开页面后的偏好，以及旧字体偏好不覆盖默认字体、系统字号运行时变化和既有缩放上下限。导航、状态、内网 IP、出站模式及请求页面的字号场景通过系统缩放模拟，不依赖已退役的应用偏好。

[profiles_test.dart](flutter/test/profiles_test.dart) 覆盖新建与二进制导入的内容/元数据、添加菜单与取消导入、菜单能力、原始订阅链接、相对时间边界、自动更新校验、选中配置定位、列表及弹层在启停期间禁止切换与状态稳定后恢复、切换失败、排序冲突及远程会话隔离。[navigation_test.dart](flutter/test/navigation_test.dart) 另输出配置编辑与排序弹层在窄屏、大字号和宽屏下的截图；这些是布局回归产物，配置页尚未声明与上游逐像素一致。

[proxies_test.dart](flutter/test/proxies_test.dart) 集中验证自动组禁止手选、延迟测试目标、分组切换、列表折叠及视口外节点定位。[connections_test.dart](flutter/test/connections_test.dart) 检查连接页不请求抓取数据且关闭后释放订阅；[status_panel_test.dart](flutter/test/status_panel_test.dart) 将启动按钮的独立无障碍点击入口与配置可用性、实际桥接调用一起验证。

配置测试另覆盖本地时间的未知占位及原生元数据事件刷新；[code_editor_test.dart](flutter/test/code_editor_test.dart) 对照实际 `RenderEditable` 检查行号在折行、宽度/字号变化及滚动后的对齐，并覆盖空行、末尾换行和替换内容。[intranet_panel_test.dart](flutter/test/intranet_panel_test.dart) 检查大字号下复制按钮的尺寸与位置，以及运行/停服/远程会话的复制内容；这些几何断言不代替实机视觉验收。

[service_settings_test.dart](flutter/test/service_settings_test.dart) 覆盖三个服务开关的即时应用、等待原生确认期间禁用开关及移除手动应用按钮；[box_test.dart](flutter/test/box_test.dart) 检查重复编辑、停止/远程会话、启停过渡及失败后的原生设置恢复。实际服务重启与恢复的 Android 验证范围见 [app.md](app.md#验证与历史记录)。

[logs_test.dart](flutter/test/logs_test.dart) 覆盖本地/远程日志状态的默认阈值、保留更严重的记录、手动筛选及恢复跟随配置，并检查暂停、搜索、导出和清空；实际内核级别与配置重载由 Android 仪器测试覆盖。

[state_updates_test.dart](flutter/test/state_updates_test.dart) 检查高频数据只重建对应订阅者、服务状态仍通知、应用根部不随遥测/工具进度变化重建且外观设置继续生效，并覆盖日志增量淘汰、暂停快照、连接增量对象复用/重置及启动响应不覆盖早到事件。[data_subscription_test.dart](flutter/test/data_subscription_test.dart) 对各类数据检查卡片/详情共享订阅、隐藏页面、TickerMode、前后台和销毁时的订阅切换。[connection_list_test.dart](flutter/test/connection_list_test.dart) 覆盖搜索、过滤、各类排序和增量更新；[capture_poll_test.dart](flutter/test/capture_poll_test.dart) 检查完成详情停轮询、手动刷新和前台恢复。测试验证通知和调用行为，实际 PSS/RSS、CPU 和帧耗时仍需在同设备、同配置下测量。

上述增量、订阅与轮询优化的源码 SHA、统一 Actions 结果和未覆盖范围见 [界面刷新与存储优化验证](app.md#界面刷新与存储优化验证)。

[access_test.dart](flutter/test/access_test.dart) 覆盖进入托管页面不扫描、加载期间接收最新托管选择、显示范围与隐藏选择保留、保存失败后重试、图标复用、扫描等待/成功/失败及查询设置返回后的刷新。它验证交互和调用行为，不代替实机加载耗时或视觉验收。

[privilege_test.dart](flutter/test/privilege_test.dart) 覆盖模块未激活/版本变化、MODULE_SETTINGS 入口、等待保存确认、失败保留草稿、选择保留、远程隔离、迟到结果和取消导出；320 宽大字号深色及 800 宽浅色场景输出 `privilege/` 截图并检查自检页面布局。桥接替身和截图不证明真实 LSPosed 注入、Root 权限流程或目标应用中的隐藏效果；实际验证结果以对应源码的 Actions 记录为准。

[requests_test.dart](flutter/test/requests_test.dart) 覆盖独立工具入口、远程隔离、本机设置修订、MITM 域名选择、端口、启动确认及后台轮询/迟到结果；证书测试检查无配置时从抓包设置经 MITM 页进入证书、明确生成、取消导出、活跃 MITM 禁止重生成、远程切换和信任刷新。其他回归包括 HAR 搜索、停止抓取时的历史分页与搜索重置、TCP 流完成/导出限制、既有 socket 元数据、报文菜单、字段/Cookie 复制及正文格式/Unicode 展开；分页检查“显示更多”可实际点击并加载下一批，请求页不订阅连接。窄屏、大字号深色及宽屏测试运行后将截图写入 build/screenshots/requests/，包含独立配置、请求设置、证书、请求列表、分页按钮及报文详情；桥接替身不能代替实际 TLS 或系统安装验收。

配置页面将滚动视口放在共享标题下方，避免文字穿过标题；测试核对几何位置与 320 宽、1.3 倍字号深色主题。配置约定见 [MITM 配置](libbox.md#独立-mitm-配置)，当前截图检查范围记录在本节末尾。

[http_message_test.dart](flutter/test/http_message_test.dart) 检查 JSON 数值/重复键/转义保真、无效或过大内容回退，以及重复 Cookie、空值、等号和 Expires 逗号。请求正文不完整或截断时不提供 cURL 导出。

[status_panel_parity_test.dart](flutter/test/status_panel_parity_test.dart) 与冻结的 [flclash_status_reference.dart](flutter/test/support/flclash_status_reference.dart) 比较像素，覆盖中英文、深浅主题、0.8/1.0/1.4 字号比例、IPv6、查询等待/失败、组织/ASN 回退及质量等级；停止按钮另行比较。停服状态包含无配置首次启动和设备网络查询的成功/等待/失败情况。

参考渲染器独立保留原版标题、chips、徽章、形变、卡片表面和排版，不导入 B-box 的适配仪表盘组件。比较使用相同视口、字号、语言、主题及 Roboto/Noto Sans SC 字体，按卡片自然高度截图并断言高度，双方图像上传为 CI Artifact。

这些测试只覆盖声明的页面和情景，不等于全应用逐像素一致或已经完成实机验收。升级参考版本时同步审查来源清单、适配文件、许可和独立参考渲染器，按实际 Actions 运行记录结果；Android 验证入口见 [app.md](app.md)。

本轮实际查看上述运行在同一源码生成的 `newui-validation`，以下文件名均相对产物中的 `build/screenshots/`：

| 情景 | 实际查看的截图 | 检查结果 |
| --- | --- | --- |
| 800 宽浅色 | `800-light-1.0-Proxies-populated.png`、`800-light-1.0-Profiles-populated.png`、`800-light-1.0-Tools.png`、`800-light-1.0-Profile-edit.png` | 收起侧栏的选择与实际页面一致；代理延迟、配置元数据、工具分区和居中表单可读，表单保留取消/保存操作 |
| 360 宽浅色及深色大字号 | `360-light-1.0-Dashboard-populated.png`、`360-dark-1.4-Appearance.png`、`360-dark-1.4-Profile-edit.png` | 状态、速度和模式卡片使用新版布局；外观页无隐藏首页工具栏残影；编辑表单标签、开关及顶部操作可见，长输入保留横向编辑与页面滚动 |
| MITM，320 宽、1.3 倍深色 | `requests/independent-mitm-config.png`、`requests/independent-mitm-tcp-quic.png` | 首屏和滚动后的 TCP/QUIC、证书及 JSON 编辑入口可读，标题与滚动内容分离，顶部无代理页按钮残影 |
| 请求菜单与宽屏设置 | `requests/360-1.4-dark-message-menu.png`、`requests/800-1.0-light-settings.png` | 请求/响应/Cookies 菜单保留选中态，正文在有界卡片中展示，宽屏设置侧页保留关闭与保存按钮 |
| 固定导航，320 宽 | `navigation/fixed-light.png`、`navigation/fixed-dark.png`、`navigation/fixed-black.png` | 浅色、深色及纯黑背景与页面一致；栏体及底部安全区不遮挡内容。1.4 倍字号的长英文标签按现有策略省略，完整标签仍用于语义 |
| 中文状态卡，深色大字号 | `status-card/zh-dark-large-bbox.png` | 中文字体、状态徽章、启停图形和卡片排版可读；声明的状态情景同时通过独立上游参考渲染器的像素比较 |

工具栏残影由首页保活方式与 Prism 浮层定位的组合引起，当前使用参考版分页保活；复制及保存失败横幅避让顶部操作。相应像素、点击和失败重试断言在本轮通过，宽屏截图在实际切页并核对当前页后生成。旧截图和失败运行不作为本版通过依据。

同次 `android-smoke-api-31` 产物中实际查看了 `03-vpn-running.png`、`04-core-proxies.png` 和 `05-restored-profile.png`：应用显示运行状态、真实 `Smoke selector` 分组及 `direct` 节点，停止并重启进程后保留导入配置。模拟器报告与上述源码 SHA 一致。

以上是抽样视觉检查及已声明情景的像素回归；没有进行全应用逐像素验收、ARM64 实机性能测量、完整系统证书设置或 Root 管理器交互验收。模拟器运行范围见 [应用验证记录](app.md#prism-ui-升级验证)，此前视觉记录通过本文 Git 历史查询。

### LSPosed 新 UI 验证

2026-11-07，验证源码为 `0a3c0fa28df1ce776c5b0187eec14243c451117a`，对应 [Actions 42308927615](https://github.com/giturass/B-box/actions/runs/42308927615)。Flutter 分析、226 项交互/布局测试及 ARM64 Debug/Release Flutter AAR 构建通过；分析包含 86 条 info、0 条 warning/error。后续补充验证记录的 Markdown 提交不改变应用或测试源码。

新增特权页回归已通过模块未激活/版本不匹配、MODULE_SETTINGS 导航、保存等待/失败草稿、应用选择保留、远程会话隔离、迟到结果及取消诊断导出检查。已实际查看同一源码 `newui-validation` 产物中的 `build/screenshots/privilege/320-1.4-dark.png` 和 `build/screenshots/privilege/800-1.0-light.png`：模块状态、隐藏设置、应用选择、接口名称及诊断入口可读；窄屏大字号允许纵向滚动，顶部标题按现有策略省略，宽屏表单正常居中。自检结果页布局也通过测试。

这些截图使用桥接替身，不证明实际系统模块已激活或目标应用的 VPN 检测已被隐藏；没有新增全应用逐像素验收结论。Android、签名 Release 产物及未覆盖的实机范围见 [LSPosed 恢复验证](app.md#lsposed-恢复验证)。
