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
