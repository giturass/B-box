import 'dart:async';
import 'dart:convert';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/components/status_panel.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'support/errors.dart';

class _Dashboard {
  final channel = const MethodChannel('test.status-ip');
  final events = StreamController<dynamic>.broadcast();
  final calls = <MethodCall>[];
  final ids = <String>[];
  final closed = <String>[];
  final addresses = <String, String>{};
  final pendingIP = <String, Completer<Object?>>{};
  final pendingQuality = <String, Completer<Object?>>{};
  String ip = '203.0.113.9';
  String status = 'Started';
  bool holdIP = false, failQuality = false, autoQuality = false, lateOnClose = false;

  Map<String, Object?> ipInfo(String id) => {'ip': addresses[id], 'country': 'GB',
    'ipSource': 'www.cloudflare.com', 'checkedAt': DateTime.now().millisecondsSinceEpoch};
  Map<String, Object?> quality(String id, {String organization = 'Example Business'}) => {
    'status': 200,
    'body': jsonEncode({'ip': addresses[id], 'type': 'business', 'aso': organization, 'asn': 64500}),
  };

  Future<void> mount(WidgetTester tester, {String locale = 'en', double scale = 1}) async {
    reportFlutterErrors();
    tester.binding.platformDispatcher.localesTestValue = [Locale(locale)];
    addTearDown(tester.binding.platformDispatcher.clearLocalesTestValue);
    tester.view.physicalSize = const Size(360, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(channel, (call) async {
      calls.add(call);
      final args = json(call.arguments);
      final id = args['id'] as String?;
      switch (call.method) {
        case 'ready': return {
          'version': 'test', 'coreVersion': 'test',
          'preferences': {'fontScale': scale,
            'hiddenCards': ['Network speed', 'Outbound mode', 'Current profile', 'Proxy groups', 'Memory', 'Intranet IP']},
          'dashboard': {'status': status, 'mode': 'rule'},
        };
        case 'dashboard.egress':
          ids.add(id!);
          addresses[id] = ip;
          if (holdIP) return (pendingIP[id] = Completer<Object?>()).future;
          return ipInfo(id);
        case 'dashboard.probe.get':
          if (failQuality || Uri.parse(args['url'] as String).host != 'ident.me') return {'status': 503, 'body': ''};
          if (autoQuality) return quality(id!);
          return (pendingQuality[id!] = Completer<Object?>()).future;
        case 'dashboard.probe.close':
          closed.add(id!);
          final ipReply = pendingIP[id];
          if (ipReply != null && !ipReply.isCompleted) {
            if (lateOnClose) { ipReply.complete(ipInfo(id)); }
            else { ipReply.completeError(PlatformException(code: 'cancelled', message: 'Operation cancelled')); }
          }
          final qualityReply = pendingQuality[id];
          if (qualityReply != null && !qualityReply.isCompleted) {
            if (lateOnClose) { qualityReply.complete(quality(id, organization: 'Stale Organization')); }
            else { qualityReply.completeError(PlatformException(code: 'cancelled', message: 'Operation cancelled')); }
          }
          return null;
      }
      return null;
    });
    addTearDown(() async {
      await tester.pumpWidget(const SizedBox());
      await tester.pump();
      await events.close();
      messenger.setMockMethodCallHandler(channel, null);
    });
    final controller = BoxController(channel: channel, events: events.stream)
      ..lan = {'addresses': ['192.168.1.2'], 'stamp': 'wifi:1'};
    await tester.pumpWidget(BoxApp(controller: controller));
    await tester.pump();
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 100));
  }

  void changeRoute({bool network = false}) => events.add(network
    ? {'type': 'lan', 'value': {'addresses': ['192.168.1.2'], 'stamp': 'wifi:2'}}
    : {'type': 'dashboard', 'value': {'status': 'Started', 'mode': 'global'}});
  void answerQuality(String id) => pendingQuality[id]!.complete(quality(id));
}

Future<void> _advance(WidgetTester tester) async {
  await tester.pump();
  await tester.pump(const Duration(milliseconds: 500));
  await tester.pump();
}

void main() {
  testWidgets('start is disabled until a usable profile is selected', (tester) async {
    final dashboard = _Dashboard()..status = 'Stopped';
    await dashboard.mount(tester);
    await tester.pumpAndSettle();
    final toggle = find.byTooltip('Start');
    await tester.tap(toggle);
    await tester.pumpAndSettle();
    expect(dashboard.calls.where((call) => call.method == 'service.start'), isEmpty);
    expect(dashboard.ids, isEmpty);
    dashboard.events.add({'type': 'profiles', 'value': [{'id': 1, 'name': 'Profile'}]});
    dashboard.events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'selectedProfileId': 1}});
    await tester.pumpAndSettle();
    await tester.tap(toggle);
    await tester.pumpAndSettle();
    expect(dashboard.calls.where((call) => call.method == 'service.start'), hasLength(1));
    expect(tester.takeException(), isNull);
  });

  testWidgets('a running tunnel can stop after its selected profile disappears', (tester) async {
    final dashboard = _Dashboard()..autoQuality = true;
    await dashboard.mount(tester);
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Stop'));
    await tester.pumpAndSettle();
    expect(dashboard.calls.where((call) => call.method == 'service.stop'), hasLength(1));
    expect(tester.takeException(), isNull);
  });

  for (final (locale, scale) in [('en', 1.0), ('zh', 1.4)]) {
    testWidgets('IP appears before quality and open details update: $locale', (tester) async {
      final dashboard = _Dashboard();
      await dashboard.mount(tester, locale: locale, scale: scale);
      expect(find.text('203.0.113.9'), findsOneWidget);
      expect(find.text('Example Business'), findsNothing);
      expect(find.byType(SkeletonPill), findsWidgets);
      await tester.tap(find.text('203.0.113.9'));
      await _advance(tester);
      expect(find.byType(EgressDetails), findsOneWidget);
      final details = find.byType(EgressDetails);
      expect(find.descendant(of: details, matching: find.text('www.cloudflare.com')), findsOneWidget);
      expect(find.descendant(of: details, matching: find.text(locale == 'zh' ? '类型未知' : 'Unknown type')), findsOneWidget);
      dashboard.answerQuality(dashboard.ids.single);
      await tester.pumpAndSettle();
      expect(find.descendant(of: details, matching: find.text('Example Business')), findsOneWidget);
      expect(find.descendant(of: details, matching: find.text('AS64500')), findsOneWidget);
      expect(find.descendant(of: details, matching: find.text('ident.me')), findsOneWidget);
      expect(find.descendant(of: details, matching: find.text(locale == 'zh' ? '商业' : 'Business')), findsOneWidget);
      expect(dashboard.calls.where((call) => call.method == 'dashboard.qualityCache.put'), hasLength(1));
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('quality failure retains the IP and the badge retries immediately', (tester) async {
    final dashboard = _Dashboard()..failQuality = true;
    await dashboard.mount(tester);
    await tester.pumpAndSettle();
    expect(find.text('203.0.113.9'), findsOneWidget);
    expect(find.text('Check failed'), findsNothing);
    expect(find.text('Unknown type'), findsOneWidget);
    await tester.tap(find.text('203.0.113.9'));
    await tester.pumpAndSettle();
    await tester.scrollUntilVisible(find.text('IP quality unavailable'), 250,
      scrollable: find.descendant(of: find.byType(EgressDetails), matching: find.byType(Scrollable)).first);
    await tester.pumpAndSettle();
    expect(find.text('IP quality unavailable'), findsOneWidget);
    await tester.tap(find.byTooltip('Close'));
    await tester.pumpAndSettle();
    expect(find.byType(EgressDetails), findsNothing);

    dashboard.failQuality = false;
    await tester.tap(find.byTooltip('Retry'));
    await tester.pump();
    await tester.pump();
    expect(dashboard.ids, hasLength(2));
    dashboard.answerQuality(dashboard.ids.last);
    await tester.pumpAndSettle();
    expect(find.text('203.0.113.9'), findsOneWidget);
    expect(find.text('Example Business'), findsOneWidget);
    expect(find.text('Business'), findsOneWidget);
    expect(dashboard.calls.where((call) => call.method == 'dashboard.probe.get'), hasLength(9));
    expect(tester.takeException(), isNull);
  });

  testWidgets('route changes cancel IP requests and ignore their late answers', (tester) async {
    final dashboard = _Dashboard()..holdIP = true..lateOnClose = true;
    await dashboard.mount(tester);
    final old = dashboard.ids.single;
    dashboard.ip = '198.51.100.2';
    dashboard.autoQuality = true;
    dashboard.changeRoute();
    await _advance(tester);
    expect(dashboard.ids, hasLength(2));
    expect(dashboard.closed, contains(old));
    final current = dashboard.ids.last;
    dashboard.pendingIP[current]!.complete(dashboard.ipInfo(current));
    await tester.pumpAndSettle();
    expect(find.text('198.51.100.2'), findsOneWidget);
    expect(find.text('203.0.113.9'), findsNothing);
    expect(find.text('Example Business'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  for (final change in ['mode', 'network']) {
    testWidgets('$change changes cancel quality requests and ignore stale organizations', (tester) async {
      final dashboard = _Dashboard()..lateOnClose = true;
      await dashboard.mount(tester);
      final old = dashboard.ids.single;
      dashboard.ip = '198.51.100.2';
      dashboard.autoQuality = true;
      dashboard.changeRoute(network: change == 'network');
      await tester.pumpAndSettle();
      expect(dashboard.closed, contains(old));
      expect(find.text('198.51.100.2'), findsOneWidget);
      expect(find.text('203.0.113.9'), findsNothing);
      expect(find.text('Example Business'), findsOneWidget);
      expect(find.text('Stale Organization'), findsNothing);
      final writes = dashboard.calls.where((call) => call.method == 'dashboard.qualityCache.put');
      expect(writes, hasLength(1));
      expect((writes.single.arguments as Map)['ip'], '198.51.100.2');
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('backgrounding cancels the probe and resume queries the current route', (tester) async {
    final dashboard = _Dashboard();
    await dashboard.mount(tester);
    final old = dashboard.ids.single;
    addTearDown(() => tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed));
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    await tester.pump();
    expect(dashboard.closed, contains(old));
    dashboard.ip = '198.51.100.2';
    dashboard.autoQuality = true;
    dashboard.changeRoute();
    await _advance(tester);
    expect(dashboard.ids, hasLength(1));
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pumpAndSettle();
    expect(dashboard.ids, hasLength(2));
    expect(find.text('198.51.100.2'), findsOneWidget);
    expect(find.text('Example Business'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}
