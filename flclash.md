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
| 外观与关于 | [appearance.dart](flutter/lib/pages/appearance.dart)、[about.dart](flutter/lib/pages/about.dart) | 复用主题预览、调色和版本/许可布局，保持 B-box 品牌及其 Release 来源 |
| 备份 | [backup.dart](flutter/lib/pages/backup.dart)、[form.dart](flutter/lib/components/form.dart) | 复用本地/远程分区、WebDAV 表单、连通状态、恢复范围及策略，后端使用 `bbox-1` 格式 |

### 导航与交互约定

- 首次启动直接进入仪表盘，没有免责声明接受门槛。Android 默认显示状态、网络速度、出站模式、配置和代理组五张卡；内存与内网 IP 可选。连接从工具页进入，不提供首页连接卡，旧布局/备份忽略该卡，下一次布局保存清理其键。
- 出站模式只显示内核提供的值。规则、全局、直连使用参考版字形，名称和选中态忽略大小写，排列和切换参数保留内核原值。系统 HTTP 代理开关按内核能力显示，标题使用与局域网代理相同的加粗样式和前置网络图标。
- 服务按钮的暂停图形表示停止服务，不新增暂停核心能力。远程会话、启停过渡和不可用操作必须反映真实状态。
- 工具页远程控制紧跟终端；终端设置在终端内，仪表盘编辑在首页工具栏。设置依次为外观、常规、访问控制、备份与恢复；高级配置从配置详情进入，不保留重复资源入口。
- 应用始终跟随系统语言并响应运行时变化。旧设置及备份中的语言选项丢弃，其他偏好保留。
- 代理列表保留圆角吸顶分组标题、固定高度的懒加载行、折叠动效及填充/普通/隐藏分组图标。折叠状态按选中配置保存，定位节点通过行偏移实现，不要求节点预先进入视口。
- 首页编辑保留卡片的全局 key，防止重复出口请求及丢失状态。改动立即保存；保存失败保留草稿，点击完成再次尝试。
- sing-box 专属 Tailscale、Taildrop、终端、认证、USB/IP、诊断和报告使用统一 Flutter 页面，复用原生后端；能力边界见 [app.md](app.md)。

### 配置页与内核差异

配置列表按参考版使用两行卡片、固定行高、选中徽标、更新动画及最多三列的自适应网格。普通配置页首次显示时定位已选配置，切换配置的弹层从顶部打开。添加按钮和空状态按钮均打开菜单；编辑使用分组表单，排序使用分组背景、拖动手柄及拖动浮起效果。菜单按编辑、详情、更新、更多、删除组织，删除标为危险操作；高级配置仍从详情进入。

| 参考版能力 | B-box 的适配边界 |
| --- | --- |
| 链接、文件、二维码导入 | 使用 sing-box JSON / 分享链接，保留 `.bpf`、动态 QRS 和剪贴板入口；不转换 Clash YAML |
| 自定义配置、提供者与覆写 | 新建本地 sing-box JSON；通过已有原始/高级配置编辑器修改，不引入 Mihomo 配置生成、提供者或覆写脚本 |
| 配置预览与文件编辑 | 操作明确标为“打开配置内容”，读取保存的 JSON，不将其称为运行时配置 |
| 自动更新 | 保留 Android 后端至少 15 分钟的限制；关闭后隐藏间隔输入，无效草稿不阻止关闭，保留原有效间隔及导入元数据；批量更新按原生互斥写入流程串行执行 |
| 流量用量与到期徽标 | 当前 Room/桥接没有订阅额度和到期字段，卡片显示真实本地/远程类型，不用会话流量代替额度；这是当前元数据能力的差异，不表示 sing-box 无法支持订阅信息展示 |
| 上次更新时间 | 远程配置使用 Room 时间戳，采用参考版紧凑相对时间并提供完整时间提示；未知时间显示未知，本地配置不显示订阅更新时间 |
| 分享与导出 | 复制链接得到原始订阅 URL，分享/二维码保留 sing-box 导入链接和名称，导出仍为 JSON |

编辑保留 B-box 已有的本地/远程类型切换；不会因视觉对齐加入参考版的 age 密钥等未接入能力。保存、修订冲突、配置应用及失败恢复继续由既有桥接处理，见 [app.md](app.md#配置服务应用与备份)。

### 状态卡片与出口查询

来源、解析、回退及质量判定沿用上述固定版本。IP 竞速由 [ui_egress.go](libbox/ui_egress.go) 负责：Cloudflare、IP.SB、IPinfo、ipwho.is、GeoJS、country.is、IPQuery、ident.me 共八源，具体限额及传输约束见 [libbox.md](libbox.md)。

质量查询在 [ip_quality.dart](flutter/lib/common/ip_quality.dart) 中依次分批使用 ident.me/ip-api.com/IPQuery、IPLocate、ipapi.is、proxycheck.io；等待一秒或当前请求全部失败后进入后续批次，单请求最多八秒。保留参考版组织/ASN 解析、显式类型优先、住宅类型推断及 Tor/abuser 质量规则，模型见 [ip_quality_models.dart](flutter/lib/common/ip_quality_models.dart)。

[ip_quality_cache.dart](flutter/lib/common/ip_quality_cache.dart) 将成功质量结果按 IP 持久缓存三天，失败仅在内存冷却五分钟，手动重试可跳过失败缓存。先显示 IP，再补充质量；质量失败保留 IP，详情展示来源和错误。

刷新与会话约定：

- 停止的本地服务通过设备当前网络查询，即使没有配置也可查询和手动重试；运行中通过 sing-box 路由查询。启停时取消旧探测，等待稳定服务状态再查询。远程会话保留占位，不展示本机出口。
- 返回前台保留未变路由的成功 IP 和质量；IP 查询失败才自动重试，间隔至少五秒，手动刷新可跳过。质量缓存到期只刷新质量。
- 短暂失焦不取消请求；已经发出的请求允许在后台完成，新查询等待前台。UI 在后台断开连接时保留本地分组快照，用于返回时比较。
- 启停、运行中的配置/模式/节点及网络变化取消旧请求、忽略迟到结果，并合并 300 毫秒内的变化后查询新出口，不受旧路由冷却限制。分组排序、延迟更新和停服时选择配置不触发无关的出口失效。

### 内网 IP 与备份展示

内网 IP 卡片复用参考版共享开关、端口副标题、Wi-Fi 徽章及复制操作。共享开关修改选中配置的 mixed/http/socks listen；无代理入站时先显示端口表单，明确新增 mixed 入站。运行时复制已成功启动配置的端点，停服/远程会话复制设备 IP，详情保留多入站/多端口。配置变换和保存约束分别见 [libbox.md](libbox.md) 与 [app.md](app.md)。

备份页沿用参考版的本地/远程布局、文件名、范围和兼容/覆盖策略，实际格式是 B-box `bbox-1`。覆盖会在页面确认后移除备份中不存在的配置；取消选择、写入失败或上传未确认时不显示成功。事务、文件恢复和传输限额由 Android/libbox 实现，见 [app.md](app.md)。

## 验证与升级参考版本

[newui.yml](.github/workflows/newui.yml) 配置 Flutter 分析、测试及 AAR 构建。现有测试覆盖导航、配置编辑、首页编辑、代理列表、备份、外观、关于、终端设置、模式、内网 IP、IP 查询/缓存和迁移回归；源码约束检查为 [verify_newui.py](tools/verify_newui.py)。

[profiles_test.dart](flutter/test/profiles_test.dart) 覆盖添加菜单与取消导入、菜单能力、原始订阅链接、相对时间边界、自动更新校验、选中配置定位、切换失败、排序冲突及远程会话隔离。[navigation_test.dart](flutter/test/navigation_test.dart) 另输出配置编辑与排序弹层在窄屏、大字号和宽屏下的截图；这些是布局回归产物，配置页尚未声明与上游逐像素一致。

[status_panel_parity_test.dart](flutter/test/status_panel_parity_test.dart) 与冻结的 [flclash_status_reference.dart](flutter/test/support/flclash_status_reference.dart) 比较像素，覆盖中英文、深浅主题、0.8/1.0/1.4 字号比例、IPv6、查询等待/失败、组织/ASN 回退及质量等级；停止按钮另行比较。停服状态包含无配置首次启动和设备网络查询的成功/等待/失败情况。

参考渲染器独立保留原版标题、chips、徽章、形变、卡片表面和排版，不导入 B-box 的适配仪表盘组件。比较使用相同视口、字号、语言、主题及 Roboto/Noto Sans SC 字体，按卡片自然高度截图并断言高度，双方图像上传为 CI Artifact。

这些测试只覆盖声明的页面和情景，不等于全应用逐像素一致或已经完成实机验收。升级参考版本时同步审查来源清单、适配文件、许可和独立参考渲染器，按实际 Actions 运行记录结果；Android 验证入口见 [app.md](app.md)。
