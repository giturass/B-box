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

清单中 `files` 和 `adaptedFiles` 的本地路径相对 `flutter/`，`adaptedNativeFiles` 的本地路径相对仓库根目录；`source`/`sources` 指向固定参考提交内的上游路径。清单记录逐文件哈希和改动来源，本文维护使用说明，两者一并保留。

共享组件位于 [flutter/lib/ui](flutter/lib/ui)。原始许可保留在 [flutter/assets/licenses/LICENSE](flutter/assets/licenses/LICENSE)，字体归属见 [JetBrainsMono-OFL.txt](flutter/assets/licenses/JetBrainsMono-OFL.txt) 和 [Twemoji-LICENSE.md](flutter/assets/licenses/Twemoji-LICENSE.md)。Twemoji 原始图形来自 Twitter，字体转换由 Mozilla 提供，图形未修改。这些文件同时用于应用内许可证页，不再单独保存重复副本。

## 界面复用与业务适配

Flutter 承担仪表盘、代理、配置、工具及所有子页面的导航。复用 FlClash 的控件、字形、形状、排版、字体、动效、弹层及导航；页面状态和操作经 Android 桥接接入 B-box 的 Room 与 libbox。没有引入 FlClash/Mihomo 原生库、Clash 配置模型、脚本、订阅转换器或 Clash 专用服务。

| 区域 | 主要实现 | 当前适配 |
| --- | --- | --- |
| 首页与状态 | [dashboard.dart](flutter/lib/pages/dashboard.dart)、[status_panel.dart](flutter/lib/components/status_panel.dart)、[start_toggle.dart](flutter/lib/components/start_toggle.dart) | 复用状态布局、国家徽章、服务按钮形变及网络速率展示；启停调用原生服务流程 |
| 配置卡与分组概览 | [profile_panel.dart](flutter/lib/components/profile_panel.dart)、[proxy_groups_panel.dart](flutter/lib/components/proxy_groups_panel.dart) | 使用真实配置元数据和内核分组，远端未知数量保持未知 |
| 代理 | [proxies.dart](flutter/lib/pages/proxies.dart)、[proxy_card.dart](flutter/lib/components/proxy_card.dart)、[proxy_list.dart](flutter/lib/components/proxy_list.dart) | 卡片与列表都使用 libbox 分组；只有可选择的组显示选择操作 |
| 配置管理 | [profiles.dart](flutter/lib/pages/profiles.dart)、[profile_card.dart](flutter/lib/components/profile_card.dart)、[profile_details.dart](flutter/lib/pages/profile_details.dart) | 复用行高、选中态、相对更新时间、添加菜单、分组表单及排序样式，编辑和导入 sing-box JSON |
| 首页编辑 | [dashboard_editor.dart](flutter/lib/components/dashboard_editor.dart) | 原地编辑卡片、摇摆、长按排序、移除徽章及添加列表 |
| 内存与局域网 | [memory_panel.dart](flutter/lib/components/memory_panel.dart)、[intranet_panel.dart](flutter/lib/components/intranet_panel.dart) | 分别使用 Go/进程内存和 sing-box 监听配置，不虚构 Mihomo 内存或全局代理端口 |
| 系统 HTTP 代理 | [system_proxy_panel.dart](flutter/lib/components/system_proxy_panel.dart) | 采用参考版 VPN 卡片的布局、状态徽章和紧凑开关，使用 B-box 自有的大陆轮廓地球字形；开关接入内核上报状态及既有 Android 桥接 |
| 外观与关于 | [appearance.dart](flutter/lib/pages/appearance.dart)、[about.dart](flutter/lib/pages/about.dart) | 复用主题预览、调色和版本/许可布局，保持 B-box 品牌及其 Release 来源 |
| 备份 | [backup.dart](flutter/lib/pages/backup.dart)、[form.dart](flutter/lib/components/form.dart) | 复用本地/远程分区、WebDAV 表单、连通状态、恢复范围及策略，后端使用 `bbox-1` 格式 |
| 请求调试 | [requests.dart](flutter/lib/pages/requests.dart)、[request_settings.dart](flutter/lib/pages/request_settings.dart)、[capture_certificate.dart](flutter/lib/pages/capture_certificate.dart) | 工具中的请求列表、详情、筛选/导出和 CA 管理；使用共享列表、表单与导航组件，数据来自本地 libbox |

### 导航与交互约定

- 首次启动直接进入仪表盘，没有免责声明接受门槛。Android 默认显示状态、网络速度、出站模式、配置和代理组五张卡；内存与内网 IP 可选。连接从工具页进入，不提供首页连接卡，旧布局/备份忽略该卡，下一次布局保存清理其键。
- 出站模式只显示内核提供的值。规则、全局、直连使用参考版字形，名称和选中态忽略大小写，排列和切换参数保留内核原值。系统 HTTP 代理卡片按内核能力显示，支持首页编辑中的排序、移除和重新添加；能力暂不可用时保留顺序和隐藏偏好。卡片沿用参考版 VPN 卡片的 48 像素徽章、加粗标题、启用/禁用副标题及紧凑开关；启用时徽章为主题色六瓣形，禁用时为低强调圆形。卡片和首页编辑列表共用 [system_proxy_glyph.dart](flutter/lib/components/system_proxy_glyph.dart) 中的大陆轮廓地球字形，使用共享 Glyph 的线宽与圆角；全局出站模式保留参考版经纬线球体字形。
- 服务按钮的暂停图形表示停止服务，不新增暂停核心能力。远程会话、启停过渡和不可用操作必须反映真实状态。
- 工具页远程控制紧跟终端；终端设置在终端内，仪表盘编辑在首页工具栏。设置依次为外观、常规、访问控制、备份与恢复；高级配置从配置的“编辑”页进入，不保留重复资源入口。
- “请求”紧挨在工具页“连接”上方；列表支持搜索和分批加载，详情区分请求/响应头、正文前缀与截断/未完成状态。抓取开关等待原生确认，停服时保留历史记录；远程会话显示不可用。HTTPS 白名单说明与 [核心选择规则](libbox.md#httphttps-请求调试) 一致；CA 页面区分导入、待重启与最近系统信任检查，Android 处理见 [app.md](app.md#请求抓取与调试证书)。
- 常规服务页使用即时应用的开关，保存和重启期间显示进度并禁用开关，底部没有“应用服务设置”按钮；启动确认及失败恢复见 [app.md](app.md#配置服务应用与备份)。
- 日志页默认选择“跟随配置”，显示内核上报的日志级别及更严重的记录；菜单同时显示当前级别，仍可手动选择“全部”或单一级别，手动筛选不会被后续状态更新覆盖。搜索、暂停和导出沿用同一筛选结果；默认级别尚未收到时保留已接收记录。日志来源及本地重载同步见 [app.md](app.md#配置服务应用与备份)。
- 应用始终跟随系统语言并响应运行时变化。旧设置及备份中的语言选项丢弃，其他偏好保留。
- 代理列表保留圆角吸顶分组标题、固定高度的懒加载行、折叠动效及填充/普通/隐藏分组图标。折叠状态按选中配置保存，定位节点通过行偏移实现，不要求节点预先进入视口。
- 访问控制按上游 SFA 的列表/扫描分离方式加载，进入页面直接显示应用与已保存的托管选择；“重新扫描托管应用”为独立操作，扫描期间保留列表。筛选/搜索/排序结果在列表及相关条件未变化时复用，流量等无关状态更新不重新排序。图标保持按可见行请求和缓存，切换显示范围或无修改地关闭查询设置不清空图标；查询策略变更或申请权限后关闭设置只刷新一次。原生查询与托管更新约定见 [app.md](app.md#配置服务应用与备份)。
- 首页编辑保留卡片的全局 key，防止重复出口请求及丢失状态。改动立即保存；保存失败保留草稿，点击完成再次尝试。
- sing-box 专属 Tailscale、Taildrop、终端、认证、USB/IP、诊断和报告使用统一 Flutter 页面，复用原生后端；能力边界见 [app.md](app.md)。

[box.dart](flutter/lib/box.dart) 按应用状态、启动/外观、流量、内存、连接和日志分开通知；流量采样与仪表盘累计值只更新相关组件，日志和连接变化不触发应用根部、代理或配置列表重建。出口查询监听应用状态变化，不因流量或内存采样重新计算路由标识。显式批量通知仍刷新全部依赖。

[data_subscription.dart](flutter/lib/components/data_subscription.dart) 按页面活动状态、TickerMode 和应用生命周期合并内存/日志订阅；卡片与详情页共享一个原生采样请求，最后一个可见消费者退出时停止。日志增量保留既有记录对象，暂停快照独立于后续追加和淘汰，恢复可见时重新同步原生缓冲。原生采样与消息边界见 [app.md](app.md#配置服务应用与备份)。

[capture_poll.dart](flutter/lib/components/capture_poll.dart) 仅在本地请求列表/详情页活动、TickerMode 开启且应用前台时每两秒查询；调用不重叠，筛选变化合并刷新，并忽略旧筛选或会话的迟到结果。隐藏页面停止轮询，抓取引擎继续运行至用户或服务停止。页面为 B-box 业务代码，没有直接修改 FlClash 导入文件。

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

[newui.yml](.github/workflows/newui.yml) 配置 Flutter 分析、测试及 AAR 构建。现有测试覆盖导航、配置编辑、首页编辑、代理列表、备份、外观、关于、终端设置、模式、内网 IP、IP 查询/缓存和迁移回归；源码约束检查为 [verify_newui.py](tools/verify_newui.py)。

[profiles_test.dart](flutter/test/profiles_test.dart) 覆盖添加菜单与取消导入、菜单能力、原始订阅链接、相对时间边界、自动更新校验、选中配置定位、列表及弹层在启停期间禁止切换与状态稳定后恢复、切换失败、排序冲突及远程会话隔离。[navigation_test.dart](flutter/test/navigation_test.dart) 另输出配置编辑与排序弹层在窄屏、大字号和宽屏下的截图；这些是布局回归产物，配置页尚未声明与上游逐像素一致。

配置测试另覆盖本地时间的未知占位及原生元数据事件刷新；[code_editor_test.dart](flutter/test/code_editor_test.dart) 对照实际 `RenderEditable` 检查行号在折行、宽度/字号变化及滚动后的对齐，并覆盖空行、末尾换行和替换内容。[intranet_panel_test.dart](flutter/test/intranet_panel_test.dart) 检查大字号下复制按钮的尺寸与位置，以及运行/停服/远程会话的复制内容；这些几何断言不代替实机视觉验收。

[service_settings_test.dart](flutter/test/service_settings_test.dart) 覆盖三个服务开关的即时应用、等待原生确认期间禁用开关及移除手动应用按钮；[box_test.dart](flutter/test/box_test.dart) 检查重复编辑、停止/远程会话、启停过渡及失败后的原生设置恢复。实际服务重启与恢复的 Android 验证范围见 [app.md](app.md#验证与历史记录)。

[logs_test.dart](flutter/test/logs_test.dart) 覆盖本地/远程日志状态的默认阈值、保留更严重的记录、手动筛选及恢复跟随配置，并检查暂停、搜索、导出和清空；实际内核级别与配置重载由 Android 仪器测试覆盖。

[state_updates_test.dart](flutter/test/state_updates_test.dart) 检查高频数据只重建对应订阅者、服务状态仍通知、应用根部不随遥测变化重建且外观设置继续生效，并覆盖日志增量淘汰、暂停快照与重置。[data_subscription_test.dart](flutter/test/data_subscription_test.dart) 检查卡片/详情共享订阅、隐藏页面、TickerMode、前后台和销毁时的订阅切换。测试验证通知和调用行为，实际 PSS/RSS、CPU 和帧耗时仍需在同设备、同配置下测量。

[access_test.dart](flutter/test/access_test.dart) 与 [migration_regression_test.dart](flutter/test/migration_regression_test.dart) 覆盖进入托管页面不扫描、显示范围与隐藏选择保留、图标复用、扫描等待/成功/失败及查询设置返回后的刷新。它们验证交互和调用行为，不代替实机加载耗时或视觉验收。

[requests_test.dart](flutter/test/requests_test.dart) 覆盖工具入口顺序、远程隔离、空 HTTPS 白名单保存、启动确认、后台停止轮询、迟到搜索结果、CA 待重启状态及窄屏/大字号/宽屏布局。正文不完整或截断时不提供 cURL 导出。该测试使用桥接替身，不等于真实 TLS 或 Root 验收；相应核心和 Android 验证见 [libbox.md](libbox.md#构建同步和验证) 与 [app.md](app.md#验证与历史记录)。

[status_panel_parity_test.dart](flutter/test/status_panel_parity_test.dart) 与冻结的 [flclash_status_reference.dart](flutter/test/support/flclash_status_reference.dart) 比较像素，覆盖中英文、深浅主题、0.8/1.0/1.4 字号比例、IPv6、查询等待/失败、组织/ASN 回退及质量等级；停止按钮另行比较。停服状态包含无配置首次启动和设备网络查询的成功/等待/失败情况。

参考渲染器独立保留原版标题、chips、徽章、形变、卡片表面和排版，不导入 B-box 的适配仪表盘组件。比较使用相同视口、字号、语言、主题及 Roboto/Noto Sans SC 字体，按卡片自然高度截图并断言高度，双方图像上传为 CI Artifact。

这些测试只覆盖声明的页面和情景，不等于全应用逐像素一致或已经完成实机验收。升级参考版本时同步审查来源清单、适配文件、许可和独立参考渲染器，按实际 Actions 运行记录结果；Android 验证入口见 [app.md](app.md)。
