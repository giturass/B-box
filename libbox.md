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

HTTPS 默认关闭；开启后通过 [专用采集器](tools/ebpf/) 的 eBPF uprobes 观察目标进程在 `SSL_read`/`SSL_write`、`_ex` 公共接口及受支持的 Go TLS 客户端中读写的明文。可另行开启原始网络包和 Go TLS 密钥保存；密钥来自应用已有的握手计算，不从网络密文推导，不接管握手或修改目标内存，应用照常验证服务端证书。CA 生成、证书安装/模块、MITM、HTTP 改写和 JavaScript 已移除。

采集器参考 stackplz 的 Android 文件定位、APK 偏移和 UID 筛选方式，以及 eCapture 的 Go TLS、连接关联和独立解密思路，继续使用固定的 SeeFlowerX/ebpf Android fork；比较提交、依赖来源和许可见 [采集器说明](tools/ebpf/README.md#参考与来源)。依据 [Linux uprobes](https://www.kernel.org/doc/html/latest/trace/uprobetracer.html) 和经核对的函数 ABI 工作，不读取 BoringSSL 私有结构偏移。支持 ARM64/x86_64 的 64 位库和 Go 可执行文件，需要 Root、Linux 5.10 或更新内核、BPF/uprobe/perf 支持及 SELinux 许可。自动检查系统 Conscrypt/libssl、所选已安装应用的原生库目录、base/split/shared APK 和当前 Android 用户的可执行映射，可补充最多 16 个绝对库、APK 或 Go 可执行文件路径。没有兼容库或全部挂载失败时启动报错；挂载库数不表示所有目标应用均受支持。32 位进程、下述范围之外的 Go TLS、不能通过识别的无符号自有/裁剪 TLS 库和 QUIC/HTTP/3 不在当前支持范围。

[Go TLS 识别器](tools/ebpf/gotls.go) 接受 Go 1.24–1.26 正式 patch 版本的 gc 编译结果，拒绝开发版、其他编译器和自定义 `GOEXPERIMENT`。从 build info 校验版本，以标准库 `debug/gosym` 解析 pclntab，支持普通和 stripped PIE ELF；Go 1.24/1.25 从 pclntab 读取 `runtime.text`，Go 1.26 按其 [moduledata 布局](tools/ebpf/gotls_module.go) 定位并交叉校验函数表、切片和代码范围，均解析 ELF REL/RELA 重定位，不把包含 C 启动代码的 `.text` 节起点当作 Go 函数基址。每次从实际指令定位 `Read`、`Write`、`clientHandshake` 的 RET，以普通 uprobe 捕获返回，不使用会干扰 Go 栈的内核 uretprobe。读写按 PID、goroutine 指针和方向配对，核对栈帧并在 Go 栈搬移后调整栈内缓冲地址；新代次只由客户端握手建立，兼容首个 I/O 内的隐式握手，失败握手和 `Close` 释放代次。服务端、缺失函数表或函数、无法确认 ABI/返回边界的程序不挂载；同一 ELF 同时包含公开 SSL 接口时保留两套适配。实际版本/架构验证入口见下文，不把版本门限当作全部 Android 应用已实测的声明。

[自动定位](tools/ebpf/libraries.go) 优先使用 ELF 动态/本地符号及有界 XZ mini-debugdata，核对当前 ABI、可执行节和加载段中的函数文件偏移。ARM64 库缺少 SSL 符号、但保留 BoringSSL/Chromium socket 源文件标记时，使用[静态识别器](tools/ebpf/chromium.go)检查当前内容；识别不依赖库名，因而也适用于保留相同结构的改名 Cronet 库。依据公共错误码、标准 unwind 函数范围及调用关系识别 `SSL_new/free/read/write`；必须确认分配/释放配对、socket 中同一 SSL 对象的调用、整数长度 ABI，以及 `SSL_read` 对 `SSL_peek` 结果的消耗和原值返回。Chrome 版本、build ID、代码哈希、固定地址、源码行号、寄存器和私有结构字段的绝对偏移均不作为版本签名。识别有歧义、符号冲突、缺少生命周期或不支持的编译布局时不挂载猜测地址，其他兼容库继续采集。普通更新移动代码/行号不需要维护地址表，修改函数实现、编译布局或删除识别信息仍可能需要更新识别器。

[ELF 检查](tools/ebpf/chromium_elf.go)核对加载段、节标志和 `.eh_frame_hdr`/FDE/CIE 范围。依据 [Chromium Android 的 `--no-rosegment` 链接配置](https://chromium.googlesource.com/chromium/src/+/861389936bf33ea77529ee5dcc0605e68f6bb620/build/config/compiler/BUILD.gn#1161)，允许 `.rodata` 和 unwind 数据与 `.text` 共用可读/可执行的 `PT_LOAD`；每个节仍须完整位于加载段的文件范围内，并保持虚拟地址与文件偏移对应，`.text` 同时要求节和加载段带执行标志。目前接受 Android clang 常用的 `pcrel/sdata4`、`datarel/sdata4` 表及 `zR` CIE。只缓存有界候选函数，分块扫描 `.text` 并保留取消检查；APK 探针地址为 ZIP 数据偏移加 ELF 文件偏移，不使用运行时虚拟地址。安装后的新 inode 会重新检查，旧结果不会按包名或版本号套用。

[挂载](tools/ebpf/attach.go) 在解析期间保持原始文件描述符，通过 `/proc/self/fd` 将同一 inode 交给 uprobe PMU，地址为 APK 条目数据偏移加 ELF 函数文件偏移。只挂载 APK 内未压缩的对应 ABI `.so`；压缩条目的提取副本由原生库目录和进程映射发现。映射路径在目标进程的 mount namespace 中解析，设备号/inode 不符或文件已删除时使用 `map_files`，避免包更新后把新文件当作旧映射。

采集器的 `--inspect-library <ELF 或 APK 路径>` 只读诊断入口使用同一识别器，输出条目、识别方式、APK 数据偏移及实际识别出的函数文件偏移；它不需要 Root、不加载 BPF，也不读取目标进程或网络数据。可用 CI 构建的对应 ABI 采集器检查安装文件，结果仅证明静态识别，不能当作实机抓包通过。

[库发现](tools/ebpf/discovery.go) 优先检查显式库路径和系统 TLS 库，首个库的完整探针挂载成功后发送 ready，其余安装文件、进程映射及调试符号检查在独立工作协程继续，新增挂载通过 status 更新数量。`scanning` 表示仍在准备其他库，界面在准备完成后提示目标应用重新连接。最多保留三条有界库识别/挂载诊断，经 ready/status 传递，不将诊断当成全局丢数据或清零挂载数。事件读取和完整性检查不等待整个扫描结束，ready 始终先于采集数据。启动进度覆盖 BPF 加载、perf 缓冲及库检查/挂载，Android 的超时与协议处理见 [app.md](app.md#请求抓取与-rootebpf)。owner EOF 取消库扫描及后续文件读取，先释放探针并等待扫描退出，再关闭共享 BPF 程序和 map；扫描最多保留 4096 条候选路径、2048 个文件身份及 64 个挂载库。

[诊断日志](libbox/httpdebug/logs.go) 独立于上述三条状态摘要，按时间记录启动环境及阶段、库路径/条目、识别方式、文件身份、APK 数据偏移、实际挂载函数的文件偏移、后续库检查及全部识别/挂载失败。连接日志包含代次 ID、PID/UID、应用、读写字节数、丢失/解析错误和停止原因，不逐明文分片写日志。采集器就绪后用 `log` 事件发送后续诊断，libbox 与 Android 桥接记录启动失败及服务停止；页面不可见仍持续记录。内存环形缓冲最多 1000 条、512 KiB 消息，每条最多 4 KiB，截断保留有效 UTF-8；停抓和重新开始保留历史，应用进程退出后清除。`ReadUICaptureLogs`、`AppendUICaptureLog` 和 `ClearUICaptureLogs` 提供独立的读取、应用侧补记及清日志绑定，清日志不清请求。界面入口及查询方式见 [flclash.md](flclash.md#导航与交互约定)。

状态分别统计已观测 TLS/Go TLS 连接、已关联 socket、收到明文的连接/字节、解析出的 HTTPS 请求、筛选排除数及保存数；保存的网络包/密钥数来自持久存储。停止后保留本次统计，重新开始重置会话计数。首次明文、首次保存请求及 socket 冲突单独记日志，避免把“探针挂载成功”当作“已收到并解析目标请求”。日志不写明文正文或密钥内容。

启动参数的路径校验区分补充 TLS 库与自动安装目标，错误包含对应列表的索引和有界、转义后的路径；[配置回归](tools/ebpf/config_test.go) 检查来源及错误消息边界。Android 自动候选过滤见 [应用侧发现规则](app.md#请求抓取与-rootebpf)，手动填写的库路径继续严格校验。

采集器在确认 Root 后直接尝试提高自身的 `RLIMIT_MEMLOCK`，支持仍受该限额约束的内核；提高失败后由实际 BPF 加载报告分配错误，使用 memcg 计费的内核不依赖提高此限额。不导入会在 Go 初始化阶段探测 BPF map 的 rlimit 包，避免 Android 应用 seccomp 在权限错误返回前以 `SIGSYS` 终止进程。

加载依赖由 [go.mod](tools/ebpf/go.mod) / [go.sum](tools/ebpf/go.sum) 固定，Linux 与 Android 由 [build.sh](tools/ebpf/build.sh) 直接构建同一份模块源码。探针采用 `perf_event_open` 和 `PERF_EVENT_IOC_SET_BPF`，无需 BPF token、attach cookie 或 Linux 5.15 的 BPF link。内核/权限错误保留在启动诊断中；[平台测试](tools/ebpf/platformtest/platform_test.go) 检查 Android 能进入实际 Linux 系统调用路径。

每个库先挂载 I/O 和释放探针，最后启用 `SSL_new`，部分失败按相反顺序释放全部句柄。仅接受观察到分配后的新连接；空闲时 `SSL_clear` 在入口结束旧代次，仅返回成功时创建新代次，`SSL_free` 结束当前代次。OpenSSL 在首个 I/O 内部初始化握手而调用 `SSL_clear` 时，若尚无明文字节且只有当前线程占用一个方向，保留初始代次；已有数据或其他线程调用期间的重置则标为不完整，并等待后续空闲 clear/new。普通与 `_ex`/early-data 嵌套调用按方向去重，`SSL_read_early_data` 的 FINISH 返回也读取实际字节数。跳过返回或线程退出的未完成调用会使旧代次不完整，并释放占用的跟踪槽。开启后需让目标应用重新连接。UID 在 BPF 中筛选，排除 B-box 自身及其他 Android 用户；共享 UID 使用同一选择，包名集合保留在记录中。HTTPS 可以观察选中应用的本机连接，不依赖其是否通过 sing-box；本地服务的启停仍决定抓包会话的生命周期。

域名和端口按每条 HTTP 请求判断，支持 HTTP/2 合并连接上的不同域名。HTTPS 域名列表为空表示不限制受支持的域名；精确名称匹配本域，`*.example.com` 匹配子域，排除优先。通用包含/排除筛选同时约束 HTTP 和 HTTPS 请求记录，HTTPS 排除仅影响对应 HTTP 记录。默认端口 80、443、8080、8443，最多 32 个。HTTPS 端口始终使用 HTTP authority（默认 443），实际 socket 端口独立展示，不随关联事件的到达时机改变筛选结果。

[socket 关联](tools/ebpf/bpf/sockets.h) 在 TLS I/O/握手上下文中的读写 syscall 入口读取当前 FD，通过设备 BTF 的 CO-RE 字段重定位核对实际 file/socket、inode、网络命名空间和 IPv4/IPv6 地址，再绑定到 TLS 代次。没有按 FD 数字缓存猜测对象，也不使用固定内核偏移。同一代次出现不同 socket 时清除关联；冲突先到时，跨 CPU 迟到的旧元数据也不能恢复该关联。迟到地址可补到已完成的 HTTP 记录。BTF/挂载不可用时记录原因、地址保持未知，普通 TLS 明文继续采集；sing-box 出站和路由规则仍未知，不能由地址推断。

[明文解析器](libbox/httpdebug/plaintext.go) 处理 HTTP/1.1 分片、顺序请求、信息响应、定长/chunked/流式正文；[HTTP/2 解析器](libbox/httpdebug/plaintext_h2.go) 要求完整客户端前言，以独立 HPACK 状态和 stream ID 还原并发报文。`RST_STREAM` 只结束对应请求，保留已保存正文及取消原因，其他流继续；取消后到达的 HEADERS 仍经共享 HPACK 解码，服务器取消先到时等待请求头。无法确认正常结束的 close-delimited 响应保留不完整标志。升级仅记录 HTTP 握手，HTTP trailers 不加入头部预览。HTTP/2 server push、过大帧/头或不能确定边界的协议终止该连接的录制并报告原因，网络连接本身继续运行。

资源与完整性边界：

- 最多记录 300 条、磁盘 32 MiB；每方向正文默认保留 64 KiB，最高 1 MiB，头部上限 64 KiB。普通 HTTP 最多接管 32 条连接、每条 HTTP/2 最多 16 个流；eBPF 最多同时解析 32 条连接，每条 HTTP/2 最多 32 个流。
- 每方向明文队列最多 256 KiB；单次 SSL 调用最多 128 KiB，以 4096 字节分片输出。超长调用、不可读内存、同方向重叠或生命周期缺失产生该连接代次的独立缺口事件，丢弃其未完成分片并停止该连接的录制，其他连接继续。事件按连接代次、每方向序号与偏移重组，总重组缓存最多 4 MiB；重复、缺失、perf 丢失和 BPF 输出失败均报告全局完整性错误。短期保留已关闭代次，忽略跨 CPU 迟到的缺口事件，避免重建已经结束的流。
- 局部溢出、解析错误和两分钟无数据的连接终止录制；全局丢事件或采集器退出停止抓包。未结束记录保留不完整标志及错误，服务重载/停止关闭管道、采集器和处理器。抓包不会自动恢复。

预览/HAR 的 gzip/deflate 解码上限 64 KiB；不支持的编码或二进制保留 Base64，HAR 另保留原始压缩前缀和截断/完整性标记。耗时仅为总时长，不冒充 DNS/TCP/TLS 分阶段计时。cURL 要求请求头与正文完整且未截断，正文缺失则拒绝导出。停止服务后仍可查询保存记录。

原始数据保存由独立且默认关闭的 `captureNetwork` / `captureTlsKeys` 控制，仅在 HTTPS 采集期间生效：

- [网络包采集](tools/ebpf/packets.go) 使用 AF_PACKET 保存与已确认目标 socket 同网络命名空间的原始 IPv4/IPv6 TCP 包，snaplen 为 32768，保留内核时间戳和原始长度。关联前缓存最多三秒、4 MiB、4096 包，不输出未关联流量，不合成 TCP/TLS 包；只接受本次 TLS 代次开始后的包，可能缺少建连 SYN。IP 分片不重组，其他命名空间不进入；共享/复用 TCP tuple 或出现新 SYN 时停止该 tuple 的包关联，直到重新开抓，避免把没有 inode 的旧包配给新代次。最多记住 1024 对端点，缓存、内核/队列丢包和关联冲突均有独立诊断，不把包丢失伪装成完整抓取。
- [Go TLS 密钥探针](tools/ebpf/bpf/gotls.h) 在 `crypto/tls.(*Config).writeKeyLog` 入口读取已生成的 NSS label、client random 和 secret，`KeyLogWriter` 无需开启。支持 TLS 1.2 master secret 和 TLS 1.3 traffic secret；当前不提取 OpenSSL/BoringSSL 私有结构中的密钥。只有密钥和原始握手/网络包均被保留时，独立解密才具备所需数据。
- [原始数据存储和导出](libbox/httpdebug/wire.go) 将包/密钥保存为应用私有的 0600 日志，独立限制 32 MiB、128 条 TLS 连接、每连接 32 条去重密钥。恢复中断写入时只保留有效前缀，淘汰/恢复/丢包警告随存储保留；无法执行淘汰时停止写入并报告失败。清请求同时清包和密钥，清日志不影响它们。这些数据不进入配置或备份。
- `ExportUICaptureWire` 输出标准 pcapng（RAW IP、纳秒时间戳、TLS Decryption Secrets Block）或独立 NSS key-log 文本。每条 TLS 连接使用独立 pcapng section，密钥块置于包之前，支持单遍读取。非空搜索按匹配请求选择整条 TLS 连接，可能包含同连接上的其他 HTTP/2/顺序请求；空搜索也导出未成功解析 HTTP 的原始流。原始采集只按应用选择，域名/端口筛选约束 HTTP 记录，不承诺从加密包中隔离单个请求。

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

[httpdebug_test.go](libbox/httpdebug/httpdebug_test.go) 保留 HTTP 路由、SSE/WebSocket、存储上限和导出回归；[plaintext_test.go](libbox/httpdebug/plaintext_test.go) 覆盖分片、HTTP/2 并发/跨域筛选、有界缺口说明、半截正文、准备状态/库诊断、采集器退出与源关闭，以及诊断事件不干扰抓取、超过三条的失败历史、停抓保留和清日志不清请求。[logs_test.go](libbox/httpdebug/logs_test.go) 检查日志条数/字节限额、UTF-8 截断、独立快照和并发记录/读取/清空。[ui_capture_test.go](libbox/ui_capture_test.go) 检查本地设置、退役字段清理、schema 及未开抓包时的诊断绑定；[路由测试](libbox/route/httpdebug_test.go) 检查原出站和字节回放。获准本机测试时可使用 [verify_httpdebug.py](tools/verify_httpdebug.py)，它不构建 Android 绑定。

UI 验证在 Linux ARM64/x86_64 编译 BPF/采集器，运行 [事件测试](tools/ebpf/events_test.go)、[库发现测试](tools/ebpf/discovery_test.go)、[偏移解析测试](tools/ebpf/libraries_test.go)、[Go ABI/返回指令测试](tools/ebpf/gotls_test.go)、[socket/包关联测试](tools/ebpf/packets_test.go)、[挂载回滚测试](tools/ebpf/attach_test.go)、[映射 inode 测试](tools/ebpf/targets_test.go) 和 [平台回归](tools/ebpf/platformtest/platform_test.go)。[真实 OpenSSL smoke](tools/ebpf/smoke.py) 检查两方向 TLS 明文；x86_64 的 [APK smoke](tools/ebpf/apk_smoke.py) 执行直接映射在未压缩 APK 中的独立测试库，检查自动偏移、原始 inode、跨 CPU、UID 过滤、嵌套/early-data 去重、SSL_clear、超长/重叠/跳过返回/进程退出的局部缺口及 owner EOF 清理。

[Go TLS smoke](tools/ebpf/gotls_smoke.py) 分别用 Go 1.24.0、1.25.0、1.26.10 的 internal/external 链接构建 stripped PIE 客户端，在两种架构上验证显式/隐式握手、跨 goroutine I/O、socket、TLS 1.2/1.3 密钥和原始网络包。`KeyLogWriter` 保持 nil，由 tshark 独立解密；同次运行的 `gotls-wire-validation-amd64` / `gotls-wire-validation-arm64` 产物继续交给核心的 [wire_test.go](libbox/httpdebug/wire_test.go)，通过正式日志/导出器再验证 24 组 pcapng。该测试还覆盖私有权限、截断恢复、搜索选连接、限额和清理；[HTTP/2 取消测试](libbox/httpdebug/plaintext_h2_test.go) 检查两方向取消、部分正文、共享 HPACK 及连续取消后的其他流。smoke 按相同 TLS 版本记录开抓前后样本延迟、采集器 CPU/RSS 和丢失信息，样本少且来自 Linux fixture，不表示 Android 性能收益或实机应用验收。

平台测试检查 map/程序能进入内核调用，允许真实权限或内核限制，不将其通过视为抓取可用。共享 Android 构建 Action 使用 NDK 编译 PIE 采集器，链接 16 KiB page alignment，打包为 `libbbox_capture.so`；x86_64 构建额外生成 Android 平台回归二进制，由 Android smoke 在 API 24/31/35 的无特权 shell 中执行。生成的 BPF 对象和可执行文件不入库。Linux smoke 和无特权平台测试不代替 Android 实机内核、SELinux、Conscrypt 和目标应用验收。

[Chromium 识别回归](tools/ebpf/chromium_test.go)纳入同一 Go 测试入口，以[带来源和许可的实际 ARM64 指令片段](tools/ebpf/testdata/chromium/README.md)构造无符号 ELF，改变函数排列、虚拟地址和错误行号，覆盖只读数据与代码分段及共用可读/可执行段的布局，并检查节标志/加载映射不符、APK 文件偏移、取消、歧义/损坏 unwind、错误参数、非消耗读取和缺少释放配对。库发现及明文管道测试另检查兼容结果与未支持提示并存、ready 在前、挂载数保留及其他应用继续采集；这些静态指令/协议回归不能代替 Chrome 实机抓取结果。

升级核心时先检查补丁是否仍准确排除 legacy 分支，核对 sing-tun 依赖版本及自动重定向补丁，检查 HTTP 调试路由/服务生命周期补丁及覆盖文件，再逐项核对扩展所依赖的路由、配置解析、HTTP 客户端和生成绑定。Go 导出名与 gomobile 生成的 Java 方法名不能凭拼写推断，例如 `UIHTTPGet` 当前由 Kotlin 调用为 `Libbox.uihttpGet`。任何扩展增删都要核对共享准备 Action 的安装范围及三条工作流的调用。

以上是源码和工作流的覆盖范围，不是本次测试通过记录；实际结果须对应提交 SHA 和 Actions 运行。本机构建限制见 [AGENTS.md](AGENTS.md)。
