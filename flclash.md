# FlClash UI 使用情况

本文记录界面来源、复用和适配范围。Android 后端差异见 [app.md](app.md)，核心扩展见 [libbox.md](libbox.md)，维护约束见 [AGENTS.md](AGENTS.md)。

## 参考版本与来源清单

固定参考 [chen08209/FlClash v0.9.2](https://github.com/chen08209/FlClash/tree/7fd417fb9a9a277c6a9339a8ac87f907df7c25f7)，提交 `7fd417fb9a9a277c6a9339a8ac87f907df7c25f7`（2026-10-24）。当前使用实际 Flutter 组件，历史 Compose 仿制按钮和卡片不再定义界面。

[flutter/flclash-manifest.json](flutter/flclash-manifest.json) 是逐文件来源清单：

| 清单字段 | 使用方式 |
| --- | --- |
| `files` | 直接导入的共享组件、两个工具文件、字体和 glass shader；哈希对应将 Dart 包引用从 `fl_clash` 机械改名为 `b_box_ui` 后的文件 |
| `adaptedFiles` | 仪表盘、配置/代理、外观、备份、IP 质量查询及参考测试等适配文件；记录原始来源哈希和实际改动说明 |
| `adaptedNativeFiles` | `libbox/ui_egress.go` 对 FlClash IP 查询的适配来源；用 sing-box 路由和 libbox HTTP 替换原传输 |
| `supplementalReferences` | 独立于整体 UI 基线的 MITM dev 补充参考，按原样导入、改写或设计参考记录源文件哈希；本地路径相对仓库根目录 |

清单中 `files` 和 `adaptedFiles` 的本地路径相对 `flutter/`，`adaptedNativeFiles` 的本地路径相对仓库根目录；`source`/`sources` 指向固定参考提交内的上游路径。清单记录逐文件哈希和改动来源，本文维护使用说明，两者一并保留。

本次请求/MITM 功能补充核对 FlClash `dev` 的 [`63aa150e23288558074d877f745a6d3b5f6418f6`](https://github.com/chen08209/FlClash/tree/63aa150e23288558074d877f745a6d3b5f6418f6)，相关提交为 [`f54486102172566e7342d7f5c138663c3c5dec0b`](https://github.com/chen08209/FlClash/commit/f54486102172566e7342d7f5c138663c3c5dec0b) 的 MITM 应用侧设计和 [`1a7556d30a38707f39abfba4b7d6a6b8f57774ed`](https://github.com/chen08209/FlClash/commit/1a7556d30a38707f39abfba4b7d6a6b8f57774ed) 的请求/连接整合。该 dev 源码的 `FEATURE_MITM` 默认关闭，公开 core 不提供完整 MITM 实现；B-box 自行实现 sing-box 路由上的 TLS 接管，不能把上游页面当成可用核心。此次采用证书信任、报文展示和 CA 模块设计，连接与请求按 B-box 的数据边界合并显示，不整体迁移该版本的 UI 依赖。整体 FlClash UI、Android 上游和核心三个基线均不变。

共享组件位于 [flutter/lib/ui](flutter/lib/ui)。原始许可保留在 [flutter/assets/licenses/LICENSE](flutter/assets/licenses/LICENSE)，字体归属见 [JetBrainsMono-OFL.txt](flutter/assets/licenses/JetBrainsMono-OFL.txt) 和 [Twemoji-LICENSE.md](flutter/assets/licenses/Twemoji-LICENSE.md)。Twemoji 原始图形来自 Twitter，字体转换由 Mozilla 提供，图形未修改。这些文件同时用于应用内许可证页，不再单独保存重复副本。

## 界面复用与业务适配

Flutter 承担仪表盘、代理、配置、工具及所有子页面的导航。复用 FlClash 的控件、字形、形状、排版、字体、动效、弹层及导航；页面状态和操作经 Android 桥接接入 B-box 的 Room 与 libbox。没有引入 FlClash/Mihomo 原生库、Clash 配置模型、Mihomo 配置覆写脚本、订阅转换器或 Clash 专用服务。B-box 请求/响应脚本与定时任务的范围见 [独立脚本与 Cron](libbox.md#独立脚本与-cron)。

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
| 连接与请求 | [connections.dart](flutter/lib/pages/connections.dart)、[requests.dart](flutter/lib/pages/requests.dart)、[request_settings.dart](flutter/lib/pages/request_settings.dart)、[mitm_certificate.dart](flutter/lib/pages/mitm_certificate.dart)、[capture_domains.dart](flutter/lib/pages/capture_domains.dart) | 连接与请求合并列表、各自详情、本机筛选、独立 MITM 与证书、TCP 流、域名选择及 HAR/cURL 导出；复用共享列表、表单和导航 |

### 导航与交互约定

- 首次启动直接进入仪表盘，没有免责声明接受门槛。Android 默认显示状态、网络速度、出站模式、配置和代理组五张卡；内存与内网 IP 可选。连接与请求从工具页进入，不提供首页连接卡，旧布局/备份忽略该卡，下一次布局保存清理其键。
- 手机可选固定或悬浮导航栏。固定样式由 [main.dart](flutter/lib/main.dart) 中的 `Scaffold.bottomNavigationBar` 承载，栏体高度为 64 dp，仅追加系统底部安全区；栏体及底部安全区使用主界面背景色，无阴影和表面叠色，随浅色、深色及纯黑主题同步。外观页预览使用相同底色和紧凑比例。
- 出站模式只显示内核提供的值。规则、全局、直连使用参考版字形，名称和选中态忽略大小写，排列和切换参数保留内核原值。系统 HTTP 代理卡片按内核能力显示，支持首页编辑中的排序、移除和重新添加；能力暂不可用时保留顺序和隐藏偏好。卡片沿用参考版 VPN 卡片的 48 像素徽章、加粗标题、启用/禁用副标题及紧凑开关；启用时徽章为主题色六瓣形，禁用时为低强调圆形。卡片和首页编辑列表共用 [system_proxy_glyph.dart](flutter/lib/components/system_proxy_glyph.dart) 中的大陆轮廓地球字形，使用共享 Glyph 的线宽与圆角；全局出站模式保留参考版经纬线球体字形。
- 服务按钮的暂停图形表示停止服务，不新增暂停核心能力。远程会话、启停过渡和不可用操作必须反映真实状态。
- 工具页远程控制紧跟终端；终端设置在终端内，仪表盘编辑在首页工具栏。设置依次为外观、常规、访问控制、备份与恢复；高级配置从配置的“编辑”页进入，不保留重复资源入口。
- 工具页统一提供“连接与请求”入口，页面标题与入口一致。默认“全部”视图在抓取关闭时显示内核返回的活动及已关闭连接；开启后在连接列表尾部追加抓到的请求，停止后恢复只显示连接。两类记录使用同一套卡片、边距及字形，以“连接”/“请求”标记区分并打开各自详情；键包含记录来源，不把同名 ID 当作同一条数据。连接保留速度、创建时间及上下行总量排序，请求保持从新到旧且每批加载 50 条，最多显示内核保留的 300 条。
- “显示记录”菜单提供全部、连接、活动连接、已关闭连接和请求筛选。请求筛选及“查看已抓取的请求”入口可在抓取停止或停服后查看历史；同一搜索框分别筛选连接与请求，HAR 导出沿用请求搜索条件。关闭单条/全部连接与清空请求各自作用于原数据来源。抓取开关等待原生确认，失败保留已确认状态；远程会话继续展示远端连接，隐藏本机抓取控件和请求，并丢弃切换前迟到的本机查询结果。
- 请求详情使用同一点击复制行及反馈，长 URL、头部与 Cookie 分行显示；右上角菜单切换请求、响应和 Cookies，请求与响应 Cookie 分组展示，保留重复 `Set-Cookie`、空值、值中的等号及属性，点击 Cookie 复制该条原文。
- 抓包设置只提供记录应用、原始目标端口、正文限额和可见域名提取，保存检查本机修订，失败保留草稿。MITM 独立提供配置及证书设置，默认关闭，只解密配置的域名；空列表不解密，无排除列表，可开启 TCP 解密及 QUIC 回退。证书页显示有效期、公钥 SHA-256、用户/系统信任和私钥可用性，提供 CRT/系统模块导出与 Android 设置入口；打开页面不生成证书，生成是明确操作，重新生成需确认，返回前台可刷新信任。远程会话隔离与平台边界见 [app.md](app.md#请求抓取与-mitm)。
- 合并页菜单保留抓包设置、当前搜索的 HAR 导出和清空请求；抓取开关只显示请求录制状态，没有“配置中的 HTTP 改写已生效”提示及抓包日志功能。运行错误和脚本 console 输出进入应用现有日志，见 [libbox.md](libbox.md#httphttps-请求调试)。
- 请求详情保留重复头部、Cookie、正文状态及 trailers，显示原路由/应用、真实 SNI、双方 TLS 版本、ALPN 和上游证书 SHA-256，字段支持复制。已有记录中保存过的 PID/socket 字段仍可读，缺失值保持未知。域名观察与握手失败标为“仅域名”，不能导出 cURL；请求正文/头部不完整、截断或含 trailers 也不能导出 cURL。TLS/TCP 详情分别显示两个方向的原始字节，不显示 HTTP 状态、头部和 Cookies，禁用 cURL；正常完成的流显示“已完成”。页面复用现有共享组件，没有修改 FlClash 直接导入文件。
- 常规服务页使用即时应用的开关，保存和重启期间显示进度并禁用开关，底部没有“应用服务设置”按钮；启动确认及失败恢复见 [app.md](app.md#配置服务应用与备份)。
- 日志页默认选择“跟随配置”，显示内核上报的日志级别及更严重的记录；菜单同时显示当前级别，仍可手动选择“全部”或单一级别，手动筛选不会被后续状态更新覆盖。搜索、暂停和导出沿用同一筛选结果；默认级别尚未收到时保留已接收记录。日志来源及本地重载同步见 [app.md](app.md#配置服务应用与备份)。
- 应用始终跟随系统语言并响应运行时变化。旧设置及备份中的语言选项丢弃，其他偏好保留。
- 代理列表保留圆角吸顶分组标题、固定高度的懒加载行、折叠动效及填充/普通/隐藏分组图标。折叠状态按选中配置保存，定位节点通过行偏移实现，不要求节点预先进入视口。
- 访问控制按上游 SFA 的列表/扫描分离方式加载，进入页面直接显示应用与已保存的托管选择；“重新扫描托管应用”为独立操作，扫描期间保留列表。筛选/搜索/排序结果在列表及相关条件未变化时复用，流量等无关状态更新不重新排序。图标保持按可见行请求和缓存，切换显示范围或无修改地关闭查询设置不清空图标；查询策略变更或申请权限后关闭设置只刷新一次。原生查询与托管更新约定见 [app.md](app.md#配置服务应用与备份)。
- 首页编辑保留卡片的全局 key，防止重复出口请求及丢失状态。改动立即保存；保存失败保留草稿，点击完成再次尝试。
- sing-box 专属 Tailscale、Taildrop、终端、认证、USB/IP、诊断和报告使用统一 Flutter 页面，复用原生后端；能力边界见 [app.md](app.md)。

[box.dart](flutter/lib/box.dart) 按应用状态、启动/外观、流量、内存、连接和日志分开通知；流量采样与仪表盘累计值只更新相关组件，日志和连接变化不触发应用根部、代理或配置列表重建。出口查询监听应用状态变化，不因流量或内存采样重新计算路由标识。显式批量通知仍刷新全部依赖。

[data_subscription.dart](flutter/lib/components/data_subscription.dart) 按页面活动状态、TickerMode 和应用生命周期合并内存/日志订阅；卡片与详情页共享一个原生采样请求，最后一个可见消费者退出时停止。日志增量保留既有记录对象，暂停快照独立于后续追加和淘汰，恢复可见时重新同步原生缓冲。原生采样与消息边界见 [app.md](app.md#配置服务应用与备份)。

[capture_poll.dart](flutter/lib/components/capture_poll.dart) 仅在本地“连接与请求”列表/请求详情活动、TickerMode 开启且应用前台时每两秒查询；连接仍使用原生事件订阅，连接更新不会另发抓取查询。调用不重叠，筛选变化合并刷新，忽略旧筛选或会话的迟到结果。隐藏页面停止轮询，后台录制继续随服务运行。页面为 B-box 业务代码，没有修改 FlClash 直接导入文件。

[captured_body.dart](flutter/lib/components/captured_body.dart) 将正文预览放在等宽、自动折行、有界滚动的卡片中，展示内容类型、保存/总字节数及 Base64、截断、未完成状态。完整 JSON 可切换格式化与原文；[http_message.dart](flutter/lib/common/http_message.dart) 仅调整 JSON 字符串外的空白，保留数值字面量、重复键及转义，无法解析或超过格式化限额时显示原文。长预览分批展开，边界不拆开 UTF-16 代理对；复制始终使用内核返回的原始预览，不受当前格式或已展开长度影响，也不代表未保存的完整正文。

### 配置页与内核差异

配置列表按参考版使用两行卡片、固定行高、选中徽标、更新动画及最多三列的自适应网格。普通配置页首次显示时定位已选配置，切换配置的弹层从顶部打开。添加按钮和空状态按钮均打开菜单，新建入口标为“新建配置”；排序使用分组背景、拖动手柄及拖动浮起效果。长按菜单依次提供编辑、更新（远程配置）、复制链接和二维码（远程配置）、分享、导出及删除，删除标为危险操作。编辑页显示配置摘要，提供元数据分组表单及原始/高级配置入口。

服务启动中或停止中，配置列表和已打开的切换弹层禁用配置选择，首页切换配置按钮同步禁用；服务进入运行或停止状态后恢复。原生状态复核见 [app.md](app.md#配置服务应用与备份)。

| 参考版能力 | B-box 的适配边界 |
| --- | --- |
| 链接、文件、二维码导入 | 使用 sing-box JSON / 分享链接，保留 `.bpf`、动态 QRS 和剪贴板入口；不转换 Clash YAML |
| 自定义配置、提供者与覆写 | “新建配置”创建本地 sing-box JSON；通过已有原始/高级配置编辑器修改，不引入 Mihomo 配置生成、提供者或覆写脚本 |
| 配置预览与文件编辑 | 从编辑页的“打开配置内容”入口读取保存的 JSON，不将其称为运行时配置 |
| 自动更新 | 保留 Android 后端至少 15 分钟的限制；关闭后隐藏间隔输入，无效草稿不阻止关闭，保留原有效间隔及导入元数据；批量更新按原生互斥写入流程串行执行 |
| 流量用量与到期徽标 | 当前 Room/桥接没有订阅额度和到期字段，卡片显示真实本地/远程类型，不用会话流量代替额度；这是当前元数据能力的差异，不表示 sing-box 无法支持订阅信息展示 |
| 上次更新时间 | 首页和配置列表均显示 Room 时间戳，采用参考版紧凑相对时间并提供完整时间提示；本地配置显示创建/内容保存时间，远程配置显示订阅更新时间。没有历史记录时显示未知，时间记录和失败恢复见 [app.md](app.md#配置服务应用与备份) |
| 分享与导出 | 复制链接得到原始订阅 URL，分享/二维码保留 sing-box 导入链接和名称，导出仍为 JSON |

编辑保留 B-box 已有的本地/远程类型切换；不会因视觉对齐加入参考版的 age 密钥等未接入能力。保存、修订冲突、配置应用及失败恢复继续由既有桥接处理，见 [app.md](app.md#配置服务应用与备份)。

原始 JSON 编辑器使用 [code_editor.dart](flutter/lib/components/code_editor.dart) 显示从 1 开始的逻辑行号，空行和末尾换行保留编号；长行折行不增加行号，行号随正文滚动。行号布局与正文使用相同的字形、字号比例和换行宽度，仅创建可见编号；搜索、格式化、撤销及修订校验沿用原编辑流程。

配置“编辑”页的高级配置依次提供“MITM”、“HTTP 改写”和“脚本”，由 [configuration.dart](flutter/lib/pages/configuration.dart) 复用共享分组、规则列表、排序及 JSON 表单。MITM 一级页直接编辑 HTTPS、允许域名、TCP 和 QUIC 回退，并提供单一证书入口；证书入口独立于配置加载。端口、应用等扩展字段通过同页的 MITM JSON 编辑入口修改。HTTP 改写仅编辑 Header/URL/Body Rewrite、Map Remote、Map Local 与静态资源；脚本独立编辑启用状态、HTTP 规则、Cron 任务和资源。新建规则与任务提供默认禁用的示例，完整 JSON 仍可编辑。字段与执行语义见 [libbox.md](libbox.md#独立脚本与-cron)。

所有修改通过 ProfileConfigStore.set 保留其他字段、修订及原生校验/失败恢复；关闭失败表单前保留草稿。CA 不写入配置，活跃 MITM 或手动记录时禁用重新生成，独立脚本/改写不占用证书。映射请求详情继续把连接出站/规则标为原连接。

[configuration_test.dart](flutter/test/configuration_test.dart) 验证三种开关及 Cron 草稿通过独立路径保存，关闭改写保留 MITM/脚本，校验失败保留内容；[requests_test.dart](flutter/test/requests_test.dart) 覆盖空域名、域名选择、旧记录设置清理、无改写横幅/抓包日志入口及 TCP 详情。生成的独立配置截图为 requests/independent-mitm-config.png、requests/independent-scripts-cron.png；结果按实际源码 SHA 记录。

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

[newui.yml](.github/workflows/newui.yml) 配置 Flutter 分析、测试及 AAR 构建，由 [setup-flutter](.github/actions/setup-flutter/action.yml) 固定 SDK 和依赖锁文件。测试按功能组织，覆盖导航、配置编辑、首页编辑、代理列表、备份、外观、关于、终端设置、模式、内网 IP 和 IP 查询/缓存；源码约束检查为 [verify_newui.py](tools/verify_newui.py)。截图/像素比较仍由同一测试入口运行，截图与 AAR 分别上传到 `newui-validation` 和 `flutter-aar-arm64`，Release 消费同次运行的 AAR，见 [构建与发布](app.md#构建与发布)。

[profiles_test.dart](flutter/test/profiles_test.dart) 覆盖新建与二进制导入的内容/元数据、添加菜单与取消导入、菜单能力、原始订阅链接、相对时间边界、自动更新校验、选中配置定位、列表及弹层在启停期间禁止切换与状态稳定后恢复、切换失败、排序冲突及远程会话隔离。[navigation_test.dart](flutter/test/navigation_test.dart) 另输出配置编辑与排序弹层在窄屏、大字号和宽屏下的截图；这些是布局回归产物，配置页尚未声明与上游逐像素一致。

[proxies_test.dart](flutter/test/proxies_test.dart) 集中验证自动组禁止手选、延迟测试目标、分组切换、列表折叠及视口外节点定位。[connections_test.dart](flutter/test/connections_test.dart) 检查关闭连接页后释放订阅；[status_panel_test.dart](flutter/test/status_panel_test.dart) 将启动按钮的独立无障碍点击入口与配置可用性、实际桥接调用一起验证。

配置测试另覆盖本地时间的未知占位及原生元数据事件刷新；[code_editor_test.dart](flutter/test/code_editor_test.dart) 对照实际 `RenderEditable` 检查行号在折行、宽度/字号变化及滚动后的对齐，并覆盖空行、末尾换行和替换内容。[intranet_panel_test.dart](flutter/test/intranet_panel_test.dart) 检查大字号下复制按钮的尺寸与位置，以及运行/停服/远程会话的复制内容；这些几何断言不代替实机视觉验收。

[service_settings_test.dart](flutter/test/service_settings_test.dart) 覆盖三个服务开关的即时应用、等待原生确认期间禁用开关及移除手动应用按钮；[box_test.dart](flutter/test/box_test.dart) 检查重复编辑、停止/远程会话、启停过渡及失败后的原生设置恢复。实际服务重启与恢复的 Android 验证范围见 [app.md](app.md#验证与历史记录)。

[logs_test.dart](flutter/test/logs_test.dart) 覆盖本地/远程日志状态的默认阈值、保留更严重的记录、手动筛选及恢复跟随配置，并检查暂停、搜索、导出和清空；实际内核级别与配置重载由 Android 仪器测试覆盖。

[state_updates_test.dart](flutter/test/state_updates_test.dart) 检查高频数据只重建对应订阅者、服务状态仍通知、应用根部不随遥测变化重建且外观设置继续生效，并覆盖日志增量淘汰、暂停快照与重置。[data_subscription_test.dart](flutter/test/data_subscription_test.dart) 检查卡片/详情共享订阅、隐藏页面、TickerMode、前后台和销毁时的订阅切换。测试验证通知和调用行为，实际 PSS/RSS、CPU 和帧耗时仍需在同设备、同配置下测量。

[access_test.dart](flutter/test/access_test.dart) 覆盖进入托管页面不扫描、加载期间接收最新托管选择、显示范围与隐藏选择保留、保存失败后重试、图标复用、扫描等待/成功/失败及查询设置返回后的刷新。它验证交互和调用行为，不代替实机加载耗时或视觉验收。

[requests_test.dart](flutter/test/requests_test.dart) 覆盖统一工具入口、抓取关闭时全部连接、开启后追加请求、同名 ID 的详情区分、停止后历史、连接筛选/排序、请求分页、各自关闭/清空和远程隔离，以及本机设置修订、MITM 域名选择、端口、启停确认/失败及后台轮询/迟到结果；证书测试检查无配置时从抓包设置经 MITM 页进入证书、明确生成、取消导出、活跃 MITM 禁止重生成、远程切换和信任刷新。其他回归包括 HAR 搜索、TCP 流完成/导出限制、既有 socket 元数据、报文菜单、字段/Cookie 复制及正文格式/Unicode 展开。窄屏、大字号深色及宽屏测试运行后将截图写入 build/screenshots/requests/，包含独立配置、请求设置、证书、合并列表及报文详情；桥接替身不能代替实际 TLS 或系统安装验收。

配置页面将滚动视口放在共享标题下方，避免文字穿过标题；测试核对几何位置与 320 宽、1.3 倍字号深色主题。当前独立 MITM/TCP/Cron 的实际验证见 [app.md](app.md#独立-mitmtcpcron-验证)，截图检查范围记录在本节末尾。

[http_message_test.dart](flutter/test/http_message_test.dart) 检查 JSON 数值/重复键/转义保真、无效或过大内容回退，以及重复 Cookie、空值、等号和 Expires 逗号。请求正文不完整或截断时不提供 cURL 导出。

[status_panel_parity_test.dart](flutter/test/status_panel_parity_test.dart) 与冻结的 [flclash_status_reference.dart](flutter/test/support/flclash_status_reference.dart) 比较像素，覆盖中英文、深浅主题、0.8/1.0/1.4 字号比例、IPv6、查询等待/失败、组织/ASN 回退及质量等级；停止按钮另行比较。停服状态包含无配置首次启动和设备网络查询的成功/等待/失败情况。

参考渲染器独立保留原版标题、chips、徽章、形变、卡片表面和排版，不导入 B-box 的适配仪表盘组件。比较使用相同视口、字号、语言、主题及 Roboto/Noto Sans SC 字体，按卡片自然高度截图并断言高度，双方图像上传为 CI Artifact。

这些测试只覆盖声明的页面和情景，不等于全应用逐像素一致或已经完成实机验收。升级参考版本时同步审查来源清单、适配文件、许可和独立参考渲染器，按实际 Actions 运行记录结果；Android 验证入口见 [app.md](app.md)。

2026-11-03 的独立 MITM/TCP/Cron 视觉检查读取 [Actions 41637368901](https://github.com/giturass/B-box/actions/runs/41637368901) 在 `339ecfc8bf42c4a07d800a6b8024d2e02a466ae9` 生成的 `newui-validation` 产物，实际查看 requests 截图目录中的 `independent-mitm-config.png`、`independent-scripts-cron.png`、`profile-rewriting-config.png` 和 `requests-recording-off.png`。前三张为 320 宽、1.3 倍字号深色主题：独立 MITM 的解密域名与 TCP 开关、脚本的 HTTP/Cron 入口、改写规则名称和数量均可读，标题与滚动内容分离。MITM 下方设置继续通过滚动访问，首屏截图不覆盖全部内容。请求页为浅色停止录制状态，只显示请求记录状态，已移除配置改写生效横幅。

这些为英文 Flutter fixture 截图，未覆盖中文实机、系统证书设置或 Root 管理器；完整结果及未覆盖范围见 [独立 MITM/TCP/Cron 验证](app.md#独立-mitmtcpcron-验证)。此前配置改写和请求录制/CA 的截图记录可通过本文 Git 历史查询，对应运行分别见 [JSON 配置改写验证](app.md#json-配置改写验证) 与 [MITM 分支验证](app.md#mitm-分支验证)。

### 连接与请求合并的提交前检查

2026-11-04，针对 `f7ecec18a8982486f8f542f9a0b04db765beab97` 基础上的改动，提交前已执行 `git diff --check`、`python3 tools/verify_newui.py` 及本文与 app.md 的本地链接/标题锚点检查，均通过。此记录仅包含提交前静态检查；合并页的 Flutter 分析、交互测试、布局截图及构建结果应按实际源码 SHA 和 Actions 链接另行记录，实机行为仍待验证。
