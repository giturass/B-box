import 'dart:async';
import 'package:flutter/foundation.dart' show LicenseRegistry, LicenseEntryWithLineBreaks, defaultTargetPlatform;
import 'package:flutter/services.dart';
import 'widgets.dart';
import 'pages/dashboard.dart';
import 'pages/proxies.dart';
import 'pages/profiles.dart';
import 'pages/tools.dart';
import 'pages/taildrop.dart';
import 'theme_settings.dart';
import 'components/page_switcher.dart';
import 'components/navigation_bar.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  SystemChrome.setEnabledSystemUIMode(SystemUiMode.edgeToEdge);
  LicenseRegistry.addLicense(() async* {
    for (final entry in const {'FlClash': 'LICENSE', 'prism_ui': 'LICENSE', 'prism_ui third-party notices': 'prism_ui-NOTICE', 'prism_ui shapes / material_new_shapes': 'prism_shapes-LICENSE', 'prism_ui lists / super_sliver_list': 'prism_sliver_list-LICENSE', 'prism_ui sheets / navigator_resizable': 'prism_navigator-LICENSE', 'Goja': 'goja-LICENSE', 'gojq': 'gojq-LICENSE', 'timefmt-go': 'timefmt-go-LICENSE', 'robfig/cron': 'cron-LICENSE', 'regexp2': 'regexp2-LICENSE', 'go-sourcemap': 'sourcemap-LICENSE', 'Google pprof': 'pprof-LICENSE', 'Brotli': 'brotli-LICENSE', 'klauspost/compress': 'klauspost-compress-LICENSE', 'JetBrains Mono': 'JetBrainsMono-OFL.txt', 'Twemoji / Mozilla': 'Twemoji-LICENSE.md'}.entries) {
      yield LicenseEntryWithLineBreaks([entry.key], await rootBundle.loadString('assets/licenses/${entry.value}'));
    }
  });
  runApp(BoxApp(controller: BoxController()));
}

class BoxApp extends StatefulWidget {
  const BoxApp({super.key, required this.controller});
  final BoxController controller;
  @override State<BoxApp> createState() => _BoxAppState();
}

class _BoxAppState extends State<BoxApp> with WidgetsBindingObserver {
  final _navigator = GlobalKey<NavigatorState>();
  StreamSubscription<String>? _messages;
  @override void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _messages = widget.controller.messages.stream.listen((message) {
      final context = _navigator.currentContext;
      if (context != null) BannerHost.maybeOf(context)?.show(message);
    });
    widget.controller.initialize();
  }
  @override void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _messages?.cancel();
    widget.controller.dispose();
    super.dispose();
  }

  final _themes = <String, ThemeData>{};

  @override void didChangeLocales(List<Locale>? locales) => setState(_themes.clear);

  ThemeData _theme(Brightness brightness) {
    final scheme = appColorScheme(widget.controller, brightness);
    final key = "$brightness:${scheme.hashCode}";
    return _themes.putIfAbsent(key, () => ThemeData(
      colorScheme: scheme,
      fontFamilyFallback: appFontFamilyFallback(platform: defaultTargetPlatform, locales: WidgetsBinding.instance.platformDispatcher.locales),
      elevatedColorScheme: brightness == Brightness.dark ? scheme.elevated : null,
    ));
  }

  @override
  Widget build(BuildContext context) => BoxScope(
    controller: widget.controller,
    child: Builder(
      builder: (context) {
        final prefs = BoxScope.of(context, aspect: BoxAspect.appearance).preferences;
        return PrismApp(
          title: 'B-box',
          debugShowCheckedModeBanner: false,
          navigatorKey: _navigator,
          theme: _theme(Brightness.light),
          darkTheme: _theme(Brightness.dark),
          themeMode: ThemeMode.values.firstWhere((mode) => mode.name == prefs['theme'], orElse: () => ThemeMode.system),
          supportedLocales: const [Locale('en'), Locale('zh')],
          localizationsDelegates: const [
            BoxUiDelegate(), GlobalWidgetsLocalizations.delegate,
          ],
          minTextScale: .8,
          maxTextScale: 1.4,
          bannerTopInset: bannerBarClearance,
          liquidGlass: switch (prefs['liquidGlass']) {
            'ultra' => LiquidGlassQuality.ultra,
            'standard' => LiquidGlassQuality.standard,
            _ => null,
          },
          emojiFont: (family: FontFamily.twEmoji.value, pattern: emojiPattern),
          builder: (context, child) {
            final media = MediaQuery.of(context);
            return ViewScope(
              size: media.size,
              isMobile: media.size.width < 600,
              child: AnnotatedRegion<SystemUiOverlayStyle>(
                  value: SystemUiOverlayStyle(
                    statusBarColor: Colors.transparent, systemNavigationBarColor: Colors.transparent,
                    statusBarIconBrightness: Theme.of(context).brightness == Brightness.dark ? Brightness.light : Brightness.dark,
                    systemNavigationBarIconBrightness: Theme.of(context).brightness == Brightness.dark ? Brightness.light : Brightness.dark,
                  ),
                  child: child!,
              ),
            );
          },
          home: widget.controller.ready ? const HomePage() : Builder(builder: (context) => Surface(
            color: Theme.of(context).scaffoldBackgroundColor,
            child: SafeArea(child: Center(child: widget.controller.startupError == null
                ? const ActivityIndicator()
                : ErrorState(widget.controller.startupError!, retry: widget.controller.initialize))),
          )),
        );
      },
    ),
  );
}

class HomePage extends StatefulWidget {
  const HomePage({super.key});
  @override State<HomePage> createState() => _HomePageState();
}

class _HomePageState extends State<HomePage> {
  int _index = 0;
  bool _sidebarExpanded = false;
  StreamSubscription<String>? _intents;
  BoxController? _box;
  bool _handlingIntent = false;
  static const _items = <(String, Glyph)>[
    ('Dashboard', AppGlyphs.dashboard), ('Proxies', AppGlyphs.proxies),
    ('Profiles', AppGlyphs.profiles), ('Tools', AppGlyphs.tools),
  ];
  static const _pages = [DashboardPage(), ProxiesPage(), ProfilesPage(), ToolsPage()];

  @override void didChangeDependencies() {
    super.didChangeDependencies();
    if (_box != null) return;
    _box = context.box;
    _intents = _box!.intents.stream.listen((_) => _drainIntents());
    WidgetsBinding.instance.addPostFrameCallback((_) => _drainIntents());
  }

  Future<void> _drainIntents() async {
    if (_handlingIntent || !mounted) return;
    _handlingIntent = true;
    try {
      while (mounted && _box!.pendingIntents.isNotEmpty) {
        await _import(_box!.pendingIntents.removeAt(0));
      }
    } finally { _handlingIntent = false; }
  }

  Future<void> _import(String uri) async {
    try {
      if (uri.startsWith('bbox-share:')) {
        if (mounted) await openPage(context, SharePage(token: uri.substring('bbox-share:'.length)));
        return;
      }
      final data = json(await _box!.call('profiles.decodeIntent', {'uri': uri}));
      if (mounted) await openForm(context, ProfileForm(initial: data));
    } catch (error) { _box!.report(error); }
  }

  @override void dispose() { _intents?.cancel(); super.dispose(); }

  @override Widget build(BuildContext context) {
    final mobile = context.isMobileView;
    final floating = context.box.preferences['floatingNavigation'] != false;
    final content = PageSwitcher(
      index: _index,
      animation: tabAnimationOf(context.box.preferences),
      children: [for (var i = 0; i < _pages.length; i++)
        PageActivityScope(isActive: i == _index, child: TickerMode(enabled: i == _index,
          child: ExcludeFocus(excluding: i != _index, child: _pages[i]))),
      ],
    );
    return Surface(
      color: Theme.of(context).scaffoldBackgroundColor,
      child: Column(children: [
        Expanded(child: Row(children: [
          if (!mobile) NavigationSidebar(
            selectedIndex: _index,
            expanded: _sidebarExpanded,
            onToggle: () => setState(() => _sidebarExpanded = !_sidebarExpanded),
            onSelected: (index) => setState(() => _index = index),
            destinations: [for (final item in _items) SidebarDestination(
              glyph: item.$2, label: context.tr(item.$1),
            )],
          ),
          Expanded(child: Stack(children: [
            Positioned.fill(child: BottomInsetScope(
              inset: mobile && floating ? NavigationDock.insetOf(context) : 0,
              child: MediaQuery.removePadding(context: context, removeBottom: mobile, removeLeft: mobile, removeRight: mobile, child: content),
            )),
            if (mobile && floating) PositionedDirectional(
              start: 0, end: 0, bottom: 0,
              child: NavigationDock(
                selectedIndex: _index,
                onSelected: (index) => setState(() => _index = index),
                destinations: [for (final item in _items) NavigationDockDestination(glyph: item.$2, label: context.tr(item.$1))],
              ),
            ),
          ])),
        ])),
        if (mobile && !floating) BoxNavigationBar(
          selectedIndex: _index,
          onDestinationSelected: (index) => setState(() => _index = index),
          destinations: [for (final (i, item) in _items.indexed)
            NavigationDestination(icon: AnimatedGlyph(glyph: item.$2, filled: i == _index), label: context.tr(item.$1)),
          ],
        ),
      ]),
    );
  }
}
