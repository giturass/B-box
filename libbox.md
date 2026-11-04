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
| 独立配置处理 | [http_rewrite.patch](libbox/http_rewrite.patch)、[ui_processing.go](libbox/ui_processing.go)、[processing.go](libbox/option/processing.go) | 独立的 MITM、HTTP 改写、HTTP 脚本与 Cron，统一校验及服务生命周期 |

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

[httpdebug/](libbox/httpdebug/) 在 sing-box 已选定路由后记录 HTTP 以及配置允许解密的 HTTPS/TLS 流。请求记录、[MITM](#独立-mitm-配置)、[HTTP 改写](#json-配置中的-http-改写)和[脚本](#独立脚本与-cron)分别控制；没有另开代理监听端口。手动抓包设置 version 3 仅含 `packages`、`ports`、`bodyLimit`、`extractHosts`，不含解密域名、证书、规则或脚本。

[路由钩子](libbox/route/httpdebug.go) 将嗅探结果仅用于接管选择。转发继续调用原选定出站的 `ConnectionHandler` 或原连接管理器，保留原目的地址、解析结果、网络策略、TLS 分片、规则、代理组和应用归属。CONNECT/SOCKS 隧道先通过核心握手成功接口响应客户端，再等待请求或 ClientHello。需要观察的流通过普通 stream 路径处理；未命中流保留嗅探读到的所有字节继续原路由。

HTTP 转发保留 Host、原始查询参数、重复响应头和客户端提供的 Forwarded/X-Forwarded 字段。正文边转发边保存前缀，不因存储限额截断实际传输；SSE 立即刷新，WebSocket 透传升级后的连接且只记录 HTTP 握手。请求/响应 trailers 单独保存；并发 HTTP/2 流独立记录，取消保留已收到的正文及未完成标志。

每条请求/响应记录保留独立 `id`，详情查询、正文文件和导出继续使用它。路由入口从 sing-box [trafficcontrol.Tracker](https://github.com/SagerNet/sing-box/blob/824dbcfb4dd0a95921840b1737b950cde590241e/common/trafficcontrol/tracker.go) 读取原始客户端连接的 UUID，写入可选的 `metadata.connectionId`，与 libbox 连接列表的 ID 一致；读取会穿过嗅探缓存等连接包装。同一连接的 HTTP/1.1 请求和 HTTP/2 流共享此关联，HTTPS 与 TLS/TCP 记录也保留它，跨目标改写不替换为新出站连接的 ID。

没有跟踪器的流、跟踪前的 QUIC 域名观察和旧记录可缺省该字段；旧 `captureId` 不作为连接 ID。关联以字符串随记录保存，连接关闭或从连接历史淘汰不删除请求记录，后者仍按自身限额清理；启停抓包接口不接收连接 ID。

资源与完整性边界：

- 不设固定的接管连接总数上限；每条 HTTP/2 仍最多 16 个并发流。ClientHello 检查最多 64 KiB/5 秒，[tls_selection.go](libbox/httpdebug/tls_selection.go) 跟踪探测、待交接与正式处理的生命周期，调用取消及服务停止会回收尚未交接的连接。拨号和握手仍受超时及会话取消约束。
- [recording.go](libbox/httpdebug/recording.go) 将记录策略与服务处理引擎分开。启停记录不替换引擎或关闭已有 HTTP、SSE、WebSocket、TLS/TCP 连接；停止时封存已有前缀和当时的完整性状态，迟到正文、响应及 EOF 不再更新该记录。重新开启从已有拦截连接的新 HTTP 请求或新 TCP 流开始记录，不能补录正在传输的旧流；尚未被接管的连接需由应用重新建立。服务停止/重载才关闭旧处理连接，记录不会自动恢复。
- 抓包端口默认 80、443、8080、8443，最多 32 个；按原始目标端口筛选。应用列表为空表示不限；未知归属不能匹配指定应用，共享 UID 使用同一归属结果。这些设置只限制记录，不改变配置中的解密或处理范围。
- 最多保存 300 条、磁盘 32 MiB；每方向正文默认保留 64 KiB，最高 1 MiB；头部和 trailers 分别最多 64 KiB。存储失败、截断、未完成和流错误分别保留。
- 正文处理、预览和 HAR 共用 [content_encoding.go](libbox/httpdebug/content_encoding.go)，支持 identity、gzip/x-gzip、deflate（zlib/raw）、br 和 zstd，最多三层 Content-Encoding，合并重复头后逆序解码，每层输出均检查限额。预览/HAR 解码最多 64 KiB；不支持的编码或二进制使用 Base64，HAR 请求/响应均保留原始前缀与完整性标志。HAR 用 `_trailers` 保存尾部字段，耗时仅为总时长。cURL 仅允许完整、未截断且无 trailers 的 HTTP 请求；TLS/TCP 流和仅域名记录不进入 HAR，也不能导出 cURL。

[store.go](libbox/httpdebug/store.go) 为每条记录的两个方向按需保留正文文件句柄，方向内串行写入，EOF、前缀限额、完成、淘汰和清空均释放句柄；达到前缀上限后继续累计实际转发字节。正文写入前在索引锁内预留存储字节，再释放索引锁执行写入，避免并发写入重复占用剩余额度；迟到写入不能重建已清理记录。完成标志等待在途写入提交计数，元数据沿用原子替换。详情在锁内复制元数据并打开文件，随后在锁外只读取该快照已保存的字节数并解码，保留并发追加及淘汰期间的快照一致性。

`extractHosts` 额外保存可见 TLS/QUIC 名称，标为“仅域名”；它不启用解密、阻断 QUIC 或恢复 ECH 隐藏的名称。域名导出对当前搜索条件下的保留记录去重。保存记录在停服后仍可读取。

没有独立抓包日志页面、缓冲或读写绑定。[logging.go](libbox/httpdebug/logging.go) 将运行错误与脚本 console 输出送到当前核心的普通日志，遵守配置日志等级；停止服务后释放日志回调。正文预算不足、编码/流式跳过、ClientHello 无法解析和 MITM 范围未命中保留具体原因；请求处理日志带连接/请求 ID，错误页指向请求详情及现有服务日志。TLS 客户端/上游握手错误区分阶段；握手完成但未收到 HTTP 请求就结束另记 `tlsNoRequest`，只提示可能的 pinning 或客户端取消，不认定已证实的证书固定校验失败。正常服务取消不产生该提示。

`ReadUICaptureStatus` 保留抓包状态、计数和最近错误，并报告 `bodyBudgetReserved`/`bodyBudgetLimit`、`httpScriptsRunning`/`cronScriptsRunning`，供平台确认状态；请求页不展示配置已生效横幅。

`CommandServer.StartUICapture(directory, config, certificate, signer)` 与核心重载/关闭串行；后两个参数保留生成调用兼容，解密策略及 CA 只取已启动配置的 MITM。`GenerateUICaptureCA(publicKeyDER, UICaptureSigner)` 只接收平台公钥和签名回调，不接收或导出根私钥。其他 `Open/Validate/Stop/Read/List/Clear/ExportUICapture*`、`ExtractUICaptureHosts` 提供本地访问，轮询只传有界预览。

### 独立 MITM 配置

`experimental.mitm` 是与 `experimental.http_rewrite`、`experimental.scripts` 平级的扩展，定义见 [mitm_config.go](libbox/httpdebug/mitm_config.go)，示例见 [examples/mitm.json](examples/mitm.json)。生成 schema、完整配置校验、格式化、分享和备份均保留它；CA 私钥和本机证书不进入 JSON。

| 字段 | 含义 |
| --- | --- |
| `enabled` | 启用 HTTPS/TLS 解密，默认 false；不依赖改写、脚本或抓包开关 |
| `hosts` | 精确域名、IP 或 `*.example.com`，最多 256 项；空列表不解密任何连接，无排除列表。精确名称只匹配本域，通配项只匹配子域；不接受 `*`、`?`、URL 或内嵌端口 |
| `ports` / `packages` | 原始目的端口及应用范围；MITM 端口留空允许任意 TLS 端口，仍须命中域名；应用为空表示不限 |
| `tcp` | 默认 false；开启后支持非 HTTP ALPN 和没有协商 HTTP 协议的 TLS/TCP 字节流 |
| `block_quic` | 默认 false；在 MITM 启用且命中域名/端口/应用时，拒绝已观测的 QUIC 连接以促使应用改用 TCP |

[MITM 处理器](libbox/httpdebug/mitm.go) 用只读 TLS 探测器读取实际 ClientHello，探测阶段不写 ServerHello 或 alert。域名以实际 SNI 为准；没有 SNI 时仅接受已知 IP 目标，不以 DNS 映射冒充 SNI。无法解析的 hello、未命中域名及关闭 TCP 解密时的非 HTTP ALPN 流原样回放。route 的端口覆写不改变原始筛选端口和抓取 authority。

普通 MITM 先通过原路由完成上游握手，用 `adapter.RootPoolFromContext` 的当前证书库验证服务器链、有效期和名称，再签发客户端证书；已验证的连接直接复用，后续连接重新验证，没有跳过验证选项。支持 TLS 1.2/1.3，ALPN 跟随上游。HTTP/1.1、HTTP/2 进入 HTTP 引擎；`tcp=false` 时没有 ALPN 的连接按 HTTP/1.1 处理，`tcp=true` 时按原始 TCP 流处理。同时提供 HTTP 和其他 ALPN 的客户端也先协商上游，再选择实际处理方式。

[mitm_tcp.go](libbox/httpdebug/mitm_tcp.go) 双向转发解密后的原始字节，支持服务器先发数据、二进制和 TLS 半关闭；不把 TCP 内容送进 HTTP 解析器、改写或 HTTP 脚本。开启记录时保存为 `TLS/TCP`，分别保留两个方向的有界前缀和完整性状态，元数据带 `rawTCP`。HTTPS 和 TCP 均记录真实 SNI、双方 TLS 版本、ALPN 和上游叶证书 SHA-256。

[签发器](libbox/httpdebug/authority.go) 使用平台 P-256 `crypto.Signer`，根私钥留在 Android Keystore。三年期自签名 CA 的名称包含公钥摘要；启动验证 CA 约束、有效期、公钥及随机挑战签名。[绑定](libbox/ui_capture_signer.go) 只接受 SHA-256 摘要并验证返回的 ECDSA 签名。叶证书只含当前名称的一个 SAN，有效期不超过十二小时及根有效期，最多缓存 128 个。HTTP authority 不同时返回 421，防止 HTTP/2 合并连接越过允许域名。

HTTP 离线 Mock 的握手方式见下文。TCP 支持限于直接建立 TLS 的连接，不解密任意私有加密协议、STARTTLS 升级、HTTP/3、真实 ECH 或客户端证书认证；嵌套 CONNECT 不支持。证书固定校验或独立信任库可拒绝连接，可移除该域名或由目标客户端调整信任。QUIC 回退只处理已恢复名称的匹配流，其他 UDP、未知名称和未命中的数据报保留；不保证应用会回退。证书安装及平台限制见 [app.md](app.md#请求抓取与-mitm)。

握手设计参考 mitmproxy [`b08148b35d7679bc9be8d4a3d85b32bcea5ce250`](https://github.com/mitmproxy/mitmproxy/tree/b08148b35d7679bc9be8d4a3d85b32bcea5ce250) 的 `addons/tlsconfig.py`、`proxy/layers/tls.py` 和 `certs.py`；生产实现使用 Go `crypto/tls`。FlClash 补充参考见 [界面来源](flclash.md#参考版本与来源清单)。

### JSON 配置中的 HTTP 改写

`experimental.http_rewrite` 仅包含声明式 HTTP 规则及静态资源，定义见 [rewrite_config.go](libbox/httpdebug/rewrite_config.go)，完整示例见 [examples/http-rewrite.json](examples/http-rewrite.json)。它不包含 MITM 或脚本。以下本地 Mock 通过平级 MITM 开启指定 HTTPS 域名：

```json
{
  "experimental": {
    "mitm": {"enabled": true, "hosts": ["api.example.test"]},
    "http_rewrite": {
      "enabled": true,
      "map_local": [{
        "pattern": "^https://api\\.example\\.test/mock$",
        "data": "{\"ok\":true}",
        "headers": [{"name": "Content-Type", "value": "application/json"}]
      }]
    }
  }
}
```

启用 HTTPS MITM 前需准备并安装 CA，入口见 [高级配置](flclash.md#配置页与内核差异)。所有扩展的 `enabled` 默认 false，随本地服务启动/重载生效，停止服务或移除配置后结束；手动抓包只控制记录。应用与失败恢复见 [app.md](app.md#配置处理生命周期)。示例使用测试域名，移植时替换域名及映射地址，再合并相应的 `experimental` 字段到自己的 sing-box JSON。

| 字段 | 含义 |
| --- | --- |
| `enabled` | 启用声明式 HTTP 处理，默认 false |
| `ports` / `packages` | 原始目的端口及应用范围；端口默认 80、443、8080、8443，最多 32 个；应用为空表示不限 |
| `max_body_bytes` | 正文缓冲及输出上限，1～1048576 字节，默认 1 MiB；与记录前缀限额独立 |
| `resources` | 静态资源字典，值为 `{ "data_type": "text"或"base64", "data": "..." }` |

规则总数最多 256，扩展 JSON 最多 2 MiB。规则有必填 `pattern`、可选 `name` 和 `disabled`（默认 false）。`pattern` 对进入引擎的完整原始 URL（含查询参数）匹配，请求与响应阶段使用相同值。声明式正则采用 Go RE2，最多 4096 字节、32 个捕获组，不支持前后查找或反向引用；替换模板支持 `$1`、`${name}`、`$$`。正文正则中的 `^`、`$` 按行匹配。

| 能力 / 字段 | 规则内容及行为 |
| --- | --- |
| Header Rewrite / `header_rewrite` | `phase`: `request`或`response`；`operation`: `add`、`delete`、`replace`、`replace_regex`；指定 `header`，普通操作用 `value`，正则操作用 `regex`/`replacement`。add 保留重复字段；replace 仅替换已存在字段，新增或覆盖可依次 delete/add。请求 Host 仅允许 replace/replace_regex |
| URL Rewrite / `url_rewrite` | `action`: `header` 透明改写并更新 Host，`302`/`307` 返回重定向，`reject` 返回 403；前两类使用绝对 HTTP(S) `replacement`；首条命中生效 |
| Map Remote / `map_remote` | `replacement` 指定绝对 HTTP(S) URL，默认更新 Host，`preserve_host: true` 保留 Host；首条命中生效。与 URL header 同时匹配时决定最终目标 |
| Body Rewrite / `body_rewrite` | `phase`、URL `pattern`、正文 `regex` 和 `replacement`；所有匹配规则依次执行 |
| Map Local / `map_local` | 首条匹配返回本地响应。`status` 为 200～599，默认 200；`headers` 为 `{name,value}` 数组；`data_type` 为 text、base64 或 tiny-gif，`data` 为内容；也可用 `resource` 引用资源，与 data/data_type 互斥 |

合并处理顺序：请求 Header → URL → Map Remote → Body → 独立请求脚本 → Map Local → 上游 → 响应 Header → Body → 独立响应脚本；各扩展只在自己的范围与启用状态下参与。重定向、拒绝或脚本 Mock 提前结束请求路径，响应仍经过响应阶段。异常、超时、非法脚本结果或上游失败返回 502，超限请求正文返回 413。

请求处理出错时，HTTP/1 立即终止后续正文读取，并随错误响应关闭连接，避免正文清理等待暂停的上传而阻塞 413/502。HTTP/2 只关闭本次请求正文，保留同一连接处理其他请求；成功的请求处理恢复正常读取截止时间。

只缓冲命中的报文，先按原始头检查编码及流式类型，再读取正文；支持的编码与分层限制见 [资源与完整性边界](#httphttps-请求调试)。压缩前后均检查限额；不支持/无效编码和非 UTF-8 文本保留原文。响应超限回放已读前缀并继续转发，请求正文超过配置限额仍返回 413。SSE、gRPC/grpc-web、NDJSON、JSON sequence、multipart/x-mixed-replace、协议升级、HEAD 和 206 响应跳过正文处理；无需正文的规则仍可执行。改写后移除旧压缩、摘要、ETag、范围和 trailer 信息，重新计算 Content-Length；禁止手动改变分帧、升级头或 101 状态。HEAD/204/205/304 不发送不允许的正文。

[body_budget.go](libbox/httpdebug/body_budget.go) 为所有连接的正文处理、预览/HAR 解码及 Cron 共用 64 MiB 工作预留预算。读取前按 `8 × max(4096, max_body_bytes + 1)` 预留 wire、分层解码、改写及脚本副本的余量；处理后的正文和超限响应回放持有预留，直至消费者关闭正文。预算不足时不读取原正文，跳过正文改写及相应脚本并透传原流，记录跳过原因；请求脚本即使不读取正文也可能生成 Mock，仍须预留。该预算限制准入的正文工作，不是 Goja 任意分配或进程 RSS 的硬隔离，也不是设备压测得出的容量上限。zstd 另设 8 MiB 解码窗口/内存参数及单解码器并发。

未改变 origin 的请求沿用原出站、解析结果和网络选项；跨 origin 目标重新进入同一核心，保留入站、来源和应用归属，清除旧目的地/规则缓存并阻止递归处理，无直连回退。记录保留原 URL 与命中说明，路由字段表示原连接。

为支持离线静态/脚本 Mock，[rewrite_transport.go](libbox/httpdebug/rewrite_transport.go) 对确定按 HTTP 处理且配置了 HTTP 规则的 TLS 连接先完成客户端握手，实际转发时才连接上游。每次真实上游仍验证证书链、名称及有效期，Mock 不伪造上游 TLS 信息。纯 MITM 和原始 TCP 流使用上游优先握手。

### 独立脚本与 Cron

`experimental.scripts` 定义于 [script_config.go](libbox/httpdebug/script_config.go)，示例见 [examples/scripts.json](examples/scripts.json)。`enabled` 默认 false；`http` 和 `cron` 共计最多 256 条，JSON 最多 2 MiB。每条规则使用 `source` 或 `resource`，二者互斥；资源保存在本扩展的 `resources` 字典中，每段源码最多 128 KiB。校验编译脚本及时间表达式，但不执行脚本、访问网络或读取本机源码文件。

`ports`/`packages` 只限制被处理的 HTTP 流，默认端口与 HTTP 改写相同，不限制脚本主动发出的 HTTP 请求。`max_body_bytes` 默认 1 MiB，范围 1～1048576，控制脚本正文及 `$httpClient` 输入/输出上限；HTTP 规则自己的较低限额同样适用于其客户端。

HTTP 规则指定 `pattern`、`phase`（request/response）、可选 `name`/`disabled`，每个方向只执行第一条匹配脚本；失败不回退到下一条。HTTPS 必须另行启用 MITM 并匹配域名。接口由 [script.go](libbox/httpdebug/script.go) 提供：

- `$request` 提供 url/method/headers/id；请求阶段 `requires_body: true` 时提供 body。响应阶段的 request 不携带正文；URL 是前序规则处理后的值。
- `$response` 提供 status/headers，`requires_body: true` 时提供 body；内容包含先前静态改写结果。
- `$done()`/`$done({})` 保持原报文并结束脚本。请求可返回 url/headers/body/response/abort，以及 B-box 的 method；响应可返回 status/headers/body/abort。动态 Mock 的 response 支持 status/headers/body。
- 默认 body 为 UTF-8 字符串，`binary_body: true` 使用 Uint8Array。替换当前报文 body 要求 requires_body，生成 Mock response.body 不要求读取请求正文。
- `full_headers: true` 以 `{field,value}` 数组保留重复头；返回值接受数组或对象，普通输入对象每字段只取首值。
- `argument` 通过 `$argument` 提供；`$script` 包含 name/type/binaryBodyMode。console.log/info/warn/error/debug 进入普通服务日志，每次最多 10 条、正文最多 2 KiB。

HTTP `timeout_ms` 默认 200，范围 1～2000；规则可降低自己的 max_body_bytes。每次独立 Goja VM，HTTP 最多 4 个解释器，Cron 使用独立的 2 个解释器，避免慢定时 HTTP 请求占用交互请求的执行名额；等待名额也计入该次超时并报告具体等待原因。调用栈最多 256 层，原生 JS 正则另设 200 毫秒截止时间。执行超时不等于进程级硬内存隔离。

Cron 规则含 `cron`、可选 `name`/`disabled`、`timezone`、`argument`、`timeout_ms`（默认 5000，范围 1～30000）及 source/resource。支持五段“分 时 日 月 周”或六段“秒 分 时 日 月 周”，支持范围、列表、步长及月份/星期名称；时区使用 IANA 名称，未指定时采用服务启动时的 Android 设备时区。运行环境包含 tzdata。`$cronexp` 提供原表达式，`$trigger` 和 `$script.type` 为 cron，没有 `$request`/`$response`。

[script_cron.go](libbox/httpdebug/script_cron.go) 随本地服务运行，成功启动后才调度，重载/停止时取消正在执行的任务、丢弃等待队列，并等待调度器及 worker 退出。抓包启停不重置调度或取消 Cron。到期任务进入有界 FIFO 队列，由 2 个 worker 持续取出执行；同一批到期任务按配置顺序入队，后续触发不能越过已排队的任务。同一任务在排队或运行时再次到期，跳过该次触发并写入日志，不积累重复调用；排队与运行的调用总数不超过已启用的 Cron 数量，最多 256 个。

排队期间只保留任务索引，不创建 VM、不预留正文预算，也不消耗 `timeout_ms`；出队执行时才开始原有的解释器等待与执行超时。每次执行持有正文预算，预算不足则跳过并写入日志，worker 继续处理后续任务。设备休眠错过的周期不补跑，也不申请系统定时唤醒。任务必须调用 `$done()`，调用后立即停止该次执行；异常及超时写入普通日志。

#### 脚本 HTTP 客户端

HTTP 请求/响应脚本与 Cron 共用 [HTTP 接口](libbox/httpdebug/script_http.go)：`$httpClient.get/post/put/patch/delete/head/options(url或{url,headers,body}, callback)`。回调接收 error/response/data；成功时 error 为 null，response 有 status/headers，data 为 UTF-8 文本；失败时 error 为字符串、response 为 null、data 为 undefined。body 只接受字符串，不自动转换对象或解码压缩响应，不跟随重定向。

- HTTP 规则默认禁止主动联网。通过规则的 `http_client: {"allowed_origins":["https://api.example.test"],"max_requests":1}` 显式授权。允许列表最多 16 项，按 scheme、完整主机和有效端口匹配；只接受 HTTP(S) origin，可带根路径斜杠，不接受通配符、其他路径、凭据、查询或片段。域名大小写、末尾点和默认端口会规范化；不能用 Host 头切换到其他 origin。
- HTTP 每次执行默认最多 4 次请求，`max_requests` 可降到 1～4；Cron 默认最多 16 次，可降到 1～16。失败和被拒绝的调用也计数。Cron 未配置 `http_client` 时保留原有路由联网能力；一旦配置即按允许列表限制，空列表禁止联网。
- 网络调用与回调在 VM 的调用线程中顺序执行，前一调用返回后才执行后续脚本或定时器；所有等待、拨号、TLS 和正文读取共用本次 `timeout_ms`，不接受额外的 timeout、policy、proxy 或 insecure 选项。HTTP 脚本需要较长网络等待时可显式设置 `timeout_ms: 2000`，仍不超过其总时限。
- 每次客户端调用额外持有 [正文工作预算](#json-配置中的-http-改写)，直到回调退出；即使脚本不读取被拦截正文也计入预算。预算不足通过回调报错，不发起请求。

请求统一以 `b-box-scripts` 入站上下文重新匹配**当前 sing-box 实例的路由**，使用该实例的证书库并验证 HTTPS。该上下文不是新增监听器；不会回退设备直连，也不递归触发 HTTP 改写、脚本或记录。HTTP 规则的允许列表独立于 MITM 域名，后者只决定哪些客户端连接可解密。停服、重载、服务实例更换或原请求取消会结束在途调用。

#### 脚本持久存储

HTTP 与 Cron 规则可设置 `storage_namespace`，格式为 1～64 个 ASCII 字母、数字、点、下划线或连字符，首字符须为字母或数字。[存储实现](libbox/httpdebug/script_storage.go) 将这个配置值固定到本次 VM，脚本不能枚举或切换命名空间。命名空间在本机所有配置之间按完整名称共享；不同规则、HTTP/Cron 或配置只有显式使用相同名称才共享数据。修改脚本源码、规则名称或顺序不会改变指定的命名空间。

- `$persistentStore.read(key)` 返回字符串，不存在时返回 null；空字符串是有效值。必须显式提供字符串 key，读取失败或未授权时抛出异常。
- `$persistentStore.write(value, key)` 成功返回 true；value 必须为字符串，null 表示删除。配额、存储不可用、未授权或 I/O 失败返回 false，首次失败写入普通日志；非法参数类型抛出异常。
- 每个 key 为 1～256 UTF-8 字节，单值最多 16 KiB；每个命名空间最多 128 键，序列化 JSON（含转义及结构）最多 64 KiB。所有命名空间合计最多 256 个、4 MiB，每次脚本最多 64 次读写调用。删除最后一键释放整个命名空间；移除规则不会自动删除数据。
- 写入在共享锁中根据最新数据检查配额、同步临时文件并原子替换，成功后才更新缓存与计数。等待锁及提交前检查脚本取消；重载准备和旧脚本共用同一实例，避免旧快照覆盖新值。每次读/写独立原子化，read 后 write 不构成跨调用事务；已经成功写入的数据不因后续脚本异常或 `$done` 结果失败而回滚。
- 存储不使用脚本 key 作为文件路径，也不提供文件系统 API。打开时校验版本、内容和限额，拒绝快照/专用目录的符号链接；中断写入的临时文件不被提升为有效数据。文件位置、备份及录制清理边界见 [应用生命周期](app.md#配置处理生命周期)。

#### 脚本定时器

[定时器](libbox/httpdebug/script_timers.go) 提供 `setTimeout(function, delayMs, ...args)`、`setInterval(function, delayMs, ...args)` 和可互换清理句柄的 `clearTimeout`/`clearInterval`。回调必须是函数，不执行字符串代码；delay 默认为 0，只接受 0～30000 的有限数值，实际最小间隔 1 毫秒。

每次 VM 最多 32 个待执行定时器、累计注册 128 次、调用回调 128 次；顶层脚本返回后在同一线程执行到期回调，支持在回调中调用 HTTP、存储和 `$done`。例如 `setTimeout(() => $done(), 50)`。重复定时器从上次回调结束后计算下一次，不补跑阻塞或休眠期间错过的间隔。

定时器不延长 HTTP/Cron 的原始超时，不跨执行或服务重启保存，不提供系统唤醒。仍须调用 `$done`；`$done`、异常、超时、原请求取消或停服会结束本次定时器。它们与 Cron 的日历调度相互独立。

未实现 Node.js 模块、通知、jq、WebView WebAPI 或完整 Surge API；不自动下载外链脚本。HTTP/改写接口参考 2026-11-03 读取的 Surge [HTTP Processing](https://manual.nssurge.com/http/overview.html)、[MITM](https://manual.nssurge.com/http/mitm.html)、[URL Rewrite](https://manual.nssurge.com/http/url-rewrite.html)、[Header Rewrite](https://manual.nssurge.com/http/header-rewrite.html)、[Body Rewrite](https://manual.nssurge.com/http/body-rewrite.html)、[Map Local](https://manual.nssurge.com/http/map-local.html)、[请求脚本](https://manual.nssurge.com/scripting/http-request.html)、[响应脚本](https://manual.nssurge.com/scripting/http-response.html)和[Cron](https://manual.nssurge.com/scripting/cron.html)；客户端、持久存储和定时器对照 2026-11-04 的 [JavaScript API Reference](https://manual.nssurge.com/scripting/api.html)。B-box 的授权、命名空间、显式 key、配额与同步回调语义以上述实现为准，不导入 Surge INI 或 Mihomo 覆写，也不宣称完全兼容其脚本。

核心集成固定 [Goja `a177d211bf606f6275674a35bc0d31d8ee0ee396`](https://github.com/dop251/goja/tree/a177d211bf606f6275674a35bc0d31d8ee0ee396) 和 [robfig/cron v3.0.1](https://github.com/robfig/cron/tree/v3.0.1)。setup-core 将模块最低 Go 版本设为 1.26.0，实际构建仍用 version.properties 的 go1.26.10；Goja 引入 regexp2/v2、sourcemap、pprof 及 x/text v0.43.0。正文解码复用当前核心依赖 [andybalholm/brotli v1.1.0](https://github.com/andybalholm/brotli/tree/v1.1.0) 和 [klauspost/compress v1.19.1](https://github.com/klauspost/compress/tree/v1.19.1)。相关许可证随 Flutter assets 注册；三个上游基线不变，统一测试与 Release 构建共用同一核心安装入口。

### 旧抓包配置清理

抓包筛选为应用私有设置。`experimental.http_debug` 已退役，[兼容处理](libbox/ui_capture_migration.go) 在解析前删除它，不解释、迁移或执行其中规则/脚本。旧本机 HTTPS 设置不会被提升为可执行配置，原子迁移见 [app.md](app.md#请求抓取与-mitm)。

旧 `http_rewrite.mitm` 和 `http_rewrite.scripts` 会移到平级扩展，保留原来的应用/端口/正文范围及脚本资源；原改写关闭时迁移目标仍关闭。旧 MITM 的默认端口（含空/null 列表）显式保留为 80/443/8080/8443，避免变为任意 TLS 端口；域名空列表不解密。存在旧排除项时关闭该 MITM，待用户重新确认允许域名；排除字段被移除。若平级与嵌套字段同时存在则明确报错，不覆盖任一份配置。其他未知字段继续由核心校验。

打开存储时，[存储清理](libbox/httpdebug/store_migration.go) 删除旧 `wire/` 中以 64 个十六进制字符 ID 命名的 `.wire` 包/密钥日志及 warning.txt，失败返回错误；不读取内容、不跟随符号链接，保留请求及无关文件，仅删除已清空的专用目录。

## Android 自动重定向兼容

当前核心依赖 [sing-tun `611d83b7a57b4719be45581eb674942ff561de6c`](https://github.com/SagerNet/sing-tun/tree/611d83b7a57b4719be45581eb674942ff561de6c)，模块版本为 `v0.9.7-0.20261023115453-611d83b7a57b`。该版本的 `redirect_ingress_discard_android.go` 只接受 hash 类型、16 字节 key、8 字节 value 的系统 map；[AOSP Android 15 的实现](https://android.googlesource.com/platform/packages/modules/Connectivity/+/refs/tags/android-15.0.0_r1/bpf_progs/netd.c) 使用两个接口编号。一加 10 Pro / Android 15 的问题报告返回 `type 1, key 16, value 4`，因此会在自动重定向启动时被上游的布局检查拒绝。不能仅按 Android 版本号选择布局。

[兼容补丁](libbox/sing-tun-android-ingress.patch) 从 BPF map 元数据读取实际 value 大小，支持单接口编号的 4 字节值及双接口编号的 8 字节值。只处理当前 TUN 的 IPv4 地址，且所有 value 槽位都必须匹配当前 TUN 接口编号；其他 map 类型、key 长度和 value 大小继续报错。保存条目保留原始长度及字节，关闭时只向布局未变且 TUN 接口编号仍匹配的 map 恢复，继续使用 `BPF_NOEXIST` 避免覆盖系统新建条目。不跳过整张系统 map 的校验或清空无关规则。

[patch_sing_tun.py](tools/patch_sing_tun.py) 在匹配的 Go 工具链准备完成后运行，核对依赖版本，将 Go 模块缓存中的源码复制到全新 `.core/.b-box/sing-tun/`，检查并应用补丁，通过当前核心的本地 `replace` 引用副本；共享模块缓存不被修改。依赖版本变化或副本已存在时失败，升级核心须重新核对补丁，不能静默沿用。该处理用于统一测试中的核心/Android smoke 和独立 Release 构建，不改变核心标签、应用上游或 FlClash 基线。

[布局回归测试](libbox/sing-tun/redirect_ingress_discard_linux_test.go) 在 Linux CI 中测试生产代码的布局校验、单/双接口归属和恢复条件，覆盖 4/8 字节、未知布局、其他接口、map 大小变化和 TUN 更换。它不操作系统固定路径的 BPF map；一加设备上的实际启动、联网、停服恢复及与其他 VPN 的交互仍需 Root 实机验证。

## 保留与已撤销的差异

2026-10-25 已回退历史 MOD-013 核心裁剪，恢复上游 NaiveProxy、Tailscale、OpenVPN、Snell、OpenConnect、USBIP、Tor、Tailscale/Taildrop 客户端绑定及原生 Shell/PTY。`libbox-trim.patch` 和裁剪专用校验器已删除，不能恢复旧裁剪标签或继续使用当时生成的裁剪 AAR。

当前 legacy 补丁只排除 Android API 21 产物，不裁剪标准核心协议、上游构建标签或已有导出绑定。`ui_*.go` 是对标准核心的新增适配，不能将整个 B-box 核心描述为“与上游完全无差异”。

## 构建、同步和验证

- [Release 工作流](.github/workflows/build-release.yml) 仅手动构建、不运行测试；通过共享构建 Action 使用 JDK 17、版本文件指定的 Go 和 NDK 28.0.13004108，执行上游 `make lib_install` 与 `build_libbox -target android -platform android/arm64`，与 Flutter Release AAR 并行准备。
- [统一测试工作流](.github/workflows/newui.yml) 的 Android job 使用同一构建 Action，核心目标为 `android/amd64`，用于 JVM 及 [Android 模拟器矩阵](app.md#验证与历史记录) 的回归。两种架构的构建均要求不存在 `libbox-legacy.aar`，再将标准产物放到 `app/libs/libbox.aar`。
- 同一工作流的 core job 使用共享准备 Action，运行 `go test -ldflags=-checklinkname=0 github.com/sagernet/sing-tun -run '^TestAndroidIngressDiscard' -count=1` 及 `go test -ldflags=-checklinkname=0 ./experimental/libbox -run '^TestUI' -count=1`。所有生产扩展均参与编译，所有 `TestUI` 测试自动纳入，不再维护具体扩展名的筛选清单；该 job 不构建核心 AAR。另执行 `go test -race ./experimental/httpdebug -count=1` 和 `go test -ldflags=-checklinkname=0 ./route -run '^TestBboxHTTPDebug' -count=1`，覆盖 HTTP/MITM 抓取引擎、数据竞争及原出站和嗅探字节保留。
- 对应回归文件为 [ui_http_test.go](libbox/ui_http_test.go)、[ui_egress_test.go](libbox/ui_egress_test.go)、[ui_lan_test.go](libbox/ui_lan_test.go)，覆盖请求限制/重定向、源回退/公网 IP 校验/解析失败后继续竞速/取消/服务替换、监听转换和端点生成。

[httpdebug_test.go](libbox/httpdebug/httpdebug_test.go) 覆盖 HTTP、SSE/WebSocket、存储和导出；[mitm_test.go](libbox/httpdebug/mitm_test.go) 覆盖 TLS 1.2/1.3、HTTP/1.1/2、同连接请求的独立 ID 与连接关联、上游连接复用、独立信任链、过期/错误名称/不受信任上游、客户端 pinning、未匹配域名和非 HTTP hello 回放、重复 Cookie、请求/响应 trailers、大正文、并发流、SSE 取消、跨 authority 拒绝、QUIC 筛选及停止/拨号超时。[authority_test.go](libbox/httpdebug/authority_test.go) 覆盖独立根身份、SAN/IP、叶证书缓存和密钥错配；[ui_capture_signer_test.go](libbox/ui_capture_signer_test.go) 验证平台签名接口的摘要、算法及返回签名校验。

[store_concurrency_test.go](libbox/httpdebug/store_concurrency_test.go) 覆盖正文句柄复用和关闭边界、前缀/转发总量、并发双向写入与详情快照，以及模拟慢写时其他记录和清空继续工作、完成状态等待计数提交。由现有 HTTP 调试 `-race` 测试入口运行。

[tls_selection_test.go](libbox/httpdebug/tls_selection_test.go) 在已有 48 条连接时执行真实 ClientHello 探测、TLS 握手及 HTTP Mock，检查仍接管请求，并验证探测取消、未交接关闭及正式接管后的服务回收。[recording_test.go](libbox/httpdebug/recording_test.go) 在同一 HTTP/1.1、HTTP/2 连接反复启停记录，检查处理不中断、连接 ID 不变；结合既有 SSE、WebSocket 和 TCP 测试验证停止后继续传输及记录冻结。[diagnostics_test.go](libbox/httpdebug/diagnostics_test.go) 检查无 HTTP 请求的 TLS 结束提示、错误页及迟到更新隔离。

[body_processing_test.go](libbox/httpdebug/body_processing_test.go) 覆盖新编码/多层/重复头在改写、预览与 HAR 中一致，压缩膨胀、响应回放、上传截断、预算不足时零读取和正文关闭后释放；[streaming_processing_test.go](libbox/httpdebug/streaming_processing_test.go) 覆盖 HTTP/2 gRPC 在上传结束前响应、trailers、Expect: 100-continue 及慢上传取消。[script_resources_test.go](libbox/httpdebug/script_resources_test.go) 用实际等待中的 Cron HTTP 请求占满 Cron 池，验证 HTTP 脚本仍执行以及取消释放资源。这些是工作流配置的回归范围，实际通过结果以 [验证记录](app.md#验证与历史记录) 为准。

[upload_rejection_test.go](libbox/httpdebug/upload_rejection_test.go) 使用真实 TCP 连接，覆盖固定长度/chunked 上传超限后保持连接且停止发送时，HTTP/1 及时返回完整 413、关闭连接并完成记录及预算释放；还覆盖未读取正文就发生的脚本错误返回 502，以及 HTTP/2 超限返回 413 后同一连接仍可处理新请求。由现有 HTTP 调试 `-race` 测试入口运行。

[script_api_test.go](libbox/httpdebug/script_api_test.go) 覆盖两阶段 HTTP 客户端的路由注入、允许 origin/Host/次数/正文限制、重定向、TLS 信任、取消和全局预算；[script_storage_test.go](libbox/httpdebug/script_storage_test.go) 覆盖持久化、命名空间隔离及 HTTP/Cron 共享、字节/键/全局配额、并发写入、原子失败恢复、符号链接和重载/清空记录边界；[script_timers_test.go](libbox/httpdebug/script_timers_test.go) 覆盖顺序、参数、interval 清理、回调异常、上限及 HTTP/Cron 截止时间。新增用例由现有 `httpdebug -race` 入口自动纳入；它们的存在不代表当前源码已运行通过。

[script_cron_queue_test.go](libbox/httpdebug/script_cron_queue_test.go) 通过阻塞路由拨号控制真实脚本的完成顺序，覆盖同批任务的 FIFO 执行、新触发不能越过旧等待任务、排队/运行时重复触发不扩大队列、等待不消耗执行超时、单次超时后继续执行、256 条配置上限及停服时取消/丢弃队列和资源释放。由同一 `httpdebug -race` 入口运行。

连接关联回归还包括 [路由测试](libbox/route/httpdebug_test.go) 从真实流量跟踪器取得 UUID、穿过嗅探缓存、清空连接历史后重新打开请求存储，以及无跟踪器的 HTTP；[改写测试](libbox/httpdebug/rewrite_test.go) 检查跨目标改写仍保留原始连接 ID，[TCP 测试](libbox/httpdebug/mitm_tcp_test.go) 检查 TLS/TCP 关联，[迁移测试](libbox/httpdebug/store_migration_test.go) 检查旧记录不将 `captureId` 误认为连接 ID。

[ui_capture_test.go](libbox/ui_capture_test.go) 覆盖本地设置及 schema/退役字段；[路由测试](libbox/route/httpdebug_test.go) 通过真实 TLS 握手核对原选定出站、SNI 与 DNS 映射隔离、端口覆写、CONNECT 确认顺序、证书库及未匹配流的字节回放。[mitm_tcp_test.go](libbox/httpdebug/mitm_tcp_test.go) 覆盖 TLS/TCP 二进制、混合 ALPN、服务器先发、半关闭及记录/导出边界；[script_cron_test.go](libbox/httpdebug/script_cron_test.go) 覆盖独立 HTTP 脚本、Cron 语法/时区/DST、回调、证书校验和调度生命周期；[ui_processing_test.go](libbox/ui_processing_test.go) 验证扩展 schema 及迁移；[store_migration_test.go](libbox/httpdebug/store_migration_test.go) 覆盖旧文件清理、请求保留和符号链接隔离。Android 真正的 Keystore/gomobile 签名与模块打包由 [平台测试](app.md#验证与历史记录) 覆盖；Linux 测试不能代替 Android 系统 CA 模块或目标应用的实机验收。获准本机测试时可使用 [verify_httpdebug.py](tools/verify_httpdebug.py)，它不构建 Android 绑定。

升级核心时先检查补丁是否仍准确排除 legacy 分支，核对 sing-tun 依赖版本及自动重定向补丁，检查 HTTP 调试路由/服务生命周期补丁及覆盖文件，再逐项核对扩展所依赖的路由、配置解析、HTTP 客户端和生成绑定。Go 导出名与 gomobile 生成的 Java 方法名不能凭拼写推断，例如 `UIHTTPGet` 当前由 Kotlin 调用为 `Libbox.uihttpGet`。任何扩展增删都要核对共享准备 Action 的安装范围，以及统一测试与 Release 两条工作流的调用。

以上列出源码和工作流的覆盖范围。连接与正文处理优化的实际源码 SHA、Actions 结果和未覆盖范围见 [MITM 连接与正文处理优化验证](app.md#mitm-连接与正文处理优化验证)。存储锁与文件句柄优化的实际结果见 [界面刷新与存储优化验证](app.md#界面刷新与存储优化验证)。此前独立 MITM/TCP/Cron 的验证结果见 [验证记录](app.md#独立-mitmtcpcron-验证)；此前配置改写的实际检查、源码 SHA、Actions 运行及未覆盖范围见 [JSON 配置改写验证](app.md#json-配置改写验证)，此前的请求录制/CA 记录见 [MITM 分支验证](app.md#mitm-分支验证)。本机构建限制见 [AGENTS.md](AGENTS.md)。
