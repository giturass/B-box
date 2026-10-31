# libbox 与上游的差异

本文记录核心补丁和新增绑定；项目约束见 [AGENTS.md](AGENTS.md)，Android 调用及数据持久化见 [app.md](app.md)，FlClash 查询规则和界面适配见 [flclash.md](flclash.md)。

## 基线与集成方式

上游为 [SagerNet/sing-box](https://github.com/SagerNet/sing-box)，绑定位于 `experimental/libbox`。当前基线是 [`v1.15.0-alpha.13`](https://github.com/SagerNet/sing-box/tree/v1.15.0-alpha.13/experimental/libbox)，Go 为 `go1.26.10`；实际版本由 [version.properties](version.properties) 的 `VERSION_NAME` 和 `GO_VERSION` 决定，工作流检出 `v${VERSION_NAME}`。

本仓库不存储完整核心或 AAR，构建补丁与扩展源码统一保存在 [libbox/](libbox/)。CI 检出上游至 `.core/`，应用构建补丁、sing-tun 兼容补丁和 HTTP 调试补丁，安装抓取引擎、路由适配及五个 `ui_*.go` 生产扩展，构建标准 `libbox.aar` 供 Android 使用。仅有上游原版 AAR 不足以编译当前 B-box，因为它没有下面的 UI 扩展绑定，也不包含本仓库的路由和自动重定向兼容补丁。

| 差异 | 文件 | 作用 |
| --- | --- | --- |
| 排除 legacy 产物 | [libbox-no-legacy.patch](libbox/libbox-no-legacy.patch) | 修改上游 `cmd/internal/build_libbox/main.go`，删除 API 21 AAR 分支及只供该分支使用的 `filterTags` |
| Android 自动重定向兼容 | [sing-tun-android-ingress.patch](libbox/sing-tun-android-ingress.patch) | 兼容 ingress discard BPF map 的 4/8 字节 value，保留接口归属检查和原值恢复 |
| 运行时内存绑定 | [ui_dashboard.go](libbox/ui_dashboard.go) | 读取 Go 运行时内存和 goroutine 数，提供释放内存操作 |
| HTTP/WebDAV 绑定 | [ui_http.go](libbox/ui_http.go) | 返回 HTTP 状态码和有大小上限的响应，供备份及 Release 元数据使用 |
| 出口探测绑定 | [ui_egress.go](libbox/ui_egress.go) | 可取消的设备网络/运行中路由探测、出口 IP 多源查询与质量请求传输 |
| 局域网代理配置绑定 | [ui_lan.go](libbox/ui_lan.go) | 读取 mixed/http/socks 监听端点、修改 listen 或显式新增 mixed 入站 |
| HTTP/HTTPS 调试与改写 | [httpdebug.patch](libbox/httpdebug.patch)、[httpdebug/](libbox/httpdebug/)、[route/](libbox/route/)、[ui_capture.go](libbox/ui_capture.go) | 抓取、MITM/绕过、域名提取、Header/URL/Body Rewrite、Map Local 与 JavaScript；提供 JSON 配置、记录及导出绑定 |

## 新增接口与行为

### 运行时内存

`ReadUIRuntimeMemory` 返回 `UIRuntimeMemory`，包含总量、在用/空闲堆、栈、其他运行时内存及 goroutine 数；总量为 `Sys - HeapReleased`。`CollectUIRuntimeMemory` 调用 `debug.FreeOSMemory`。Android 的 `BoxDashboard` 将这些值与同进程 RSS 结合显示；它们不是独立核心进程的系统内存统计。

### HTTP 与 WebDAV

`UIHTTPResponse` 保留 `Status` 与 `Content`，不修改上游 `HTTPRequest.Execute` 的行为。

- `UIWebDAVRequest` 支持 GET、PUT、OPTIONS、MKCOL，使用上游 HTTP 客户端的证书库和 Modern TLS；请求/响应内容上限 64 MiB，超时 30 秒。
- WebDAV 不跨源转发凭据和正文，不接受 HTTPS 降级；会改变请求方法的重定向交回调用方判断，防止把失败写入报告为成功。
- `UIHTTPGet` 用于匿名 Release 元数据 GET，上限 2 MiB，超时 15 秒。允许访问重定向后的资产地址，剥离凭据头且禁止 HTTPS 降级。

这些接口不使用下述 `UIEgressProbe` 路由注入。需要查询当前 sing-box 出口时必须走探测接口，不能用一般 HTTP 请求替代。

### 出口 IP 和质量查询传输

`NewUIDirectEgressProbe` 创建设备当前网络上的探测；Android 桥接只在本地服务停止时使用它。`CommandServer.NewUIEgressProbe` 绑定当前服务实例，经 `RouteConnectionEx` 和 `b-box-dashboard` 入站上下文使用 sing-box 路由，服务更换后拒绝旧实例请求。这里的入站上下文不是新增配置监听器。

`UIEgressProbe` 提供：

- `LookupOutboundIP`：对八个来源错峰竞速，总时限 6 秒、间隔 800 毫秒、最多并发 3 个请求、响应上限 4096 字节；每个 HTTP 200 响应先按对应来源解析并校验公网 IP 与国家字段，首个完整解析成功的结果才取消其余请求。解析失败立即继续其他来源，国家字段允许空字符串表示未知。返回 IP、地理信息、来源与查询时间。
- `Get`：供 Flutter 质量查询使用，保留 HTTP 状态，响应上限 128 KiB，调用方可指定 1～8000 毫秒超时。
- `Close`：取消本次探测的请求和等待中的连接。每个请求独立持有连接与截止时间，不将 IP 竞速连接复用于后续质量查询。

两种探测共用 libbox TLS 证书库；运行中的路由查询失败不能回退到设备网络。远程会话不提供本机出口探测。FlClash 来源、分批质量查询、缓存和界面刷新规则见 [flclash.md](flclash.md)；其中质量解析与缓存主要在 Dart/Android 层，不能将它们误记为 Go 核心补丁。

### 局域网代理

`ReadUILANProxyListeners` 解析配置及设备接口地址，返回每个 mixed/http/socks 入站的监听地址、端口、接口限制、认证/TLS 标志及可复制端点。省略 listen 按当前上游 TCP 实现解释为 `127.0.0.1`；通配地址转换为设备实际 IPv4/IPv6 地址并遵守 `bind_interface`。端口 0 和独立网络命名空间的监听不作为可用端点展示，IPv6 端点使用方括号。

`SetUILANProxyEnabled` 在回环与通配监听之间切换，开启时保留既有显式局域网地址。保留各入站的端口、tag、认证、TLS、接口限制和配置中的路由。无代理入站且要求开启时，必须提供合法端口；检查配置内端口占用，使用不冲突的 `b-box-lan` tag 新增 mixed 入站。不引入 Mihomo 的全局 `allow-lan` 或 `mixed-port`。

该绑定只返回变换后的 JSON，不负责保存和启动服务。Android 调用方必须继续执行内容修订检查、完整 `Libbox.checkConfig`、原子写入、服务应用及失败恢复；运行时复制端点使用已成功启动的配置快照。

### HTTP/HTTPS 请求调试

[patch_httpdebug.py](tools/patch_httpdebug.py) 对匹配核心应用配置、路由和服务生命周期补丁，将 `libbox/httpdebug/` 安装到 `experimental/httpdebug/`、路由适配安装到 `route/b_box_*.go`，并复制 `ui_capture.go`。已有覆盖文件时拒绝重复安装。补丁在路由选择、出站组解析和连接跟踪之后接管符合条件的新连接；没有改写目标地址时，上游连接继续使用原来的 `ConnectionHandler` 或连接管理器，保留目的地址、路由选项、出站链和应用归属，不新增监听器或独立直连回退。补充嗅探只修改元数据副本，不匹配的已读字节回放到原出站。抓取关闭时路由保持原行为；启用后候选流走连接路径，显式 bypass 规则仍保留。改写目标地址后的选路见下节。

HTTPS 默认关闭。开启后，**HTTPS 域名白名单为空表示不限制解密域名**；填写后只解密匹配的域名。精确域名和 `*.example.com` 分别匹配本域和子域，通配符不包含主域；排除域名优先，通用抓取域名筛选同时约束 HTTP 与 HTTPS。`decryptBypassHosts` 只绕过 MITM，`excludeHosts` 排除全部抓取及域名提取。应用选择为空表示所有归属，非空时未知归属不匹配，共享 UID 按包名集合判断。默认端口为 80、443、8080、8443，可选择最多 32 个端口。

TLS 解密使用每个安装独立的 CA，签发带 SAN 的 ECDSA P-256 叶证书，叶证书缓存最多 128 项；上游继续使用当前 libbox 根证书池验证真实服务端，失败不会跳过验证。支持 HTTP/1.1、TLS ALPN 的 HTTP/2 及流式请求/响应；SSE 及时转发，WebSocket 只记录 HTTP 升级握手，后续帧双向透传。合并到同一 TLS 连接但域名不同的请求返回 421，要求客户端另建连接，避免越过域名选择。CA 的 Android 保存及系统导入见 [app.md](app.md#请求抓取与调试证书)。

记录保留最近 300 条，磁盘总量上限 32 MiB；每条请求和响应各保存正文前缀，默认 64 KiB、最大 1 MiB，记录头上限 64 KiB。完整正文持续转发，不为录制缓存整个下载；最多同时接管 32 条连接，每条 HTTP/2 连接最多 16 个流，超过连接容量的新连接继续走原出站。停止抓取关闭已接管连接并等待处理器退出；服务重载、关闭及进程重启后不自动恢复抓取。记录可在停服时查看，进程重启将未结束记录标记为中断，并核对正文文件实际长度。

正文预览和 HAR 中的压缩正文解码支持 gzip/deflate，展开上限 64 KiB；其他编码或二进制可查看 Base64。HAR 保留原始压缩前缀于扩展字段，标准 `content.text` 只放解码结果，无法解码时省略；截断、完整性和未知大小均有标记。耗时仅记录总时长，HAR 的 `wait` 使用总时长并明确标注 send/receive 未测量，不表示 DNS/TCP/TLS 分阶段计时。cURL 仅导出完整且未截断的 HTTP 请求头/正文，使用 shell 引用和 Base64 管道保留正文；TLS 失败和正文文件缺失时拒绝生成。没有录制 HTTP trailers 或 WebSocket 帧，也不提供录制后重放；转发时的改写见下节。

`extractHosts` 启用后，MITM 关闭或绕过的 TLS、可见 QUIC ClientHello 也可记录“仅域名”条目；不伪造 HTTP 头部、正文或响应状态。域名提取合并当前搜索条件下全部保留记录的原始/改写 URL 与观察域名，去重排序，可复制或导出文本。仅域名条目受同一存储限额约束，清空记录同时移除它们；HAR 排除这类非 HTTP 条目。无法恢复 ECH 等隐藏的信息，也不表示已完成 TLS 解密。

可选 QUIC 阻断只作用于已观察到 ClientHello 域名、且匹配 HTTPS 解密及应用/端口筛选的连接，尝试促使客户端回退到 HTTP/2 或 HTTP/1.1；未知域名和不完整的嗅探数据仍回放。HTTP/3 不解密，不能保证客户端一定回退。证书固定、自有证书库、mTLS、ECH 和不兼容的 TLS 协议可能需要排除目标域名。

`CommandServer.StartUICapture` 必须绑定运行中的本地实例，并与服务重载/关闭串行；其余 `Open/Validate/Stop/Read/List/Clear/ExportUICapture*`、`ExtractUICaptureHosts` 绑定提供配置校验、状态、列表、详情、清空和导出。Android 只读取有界预览，原始正文通过文件导出，不在轮询桥接中传送。

### HTTP 改写配置

在原 sing-box JSON 的 `experimental.http_debug` 下保存抓包与改写设置，完整示例见 [http-debug.json](examples/http-debug.json)。合入原有配置时只合并该对象，保留自己的入站、出站、DNS 和路由；示例不是 VPN 入站模板。`httpdebug.patch` 向当前核心的 `option.ExperimentalOptions` 添加真实类型，[配置适配](libbox/option/httpdebug.go) 安装到 `option/b_box_httpdebug.go`，复用引擎的严格 JSON 校验并实现上游 `schema.Describer`；`Libbox.checkConfig`、格式化与 schema 生成均识别该对象。`ReadUICaptureConfig`/`SetUICaptureConfig` 提供读取/变换，Android 仍负责完整校验、修订检查和原子保存。没有该对象的旧配置可读取原本机抓包设置作为初始值，显式保存后使用配置中的值。

MITM 使用 `https`、`decryptHosts` 和 `decryptBypassHosts`；公开 CA 与私钥不放进 JSON。规则总开关为 `rewriteEnabled`，默认关闭；规则仅在用户启动本地抓包后生效，配置加载不自动抓包。规则均支持 `name`、完整 URL 正则 `pattern` 及 `disabled`。最多 128 条规则，其中脚本最多 16 条；设置对象最大 4 MiB。

| 数组 | 规则字段与行为 |
| --- | --- |
| `headerRewrite` | `type` 为 `http-request`（默认）或 `http-response`；`action` 支持 `header-add`、`header-del`、`header-replace`、`header-replace-regex`，后两种只作用于已有头部。`header` 指定名称，`value` 为值或匹配正则，正则替换另用 `replacement`。保留重复头部；不能修改 Content-Length、Transfer-Encoding 或 Trailer 的消息边界 |
| `urlRewrite` | `type` 默认 `header`，透明改写并更新 Host；也支持 `301`/`302`/`307`/`308` 重定向及 `reject`（本地 403）。`replacement` 为绝对 HTTP(S)/WS(S) URL；首条匹配规则生效，替换支持捕获组 |
| `bodyRewrite` | `type` 指请求或响应（默认请求），`regex`/`replacement` 对 UTF-8 正文依次替换；`^`/`$` 按行匹配，支持空正文生成内容。无效 UTF-8 跳过并记入说明 |
| `mapLocal` | 首条匹配规则直接返回本地响应；`dataType` 为 `text`（默认）、`base64` 或 `tiny-gif`，`data` 保存内容，`status` 默认 200、范围 200–999，`headers` 是 `name`/`value` 数组。文件由 Android 选择并嵌入 Base64，不读取任意路径、不自动下载 URL；单条内容最多 1 MiB |
| `scripts` | `type` 指请求或响应，`source` 为本地 JavaScript 源码，`argument` 对应 `$argument`；`requiresBody` 默认 false，`binaryBodyMode` 使用 Uint8Array，`fullHeaderMode` 使用 `{field,value}` 数组保留重复头部。`maxSize` 为 1–1048576 字节，`timeoutMs` 为 10–3000 毫秒，默认分别 1 MiB / 1000 毫秒 |

处理顺序参考 Surge 的 [HTTP Processing](https://manual.nssurge.com/http/overview.html)、[Header Rewrite](https://manual.nssurge.com/http/header-rewrite.html)、[URL Rewrite](https://manual.nssurge.com/http/url-rewrite.html)、[Body Rewrite](https://manual.nssurge.com/http/body-rewrite.html) 与 [Map Local](https://manual.nssurge.com/http/map-local.html)：请求按 Header → URL → Body → Script 执行，再检查 Map Local；重定向、拒绝和脚本本地响应提前结束上游请求。响应执行 Header → Body → Script，每个方向只运行首个匹配脚本。脚本通过 `$request`、`$response`、`$script`、`$argument`、`console.log` 和 `$done()` 工作，参见 Surge 的 [http-request](https://manual.nssurge.com/scripting/http-request.html) / [http-response](https://manual.nssurge.com/scripting/http-response.html) 契约。`$done({})` 保留报文，返回 headers 替换全部头部，body 替换正文，response 可直接返回本地响应，abort 终止请求。请求脚本返回 url 时保留 Host，需同时返回 headers 才修改它；另支持显式返回 method。响应可修改 status。调用 `$done()` 后立即结束，未调用、脚本异常和超时记入抓包错误。

脚本使用固定 [Goja `58d95d85e994`](https://github.com/dop251/goja/tree/58d95d85e994)（MIT 许可），由安装脚本在三条 CI 工作流中解析依赖；该版本不要求升级核心现有的 x/text。每次调用独立运行时，源码上限 128 KiB、调用栈上限 256；正文处理与脚本全进程最多同时运行两项，排队最多三秒。仅提供同步报文 API，没有网络、文件、持久化存储、定时器、cron 或配置覆写能力；不自动下载/执行远程脚本。超时中断 JavaScript，输入/输出有大小限制，但这不是具有独立进程及硬堆内存配额的通用代码沙箱。

正文改写与 `requiresBody` 才缓冲完整正文，压缩解码支持 gzip、deflate 和 Brotli；原始与解码后内容均限制 1 MiB。超大请求失败；超大响应回放已读原始前缀并继续完整透传，跳过需要正文的处理。SSE、gRPC、升级、HEAD、204/304 与部分响应不改写正文；请求 chunked/Expect 情况保留正文，脚本仍可改写头部。正文改变后重算长度并清理压缩、分块、trailer 和旧摘要/ETag；录制限额仍独立，详情与导出记录有效报文、原始 URL 及有界规则说明。本地响应未读取/转发的请求正文保持未完成标记。

目标主机或端口改变后，使用 `RouteConnectionEx` 重新执行 sing-box 路由，保留入站、源地址及应用归属，清除旧目标的 DNS/规则状态，并阻止再次进入抓包引擎；不回退为直连。新 HTTPS 目标继续验证证书。详情保留原始 URL/目标，但清空未观察到的新出站及命中规则，避免把原连接的选择当作改写后的路由。

这里实现的是 B-box JSON 扩展，不读取 Surge INI 配置。域名筛选沿用前述精确/子域通配规则；配置正则使用 Go RE2，不支持模式内反向引用和环视，单次正文替换最多 16384 个匹配，并限制捕获组索引及展开长度。未实现 jq、Surge 的完整 Host List 语法、远程资源更新或通用脚本 API；不声称兼容任意 Surge 配置或脚本。

## Android 自动重定向兼容

当前核心依赖 [sing-tun `611d83b7a57b4719be45581eb674942ff561de6c`](https://github.com/SagerNet/sing-tun/tree/611d83b7a57b4719be45581eb674942ff561de6c)，模块版本为 `v0.9.7-0.20261023115453-611d83b7a57b`。该版本的 `redirect_ingress_discard_android.go` 只接受 hash 类型、16 字节 key、8 字节 value 的系统 map；[AOSP Android 15 的实现](https://android.googlesource.com/platform/packages/modules/Connectivity/+/refs/tags/android-15.0.0_r1/bpf_progs/netd.c) 使用两个接口编号。一加 10 Pro / Android 15 的问题报告返回 `type 1, key 16, value 4`，因此会在自动重定向启动时被上游的布局检查拒绝。不能仅按 Android 版本号选择布局。

[兼容补丁](libbox/sing-tun-android-ingress.patch) 从 BPF map 元数据读取实际 value 大小，支持单接口编号的 4 字节值及双接口编号的 8 字节值。只处理当前 TUN 的 IPv4 地址，且所有 value 槽位都必须匹配当前 TUN 接口编号；其他 map 类型、key 长度和 value 大小继续报错。保存条目保留原始长度及字节，关闭时只向布局未变且 TUN 接口编号仍匹配的 map 恢复，继续使用 `BPF_NOEXIST` 避免覆盖系统新建条目。不跳过整张系统 map 的校验或清空无关规则。

[patch_sing_tun.py](tools/patch_sing_tun.py) 在匹配的 Go 工具链准备完成后运行，核对依赖版本，将 Go 模块缓存中的源码复制到全新 `.core/.b-box/sing-tun/`，检查并应用补丁，通过当前核心的本地 `replace` 引用副本；共享模块缓存不被修改。依赖版本变化或副本已存在时失败，升级核心须重新核对补丁，不能静默沿用。该处理用于 UI 验证、Android smoke 和 Release 三条工作流，不改变核心标签、应用上游或 FlClash 基线。

[布局回归测试](libbox/sing-tun/redirect_ingress_discard_linux_test.go) 在 Linux CI 中测试生产代码的布局校验、单/双接口归属和恢复条件，覆盖 4/8 字节、未知布局、其他接口、map 大小变化和 TUN 更换。它不操作系统固定路径的 BPF map；一加设备上的实际启动、联网、停服恢复及与其他 VPN 的交互仍需 Root 实机验证。

## 保留与已撤销的差异

2026-10-25 已回退历史 MOD-013 核心裁剪，恢复上游 NaiveProxy、Tailscale、OpenVPN、Snell、OpenConnect、USBIP、Tor、Tailscale/Taildrop 客户端绑定及原生 Shell/PTY。`libbox-trim.patch` 和裁剪专用校验器已删除，不能恢复旧裁剪标签或继续使用当时生成的裁剪 AAR。

当前 legacy 补丁只排除 Android API 21 产物，不裁剪标准核心协议、上游构建标签或已有导出绑定。`ui_*.go` 是对标准核心的新增适配，不能将整个 B-box 核心描述为“与上游完全无差异”。

## 构建、同步和验证

- [Release 工作流](.github/workflows/build-release.yml) 检查并应用 legacy、sing-tun 兼容和 HTTP 调试补丁，运行上述布局回归测试，安装抓取引擎、路由适配和五个生产扩展，使用 JDK 17、版本文件指定的 Go 和 NDK 28.0.13004108，执行上游 `make lib_install` 与 `build_libbox -target android -platform android/arm64`。要求不存在 `libbox-legacy.aar`，再将标准产物放到 `app/libs/libbox.aar`。
- [Android smoke 工作流](.github/workflows/newui-android.yml) 同样应用上述补丁、抓取引擎、路由适配和五个扩展，构建 `android/amd64` 核心用于 x86_64 模拟器。
- [UI 验证工作流](.github/workflows/newui.yml) 应用上述补丁并安装抓取扩展，运行 `go test -ldflags=-checklinkname=0 github.com/sagernet/sing-tun -run '^TestAndroidIngressDiscard' -count=1`，再在匹配核心中复制 HTTP、出口、LAN 与抓包配置测试，运行 `go test -ldflags=-checklinkname=0 ./experimental/libbox -run '^TestUI(WebDAV|HTTPGet|Egress|LAN|Capture)' -count=1`。它不是完整核心 AAR 构建，内存绑定由 Android/Release 构建覆盖。另执行 `go test -race ./experimental/httpdebug -count=1` 和 `go test -ldflags=-checklinkname=0 ./route -run '^TestBboxHTTPDebug' -count=1`，覆盖抓取/改写引擎、数据竞争及原出站和嗅探字节保留。
- 对应回归文件为 [ui_http_test.go](libbox/ui_http_test.go)、[ui_egress_test.go](libbox/ui_egress_test.go)、[ui_lan_test.go](libbox/ui_lan_test.go)，覆盖请求限制/重定向、源回退/公网 IP 校验/解析失败后继续竞速/取消/服务替换、监听转换和端点生成。

[httpdebug_test.go](libbox/httpdebug/httpdebug_test.go) 覆盖域名/应用过滤、HTTP/HTTPS/HTTP2、上游证书验证、SSE/WebSocket、正文和记录上限、恢复及导出；[httpdebug_test.go](libbox/route/httpdebug_test.go) 在匹配核心中验证路由不被嗅探覆盖及不匹配流量字节回放。获准本机验证时可运行 [verify_httpdebug.py](tools/verify_httpdebug.py)，限制 Go 并发和内存；该脚本只测试独立引擎，不编译路由或生成 Android 绑定。

[rewrite_test.go](libbox/httpdebug/rewrite_test.go) 覆盖规则顺序、有效报文录制、首个脚本、Map Local/重定向不访问上游、压缩与超限透传、SSE、chunked/Expect、脚本超时/二进制/消息边界、改写目标的选路与 TLS、MITM 绕过及域名导出。[ui_capture_test.go](libbox/ui_capture_test.go) 检查完整配置校验、格式化往返与 schema；路由测试另检查重选路清除旧规则状态并阻止递归抓包。对应结果须由当前 SHA 的 Actions 提供。

升级核心时先检查补丁是否仍准确排除 legacy 分支，核对 sing-tun 依赖版本及自动重定向补丁，检查 HTTP 调试路由/服务生命周期补丁及覆盖文件，再逐项核对扩展所依赖的路由、配置解析、HTTP 客户端和生成绑定。Go 导出名与 gomobile 生成的 Java 方法名不能凭拼写推断，例如 `UIHTTPGet` 当前由 Kotlin 调用为 `Libbox.uihttpGet`。任何扩展增删都要核对上述各工作流的复制清单。

以上是源码和工作流的覆盖范围，不是本次测试通过记录；实际结果须对应提交 SHA 和 Actions 运行。本机构建限制见 [AGENTS.md](AGENTS.md)。
