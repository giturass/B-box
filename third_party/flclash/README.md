# FlClash presentation components

Source: [chen08209/FlClash v0.9.2](https://github.com/chen08209/FlClash/tree/7fd417fb9a9a277c6a9339a8ac87f907df7c25f7).

The shared Flutter UI components (`flutter/lib/ui`), their two utility files,
fonts and glass shader are imported from this fixed revision. Package imports
are renamed to `b_box_ui`; the original license is in [LICENSE](LICENSE).
Adapted dashboard controls are listed under `adaptedFiles`. Font licensing is
in `JetBrainsMono-OFL.txt` and `Twemoji-LICENSE.md`; the unmodified Twemoji
art is by Twitter and the font conversion by Mozilla. These notices also ship
in the app license page.

The status-card IP race, quality source parsers, wave schedule, quality rules
and cache lifetime also adapt this revision. Flutter query adaptations and
their reference fixtures are listed under `adaptedFiles`; the routed Go
transport is listed under `adaptedNativeFiles`. HTTP calls use libbox and the
active sing-box router instead of Mihomo/Dio.

`manifest.json` records the imported file hashes after that mechanical rename.

The B-box application screens and Android bridge adapt this presentation to
sing-box. No FlClash/Mihomo native library, Clash configuration model, scripts,
subscription converter, or Clash-specific service is included.
