# MiChongs 主内核与 Concord 集成

本文记录核心补丁和新增绑定；项目约束见 [AGENTS.md](AGENTS.md)，Android 调用及数据持久化见 [app.md](app.md)，FlClash 查询规则和界面适配见 [flclash.md](flclash.md)。

## 基线与集成方式

主内核为 [MiChongs/sing-box](https://github.com/MiChongs/sing-box)，固定比较提交 `f88982450a13a818f680de884663fa914798e84a`。HTTP 处理复用 [Concord](https://github.com/chen08209/concord)，固定比较提交 `4db36a472bd3f290195dec55dfc30ac2ebc03b7a`。两个项目分别 fork 至 [giturass/sing-box](https://github.com/giturass/sing-box) 与 [giturass/concord](https://github.com/giturass/concord)，通过 [core/sing-box](core/sing-box) 与 [core/concord](core/concord) 子模块引入。实际构建 SHA 由根仓库 gitlink 固定，[upstreams.json](upstreams.json) 保存原始比较 SHA 与 fork 地址；显示核心版本 `1.15.0-alpha.13-xiaobaf14g.17` 和 Go `go1.27.0` 由 [version.properties](version.properties) 决定。应用及 Android 上游基线独立维护，见 [app.md](app.md#比较基线与应用身份)。

sing-box 管理 VPN、网络接口、DNS、路由、分组、Provider、服务及全部出站；Concord 只构建 `libconcord_processing.so`，复用其 MITM、HTTP 改写、QuickJS、模块解析和合成，不启动 Concord engine、TUN 或第二套代理。改写到新目标的请求重新进入 sing-box 路由，原目标沿用原选路；处理器内部拨号跳过处理钩子以防递归。

B-box 扩展直接维护在 fork，根仓库不再保存 overlay 或向 SagerNet 原版覆盖源码。[setup-core](.github/actions/setup-core/action.yml) 校验 gitlink，建立隔离 `.core` 工作树；[依赖准备脚本](core/sing-box/b-box/prepare_dependencies.py) 核对逻辑模块、版本及 MiChongs replacement，复制需要补丁的依赖到该工作树，再应用检查过的补丁和本地 replace，不修改共享模块缓存。补丁来源与扩展路径由 [fork 集成说明](core/sing-box/b-box/README.md) 维护。

| 集成范围 | 当前源码 | 行为 |
| --- | --- | --- |
| Android 单 AAR / 全部原生标签 | [build_libbox](core/sing-box/cmd/internal/build_libbox/main.go)、[android.txt](core/sing-box/b-box/buildtags/android.txt) | API 24 起，保留 MiChongs 全部功能标签和完整 Tailscale，不生成 legacy AAR |
| 节点、订阅及分组绑定 | [experimental/libbox](core/sing-box/experimental/libbox) 的 `ui_nodes*.go`、`ui_groups.go` | 节点写入配置，订阅不覆盖整份配置，组类型及可选择性由当前内核判断 |
| 内存、HTTP、下载、WARP、出口与 LAN | 同目录 `ui_dashboard.go`、`ui_http.go`、`ui_download.go`、`ui_warp.go`、`ui_egress.go`、`ui_lan.go` | 保留原 Android 扩展及取消/生命周期合同 |
| Concord 桥接 | [experimental/concord](core/sing-box/experimental/concord)、[ui_concord.go](core/sing-box/experimental/libbox/ui_concord.go) | C ABI v1、宿主拨号/信任/Keystore 签名、状态、模块预览、任务及共享请求记录 |
| HTTP 记录与旧配置兼容 | [experimental/httpdebug](core/sing-box/experimental/httpdebug)、[路由钩子](core/sing-box/route/b_box_httpdebug.go) | 原请求存储和导出；旧 Go MITM、改写、Goja 仅用于已有配置 |
| 配置 schema 与校验 | [processing.go](core/sing-box/option/processing.go)、[config.go](core/sing-box/experimental/libbox/config.go) | 新处理配置保留原生语法，保存前拒绝未接入的 DNS/路由脚本及旧处理器混用 |
| 依赖兼容 | [b-box](core/sing-box/b-box) | sing-tun 4/8 字节 ingress map、sing-vmess Vision 指针、旧 Goja/regexp2 取消；固定版本及回归随 fork 维护 |

MiChongs 原生 Provider、分组、证书提供者、服务、HTTP 客户端、namespace、实验选项和协议均保留。完整 JSON 可由原始编辑器及“网络”对应入口保存，再交给当前内核校验；这些能力的系统权限和平台条件沿用主内核，不以 UI 的参考范围裁剪它们。官方原版 AAR 缺少 B-box 新绑定和 Concord 动态库，不能按显示版本直接替换。

## 新增接口与行为

### 节点导入与配置合并

[ui_nodes_parse.go](core/sing-box/experimental/libbox/ui_nodes_parse.go) 的 `ParseUINodes` 读取节点分享 URI、Base64 订阅、Clash 节点列表、SIP008、wg-quick 以及 sing-box 出站/端点，返回节点、逐项拒绝原因和完整配置提示。节点按当前核心的出站及端点注册表判断协议，并经过选项解码和构造检查；Clash 内容只作为节点来源，不作为应用配置或静默转换整份 Clash 配置。WireGuard 保存在 `endpoints`，服务端、Tailscale、代理组及 direct 不作为可导入代理节点。

`AddUIConfigNodes(config, nodes, defaultProfile)` 返回 `UIConfigNodeUpdate` 的 `Config`、`Added`、`Skipped`，直接追加到所选配置的 `outbounds` / `endpoints`。重名的新节点按“名称 2”等空闲名称保存，并同步改写同批节点之间的 detour 引用；重复导入相同节点跳过。可用节点加入配置中的 selector/urltest，未解析、循环或通过代理组的 detour 不自动加入组，以避免分组产生循环。`GetUINode(config, tag)` 返回单节点的 `endpoint` 和格式化 `content`；`SetUIConfigNode` 修改配置节点及所有旧名称引用，`RemoveUIConfigNodes` 清理成员和默认选择，其他引用仍在使用时拒绝删除。

本地追加的节点名称以配置开头的 `// b-box-local-nodes: ["名称"]` JSON 注释保存。sing-box 原生 JSON 解码忽略该注释，`CheckConfig` 和服务直接读取完整配置，没有额外运行时展开或共享节点库；本地追加节点不绑定 URL 来源。标记随配置分享、导出与备份保留，单节点改名及删除同步更新它。`PreserveUIConfigNodes(previous, edited)` 在平台 JSON 编辑移除注释后保留仍存在的本地节点标签；`edited` 自带标记时以它为准，空数组同样有效。[FormatConfig](core/sing-box/experimental/libbox/config.go) 修改原有 `FormatConfig` 的返回路径，使标准格式化调用也保留标记。Android 持久化与修订检查见 [应用配置写入](app.md#配置服务应用与备份)。

`ApplyUISubscriptionNodes(current, subscription, previousTags, defaultProfile)` 把一个 URL 来源的节点写入现有选中配置，返回 `UISubscriptionNodeUpdate` 的 `Config`、`Tags`、`Added`、`Skipped` 和 `Warnings`；`Tags` 是实际保存名称的 JSON 数组，`Skipped` 为因明确不支持的能力而跳过的节点数，`Warnings` 为按固定原因聚合的 `[{"reason":"原因","count":数量}]` JSON 数组，无跳过时返回 `[]`。报告不包含节点链接、名称或凭据。新增来源传入 `[]`，只追加节点并保留全部现有节点；更新传入该来源上次的 `Tags`，只替换这些名称对应的代理节点，不删除其他来源、本地标记节点或非代理出站/端点。配置内容及来源名称由 Android 在同一成功提交中保存，来源数据及应用/恢复流程见 [应用配置写入](app.md#配置服务应用与备份)。此接口要求 `current` 已存在，不创建配置或独立节点库。

订阅只读取上述格式中的实际代理节点，忽略 DNS、路由、入站、代理组、配置处理和本地标记，不读取提供者、覆写或脚本。sing-box JSON 只把 `outbounds` / `endpoints` 交给节点解析，其他根级 `servers`、`proxies` 或提供者元数据不会抢先改变格式判断。WireGuard、原生 `masque-client` 等代理端点按当前注册表保留在 `endpoints`；Tailscale 和服务端等基础设施仍保留原配置能力，不作为订阅代理导入。已识别但当前核心明确不支持的能力（如 KCP 传输或 SSR）只跳过对应节点，并由上述结果返回数量和原因；Android 随来源保存报告，界面在导入、更新及来源列表中明确展示。能力拒绝在对应转换器检查到不支持项时返回，该节点后续字段不再构造或保存。未知链接协议、非法列表或协议字段、受支持节点的非法选项，以及失效或循环 detour 仍拒绝整个更新；detour 指向被跳过节点，或存在未命名的被跳过节点而无法确定依赖时，同样拒绝，不能改绑配置中的同名节点。全部节点被跳过、没有可用节点或仅有提供者时失败，旧内容及来源不变。能力拒绝使用独立错误类型，不能把一般解析或校验错误当作可忽略节点。

订阅更新保留已有节点原名称，订阅节点与已有名称冲突时另取“名称 2”等空闲名称，并同步调整本批 detour。节点可以通过当前配置保留的本地节点、其他来源节点或 direct 拨号；通过本地代理组的节点不自动加入组，避免分组产生循环。更新只清理在具体成员组中形成拨号循环的代理成员，包括经本次更新节点间接形成循环的其他来源和本地节点；通过其他组拨号但不形成循环的合法成员关系及节点内容保留，嵌套组成员保留，组没有合法成员时更新失败。订阅自行定义但已丢弃的代理组只能报错，不能误绑定当前配置的同名组。每轮先移除本来源旧代理再添加新节点，重复更新不会持续增加重名节点。组成员保留仍有效的原顺序，清理已消失名称后追加新名称；失效的默认选择清理。`defaultProfile` 表示需要处理内置模板的 auto 兜底，不等同于不可删除的默认配置身份；Android 的系统默认及普通本地模板判定见 [应用配置流程](app.md#配置服务应用与备份)。其他组及非模板配置明确声明的 direct 成员保留。路由、DNS、本地节点或其他来源仍直接引用已消失节点时整个更新失败，不删除用户规则。订阅节点不获得本地标记，完整 `CheckConfig` 和原子保存由 Android 共用流程完成。

`RetainUISubscriptionNodes(current, tags)` 在移除来源时把仍存在的代理节点加入本地标记，仅改注释，保留文件正文和节点内容，后续来源或旧远端更新不会删除这些节点。缺失名称和非代理节点不获得本地标记。`UpdateUISubscriptionNodes(current, subscription)` 保留旧单 URL 远端配置的兼容行为：替换全部非本地代理，空 `current` 使用默认配置；该旧 StringBox 绑定没有报告通道，含不支持节点时仍拒绝整个更新，避免静默应用部分来源。多来源应用使用 `ApplyUISubscriptionNodes` 按来源维护所有权。`MergeUIConfigNodes(current, incoming)` 仍保留原兼容绑定，新来源不通过它覆盖整份配置。

行为核对参考 Throne `dev` 的固定提交 [`48273be2870fd2c81059628ff2cc439ac711808e`](https://github.com/throneproj/Throne/tree/48273be2870fd2c81059628ff2cc439ac711808e)：[SubscriptionParser.cpp](https://github.com/throneproj/Throne/blob/48273be2870fd2c81059628ff2cc439ac711808e/src/configs/sub/SubscriptionParser.cpp#L267) 识别 `outbounds` / `endpoints` 并分别遍历代理，跳过基础设施类型；[Protocols.cpp](https://github.com/throneproj/Throne/blob/48273be2870fd2c81059628ff2cc439ac711808e/src/configs/common/Protocols.cpp#L95) 包含 WireGuard / MASQUE 的解析能力。B-box 将节点写入选中配置，由本仓库独立实现，未复制 Throne 代码或引入其依赖，无新增许可归属。

[ui_nodes_subscription_test.go](core/sing-box/experimental/libbox/ui_nodes_subscription_test.go) 和 [ui_nodes_subscription_utls_test.go](core/sing-box/experimental/libbox/ui_nodes_subscription_utls_test.go) 覆盖配置设置保留、分享 URI/Base64/sing-box/Clash/SIP008/wg-quick、严格拒绝无效更新及 Plain/Base64 中的未知 URI 协议、Clash 协议字段结构检查、Plain/Base64/Clash 混合 KCP 的明确跳过报告及修复后清空报告、被跳过节点的 detour 不改绑保留或同批同名节点、VMess 拒绝名称来自内层 JSON、46 条脱敏混合链接（包含 7 条 XHTTP）全部导入的完整核心校验、组循环清理与合法 detour 成员保留、本地/来源重名及 detour、重复更新、双来源更新隔离、来源移除转本地、WireGuard/MASQUE 端点及元数据不改变端点格式。脱敏混合样例含 REALITY，在统一工作流的全部功能标签 `TestUI` 步骤运行。测试源码及静态检查不代表真实订阅网络已经验收。

`GetUIDefaultConfig` 提供无账号的合法 sing-box 模板：proxy 默认包含 auto，auto 在未添加节点时使用 direct。模板显式使用 `certificate.store: "mozilla"`，以当前固定核心内置的可信 CA 校验 HTTPS，避免旧 Android 系统证书库无法验证当前公共证书链；仍检查链、域名及有效期。机制与可信库范围见同版本的 [官方证书配置](https://github.com/SagerNet/sing-box/blob/824dbcfb4dd0a95921840b1737b950cde590241e/docs/configuration/certificate/index.md)。配置可显式选择 `system` / `chrome` / `none` 或增加自有 PEM 证书；没有改变这些原生选项的语义。已有文件的保留规则见 [应用配置流程](app.md#配置服务应用与备份)。

`AddUIConfigNodes` / `ApplyUISubscriptionNodes` 的 `defaultProfile=true` 在添加、导入或刷新节点时，只要受内置默认模板管理的 auto/urltest 组中存在合法代理成员，就移除所有指向 direct 出站的兜底成员，保留代理成员顺序；已有 direct 与代理混合成员、没有新增节点的重复导入同样清理。按实际出站类型识别 direct，direct 出站本身及 DNS/路由引用保留；没有合法代理成员时保留兜底，既有代理可通过其他合法分组拨号但不能形成回到 auto 的循环。读取配置和启动服务不执行此清理。`RemoveUIConfigNodes` 使用同一模板判定，在删除最后一个节点时恢复配置中已有 direct。其他组、非模板本地配置及已有远端配置保留明确声明的 direct 成员，缺少合法成员时仍拒绝删除；证书设置和其他根级配置保留。默认配置的显示及配置卡片布局由 [Flutter 配置页](flclash.md#导航与交互约定) 维护。

### 节点分享

`ShareUIConfigNodes(config, tags)` 从所选配置的 `outbounds` 和 `endpoints` 读取代理节点，按传入标签的顺序返回各节点的可用分享链接及完整节点 JSON 数组。当前可生成 SIP002 Shadowsocks、v2rayN VMess、VLESS、Trojan、Hysteria、Hysteria2、TUIC、AnyTLS、SOCKS、Naive 和单对端 WireGuard 的常见链接。核心重新解析生成的链接并比较 sing-box 选项。证书路径、配置内的拨号依赖和无法在 URI 中表达的选项会使该节点使用 JSON 分享。JSON 保留节点的配置内引用，接收端若要使用该节点，仍需有对应的配置或文件。[核心实现](core/sing-box/experimental/libbox/ui_nodes_share.go) · [编码与回读回归](core/sing-box/experimental/libbox/ui_nodes_share_test.go) · [Shadowsocks SIP002](https://shadowsocks.org/doc/sip002.html) · [Hysteria 2 URI](https://v2.hysteria.network/docs/developers/URI-Scheme/)

### VMess 外部节点的 auto 加密

行为参考 [MiChongs/sing-box 修复提交 `22c9be74053d383baa831ccd7ea11c3a65bb2c29`](https://github.com/MiChongs/sing-box/commit/22c9be74053d383baa831ccd7ea11c3a65bb2c29) 的 [VMess 订阅加密解析](https://github.com/MiChongs/sing-box/blob/22c9be74053d383baa831ccd7ea11c3a65bb2c29/provider/parser/vmess.go)。当前官方核心的 [VMess 出站](https://github.com/SagerNet/sing-box/blob/824dbcfb4dd0a95921840b1737b950cde590241e/protocol/vmess/outbound.go) 在启用 TLS 时将 `security: "auto"` 改为 `zero`，与外部节点来源所表达的自动 AEAD 加密不同。

[uiNodeVMessNode](core/sing-box/experimental/libbox/ui_nodes_scheme.go) 在 Clash `cipher`、VMess Base64 JSON 的 `scy` 及 VMess URI 的 `encryption` 为 `auto` 或缺省时，按当前固定 [sing-vmess 的 `AutoSecurityType`](https://github.com/SagerNet/sing-vmess/blob/a0c58542bd9d99b00ea0c3cfacce6f03cf8a4017/protocol.go#L137) 写入明确的 `aes-128-gcm` 或 `chacha20-poly1305`，有无 TLS 使用同一规则。显式 `none`、`zero` 和其他受支持 cipher 保留；原生 sing-box JSON 的 `security` 不经过此转换，继续使用核心原有语义。已有节点通过重新导入或刷新对应订阅来源获得修复，不批量改写已有配置。

[ui_nodes_vmess_test.go](core/sing-box/experimental/libbox/ui_nodes_vmess_test.go) 覆盖上述导入格式、TLS 开关、显式 cipher、原生 JSON 保留及订阅应用后的配置校验。文件直接位于 fork，并由统一工作流以全部发布标签运行 `^TestUI`；测试源码的存在不代表当前源码已通过 Actions 或外部服务互通验收。

### 配置分组与停服选择

[ui_groups.go](core/sing-box/experimental/libbox/ui_groups.go) 的 `GetUIConfigGroups(config)` 使用当前 MiChongs 核心的配置解码器读取原生分组，按配置顺序返回与运行时相同的 `OutboundGroupIterator`，成员保留原生协议类型，包含 direct、嵌套组及端点。手动组的选中项按当前核心顺序取合法缓存、显式 default 或首个成员；没有运行内核时，自动组的选中项和所有成员的延迟保持未知，不虚构测速结果。未被分组引用的出站不额外组成虚拟节点组。

`SelectUIConfigGroup(config, group, tag)` 检查完整配置、手动组及成员后，以单次 bbolt 事务只写入核心的 `selected` 缓存。路径与 libbox 服务一致，使用工作目录及 `experimental.cache_file.path`，命名空间沿用 `cache_id` 的 `0x00 + ID` bucket；不修改其他组、DNS、规则集等缓存，不清空或重建损坏文件。读取只打开已有文件，缺少文件时使用配置默认选择；锁定或损坏报错。运行中保留 MiChongs Nested 手动选择；停服路径只对仍存在的静态成员维护 `name@group` 选择缓存，动态 Provider 成员不虚构为已加载节点。两项绑定仅供本机停服路径调用，Android 在配置及启动快照锁内确认状态及修订，见 [应用说明](app.md#配置服务应用与备份)。

当前上游 [`box.go`](https://github.com/SagerNet/sing-box/blob/824dbcfb4dd0a95921840b1737b950cde590241e/box.go) 在提供平台日志写入器时总是启用缓存，Android daemon 的启动路径提供该写入器，因此未声明或关闭 `cache_file.enabled` 也需使用同一缓存。上游 [`selector.go`](https://github.com/SagerNet/sing-box/blob/824dbcfb4dd0a95921840b1737b950cde590241e/protocol/group/selector.go) 启动时先读取缓存再读取 default；停服选择直接保存该缓存，启动无需改写配置或补发选择命令。

[ui_groups_test.go](core/sing-box/experimental/libbox/ui_groups_test.go) 覆盖分组成员、未命名节点的实际标签、重复出站/端点标签拒绝、默认及缓存选择、缓存路径/命名空间隔离、自动组和失效成员拒绝、缓存锁定/损坏保留，以及启动 selector 读取停服选择。启动夹具按测试进程的 UID/GID 配置文件管理上下文，对应 Android 的 libbox `Setup`，不修改生产属主设置。统一工作流的 `TestUI` 入口运行这些 fork 扩展；实际运行结果见 [应用验证记录](app.md#当前验证记录)。

### XHTTP 与 VLESS Encryption

XHTTP 与 VLESS Encryption 是 MiChongs 主内核的原生能力；生产源码位于 [transport/v2rayxhttp](core/sing-box/transport/v2rayxhttp)、[protocol/vless/encryption](core/sing-box/protocol/vless/encryption)，选项和插件注册保留。固定比较提交及 B-box 适配见 [fork 集成说明](core/sing-box/b-box/README.md#xhttp-与-vless-encryption)。不再单独移植协议文件或复制旧 QUIC TLS 适配；MiChongs 的 Provider 和其他扩展同样保留。协议行为参考 [Xray-core v26.9.30](https://github.com/XTLS/Xray-core/tree/b26a91de4f3294e26a0ad0a970b81a386a41f789)。

`transport.type` 为 `xhttp`，与 VLESS、VMess、Trojan 等已有 V2Ray 传输层协议配合使用；同时提供入站和出站。支持 HTTP/1.1、HTTP/2、HTTP/3，模式为 `auto`、`packet-up`、`stream-up`、`stream-one`。明文默认 HTTP/1.1，REALITY 使用 HTTP/2，TLS 的 ALPN 仅为 `h3` 时使用 HTTP/3，仅为 `http/1.1` 时使用 HTTP/1.1，其余使用 HTTP/2。`auto` 在 REALITY 下选择 `stream-one`（上下行分离时为 `stream-up`），其他情况选择 `packet-up`。

核心 JSON 将 XHTTP 字段直接放在 `transport` 中：`host`、`path`、`mode`、`headers`、填充/会话/序号/上行混淆选项、`xmux`、`download_settings` 及 QUIC 参数，具体字段由 [v2rayxhttp.go](core/sing-box/option/v2rayxhttp.go) 定义。范围值接受整数或 `"最小值-最大值"`。`download_settings` 的 XHTTP 参数不从上行继承，应显式提供；服务器地址和端口分别回退到上行，支持只指定下行端口。HTTP/3 忽略 uTLS 指纹，使用标准 TLS，同时保留 SNI、证书验证与当前核心支持的 ECH 设置；TLS 字段由当前 MiChongs 解码器校验。

VLESS 出站新增 `encryption`，入站新增 `decryption`，空值或 `none` 保持原 VLESS 行为。加密方式为 `mlkem768x25519plus`，外观支持 `native`、`xorpub`、`random`；客户端使用 `0rtt` 或 `1rtt`，服务端使用票据有效期（如 `600s`、`300-600s` 或禁用复用的 `0s`），随后为可选填充和服务端提供的密钥参数。加密层使用 ML-KEM-768 与 X25519 混合握手，支持 X25519 或 ML-KEM-768 服务端认证、0-RTT 票据和重放拒绝。启用加密后 `xtls-rprx-vision` 可与 XHTTP、WebSocket 等传输配合，不要求外层 TLS；未启用加密时沿用原 Vision 的 TLS/REALITY 要求。

例如服务器提供加密参数后，可在已有配置的 `outbounds` 中添加下列节点（占位值必须替换，不是可直接连接的账号）：

```json
{
  "type": "vless",
  "tag": "xhttp-vless",
  "server": "example.com",
  "server_port": 443,
  "uuid": "服务器提供的 UUID",
  "encryption": "mlkem768x25519plus.native.0rtt.服务器提供的客户端密钥",
  "flow": "xtls-rprx-vision",
  "tls": { "enabled": true, "server_name": "example.com" },
  "transport": { "type": "xhttp", "path": "/xhttp", "mode": "auto" }
}
```

节点分享链接识别 `type=xhttp` 和 `type=splithttp`（保存为 `xhttp`）、`host`、`path`、`mode` 与 `extra`；Clash 节点支持 `network: xhttp/splithttp` 和 `xhttp-opts`，VLESS 的 `encryption` 按原值保存，大小写不会改变密钥。Xray/Clash 的 XHTTP 设置转换为核心选项，再统一解码和构造校验；不支持的额外设置明确报告，非法模式、范围或加密参数不能作为可忽略节点静默保存。sing-box JSON 节点直接保留原生字段。所有导入仍只提取节点，保存前检查完整配置，更新只替换对应来源节点。

[XHTTP 传输测试](core/sing-box/transport/v2rayxhttp) 覆盖各模式、真实 HTTP/1.1/2/3 传输、上下行分离、连接复用、取消/关闭/期限、混淆及证书拒绝；[加密测试](core/sing-box/protocol/vless/encryption/encryption_test.go) 覆盖认证/握手、0-RTT、票据过期、重放和 Vision。节点解析、实际 Android AAR 的配置及来源回归由统一测试工作流运行。这些回归及源码来源不代替与外部 Xray 服务的互通或 ARM64 实机验收，实际运行结果见 [应用验证记录](app.md#当前验证记录)。

HTTP/1.1 stream-up 的 POST 结束后，隧道读取可能遇到 Go 的 [http.ErrBodyReadAfterClose](https://github.com/golang/go/blob/go1.27.0/src/net/http/transfer.go#L825)。XHTTP 用 `errors.Is` 将该标准错误及其包装链归类为正常关闭，保留原始错误与消息；同文案普通错误不匹配。分类回归覆盖直接/包装值和同文案反例，现有各 HTTP 版本的 E2E 关闭断言保持原语义。

主内核保留 MiChongs/sing-box 的 GPL-3.0-or-later 许可及 Xray-core 的 MPL-2.0 来源；原始许可随应用打包并通过许可证页展示：[MiChongs/sing-box](flutter/assets/licenses/michongs-sing-box-LICENSE)、[Xray-core](flutter/assets/licenses/xray-core-LICENSE)。

### Cloudflare WARP MASQUE

B-box 的 WARP 注册支持选择 MASQUE 或 WireGuard：MASQUE 保存为 `outbounds` 中的 `type: "masque"`，WireGuard 使用上游原生 `endpoints` 中的 `type: "wireguard"`。新增 MASQUE 出站复用当前上游 MASQUE 的 IP 栈、TCP/UDP/ICMP、路由、代理组和网络切换生命周期。上游 `masque-client` / `masque-server` 继续使用标准 CONNECT-IP。WARP MASQUE 使用自己的 `cf-connect-ip` 握手和注册时分配的地址；只将普通 MASQUE 端点改为 WARP 服务器并不能完成认证。

注册与传输核对 Throne 固定提交 [`48273be2870fd2c81059628ff2cc439ac711808e`](https://github.com/throneproj/Throne/tree/48273be2870fd2c81059628ff2cc439ac711808e) 的 [注册流程](https://github.com/throneproj/Throne/blob/48273be2870fd2c81059628ff2cc439ac711808e/core/internal/warp/register.go)、[API 请求](https://github.com/throneproj/Throne/blob/48273be2870fd2c81059628ff2cc439ac711808e/core/internal/warp/api.go)、[WireGuard 配置](https://github.com/throneproj/Throne/blob/48273be2870fd2c81059628ff2cc439ac711808e/src/configs/outbounds/wireguard.cpp)、[MASQUE 配置](https://github.com/throneproj/Throne/blob/48273be2870fd2c81059628ff2cc439ac711808e/src/configs/outbounds/masque.cpp)，以及其 `core/go.mod` 固定的 [throneproj/sing-box `183cf759ed909d9200d2baccab233f3221d034b1`](https://github.com/throneproj/sing-box/tree/183cf759ed909d9200d2baccab233f3221d034b1/transport/warpmasque)。传输范围同时参考 [Cloudflare 官方网络要求](https://developers.cloudflare.com/cloudflare-one/team-and-resources/devices/cloudflare-one-client/deployment/firewall/#masque)。B-box 独立实现所需行为，继续使用官方核心及其已有 TLS、QUIC、HTTP/2、WireGuard 和 IP 栈，不替换整个核心或引入 Throne 的运行时依赖；既有 MASQUE 节点仍保存在 `outbounds`。

[ui_warp.go](core/sing-box/experimental/libbox/ui_warp.go) 的 `NewUIWARPRegistrationTask` 用于本机停服时的设备网络；运行中的 `CommandServer.NewUIWARPRegistrationTask` 使用当前服务路由及既有取消/退出等待边界，不在路由失败后回退直连。两个注册绑定只返回可追加的单节点 JSON，不返回 token、设备 ID 或许可证；同一任务只能调用其中一个绑定一次。任务总时限为 45 秒，响应上限各为 1 MiB，`Close` 取消在途请求，禁止重定向及自动重试非幂等注册。API 错误不包含响应正文或凭据。完整成功响应在取消边界仍返回节点，供 Android 保留待保存项；配置提交继续由调用方复核取消、会话、所选配置与修订。

`Register(name, httpVersion)` 保留 MASQUE 兼容绑定：先向 `https://api.cloudflareclient.com/v0a4471/reg` POST Curve25519/WireGuard 注册，再以返回的 Bearer token PATCH 同一设备为 P-256/secp256r1 MASQUE。`RegisterWireGuard(name)` 只执行初始 POST，直接保留同次注册的 Curve25519 私钥和 WireGuard 响应，不再 PATCH 为 MASQUE。

#### WireGuard 注册端点

WireGuard 输出结构核对当前核心 [`v1.15.0-alpha.13` 的选项](https://github.com/SagerNet/sing-box/blob/824dbcfb4dd0a95921840b1737b950cde590241e/option/wireguard.go)及[端点文档](https://github.com/SagerNet/sing-box/blob/824dbcfb4dd0a95921840b1737b950cde590241e/docs/configuration/endpoint/wireguard.md)。`AddUIConfigNodes` 将注册结果追加到当前所选配置的 `endpoints`，标记为本地节点，并加入可用的 selector/urltest；配置的 DNS、路由、入站、既有端点及分组选择保持原值。接口地址至少一种地址族有效，peer 公钥必须是 Base64 编码的非零 32 字节公钥，`client_id` 必须解码为三个字节；无效响应拒绝生成节点。

| 字段 | 注册结果 |
| --- | --- |
| `type` / `tag` | `wireguard` 与用户输入的节点名称 |
| `address` | 注册接口分配的 IPv4 `/32`、IPv6 `/128` 地址 |
| `private_key` | 同次 POST 使用的 Curve25519 私钥，按 WireGuard 规则 clamp 后保存为 Base64 原始 32 字节 |
| `peers[0].address` / `port` | 注册响应首个 peer 的 `endpoint.host`；缺失时依次使用 `endpoint.v4`、`endpoint.v6`，全部缺失时沿用 Throne 的 `engage.cloudflareclient.com:2408`。拆分主机和端口，IPv6 不保存方括号；已提供但无效的地址或端口拒绝保存 |
| `peers[0].public_key` | 同次注册返回的 WireGuard peer 公钥 |
| `peers[0].allowed_ips` | `0.0.0.0/0` 和 `::/0`，允许被路由至该节点的流量通过隧道 |
| `peers[0].reserved` | 注册节点将 `config.client_id` Base64 解码为三个 JSON 整数字节，保留 `0`–`255` 的值；标准核心格式化可将字节序列规范化为 Base64 字符串，两种表示均按同一字节值解码 |
| `mtu` | `1280`，不申请系统 WireGuard 接口 |

#### MASQUE 出站

注册节点使用 Throne 的 `162.159.198.2:443`：注册接口返回的 `.1` 在 TCP 上是普通 CDN 边缘，不能用于 HTTP/2 MASQUE。注册后的地址、客户端私钥和对端公钥来自同一次账号注册；写入目标与修订、服务应用和失败重试由 [Android 注册流程](app.md#配置服务应用与备份) 维护。

| 字段 | 作用 |
| --- | --- |
| `server` / `server_port` | 当前网络可用的 WARP MASQUE 服务地址，端口默认 `443`；IPv6 地址不加方括号 |
| `private_key` | 已注册 MASQUE 设备的 ECDSA P-256 私钥；支持 PEM、Base64 DER，SEC1 或 PKCS#8 |
| `peer_public_key` | 同一注册结果中的服务端 ECDSA P-256 公钥；支持 PEM 或 Base64 DER SubjectPublicKeyInfo |
| `address` | 同一注册结果的隧道地址，IPv4 使用 `/32`、IPv6 使用 `/128`；每种地址族最多一个，至少一个 |
| `tls` | 使用当前核心的 TLS 选项，须启用；省略整个 `tls` 时默认 SNI 为 `consumer-masque.cloudflareclient.com`，显式 `tls` 沿用核心的 SNI 规则。自定义 SNI 或 `insecure` 仍校验注册公钥；客户端证书/密钥由 `private_key` 派生，拒绝 REALITY、外部客户端证书和与 `peer_public_key` 冲突的证书/固定公钥设置 |
| `http_version` | `0`（默认）或 `3` 优先 QUIC/HTTP/3（UDP），失败后回退 TLS/HTTP/2（TCP）；`2` 仅使用 HTTP/2 |
| `disable_version_fallback` | 禁止传输回退，默认关闭；无 QUIC 的构建在允许回退时使用 HTTP/2 |
| `version` / `server_name` | 兼容先前 B-box 配置；显式 `version` 固定原传输，`server_name` 映射到 TLS SNI，与新字段冲突时拒绝 |
| `mtu` | IP 栈 MTU，默认 `1280`，有效范围 `1280`–`65535` |
| HTTP/2 / QUIC 字段 | 复用当前核心的 `HTTP2Options` / `QUICOptions`；HTTP/3 默认初始包大小 `1242`，启用 datagram |
| 通用拨号字段 | 使用 sing-box 原生 `detour`、`bind_interface`、`domain_resolver` 等，沿用 Android socket 保护 |

可直接通过 [工具页 WARP 注册](flclash.md#配置页与内核差异) 生成节点；已有账号也可填写 [warp-masque.json](examples/warp-masque.json) 后导入。示例的 `2001:db8::2/128` 是文档地址，须替换为注册结果；没有 IPv6 隧道地址时删除该项。usque 配置的 `private_key` 对应这里的 `private_key`，`endpoint_pub_key` 对应 `peer_public_key`，`ipv4` / `ipv6` 加上主机前缀长度后填入 `address`。HTTP/3 和 HTTP/2 使用同一组 MASQUE 设备凭据；WireGuard 的 Curve25519 私钥和 peer key 不能用于这个出站。

每次 TLS 握手使用已注册的私钥签发临时客户端证书；证书更新不替换注册私钥。服务端证书以注册公钥固定校验，缺少公钥、密钥格式错误、重复地址族和失败的 CONNECT 都返回错误。HTTP/3 使用核心受保护的非连接 UDP socket，保留 detour、DNS 和 QUIC 的原生 UDP 能力；发送 WARP 旧 datagram setting，在服务端未声明 extended CONNECT 时仍完成 `cf-connect-ip`，读取并丢弃有界控制 capsule，datagram 容量为 QUIC packet number 预留四字节。HTTP/2 接受 `h2` 或空 ALPN，使用普通 CONNECT 和 WARP 专用头，capsule 内直接传 IP 数据，不添加 HTTP/3 的 context ID。允许回退时 HTTP/3 握手最多等待五秒再尝试 HTTP/2，成功传输用于后续重连；握手期限不会取消已建立的隧道。

注册地址在认证完成后配置给 IP 栈，拨号等待就绪，不等待 WARP 不发送的 `ADDRESS_ASSIGN`；普通 MASQUE 仍保留地址请求/分配。取消握手、停服和网络切换会关闭旧 TCP/QUIC 连接，已成功打开的隧道不被握手期限结束取消；重连沿用上游的退避与服务生命周期。网络切换取消会话时立即撤销其就绪状态，此后的拨号等待新隧道认证完成，不把首批报文写入已取消的旧隧道；上游客户端在重连循环回收旧会话前仍报告就绪，该修正由同一补丁维护，标准 MASQUE 端点同样生效。

[MiChongs fork](core/sing-box/transport/masque) 对干净的匹配核心检查补丁并安装选项、出站和传输，重复安装失败。注册扩展与其他 `ui_*.go` 由共享 [setup-core](.github/actions/setup-core/action.yml) 安装，使统一测试、Android smoke 和 ARM64 Release 使用同一实现；正式 libbox 构建已有 `with_quic`。

[注册回归](core/sing-box/experimental/libbox/ui_warp_test.go) 使用本地 API 替身检查 MASQUE 两阶段与 WireGuard 单阶段注册、已注册私钥对应关系、地址和 reserved 字节、API peer 地址及缺省地址、WireGuard 端点追加/代理组/本地标记/重复保存、完整配置校验、错误状态/非法响应、响应上限、重定向拒绝、关闭取消及完整响应在取消边界的保留；[传输回归](core/sing-box/transport/masque/b_box_warp_test.go) 与 [HTTP/3 回归](core/sing-box/transport/masque/b_box_warp_http3_test.go) 检查握手、公钥固定、空 ALPN、传输回退、报文、失败/取消与重连，以及网络重置后拨号等待替换隧道；[ui_masque_test.go](core/sing-box/experimental/libbox/ui_masque_test.go) 检查配置/格式化及真实 IP 栈经 selector 传输 UDP 和网络重置。Android 配置绑定与节点持久化回归由 [应用文档](app.md#android-仪器测试) 维护。实际运行记录维护在 [app.md](app.md#当前验证记录)；本地模拟服务不代表 Cloudflare 真实网络或注册流程已经验收。

### 运行时内存

`ReadUIRuntimeMemory` 返回 `UIRuntimeMemory`，包含总量、在用/空闲堆、栈、其他运行时内存及 goroutine 数；总量为 `Sys - HeapReleased`。`CollectUIRuntimeMemory` 调用 `debug.FreeOSMemory`。Android 的 `BoxDashboard` 将这些值与同进程 RSS 结合显示；它们不是独立核心进程的系统内存统计。

### HTTP 与 WebDAV

`UIHTTPResponse` 保留 `Status` 与 `Content`，不修改上游 `HTTPRequest.Execute` 的行为。

- `UIWebDAVRequest` 支持 GET、PUT、OPTIONS、MKCOL，使用上游 HTTP 客户端的证书库和 Modern TLS，完整读取请求/响应，不设固定容量或下载时限。
- WebDAV 不跨源转发凭据和正文，不接受 HTTPS 降级；会改变请求方法的重定向交回调用方判断，防止把失败写入报告为成功。
- `UIHTTPGet` 用于匿名 Release 元数据 GET；`UIHTTPGetWithTimeout` 接受可表示为 Go duration 的正整数毫秒，`-1` 不增加期限，响应不设固定容量上限。调用方可传入剩余时限，让一轮查询共享期限。Android 使用同一任务的 `UIHTTPTask.GetWithTimeout`（绑定 `task.getWithTimeout`），使协程取消立即关闭正在等待的请求；静态绑定保留兼容。元数据允许访问重定向后的资产地址，剥离凭据头且禁止 HTTPS 降级。版本查询调度与缓存见 [app.md](app.md#应用版本与应用内更新)。
- `UIHTTPTask.DownloadFile`（绑定 `task.downloadFile`）使用相同 HTTP 客户端和 Modern TLS，以匿名 HTTPS 下载 APK；跨源重定向继续要求 HTTPS，不携带认证或 Cookie。调用方给出已确认大小和私有绝对路径，文件以独占方式创建为 `0600`，不覆盖已存在的文件。64 KiB 缓冲流式写入并报告实际存储进度，最多读取预期大小加一字节以拒绝超长响应；声明大小、实际大小、响应状态或写入失败均删除暂存文件，`Close` 同时中止等待响应头和正文。完成同步写入后返回 `UIHTTPDownloadResult` 的 `Status`、`Size`、`Digest`（SHA-256），不将完整 APK 经绑定或 Dart 传递；Android 的 APK 身份/签名验证由 [应用内更新](app.md#应用版本与应用内更新) 维护。
- `UIProfileGet` 下载订阅原始文本，完整读取实际响应及 gzip 解压后的内容，不设固定容量或总时限；节点解析及按来源配置合并由上述 `ApplyUISubscriptionNodes` 完成。保留 SFA User-Agent、URL 认证、证书库及 Modern TLS；非 HTTP 200 只报告状态并关闭正文。重定向检测重复的 URL 与方法，不按固定次数拒绝。
- Android 订阅/WebDAV 使用 `NewUIHTTPTask` 的 `ProfileGet` / `WebDAVRequest`；协程取消立即调用 `Close`，使正在等待响应头、正文或 TLS 握手的阻塞绑定退出。原静态绑定保留兼容。没有 B-box 的固定 TLS 握手期限；调用方期限与取消继续生效。

[ui_profile_test.go](core/sing-box/experimental/libbox/ui_profile_test.go)、[ui_http_test.go](core/sing-box/experimental/libbox/ui_http_test.go) 和 [ui_download_test.go](core/sing-box/experimental/libbox/ui_download_test.go) 覆盖订阅认证/重定向、超过原容量的声明长度/分块/gzip 正文、WebDAV 大文件、等待响应头/正文时的取消、调用方期限、错误响应不读正文、APK 流式摘要/进度、大小不匹配和失败清理、独占文件创建，以及非法地址和重定向循环；通过统一测试工作流的 `TestUI` 入口运行。

这些接口不使用下述 `UIEgressProbe` 路由注入。需要查询当前 sing-box 出口时必须走探测接口，不能用一般 HTTP 请求替代。

### 出口 IP 和质量查询传输

`NewUIDirectEgressProbe` 创建设备当前网络上的探测；Android 桥接只在本地服务停止时使用它。`CommandServer.NewUIEgressProbe` 绑定当前服务实例，经 `RouteConnectionEx` 和 `b-box-dashboard` 入站上下文使用 sing-box 路由，服务更换后拒绝旧实例请求。这里的入站上下文不是新增配置监听器。

服务持有自身的路由探测集合，创建探测与停服/重载共用生命周期锁。[服务补丁](core/sing-box/option/processing.go) 在关闭核心之前取消这些探测，并等待已登记的 `RouteConnectionEx` 调用退出；探测关闭后不再允许 HTTP 客户端登记新的异步路由调用。这样，已经通过实例检查但尚未进入 DNS/拨号的请求不会在核心关闭后继续访问它。普通 `Close` 发出取消后快速返回，探测在路由退出后才从服务集合移除，因此后台停服仍会等待已经由界面取消的请求，不依赖 Flutter 收到状态通知后的清理时序。

探测与脚本使用同一 [uiRouteLifecycle](core/sing-box/experimental/libbox/ui_route.go)。停止先关闭路由准入并取消，随后停止处理引擎、等待已登记调用退出，最后关闭核心；脚本显式 `policy` 的 `DialContext` 也在此边界内。取消后才成功返回的连接立即关闭。[ui_route_test.go](core/sing-box/experimental/libbox/ui_route_test.go) 检查路由/指定出站退出等待、关闭与登记竞争，以及迟到拨号结果。

`UIEgressProbe` 提供：

- `LookupOutboundIP`：对八个来源错峰竞速，间隔 800 毫秒，不设置固定总时限、并发名额或响应容量上限；每个 HTTP 200 响应先按对应来源解析并校验公网 IP 与国家字段，首个完整解析成功的结果才取消其余请求。解析失败立即继续其他来源，未结束的来源不阻塞后续来源启动。国家字段允许空字符串表示未知。返回 IP、地理信息、来源与查询时间。
- `Get`：供 Flutter 质量查询使用，保留 HTTP 状态及完整正文，调用方可指定可表示的正整数毫秒期限，`-1` 不增加期限；两种查询的重定向都检测循环，不按固定次数拒绝。
- `Close`：取消本次探测的请求和等待中的连接。每个请求独立持有连接与截止时间，不将 IP 竞速连接复用于后续质量查询。

两种探测共用 libbox TLS 证书库；运行中的路由查询失败不能回退到设备网络。远程会话不提供本机出口探测。FlClash 来源、分批质量查询、缓存和界面刷新规则见 [flclash.md](flclash.md)；其中质量解析与缓存主要在 Dart/Android 层，不能将它们误记为 Go 核心补丁。

[ui_egress_test.go](core/sing-box/experimental/libbox/ui_egress_test.go) 用可控的路由退出时机检查探测关闭和服务关闭：取消必须传到请求，普通取消快速返回，停服须等仍在退出的路由结束，关闭中的探测拒绝后续请求；统一测试工作流另以 Go race detector 运行路由、出口探测、HTTP 任务和订阅回归。实际运行按 [应用验证记录](app.md#验证与历史记录) 中的源码 SHA 区分。

### 局域网代理

`ReadUILANProxyListeners` 解析配置及设备接口地址，返回每个 mixed/http/socks 入站的监听地址、端口、接口限制、认证/TLS 标志及可复制端点。省略 listen 按当前上游 TCP 实现解释为 `127.0.0.1`；通配地址转换为设备实际 IPv4/IPv6 地址并遵守 `bind_interface`。端口 0 和独立网络命名空间的监听不作为可用端点展示，IPv6 端点使用方括号。

`SetUILANProxyEnabled` 在回环与通配监听之间切换，开启时保留既有显式局域网地址。保留各入站的端口、tag、认证、TLS、接口限制和配置中的路由。无代理入站且要求开启时，必须提供合法端口；检查配置内端口占用，使用不冲突的 `b-box-lan` tag 新增 mixed 入站。不引入 Mihomo 的全局 `allow-lan` 或 `mixed-port`。

该绑定只返回变换后的 JSON，不负责保存和启动服务。Android 调用方必须继续执行内容修订检查、完整 `Libbox.checkConfig`、原子写入、服务应用及失败恢复；运行时复制端点使用已成功启动的配置快照。

### HTTP/HTTPS 请求调试

[路由钩子](core/sing-box/route/b_box_httpdebug.go) 在 sing-box 完成选路后将需要处理的 TCP 流交给当前处理器，不另开代理端口；CONNECT/SOCKS 握手、嗅探缓存、原目的地址、应用归属和连接 UUID 继续来自 sing-box。Concord 处理 HTTP/1.1、HTTP/2、HTTPS、SSE 与 WebSocket 握手，透传升级后的流；未接管的字节完整回放。原目标拨号复用已选路由，改写目标重新路由，脚本网络同样经过主内核。

Concord 使用 [ExternalRecorder](core/sing-box/experimental/httpdebug/external.go) 把请求、响应及正文投影到原有 B-box 存储；Rust 不再维护第二份 capture body 缓存。每条请求有独立 ID，`metadata.connectionId` 仍关联原客户端连接 UUID；流、请求和处理器 generation 隔离。停止记录封存已有前缀，迟到数据不再更新该记录，重开只从新的请求边界录制。清空记录不清理脚本持久数据或配置，停止服务才结束处理任务和连接。

手动记录设置 version 3 仅含 `packages`、`ports`、`bodyLimit`、`extractHosts`；默认端口 80/443/8080/8443，应用与端口只限定记录范围。每方向正文默认保存 64 KiB，可填写正整数字节数或 `-1` 完整保存；实际传输不按保存前缀截断。Concord 自身的 MITM/正文/脚本限制继续按其原生 `mitm.limits` 处理，与 B-box 磁盘保存限额分别生效。

`QueryUICapture(optionsJSON)` 保留搜索、活动状态、排序、连接 ID、分页窗口和摘要版本；仅编码查询窗口。`SelectUICaptureHAR` 在文件选择前固定全历史匹配 HTTP ID，`ExportUICaptureHARSelection` 按该选择导出，已清空的记录跳过。旧 List/Read/Export 绑定保留。正文预览、编码解压、HAR/cURL、资源错误和完整性继续由 [请求存储](core/sing-box/experimental/httpdebug) 提供；Concord 尚未向宿主投影的 TLS 握手细节保持未知，不填入旧 Go 处理器的推断值。界面与平台调用见 [app.md](app.md#请求抓取与-mitm)。

`CommandServer.StartUICapture` 只启用当前会话记录，不切换处理器或重启脚本调度。`ReadUICaptureStatus` 的 `backend` 标明 Concord 或兼容处理器，`active` 只代表手动记录；规则数量、错误及配置状态来自实际处理会话。旧 Go 实现的非 HTTP TLS/TCP 记录仅用于旧配置的 `mitm.tcp`，不能当作新 Concord 处理器的功能声明。

### 独立 MITM 配置

当前配置入口为 `experimental.processing`，其 `enabled` 统一启用 Concord HTTP 处理；`mitm` 使用 Concord 的 kebab-case 字段，不接受另一个 `mitm.enable` 开关。定义和保存校验见 [Config](core/sing-box/experimental/concord/config.go) 及 [Concord MitmSection](core/concord/crates/config/src/mitm/mod.rs)。

| 字段 | 当前行为 |
| --- | --- |
| `mitm.hostname` / `hostname-disabled` | Concord Host List，保留顺序、排除、通配符和端口；空允许列表不解密 HTTPS |
| `mitm.decrypt-tls` | 可单独关闭 TLS 解密并保留明文 HTTP 处理；默认沿用 Concord |
| `mitm.h2` | 是否向客户端协商 HTTP/2；上游独立协商，默认沿用 Concord |
| `mitm.skip-server-cert-verify` / `skip-server-cert-verify-hostname` | 全局或指定主机跳过上游信任校验；未开启时由当前 libbox RootPool 验证证书链、域名和有效期 |
| `mitm.auto-quic-block` | 对实际匹配的 QUIC 流使用 Concord 的回退判断，无法恢复名称的其他 UDP 保留 |
| `mitm.client-source-address` | Concord 原生源地址条件，应用包筛选继续属于手动记录设置 |
| `mitm.ca-fingerprint` | 校验当前 CA 身份，不携带根私钥 |
| `mitm.limits` | 原生正文、证书缓存、并发会话及脚本预算；设置及缺省值由 Concord 解析器和运行时决定 |

CA 仍由 Android 主动生成和安装。[外部签发器](core/sing-box/experimental/httpdebug/external_authority.go) 使用 Android Keystore P-256 根签名回调，为 Concord 提供叶证书链和叶私钥；根私钥不交给 Rust，不写入配置或备份。Concord 负责 TLS 接管与 HTTP 转发，默认上游验证使用当前 libbox 的可信 CA 池，不另建弱校验路径。TLS/HTTP 兼容边界以当前 Concord 源码为准；旧配置专用的 `tcp`、`ports`、`packages` 不能按同名开关自动搬到新 MITM。

[示例](examples/concord-processing.json) 展示处理器配置。旧 `experimental.mitm` 及 [旧示例](examples/mitm.json) 继续由 Go 兼容处理器解析；包含 `experimental.processing` 时，必须先关闭旧 MITM、改写和脚本，即使新处理开关暂时关闭也不能保留已启用的旧处理器。Flutter 提供完整候选配置审阅，保留旧规则原文，不自动转换语义不同的字段。

### JSON 配置中的 HTTP 改写

新规则位于 `experimental.processing.mitm` 的 `url-rewrite`、`header-rewrite`、`body-rewrite`、`map-local`、`request-body-mock` 字符串列表，复用 [Concord 规则解析与执行](core/concord/crates/mitm/src/rules.rs)。支持 URL 改写/拒绝/重定向、请求及响应头改写、正文替换/jq、本地响应及请求正文 Mock；规则顺序、匹配、参数和错误由原生预览及完整配置校验反馈。

URL 改写到其他主机或端口后，宿主对实际新目标重新执行 sing-box 路由；原连接的应用及入站归属保留。原目标继续使用原选路，不把模块的 Clash policy 当作 sing-box 出站。Map Local 的文本、文件和 URL 内容由共享资源加载器提供，命名资源随配置保存。HTTP 页面直接编辑这些原生列表和完整 JSON，不再用旧 Map Remote 对象伪装 Concord 语法。

旧 `experimental.http_rewrite` 和 [旧 JSON 示例](examples/http-rewrite.json) 由 [Go 兼容解析器](core/sing-box/experimental/httpdebug/rewrite_config.go) 执行，仅服务于已有配置。它的 Map Remote、独立正文限额和 Go jq 设置不自动转换为新规则。

### 模块

`processing.mitm.modules` 是按顺序保存的模块数组，每项至少包含唯一 `name` 和内嵌 `text`，可带 `enabled`（缺省 true）、`arguments` 与 `attachments`。`module_urls` 保存下载来源；导入时先下载/读取正文，再用原生 `ReadUIProcessingModule` 预览，确认后原子保存完整配置，不在校验期间联网。

解析和合成复用 Concord 的 [Surge/Loon/Quantumult X/Stash 模块](core/concord/crates/config/src/mitm/module) 和 [compose](core/concord/crates/config/src/mitm/modules.rs)。预览包含格式、元数据、参数、声明脚本、附件、行号、有效规则、错误及忽略原因。模块要求不满足或无法解析时，保存阶段拒绝启用；停用模块仍保留正文、要求诊断和实际 `active` 状态。模块行跟在配置自身规则之后，hostname 的插入语义由原生合成实现。

DNS、路由、规则集及网络设置不由 Concord 应用。启用含这些设置的模块默认拒绝；预览确认后可显式选择 `processing.http_only_modules: true`，只应用 HTTP 部分并继续报告未应用内容。`module-policy`、`adopt-resources`、`type=rule` 和 `type=dns` 脚本明确拒绝；模块中的未知/忽略行通过原生诊断展示，不静默改为 Mihomo 配置。

### 文件和 URL 资源加载

`processing.resources` 按名称保存 `data_type: text/file/url` 和 `data`。文本可嵌入 JS、jq 或 Mock；文件必须在当前配置目录内，校验拒绝绝对路径及逃逸，运行时检查符号链接后的真实路径；URL 必须为带主机且无用户凭据的 HTTP(S)。模块附件可映射到同一资源字典。

保存/预览只检查配置结构和内嵌脚本语法，不读本地资源、下载远端文件或执行脚本。启动时将使用的资源写入 Concord 私有资源目录，远端下载通过已启动 sing-box 实例的路由与 RootPool。加载失败阻止启动，并由应用恢复旧配置。当前 Concord 适配未实现旧 Go URL 缓存刷新语义，不能把旧兼容实现的自动刷新当作新处理器能力。

Android 文档导入的原子复制、修订复核、取消及备份范围由 [app.md](app.md#配置处理生命周期) 维护。

### 独立脚本与 Cron

新脚本使用 `processing.mitm.script` 中的 Concord 原生脚本行和 [QuickJS 运行时](core/concord/crates/mitm/src/quickjs)。支持 HTTP 请求/响应、通用任务、Cron 及 engine-started、profile-reloaded、network-changed 事件；语法、参数、正文模式和限制由原生检查及运行时决定。脚本资源使用共享 `resources`；内嵌 JS 在保存前编译检查，不执行。

HTTP 脚本通过 Concord 请求/响应对象、`$done`、异步 HTTP 客户端、持久存储、通知和定时器运行，binary-body 及响应 trailers 按原生语义处理。脚本/资源网络请求均由宿主 sing-box 拨号；通知转为原生平台通知并保留日志。Cron 读取 Android 当前时区，重载和默认接口变化发送相应原生事件。记录启停不重建调度；关闭/重载取消在途任务，等待 Rust Tasks、阻塞宿主回调和 Go 路由全部退出，再释放回调 handle。

`CommandServer.RunUIProcessingScript(configContent, name)` 在当前服务实例运行通用/Cron 任务并返回原生输出。Android 先持有配置锁并确认运行快照，核心再次比较处理器启动时的不可变原文；重载后同名脚本不接收旧页面的运行请求。运行需要本机会话及真实 Started 状态，不用离线预览结果冒充执行结果。

旧 `experimental.scripts` 继续使用 [Goja 兼容运行时](core/sing-box/experimental/httpdebug/script.go)。旧字段、正文转换、网络限制和持久数据按原配置保留；新入口不自动引入它，也不复制成第二套 QuickJS 实现。

#### 一元 gRPC 正文脚本

Concord 支持其原生 binary-body 和 HTTP/2 trailers 语义。旧 `grpc_mode` 及空响应快捷动作仅属于 [Go 兼容正文处理](core/sing-box/experimental/httpdebug/grpc_body.go)，不能直接放入新脚本行；用户须按原生脚本格式重新配置。

#### 脚本解压工具

Concord 的解码和脚本宿主由原生运行时提供；旧 Goja 的 `$utils` 解压调用继续在兼容处理器生效。保存或预览不解压外部资源，也不运行用户 JS。

#### 脚本 HTTP 客户端

Concord 的 `$httpClient` 和任务网络通过宿主拨号进入当前 sing-box 路由，TLS 使用当前 libbox 信任池。未接入的旧 Go `network`/`policy` 对象不自动转换；DNS 和路由脚本仍由保存检查拒绝。

#### 脚本持久存储

Concord 的脚本数据位于处理会话私有 `concord/mitm` 目录，持久值不写入配置/备份；旧 Goja 数据和缓存留在各自原目录。清空记录、启停记录、重载或移除规则不清空无关数据。

#### 脚本定时器

定时器、异步回调和 Cron 属于 Concord 会话任务，停止先取消并等待任务和宿主回调退出。旧兼容处理器的 Goja 定时器继续遵循原先生命周期，不在新配置校验中执行。

### 旧抓包配置清理

抓包筛选为应用私有设置。`experimental.http_debug` 已退役，[兼容处理](core/sing-box/experimental/libbox/ui_capture_migration.go) 在解析前删除它，不解释、迁移或执行其中规则/脚本。旧本机 HTTPS 设置不会被提升为可执行配置，原子迁移见 [app.md](app.md#请求抓取与-mitm)。

旧 `http_rewrite.mitm` 和 `http_rewrite.scripts` 会移到平级扩展，保留原来的应用/端口/正文范围及脚本资源；原改写关闭时迁移目标仍关闭。旧 MITM 的默认端口（含空/null 列表）显式保留为 80/443/8080/8443，避免变为任意 TLS 端口；域名空列表不解密。存在旧排除项时关闭该 MITM，待用户重新确认允许域名；排除字段被移除。若平级与嵌套字段同时存在则明确报错，不覆盖任一份配置。其他未知字段继续由核心校验。

打开存储时，[存储清理](core/sing-box/experimental/httpdebug/store_migration.go) 删除旧 `wire/` 中以 64 个十六进制字符 ID 命名的 `.wire` 包/密钥日志及 warning.txt，失败返回错误；不读取内容、不跟随符号链接，保留请求及无关文件，仅删除已清空的专用目录。

## Android 自动重定向兼容

MiChongs 使用 [reF1nd/sing-tun](https://github.com/reF1nd/sing-tun/tree/68a8ad81087d)，effective replacement 为 `v0.9.7-0.20261023142205-68a8ad81087d`；逻辑模块版本与 replacement 由 fork 的 [go.mod](core/sing-box/go.mod) 及 [准备脚本](core/sing-box/b-box/prepare_dependencies.py) 核对。该版本的 `redirect_ingress_discard_android.go` 只接受 hash 类型、16 字节 key、8 字节 value 的系统 map；[AOSP Android 15 的实现](https://android.googlesource.com/platform/packages/modules/Connectivity/+/refs/tags/android-15.0.0_r1/bpf_progs/netd.c) 使用两个接口编号。一加 10 Pro / Android 15 的问题报告返回 `type 1, key 16, value 4`，因此会在自动重定向启动时被上游的布局检查拒绝。不能仅按 Android 版本号选择布局。

[兼容补丁](core/sing-box/b-box/sing-tun-android-ingress.patch) 从 BPF map 元数据读取实际 value 大小，支持单接口编号的 4 字节值及双接口编号的 8 字节值。只处理当前 TUN 的 IPv4 地址，且所有 value 槽位都必须匹配当前 TUN 接口编号；其他 map 类型、key 长度和 value 大小继续报错。保存条目保留原始长度及字节，关闭时只向布局未变且 TUN 接口编号仍匹配的 map 恢复，继续使用 `BPF_NOEXIST` 避免覆盖系统新建条目。不跳过整张系统 map 的校验或清空无关规则。

[prepare_dependencies.py](core/sing-box/b-box/prepare_dependencies.py) 在匹配的 Go 工具链准备完成后运行，核对依赖版本，将 Go 模块缓存中的源码复制到全新 `.core/.b-box/sing-tun/`，检查并应用补丁，通过当前核心的本地 `replace` 引用副本；共享模块缓存不被修改。依赖版本变化或副本已存在时失败，升级核心须重新核对补丁，不能静默沿用。该处理用于统一测试中的核心/Android smoke 和独立 Release 构建，不改变核心标签、应用上游或 FlClash 基线。

[布局回归测试](core/sing-box/b-box/sing-tun/redirect_ingress_discard_linux_test.go) 在 Linux CI 中测试生产代码的布局校验、单/双接口归属和恢复条件，覆盖 4/8 字节、未知布局、其他接口、map 大小变化和 TUN 更换。它不操作系统固定路径的 BPF map；一加设备上的实际启动、联网、停服恢复及与其他 VPN 的交互仍需 Root 实机验证。

## 保留与已撤销的差异

2026-10-25 已回退历史 MOD-013 核心裁剪，恢复上游 NaiveProxy、Tailscale、OpenVPN、Snell、OpenConnect、USBIP、Tor、Tailscale/Taildrop 客户端绑定及原生 Shell/PTY。`libbox-trim.patch` 和裁剪专用校验器已删除，不能恢复旧裁剪标签或继续使用当时生成的裁剪 AAR。

当前 fork 构建器只排除 Android API 21 产物，不裁剪标准核心协议、上游构建标签或已有导出绑定。`ui_*.go` 是对标准核心的新增适配，不能将整个 B-box 核心描述为“与上游完全无差异”。

HTTP 调试的 MITM 域名判断由 [mitm_hosts.go](core/sing-box/experimental/httpdebug/mitm_hosts.go) 的 Host List 匹配器处理，记录设置只按应用、原始端口和协议筛选。无法检查 ClientHello 时，[mitm.go](core/sing-box/experimental/httpdebug/mitm.go) 的 `SelectTLS` 直接更新 `skipped` 计数并记录原因。内部无调用辅助函数的清理不改变这些路径、配置字段或 `ui_*` 导出绑定。

## 构建、同步和验证

本节维护核心的构建入口、工作流配置的回归范围和升级步骤。回归文件的存在和下列描述不是通过声明；实际运行的源码 SHA、Actions 结果和未覆盖范围统一维护在 [app.md](app.md#验证与历史记录)。本机构建限制见 [AGENTS.md](AGENTS.md)。

### 构建与工作流

- [setup-core](.github/actions/setup-core/action.yml) 使用已锁定 fork 工作树和隔离依赖补丁；[build-libbox](.github/actions/build-libbox/action.yml) 使用 JDK 17、版本文件 Go 和 NDK 28.0 构建标准 API 24 AAR，再以 NDK 28.2 构建相同 ABI 的 Concord Rust 处理库。Android 完整标签由 fork 的 `b-box/buildtags/android.txt` 内嵌，无 `ts_omit_*` 裁剪。
- [统一测试](.github/workflows/newui.yml) 的 core job 构建真实 Linux Concord 动态库，再运行完整功能标签的 `TestUI`、真实 C ABI/race、sing-tun map、sing-vmess Vision、XHTTP/VLESS Encryption、MASQUE/WireGuard、路由/出口探测及旧 HTTP/Goja 回归。Linux 完整标签链接使用 [setup-naive](.github/actions/setup-naive/action.yml) 准备的 Chromium 编译器、sysroot 和 lld；Cronet 提交由主内核的 `.github/CRONET_GO_VERSION` 固定并与 replacement 核对，准备方式遵循该版本的 [下游构建说明](https://github.com/MiChongs/cronet-go/blob/d20ca2de32ca9d00a686c03ce7c2ccf28abe8ca8/README.md#linux-build-instructions)。旧处理器测试与新 Concord bridge 测试分别运行，不相互代替。
- 同一工作流的 native-arm64 job 独立构建完整标签 ARM64 AAR；Android job 构建同来源 x86_64 AAR、Flutter 和 APK，运行 JVM、play 编译及四组 smoke。Flutter job 分析/测试新处理页面和 Prism 子模块，并构建 ARM64 Flutter AAR。
- [package_processing.py](tools/package_processing.py) 检查 AAR 与 Concord ABI、ELF machine、LOAD 对齐至少 16 KiB、六个 C ABI 导出及无动态 `libc++_shared`，原子写入同 ABI `libconcord_processing.so`，拒绝重复或错架构库。
- [Release](.github/workflows/build-release.yml) 仅手动触发，独立构建同一锁定核心/Concord/Flutter 的 ARM64 Release，不调用测试或自动发布本次开发改动。APK 必须同时含 libbox 与处理库，签名和发布规则见 [app.md](app.md#构建与发布)。

[真实 bridge 回归](core/sing-box/experimental/concord/session_test.go) 覆盖 QuickJS、HTTP/2、TLS 信任、FD 与取消/关闭；[ui_concord_test.go](core/sing-box/experimental/libbox/ui_concord_test.go) 覆盖保存前校验/兼容排除和运行原文检查；[Concord 定义测试](core/concord/bins/processing/src/definition.rs) 覆盖模块 HTTP scope、要求诊断和结构检查。下面的详细 HTTP/脚本列表主要是旧 Go 兼容处理器的回归，不能当作新 Rust 实现已通过相同行为的证据。

### 回归文件覆盖

下列文件按 core job 的入口分组，描述各文件断言的行为。Linux 上的回归不能代替 Android 系统 CA 模块、Keystore/gomobile 签名或目标应用的实机验收，后者的仪器测试范围见 [app.md](app.md#android-仪器测试)；大样本越过旧阈值只证明不再因该阈值拒绝，不代表 Android 容量测量。

**核心扩展（`./experimental/libbox`，步骤 2；路由、出口、HTTP 和订阅另由步骤 4 以 race 运行）**

- [ui_http_test.go](core/sing-box/experimental/libbox/ui_http_test.go)：WebDAV 状态/内容/重定向及超过旧大小上限的传输，HTTP 任务关闭时取消等待中的响应头与正文，Release 元数据的重定向、大响应、重定向循环和共享期限。
- [ui_profile_test.go](core/sing-box/experimental/libbox/ui_profile_test.go)：订阅请求头保留、超过旧容量的正文、覆盖响应头与正文的期限、错误状态不读正文、重定向循环和非法地址。
- [ui_nodes_profile_test.go](core/sing-box/experimental/libbox/ui_nodes_profile_test.go) 与 [ui_nodes_config_test.go](core/sing-box/experimental/libbox/ui_nodes_config_test.go)：直接追加、重名及重复导入、端点分类、单节点编辑/删除、默认配置、远端刷新只保留本地节点、冲突节点链改名、消失的上游引用拒绝、标记在标准格式化/平台编辑/删除后保留或清除，以及无本地节点时保持超过 16 MiB 的订阅原文。[ui_nodes_default_test.go](core/sing-box/experimental/libbox/ui_nodes_default_test.go) 补充默认 auto 的混合 direct 成员、重复导入、订阅新增及刷新清理，按实际类型区分直连成员与节点名称，以及空组兜底和删除最后节点的恢复。[ui_nodes_headers_test.go](core/sing-box/experimental/libbox/ui_nodes_headers_test.go) 验证普通配置及远端声明的 direct 成员保留，节点追加、合并和改名不会改写自由 HTTP 头内容或把头键误判为出站引用。[ui_nodes_parse_test.go](core/sing-box/experimental/libbox/ui_nodes_parse_test.go)、[ui_nodes_document_test.go](core/sing-box/experimental/libbox/ui_nodes_document_test.go)、[ui_nodes_link_test.go](core/sing-box/experimental/libbox/ui_nodes_link_test.go) 与 [ui_nodes_wireguard_test.go](core/sing-box/experimental/libbox/ui_nodes_wireguard_test.go) 保留 URI、订阅文档和 WireGuard 协议解析覆盖。上述文件描述当前断言范围，实际运行结果以 [应用验证记录](app.md#当前验证记录) 为准。
- [ui_egress_test.go](core/sing-box/experimental/libbox/ui_egress_test.go)：来源解析、公网 IP 校验、竞速并发与取消、失败或解析失败后继续竞速、服务替换、期限和关闭取消路由拨号、停服等待路由退出、不回退直连，以及 IP 胜出后保持质量查询会话。
- [ui_route_test.go](core/sing-box/experimental/libbox/ui_route_test.go)：停服等待路由和指定出站的拨号退出，以及准入与关闭的竞争。
- [ui_lan_test.go](core/sing-box/experimental/libbox/ui_lan_test.go)：监听地址、开关保留原生配置、创建/停用 mixed 入站、新监听冲突与校验。
- [ui_masque_test.go](core/sing-box/experimental/libbox/ui_masque_test.go)：WARP 配置校验/格式化、非法账号拒绝、VPN 示例，以及真实 IP 栈经 HTTP/2 出站传输 UDP 和网络重置；步骤 3 另在 `with_quic` 构建中运行。
- [ui_warp_test.go](core/sing-box/experimental/libbox/ui_warp_test.go)：MASQUE POST/PATCH 与 WireGuard 单 POST、P-256 DER/Curve25519 私钥和分配地址、peer 地址选择及 client_id reserved 数值、原生端点追加/代理组/本地标记/重复保存、完整配置校验、非法响应和错误状态、响应上限、拒绝重定向、任务单次调用及关闭/完成边界取消；步骤 3 另以 `with_quic,with_wireguard` 和 race 运行。无 `with_wireguard` 的基础检查仍验证注册请求及端点选项解码，追加/完整配置子测试在包含 WireGuard 的核心中运行。
- [ui_capture_test.go](core/sing-box/experimental/libbox/ui_capture_test.go) 与 [ui_capture_signer_test.go](core/sing-box/experimental/libbox/ui_capture_signer_test.go)：本地抓包设置、schema 和退役字段迁移；平台签名接口的摘要、算法及返回签名校验。
- [ui_processing_test.go](core/sing-box/experimental/libbox/ui_processing_test.go)：改写 schema 校验与格式化、退役选项不激活新规则、一元 gRPC 与 reject 动作往返、迁移保留范围和端口缺省、脚本能力 schema，以及 Surge 字段格式化时不加载资源。

**WARP MASQUE 传输（`./transport/masque`，步骤 3）**

- [warp_test.go](core/sing-box/transport/masque/b_box_warp_test.go)：HTTP/2 握手、数据报与重连，调用方取消和网络重置下的停滞握手，错误对端公钥与 HTTP 失败，私钥/公钥格式校验，静态账号地址与客户端生命周期，网络重置后撤销就绪并等待替换隧道，以及标准 MASQUE 地址协商不受 WARP 方言影响。
- [warp_http3_test.go](core/sing-box/transport/masque/b_box_warp_http3_test.go)：未声明 Extended CONNECT 设置的 HTTP/3 握手、停滞握手取消和不可用对端拒绝。

**sing-tun 与路由接入（步骤 1 和步骤 5）**

- [redirect_ingress_discard_linux_test.go](core/sing-box/b-box/sing-tun/redirect_ingress_discard_linux_test.go)：map 布局校验、单/双接口归属和恢复条件，范围说明见 [Android 自动重定向兼容](#android-自动重定向兼容)。
- [route/httpdebug_test.go](core/sing-box/route/b_box_httpdebug_test.go)：嗅探缓冲随大请求头增长并完整回放；通过真实 TLS 握手核对原选定出站、SNI 与 DNS 映射隔离、端口覆写、CONNECT 确认顺序、证书库及未匹配流的字节回放；从真实流量跟踪器取得连接 UUID、穿过嗅探缓存、清空连接历史后重新打开请求存储，以及无跟踪器的 HTTP。

**HTTP 调试引擎（`./experimental/httpdebug`，步骤 5，全部以 race 运行）**

抓取、存储与导出：

- [httpdebug_test.go](core/sing-box/experimental/httpdebug/httpdebug_test.go)：域名选择、HTTP 流式正文与原路由、SSE/WebSocket、存储保留/持久化/清空、HAR 与 cURL 导出，以及缺失正文不导出为完整请求。
- [store_concurrency_test.go](core/sing-box/experimental/httpdebug/store_concurrency_test.go)：正文句柄复用和关闭边界、前缀/转发总量、并发双向写入与详情快照，以及模拟慢写时其他记录和清空继续工作、完成状态等待计数提交。
- [store_activity_test.go](core/sing-box/experimental/httpdebug/store_activity_test.go)：摘要关联的持久化、独立正文计数、改写前 URL、旧记录缺省，以及 HAR 选择的去重、顺序、空选择、非法输入、非 HTTP 排除和淘汰后不扩大导出范围；真实 HTTP/1.1 复用连接的用例同时检查摘要与详情的关联一致。
- [store_query_test.go](core/sing-box/experimental/httpdebug/store_query_test.go)：超过 300 条历史的分页与搜索、当前连接和请求字段组合匹配、未加载父连接去重、状态和排序、摘要增量、全历史 HAR 选择、清空后不扩大导出、非法范围与选项，以及查询与录制/清空竞争。
- [store_migration_test.go](core/sing-box/experimental/httpdebug/store_migration_test.go)：旧文件清理、请求保留、符号链接隔离，以及旧记录不将 `captureId` 误认为连接 ID、未知上游 ALPN 保持区分。
- [body_inspection_test.go](core/sing-box/experimental/httpdebug/body_inspection_test.go)：正文分页不拆分 UTF-8 字符、不丢失或重复字节，预览只读取并解码所在页，流式 HAR 和 cURL 保留完整的已存正文及原始压缩数据，快照在追加和清空后保持已固定的前缀。
- [recording_test.go](core/sing-box/experimental/httpdebug/recording_test.go)：在同一 HTTP/1.1、HTTP/2 连接反复启停记录时处理不中断、连接 ID 不变，停止记录冻结 SSE 记录但不断开连接。
- [diagnostics_test.go](core/sing-box/experimental/httpdebug/diagnostics_test.go)：无 HTTP 请求的 TLS 结束提示、处理失败指向既有服务日志，以及停止后的迟到更新隔离。

MITM 与 TLS：

- [mitm_test.go](core/sing-box/experimental/httpdebug/mitm_test.go)：TLS 1.2/1.3、HTTP/1.1/2、同连接请求的独立 ID 与连接关联、上游连接复用、独立信任链、过期/错误名称/不受信任上游、客户端 pinning、未匹配域名和非 HTTP hello 回放、重复 Cookie、请求/响应 trailers、大正文、并发流、SSE 取消、跨 authority 拒绝、QUIC 筛选及停止/拨号超时。
- [authority_test.go](core/sing-box/experimental/httpdebug/authority_test.go)：独立根身份、SAN/IP、叶证书缓存和密钥错配。
- [tls_selection_test.go](core/sing-box/experimental/httpdebug/tls_selection_test.go)：已有 48 条连接时执行真实 ClientHello 探测、TLS 握手及 HTTP Mock 仍接管请求，并验证探测取消、未交接关闭及正式接管后的服务回收。
- [mitm_tcp_test.go](core/sing-box/experimental/httpdebug/mitm_tcp_test.go)：TLS/TCP 二进制、混合 ALPN、服务器先发、半关闭、连接关联及记录/导出边界。
- [mitm_compat_test.go](core/sing-box/experimental/httpdebug/mitm_compat_test.go)：默认配置不降级仅支持 HTTP/2 的 gRPC 客户端，无 ALPN 的 HTTP 仍经过改写、脚本和导出，客户端先发的原始 TLS 字节回放与半关闭，HTTP/2 选项区分客户端和源站版本，上游证书校验默认开启及显式关闭，停服取消无 ALPN 的协议探测。
- [mitm_config_test.go](core/sing-box/experimental/httpdebug/mitm_config_test.go)：Host List 的顺序、通配符、排除和端口匹配，默认值及显式关闭的协议选项在格式化后保留。

正文处理与传输：

- [body_processing_test.go](core/sing-box/experimental/httpdebug/body_processing_test.go)：各编码、多层和重复头在改写、预览与 HAR 中一致；不符合条件的正文不被读取并继续流式转发；正文预留按实际字节登记并在输出后释放，不再有全局配额；显式解码上限和截断上传保持完整性；只读头部的脚本不为原始流保留正文预留。
- [streaming_processing_test.go](core/sing-box/experimental/httpdebug/streaming_processing_test.go)：HTTP/2 gRPC 在上传结束前响应并保留 trailers、`Expect: 100-continue` 上传改写不死锁，以及慢上传取消后释放预留。
- [grpc_body_test.go](core/sing-box/experimental/httpdebug/grpc_body_test.go)：通过真实 HTTP/2 MITM 检查一元 RPC 双向压缩消息改写、长度更新及请求/响应 trailers；gRPC-Web 二进制/Base64 填充、损坏及多消息跳过、无操作原字节保留、输出校验和取消释放。
- [upload_rejection_test.go](core/sing-box/experimental/httpdebug/upload_rejection_test.go)：使用真实 TCP 连接，固定长度/chunked 上传超过显式上限且停止发送时，HTTP/1 及时返回完整 413、关闭连接并完成记录及预留释放；未读取正文就发生的脚本错误返回 502；HTTP/2 返回 413 后同一连接仍可处理新请求。
- [transport_limits_test.go](core/sing-box/experimental/httpdebug/transport_limits_test.go)：慢上传越过旧正文期限后不中止，仍完成改写。
- [processing_limits_test.go](core/sing-box/experimental/httpdebug/processing_limits_test.go)：大规模配置、MITM 范围、改写与脚本数量不受旧数量或大小上限限制，长正则与大量分组可用，jq 无输出数量/嵌套上限并响应调用方取消，记录保留大正文和全部历史直至显式清空。

改写、资源与 Surge 兼容：

- [rewrite_test.go](core/sing-box/experimental/httpdebug/rewrite_test.go)：配置校验、改写管线与记录内容、HTTPS 离线 Mock 及停止记录后继续处理、映射地址拨号与 TLS 校验、超限和流式响应保持原样、脚本隔离/二进制/错误、正文上限与 HTTP 分帧、Upgrade 保留协议并跳过正文、替换与分组，以及跨目标改写保留原始连接 ID。
- [surge_rewrite_test.go](core/sing-box/experimental/httpdebug/surge_rewrite_test.go)：四种前后查找、数字/命名反向引用及 Unicode 替换位置，原生正则的取消与并发使用，URL 候选、WebSocket、网关重定向和非法目标，jq 正文规则及失败时保留原报文，jq 先于脚本执行，正文大小缺省值与非标准 Mock 状态码。
- [surge_compatibility_test.go](core/sing-box/experimental/httpdebug/surge_compatibility_test.go)：空正文与参数缺省、会话信息、`$utils.ungzip` 的子视图/失败/预留释放、调试备注、空响应快捷动作，以及 HTTP/1 和 HTTP/2 的脚本 abort；HTTP/2 中核对后续请求仍复用连接。
- [surge_resources_test.go](core/sing-box/experimental/httpdebug/surge_resources_test.go)：内嵌资源的内容类型，配置校验不加载文件或下载资源，Map Local 读取配置目录内文件并拒绝目录逃逸、FIFO 等非普通文件，URL 资源缓存、并发去重和离线重载，远程脚本自动刷新并保留上次可用版本，本地脚本的 `debug` 重载及加载失败时透传上游，刷新随服务停止。
- [resource_eviction_test.go](core/sing-box/experimental/httpdebug/resource_eviction_test.go)：缓存清理只回收未引用且由缓存拥有的文件，写入超过旧磁盘配额仍可提交，一个来源停滞时其他资源继续刷新并在关闭时取消。

脚本：

- [script_api_test.go](core/sing-box/experimental/httpdebug/script_api_test.go)：能力校验不执行脚本、两阶段 HTTP 客户端的路由注入、未配置目标与 Host 覆写拒绝、重定向/正文/请求次数的显式限制、不回退并校验 TLS、取消后释放解释器与正文预留，以及只读头部的脚本 HTTP 不受旧全局正文配额影响。
- [script_admission_test.go](core/sing-box/experimental/httpdebug/script_admission_test.go)：HTTP 准入先于正文读取和序列化、复制到宿主前检查 UTF-8 与视图长度、原生正则取消按 VM 隔离，以及 HTTP 脚本无固定 VM 栈或日志配额。
- [script_storage_test.go](core/sing-box/experimental/httpdebug/script_storage_test.go)：持久化、命名空间隔离与删除、超过旧配额的写入、并发写入与取消的锁等待、原子失败和中断写入恢复、损坏与符号链接拒绝且不丢数据、HTTP/Cron 共享且无调用次数配额，以及重载/清空记录后保留。
- [script_timers_test.go](core/sing-box/experimental/httpdebug/script_timers_test.go)：顺序、参数、interval 清理与 `$done`，延迟校验和取消，以及 Cron 定时器使用同一调用期限。
- [script_source_test.go](core/sing-box/experimental/httpdebug/script_source_test.go)：执行含 256 KiB 字符串的请求、响应及 Cron 脚本，覆盖内联、text/base64 资源、相对文件、URL、资源引用、压缩及多层下载和离线缓存；源码加载登记共享正文预留，加载失败后释放。
- [processing_resources_cache_test.go](core/sing-box/experimental/httpdebug/processing_resources_cache_test.go)：使用实际磁盘、HTTP 下载和 Goja 编译结果，检查同 URL 并发加载、离线重载、按内容哈希复用编译结果、`debug` 绕过、刷新失败保留上次可用程序，以及取消源码限额后仍可加载既有版本写入的缓存。
- [script_http_encoding_test.go](core/sing-box/experimental/httpdebug/script_http_encoding_test.go)：通过真实 HTTP 服务器及 Cron 路由客户端，覆盖显式/未提供 `Accept-Encoding` 时的所有支持编码、重复/多层编码、压缩前后及中间层的显式上限、损坏/截断/未知编码、UTF-8 校验、HEAD/204/304 元数据和容量释放。
- [surge_script_http_test.go](core/sing-box/experimental/httpdebug/surge_script_http_test.go)：超过旧并发/排队上限的请求、重定向/Cookie/JSON/二进制/`policy`、连续 20 次以上调用、跳过证书校验与单次请求期限，以及脚本异常或 `$done` 后取消未完成的请求。
- [script_resources_test.go](core/sing-box/experimental/httpdebug/script_resources_test.go)：多个 Cron HTTP 调用同时等待时互不阻塞，HTTP 脚本仍立即返回；取消使这些 Cron 调用失败并释放解释器与正文容量。

Cron：

- [script_cron_test.go](core/sing-box/experimental/httpdebug/script_cron_test.go)：脚本独立校验且校验不执行 Cron，Cron 语法/时区/DST，HTTP 回调与证书校验，仅含 Cron 的配置不拦截 HTTP，记录开关不重建调度，停服取消在途请求。
- [script_cron_queue_test.go](core/sing-box/experimental/httpdebug/script_cron_queue_test.go)：通过阻塞路由拨号控制脚本完成顺序，检查不同任务并行运行、同一任务未完成时的重复触发合并且不重叠、单次超时不影响其他任务和后续调度，以及关闭时取消并等待全部调用、不保留待运行任务。

XHTTP 与 VLESS Encryption 的独立传输、加密与 race 回归由同一 core job 分别在 `with_xhttp` 和 `with_xhttp,with_quic,with_utls` 下运行 `./transport/v2rayxhttp ./protocol/vless/encryption`。同一步骤以 `with_utls` 和 race 运行 sing-vmess 的 `^TestBboxVision`，检查标准/uTLS 缓冲及非法字段布局。依赖准备脚本对当前固定 sing-vmess 版本应用最小指针补丁，副本位于 `.core/.b-box/sing-vmess` 并由本地 replace 引用；模块缓存、依赖来源及版本不变，保留 Go `checkptr` 检查。具体来源及补丁范围见 [fork 集成说明](core/sing-box/b-box/README.md#xhttp-与-vless-encryption)；传输范围见 [XHTTP 与 VLESS Encryption](#xhttp-与-vless-encryption)。

### 升级核心

升级 MiChongs 时固定新的原始比较 SHA，在 fork 合并并审查源码差异，核对单 AAR/全部标签、effective replacements、隔离依赖补丁、全部 UI 扩展、路由生命周期、Concord C ABI 与生成绑定；推送可拉取的 fork 提交后更新根仓库 gitlink 和 upstreams.json。Concord 与 Prism 分别更新，不自动改变 Android 或 FlClash 基线。Go 导出名与 gomobile 生成的 Java 方法名不能凭拼写推断，例如 `UIHTTPGet` 当前由 Kotlin 调用为 `Libbox.uihttpGet`。任何扩展增删都要核对 fork 源码、绑定和完整构建范围，以及统一测试与 Release 两条工作流的调用。
