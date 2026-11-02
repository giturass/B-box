# ARM64 Chromium instruction fixtures

`instructions.json` contains 5,584 instruction bytes from nine functions in
Chrome 156.0.8078.75 for Android ARM64 (`lib/arm64-v8a/libchrome.so`). It contains
no APK, application data, network traffic or complete TLS implementation. The
library SHA-256 and each original function's address/hash document extraction
provenance; production discovery does not read this fixture or these identities.

Sources corresponding to this binary:

- Chromium commit [`861389936bf33ea77529ee5dcc0605e68f6bb620`](https://chromium.googlesource.com/chromium/src/+/861389936bf33ea77529ee5dcc0605e68f6bb620/), including
  [`net/socket/ssl_client_socket_impl.cc`](https://chromium.googlesource.com/chromium/src/+/861389936bf33ea77529ee5dcc0605e68f6bb620/net/socket/ssl_client_socket_impl.cc).
  The `Chrome_*` samples are `SSLClientSocketImpl::Init`, `DoPayloadRead` and
  `DoPayloadWrite`. Copyright The Chromium Authors; BSD terms are preserved in
  [LICENSE.chromium](LICENSE.chromium).
- Its DEPS pins BoringSSL commit [`2ea3200c3a3ec176613ab7a0b9a4ffc6db846120`](https://boringssl.googlesource.com/boringssl/+/2ea3200c3a3ec176613ab7a0b9a4ffc6db846120/).
  The `SSL_*` and `ssl_read_impl` samples come from
  [`ssl/ssl_lib.cc`](https://boringssl.googlesource.com/boringssl/+/2ea3200c3a3ec176613ab7a0b9a4ffc6db846120/ssl/ssl_lib.cc).
  Copyright 1995–2016 The OpenSSL Project Authors; Copyright 2002 Oracle and/or
  its affiliates; Copyright 2005 Nokia. All rights reserved. Apache 2.0 terms
  and the upstream license notices are preserved in
  [LICENSE.boringssl](LICENSE.boringssl).

The functions were identified against the source, ARM64 disassembly, source-file
references, `.eh_frame_hdr`/FDE boundaries and calls from Chromium's socket
implementation. The fixed addresses are evidence for these samples, not a
runtime offset table.

[chromium_test.go](../../chromium_test.go) constructs inert ELF files without SSL
symbols or build IDs, shuffles functions and gaps, relocates all direct calls and
source references, changes error source line constants, and checks the resulting
file offsets. Missing external callees are synthetic `RET` stubs; irrelevant
literal pages are empty. No sample code is executed. Mutated negative fixtures
must fail recognition rather than produce guessed probe addresses. APK tests
also distinguish ELF virtual addresses, ELF file offsets and ZIP data offsets.

These tests exercise the production static recognizer on actual compiler output.
They do not establish compatibility with every future compiler/BoringSSL change,
or verify Root, SELinux, uprobes, Chrome reconnection or HTTP/3 on a device.

## Cronet with sparse unwind information

`cronet-150.json` contains 5,520 instruction bytes from the same nine function
roles in Android ARM64 `libcronet.150.0.7871.28.so`, SHA-256
`38c66f809e3e2acde4fffc20479cf31bc0a93eaccb40171cc0de900c2c86f93a`.
The installed Google speech service's ARM64 split supplies this public library;
no APK, application data or runtime traffic is included. Its `.eh_frame_hdr`
contains 575 entries, none covering these nine functions. Direct call sites,
disassembly and source relationships identify the extracted ranges; trailing
BRK/HLT crash sequences are retained.

The matching Chromium tag resolves to
[`48db307645dcbaa0bb5ccee0cd096cf22971bb84`](https://chromium.googlesource.com/chromium/src/+/48db307645dcbaa0bb5ccee0cd096cf22971bb84/).
Its [compiler configuration](https://chromium.googlesource.com/chromium/src/+/48db307645dcbaa0bb5ccee0cd096cf22971bb84/build/config/compiler/BUILD.gn#495)
allows C/C++ unwind tables to be omitted. Its DEPS pins BoringSSL
[`3a9254f16eda7a4c5d2260039ff23456a0a34de4`](https://boringssl.googlesource.com/boringssl/+/3a9254f16eda7a4c5d2260039ff23456a0a34de4/).
The [SSL source](https://boringssl.googlesource.com/boringssl/+/3a9254f16eda7a4c5d2260039ff23456a0a34de4/ssl/ssl_lib.cc#913)
defines the copy and consumption relationships even when clang inlines
`SSL_peek` into `SSL_read`. Both pinned repositories' licenses are byte-for-byte
identical to [LICENSE.chromium](LICENSE.chromium) and
[LICENSE.boringssl](LICENSE.boringssl) already preserved here.

[chromium_cronet_test.go](../../chromium_cronet_test.go) reconstructs complete
and sparse unwind variants, relocates the functions, calls and source references,
and changes line constants. Synthetic caller instructions stand in for callers
outside the extracted functions. Inert ELF import metadata and PLT stubs model
`memcpy`, `abort` and `__stack_chk_fail`; the imported implementations are not
included. A `.plt` view spans those stubs in the shuffled executable segment.
Unknown helpers remain RET stubs. Corrupted FDEs, unavailable call entries,
unbounded control flow, incorrect imports, wrong copy bounds/buffers and
non-consuming reads must all fail recognition. These fixtures are static input,
never executable tests or production address tables.

## Older Cronet allocation and span helpers

Two additional Android ARM64 samples cover the reported Google and WeChat
libraries. They contain only the bounded instruction ranges needed for static
recognition, with the same relocation and negative-mutation tests above:

| Fixture | Original library | Instruction bytes | Function ranges |
| --- | --- | --- | --- |
| [cronet-134.json](cronet-134.json) | `libcronet.134.0.6986.0.so` | 6,820 | 9 |
| [cronet-119.json](cronet-119.json) | `libcronet.119.0.6045.214.so` | 4,916 | 13 |

Cronet 134's ELF SHA-256 is
`aad6facaedcfadf0115f5dd5b881d2e444d8b555198da30f4a3cb3a19a63f9ee`.
The matching Chromium tag points to
[`b327e905c0cbe6201e6cf669a22b4bc082fda761`](https://chromium.googlesource.com/chromium/src/+/b327e905c0cbe6201e6cf669a22b4bc082fda761/),
whose DEPS pins BoringSSL
[`817ab07ebb53da35afea409ab9328f578492832d`](https://boringssl.googlesource.com/boringssl/+/817ab07ebb53da35afea409ab9328f578492832d/ssl/ssl_lib.cc).
Its socket read computes the remaining length and buffer address next to the
SSL member load; arithmetic must invalidate the destination register while
preserving unrelated aliases.

Cronet 119's ELF SHA-256 is
`380142b446c0cd703462788ebd5b97a0dee110b0e93156dfefac683aba328b47`.
The Chromium tag points to
[`42d9ae0937c2bdac793feeb5922a0b0cb25ebae2`](https://chromium.googlesource.com/chromium/src/+/42d9ae0937c2bdac793feeb5922a0b0cb25ebae2/),
whose DEPS pins BoringSSL
[`d24a38200fef19150eef00cad35b138936c08767`](https://boringssl.googlesource.com/boringssl/+/d24a38200fef19150eef00cad35b138936c08767/ssl/ssl_lib.cc).
Besides the nine original roles, this fixture retains `MakeUnique`,
`unique_ptr::reset`, the transparent `SSL_free` wrapper and `Span::subspan`.
The recognizer verifies context references, allocation/constructor arguments,
replacement and release of the same socket member, span bounds and consumption,
and unchanged signed read counts. It does not accept fixed private field offsets.

Both Chromium licenses match [LICENSE.chromium](LICENSE.chromium). These older
BoringSSL commits predate its Apache relicensing; their OpenSSL/SSLeay, ISC and
other upstream notices are preserved separately in
[LICENSE.boringssl-119](LICENSE.boringssl-119) and
[LICENSE.boringssl-134](LICENSE.boringssl-134), with trailing spaces normalized.
This product includes software developed by the OpenSSL Project for use in the
OpenSSL Toolkit (http://www.openssl.org/), cryptographic software written by
Eric Young (eay@cryptsoft.com), and software written by Tim Hudson
(tjh@cryptsoft.com). The original SSL source also credits Oracle and Nokia.

The outlined `unique_ptr::reset` instantiation follows Chromium 119's libc++ pin
[`7cf98622abaf832e2d4784889ebc69d5b6fde4d8`](https://chromium.googlesource.com/external/github.com/llvm/llvm-project/libcxx/+/7cf98622abaf832e2d4784889ebc69d5b6fde4d8/include/__memory/unique_ptr.h).
Its Apache 2.0 with LLVM exceptions and prior notices are retained in
[LICENSE.libcxx-119](LICENSE.libcxx-119). Test fixtures are not bundled in the
Android application or read by production discovery.
