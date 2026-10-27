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

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  SystemChrome.setEnabledSystemUIMode(SystemUiMode.edgeToEdge);
  LicenseRegistry.addLicense(() async* {
    for (final entry in const {'FlClash': 'LICENSE', 'JetBrains Mono': 'JetBrainsMono-OFL.txt', 'Twemoji / Mozilla': 'Twemoji-LICENSE.md'}.entries) {
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

class _BoxAppState extends State<BoxApp> {
  final _messenger = GlobalKey<ScaffoldMessengerState>();
  StreamSubscription<String>? _messages;
  @override void initState() {
    super.initState();
    _messages = widget.controller.messages.stream.listen((message) {
      _messenger.currentState?.showSnackBar(SnackBar(content: Text(message)));
    });
    widget.controller.initialize();
  }
  @override void dispose() {
    _messages?.cancel();
    widget.controller.dispose();
    super.dispose();
  }

  final _themes = <String, ThemeData>{};

  ThemeData _theme(Brightness brightness) {
    final prefs = widget.controller.preferences;
    final scheme = appColorScheme(widget.controller, brightness);
    final family = prefs['fontFamily'] as String?;
    final key = "$brightness:${scheme.hashCode}:$family:${prefs['locale']}";
    return _themes.putIfAbsent(key, () => ThemeData(
      useMaterial3: true,
      colorScheme: scheme,
      fontFamily: family,
      fontFamilyFallback: appFontFamilyFallback(platform: defaultTargetPlatform, locales: WidgetsBinding.instance.platformDispatcher.locales, fontFamily: family),
      actionIconTheme: ActionIconThemeData(
        backButtonIconBuilder: (context) => GlyphIcon(AppGlyphs.backFor(Theme.of(context).platform)),
        closeButtonIconBuilder: (_) => const GlyphIcon(AppGlyphs.close),
      ),
      pageTransitionsTheme: const PageTransitionsTheme(builders: {
        TargetPlatform.android: commonSharedXPageTransitions,
      }),
      extensions: [if (brightness == Brightness.dark) ElevatedColorScheme(scheme.elevated)],
    ).withAppShapes.withAppSurfaces.withAppTypography);
  }

  @override
  Widget build(BuildContext context) => BoxScope(
    controller: widget.controller,
    child: ListenableBuilder(
      listenable: widget.controller,
      builder: (context, _) {
        final prefs = widget.controller.preferences;
        final locale = prefs['locale'] as String?;
        return MaterialApp(
          title: 'B-box',
          debugShowCheckedModeBanner: false,
          scaffoldMessengerKey: _messenger,
          theme: _theme(Brightness.light),
          darkTheme: _theme(Brightness.dark),
          themeMode: ThemeMode.values.firstWhere((mode) => mode.name == prefs['theme'], orElse: () => ThemeMode.system),
          locale: locale == null || locale == 'system' ? null : Locale(locale),
          supportedLocales: const [Locale('en'), Locale('zh')],
          localizationsDelegates: const [
            BoxUiDelegate(), ...GlobalMaterialLocalizations.delegates,
          ],
          builder: (context, child) {
            final media = MediaQuery.of(context);
            final scale = appTextScale(prefs, media.textScaler.scale(1));
            final glass = (prefs['liquidGlass'] ?? 'none') != 'none';
            return MeasureScope(
              measure: Measure.of(context, scale.clamp(.8, 1.4)),
              child: ViewScope(
              size: media.size,
              isMobile: media.size.width < 600,
              child: MediaQuery(
                data: media.copyWith(textScaler: TextScaler.linear(scale.clamp(.8, 1.4))),
                child: AnnotatedRegion<SystemUiOverlayStyle>(
                  value: SystemUiOverlayStyle(
                    statusBarColor: Colors.transparent, systemNavigationBarColor: Colors.transparent,
                    statusBarIconBrightness: Theme.of(context).brightness == Brightness.dark ? Brightness.light : Brightness.dark,
                    systemNavigationBarIconBrightness: Theme.of(context).brightness == Brightness.dark ? Brightness.light : Brightness.dark,
                  ),
                  child: LiquidGlassScope(enabled: glass,
                    quality: prefs['liquidGlass'] == 'ultra' ? LiquidGlassQuality.ultra : LiquidGlassQuality.standard,
                    child: LiquidGlassHost(enabled: glass, child: child!)),
                ),
              ),
            ),
            );
          },
          home: widget.controller.ready ? const HomePage() : Scaffold(
            body: SafeArea(child: Center(child: widget.controller.startupError == null
                ? const CircularProgressIndicator()
                : ErrorState(widget.controller.startupError!, retry: widget.controller.initialize))),
          ),
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
      onSelected: (index) => setState(() => _index = index),
      children: [for (var i = 0; i < _pages.length; i++)
        TickerMode(enabled: i == _index, child: PageActivityScope(isActive: i == _index, child: _pages[i])),
      ],
    );
    return PopScope(
      canPop: _index == 0,
      onPopInvokedWithResult: (didPop, _) { if (!didPop && _index != 0) setState(() => _index = 0); },
      child: Scaffold(
        body: Row(children: [
          if (!mobile) NavigationRail(
            selectedIndex: _index,
            onDestinationSelected: (index) => setState(() => _index = index),
            labelType: NavigationRailLabelType.all,
            destinations: [for (final item in _items) NavigationRailDestination(
              icon: GlyphIcon(item.$2), label: Text(context.tr(item.$1)),
            )],
          ),
          Expanded(child: Column(children: [
            Expanded(child: Stack(children: [
              Positioned.fill(child: BottomInsetScope(
                inset: mobile && floating ? NavigationDock.insetOf(context) : 0,
                child: content,
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
            if (mobile && !floating) NavigationBar(
              selectedIndex: _index,
              onDestinationSelected: (index) => setState(() => _index = index),
              destinations: [for (final (i, item) in _items.indexed)
                NavigationDestination(icon: AnimatedGlyph(glyph: item.$2, filled: i == _index), label: context.tr(item.$1)),
              ],
            ),
          ])),
        ]),
      ),
    );
  }
}
