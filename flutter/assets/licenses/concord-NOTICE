# Third-party notices

Concord is licensed under GPL-3.0-or-later. This file records the code, data and designs it takes from other projects,
and the licence terms that come with them. Licence texts were read from the upstream repositories or from the crates
and Go modules named below; where a text could not be read, the entry says so.

## Derived from other projects

Concord is a new implementation. Where a file transcribes a table, adapts an algorithm, or follows another project's
behaviour closely enough for its licence to matter, the project is listed here with the Concord files concerned.

| Source | Licence | Where it is used |
|---|---|---|
| mihomo (MetaCubeX), and the fork of it in FlClash's `core/Clash.Meta` | GPL-3.0 | The behavioural reference for rules, DNS, groups, MITM and the SSR, Snell, Hysteria and ShadowQUIC wire formats. Concord is checked against it; nothing is linked. |
| FlClash's Go core (`core/` in the FlClash repository) | GPL-3.0-or-later | The method set, event semantics and response handling behind `crates/proto`, `crates/api` and `crates/engine`. |
| uTLS (`u_parrots.go`, via the MetaCubeX fork v1.8.8) | BSD-3-Clause, Copyright 2017 Google Inc.; repository licence Copyright (c) 2009 The Go Authors | ClientHello fingerprint tables transcribed into `crates/tls/src/fingerprints.rs`. |
| golang.org/x/net `http2/hpack` | BSD-3-Clause, Copyright 2009 The Go Authors | Huffman and static tables in `crates/tunnel/src/sniff/hpack_tables.rs` and `crates/transport/src/xhttp/padding.rs`. |
| golang.org/x/net `publicsuffix` v0.35.0 | BSD-3-Clause, Copyright 2009 The Go Authors | The table layout, its generator and the lookup in `xtask/src/public_suffix.rs` and `crates/group/src/public_suffix.rs`. |
| Public Suffix List (publicsuffix.org), commit `3955e3ec29b94c3cca7bd4509c5f14a7c0959e26` | MPL-2.0 | `crates/group/data/public-suffix/public_suffix_list.dat`, unmodified, and the table generated from it in the same directory, which is compiled into the core. Source for the MPL-covered work is this repository. |
| go-wildcard (IGLOU-EU), commit ce22b7af, as modified in mihomo `component/wildcard` | BSD-3-Clause, Copyright (c) 2023 Iglou.eu and Adrien Kara | The wildcard matcher in `crates/rules/src/text.rs` and `crates/dns/src/matchers.rs`. |
| v2fly/v2ray-core QUIC sniffing, via mihomo | MIT, Copyright (c) 2015-2025 V2Fly Community | `crates/tunnel/src/sniff/quic*.rs`. |
| Xray-core XTLS Vision, via mihomo | MPL-2.0 | The Vision framing in `crates/proxy/src/vless/stream.rs`. Source for the MPL-covered work is this repository. |
| meow-rs | MIT, Copyright (c) 2026 Max Lv | Leaf code and design, ported across crates. |
| shoes (cfal) | MIT, Copyright (c) 2021-2023 Alex Lau | Protocol code, ported across `crates/proxy` and `crates/transport`. |
| sing-tun (SagerNet / MetaCubeX fork v0.4.28) | GPL-3.0-or-later | Policy-routing plan in `crates/tun/src/route/linux.rs` and forwarding rules in `crates/tun/src/stack/driver.rs` and `crates/netstack/src/wire.rs`. |
| sing, sing-vmess, sing-shadowsocks2, sing-quic, sing-mux (MetaCubeX forks) | GPL-3.0-or-later | Behavioural reference for VMess, Shadowsocks 2022, QUIC-based protocols and sing-mux. The Hysteria2 realm client in `crates/proxy/src/hysteria2/realm` follows sing-quic `hysteria2/realm` (control API calls, STUN discovery, the punch packet format and the port-prediction rule) and is checked against mihomo's `hysteria2-realm` listener. |
| shadowsocks-rust | MIT, Copyright (c) 2017 Y. T. Chung | Shadowsocks stream and AEAD behaviour in `crates/proxy`. |
| smux (xtaci, MetaCubeX fork) | MIT, Copyright (c) 2016-2017 xtaci | `crates/proxy/src/smux.rs`. |
| Hysteria v1.3.5 (apernet) | MIT, Copyright (c) 2021 Toby | The wire format in `crates/proxy/src/hysteria`. The repository licence adds that executables built with `-tags gpl` are distributed under GPLv3; Concord uses no code built that way. |
| amneziawg-go (Amnezia VPN, as vendored by mihomo) | MIT, Copyright (C) 2017-2025 WireGuard LLC | The AmneziaWG wire format in `crates/proxy/src/wireguard/amnezia`: junk packets, message type headers, size padding, the I and J packet tag languages, header protection and the MAC rules. Checked against the library itself acting as the server. |
| mieru (enfein) v3.38.1, as vendored by mihomo | GPL-3.0-or-later, Copyright (C) 2023 mieru authors | The wire format in `crates/proxy/src/mieru`: key derivation, segment and metadata layout, session handling, traffic patterns and the low entropy codec follow `pkg/cipher`, `pkg/protocol` and `apis/`. The UDP transport follows `pkg/protocol/underlay_packet.go` and `session.go` (acknowledgements, retransmission, receive window) and `pkg/congestion`, whose RTT estimator is MIT-licensed code from quic-go (Copyright (c) 2016 the quic-go authors & Google, Inc.). Checked against mihomo's mieru listener, which runs the same library. |
| clash-rs (Watfaq) | Apache-2.0 | Named in the design notes as the source for Hysteria2 congestion control. Not verified file by file for this notice. |
| jls-tls (MetaCubeX) | BSD-3-Clause | Compared against for the ShadowQUIC JLS handshake; no code taken. |

Checked while writing this file:

- **sing-\*.** The LICENSE of sing, sing-tun, sing-vmess, sing-shadowsocks2, sing-quic and sing-mux is the plain GPL-3.0-or-later
  notice, byte-identical between the MetaCubeX forks (as cached by the Go module proxy) and the SagerNet `main` or `dev` branch.
  It carries no additional terms. The name clause ("no derivative work may use the name or imply association with this
  application without prior consent") is in sing-box's LICENSE only; Concord uses no sing-box code.
- **go-wildcard.** mihomo's header calls the licence "OpenBSD's ISC-style". The upstream LICENSE at commit ce22b7af
  contains the three-clause BSD text, not ISC, so it is reproduced as BSD-3-Clause below.
- **gotatun.** The crate carries no LICENSE file. The upstream repository has `LICENSE` (MPL-2.0) and `LICENSE-CLOUDFLARE`
  (BSD-3-Clause, Copyright (c) 2019 Cloudflare, Inc.), and the crate's source headers carry both notices.
- **russh.** The crate carries no LICENSE file; upstream `LICENSE.txt` is the unmodified Apache-2.0 text with no
  copyright line of its own.
- **Hysteria v1.3.5.** The Go module cache copy has `LICENSE.md`, which is MIT as above.
- **Not checked.** The clash-rs and jls-tls rows rest on the design notes.

## Dependencies

Rust dependencies are used under their own licences, as declared in each crate. `deny.toml` allows only these:
0BSD, Apache-2.0 (also with LLVM-exception), BSD-2-Clause, BSD-3-Clause, BSL-1.0, CC0-1.0, GPL-3.0-or-later, ISC, MIT,
MPL-2.0, Unicode-3.0, Unlicense and Zlib. `cargo deny list` prints the licence of every crate in the build.

Dependencies that need a note:

- **gotatun 0.10.2** (WireGuard): MPL-2.0, Copyright (c) Mullvad VPN AB, with files derived from Cloudflare's BoringTun under
  the BSD-3-Clause terms below. Concord uses the unmodified crate from crates.io; its source is at
  https://github.com/mullvad/gotatun.
- **russh 0.65** (SSH outbound): Apache-2.0.
- **rquickjs 0.14** (MITM scripts): MIT, Copyright (c) 2020 Mees Delzenne. It embeds QuickJS, which is MIT.
- **boring / boring-sys 5.4** (BoringSSL for client fingerprints): `boring` is Apache-2.0 and `boring-sys` is MIT. The
  BoringSSL sources `boring-sys` builds were not audited for this file. `aws-lc-rs` and `aws-lc-sys` declare ISC, MIT, MIT-0,
  BSD-3-Clause and Apache-2.0 terms; the `fips` feature stays off because `aws-lc-fips-sys` declares the OpenSSL licence.
- **wintun.dll 0.14.1** (`assets/wintun/`, compiled into the Windows core): the unmodified amd64 and arm64 DLLs from
  the official ZIP at https://www.wintun.net/builds/wintun-0.14.1.zip (SHA-256
  `07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51`), under WireGuard LLC's Prebuilt Binaries
  License in `assets/wintun/LICENSE.txt`. It allows redistribution alongside software that uses the DLL only through
  the `wintun.h` API, which is all tun-rs does; the licence text has to ship with the core.
- **`assets/ca-certificates.crt`** is embedded into the core as the CA bundle. The file in this repository is empty;
  CI fills it before building (Concord's `Check` job, and FlClash's build jobs for the copy they check out). If it is
  filled from Mozilla's root store, that data is MPL-2.0.

## Vendored crates

These crates.io releases are copied into `vendor/` with changes and replace the registry copies through
`[patch.crates-io]` in `Cargo.toml`. Each directory keeps the crate's own licence files, each source change carries a
`Concord:` comment, and `vendor/PATCHES.md` describes every change.

| Crate | Version | Licence |
|---|---|---|
| boring | 5.4.0 | Apache-2.0 |
| h2 | 0.4.20 | MIT |
| mio | 1.2.4 | MIT |
| quinn | 0.11.12 | MIT OR Apache-2.0 |
| quinn-proto | 0.11.19 | MIT OR Apache-2.0 |
| rustls | 0.23.46 | Apache-2.0 OR ISC OR MIT |
| shadowsocks | 1.25.0 | MIT |
| tun-rs | 2.8.16 | Apache-2.0 |

## Licence texts

### BSD-3-Clause (Go Authors, as used by uTLS and x/net)

Copyright 2009 The Go Authors.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following disclaimer
in the documentation and/or other materials provided with the
distribution.
   * Neither the name of Google LLC nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

### BSD-3-Clause (Cloudflare, Inc., in gotatun; and Iglou.eu and Adrien Kara, in go-wildcard)

Copyright (c) 2019 Cloudflare, Inc. All rights reserved.

Copyright (c) 2023 Iglou.eu, Copyright (c) 2023 Adrien Kara

Redistribution and use in source and binary forms, with or without modification, are permitted provided that the following conditions are met:

1. Redistributions of source code must retain the above copyright notice, this list of conditions and the following disclaimer.
2. Redistributions in binary form must reproduce the above copyright notice, this list of conditions and the following disclaimer in the documentation and/or other materials provided with the distribution.
3. Neither the name of the copyright holder nor the names of its contributors may be used to endorse or promote products derived from this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

### MIT

Applies to v2fly/v2ray-core (Copyright (c) 2015-2025 V2Fly Community), meow-rs (Copyright (c) 2026 Max Lv), shoes
(Copyright (c) 2021-2023 Alex Lau), shadowsocks-rust (Copyright (c) 2017 Y. T. Chung), smux (Copyright (c) 2016-2017
xtaci), Hysteria (Copyright (c) 2021 Toby), rquickjs (Copyright (c) 2020 Mees Delzenne) and
amneziawg-go (Copyright (C) 2017-2025 WireGuard LLC).

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

### MPL-2.0

Applies to gotatun, the Xray-core Vision framing and the Public Suffix List. The full text is at
https://www.mozilla.org/MPL/2.0/ and in the `LICENSE` file of https://github.com/mullvad/gotatun.

### GPL-3.0-or-later

Applies to Concord itself and to the sing-tun-derived code. The text is in [LICENSE](LICENSE).
