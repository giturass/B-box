# Flutter locates the generated plugin entry point by its class name.
-keep class io.flutter.plugins.GeneratedPluginRegistrant { *; }

# LSPosed loads the entry points from META-INF/xposed and hooks these callbacks.
-keep class io.nekohasekai.sfa.xposed.** { *; }
-keep class io.github.libxposed.service.** { *; }
-keep class io.nekohasekai.sfa.bg.LogEntry { *; }
-keep class io.nekohasekai.sfa.bg.PackageEntry { *; }
