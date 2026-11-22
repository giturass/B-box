import 'dart:async';
import 'dart:convert' show jsonEncode;
import 'dart:io';
import 'dart:ui' as ui;

import 'package:b_box_ui/components/battery_optimization.dart';
import 'package:b_box_ui/pages/settings.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:flutter/foundation.dart' show TargetPlatform, debugDefaultTargetPlatformOverride;
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/errors.dart';
import 'support/flclash_battery_reference.dart' as reference;

enum _Tone { light, dark, pureBlack }
enum _PixelState { unauthorized, authorized, initialLoading, requestPending }

class _Viewport {
  const _Viewport(this.width, this.scale, this.tone, {this.height = 800});

  final double width, height, scale;
  final _Tone tone;

  String get name => '${width.toInt()}-${tone.name}-${scale.toStringAsFixed(1)}';
}

class _Scenario {
  const _Scenario(this.name, {
    this.locale = 'en',
    this.viewport = const _Viewport(360, 1, _Tone.light),
    this.position = ItemPosition.end,
  });

  final String name, locale;
  final _Viewport viewport;
  final ItemPosition position;
}

class _BatteryFixture {
  _BatteryFixture(this.box, this.events, this.nativeExempt);

  final BoxController box;
  final StreamController<dynamic> events;
  final calls = <MethodCall>[];
  final messages = <String>[];
  bool nativeExempt;
  Completer<Object?>? statusReply, requestReply;

  int count(String method) => calls.where((call) => call.method == method).length;

  void emitSettings(bool exempt) {
    nativeExempt = exempt;
    events.add({'type': 'settings', 'value': {...box.settings, 'batteryExempt': exempt}});
  }
}

ThemeData _theme(_Scenario scenario) {
  final scheme = seededColorScheme(
    seedColor: const Color(0xff007aff),
    brightness: scenario.viewport.tone == _Tone.light ? Brightness.light : Brightness.dark,
    style: ColorStyle.vivid,
  ).toPureBlack(scenario.viewport.tone == _Tone.pureBlack);
  return ThemeData(
    platform: TargetPlatform.android,
    colorScheme: scheme,
    fontFamily: 'Roboto',
    fontFamilyFallback: const ['BboxBatteryCJK'],
    elevatedColorScheme: scheme.brightness == Brightness.dark ? scheme.elevated : null,
  );
}

Widget _app(_Scenario scenario, BoxController box, Widget home) => BoxScope(
  controller: box,
  child: PrismApp(
    locale: Locale(scenario.locale),
    supportedLocales: const [Locale('en'), Locale('zh')],
    localizationsDelegates: const [BoxUiDelegate(), GlobalWidgetsLocalizations.delegate],
    theme: _theme(scenario),
    textScale: scenario.viewport.scale,
    debugShowCheckedModeBanner: false,
    builder: (context, child) => ViewScope(
      size: Size(scenario.viewport.width, scenario.viewport.height),
      isMobile: true,
      child: MediaQuery(
        data: MediaQuery.of(context).copyWith(disableAnimations: true),
        child: child!,
      ),
    ),
    home: home,
  ),
);

Widget _rowFrame(_Scenario scenario, BoxController box, Widget child, GlobalKey boundary) =>
  _app(scenario, box, Builder(builder: (context) => Surface(
    child: Align(
      alignment: Alignment.topCenter,
      child: UnconstrainedBox(
        constrainedAxis: Axis.horizontal,
        alignment: Alignment.topCenter,
        child: RepaintBoundary(
          key: boundary,
          child: ColoredBox(
            color: SurfaceLevel.page.colorOf(context.colorScheme),
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: SizedBox(
                width: scenario.viewport.width - 32,
                child: ItemPositionProvider(position: scenario.position, child: child),
              ),
            ),
          ),
        ),
      ),
    ),
  )));

// The loading indicator has a repeating controller even with a still painter.
// Fixed pumps retain that upstream widget without waiting on its infinite loop.
Future<void> _pump(WidgetTester tester) async {
  await tester.pump();
  await tester.pump(const Duration(milliseconds: 500));
  await tester.pump();
}

Future<_BatteryFixture> _fixture(WidgetTester tester, _Scenario scenario, {
  bool exempt = false,
  Completer<Object?>? statusReply,
  Completer<Object?>? requestReply,
}) async {
  reportFlutterErrors();
  final previousPlatform = debugDefaultTargetPlatformOverride;
  debugDefaultTargetPlatformOverride = TargetPlatform.android;
  addTearDown(() => debugDefaultTargetPlatformOverride = previousPlatform);
  tester.view.physicalSize = Size(scenario.viewport.width, scenario.viewport.height);
  tester.view.devicePixelRatio = 1;
  tester.binding.platformDispatcher.localesTestValue = [Locale(scenario.locale)];
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  addTearDown(tester.binding.platformDispatcher.clearLocalesTestValue);

  const channel = MethodChannel('test.battery-optimization');
  final events = StreamController<dynamic>.broadcast();
  final fixture = _BatteryFixture(BoxController(channel: channel, events: events.stream), events, exempt)
    ..statusReply = statusReply
    ..requestReply = requestReply;
  final messages = fixture.box.messages.stream.listen(fixture.messages.add);
  final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  messenger.setMockMethodCallHandler(channel, (call) async {
    fixture.calls.add(call);
    switch (call.method) {
      case 'ready': return {
        'version': 'test', 'coreVersion': 'test',
        'dashboard': {'status': 'Stopped'},
        'settings': {'batteryExempt': fixture.nativeExempt, 'allowBypass': false,
          'autoRedirect': false, 'dynamicNotification': true},
        'preferences': {'clipboardImport': false},
      };
      case 'battery.status': return fixture.statusReply?.future ?? fixture.nativeExempt;
      case 'battery.open': return fixture.requestReply?.future ?? fixture.nativeExempt;
      default: return null;
    }
  });
  addTearDown(() async {
    await tester.pumpWidget(const SizedBox());
    final pendingStatus = fixture.statusReply, pendingRequest = fixture.requestReply;
    if (pendingStatus != null && !pendingStatus.isCompleted) pendingStatus.complete(fixture.nativeExempt);
    if (pendingRequest != null && !pendingRequest.isCompleted) pendingRequest.complete(fixture.nativeExempt);
    await tester.pump();
    fixture.box.dispose();
    await events.close();
    await messages.cancel();
    messenger.setMockMethodCallHandler(channel, null);
  });
  await fixture.box.initialize();
  return fixture;
}

Future<_BatteryFixture> _mountRow(WidgetTester tester, {
  _Scenario scenario = const _Scenario('interaction'),
  bool exempt = false,
  Completer<Object?>? statusReply,
  Completer<Object?>? requestReply,
}) async {
  final fixture = await _fixture(tester, scenario, exempt: exempt,
    statusReply: statusReply, requestReply: requestReply);
  await tester.pumpWidget(_rowFrame(scenario, fixture.box,
    const BatteryOptimizationItem(), GlobalKey()));
  await _pump(tester);
  return fixture;
}

Finder get _row => find.byType(BatteryOptimizationItem);
Finder get _button => find.descendant(of: _row, matching: find.byType(FilledButton));
Finder get _indicator => find.descendant(of: _row, matching: find.byType(ActivityIndicator));

// Opacity(0) still participates in hit testing: use the actual foreground text
// rather than treating the hidden alternative label as visible with hitTestable.
String _label(WidgetTester tester) {
  final stack = tester.widget<FilledButton>(_button).child as Stack;
  return (stack.children.last as Text).data!;
}

Future<ui.Image> _capture(WidgetTester tester, GlobalKey key, String name) async =>
  (await tester.runAsync(() async {
    final boundary = key.currentContext!.findRenderObject()! as RenderRepaintBoundary;
    final bitmap = await boundary.toImage();
    final png = await bitmap.toByteData(format: ui.ImageByteFormat.png);
    final file = File('build/screenshots/battery-optimization/$name.png');
    await file.parent.create(recursive: true);
    await file.writeAsBytes(png!.buffer.asUint8List());
    return bitmap;
  }))!;

Future<void> _settleForPixels(WidgetTester tester) async {
  // Even the reduced-motion indicator breathes according to progress. Its
  // pinned painter exposes that controller: freeze the same input in both
  // trees, retaining the actual upstream ring, color and rasterization.
  for (final paint in tester.widgetList<CustomPaint>(find.descendant(
    of: find.byType(ActivityIndicator), matching: find.byType(CustomPaint)))) {
    final progress = (paint.painter as dynamic).progress as AnimationController;
    progress.stop(canceled: false);
    progress.value = .25;
  }
  await tester.pumpAndSettle();
}

Future<void> _expectPixels(WidgetTester tester, ui.Image actual, ui.Image expected, String name) async {
  expect(actual.width, expected.width);
  expect(actual.height, expected.height);
  final differentPixels = (await tester.runAsync(() async {
    final actualData = (await actual.toByteData(format: ui.ImageByteFormat.rawRgba))!;
    final expectedData = (await expected.toByteData(format: ui.ImageByteFormat.rawRgba))!;
    final actualBytes = actualData.buffer.asUint8List(actualData.offsetInBytes, actualData.lengthInBytes);
    final expectedBytes = expectedData.buffer.asUint8List(expectedData.offsetInBytes, expectedData.lengthInBytes);
    var different = 0;
    for (var offset = 0; offset < actualBytes.length; offset += 4) {
      if (actualBytes[offset] != expectedBytes[offset] ||
          actualBytes[offset + 1] != expectedBytes[offset + 1] ||
          actualBytes[offset + 2] != expectedBytes[offset + 2] ||
          actualBytes[offset + 3] != expectedBytes[offset + 3]) different++;
    }
    final report = File('build/screenshots/battery-optimization/$name-comparison.json');
    await report.parent.create(recursive: true);
    await report.writeAsString('${jsonEncode({
      'width': actual.width, 'height': actual.height,
      'totalPixels': actual.width * actual.height, 'differentPixels': different,
      'allowedDifferentPixels': 0,
    })}\n');
    return different;
  }))!;
  // Keep the established reference matcher for its diagnostics, and require
  // exactly zero differing RGBA pixels independently of any golden settings.
  await expectLater(actual, matchesReferenceImage(expected));
  expect(differentPixels, 0, reason: 'The complete RGBA images must match with zero tolerance');
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUpAll(() async {
    final cjk = await File('test/support/fonts/NotoSansSC-battery.ttf').readAsBytes();
    await (FontLoader('BboxBatteryCJK')..addFont(Future.value(ByteData.sublistView(cjk)))).load();
    final root = Platform.environment['FLUTTER_ROOT'];
    if (root == null) throw StateError('FLUTTER_ROOT is required for the pinned SDK Roboto test font');
    final font = File('$root/bin/cache/artifacts/material_fonts/Roboto-Regular.ttf');
    await (FontLoader('Roboto')..addFont(Future.value(ByteData.sublistView(await font.readAsBytes())))).load();
  });

  testWidgets('only the authorization button requests Android permission', (tester) async {
    final reply = Completer<Object?>();
    final fixture = await _mountRow(tester, requestReply: reply);
    final row = tester.widget<DecorationListItem>(find.descendant(of: _row,
      matching: find.byType(DecorationListItem)));
    expect(row.leading, isNull);
    expect(row.subtitle, isNull);
    expect(row.onPressed, isNull);
    await tester.tap(find.text('Ignore battery optimization'));
    await _pump(tester);
    final rect = tester.getRect(_row);
    await tester.tapAt(Offset(rect.left + 4, rect.center.dy));
    await _pump(tester);
    expect(fixture.count('battery.open'), 0);

    await tester.tap(_button);
    await _pump(tester);
    expect(fixture.count('battery.open'), 1);
    expect(fixture.calls.lastWhere((call) => call.method == 'battery.open').arguments, isNull);
    expect(_label(tester), 'Tap to authorize');
    expect(_button.hitTestable(), findsOneWidget);
    expect(_indicator, findsNothing);
    await tester.tap(_button);
    await tester.tap(_button);
    await _pump(tester);
    expect(fixture.count('battery.open'), 1);
    reply.complete(false);
    await _pump(tester);
    expect(fixture.box.settings['batteryExempt'], false);
    expect(fixture.count('settings.set'), 0);
    expect(fixture.count('service.start'), 0);
    expect(tester.takeException(), isNull);
  });

  testWidgets('initial status loading retains the button space and right aligned indicator', (tester) async {
    final reply = Completer<Object?>();
    final fixture = await _mountRow(tester, statusReply: reply);
    expect(fixture.count('battery.status'), 1);
    expect(_button.hitTestable(), findsNothing);
    expect(_indicator, findsOneWidget);
    expect(tester.widget<ActivityIndicator>(_indicator).size, 24);
    final before = tester.getRect(_button), rowBefore = tester.getRect(_row);
    final indicator = tester.getRect(_indicator);
    expect(indicator.right, closeTo(before.right, .001));
    expect(indicator.center.dy, closeTo(before.center.dy, .001));
    await tester.tap(find.text('Ignore battery optimization'));
    await _pump(tester);
    expect(fixture.count('battery.open'), 0);
    reply.complete(false);
    await _pump(tester);
    expect(_indicator, findsNothing);
    expect(_button.hitTestable(), findsOneWidget);
    expect(tester.getRect(_button), before);
    expect(tester.getRect(_row), rowBefore);
    expect(tester.takeException(), isNull);
  });

  for (final failure in [false, true]) {
    testWidgets('authorization ${failure ? 'failure' : 'cancellation'} preserves the denied state and allows retry', (tester) async {
      final reply = Completer<Object?>();
      final fixture = await _mountRow(tester, requestReply: reply);
      await tester.tap(_button);
      await _pump(tester);
      if (failure) {
        reply.completeError(PlatformException(code: 'unavailable', message: 'System request unavailable'));
      } else {
        reply.complete(false);
      }
      await _pump(tester);
      expect(fixture.box.settings['batteryExempt'], false,
        reason: 'Returning from a system screen is not an allowlist grant');
      expect(_label(tester), 'Tap to authorize');
      expect(_indicator, findsNothing);
      expect(fixture.messages, failure ? ['System request unavailable'] : isEmpty);
      fixture.requestReply = Completer<Object?>();
      await tester.tap(_button);
      await _pump(tester);
      expect(fixture.count('battery.open'), 2);
      fixture.requestReply!.complete(true);
      await _pump(tester);
      expect(_label(tester), 'Authorized');
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('confirmed grant changes label and color without resizing or repeating the request', (tester) async {
    final reply = Completer<Object?>();
    final fixture = await _mountRow(tester, requestReply: reply);
    final before = tester.getSize(_button);
    final denied = tester.widget<FilledButton>(_button);
    final scheme = tester.element(_button).colorScheme;
    expect(denied.style!.backgroundColor!.resolve(const {}), scheme.errorContainer);
    expect(denied.style!.foregroundColor!.resolve(const {}), scheme.onErrorContainer);
    await tester.tap(_button);
    await _pump(tester);
    reply.complete(true);
    await _pump(tester);
    expect(fixture.box.settings['batteryExempt'], true);
    expect(fixture.box.settings['dynamicNotification'], true);
    expect(fixture.box.preferences['clipboardImport'], false);
    expect(_label(tester), 'Authorized');
    expect(tester.getSize(_button), before);
    final authorized = tester.widget<FilledButton>(_button);
    expect(authorized.style!.backgroundColor, isNull);
    expect(authorized.style!.foregroundColor, isNull);
    await tester.tap(_button);
    await _pump(tester);
    expect(fixture.count('battery.open'), 1);
    expect(tester.takeException(), isNull);
  });

  testWidgets('already authorized button stays enabled and is a no-op', (tester) async {
    final fixture = await _mountRow(tester, exempt: true);
    expect(_label(tester), 'Authorized');
    expect(tester.widget<FilledButton>(_button).onPressed, isNotNull);
    await tester.tap(_button);
    await _pump(tester);
    expect(fixture.count('battery.open'), 0);
    expect(tester.takeException(), isNull);
  });

  testWidgets('only the foreground authorization label enters button semantics', (tester) async {
    final semantics = tester.ensureSemantics();
    addTearDown(semantics.dispose);
    final fixture = await _mountRow(tester);
    expect(find.bySemanticsLabel('Tap to authorize'), findsOneWidget);
    expect(find.bySemanticsLabel('Authorized'), findsNothing);
    fixture.emitSettings(true);
    await _pump(tester);
    expect(find.bySemanticsLabel('Authorized'), findsOneWidget);
    expect(find.bySemanticsLabel('Tap to authorize'), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('a settings event wins over a late authorization result and can revoke the grant', (tester) async {
    final reply = Completer<Object?>();
    final fixture = await _mountRow(tester, requestReply: reply);
    await tester.tap(_button);
    await _pump(tester);
    fixture.emitSettings(true);
    await _pump(tester);
    expect(_label(tester), 'Authorized');
    reply.complete(false);
    await _pump(tester);
    expect(fixture.box.settings['batteryExempt'], true);
    expect(fixture.count('battery.status'), 2);
    expect(_label(tester), 'Authorized');
    fixture.emitSettings(false);
    await _pump(tester);
    expect(_label(tester), 'Tap to authorize');
    expect(tester.takeException(), isNull);
  });

  testWidgets('a settings event wins over a late initial status read', (tester) async {
    final reply = Completer<Object?>();
    final fixture = await _mountRow(tester, statusReply: reply);
    fixture.emitSettings(true);
    await _pump(tester);
    reply.complete(false);
    await _pump(tester);
    expect(fixture.box.settings['batteryExempt'], true);
    expect(_label(tester), 'Authorized');
    expect(_indicator, findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('a settings snapshot during the prompt cannot hide a later real grant', (tester) async {
    final reply = Completer<Object?>();
    final fixture = await _mountRow(tester, requestReply: reply);
    await tester.tap(_button);
    await _pump(tester);
    fixture.emitSettings(false);
    await _pump(tester);
    // This newer platform state is returned by the confirmation read after
    // the intervening settings snapshot invalidates the prompt's response.
    fixture.nativeExempt = true;
    reply.complete(true);
    await _pump(tester);
    expect(fixture.count('battery.open'), 1);
    expect(fixture.count('battery.status'), 2);
    expect(fixture.box.settings['batteryExempt'], true);
    expect(_label(tester), 'Authorized');
    expect(tester.takeException(), isNull);
  });

  testWidgets('a reconnected backend snapshot wins over a pending old status read', (tester) async {
    final reply = Completer<Object?>();
    final fixture = await _mountRow(tester, statusReply: reply);
    fixture.nativeExempt = true;
    fixture.events.add({'type': 'backendReconnected', 'value': null});
    await _pump(tester);
    expect(fixture.count('ready'), 2);
    expect(fixture.box.settings['batteryExempt'], true);
    reply.complete(false);
    await _pump(tester);
    expect(fixture.box.settings['batteryExempt'], true);
    expect(_label(tester), 'Authorized');
    expect(_indicator, findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('returning to foreground refreshes the real state without a new loading appearance', (tester) async {
    final fixture = await _mountRow(tester);
    final reply = Completer<Object?>();
    fixture.statusReply = reply;
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await _pump(tester);
    expect(fixture.count('battery.status'), 2);
    expect(_label(tester), 'Tap to authorize');
    expect(_button.hitTestable(), findsOneWidget);
    expect(_indicator, findsNothing);
    reply.complete(true);
    await _pump(tester);
    expect(_label(tester), 'Authorized');
    expect(tester.takeException(), isNull);
  });

  testWidgets('returning during a system request waits for its confirmed response', (tester) async {
    final reply = Completer<Object?>();
    final fixture = await _mountRow(tester, requestReply: reply);
    await tester.tap(_button);
    await _pump(tester);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await _pump(tester);
    expect(fixture.count('battery.status'), 1);
    expect(fixture.count('battery.open'), 1);
    expect(_label(tester), 'Tap to authorize');
    expect(_indicator, findsNothing);
    reply.complete(true);
    await _pump(tester);
    expect(_label(tester), 'Authorized');
    expect(tester.takeException(), isNull);
  });

  for (final locale in ['zh', 'en']) {
    for (final viewport in const [
      _Viewport(320, 1, _Tone.light),
      _Viewport(320, 1.4, _Tone.dark),
      _Viewport(360, .8, _Tone.light),
      _Viewport(360, 1, _Tone.dark),
      _Viewport(412, 1.4, _Tone.pureBlack),
      _Viewport(412, .8, _Tone.light),
    ]) {
      for (final state in _PixelState.values) {
        final scenario = _Scenario('$locale-${viewport.name}-${state.name}', locale: locale, viewport: viewport);
        testWidgets('battery row matches frozen FlClash pixels: ${scenario.name}', (tester) async {
          final statusReply = state == _PixelState.initialLoading ? Completer<Object?>() : null;
          final requestReply = state == _PixelState.requestPending ? Completer<Object?>() : null;
          final fixture = await _fixture(tester, scenario,
            exempt: state == _PixelState.authorized, statusReply: statusReply, requestReply: requestReply);
          final boundary = GlobalKey();
          await tester.pumpWidget(_rowFrame(scenario, fixture.box,
            const BatteryOptimizationItem(), boundary));
          await _pump(tester);
          if (state == _PixelState.requestPending) {
            await tester.tap(_button);
            await _pump(tester);
            expect(fixture.count('battery.open'), 1);
            expect(_indicator, findsNothing);
          }
          await _settleForPixels(tester);
          expect(tester.takeException(), isNull);
          final actual = await _capture(tester, boundary, '${scenario.name}-bbox');
          await tester.pumpWidget(_rowFrame(scenario, fixture.box,
            reference.FlClashBatteryOptimizationItem(
              authorized: state == _PixelState.authorized,
              loading: state == _PixelState.initialLoading,
            ), boundary));
          await _pump(tester);
          await _settleForPixels(tester);
          expect(tester.takeException(), isNull);
          final expected = await _capture(tester, boundary, '${scenario.name}-flclash');
          try {
            expect(actual.width, viewport.width.toInt());
            expect(actual.height, lessThan(180), reason: 'The row keeps its natural content height');
            await _expectPixels(tester, actual, expected, scenario.name);
          } finally {
            actual.dispose();
            expected.dispose();
          }
        });
      }
    }
  }

  for (final locale in ['zh', 'en']) {
    testWidgets('battery row also preserves the upstream first-row decoration: $locale', (tester) async {
      final scenario = _Scenario('$locale-360-light-1.0-upstream-first',
        locale: locale, position: ItemPosition.start);
      final fixture = await _fixture(tester, scenario);
      final boundary = GlobalKey();
      await tester.pumpWidget(_rowFrame(scenario, fixture.box, const BatteryOptimizationItem(), boundary));
      await _pump(tester);
      await _settleForPixels(tester);
      final actual = await _capture(tester, boundary, '${scenario.name}-bbox');
      await tester.pumpWidget(_rowFrame(scenario, fixture.box,
        const reference.FlClashBatteryOptimizationItem(), boundary));
      await _pump(tester);
      await _settleForPixels(tester);
      final expected = await _capture(tester, boundary, '${scenario.name}-flclash');
      try { await _expectPixels(tester, actual, expected, scenario.name); }
      finally { actual.dispose(); expected.dispose(); }
      expect(tester.takeException(), isNull);
    });
  }

  for (final (viewport, exempt) in const [
    (_Viewport(360, 1, _Tone.light), false),
    (_Viewport(320, 1.4, _Tone.dark, height: 568), true),
  ]) {
    final scenario = _Scenario('zh-${viewport.name}-service-${exempt ? 'authorized' : 'unauthorized'}',
      locale: 'zh', viewport: viewport);
    testWidgets('service page keeps the upstream row in context: ${scenario.name}', (tester) async {
      final fixture = await _fixture(tester, scenario, exempt: exempt);
      final boundary = GlobalKey();
      await tester.pumpWidget(RepaintBoundary(key: boundary,
        child: _app(scenario, fixture.box, const SettingsPage())));
      await tester.pumpAndSettle();
      await tester.tap(find.text('服务'));
      await tester.pumpAndSettle();
      expect(find.byType(ServiceSettingsPage), findsOneWidget);
      expect(_row, findsOneWidget);
      expect(find.text('忽略电池优化'), findsOneWidget);
      expect(find.text('电池优化'), findsNothing);
      expect(_label(tester), exempt ? '已授权' : '点击授权');
      expect(_button.hitTestable(), findsOneWidget);
      expect(fixture.box.preferences['clipboardImport'], false);
      final clipboard = tester.getRect(find.text('剪贴板节点导入提示'));
      final battery = tester.getRect(_row), button = tester.getRect(_button);
      expect(clipboard.bottom, lessThan(battery.top));
      expect(button.right, lessThanOrEqualTo(viewport.width - 16));
      expect(button.bottom, lessThan(viewport.height));
      expect(fixture.count('battery.open'), 0);
      expect(tester.takeException(), isNull);
      final image = await _capture(tester, boundary, scenario.name);
      image.dispose();
    });
  }
}
