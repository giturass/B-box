# libbox 与上游的差异

本文记录核心补丁和新增绑定；项目约束见 [AGENTS.md](AGENTS.md)，Android 调用及数据持久化见 [app.md](app.md)，FlClash 查询规则和界面适配见 [flclash.md](flclash.md)。

## 基线与集成方式

上游为 [SagerNet/sing-box](https://github.com/SagerNet/sing-box)，绑定位于 `experimental/libbox`。当前基线是 [`v1.15.0-alpha.13`](https://github.com/SagerNet/sing-box/tree/v1.15.0-alpha.13/experimental/libbox)，Go 为 `go1.26.10`；实际版本由 [version.properties](version.properties) 的 `VERSION_NAME` 和 `GO_VERSION` 决定，工作流检出 `v${VERSION_NAME}`。

本仓库不存储完整核心或 AAR，构建补丁与扩展源码统一保存在 [libbox/](libbox/)。CI 通过 [setup-core](.github/actions/setup-core/action.yml) 检出上游至 `.core/`，先准备版本文件指定的 Go，再应用构建补丁、sing-tun 兼容补丁和 HTTP 调试补丁。抓取引擎、路由适配及所有 `ui_*.go`及相应测试使用同一安装入口；Android 构建由 [build-libbox](.github/actions/build-libbox/action.yml) 生成标准 `libbox.aar`。仅有上游原版 AAR 不足以编译当前 B-box，因为它没有下面的 UI 扩展绑定，也不包含本仓库的路由和自动重定向兼容补丁。

| 差异 | 文件 | 作用 |
| --- | --- | --- |
| 排除 legacy 产物 | [libbox-no-legacy.patch](libbox/libbox-no-legacy.patch) | 修改上游 `cmd/internal/build_libbox/main.go`，删除 API 21 AAR 分支及只供该分支使用的 `filterTags` |
| Android 自动重定向兼容 | [sing-tun-android-ingress.patch](libbox/sing-tun-android-ingress.patch) | 兼容 ingress discard BPF map 的 4/8 字节 value，保留接口归属检查和原值恢复 |
| 运行时内存绑定 | [ui_dashboard.go](libbox/ui_dashboard.go) | 读取 Go 运行时内存和 goroutine 数，提供释放内存操作 |
| HTTP/WebDAV 绑定 | [ui_http.go](libbox/ui_http.go) | 返回 HTTP 状态码和有大小上限的响应，供备份及 Release 元数据使用 |
| 出口探测绑定 | [ui_egress.go](libbox/ui_egress.go) | 可取消的设备网络/运行中路由探测、出口 IP 多源查询与质量请求传输 |
| 局域网代理配置绑定 | [ui_lan.go](libbox/ui_lan.go) | 读取 mixed/http/socks 监听端点、修改 listen 或显式新增 mixed 入站 |
| HTTP/HTTPS 请求抓取 | [httpdebug.patch](libbox/httpdebug.patch)、[httpdebug/](libbox/httpdebug/)、[route/](libbox/route/)、[ui_capture.go](libbox/ui_capture.go) | HTTP 录制、HTTPS MITM、平台签名、域名提取、记录及导出绑定 |

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

[httpdebug/](libbox/httpdebug/) 在 sing-box 已选定路由后记录 HTTP，并按本机设置接管 HTTPS。它没有另开代理监听端口，不修改配置、DNS 映射或出站选择。参考 mitmproxy 的上游优先握手及证书校验设计，核对版本为 [`b08148b35d7679bc9be8d4a3d85b32bcea5ce250`](https://github.com/mitmproxy/mitmproxy/tree/b08148b35d7679bc9be8d4a3d85b32bcea5ce250)，主要依据 `addons/tlsconfig.py`、`proxy/layers/tls.py` 和 `certs.py`；生产代码使用 Go `crypto/tls`，没有嵌入 mitmproxy。FlClash 的独立补充参考见 [界面来源](flclash.md#参考版本与来源清单)。

[路由钩子](libbox/route/httpdebug.go) 将嗅探结果仅用于抓取选择。转发继续调用原选定出站的 `ConnectionHandler` 或原连接管理器，保留原目的地址、解析结果、网络策略、TLS 分片、规则、代理组和应用归属。CONNECT/SOCKS 隧道先通过核心握手成功接口响应客户端，再等待请求或 ClientHello，避免双方等待。需要观察的流通过普通 stream 路径处理；未命中流保留嗅探读到的所有字节继续原路由。

HTTPS 默认关闭。开启后，[MITM 处理器](libbox/httpdebug/mitm.go) 用只读 TLS 探测器读取实际 ClientHello，探测阶段不会向客户端写 ServerHello 或 alert。域名以实际 SNI 为准；没有 SNI 时仅接受已知 IP 目标，不以 DNS 映射冒充 SNI。排除项、非 HTTP ALPN、无法解析的 hello 或未命中的流原样回放。包含列表为空表示不限制域名，精确名称只匹配本域，`*.example.com` 匹配子域，排除优先。端口按原始连接目的端口筛选，route 的端口覆写不改变抓取 authority；默认 80、443、8080、8443，最多 32 个。应用筛选来自核心归属，未知归属不能匹配指定应用，共享 UID 的包使用同一归属结果。

命中后先通过原路由完成上游 TLS 握手，用 `adapter.RootPoolFromContext` 的当前证书库验证服务器链、有效期和名称，成功后才签发客户端证书。已验证的上游连接直接交给 HTTP transport 复用，后续连接也重新验证；没有跳过证书验证的选项。两端支持 TLS 1.2/1.3，ALPN 跟随上游协商 HTTP/1.1 或 HTTP/2；没有 ALPN 时使用 HTTP/1.1。SNI、双方 TLS 版本、ALPN 和上游叶证书 SHA-256 写入记录。客户端与上游握手失败分阶段记录，失败条目为 TLS/仅域名，不伪造 HTTP 响应或解密成功。

[签发器](libbox/httpdebug/authority.go) 使用平台提供的 P-256 `crypto.Signer`，根私钥始终留在 Android Keystore，Go 只持有签名接口。生成三年期、自签名、名称包含公钥摘要的独立 CA；启动验证 CA 约束、有效期、公钥一致性及随机挑战签名。[绑定](libbox/ui_capture_signer.go) 只接受 SHA-256 摘要并验证平台返回的 ECDSA 签名。临时叶证书只含当前名称的一个 SAN，有效期不超过十二小时和根证书有效期，最多缓存 128 个。不同 HTTP authority 返回 421，客户端须为该 origin 建立自己的 TLS 连接，避免通过 HTTP/2 合并连接绕过排除项。证书持久化、安装和信任边界见 [app.md](app.md#请求抓取与-mitm)。

HTTP 转发保留 Host、原始查询参数、重复响应头和客户端提供的 Forwarded/X-Forwarded 字段。正文边转发边保存前缀，不因存储限额截断实际传输；SSE 立即刷新，WebSocket 透传升级后的连接且只记录 HTTP 握手。请求/响应 trailers 单独保存，不混入初始头部；并发 HTTP/2 流独立记录，取消保留已收到的正文及未完成标志。嵌套 CONNECT 不支持。客户端证书认证、真实 ECH 加密名称和 HTTP/3 解密不在支持范围；应用证书固定校验或独立信任库仍可拒绝连接，需配置排除或由目标客户端调整信任/ECH 设置，不能宣称全应用解密。

`blockQUIC` 是单独、默认关闭的 TCP 回退选项：只拒绝已恢复 QUIC ClientHello 名称且应用、端口、HTTPS 包含/排除条件均命中的流，应用可能改用 TCP。未知名称、其他 UDP 和未命中的缓冲数据报继续原路由；不保证目标应用会回退。`extractHosts` 额外保存可见 TLS/QUIC 名称，标为“仅域名”；它本身不会阻断 QUIC，也不能恢复 ECH 隐藏的名称。域名导出对当前搜索条件下的所有保留记录去重，HAR 跳过仅域名条目。

资源与完整性边界：

- 最多接管 32 条连接（含正在检查 ClientHello 的连接），每条 HTTP/2 最多 16 个并发流。ClientHello 检查最多 64 KiB/5 秒；上游拨号和 TLS 握手有超时与会话取消边界。服务停止、重载或用户停抓关闭已接管连接，目标应用需要重新连接，抓包不会自动恢复。
- 最多保存 300 条、磁盘 32 MiB；每方向正文默认保留 64 KiB，最高 1 MiB；头部和 trailers 分别最多 64 KiB。存储失败、截断、未完成和流错误分别保留，不能把保存的前缀当成完整消息。
- 预览/HAR 的 gzip/deflate 解码最多 64 KiB；不支持的编码或二进制使用 Base64，HAR 另保留原始前缀与完整性标志。HAR 使用 `_trailers` 扩展保存尾部字段，耗时仅为总时长。cURL 只允许完整且未截断的请求头和正文，含请求 trailers 时拒绝导出，避免给出不完整重放命令。

[抓包日志](libbox/httpdebug/logs.go) 与请求存储独立，记录启停、握手阶段和失败原因，不记录正文。缓冲最多 1000 条、512 KiB，每条 4 KiB，UTF-8 截断安全。状态统计实际握手尝试/失败、QUIC 拒绝、解密连接、正文字节、解析/筛选/保存数量；停抓保留本次计数，下次开始重置。清日志不清请求，进程退出清内存日志，保存记录在停服后仍可读取。

`CommandServer.StartUICapture(directory, config, certificate, signer)` 与核心重载/关闭串行；`GenerateUICaptureCA(publicKeyDER, UICaptureSigner)` 只接收平台公钥和签名回调，不接收或导出根私钥。其他 `Open/Validate/Stop/Read/List/Clear/ExportUICapture*`、`ExtractUICaptureHosts` 及日志绑定提供本地访问，轮询只传有界预览，正文文件通过导出读取。Root 采集器、FD 管道和手动 TLS 偏移已移除；普通 Root/Shizuku 与完整核心协议保持原有能力。

### 旧抓包配置清理

抓包筛选是应用私有设置，不属于 sing-box schema。`experimental.http_debug` 类型、编辑绑定及示例已移除。[兼容清理](libbox/ui_capture_migration.go) 在 libbox 解析前仅删除该退役字段，不解释或执行其中内容；其他未知字段继续由核心拒绝。格式化以及应用配置保存会移除它，未包含该字段的内容保持原样。原字段中嵌入的规则/脚本不会迁移为可执行内容，也不会自动开启抓包。

打开请求存储时，[存储清理](libbox/httpdebug/store_migration.go) 删除旧版 `wire/` 中以 64 个十六进制字符组成的 ID 命名的 `.wire` 包/密钥日志及 `warning.txt`，删除失败返回错误。清理不读取日志内容、不跟随目录符号链接，保留 HTTP 请求和无关文件，仅移除已清空的专用目录。旧本机设置的原子迁移见 [app.md](app.md#请求抓取与-mitm)。

## Android 自动重定向兼容

当前核心依赖 [sing-tun `611d83b7a57b4719be45581eb674942ff561de6c`](https://github.com/SagerNet/sing-tun/tree/611d83b7a57b4719be45581eb674942ff561de6c)，模块版本为 `v0.9.7-0.20261023115453-611d83b7a57b`。该版本的 `redirect_ingress_discard_android.go` 只接受 hash 类型、16 字节 key、8 字节 value 的系统 map；[AOSP Android 15 的实现](https://android.googlesource.com/platform/packages/modules/Connectivity/+/refs/tags/android-15.0.0_r1/bpf_progs/netd.c) 使用两个接口编号。一加 10 Pro / Android 15 的问题报告返回 `type 1, key 16, value 4`，因此会在自动重定向启动时被上游的布局检查拒绝。不能仅按 Android 版本号选择布局。

[兼容补丁](libbox/sing-tun-android-ingress.patch) 从 BPF map 元数据读取实际 value 大小，支持单接口编号的 4 字节值及双接口编号的 8 字节值。只处理当前 TUN 的 IPv4 地址，且所有 value 槽位都必须匹配当前 TUN 接口编号；其他 map 类型、key 长度和 value 大小继续报错。保存条目保留原始长度及字节，关闭时只向布局未变且 TUN 接口编号仍匹配的 map 恢复，继续使用 `BPF_NOEXIST` 避免覆盖系统新建条目。不跳过整张系统 map 的校验或清空无关规则。

[patch_sing_tun.py](tools/patch_sing_tun.py) 在匹配的 Go 工具链准备完成后运行，核对依赖版本，将 Go 模块缓存中的源码复制到全新 `.core/.b-box/sing-tun/`，检查并应用补丁，通过当前核心的本地 `replace` 引用副本；共享模块缓存不被修改。依赖版本变化或副本已存在时失败，升级核心须重新核对补丁，不能静默沿用。该处理用于 UI 验证、Android smoke 和 Release 三条工作流，不改变核心标签、应用上游或 FlClash 基线。

[布局回归测试](libbox/sing-tun/redirect_ingress_discard_linux_test.go) 在 Linux CI 中测试生产代码的布局校验、单/双接口归属和恢复条件，覆盖 4/8 字节、未知布局、其他接口、map 大小变化和 TUN 更换。它不操作系统固定路径的 BPF map；一加设备上的实际启动、联网、停服恢复及与其他 VPN 的交互仍需 Root 实机验证。

## 保留与已撤销的差异

2026-10-25 已回退历史 MOD-013 核心裁剪，恢复上游 NaiveProxy、Tailscale、OpenVPN、Snell、OpenConnect、USBIP、Tor、Tailscale/Taildrop 客户端绑定及原生 Shell/PTY。`libbox-trim.patch` 和裁剪专用校验器已删除，不能恢复旧裁剪标签或继续使用当时生成的裁剪 AAR。

当前 legacy 补丁只排除 Android API 21 产物，不裁剪标准核心协议、上游构建标签或已有导出绑定。`ui_*.go` 是对标准核心的新增适配，不能将整个 B-box 核心描述为“与上游完全无差异”。

## 构建、同步和验证

- [Release 工作流](.github/workflows/build-release.yml) 复用 UI 验证工作流运行下述核心测试，再通过共享构建 Action 使用 JDK 17、版本文件指定的 Go 和 NDK 28.0.13004108，执行上游 `make lib_install` 与 `build_libbox -target android -platform android/arm64`。
- [Android smoke 工作流](.github/workflows/newui-android.yml) 使用同一构建 Action，核心目标为 `android/amd64`，用于 x86_64 模拟器。两种构建均要求不存在 `libbox-legacy.aar`，再将标准产物放到 `app/libs/libbox.aar`。
- [UI 验证工作流](.github/workflows/newui.yml) 使用共享准备 Action，运行 `go test -ldflags=-checklinkname=0 github.com/sagernet/sing-tun -run '^TestAndroidIngressDiscard' -count=1` 及 `go test -ldflags=-checklinkname=0 ./experimental/libbox -run '^TestUI' -count=1`。所有生产扩展均参与编译，所有 `TestUI` 测试自动纳入，不再维护具体扩展名的筛选清单；它不构建核心 AAR。另执行 `go test -race ./experimental/httpdebug -count=1` 和 `go test -ldflags=-checklinkname=0 ./route -run '^TestBboxHTTPDebug' -count=1`，覆盖 HTTP/MITM 抓取引擎、数据竞争及原出站和嗅探字节保留。
- 对应回归文件为 [ui_http_test.go](libbox/ui_http_test.go)、[ui_egress_test.go](libbox/ui_egress_test.go)、[ui_lan_test.go](libbox/ui_lan_test.go)，覆盖请求限制/重定向、源回退/公网 IP 校验/解析失败后继续竞速/取消/服务替换、监听转换和端点生成。

[httpdebug_test.go](libbox/httpdebug/httpdebug_test.go) 覆盖 HTTP、SSE/WebSocket、存储和导出；[mitm_test.go](libbox/httpdebug/mitm_test.go) 覆盖 TLS 1.2/1.3、HTTP/1.1/2、上游连接复用、独立信任链、过期/错误名称/不受信任上游、客户端 pinning、排除和非 HTTP hello 回放、重复 Cookie、请求/响应 trailers、大正文、并发流、SSE 取消、跨 authority 拒绝、QUIC 筛选及停止/拨号超时。[authority_test.go](libbox/httpdebug/authority_test.go) 覆盖独立根身份、SAN/IP、叶证书缓存和密钥错配；[ui_capture_signer_test.go](libbox/ui_capture_signer_test.go) 验证平台签名接口的摘要、算法及返回签名校验。

[ui_capture_test.go](libbox/ui_capture_test.go) 覆盖本地设置、schema/退役字段和未启动时日志；[路由测试](libbox/route/httpdebug_test.go) 通过真实 TLS 握手核对原选定出站、SNI 与 DNS 映射隔离、端口覆写、CONNECT 确认顺序、证书库及排除流的字节回放。[logs_test.go](libbox/httpdebug/logs_test.go) 检查日志限额、UTF-8 和并发访问；[store_migration_test.go](libbox/httpdebug/store_migration_test.go) 覆盖旧文件清理、请求保留和符号链接隔离。Android 真正的 Keystore/gomobile 签名与模块打包由 [平台测试](app.md#验证与历史记录) 覆盖；Linux 测试不能代替 Android 系统 CA 模块或目标应用的实机验收。获准本机测试时可使用 [verify_httpdebug.py](tools/verify_httpdebug.py)，它不构建 Android 绑定。

升级核心时先检查补丁是否仍准确排除 legacy 分支，核对 sing-tun 依赖版本及自动重定向补丁，检查 HTTP 调试路由/服务生命周期补丁及覆盖文件，再逐项核对扩展所依赖的路由、配置解析、HTTP 客户端和生成绑定。Go 导出名与 gomobile 生成的 Java 方法名不能凭拼写推断，例如 `UIHTTPGet` 当前由 Kotlin 调用为 `Libbox.uihttpGet`。任何扩展增删都要核对共享准备 Action 的安装范围及三条工作流的调用。

以上是源码和工作流的覆盖范围，不是本次测试通过记录；实际结果须对应提交 SHA 和 Actions 运行。本机构建限制见 [AGENTS.md](AGENTS.md)。
