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

请求/MITM 功能独立参考 FlClash `dev` 的 [`63aa150e23288558074d877f745a6d3b5f6418f6`](https://github.com/chen08209/FlClash/tree/63aa150e23288558074d877f745a6d3b5f6418f6)，相关提交为 [`f54486102172566e7342d7f5c138663c3c5dec0b`](https://github.com/chen08209/FlClash/commit/f54486102172566e7342d7f5c138663c3c5dec0b) 的 MITM 应用侧设计和 [`1a7556d30a38707f39abfba4b7d6a6b8f57774ed`](https://github.com/chen08209/FlClash/commit/1a7556d30a38707f39abfba4b7d6a6b8f57774ed) 的请求/连接整合。该 dev 源码的 `FEATURE_MITM` 默认关闭，公开 core 不提供完整 MITM 实现；B-box 自行实现 sing-box 路由上的 TLS 接管，不能把上游页面当成可用核心。采用证书信任、报文展示和 CA 模块设计；统一请求查看器的交互参考 Surge [近期请求说明](https://kb.nssurge.com/surge-knowledge-base/zh/guidelines/troubleshooting)及[近期/活动请求 API](https://manual.nssurge.com/tools/http-api.html)，以 B-box 的真实连接和抓取记录实现。该补充参考与 v0.9.3 UI、Android 上游和核心基线分别维护。

FlClash 应用层组件位于 [flutter/lib/ui](flutter/lib/ui)，共享控件以本地包 [flutter/packages/prism_ui](flutter/packages/prism_ui) 使用，保留原始源码与 shader，不再依赖旧 `material_ui`/`cupertino_ui`。原始许可见 [FlClash GPL](flutter/assets/licenses/LICENSE)、[prism_ui LICENSE](flutter/packages/prism_ui/LICENSE) 和 [NOTICE](flutter/packages/prism_ui/NOTICE)。Prism 内置的 shapes、sliver list、resizable navigator 的 MIT 声明另从源文件头提取至 [应用许可资源](flutter/assets/licenses)，与 HCT/Flutter 的 Apache/BSD 及 SDK、插件许可一起在 [许可证页](flutter/lib/pages/licenses.dart) 展示。该页面按包名列出、搜索并打开可选中的完整正文。

字体归属见 [JetBrainsMono-OFL.txt](flutter/assets/licenses/JetBrainsMono-OFL.txt) 和 [Twemoji-LICENSE.md](flutter/assets/licenses/Twemoji-LICENSE.md)。Twemoji 原始图形来自 Twitter，字体转换由 Mozilla 提供，图形未修改；字体许可同样用于应用内许可证页。

应用内手工登记的许可由 [main.dart](flutter/lib/main.dart) 统一注册，与 [pubspec.yaml](flutter/pubspec.yaml) 打包的 [许可资源](flutter/assets/licenses) 同步维护，覆盖当前 UI、字体和核心扩展依赖。核心正文解码及 jq 的许可包括 [brotli-LICENSE](flutter/assets/licenses/brotli-LICENSE)、[klauspost-compress-LICENSE](flutter/assets/licenses/klauspost-compress-LICENSE)、[gojq-LICENSE](flutter/assets/licenses/gojq-LICENSE) 和 [timefmt-go-LICENSE](flutter/assets/licenses/timefmt-go-LICENSE)；JavaScript 与 Cron 的许可也由同一入口登记。依赖版本及执行范围见 [libbox.md](libbox.md#独立脚本与-cron)。

应用打包的字体均有实际用途：[JetBrainsMono-Regular.ttf](flutter/assets/fonts/JetBrainsMono-Regular.ttf) 用于代码编辑、请求报文、记录详情及终端预览，[Twemoji.Mozilla.ttf](flutter/assets/fonts/Twemoji.Mozilla.ttf) 用于代理名称等 Emoji 文本。[NotoSansSC-status.ttf](flutter/test/support/fonts/NotoSansSC-status.ttf) 仅供状态卡像素比较测试使用，不进入应用资源。

## 界面复用与业务适配

Flutter 承担仪表盘、配置、工具、设置及所有子页面的导航；配置页合并配置文件与代理分组。复用 FlClash 的控件、字形、形状、排版、字体、动效、弹层及导航；页面状态和操作经 Android 桥接接入 B-box 的 Room 与 libbox。没有引入 FlClash/Mihomo 原生库、Clash 配置模型、Mihomo 配置覆写脚本、订阅转换器或 Clash 专用服务。B-box 请求/响应脚本与定时任务的范围见 [独立脚本与 Cron](libbox.md#独立脚本与-cron)。

v0.9.3 使用 `PrismApp`、Prism 主题、表面、按压反馈、输入、弹窗和返回保护；原生操作消息交给 `BannerHost`，采用 Prism 的工具栏避让间距，复制和保存失败反馈期间仍可使用顶部按钮。B-box 表单在输入框上方保留字段标签，脏草稿确认及保存期间禁止退出继续由 `BoxEditGuard` 处理。搜索使用新版回调，连接/日志的可删除关键词由页面维护，并与输入文字共同过滤；排序列表使用 Prism 的最终索引回调，代理标签页显式管理控制器并按组名保留选择。网络速度卡片跟进新版 1.45 最小比例、数值下方的背景曲线及独立重绘区域；外观预览改用逐形状模糊，调色和分组标签按新版测量接口适配。状态参考渲染器同步到 v0.9.3，采用上游普通状态，不引入其 Root 服务边框含义。

| 区域 | 主要实现 | 当前适配 |
| --- | --- | --- |
| 首页与状态 | [dashboard.dart](flutter/lib/pages/dashboard.dart)、[status_panel.dart](flutter/lib/components/status_panel.dart)、[start_toggle.dart](flutter/lib/components/start_toggle.dart) | 复用状态布局、国家徽章、服务按钮形变及网络速率展示；启停调用原生服务流程 |
| 配置卡与分组概览 | [profile_panel.dart](flutter/lib/components/profile_panel.dart)、[proxy_groups_panel.dart](flutter/lib/components/proxy_groups_panel.dart) | 使用真实配置元数据和内核分组；配置切换按钮固定在右侧，与进入配置的箭头同列居中对齐。代理组、出站、路由规则数字及加载占位与对应标签逐列居中对齐，远端未知数量保持未知 |
| 配置页代理区域 | [proxies.dart](flutter/lib/pages/proxies.dart)、[proxy_card.dart](flutter/lib/components/proxy_card.dart)、[proxy_list.dart](flutter/lib/components/proxy_list.dart) | 在配置文件下方显示 libbox 分组，保留卡片/列表、搜索、测速与节点定位；只有可选择的组显示选择操作 |
| 配置文件管理 | [profiles.dart](flutter/lib/pages/profiles.dart)、[profile_card.dart](flutter/lib/components/profile_card.dart)、[profile_details.dart](flutter/lib/pages/profile_details.dart) | 顶部横向配置卡片复用行高、选中态与相对更新时间，保留添加、更新、排序、编辑和导入 sing-box JSON；切换弹层使用自适应网格 |
| 首页编辑 | [dashboard_editor.dart](flutter/lib/components/dashboard_editor.dart) | 右上角 FlClash 铅笔按钮或长按卡片进入原地编辑，保留摇摆、长按排序、移除徽章及添加列表 |
| 内存与局域网 | [memory_panel.dart](flutter/lib/components/memory_panel.dart)、[intranet_panel.dart](flutter/lib/components/intranet_panel.dart) | 分别使用 Go/进程内存和 sing-box 监听配置，不虚构 Mihomo 内存或全局代理端口 |
| 系统 HTTP 代理 | [system_proxy_panel.dart](flutter/lib/components/system_proxy_panel.dart) | 采用参考版 VPN 卡片的布局、状态徽章和紧凑开关，使用 B-box 自有的大陆轮廓地球字形；开关接入内核上报状态及既有 Android 桥接 |
| 外观与关于 | [appearance.dart](flutter/lib/pages/appearance.dart)、[about.dart](flutter/lib/pages/about.dart) | 复用主题预览、调色和版本/许可布局，保持 B-box 品牌及其 Release 来源 |
| 备份 | [backup.dart](flutter/lib/pages/backup.dart)、[form.dart](flutter/lib/components/form.dart) | 复用本地/远程分区、WebDAV 表单、连通状态、恢复范围及策略，后端使用 `bbox-1` 格式 |
| 连接与请求 | [requests.dart](flutter/lib/pages/requests.dart)、[connection_record.dart](flutter/lib/components/connection_record.dart)、[request_settings.dart](flutter/lib/pages/request_settings.dart)、[mitm_certificate.dart](flutter/lib/pages/mitm_certificate.dart)、[capture_domains.dart](flutter/lib/pages/capture_domains.dart) | 统一网络活动列表与详情、连接管理、本机录制、独立 MITM 与证书、TCP 流、域名选择及 HAR/cURL 导出；复用共享列表、表单和导航 |

记录组件使用 [record.dart](flutter/lib/components/record.dart) 中 `RecordTone` 的标签前景色和容器色；首页拖动浮层使用 [dashboard_editor.dart](flutter/lib/components/dashboard_editor.dart) 的 `DashboardShadows.e2` 阴影。应用层已移除未调用的强调色和一级阴影辅助实现，适配差异记录在来源清单；像素比较的独立状态参考渲染器继续保留自己使用的阴影实现。

### 导航与交互约定

关于页区分完整与未完成的版本检查。部分发布暂时不可访问时显示重试提示，保留已验证候选的下载入口；没有完整结果时不显示“已是最新版本”，也不把旧候选的 APK 作为升级下载。检查范围及缓存策略见 [应用版本检查](app.md#已移除能力与兼容入口)。

- 首次启动直接进入仪表盘，没有免责声明接受门槛。Android 默认显示状态、网络速度、出站模式、配置和代理组五张卡；内存与内网 IP 可选。连接从工具页进入，不提供首页连接卡，旧布局/备份忽略该卡，下一次布局保存清理其键。
- 手机底栏依次为仪表盘、配置、工具、设置，可选固定或悬浮样式；宽屏侧栏及外观页预览使用相同顺序。固定样式由 [main.dart](flutter/lib/main.dart) 在页面下方承载 [BoxNavigationBar](flutter/lib/components/navigation_bar.dart)，保留 Prism 的目的地、选中指示和按压反馈；栏体高度为 64 dp，仅追加系统底部安全区。栏体及底部安全区使用主界面背景色，无阴影和表面叠色，随浅色、深色及纯黑主题同步。悬浮样式使用 `NavigationDock`，宽屏使用 `NavigationSidebar`；外观页预览使用相同底色和紧凑比例。
- [PageSwitcher](flutter/lib/components/page_switcher.dart) 按参考版使用 `PageView` 和保活缓存，滑动或淡入时使用页面快照，跨页切换不会绘制中间页。隐藏页不参与工具栏浮层定位，焦点、语义和原生订阅仍按活动页隔离；宽屏立即切页。
- 外观页提供主题、纯黑模式、主题色、导航栏样式、标签页动画及液态玻璃，不提供字体选择或自定义字体缩放。应用正文使用默认字体，字号随系统变化并沿用 0.8～1.4 倍的缩放范围；旧偏好的兼容处理见 [app.md](app.md#配置服务应用与备份)。
- 出站模式只显示内核提供的值。规则、全局、直连使用参考版字形，名称和选中态忽略大小写，排列和切换参数保留内核原值。系统 HTTP 代理卡片按内核能力显示，支持首页编辑中的排序、移除和重新添加；能力暂不可用时保留顺序和隐藏偏好。卡片沿用参考版 VPN 卡片的 48 像素徽章、加粗标题、启用/禁用副标题及紧凑开关；启用时徽章为主题色六瓣形，禁用时为低强调圆形。卡片和首页编辑列表共用 [system_proxy_glyph.dart](flutter/lib/components/system_proxy_glyph.dart) 中的大陆轮廓地球字形，使用共享 Glyph 的线宽与圆角；全局出站模式保留参考版经纬线球体字形。
- 服务按钮的暂停图形表示停止服务，不新增暂停核心能力。远程会话、启停过渡和不可用操作必须反映真实状态。
- 仪表盘右上角恢复 FlClash 的 `AppGlyphs.edit` 铅笔，点击进入现有卡片编辑模式，完成后返回仪表盘。设置从底栏的 [设置页](flutter/lib/pages/settings.dart) 进入，依次显示外观、服务、内核、访问控制、备份与恢复和关于。服务与内核的具体选项各自保留在二级页面，不再经过“常规”；内核页不显示电量报告开关。
- 工具页依次显示连接与请求、日志、终端、网络诊断、远程控制及按能力显示的端点；网络诊断二级页显示网络质量与 STUN 测试，终端设置仍在终端内。工具页不再展示设置、关于或崩溃/内存/电量报告入口。配置相关入口统一在配置的“编辑”页，具体排列见 [配置页与内核差异](#配置页与内核差异)。
- 工具页以“连接与请求”作为连接和抓取记录的唯一入口，入口只显示标题，页面标题与入口一致，默认显示全部网络活动。顶部可筛选全部、活动、抓取记录及已结束；活动 HTTP 请求按记录是否完成判断，已结束包括完成的请求和关闭的连接。已完成请求不会把仍存活的 keep-alive 连接标为关闭：活动筛选中没有进行中请求代表它时，仍显示连接行。远程会话展示远端连接及管理操作，隐藏抓取筛选和本机操作，说明 HTTP 调试仅支持本地。
- 普通 TCP/UDP、未解密 HTTPS 以连接信息展示；已记录 HTTP 请求、TLS/TCP 流和仅域名记录分别成行。一条连接的多个 HTTP/1.1 请求或 HTTP/2 流保留独立行，以精确 `connectionId` 关联；当前筛选结果已有请求代表该连接时省略连接行，不以目标地址或旧抓包 ID 猜测关联。缺少父连接和旧版无关联记录仍可搜索、查看及导出。
- 搜索覆盖 URL、方法、状态、协议、应用、路由及来源/目标等已观测字段，支持可删除关键词；默认按创建时间排序，也保留速度、上传和下载排序。请求行显示自身正文计数，连接行显示核心连接流量及速率；请求速度未知，不复制父连接速率或相加统计。核心对全部保留历史筛选和排序，初始读取 50 条摘要，“显示更多”每次扩大 50 条窗口，轮询只传回窗口内变化的摘要。Flutter 与连接快照合并后展示前 N 行，未加载的匹配请求仍参与父连接去重；全历史搜索和导出不受窗口限制。连接数量沿用当前内核快照。
- 关闭录制仍可查看内核的近期连接元数据，HTTP 报文及解密流的保存由显式录制控制，MITM 由配置单独控制。抓取开关等待原生确认，停止抓取或停服后保留请求历史。空态说明从新 HTTP 请求/TCP 流开始记录；需要重新连接的范围及停止时的记录冻结语义由 [核心录制生命周期](libbox.md#httphttps-请求调试) 维护，中英文文案同步。
- 请求详情使用同一点击复制行及反馈，长 URL、头部与 Cookie 分行显示；右上角菜单切换请求、响应和 Cookies，请求与响应 Cookie 分组展示，保留重复 `Set-Cookie`、空值、值中的等号及属性，点击 Cookie 复制该条原文。
- 抓包设置只提供记录应用、原始目标端口、正文保留方式和可见域名提取。正文可选择“保存完整正文”，或填写每方向的正整数字节限制；既有自定义值原样保留，不限定为 KiB 预设。记录保留到清空，页面不显示固定磁盘总配额。保存检查本机修订，失败保留草稿。MITM 独立提供配置及证书设置，默认关闭；域名、排除项及协议选项遵循 [MITM 配置](libbox.md#独立-mitm-配置)。证书页显示有效期、公钥 SHA-256、用户/系统信任和私钥可用性，提供 CRT/系统模块导出与 Android 设置入口；打开页面不生成证书，生成是明确操作，重新生成需确认，返回前台可刷新信任。远程会话隔离与平台边界见 [app.md](app.md#请求抓取与-mitm)。
- 请求菜单提供排序、关闭全部活动连接、抓包设置、当前筛选结果的 HAR 导出和清空抓取记录。HAR 将当前筛选条件交给原生层，在打开文件选择器前固定所有匹配 HTTP 记录的 ID，包含尚未展开的分页；这些 ID 不传回 Flutter。普通连接、仅域名或 TLS/TCP 不作为 HTTP 导出。关闭连接、停止录制和清空抓取记录分别调用各自操作，清空不关闭连接。页面只显示录制状态；运行错误和脚本 console 输出进入应用现有日志，见 [libbox.md](libbox.md#httphttps-请求调试)。
- 请求详情保留重复头部、Cookie、正文状态及 trailers，显示出站/规则、应用及来源/目标地址，字段支持复制。已有记录中保存过的 PID/socket 字段仍可读，缺失值保持未知。域名观察与握手失败标为“仅域名”，不能导出 cURL；请求正文/头部不完整、截断或含 trailers 也不能导出 cURL。TLS/TCP 详情分别显示两个方向的原始字节，不显示 HTTP 状态、头部和 Cookies，禁用 cURL；正常完成的流显示“已完成”。页面复用现有共享组件，没有修改 FlClash 直接导入文件。
- 服务设置页使用即时应用的开关，保存和重启期间显示进度并禁用开关，底部没有“应用服务设置”按钮；启动确认及失败恢复见 [app.md](app.md#配置服务应用与备份)。
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

[captured_body.dart](flutter/lib/components/captured_body.dart) 将正文页放在等宽、自动折行、有界滚动的卡片中，展示内容类型、保存/总字节数及 Base64、截断、未完成状态。上一页/下一页按需调用原生正文读取，替换当前正文，不在 Dart 中持有完整响应或累计已读页面；页大小、UTF-8 边界和原始/解码字节语义见 [核心正文读取](libbox.md#httphttps-请求调试)。仍有下一页不显示截断警告；复制按钮明确为“复制本页正文”，使用当前页原文，不受格式化切换影响。加载失败保留当前页，切换会话后丢弃迟到结果。

单页内完整 JSON 可切换格式化与原文；[http_message.dart](flutter/lib/common/http_message.dart) 仅调整字符串外的空白，保留数值字面量、重复键及转义。正文页格式化在生成缩进前检查输出长度，超过 64 KiB 的展示窗口或内容无效时保留原文；这只约束派生显示文本，不截断保存内容或导出。

### 配置页与内核差异

配置页顶部仅显示可横向滑动的两行卡片，下方为当前内核的代理分组；配置文件滚动和分组切换使用各自的控制器，滑动卡片不选择配置或切换底栏。卡片保留参考版固定行高、选中徽标和更新动画，进入页面及已选配置变化时定位当前选择。切换配置的弹层仍使用最多三列的自适应网格，并从顶部打开。远程会话顶部显示远程配置摘要，下方使用远端分组，隐藏本机配置卡片及管理操作。

页面右上角依次为搜索、添加、更新全部和收纳菜单；配置卡片外不再显示“配置文件”标题及独立管理按钮行。添加按钮和空状态按钮均打开菜单，新建入口标为“新建配置”。配置文件排序和节点排序并列放在收纳菜单中，测速及节点定位沿用工具栏收纳逻辑；配置文件不足两个时禁用排序，没有远程订阅或正在更新时禁用更新全部。配置排序使用分组背景、拖动手柄及拖动浮起效果。长按菜单依次提供编辑、更新（远程配置）、复制链接和二维码（远程配置）、分享、导出及删除，删除标为危险操作。当前选中配置的删除项禁用，停服时也需先切换到其他配置；配置页和切换弹层均在打开确认框前及确认后复核最新选择，原生保护见 [app.md](app.md#配置服务应用与备份)。编辑页显示配置摘要，提供元数据分组表单、分项配置和原始 JSON 编辑入口。

服务启动中或停止中，配置列表和已打开的切换弹层禁用配置选择，首页切换配置按钮同步禁用；服务进入运行或停止状态后恢复。原生状态复核见 [app.md](app.md#配置服务应用与备份)。

| 参考版能力 | B-box 的适配边界 |
| --- | --- |
| 链接、文件、二维码导入 | 使用 sing-box JSON / 分享链接，保留 `.bpf`、动态 QRS 和剪贴板入口；不转换 Clash YAML |
| 自定义配置、提供者与覆写 | “新建配置”创建本地 sing-box JSON；通过原始 JSON 及分项配置编辑器修改，不引入 Mihomo 配置生成、提供者或覆写脚本 |
| 配置预览与文件编辑 | 从编辑页的“打开配置内容”入口读取保存的 JSON，不将其称为运行时配置 |
| 自动更新 | 保留 Android 后端至少 15 分钟的限制；关闭后隐藏间隔输入，无效草稿不阻止关闭，保留原有效间隔及导入元数据；批量更新按原生互斥写入流程串行执行 |
| 流量用量与到期徽标 | 当前 Room/桥接没有订阅额度和到期字段，卡片显示真实本地/远程类型，不用会话流量代替额度；这是当前元数据能力的差异，不表示 sing-box 无法支持订阅信息展示 |
| 上次更新时间 | 首页和配置列表均显示 Room 时间戳，采用参考版紧凑相对时间并提供完整时间提示；首页显示“更新于 …”，点击时间显示提示，配置列表保留原交互。本地配置显示创建/内容保存时间，远程配置显示订阅更新时间。没有历史记录时显示未知，时间记录和失败恢复见 [app.md](app.md#配置服务应用与备份) |
| 分享与导出 | 复制链接得到原始订阅 URL，分享/二维码保留 sing-box 导入链接和名称，导出仍为 JSON |

编辑保留 B-box 已有的本地/远程类型切换；不会因视觉对齐加入参考版的 age 密钥等未接入能力。保存、修订冲突、配置应用及失败恢复继续由既有桥接处理，见 [app.md](app.md#配置服务应用与备份)。

Cloudflare WARP MASQUE 通过“网络 → 出站”的 JSON 表单或原始 JSON 编辑器配置；字段及导入示例见 [核心文档](libbox.md#cloudflare-warp-masque)。配置保存沿用上述修订检查及 Android 校验流程。

原始 JSON 编辑器使用 [code_editor.dart](flutter/lib/components/code_editor.dart) 显示从 1 开始的逻辑行号，空行和末尾换行保留编号；长行折行不增加行号，行号随正文滚动。编辑器只保存逻辑行起始偏移，在同一帧先布局正文，再复用 `RenderEditable` 的排版及未取整选区端点计算可见行号，输入、替换内容和调整尺寸时无需等待下一帧，也不为行号排版第二份完整文本。正文滚动及视口尺寸变化后同步选区端点，使手柄显示位置与拖动计算使用同一组正文坐标，避免按旧位置选中其他行；搜索、格式化、撤销及修订校验沿用原编辑流程。

[JsonEditorController](flutter/lib/pages/profile_editor.dart) 只缓存当前内容、样式和配色的高亮，纯选区变化复用结果；输入法组合输入及超过既有 150000 字符阈值时使用普通文本并释放高亮缓存。原始编辑器和分项 JSON 表单不因选区变化重建整页。`ProfileConfigStore.set` 只复制修改路径上的 Map，序列化读取其他分支；失败时原配置和修订保持不变。

选区同步补偿 [固定 Flutter SDK](.github/actions/setup-flutter/action.yml) 的 Android 滚动处理，通过已有的 `EditableTextState.selectionOverlay` 取得选择覆盖层并刷新端点。该 getter 标为测试可见，调用处仅对它豁免可见性分析；SDK 升级时需核对这个入口及滚动后的手柄回归。

分项配置的 JSON 表单使用固定 16 dp 圆角、正文顶部对齐的多行编辑框。[BoxField](flutter/lib/widgets.dart) 将圆角传给 Prism 的填充输入主题，使背景及聚焦边框使用同一轮廓，避免多行编辑区域随高度变成胶囊形。

配置“编辑”页依次直接提供“日志”、“网络”、“DNS”、“路由规则”、“规则集”、“MITM”、“HTTP 改写”和“脚本”，末尾单独保留一个“打开配置内容”入口，元数据表单仅保留元数据字段和更新时间。各分项打开时读取该配置的最新内容，由 [configuration.dart](flutter/lib/pages/configuration.dart) 复用共享分组、规则列表、排序及 JSON 表单；原始 JSON 入口独立于分项加载。内网详情的“编辑”入口复用同一配置编辑页。

MITM 页面首先提供单一证书入口，再显示 HTTPS、域名、HTTP/2、服务器证书校验、“解密TCP”和 QUIC 回退设置；证书入口独立于配置加载。页面顶部不显示配置名或解密说明，TCP 和 QUIC 开关仅显示标题。域名入口只显示标题，完整域名及排除项在二级编辑器查看和修改，保留输入顺序及通配符、端口后缀，新增抓取域名追加在列表末尾。端口、应用等额外筛选通过同页 JSON 编辑入口修改，见 [MITM 配置](libbox.md#独立-mitm-配置)。HTTP 改写页面依次显示 URL Rewrite、Header Rewrite、Body Rewrite（含 jq）、Map Local / API Mock、Map Remote，下方提供资源及完整 JSON 编辑入口；脚本独立编辑启用状态、HTTP、Cron 和资源。脚本及改写资源页提供“导入本地资源”、已有资源的 JSON 编辑及整个资源字典编辑；导入期间禁用重复提交，取消保留原配置，成功后显示可供规则引用的资源名，失败显示原因。文件位置、修订检查和备份边界见 [Android 导入流程](app.md#配置处理生命周期)。前后查找/反向引用、URL 资源及独立正文限额通过完整 JSON 表单配置，字段及运行行为见 [libbox.md](libbox.md#json-配置中的-http-改写)。新建规则/任务示例默认禁用。

字段修改通过 ProfileConfigStore.set，本地资源导入通过独立桥接；二者均保留其他字段、修订及原生校验/失败恢复；关闭失败表单前保留草稿。CA 不写入配置，活跃 MITM 或手动记录时禁用重新生成，独立脚本/改写不占用证书。映射请求详情继续把连接出站/规则标为原连接。

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

[newui.yml](.github/workflows/newui.yml) 在各分支代码推送时自动运行，统一包含核心、Flutter、JVM 和 Android smoke；[setup-flutter](.github/actions/setup-flutter/action.yml) 固定 SDK 和依赖锁文件。Flutter job 先运行 [verify_newui.py](tools/verify_newui.py) 核验 FlClash/prism_ui 文件哈希、许可、适配目标和退役依赖等源码约束，再依次执行 `flutter analyze --no-fatal-infos`、`flutter test --reporter expanded` 和 `flutter build aar --no-profile --target-platform android-arm64`。截图和像素比较由同一测试入口生成，截图及失败对比图上传为 `newui-validation`，验证用 AAR 上传为 `flutter-aar-arm64`。手动 Release 构建独立准备所需的 Flutter Release AAR，不依赖测试运行，见 [构建与发布](app.md#构建与发布)。

### 测试文件覆盖

下列测试使用桥接替身和测试数据，验证交互、通知、调用和几何断言。它们不代替实际 TLS、系统证书安装、第三方应用或实机视觉验收；实际 PSS/RSS、CPU、帧耗时和加载耗时仍需在同设备、同配置下测量。原生服务重启、配置重载和内核日志级别由 [Android 仪器测试](app.md#android-仪器测试) 覆盖。

**导航、外观与关于**

- [navigation_test.dart](flutter/test/navigation_test.dart)：固定导航在 320 宽、底部安全区、浅色/深色/纯黑及大字号下的高度、底色、页面边界、四个目的地顺序、标签切换和选中语义，并输出固定栏截图。窄屏实际横向滑动配置文件并核对底栏及选择不变，输出合并页的滑动前后截图。设置从底栏进入，二级页面返回设置；宽屏通过侧栏实际按钮切页，并在截图前核对当前页。外观与 MITM 页直接检查工具栏操作区域像素，防止隐藏首页的按钮残留；复制反馈和保存失败回归另检查顶部按钮仍可点击。另输出配置编辑与排序弹层在窄屏、大字号和宽屏下的截图，这些是布局回归产物，配置页尚未声明与上游逐像素一致。
- [appearance_test.dart](flutter/test/appearance_test.dart)：主题选择与重开页面后的偏好，旧字体偏好不覆盖默认字体，系统字号运行时变化和既有缩放上下限。导航、状态、内网 IP、出站模式及请求页面的字号场景通过系统缩放模拟，不依赖已退役的应用偏好。
- [licenses_test.dart](flutter/test/licenses_test.dart)：异步加载、共享许可按包分组、搜索及完整可选中正文。
- [about_test.dart](flutter/test/about_test.dart)：版本检查完整/未完成与可升级/不可升级的四种组合，等待时禁止重复请求、未完成提示、APK 下载条件和发布页面入口。
- [dashboard_editor_test.dart](flutter/test/dashboard_editor_test.dart)、[outbound_mode_test.dart](flutter/test/outbound_mode_test.dart) 和 [terminal_settings_test.dart](flutter/test/terminal_settings_test.dart)：铅笔点击及卡片长按进入首页原位编辑，保留卡片状态并检查排序、移除、添加及保存失败；出站模式图形与 Android 一致并保留内核顺序和当前选择；停服时从终端页打开 Ghostty 设置并保存原生配置。

**配置、编辑与备份**

- [profiles_test.dart](flutter/test/profiles_test.dart)：新建与二进制导入的内容/元数据、添加菜单与取消导入、菜单能力、原始订阅链接、相对时间边界、本地时间的未知占位及原生元数据事件刷新、自动更新校验、选中配置定位；横向滑动不切页或选择配置，卡片与列表两种代理样式仍能测速并通过原生确认切换配置。列表及弹层在启停期间禁止切换与状态稳定后恢复、切换失败、通过右上角收纳菜单进入配置排序后的冲突恢复，以及远程分组选择及本机配置隔离继续覆盖。
- [configuration_test.dart](flutter/test/configuration_test.dart)：MITM、HTTP 改写、脚本三种配置及 Cron 草稿通过独立路径保存，HTTP/2、证书校验及 QUIC 显式关闭选项正确保存，关闭改写保留 MITM/脚本，校验失败保留内容，原始编辑按修订写入内容且不改动配置元数据。
- [code_editor_test.dart](flutter/test/code_editor_test.dart)：加载应用实际的等宽字体，对照 `RenderEditable` 检查行号在折行、宽度/字号变化及滚动后的对齐，覆盖空行、末尾换行和替换内容。选区用例在 0.8、1.0、1.4 倍字号下实际拖动两端手柄，检查跨行选择、折行文档滚动后的定位，以及通过复制菜单取得的原文范围。
- [backup_test.dart](flutter/test/backup_test.dart)：取消本地选择器不报告成功也不恢复数据，导出使用紧凑 JSON；恢复传递范围与策略，失败后可重试；WebDAV 上传等待服务器确认且不打开本地选择器；WebDAV 表单校验目录 URL，保存失败和取消关闭后保留长输入。

**代理、状态与出口查询**

- [proxies_test.dart](flutter/test/proxies_test.dart) 与 [proxy_nodes_test.dart](flutter/test/proxy_nodes_test.dart)：自动组禁止手选、延迟测试目标、分组切换、列表折叠及视口外节点定位；节点筛选复用未变化的结果，超时、改名、相同延迟重排和移除使排序结果失效。
- [status_panel_test.dart](flutter/test/status_panel_test.dart)：状态卡片与出口查询的刷新和会话约定，并将启动按钮的独立无障碍点击入口与配置可用性、实际桥接调用一起验证。
- [status_panel_parity_test.dart](flutter/test/status_panel_parity_test.dart)：与冻结的 [flclash_status_reference.dart](flutter/test/support/flclash_status_reference.dart) 比较像素，覆盖中英文、深浅主题、0.8/1.0/1.4 字号比例、IPv6、查询等待/失败、组织/ASN 回退及质量等级；停止按钮另行比较，停服状态包含无配置首次启动和设备网络查询的成功/等待/失败情况。参考渲染器独立保留原版标题、chips、徽章、形变、卡片表面和排版，不导入 B-box 的适配仪表盘组件；比较使用相同视口、字号、语言、主题及 Roboto/Noto Sans SC 字体，按卡片自然高度截图并断言高度，双方图像上传为 CI Artifact。
- [ip_quality_test.dart](flutter/test/ip_quality_test.dart) 与 [ip_quality_cache_test.dart](flutter/test/ip_quality_cache_test.dart)：各来源的类型和标志解析、查询目标地址、不增加固定期限、用户取消不启动后续批次、首个声明类型的来源胜出并取消其余、前序失败或静默时进入后续批次、全部应答后回退推断类型及失败原因；成功结果的内存共享与按 IP 持久化、三天到期边界、无效记录忽略、失败五分钟内存冷却且不持久化、手动重试和刷新跳过缓存、缓存读写失败不丢弃成功结果、取消不返回旧数据也不产生冷却，以及同一 IP 的并发查询共享结果。
- [intranet_panel_test.dart](flutter/test/intranet_panel_test.dart)：大字号下复制按钮的尺寸与位置，以及运行/停服/远程会话的复制内容。

**服务设置与日志**

- [service_settings_test.dart](flutter/test/service_settings_test.dart) 与 [box_test.dart](flutter/test/box_test.dart)：三个服务开关的即时应用、等待原生确认期间禁用开关及移除手动应用按钮；重复编辑、停止/远程会话、启停过渡及失败后的原生设置恢复。
- [logs_test.dart](flutter/test/logs_test.dart)：本地/远程日志状态的默认阈值、保留更严重的记录、手动筛选及恢复跟随配置，以及暂停、搜索、导出和清空。

**数据订阅、刷新与缓存**

- [state_updates_test.dart](flutter/test/state_updates_test.dart)：高频数据只重建对应订阅者、服务状态仍通知、应用根部不随遥测/工具进度变化重建且外观设置继续生效，并覆盖日志增量淘汰、暂停快照、连接增量对象复用/重置及启动响应不覆盖早到事件。
- [data_subscription_test.dart](flutter/test/data_subscription_test.dart)：各类数据的卡片/详情共享订阅，以及隐藏页面、TickerMode、前后台和销毁时的订阅切换。
- [connection_list_test.dart](flutter/test/connection_list_test.dart) 与 [network_activity_test.dart](flutter/test/network_activity_test.dart)：搜索、过滤、排序、增量复用、一对多关联、无关联/淘汰历史、分页前去重及连接与请求计量隔离。
- [connections_test.dart](flutter/test/connections_test.dart)、[capture_poll_test.dart](flutter/test/capture_poll_test.dart) 和 [capture_page_test.dart](flutter/test/capture_page_test.dart)：统一请求页关闭后释放连接订阅并停止抓取轮询；完成详情停轮询、手动刷新、前台恢复和同帧会话切换；查询增量复用已保留的摘要并释放窗口外记录，畸形增量页被拒绝且不产生空历史。
- [app_icon_cache_test.dart](flutter/test/app_icon_cache_test.dart)：图标共享在途加载并按最近最少使用淘汰，未命中和超大图标不使缓存无界增长，加载期间清空不会重新填充或覆盖新条目。
- [access_test.dart](flutter/test/access_test.dart)：进入托管页面不扫描、加载期间接收最新托管选择、显示范围与隐藏选择保留、保存失败后重试、图标复用、扫描等待/成功/失败及查询设置返回后的刷新。

**请求与 MITM**

- [requests_test.dart](flutter/test/requests_test.dart)：统一工具入口、混合列表和活动筛选、关联详情、底层连接关闭确认、历史保留、同名远端的会话隔离、HAR 选中 ID、本机设置修订、旧记录设置清理、无改写横幅/抓包日志入口、端口、启动确认及后台轮询/迟到结果。MITM 域名覆盖空域名、有序域名/排除项选择；证书测试检查无配置时从抓包设置经 MITM 页进入证书、明确生成、取消导出、活跃 MITM 禁止重生成、远程切换和信任刷新。其他回归包括停止抓取时的历史分页与搜索重置、TCP 详情及流完成/导出限制、既有 socket 元数据、报文菜单、字段/Cookie 复制、正文格式及 Unicode 分页、完整正文/任意字节限制和超过旧条数范围的搜索导出；历史列表检查“显示更多”可实际点击，正文检查原生翻页、页内复制、失败重试及会话切换。资源页另检查导入、取消、修订失败和远程会话禁用。详情的滚动查找限定在当前详情页内，目标尚未由列表构建时先滚动，不对空查找调用 `.last`，以保留关联请求、连接过期及滚动后标题边界的实际断言。
- 滚动边界：配置页面和共享表单将滚动视口放在共享标题下方，测试核对配置页 320 宽、1.3 倍字号深色主题，以及抓包设置 360 宽浅色/1.4 倍深色、800 宽浅色的滚动前后几何位置。窄屏、大字号深色及宽屏测试运行后将截图写入 `build/screenshots/requests/`，包含独立配置（如 `independent-mitm-config.png`、`independent-scripts-cron.png`）、请求设置、证书、统一列表、关联记录、分页按钮及报文详情。
- [http_message_test.dart](flutter/test/http_message_test.dart)：JSON 数值/重复键/转义保真、无效内容回退、正文页缩进输出过大时回退原文，以及重复 Cookie、空值、等号和 Expires 逗号。请求正文不完整或截断时不提供 cURL 导出。

### 当前验证记录

2026-11-14，配置卡片顶部精简及工具栏收纳调整的验证源码为 `2be09f60fcf6244f9f30c7899c5da382359701e0`，对应 [Actions 43570946186](https://github.com/giturass/B-box/actions/runs/43570946186) 的 [Flutter 作业](https://github.com/giturass/B-box/actions/runs/43570946186/job/132374987419)。Flutter 分析、259 项交互/布局测试及 ARM64 Debug/Release Flutter AAR 构建通过；分析包含 84 条 info、0 条 warning/error。更新后的配置排序用例从右上角收纳菜单进入，确认菜单同时提供节点排序，并继续检查原生修订冲突后的草稿与重试；分组测速用例从收纳菜单执行当前分组操作，列表样式保留分组内按钮。

本次实际查看 [newui-validation](https://github.com/giturass/B-box/actions/runs/43570946186/artifacts/14104274398) 的以下截图，路径相对 `build/screenshots/`：

| 截图 | 核对结果 |
| --- | --- |
| `360-light-1.0-Profiles-populated.png` | 配置卡片外的标题和独立按钮行已移除，右上角按搜索、添加、更新、收纳排列；下方代理组及选中状态保留 |
| `360-dark-1.4-Profiles-populated.png` | 大字号深色主题下四个顶部按钮均可辨识，配置卡片和代理卡片未重叠，长文案按既有规则省略 |
| `360-dark-1.4-Profiles-scrolled.png` | 横向滑动后本地配置进入视口，四个顶部按钮和下方分组保持位置，底栏仍选中配置；卡片随滚动视口裁切 |
| `navigation/fixed-black.png` | 320 宽纯黑主题及大字号下四个顶部按钮完整显示，无远程订阅时更新按钮禁用；长英文页面标题及底栏文案省略，固定底栏和停服提示各自保留区域 |
| `800-light-1.0-Profiles-populated.png` | 宽屏使用相同的四个顶部按钮顺序，配置卡片直接排列在分组上方，侧栏保留四个目的地 |

另查看 [API 24 smoke 产物](https://github.com/giturass/B-box/actions/runs/43570946186/artifacts/14105061141) 的 `04-core-proxies.png`，确认实际应用中的导入配置和内核分组同页，配置卡片外无独立标题行，右上角四个按钮按预期排列。

本机已通过 `git diff --check`、`python3 tools/verify_newui.py` 和文档链接/锚点检查。上述 Flutter 截图使用测试桥接数据，Android 截图来自临时 x86_64 模拟器，均属于抽样检查；没有进行全应用逐像素验收或 ARM64 实机视觉、性能及真实远程会话验收。四组 Android smoke 全部通过，产物核对及核心/JVM 覆盖范围见 [应用验证记录](app.md#当前验证记录)。

### 历史验证记录

下表索引此前记录过的 Flutter 运行，只用于查询，不是当前源码的通过声明；各次均包含 Flutter 分析、交互/布局测试和 ARM64 Debug/Release Flutter AAR 构建，分析结果无 warning/error。截图路径相对各次 `newui-validation` 的 `build/screenshots/`，逐张的核对结论和未覆盖范围保留在 Git 历史，可用 `git show 06eed8f5f830a90b9853995ced3b2e211deca628:flclash.md` 查看整理前的完整记录。对应的核心与 Android 结果见 [app.md](app.md#历史验证记录)。

| 日期 | 主题 | 验证源码 | Actions | 测试 / info | 当时实际查看的截图 |
| --- | --- | --- | --- | --- | --- |
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
