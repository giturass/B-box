# MyBox

基于 [SagerNet/sing-box-for-android](https://github.com/SagerNet/sing-box-for-android) 的非官方 Android 客户端分支，主要调整仪表盘交互和布局。此项目独立维护，不代表上游官方发布。

- 仪表盘底部改为连接、代理组、启动/停止按钮。
- 配置卡片操作移至标题栏，远程配置更新按钮移至信息行。
- 移除独立连接统计卡片，保留活动连接列表入口。
- 启动按钮采用 FlClash 风格的播放/暂停形变与展开计时，保留现有配色；连接和代理组入口自适应窄屏。
- Clash 模式仅将模式选择按钮改为 1.12.23 时期的独立矩形样式，保留现有卡片和标题；系统 HTTP 代理保持原样。

维护与构建入口：**[MODIFICATIONS.md](MODIFICATIONS.md)**。该文档记录准确的上游基线、逐项差异、文件定位、AI 维护约束与回归检查。

应用显示名称为 `MyBox`，Android 包名为 `io.nekohasekai.sfa`。

启动按钮布局与交互参考 [FlClash](https://github.com/chen08209/FlClash)，Compose 适配及具体差异见维护清单 MOD-007。

## GitHub Actions 手动构建

工作流 [Build Release APK (arm64-v8a)](.github/workflows/build-release.yml) 仅支持手动触发，构建 Android 7.0+ 的 `otherRelease` APK，仅包含 `arm64-v8a`，不生成 universal APK。

将工作流提交到 GitHub 默认分支后，打开 **Actions → Build Release APK (arm64-v8a) → Run workflow**，选择分支并运行。完成后在该次运行页面的 **Artifacts** 下载 `MyBox-<版本>-arm64-v8a-release`，解压即可获得 APK；产物保留 30 天。

CI 根据 `version.properties` 的 `VERSION_NAME` 检出上游核心对应的 `v<版本>` 标签，使用 `GO_VERSION` 和上游构建脚本编译 ARM64 `libbox.aar`，然后执行 `:app:assembleOtherRelease -Parm64Only=true`，保留 R8 和 release Lint，并校验 APK 与架构。更新版本时需确认对应核心标签已经发布且接口兼容。工作流不自动创建 GitHub Release。

## Documentation

https://sing-box.sagernet.org/installation/clients/sfa/

## License

```
Copyright (C) 2022 by nekohasekai <contact-sagernet@sekai.icu>

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.

In addition, no derivative work may use the name or imply association
with this application without prior consent.
```

Under the license, that forks of the app are not allowed to be listed on F-Droid or other app stores
under the original name.
