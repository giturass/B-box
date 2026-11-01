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

该 fork 的 [perf/reader.go](https://github.com/SeeFlowerX/ebpf/blob/61cbc4a37e8264c3788494a74dcec992c636b918/perf/reader.go) 为堆栈/寄存器解析保留完整采样正文，`Record.RawSample` 包含开头的四字节 `PERF_SAMPLE_RAW` 长度。B-box 仅启用 RAW 采样，由 [events.go](events.go) 检查并移除这层封装，再处理自己的事件及内核对齐填充。升级依赖时必须核对该格式，不能把 fork 与原版 cilium 的同名 API 视为行为相同。

TLS 生命周期还依据 [OpenSSL 3.0 的 state_machine](https://github.com/openssl/openssl/blob/openssl-3.0.13/ssl/statem/statem.c)：首次 `SSL_read`/`SSL_write` 可能在内部调用 `SSL_clear` 初始化握手。采集器区分尚无明文字节的同线程初始化与已有流的重置，避免把正常首个 I/O 当作缺口；该行为由真实 OpenSSL 和 APK 测试库共同覆盖。

## 检查入口

`capture --inspect-library <ELF 或 APK 路径>` 使用生产识别器输出 `entry`、`method`、`archiveOffset` 和 `symbols`。其中 `symbols` 是 ELF 函数文件偏移，实际挂载偏移为 `archiveOffset + symbols[函数名]`。此命令只读安装文件，不加载 BPF；静态识别成功不代表设备允许挂载或目标应用实际调用了这些接口。

[newui.yml](../../.github/workflows/newui.yml) 编译并测试 Linux 采集器，运行真实 OpenSSL TLS 和 [APK 映射执行 smoke](apk_smoke.py)。后者独立加载未压缩 APK 中的 [公开 SSL ABI 测试程序](testdata/tls_fixture.c)，仅把 APK 路径交给采集器，验证挂载到原始 inode、跨 CPU 事件、UID 筛选、嵌套调用去重和异常生命周期。测试程序不实现加密，真实 TLS 由 [smoke.py](smoke.py) 覆盖。

Android 采集器和平台测试由共享构建 Action 生成，并在 [Android smoke](../../.github/workflows/newui-android.yml) 验证。构建与动态测试遵守仓库 [AGENTS.md](../../AGENTS.md)，本机默认只做静态检查。依赖下载、编译成功、Linux smoke 和无 Root 模拟器均不能替代 Root Android 目标应用验收。
