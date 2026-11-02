# Android Root TLS 采集器

本目录实现 B-box 的被动 TLS 明文采集。支持范围、解析方式和资源限制由 [libbox.md](../../libbox.md#httphttps-请求调试) 维护，Android 会话与安装包发现由 [app.md](../../app.md#请求抓取与-rootebpf) 维护。

## 参考与来源

主要参考 [SeeFlowerX/stackplz](https://github.com/SeeFlowerX/stackplz/tree/62fc9e82f025b73232f92be7faafc9fd671bb510)，固定比较提交为 `62fc9e82f025b73232f92be7faafc9fd671bb510`（Apache-2.0）：

- [config_global.go](https://github.com/SeeFlowerX/stackplz/blob/62fc9e82f025b73232f92be7faafc9fd671bb510/user/config/config_global.go) 的 `Parse_Libinfo` / `FindLibInApk` 区分解析 ELF 的路径、实际挂载文件 `RealFilePath` 和 APK 条目数据偏移 `NonElfOffset`。
- [stack.go](https://github.com/SeeFlowerX/stackplz/blob/62fc9e82f025b73232f92be7faafc9fd671bb510/user/module/stack.go) 将这些信息交给 probe manager，使用 `UAddress` / `UprobeOffset` 定位探针。
- [filtering.h](https://github.com/SeeFlowerX/stackplz/blob/62fc9e82f025b73232f92be7faafc9fd671bb510/src/common/filtering.h) 在 BPF 中按 UID/PID/TID 筛选。B-box 的筛选范围由当前 Android 用户和抓包应用选择决定。

stackplz 提供符号或调用者指定偏移的通用追踪入口。B-box 在自己的 [libraries.go](libraries.go)、[targets.go](targets.go)、[attach.go](attach.go) 和 [BPF 程序](bpf/capture.c) 中实现自动 TLS 定位、原始 inode 挂载及明文流生命周期；不打包 stackplz 可执行文件、堆栈解码库或调试服务。

[SeeFlowerX/ebpf](https://github.com/SeeFlowerX/ebpf/tree/61cbc4a37e8264c3788494a74dcec992c636b918) 是实际使用的 Go 加载依赖，固定在 `61cbc4a37e8264c3788494a74dcec992c636b918`，通过 [go.mod](go.mod) 的 `replace` 保留 `github.com/cilium/ebpf` 导入路径。该 fork 的 [link/kprobe.go](https://github.com/SeeFlowerX/ebpf/blob/61cbc4a37e8264c3788494a74dcec992c636b918/link/kprobe.go) 和 [link/perf_event.go](https://github.com/SeeFlowerX/ebpf/blob/61cbc4a37e8264c3788494a74dcec992c636b918/link/perf_event.go) 提供 PMU、`perf_event_open` 及 ioctl 挂载依据。B-box 直接按文件偏移调用同一内核接口，允许原始 APK 作为挂载文件。依赖为 MIT，版权与许可保存在 [cilium-ebpf-LICENSE](../../flutter/assets/licenses/cilium-ebpf-LICENSE)，应用许可页标注 fork 名称。

stackplz 的模块文件使用相邻目录替换依赖，未固定该 fork 的 SHA；这里使用 [ShinoLeah/eDBG](https://github.com/ShinoLeah/eDBG/tree/00063c967dec79748c8fe9397290b9c481b06963) 在 `00063c967dec79748c8fe9397290b9c481b06963` 固定的 ebpf 子模块修订。eDBG 为 MIT，作为后续应用上述 Android 机制的辅助参考。B-box 没有引入其断点、信号、内存修改或 MCP 能力。

无符号 ARM64 BoringSSL 的静态识别是 B-box 的实现，源码依据及测试片段许可见 [Chromium 测试说明](testdata/chromium/README.md)。它根据当前 ELF 内容重新计算偏移，没有版本地址表；缺少足够证据的库保持未支持状态。

尾跳转包装的参数与返回地址约束依据 [AAPCS64 的子程序调用及尾调用定义](https://github.com/ARM-software/abi-aa/blob/2025Q1/aapcs64/aapcs64.rst#subroutine-calls)。只有不修改参数、栈和 LR 的 NOP/BTI 与直接 B 转发可跨过缺少独立调用入口的包装；最终释放函数仍须通过分配/释放配对检查。

该 fork 的 [perf/reader.go](https://github.com/SeeFlowerX/ebpf/blob/61cbc4a37e8264c3788494a74dcec992c636b918/perf/reader.go) 为堆栈/寄存器解析保留完整采样正文，`Record.RawSample` 包含开头的四字节 `PERF_SAMPLE_RAW` 长度。B-box 仅启用 RAW 采样，由 [events.go](events.go) 检查并移除这层封装，再处理自己的事件及内核对齐填充。升级依赖时必须核对该格式，不能把 fork 与原版 cilium 的同名 API 视为行为相同。

TLS 生命周期还依据 [OpenSSL 3.0 的 state_machine](https://github.com/openssl/openssl/blob/openssl-3.0.13/ssl/statem/statem.c)：首次 `SSL_read`/`SSL_write` 可能在内部调用 `SSL_clear` 初始化握手。采集器区分尚无明文字节的同线程初始化与已有流的重置，避免把正常首个 I/O 当作缺口；该行为由真实 OpenSSL 和 APK 测试库共同覆盖。

[eCapture](https://github.com/gojue/ecapture/tree/331d546405b79731e1ea6189fe17e479f3bdb2d3) 固定参考提交为 `331d546405b79731e1ea6189fe17e479f3bdb2d3`（Apache-2.0）：[Go TLS 模块](https://github.com/gojue/ecapture/blob/331d546405b79731e1ea6189fe17e479f3bdb2d3/internal/probe/gotls/gotls_probe.go)、[Go 内核探针](https://github.com/gojue/ecapture/blob/331d546405b79731e1ea6189fe17e479f3bdb2d3/kern/gotls_kern.c) 和 [连接缓存](https://github.com/gojue/ecapture/blob/331d546405b79731e1ea6189fe17e479f3bdb2d3/internal/probe/openssl/connection_cache.go) 提供函数定位、寄存器 ABI 和连接代次的参考。B-box 自行实现对应适配，未引入其可执行文件或 TLS 私有结构版本表。

Go 实现依据 [Go 1.27.3 ABIInternal](https://github.com/golang/go/blob/go1.27.3/src/cmd/compile/abi-internal.md) 和 [runtime.g/stack](https://github.com/golang/go/blob/go1.27.3/src/runtime/runtime2.go)，所用字段及寄存器与 Go 1.26.10 对照核对：以 g 而非系统线程配对 I/O，处理栈搬移，以已观察的客户端握手建立连接代次。[Go 1.22.6 的 ABI](https://github.com/golang/go/blob/go1.22.6/src/cmd/compile/abi-internal.md)、[栈头](https://github.com/golang/go/blob/go1.22.6/src/runtime/runtime2.go) 及 [Go 1.23.0 ABI](https://github.com/golang/go/blob/go1.23.0/src/cmd/compile/abi-internal.md) 保持所用寄存器和参数布局。按照 [gosym.NewLineTable](https://github.com/golang/go/blob/go1.27.3/src/debug/gosym/pclntab.go) 的要求解析实际 `runtime.text` 地址及 ELF 重定位；Go 1.22–1.25 的 pcHeader 保存基址，Go 1.26/1.27 的 [pcHeader/moduledata](https://github.com/golang/go/blob/go1.27.3/src/runtime/symtab.go) 改从 moduledata 读取并核对关联表和代码边界。外部链接的 C 启动代码不计入 Go 函数偏移。x86 返回指令和缺少 unwind 的 ARM64 候选分别使用 `golang.org/x/arch` 的 `x86asm` / `arm64asm` v0.32.0 解码，依赖固定在 [go.mod](go.mod)，BSD 许可保存在 [golang-x-arch-LICENSE](../../flutter/assets/licenses/golang-x-arch-LICENSE)。Go 版本/编译器门限和功能边界由 [libbox.md](../../libbox.md#httphttps-请求调试) 维护。

可选 socket 探针用设备 BTF 的 CO-RE 重定位读取实际 FD/file/socket，不使用固定内核字段偏移；库加载与字段重定位依据上述 ebpf fork。遇到不明确的 socket 归属清除该关联并报告，不把 FD 数字当作单条 HTTP 请求的可靠标识。

## 检查入口

`capture --inspect-library <ELF 或 APK 路径>` 使用生产识别器输出 `path`、`entry`、`sha256`、`method`、`archiveOffset` 和 `symbols`。其中 `sha256` 只计算 ELF 字节，`symbols` 是 ELF 函数文件偏移，实际挂载偏移为 `archiveOffset + symbols[函数名]`。自动识别失败的条目也输出哈希及 `error`。此命令只读安装文件，不加载 BPF；静态识别成功不代表设备允许挂载或目标应用实际调用了这些接口。

[UI 验证工作流](../../.github/workflows/newui.yml) 在采集器测试通过后上传 `capture-inspector-arm64` / `capture-inspector-amd64`，其中的 `capture-inspect` 是同一源码的无 libc 依赖 Linux 可执行文件，可用上述参数只读检查对应架构的安装库。产物与运行的源码 SHA 对应，不需要在 Termux 编译；Android 应用仍使用 NDK 构建的采集器。

## 手动函数偏移

抓包设置的“手动 TLS 函数偏移”可添加、编辑或删除库配置，保存抓包设置后生效。填写绝对文件路径、APK 内完整条目（独立 ELF 留空）、ELF 的 SHA-256，以及完整的 `SSL_new/free/read/write` 函数组；可选项为当前探针支持的 `SSL_clear`、`SSL_read_ex/write_ex`、`SSL_read_early_data/write_early_data`、`SSL_connect/do_handshake`。格式和挂载校验的支持边界由 [核心说明](../../libbox.md#httphttps-请求调试) 维护。

可先用上述只读检查命令获取每个 ELF 的哈希。反汇编工具显示虚拟地址时，需要按对应 `PT_LOAD` 换算为 ELF 文件偏移：`p_offset + 函数虚拟地址 - p_vaddr`。填写十六进制 `0x...`，不要加入进程加载基址或 APK 容器偏移。

`capture --inspect-library <路径> --offsets <JSON 文件>` 可在挂载前预览同一配置。JSON 文件为数组，示例中的哈希和偏移须替换为自行确认过公开 SSL ABI 的目标函数：

```json
[
  {
    "path": "/data/app/example/base.apk",
    "entry": "lib/arm64-v8a/libtls.so",
    "sha256": "<替换为 ELF SHA-256 的 64 个十六进制字符>",
    "functions": {
      "SSL_new": "0x1000",
      "SSL_free": "0x1100",
      "SSL_read": "0x1200",
      "SSL_write": "0x1300"
    }
  }
]
```

手动配置优先于指定路径/条目的自动识别；哈希不符时报告实际哈希并拒绝该配置，不回退使用同一 inode 的自动结果。文件和可执行区间检查不证明用户提供的函数调用约定正确。Go TLS 仍须经过版本、参数和返回点识别，不能用公开 SSL 偏移覆盖 Go ABI。

## CI 验证

[newui.yml](../../.github/workflows/newui.yml) 在 Linux ARM64/x86_64 编译并测试采集器，运行真实 OpenSSL TLS 和 [Go TLS 明文 smoke](gotls_smoke.py)，在 x86_64 另运行 [APK 映射执行 smoke](apk_smoke.py)。APK 测试独立加载未压缩 APK 中的 [公开 SSL ABI 测试程序](testdata/tls_fixture.c)，分别只传入 APK 路径和传入独立换算的手动偏移/哈希，验证自动与手动模式均挂载到原始 inode、跨 CPU 事件、UID 筛选、嵌套调用去重和异常生命周期。该程序不实现加密，真实 TLS 由 [smoke.py](smoke.py) 及六个 Go 工具链构建的 [Go fixture](testdata/go_tls_client.go) 覆盖。Go 1.22.6 另构建带 [C 导出入口](testdata/go_tls_shared.go) 的 stripped c-shared 库，检查 JNI 类库布局的静态函数解析；此项不执行 JNI 或 Android 抓取。

Go smoke 为两种 TLS 版本保存临时测试会话的明文事件、socket 关联、诊断和开抓前后同协议的延迟/CPU/RSS 数据。采集字节与本次 fixture 的实际请求和响应逐条核对；产物名和覆盖范围见 [核心验证说明](../../libbox.md#构建同步和验证)。这些小样本用于回归诊断，不能据此宣称目标应用的性能收益。

Linux smoke 通过 `sudo` 启动，以当前未使用的独立 UID 执行测试负载，采集器保留 Root。测试继续使用生产 UID 筛选和进程映射发现，核对事件 UID；APK 用例另验证不选中的 UID 没有事件。临时文件放在调用者拥有的 `~/tmp`，测试进程保留调用者的组以穿过目录，临时服务端私钥仅对该测试 UID 可读。这样宿主机的 Docker/containerd 等后台进程不会因同为 Root 被计入测试库和退出耗时。owner EOF 后仍要求五秒内退出，超时保留内核线程及 Go 协程栈。

Android 采集器和平台测试由共享构建 Action 生成，并在 [Android smoke](../../.github/workflows/newui-android.yml) 验证。构建与动态测试遵守仓库 [AGENTS.md](../../AGENTS.md)，本机默认只做静态检查。依赖下载、编译成功、Linux smoke 和无 Root 模拟器均不能替代 Root Android 目标应用验收。
