# Test fonts

`NotoSansSC-status.ttf` is a 14 KiB Noto Sans SC v41 subset obtained from the
[Google Fonts CSS API](https://developers.google.com/fonts/docs/css2) on 2026-10-28
with its `text` parameter. It provides these Chinese glyphs for both renderers:

状态运行中未挂起出站检测失败点击图标重试时长组织质量类型知住宅移动网络商业机房优普通风险启动暂停止详情

SHA256: `8d4c75f24149089e28fcbcd20dc79e586d765b5cba2b8a17c245d93b2cef9d22`.
The [SIL Open Font License](OFL.txt) covers this test resource. The font is
registered as `BboxStatusCJK` by the parity tests, alongside the SDK's Roboto.

`NotoSansSC-updates.ttf` is a Noto Sans SC v41 regular (weight 400) subset
obtained on 2026-11-19 using this exact [Google Fonts request](https://fonts.googleapis.com/css?family=Noto+Sans+SC&text=+%21%22%23%24%25%26%27%28%29%2A%2B%2C-.%2F0123456789%3A%3B%3C%3D%3E%3F%40ABCDEFGHIJKLMNOPQRSTUVWXYZ%5B%5C%5D%5E_%60abcdefghijklmnopqrstuvwxyz%7B%7C%7D~%E3%80%82%E4%B8%80%E4%B8%8B%E4%B8%8D%E4%B8%8E%E4%B8%AD%E4%BA%8E%E4%BB%96%E4%BB%A3%E4%BB%A5%E4%BC%9A%E4%BD%9C%E4%BF%9D%E4%BF%A1%E5%80%BC%E5%85%81%E5%85%88%E5%85%B3%E5%85%B6%E5%86%85%E5%86%8D%E5%88%86%E5%88%9B%E5%89%8D%E5%8C%85%E5%8C%96%E5%8F%91%E5%8F%96%E5%8F%98%E5%8F%AF%E5%90%8D%E5%90%8E%E5%99%A8%E5%9B%9E%E5%9C%A8%E5%A4%9A%E5%A4%A7%E5%A4%B1%E5%AD%98%E5%AE%89%E5%AE%8C%E5%AE%A2%E5%B0%8F%E5%B0%9A%E5%B1%9E%E5%B7%B2%E5%B8%83%E5%B8%B8%E5%B9%B6%E5%BA%94%E5%BB%BA%E5%BC%80%E5%BC%83%E5%BC%B9%E5%BD%93%E5%BD%95%E5%BE%80%E5%BE%85%E6%81%AF%E6%88%90%E6%88%B7%E6%89%93%E6%8C%81%E6%93%8D%E6%94%AF%E6%94%BE%E6%96%B0%E6%97%A0%E6%97%B6%E6%98%8E%E6%98%AF%E6%9A%82%E6%9B%B4%E6%9C%80%E6%9C%89%E6%9C%AA%E6%9C%AC%E6%9F%A5%E6%A0%A1%E6%A0%B8%E6%A3%80%E6%AD%A3%E6%AD%A4%E6%B3%95%E6%B6%88%E6%BA%90%E7%89%88%E7%94%A8%E7%95%8C%E7%9A%84%E7%9B%AE%E7%9C%8B%E7%A0%81%E7%A8%8D%E7%AA%97%E7%AB%AF%E7%AD%BE%E7%BB%A7%E7%BB%AD%E7%BC%93%E7%BD%AE%E8%87%B4%E8%A1%8C%E8%A3%85%E8%A7%84%E8%AE%B8%E8%AE%BE%E8%AF%81%E8%AF%95%E8%AF%B4%E8%AF%B7%E8%AF%BB%E8%B4%A5%E8%B6%85%E8%BD%BD%E8%BF%87%E8%BF%90%E8%BF%94%E8%BF%99%E8%BF%9B%E9%83%A8%E9%87%87%E9%87%8C%E9%87%8D%E9%97%AD%E9%9D%A2%E9%A1%B5%E9%AA%8C%E9%AB%98%EF%BC%8C).
The returned TrueType font maps 225 Unicode characters and is 48,748 bytes.

SHA256: `03f81880f4f8f414d4bdd998296f587cce769bfae0ebba574c1eb6bcfbc69946`.
The same [SIL Open Font License](OFL.txt) covers this resource.
[about_test.dart](../../about_test.dart) registers this Noto Sans SC subset under
the `Ubuntu` alias used by the Linux theme's first fallback, alongside the SDK's
Roboto. This resolves all requested Chinese glyphs before Ahem's sparse Han
coverage in screenshot cases; the original font file is unchanged.
Separate Android-platform narrow-phone cases retain the original interaction
and layout assertions. These fonts are test resources and are not app assets.

`NotoSansSC-scripts.ttf` is a Noto Sans SC v41 regular (weight 400) subset
obtained on 2026-11-22 using this exact [Google Fonts request](https://fonts.googleapis.com/css?family=Noto+Sans+SC&text=%20%21%22%23%24%25%26%27%28%29%2A%2B%2C-.%2F0123456789%3A%3B%3C%3D%3E%3F%40ABCDEFGHIJKLMNOPQRSTUVWXYZ%5B%5C%5D%5E_%60abcdefghijklmnopqrstuvwxyz%7B%7C%7D~%E3%80%81%E3%80%82%E4%B8%8D%E4%B8%AA%E4%B8%AD%E4%B8%BA%E4%B9%A6%E4%BA%8B%E4%BA%8E%E4%BA%94%E4%BB%85%E4%BB%A5%E4%BB%B6%E4%BB%BB%E4%BB%BD%E4%BC%9A%E4%BD%95%E4%BD%BF%E4%BF%9D%E5%85%A5%E5%85%AD%E5%85%B3%E5%86%85%E5%86%99%E5%87%BA%E5%88%99%E5%88%B6%E5%89%8D%E5%8A%9F%E5%8A%A0%E5%8A%A1%E5%8A%A8%E5%8C%85%E5%8C%96%E5%8C%B9%E5%8C%BA%E5%8D%95%E5%8D%B3%E5%8F%91%E5%8F%96%E5%8F%98%E5%8F%A3%E5%8F%AF%E5%90%8D%E5%90%8E%E5%90%AB%E5%90%AF%E5%91%8A%E5%91%A8%E5%92%8C%E5%99%A8%E5%9B%9E%E5%9C%A8%E5%9C%B0%E5%9F%9F%E5%A1%AB%E5%A4%84%E5%A4%87%E5%A4%8D%E5%A4%B4%E5%AD%97%E5%AD%98%E5%AE%9A%E5%AE%B9%E5%AF%86%E5%AF%BC%E5%B0%86%E5%B7%B2%E5%B9%B6%E5%BA%8F%E5%BA%94%E5%BC%80%E5%BC%8F%E5%BC%95%E5%BD%93%E5%BE%88%E5%BF%97%E6%88%90%E6%88%96%E6%89%80%E6%89%8B%E6%89%93%E6%8A%A5%E6%8C%81%E6%8C%87%E6%8C%89%E6%8E%92%E6%8E%A5%E6%93%8E%E6%94%AF%E6%94%B9%E6%94%BE%E6%96%87%E6%97%A0%E6%97%A5%E6%97%B6%E6%9C%89%E6%9C%8D%E6%9C%9F%E6%9C%AA%E6%9C%AC%E6%9D%A1%E6%A0%A1%E6%AE%B5%E6%B1%82%E6%B6%88%E6%B7%BB%E6%BA%90%E7%8B%AC%E7%90%86%E7%94%A8%E7%95%99%E7%9A%84%E7%9B%AE%E7%A4%BA%E7%A7%92%E7%A7%B0%E7%A9%BA%E7%AB%8B%E7%AB%AF%E7%AC%A6%E7%BB%9C%E7%BC%96%E7%BD%91%E7%BD%AE%E8%84%9A%E8%8F%9C%E8%A1%8C%E8%A1%A5%E8%A1%A8%E8%A7%84%E8%A7%A3%E8%A7%A6%E8%AE%BE%E8%AF%81%E8%AF%A5%E8%AF%B7%E8%B5%84%E8%B7%91%E8%B7%B3%E8%BD%BD%E8%BE%91%E8%BE%93%E8%BE%BE%E8%BF%87%E8%BF%90%E8%BF%94%E8%BF%9E%E9%80%89%E9%80%9A%E9%85%8D%E9%87%8D%E9%94%99%E9%95%BF%E9%97%AD%E9%99%A4%E9%9A%8F%E9%9C%80%E9%9D%A2%E9%A1%B9%E9%A1%BA%E9%A1%BB%E9%A6%96%E9%AA%8C%EF%BC%88%EF%BC%89%EF%BC%8C%EF%BC%9B).
The request contains 263 characters; the returned TrueType font is 58,712 bytes.

SHA256: `32f9f4684762a61f1185a6fdc0324b6abaa2f4a4a17caeaae908f53af744bd07`.
The same [SIL Open Font License](OFL.txt) covers this resource.
[script_configuration_test.dart](../../script_configuration_test.dart) registers
the subset under the Linux theme's `Ubuntu` fallback for readable script output
and event editor screenshots, alongside the SDK's Roboto and the application's
JetBrains Mono. Separate Android cases retain interaction and layout coverage.
The subset is only a test resource and is not bundled as an application asset.
