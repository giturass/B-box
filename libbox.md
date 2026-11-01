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
| HTTP/HTTPS 请求抓取 | [httpdebug.patch](libbox/httpdebug.patch)、[httpdebug/](libbox/httpdebug/)、[route/](libbox/route/)、[ui_capture.go](libbox/ui_capture.go) | HTTP 录制、Root/eBPF HTTPS 明文采集、域名提取、记录及导出绑定 |

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

[patch_httpdebug.py](tools/patch_httpdebug.py) 在匹配核心中安装 [路由与生命周期补丁](libbox/httpdebug.patch)、[抓取引擎](libbox/httpdebug/)、[路由适配](libbox/route/) 和 `ui_capture*.go`。明文 HTTP 在选路及连接跟踪之后记录，继续使用原出站的 `ConnectionHandler`，保留路由、目的地址和应用归属；补充嗅探只修改元数据副本，不匹配的字节回放到原连接。没有新增代理监听器或直连回退。

HTTPS 默认关闭；开启后通过 [专用采集器](tools/ebpf/) 的 eBPF uprobes 观察目标进程在 `SSL_read`/`SSL_write` 及 `_ex` 公共接口中读写的明文。它不从网络密文推导会话密钥，也不接管 TLS 握手；应用照常验证服务端证书。CA 生成、证书安装/模块、MITM、HTTP 改写和 JavaScript 已移除。

采集器使用 [cilium/ebpf v0.22.0](https://github.com/cilium/ebpf/tree/v0.22.0)，依据 [Linux uprobes](https://www.kernel.org/doc/html/latest/trace/uprobetracer.html) 和公开 SSL 函数 ABI 工作，不读取 BoringSSL 私有结构偏移。支持 ARM64/x86_64 的 64 位库，需要 Root、Linux 5.10 或更新内核、BPF/uprobe/perf 支持及 SELinux 许可。自动检查系统 Conscrypt/libssl 和当前 Android 用户的进程映射，可补充最多 16 个绝对库路径；查找 ELF 动态/本地符号、有界 XZ mini-debugdata 及 APK 内未压缩的对应 ABI `.so`。没有兼容库或挂载失败时启动报错；挂载库数不表示所有目标应用均受支持。32 位进程、Go TLS、不能通过下述识别的无符号自有/裁剪 TLS 库和 QUIC/HTTP/3 不在当前支持范围。

ARM64 Chrome/Chromium 的 `libchrome.so`、`libmonochrome*.so`、`libtrichrome.so` 和 `libwebviewchromium.so` 缺少 SSL 符号时，使用[静态识别器](tools/ebpf/chromium.go)检查当前文件，不绑定 Chrome 版本、build ID、代码哈希或固定地址。依据 BoringSSL/Chromium 源文件字符串、公共错误码、标准 unwind 函数范围及调用关系识别 `SSL_new/free/read/write`；必须确认分配/释放配对、Chrome socket 中同一 SSL 对象的调用、整数长度 ABI，以及 `SSL_read` 对 `SSL_peek` 结果的消耗和原值返回。源码行号、寄存器和结构字段的绝对偏移不作为版本签名。识别有歧义、符号冲突、缺少生命周期或不支持的编译布局时不挂载猜测地址，并在抓包状态中提示未支持的库；其他兼容库继续采集。普通更新移动代码/行号不需要维护地址表，修改函数实现、编译布局或删除识别信息仍可能需要更新识别器。

[ELF 检查](tools/ebpf/chromium_elf.go)核对可执行加载段和 `.eh_frame_hdr`/FDE/CIE 范围，目前接受 Android clang 常用的 `pcrel/sdata4`、`datarel/sdata4` 表及 `zR` CIE。只缓存有界候选函数，分块扫描 `.text` 并保留取消检查；APK 探针地址为 ZIP 数据偏移加 ELF 文件偏移，不使用运行时虚拟地址。安装后的新 inode 会重新检查，旧结果不会按包名或版本号套用。

采集器的 `--inspect-library <ELF 或 APK 路径>` 只读诊断入口使用同一识别器，输出 APK 数据偏移及实际识别出的函数文件偏移；它不需要 Root、不加载 BPF，也不读取目标进程或网络数据。可用 CI 构建的对应 ABI 采集器检查安装文件，结果仅证明静态识别，不能当作实机抓包通过。

[库发现](tools/ebpf/discovery.go) 优先检查显式库路径和系统 TLS 库，首个库的完整探针挂载成功后发送 ready，其余进程、ELF/APK 及调试符号检查在独立工作协程继续，新增挂载通过 status 更新数量。最多保留三条有界库识别/挂载诊断，经 ready/status 传递，不将诊断当成全局丢数据或清零挂载数。事件读取和完整性检查不等待整个扫描结束，ready 始终先于采集数据。启动进度覆盖 BPF 加载、perf 缓冲及库检查/挂载，Android 的超时与协议处理见 [app.md](app.md#请求抓取与-rootebpf)。owner EOF 取消库扫描及后续文件读取，先释放探针并等待扫描退出，再关闭共享 BPF 程序和 map；扫描仍保留 2048 个文件身份及 64 个挂载库的上限。

采集器在确认 Root 后直接尝试提高自身的 `RLIMIT_MEMLOCK`，支持仍受该限额约束的内核；提高失败后由实际 BPF 加载报告分配错误，使用 memcg 计费的内核不依赖提高此限额。不导入会在 Go 初始化阶段探测 BPF map 的 [rlimit 包](https://github.com/cilium/ebpf/blob/v0.22.0/rlimit/rlimit_linux.go)，避免 Android 应用 seccomp 在权限错误返回前以 `SIGSYS` 终止进程。

[Android 平台补丁](tools/ebpf/cilium-ebpf-android.patch) 修正该版本依据 `runtime.GOOS` 判断 Linux 的问题：Android 构建使用上游由构建标签选出的 `platform.Native = Linux`，让 token、mountinfo、map、程序及 BTF 加载使用 Linux 路径，并按 Linux 前缀匹配内核版本。原先的 `bpf token: not supported on android` 在调用 BPF 系统调用前产生，不能据此认定内核或 SELinux 拒绝了操作。未提供 BPF token 时继续使用上游的普通特权加载路径，仍保留真实内核和权限错误。[prepare.py](tools/ebpf/prepare.py) 核对依赖版本后刷新 `tools/ebpf/build/` 下的副本、应用补丁，并生成备用模块文件供构建和测试使用；不修改共享模块缓存、已提交的 `go.mod/go.sum` 或 libbox 的依赖。

仅接受探针观察到 `SSL_new` 后的新连接；`SSL_clear`/`SSL_free` 区分连接代次。开启后需让目标应用重新连接。UID 在 BPF 中筛选，排除 B-box 自身及其他 Android 用户；共享 UID 使用同一选择，包名集合保留在记录中。HTTPS 可以观察选中应用的本机连接，不依赖其是否通过 sing-box；本地服务的启停仍决定抓包会话的生命周期。

域名和端口按每条 HTTP 请求判断，支持 HTTP/2 合并连接上的不同域名。HTTPS 域名列表为空表示不限制受支持的域名；精确名称匹配本域，`*.example.com` 匹配子域，排除优先。通用包含/排除筛选同时约束 HTTP 和 HTTPS，HTTPS 排除仅影响明文录制。默认端口 80、443、8080、8443，最多 32 个。eBPF 尚未关联套接字，HTTPS 端口使用 HTTP authority（默认 443），源/目的套接字地址、出站和路由规则保持未知；记录明确标明来源、PID/UID 和此限制。

[明文解析器](libbox/httpdebug/plaintext.go) 处理 HTTP/1.1 分片、顺序请求、信息响应、定长/chunked/流式正文；[HTTP/2 解析器](libbox/httpdebug/plaintext_h2.go) 要求完整客户端前言，以独立 HPACK 状态和 stream ID 还原并发报文。无法确认正常结束的 close-delimited 响应保留不完整标志。升级仅记录 HTTP 握手，HTTP trailers 不加入头部预览。HTTP/2 server push、RST_STREAM、过大帧/头或不能确定边界的协议终止该连接的录制并报告原因，网络连接本身继续运行。

资源与完整性边界：

- 最多记录 300 条、磁盘 32 MiB；每方向正文默认保留 64 KiB，最高 1 MiB，头部上限 64 KiB。普通 HTTP 最多接管 32 条连接、每条 HTTP/2 最多 16 个流；eBPF 最多同时解析 32 条连接，每条 HTTP/2 最多 32 个流。
- 每方向明文队列最多 256 KiB；单次 SSL 调用最多 128 KiB，以 4096 字节分片输出，超过限额报告缺口。事件按连接代次、每方向序号与偏移重组，总重组缓存最多 4 MiB；同方向重叠调用、重复、缺失、perf 丢失和 BPF 输出失败都有错误状态，不把后续片段拼成完整请求。
- 局部溢出、解析错误和两分钟无数据的连接终止录制；全局丢事件或采集器退出停止抓包。未结束记录保留不完整标志及错误，服务重载/停止关闭管道、采集器和处理器。抓包不会自动恢复。

预览/HAR 的 gzip/deflate 解码上限 64 KiB；不支持的编码或二进制保留 Base64，HAR 另保留原始压缩前缀和截断/完整性标记。耗时仅为总时长，不冒充 DNS/TCP/TLS 分阶段计时。cURL 要求请求头与正文完整且未截断，正文缺失则拒绝导出。停止服务后仍可查询保存记录。

`extractHosts` 额外记录经过本地服务的可见 TLS/QUIC 名称，条目标为“仅域名”，不伪造明文或响应，也不阻断 QUIC。域名导出对当前搜索条件下的全部保留记录去重，HAR 跳过仅域名条目；ECH 隐藏的名称无法由该观察恢复。

`CommandServer.StartUICapture(directory, config, fd, source)` 与核心重载/停止串行，复制 Root 管道 FD，并通过 `UICaptureSource.Close` 管理采集器；HTTP 模式不需要 Root 管道。其余 `Open/Validate/Stop/Read/List/Clear/ExportUICapture*` 和 `ExtractUICaptureHosts` 提供本地设置校验、状态及记录访问。原始正文通过文件导出，轮询仅返回有界预览。设置持久化与 Binder 生命周期见 [app.md](app.md#请求抓取与-rootebpf)。

### 旧抓包配置清理

抓包筛选是应用私有设置，不属于 sing-box schema。`experimental.http_debug` 类型、编辑绑定及示例已移除。[兼容清理](libbox/ui_capture_migration.go) 在 libbox 解析前仅删除该退役字段，不解释或执行其中内容；其他未知字段继续由核心拒绝。格式化以及应用配置保存会移除它，未包含该字段的内容保持原样。原字段中嵌入的规则/脚本不会迁移为可执行内容，也不会自动开启抓包。

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
- [Android smoke 工作流](.github/workflows/newui-android.yml) 使用同一构建 Action，核心目标为 `android/amd64`，用于 x86_64 模拟器，同时编译采集器的 x86_64 与 ARM64 版本。两种构建均要求不存在 `libbox-legacy.aar`，再将标准产物放到 `app/libs/libbox.aar`。
- [UI 验证工作流](.github/workflows/newui.yml) 使用共享准备 Action，运行 `go test -ldflags=-checklinkname=0 github.com/sagernet/sing-tun -run '^TestAndroidIngressDiscard' -count=1` 及 `go test -ldflags=-checklinkname=0 ./experimental/libbox -run '^TestUI' -count=1`。所有生产扩展均参与编译，所有 `TestUI` 测试自动纳入，不再维护具体扩展名的筛选清单；它不构建核心 AAR。另执行 `go test -race ./experimental/httpdebug -count=1` 和 `go test -ldflags=-checklinkname=0 ./route -run '^TestBboxHTTPDebug' -count=1`，覆盖 HTTP/eBPF 抓取引擎、数据竞争及原出站和嗅探字节保留。
- 对应回归文件为 [ui_http_test.go](libbox/ui_http_test.go)、[ui_egress_test.go](libbox/ui_egress_test.go)、[ui_lan_test.go](libbox/ui_lan_test.go)，覆盖请求限制/重定向、源回退/公网 IP 校验/解析失败后继续竞速/取消/服务替换、监听转换和端点生成。

[httpdebug_test.go](libbox/httpdebug/httpdebug_test.go) 保留 HTTP 路由、SSE/WebSocket、存储上限和导出回归；[plaintext_test.go](libbox/httpdebug/plaintext_test.go) 覆盖分片、HTTP/2 并发/跨域筛选、缺口、半截正文、采集器退出与源关闭。[ui_capture_test.go](libbox/ui_capture_test.go) 检查本地设置、退役字段清理和 schema；[路由测试](libbox/route/httpdebug_test.go) 检查原出站和字节回放。获准本机测试时可使用 [verify_httpdebug.py](tools/verify_httpdebug.py)，它不构建 Android 绑定。

UI 验证还编译 Linux BPF/采集器、运行 [事件测试](tools/ebpf/events_test.go)、[库发现测试](tools/ebpf/discovery_test.go)、[平台回归测试](tools/ebpf/testdata/platform_test.go) 和 [真实 OpenSSL smoke](tools/ebpf/smoke.py)，核对首个挂载不等待慢库、后续挂载计数/去重、取消与探针释放、失败不能报告 ready、两方向明文、分片/重复处理及 owner EOF 退出。平台测试复制到补丁依赖中，检查 Linux 内核版本匹配及 map/程序/BTF 加载，拒绝加载前的平台不支持错误；允许真实内核或权限限制，不将其通过视为抓取可用。共享 Android 构建 Action 使用同一补丁和 NDK 编译 PIE 采集器，链接 16 KiB page alignment，打包为 `libbbox_capture.so`；x86_64 构建额外生成 Android 平台回归二进制，由 Android smoke 在 API 24/31/35 的无特权 shell 中执行。生成的依赖副本、模块文件、BPF 对象和可执行文件不入库。Linux smoke 和无特权平台测试不代替 Android 实机内核、SELinux、Conscrypt 和目标应用验收。

[Chromium 识别回归](tools/ebpf/chromium_test.go)纳入同一 Go 测试入口，以[带来源和许可的实际 ARM64 指令片段](tools/ebpf/testdata/chromium/README.md)构造无符号 ELF，改变函数排列、虚拟地址和错误行号，并检查 APK 文件偏移、取消、歧义/损坏 unwind、错误参数、非消耗读取和缺少释放配对。库发现及明文管道测试另检查兼容结果与未支持提示并存、ready 在前、挂载数保留及其他应用继续采集；这些静态指令/协议回归不能代替 Chrome 实机抓取结果。

升级核心时先检查补丁是否仍准确排除 legacy 分支，核对 sing-tun 依赖版本及自动重定向补丁，检查 HTTP 调试路由/服务生命周期补丁及覆盖文件，再逐项核对扩展所依赖的路由、配置解析、HTTP 客户端和生成绑定。Go 导出名与 gomobile 生成的 Java 方法名不能凭拼写推断，例如 `UIHTTPGet` 当前由 Kotlin 调用为 `Libbox.uihttpGet`。任何扩展增删都要核对共享准备 Action 的安装范围及三条工作流的调用。

以上是源码和工作流的覆盖范围，不是本次测试通过记录；实际结果须对应提交 SHA 和 Actions 运行。本机构建限制见 [AGENTS.md](AGENTS.md)。
