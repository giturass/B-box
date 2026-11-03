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
| JSON 配置驱动的 HTTP 改写 | [http_rewrite.patch](libbox/http_rewrite.patch)、[rewrite_config.go](libbox/httpdebug/rewrite_config.go)、[ui_http_rewrite.go](libbox/ui_http_rewrite.go) | MITM、Map Remote、Header/URL/Body Rewrite、Map Local/API Mock 与 JavaScript 请求/响应脚本 |

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

[httpdebug/](libbox/httpdebug/) 在 sing-box 已选定路由后记录 HTTP，并按本机设置接管 HTTPS。仅抓取时不修改配置、DNS 映射或出站选择；可选的配置改写另见 [JSON 配置中的 HTTP 改写](#json-配置中的-http-改写)。它没有另开代理监听端口。抓取模式参考 mitmproxy 的上游优先握手及证书校验设计，核对版本为 [`b08148b35d7679bc9be8d4a3d85b32bcea5ce250`](https://github.com/mitmproxy/mitmproxy/tree/b08148b35d7679bc9be8d4a3d85b32bcea5ce250)，主要依据 `addons/tlsconfig.py`、`proxy/layers/tls.py` 和 `certs.py`；生产代码使用 Go `crypto/tls`，没有嵌入 mitmproxy。FlClash 的独立补充参考见 [界面来源](flclash.md#参考版本与来源清单)。

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

### JSON 配置中的 HTTP 改写

在原 sing-box JSON 的 `experimental` 中添加 `http_rewrite`，保留原入站、DNS、出站和路由。此字段是 B-box 的真实核心扩展，出现在生成的配置 schema 中；`Libbox.checkConfig` 同时校验字段、正则、静态响应和 JavaScript 语法，格式化、分享及备份保留它。它不是 Surge INI 导入器，也不是 Mihomo 配置覆写。完整示例见 [examples/http-rewrite.json](examples/http-rewrite.json)。该示例使用测试域名，HTTPS 默认关闭；移植时替换匹配域名和映射地址，将 `experimental.http_rewrite` 合并到自己的配置即可。

最小本地 Mock 示例：

```json
{
  "experimental": {
    "http_rewrite": {
      "enabled": true,
      "mitm": {"enabled": true, "hosts": ["api.example.test"]},
      "map_local": [{
        "pattern": "^https://api\\.example\\.test/mock$",
        "status": 200,
        "data_type": "text",
        "data": "{\"ok\":true}",
        "headers": [{"name": "Content-Type", "value": "application/json"}]
      }]
    }
  }
}
```

启用 HTTPS 前，通过“工具 → 请求 → MITM 证书”生成并安装本机 CA；证书与私钥不写入 JSON。`enabled` 默认 false，启用后的规则随本地服务启动/重载生效，服务停止或移除该字段后结束。请求记录仍由抓包开关控制；停止记录会关闭旧拦截连接，但不会停用配置中的规则。应用失败恢复及证书生命周期见 [app.md](app.md#配置驱动的-http-改写)。

顶层选项：

| 字段 | 含义 |
| --- | --- |
| `enabled` | 启用此配置的 HTTP 处理，默认 false |
| `ports` / `packages` | 原始目标端口及应用范围；端口默认 80、443、8080、8443，最多 32 个；应用为空表示不限 |
| `mitm.enabled` | 是否解密 HTTPS，默认 false |
| `mitm.hosts` / `mitm.excluded_hosts` | 精确域名、IP 或 `*.example.com`；启用 MITM 必须填写包含列表，排除优先；不接受 Surge 的 `*`、`?`、端口内嵌或负号列表语法 |
| `mitm.block_quic` | 按已观测的名称及配置范围拒绝 QUIC，默认 false，要求 MITM 已开启 |
| `max_body_bytes` | 改写正文的缓冲与输出上限，1～1048576 字节，默认 1 MiB；与抓包保存前缀的限额独立 |
| `resources` | JSON 内嵌资源字典，值为 `{ "data_type": "text"或"base64", "data": "..." }`；静态响应与 JS 可按名称引用 |

规则总数最多 256，扩展 JSON 最多 2 MiB。每条规则都有必填 `pattern`、可选 `name` 和 `disabled`（默认 false）。`pattern` 对进入引擎时的完整 URL（含查询参数）匹配；请求和响应阶段使用相同的原始 URL，避免映射后响应规则失配。声明式正则使用 Go RE2，最多 4096 字节、32 个捕获组，不支持前后查找或反向引用；替换模板支持 `$1`、`${name}` 和 `$$`。正文正则中的 `^`、`$` 按行匹配。

| 能力 / 字段 | 规则内容及行为 |
| --- | --- |
| Header Rewrite / `header_rewrite` | `phase`: `request`或`response`；`operation`: `add`、`delete`、`replace`、`replace_regex`；指定 `header`，普通操作使用 `value`，正则操作使用 `regex`/`replacement`。`add` 保留重复字段；`replace` 只替换已存在字段，新增或覆盖可依次 delete/add。请求 Host 仅允许 replace/replace_regex |
| URL Rewrite / `url_rewrite` | `action`: `header` 透明改写并更新 Host，`302`/`307` 返回重定向，`reject` 返回 403。前两类使用绝对 HTTP(S) `replacement` 模板；首条命中生效 |
| Map Remote / `map_remote` | `replacement` 指定绝对 HTTP(S) URL 模板，默认更新 Host，`preserve_host: true` 保留已有 Host；首条命中生效。与 URL header 规则同时匹配时，Map Remote 决定最终目标 |
| Body Rewrite / `body_rewrite` | `phase`、URL `pattern`、正文 `regex` 和 `replacement`；所有命中规则依次执行，可以从空正文生成内容 |
| Map Local / `map_local` | 首条命中返回本地响应，不请求原 HTTP 服务器。`status` 为 200～599，默认 200；`headers` 为保留重复字段的 `{name,value}` 数组；`data_type` 为 `text`（默认）、`base64` 或 `tiny-gif`，`data` 为内容；也可用 `resource` 引用内嵌资源，与 data/data_type 互斥 |
| API Mock | 静态使用 Map Local；动态使用请求脚本的 `$done({response:{...}})` |
| JavaScript / `scripts` | `phase`、URL `pattern`，以及 `source` 或 `resource`；每个方向只执行第一条启用且匹配的脚本 |

处理顺序为请求 Header → URL → Map Remote → Body → 请求脚本 → Map Local → 上游 → 响应 Header → Body → 响应脚本。重定向、拒绝或脚本 Mock 会提前结束请求路径，生成的响应仍经过响应阶段。首条命中脚本失败不会改用下一条脚本。错误写入诊断；脚本异常、超时、非法结果或上游失败返回 502，超限请求正文返回 413，不把失败改写默认为成功。

脚本接口由 [rewrite_script.go](libbox/httpdebug/rewrite_script.go) 提供：

- `$request`: `url`、`method`、`headers`、稳定的 `id`；请求脚本设置 `requires_body: true` 时提供 `body`。响应脚本中的 request 不携带正文。URL 是前序规则处理后的值。
- `$response`: 响应脚本可读取 `status`、`headers`，`requires_body: true` 时提供 `body`。先执行静态正文改写，脚本读取其结果。
- `$done()`/`$done({})` 原样继续并结束脚本；请求结果可含 `url`、`headers`、`body`、`response`、`abort`，另支持 B-box 的 `method`。脚本修改 URL 默认保留 Host，需要时一起返回 headers；响应结果可含 `status`、`headers`、`body`、`abort`。动态 response 支持 status/headers/body。
- 默认正文为 UTF-8 字符串；`binary_body: true` 使用 `Uint8Array`。替换当前报文 body 要求 `requires_body: true`；动态 Mock 的 response.body 不要求读取原请求正文。
- `full_headers: true` 将头部作为 `{field,value}` 数组传入，保留重复 Cookie 等字段；结果同时接受对象或数组。普通对象输入每个字段取首值，需完整保留重复字段的脚本应使用 full_headers。
- `argument` 通过 `$argument` 提供；`$script` 包含 name/type/binaryBodyMode；`console.log/info/warn/error/debug` 写入有界诊断日志（每次最多 10 条、每条最多 2 KiB）。只有脚本显式打印的内容进入这些日志。

脚本正文限额可用 `max_body_bytes` 降低，不能超过配置限额；`timeout_ms` 默认 200，范围 1～2000。每次创建独立 Goja runtime，同时最多执行 4 个，调用栈最多 256 层；原生 JS 正则另设 200 毫秒的匹配截止时间。超时是执行限制，不是独立进程的硬内存隔离。未实现网络请求、持久存储、定时器、Node.js 模块、通知、cron、Surge 的完整 JS API 或 jq；不自动执行外链脚本，不把资源名称解释为本机文件路径。配置及备份可以完整携带内嵌 JS 和静态资源。

正文与协议边界：只缓冲命中的报文，支持 identity/gzip/deflate，压缩前后分别检查限额；不支持的编码和非 UTF-8 文本改写会保留原报文并记录原因。响应超限会回放已读前缀并继续原样转发，不截断数据；SSE、WebSocket 升级、HEAD 和 206 响应跳过正文处理。改写后移除旧压缩、摘要、ETag、范围及 trailer 信息，重新计算 Content-Length；禁止规则/脚本手动改变消息分帧、升级头或 101 升级状态，HEAD/204/205/304 不发送不允许的正文。

最初连接仍须通过 sing-box 路由选择，改写不会绕过原路由的拒绝。未改变 origin 的请求沿用原出站、解析结果及网络选项；跨 origin 的目标经 [route/httpdebug.go](libbox/route/httpdebug.go) 重新进入同一核心，保留入站、来源和应用归属，清除旧目的地/规则缓存，并阻止递归改写。没有直连回退。记录保存改写后的请求/响应、原 URL 与命中说明；映射条目的连接路由字段仍表示原连接，界面明确标为原连接出站/规则。

配置 MITM 允许 Map Local 和脚本在原服务器离线时响应，因此 [rewrite_transport.go](libbox/httpdebug/rewrite_transport.go) 先完成客户端握手，实际需要上游时再连接；每次真实 HTTPS 上游（含映射目标）仍验证证书链、名称和有效期。本地 Mock 不伪造上游 TLS 信息，实际转发后才记录上游 TLS 和证书。仅手动抓包的连接继续使用前文的上游优先握手。CA 信任、证书固定校验、ECH、HTTP/3 及连接/HTTP2 并发边界仍见前文。

参考依据为 2026-11-03 读取的 Surge [HTTP Processing](https://manual.nssurge.com/http/overview.html)、[MITM](https://manual.nssurge.com/http/mitm.html)、[URL Rewrite](https://manual.nssurge.com/http/url-rewrite.html)、[Header Rewrite](https://manual.nssurge.com/http/header-rewrite.html)、[Body Rewrite](https://manual.nssurge.com/http/body-rewrite.html)、[Map Local](https://manual.nssurge.com/http/map-local.html)、[请求脚本](https://manual.nssurge.com/scripting/http-request.html)和[响应脚本](https://manual.nssurge.com/scripting/http-response.html)。Surge 的 URL header 模式提供透明远端映射；B-box 另提供 map_remote 列表。上表明确 B-box 的字段、顺序与支持范围，不宣称配置或脚本完全兼容 Surge。

核心集成固定 [Goja `a177d211bf606f6275674a35bc0d31d8ee0ee396`](https://github.com/dop251/goja/tree/a177d211bf606f6275674a35bc0d31d8ee0ee396)，模块版本 `v0.0.0-20261102223426-a177d211bf60`。共享 setup-core 将核心模块最低 Go 版本调整为 1.26.0，实际构建仍用 version.properties 的 go1.26.10；Goja 依赖将 x/text 提升到 v0.43.0，并引入 regexp2/v2、sourcemap 和 pprof。许可证随 Flutter assets 打包并在许可证页注册；三个上游基线不变。Release、Flutter 核心验证和 Android smoke 都使用同一个安装入口。

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

以上列出源码和工作流的覆盖范围；MITM 分支的实际检查、源码 SHA、Actions 运行及未覆盖范围见 [验证记录](app.md#mitm-分支验证)。本机构建限制见 [AGENTS.md](AGENTS.md)。
