import 'dart:async';
import 'dart:io';
import 'dart:math' as math;
import 'dart:ui' as ui;
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/components/status_panel.dart';
import 'package:b_box_ui/components/start_toggle.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'support/errors.dart';
import 'support/flclash_status_reference.dart' as reference;

class _Scenario {
  const _Scenario(this.name, {this.locale = 'zh', this.dark = false, this.scale = 1,
    this.width = 360, this.ip = '203.0.113.9', this.country = 'GB',
    this.type = reference.IpType.business, this.level = reference.IpQualityLevel.good,
    this.organization = 'Example Business', this.asn = 64500,
    this.pendingIP = false, this.pendingQuality = false,
    this.failIP = false, this.failQuality = false});

  final String name, locale, ip;
  final bool dark, pendingIP, pendingQuality, failIP, failQuality;
  final double scale, width;
  final String? country, organization;
  final int? asn;
  final reference.IpType type;
  final reference.IpQualityLevel level;

  reference.IpQuality? get quality => pendingIP || pendingQuality || failIP || failQuality ? null
    : reference.IpQuality(type: type, level: level, organization: organization, asn: asn);
}

Widget _frame(_Scenario scenario, GlobalKey boundary, Widget child, BoxController box, {String runtime = '00:05:30'}) =>
  BoxScope(controller: box, child: MaterialApp(
    locale: Locale(scenario.locale), supportedLocales: const [Locale('en'), Locale('zh')],
    localizationsDelegates: const [BoxUiDelegate(), ...GlobalMaterialLocalizations.delegates],
    theme: ThemeData(useMaterial3: true, fontFamily: 'Roboto',
      colorScheme: ColorScheme.fromSeed(seedColor: const Color(0xff007aff),
        brightness: scenario.dark ? Brightness.dark : Brightness.light))
      .withAppShapes.withAppSurfaces.withAppTypography,
    home: Builder(builder: (context) => MeasureScope(measure: Measure.of(context, scenario.scale),
      child: ViewScope(size: Size(scenario.width, 800), isMobile: true,
        child: MediaQuery(data: MediaQuery.of(context).copyWith(textScaler: TextScaler.linear(scenario.scale), disableAnimations: true),
          child: reference.DashboardDensityScope(density: reference.DashboardDensity.mobile, scale: math.max(1, scenario.scale),
            child: reference.ReferenceRuntimeScope(text: runtime,
              child: Scaffold(body: Align(alignment: Alignment.topCenter,
                child: RepaintBoundary(key: boundary,
                  child: ColoredBox(color: SurfaceLevel.page.colorOf(context.colorScheme),
                    child: Padding(padding: const EdgeInsets.all(16),
                      child: SizedBox(width: scenario.width - 32, child: child)))))))))))),
  ));

Future<ui.Image> _capture(WidgetTester tester, GlobalKey key, String filename) async =>
  (await tester.runAsync(() async {
    final boundary = key.currentContext!.findRenderObject()! as RenderRepaintBoundary;
    final image = await boundary.toImage();
    final png = await image.toByteData(format: ui.ImageByteFormat.png);
    final file = File('build/screenshots/status-card/$filename.png');
    await file.parent.create(recursive: true);
    await file.writeAsBytes(png!.buffer.asUint8List());
    return image;
  }))!;

void main() {
  setUpAll(() async {
    final root = Platform.environment['FLUTTER_ROOT'];
    if (root == null) return;
    final font = File('$root/bin/cache/artifacts/material_fonts/Roboto-Regular.ttf');
    if (font.existsSync()) {
      await (FontLoader('Roboto')..addFont(Future.value(ByteData.sublistView(await font.readAsBytes())))).load();
    }
  });

  for (final scenario in const [
    _Scenario('zh-light-business'),
    _Scenario('en-light-business', locale: 'en'),
    _Scenario('zh-dark-large', dark: true, scale: 1.4),
    _Scenario('en-dark-large', locale: 'en', dark: true, scale: 1.4),
    _Scenario('zh-small', scale: .8),
    _Scenario('ipv6', ip: '2001:db8:1234:5678:9abc:def0:1234:5678', country: null),
    _Scenario('ip-pending', pendingIP: true),
    _Scenario('quality-pending', pendingQuality: true, locale: 'en'),
    _Scenario('ip-failed', failIP: true),
    _Scenario('quality-failed', failQuality: true),
    _Scenario('asn-fallback', type: reference.IpType.hosting, level: reference.IpQualityLevel.normal, organization: null, asn: 64501),
    _Scenario('residential-risky', type: reference.IpType.residential, level: reference.IpQualityLevel.risky, width: 414),
  ]) {
    testWidgets('status card matches FlClash pixels: ${scenario.name}', (tester) async {
      reportFlutterErrors();
      tester.view.physicalSize = Size(scenario.width, 800);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final channel = MethodChannel('test.status-parity.${scenario.name}');
      final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
      final ipReply = Completer<Object?>(), qualityReply = Completer<Object?>();
      messenger.setMockMethodCallHandler(channel, (call) async {
        switch (call.method) {
          case 'dashboard.egress':
            if (scenario.pendingIP) return ipReply.future;
            if (scenario.failIP) throw PlatformException(code: 'unavailable', message: 'IP check failed');
            return {'ip': scenario.ip, 'country': scenario.country, 'ipSource': 'www.cloudflare.com'};
          case 'dashboard.qualityCache.get':
            if (scenario.pendingQuality || scenario.failQuality) return null;
            return {'quality': {'ip': scenario.ip, 'source': 'identMe', 'type': scenario.type.name,
              'organization': scenario.organization, 'asn': scenario.asn,
              'isTor': scenario.level == reference.IpQualityLevel.risky},
              'checkedAt': DateTime.now().millisecondsSinceEpoch};
          case 'dashboard.probe.get':
            if (scenario.pendingQuality && Uri.parse(json(call.arguments)['url'] as String).host == 'ident.me') return qualityReply.future;
            return {'status': 503, 'body': ''};
          case 'dashboard.probe.close':
            final error = PlatformException(code: 'cancelled', message: 'Operation cancelled');
            if (scenario.pendingIP && !ipReply.isCompleted) ipReply.completeError(error);
            if (scenario.pendingQuality && !qualityReply.isCompleted) qualityReply.completeError(error);
            return null;
        }
        return null;
      });
      final box = BoxController(channel: channel, events: const Stream.empty())
        ..dashboard = {'status': 'Started', 'selectedProfileId': 1,
          'startedAt': DateTime.now().millisecondsSinceEpoch - 330000}
        ..profiles = [{'id': 1, 'name': 'Profile'}];
      final boundary = GlobalKey();
      addTearDown(() async {
        await tester.pumpWidget(const SizedBox());
        await tester.pump();
        box.dispose();
        messenger.setMockMethodCallHandler(channel, null);
      });
      await tester.pumpWidget(_frame(scenario, boundary, const StatusPanel(), box));
      await tester.pump();
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 500));
      await tester.pump();
      expect(tester.takeException(), isNull);
      final runtime = tester.widgetList<Text>(find.byType(Text))
        .map((text) => text.data).whereType<String>()
        .singleWhere((text) => RegExp(r'^\d{2,3}:\d{2}:\d{2}$').hasMatch(text));
      if (scenario.name == 'zh-light-business') {
        expect(find.text('质量 优').hitTestable(), findsOneWidget);
        expect(find.text('商业').hitTestable(), findsOneWidget);
      }
      if (scenario.name == 'ipv6') {
        expect(tester.widget<Text>(find.text(scenario.ip)).overflow, TextOverflow.ellipsis);
        expect(find.byTooltip('出站 IP详情'), findsOneWidget);
      }
      final actual = await _capture(tester, boundary, '${scenario.name}-bbox');
      await tester.pumpWidget(_frame(scenario, boundary, reference.FlClashStatusCard(
        info: scenario.pendingIP || scenario.failIP ? null : reference.IpInfo(scenario.ip, scenario.country),
        quality: scenario.quality, qualityLoading: scenario.pendingQuality,
        phase: scenario.failIP ? reference.ProbePhase.failed
          : scenario.pendingIP ? reference.ProbePhase.probing : reference.ProbePhase.fresh,
      ), box, runtime: runtime));
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull);
      final expected = await _capture(tester, boundary, '${scenario.name}-flclash');
      try { await expectLater(actual, matchesReferenceImage(expected)); }
      finally { actual.dispose(); expected.dispose(); }
    });
  }

  for (final enabled in [false, true]) {
    testWidgets('stopped start button matches FlClash pixels: enabled=$enabled', (tester) async {
      const scenario = _Scenario('stopped');
      final box = BoxController(events: const Stream.empty());
      final boundary = GlobalKey();
      addTearDown(() async {
        await tester.pumpWidget(const SizedBox());
        box.dispose();
      });
      await tester.pumpWidget(_frame(scenario, boundary, Center(child: StartToggle(
        size: 88, enabled: enabled, running: false, suspended: false, onTap: () {})), box));
      await tester.pumpAndSettle();
      final actual = await _capture(tester, boundary, 'stopped-$enabled-bbox');
      await tester.pumpWidget(_frame(scenario, boundary, Center(child: reference.FlClashStartToggle(
        size: 88, enabled: enabled, running: false, suspended: false)), box));
      await tester.pumpAndSettle();
      final expected = await _capture(tester, boundary, 'stopped-$enabled-flclash');
      try { await expectLater(actual, matchesReferenceImage(expected)); }
      finally { actual.dispose(); expected.dispose(); }
      expect(tester.takeException(), isNull);
    });
  }
}
