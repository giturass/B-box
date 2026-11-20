import 'package:flutter/foundation.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:prism_ui/prism_ui.dart';

class TestUiLocalizations implements UiLocalizations {
  const TestUiLocalizations();

  @override
  String get back => 'Back';
  @override
  String get cancel => 'Cancel';
  @override
  String get clearSearch => 'Clear search';
  @override
  String get close => 'Close';
  @override
  String get copy => 'Copy';
  @override
  String get cut => 'Cut';
  @override
  String get dialog => 'Dialog';
  @override
  String get hidePassword => 'Hide password';
  @override
  String get more => 'More';
  @override
  String get noSearchResults => 'No matching results';
  @override
  String get paste => 'Paste';
  @override
  String get reset => 'Reset';
  @override
  String get select => 'Select';
  @override
  String get search => 'Search';
  @override
  String get selectAll => 'Select all';
  @override
  String get showLess => 'Collapse';
  @override
  String get showMore => 'Expand';
  @override
  String get showPassword => 'Show password';
  @override
  String get sort => 'Sort';
  @override
  String get submit => 'Submit';
}

class _Delegate extends LocalizationsDelegate<UiLocalizations> {
  const _Delegate();

  @override
  bool isSupported(Locale locale) => true;

  @override
  Future<UiLocalizations> load(Locale locale) =>
      SynchronousFuture(const TestUiLocalizations());

  @override
  bool shouldReload(_Delegate old) => false;
}

const testDelegates = <LocalizationsDelegate<dynamic>>[
  _Delegate(),
  GlobalWidgetsLocalizations.delegate,
];

class TestApp extends StatelessWidget {
  const TestApp({
    super.key,
    required this.child,
    this.homeBuilder = _identity,
    this.locale,
    this.theme,
    this.darkTheme,
    this.themeMode,
    this.remoteFocus = false,
    this.liquidGlass,
  });

  final Widget child;
  final Widget Function(Widget child) homeBuilder;
  final Locale? locale;
  final ThemeData? theme;
  final ThemeData? darkTheme;
  final ThemeMode? themeMode;
  final bool remoteFocus;
  final LiquidGlassQuality? liquidGlass;

  static Widget _identity(Widget child) => child;

  @override
  Widget build(BuildContext context) => PrismApp(
    locale: locale,
    theme: theme,
    darkTheme: darkTheme,
    themeMode: themeMode ?? ThemeMode.system,
    localizationsDelegates: testDelegates,
    remoteFocus: remoteFocus,
    liquidGlass: liquidGlass,
    home: homeBuilder(child),
  );
}

/// The scopes PrismApp provides, for a test with its own host.
Widget wrapAppScopes(BuildContext context, Widget child) =>
    MeasureScope(measure: Measure.of(context, 1), child: child);

/// An app with only what a widget test asks of it, on the real PrismApp.
class HostApp extends StatelessWidget {
  const HostApp({
    super.key,
    this.navigatorKey,
    this.home,
    this.onGenerateRoute,
    this.builder,
    this.theme,
    this.darkTheme,
    this.themeMode = ThemeMode.system,
    this.locale,
    this.remoteFocus = false,
    this.debugShowCheckedModeBanner = false,
  });

  final GlobalKey<NavigatorState>? navigatorKey;
  final Widget? home;
  final RouteFactory? onGenerateRoute;
  final TransitionBuilder? builder;
  final ThemeData? theme;
  final ThemeData? darkTheme;
  final ThemeMode themeMode;
  final Locale? locale;
  final bool remoteFocus;
  final bool debugShowCheckedModeBanner;

  @override
  Widget build(BuildContext context) => PrismApp(
    navigatorKey: navigatorKey,
    home: home,
    onGenerateRoute: onGenerateRoute,
    builder: builder,
    theme: theme,
    darkTheme: darkTheme,
    themeMode: themeMode,
    locale: locale,
    remoteFocus: remoteFocus,
    localizationsDelegates: testDelegates,
    debugShowCheckedModeBanner: debugShowCheckedModeBanner,
  );
}

/// A plain page route with a fade, for a test about what is under or over it.
class HostPageRoute<T> extends PageRoute<T> {
  HostPageRoute({
    required this.builder,
    super.settings,
    super.fullscreenDialog,
  });

  final WidgetBuilder builder;

  @override
  Color? get barrierColor => null;

  @override
  String? get barrierLabel => null;

  @override
  bool get maintainState => true;

  @override
  Duration get transitionDuration => const Duration(milliseconds: 300);

  @override
  Widget buildPage(
    BuildContext context,
    Animation<double> animation,
    Animation<double> secondaryAnimation,
  ) => builder(context);

  @override
  Widget buildTransitions(
    BuildContext context,
    Animation<double> animation,
    Animation<double> secondaryAnimation,
    Widget child,
  ) => FadeTransition(opacity: animation, child: child);
}

/// A plain modal with a dim, for a test about what lies under one.
Future<T?> showDialog<T>({
  required BuildContext context,
  required WidgetBuilder builder,
  bool barrierDismissible = true,
}) => showGeneralDialog<T>(
  context: context,
  barrierDismissible: barrierDismissible,
  barrierLabel: 'dismiss',
  barrierColor: const Color(0x8A000000),
  transitionDuration: const Duration(milliseconds: 150),
  pageBuilder: (context, _, _) => builder(context),
  transitionBuilder: (_, animation, _, child) =>
      FadeTransition(opacity: animation, child: child),
);
