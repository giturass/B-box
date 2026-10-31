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
  final opened = <String>[];
  final closed = <String>[];
  final addresses = <String, String>{};
  final pendingIP = <String, Completer<Object?>>{};
  final pendingQuality = <String, Completer<Object?>>{};
  String ip = '203.0.113.9';
  String status = 'Started';
  DateTime? cachedQualityAt;
  bool holdIP = false, failIP = false, failQuality = false, autoQuality = false, lateOnClose = false, remote = false;

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
          'dashboard': {'status': status, 'mode': 'rule', if (remote) 'remote': 'Remote server', if (remote) 'remoteConnected': true},
        };
        case 'dashboard.egress':
          ids.add(id!);
          addresses[id] = ip;
          if (failIP) throw PlatformException(code: 'unavailable', message: 'IP check failed');
          if (holdIP) return (pendingIP[id] = Completer<Object?>()).future;
          return ipInfo(id);
        case 'dashboard.probe.open':
          opened.add(id!);
          addresses[id] = ip;
          return null;
        case 'dashboard.qualityCache.get':
          if (cachedQualityAt == null) return null;
          return {'quality': {'ip': args['ip'], 'source': 'identMe', 'type': 'business',
            'organization': 'Cached Business', 'asn': 64500}, 'checkedAt': cachedQualityAt!.millisecondsSinceEpoch};
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
    final dashboard = _Dashboard()..status = 'Stopped'..autoQuality = true;
    await dashboard.mount(tester);
    await tester.pumpAndSettle();
    final toggle = find.byTooltip('Start');
    await tester.tap(toggle);
    await tester.pumpAndSettle();
    expect(dashboard.calls.where((call) => call.method == 'service.start'), isEmpty);
    expect(dashboard.ids, hasLength(1));
    expect(find.text('203.0.113.9'), findsOneWidget);
    expect(find.text('GB'), findsOneWidget);
    expect(find.text('Example Business'), findsOneWidget);
    expect(find.text('Business'), findsOneWidget);
    expect(find.text('Quality Good'), findsOneWidget);
    expect(find.text('Not running'), findsOneWidget);
    dashboard.events.add({'type': 'profiles', 'value': [{'id': 1, 'name': 'Profile'}]});
    dashboard.events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'selectedProfileId': 1}});
    await tester.pumpAndSettle();
    expect(dashboard.ids, hasLength(1));
    final start = find.bySemanticsLabel('Start');
    expect(start, findsOneWidget);
    await tester.tap(start);
    await tester.pumpAndSettle();
    expect(dashboard.calls.where((call) => call.method == 'service.start'), hasLength(1));
    expect(tester.takeException(), isNull);
  });

  testWidgets('stopped IP failures can be retried without starting the service', (tester) async {
    final dashboard = _Dashboard()..status = 'Stopped'..failIP = true;
    await dashboard.mount(tester);
    await tester.pumpAndSettle();
    expect(find.text('Check failed'), findsOneWidget);
    dashboard.failIP = false;
    dashboard.autoQuality = true;
    await tester.tap(find.byTooltip('Retry'));
    await tester.pumpAndSettle();
    expect(dashboard.ids, hasLength(2));
    expect(find.text('203.0.113.9'), findsOneWidget);
    expect(find.text('Example Business'), findsOneWidget);
    expect(find.text('Not running'), findsOneWidget);
    expect(dashboard.calls.where((call) => call.method == 'service.start'), isEmpty);
    expect(tester.takeException(), isNull);
  });

  for (final phase in ['IP', 'quality']) {
    testWidgets('start and stop cancel stale $phase and refresh the new exit', (tester) async {
      final dashboard = _Dashboard()
        ..status = 'Stopped'
        ..holdIP = phase == 'IP'
        ..lateOnClose = true;
      await dashboard.mount(tester);
      final direct = dashboard.ids.single;
      dashboard.events.add({'type': 'dashboard', 'value': {'status': 'Starting'}});
      await _advance(tester);
      expect(dashboard.closed, contains(direct));
      expect(dashboard.ids, hasLength(1));
      expect(find.text('203.0.113.9'), findsNothing);

      dashboard.ip = '198.51.100.2';
      dashboard.events.add({'type': 'dashboard', 'value': {'status': 'Started'}});
      await _advance(tester);
      expect(dashboard.ids, hasLength(2));
      final routed = dashboard.ids.last;
      if (phase == 'quality') expect(find.text('198.51.100.2'), findsOneWidget);
      dashboard.events.add({'type': 'dashboard', 'value': {'status': 'Stopping'}});
      await _advance(tester);
      expect(dashboard.closed, contains(routed));
      expect(dashboard.ids, hasLength(2));
      expect(find.text('198.51.100.2'), findsNothing);

      dashboard.ip = '192.0.2.3';
      dashboard.holdIP = false;
      dashboard.autoQuality = true;
      dashboard.events.add({'type': 'dashboard', 'value': {'status': 'Stopped'}});
      await tester.pumpAndSettle();
      expect(dashboard.ids, hasLength(3));
      expect(find.text('192.0.2.3'), findsOneWidget);
      expect(find.text('203.0.113.9'), findsNothing);
      expect(find.text('198.51.100.2'), findsNothing);
      expect(find.text('Stale Organization'), findsNothing);
      expect(find.text('Example Business'), findsOneWidget);
      expect(find.text('Not running'), findsOneWidget);
      final writes = dashboard.calls.where((call) => call.method == 'dashboard.qualityCache.put');
      expect(writes, hasLength(1));
      expect((writes.single.arguments as Map)['ip'], '192.0.2.3');
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('remote dashboards do not show the device exit IP', (tester) async {
    final dashboard = _Dashboard()..remote = true..autoQuality = true;
    await dashboard.mount(tester);
    await tester.pumpAndSettle();
    expect(dashboard.ids, isEmpty);
    dashboard.events.add({'type': 'dashboard', 'value': {'status': 'Stopped'}});
    await tester.pumpAndSettle();
    expect(find.text('203.0.113.9'), findsOneWidget);
    dashboard.events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'remote': 'Remote server', 'remoteConnected': true}});
    await tester.pumpAndSettle();
    expect(dashboard.ids, hasLength(1));
    expect(find.text('203.0.113.9'), findsNothing);
    await tester.tap(find.byTooltip('Retry'));
    await tester.pumpAndSettle();
    expect(dashboard.ids, hasLength(1));
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

  for (final change in ['mode', 'network', 'stopped network']) {
    testWidgets('$change changes cancel quality requests and ignore stale organizations', (tester) async {
      final dashboard = _Dashboard()
        ..lateOnClose = true
        ..status = change == 'stopped network' ? 'Stopped' : 'Started';
      await dashboard.mount(tester);
      final old = dashboard.ids.single;
      dashboard.ip = '198.51.100.2';
      dashboard.autoQuality = true;
      dashboard.changeRoute(network: change != 'mode');
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

  for (final status in ['Stopped', 'Started']) {
    testWidgets('returning with an unchanged route keeps the successful IP and quality: $status', (tester) async {
      final dashboard = _Dashboard()..status = status..autoQuality = true;
      await dashboard.mount(tester);
      await tester.pumpAndSettle();
      final qualityCalls = dashboard.calls.where((call) => call.method == 'dashboard.probe.get').length;
      addTearDown(() => tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed));
      for (final state in [AppLifecycleState.inactive, AppLifecycleState.paused, AppLifecycleState.hidden]) {
        tester.binding.handleAppLifecycleStateChanged(state);
        dashboard.events.add({'type': 'dashboard', 'value': {'status': status, 'mode': 'rule'}});
        dashboard.events.add({'type': 'lan', 'value': {'addresses': ['192.168.1.2'], 'stamp': 'wifi:1'}});
        await tester.pump();
        tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
        await tester.pumpAndSettle();
        expect(dashboard.ids, hasLength(1));
        expect(dashboard.calls.where((call) => call.method == 'dashboard.probe.get'), hasLength(qualityCalls));
        expect(find.text('203.0.113.9'), findsOneWidget);
        expect(find.text('Example Business'), findsOneWidget);
      }
      // A long stay in the background also keeps a successful result.
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
      await tester.pump(const Duration(minutes: 10));
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
      await tester.pumpAndSettle();
      expect(dashboard.ids, hasLength(1));
      expect(tester.takeException(), isNull);
    });

    testWidgets('background network changes cancel stale work and refresh on return: $status', (tester) async {
      final dashboard = _Dashboard()..status = status;
      await dashboard.mount(tester);
      final old = dashboard.ids.single;
      addTearDown(() => tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed));
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
      await tester.pump();
      expect(dashboard.closed, isNot(contains(old)));
      dashboard.ip = '198.51.100.2';
      dashboard.autoQuality = true;
      dashboard.changeRoute(network: true);
      await _advance(tester);
      expect(dashboard.closed, contains(old));
      expect(dashboard.ids, hasLength(1));
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
      await tester.pumpAndSettle();
      expect(dashboard.ids, hasLength(2));
      expect(find.text('198.51.100.2'), findsOneWidget);
      expect(find.text('Example Business'), findsOneWidget);
      expect(tester.takeException(), isNull);
    });
  }

  for (final phase in ['IP', 'quality']) {
    for (final finishHidden in [false, true]) {
      testWidgets('in-flight $phase is reused on return: completes in background=$finishHidden', (tester) async {
        final dashboard = _Dashboard()
          ..holdIP = phase == 'IP'
          ..autoQuality = phase == 'IP';
        await dashboard.mount(tester);
        final id = dashboard.ids.single;
        addTearDown(() => tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed));
        tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
        await tester.pump();
        expect(dashboard.closed, isEmpty);
        void answer() {
          if (phase == 'IP') { dashboard.pendingIP[id]!.complete(dashboard.ipInfo(id)); }
          else { dashboard.answerQuality(id); }
        }
        if (finishHidden) { answer(); await tester.pump(); }
        tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
        await _advance(tester);
        expect(dashboard.ids, hasLength(1));
        if (!finishHidden) { expect(dashboard.closed, isEmpty); answer(); }
        await tester.pumpAndSettle();
        expect(dashboard.ids, hasLength(1));
        expect(find.text('203.0.113.9'), findsOneWidget);
        expect(find.text('Example Business'), findsOneWidget);
        expect(tester.takeException(), isNull);
      });
    }
  }

  testWidgets('failed IP checks retry on return with a five-second throttle', (tester) async {
    final dashboard = _Dashboard()..failIP = true;
    await dashboard.mount(tester);
    await tester.pumpAndSettle();
    addTearDown(() => tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed));
    for (var i = 0; i < 3; i++) {
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
      await tester.pump();
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
      await tester.pump();
    }
    expect(dashboard.ids, hasLength(1));
    expect(find.text('Check failed'), findsOneWidget);
    await tester.pump(const Duration(seconds: 4));
    expect(dashboard.ids, hasLength(1));
    await tester.pump(const Duration(seconds: 2));
    await tester.pumpAndSettle();
    expect(dashboard.ids, hasLength(2));
    // A failed retry does not turn into an endless polling loop.
    await tester.pump(const Duration(seconds: 6));
    await tester.pump();
    expect(dashboard.ids, hasLength(2));
    dashboard.failIP = false;
    dashboard.autoQuality = true;
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    await tester.pump();
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pumpAndSettle();
    expect(dashboard.ids, hasLength(3));
    expect(find.text('203.0.113.9'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('a throttled retry waits in the background and manual refresh bypasses it', (tester) async {
    final dashboard = _Dashboard()..status = 'Stopped'..failIP = true;
    await dashboard.mount(tester);
    await tester.pumpAndSettle();
    addTearDown(() => tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed));
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    await tester.pump();
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pump();
    expect(dashboard.ids, hasLength(1));
    await tester.tap(find.byTooltip('Retry'));
    await tester.pumpAndSettle();
    expect(dashboard.ids, hasLength(2));
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    await tester.pump();
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pump();
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    await tester.pump(const Duration(seconds: 6));
    expect(dashboard.ids, hasLength(2));
    dashboard.failIP = false;
    dashboard.autoQuality = true;
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pumpAndSettle();
    expect(dashboard.ids, hasLength(3));
    await tester.pump(const Duration(seconds: 6));
    await tester.pump();
    expect(dashboard.ids, hasLength(3));
    expect(tester.takeException(), isNull);
  });

  testWidgets('a quality failure does not invalidate the successful IP on return', (tester) async {
    final dashboard = _Dashboard()..failQuality = true;
    await dashboard.mount(tester);
    await tester.pumpAndSettle();
    final qualityCalls = dashboard.calls.where((call) => call.method == 'dashboard.probe.get').length;
    addTearDown(() => tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed));
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    await tester.pump(const Duration(seconds: 6));
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pumpAndSettle();
    expect(dashboard.ids, hasLength(1));
    expect(dashboard.calls.where((call) => call.method == 'dashboard.probe.get'), hasLength(qualityCalls));
    expect(find.text('203.0.113.9'), findsOneWidget);
    expect(find.text('Unknown type'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('quality expiry updates only quality and waits for the foreground', (tester) async {
    final dashboard = _Dashboard()..status = 'Stopped'..autoQuality = true
      ..cachedQualityAt = DateTime.now().subtract(const Duration(days: 3)).add(const Duration(seconds: 10));
    await dashboard.mount(tester);
    await tester.pump();
    expect(find.text('Cached Business'), findsOneWidget);
    expect(dashboard.calls.where((call) => call.method == 'dashboard.probe.get'), isEmpty);
    addTearDown(() => tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed));
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    await tester.pump(const Duration(seconds: 20));
    expect(dashboard.opened, isEmpty);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pumpAndSettle();
    expect(dashboard.ids, hasLength(1));
    expect(dashboard.opened, hasLength(1));
    expect(dashboard.closed, contains(dashboard.opened.single));
    expect(find.text('203.0.113.9'), findsOneWidget);
    expect(find.text('Example Business'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('rapid route changes coalesce and bypass a previous IP failure cooldown', (tester) async {
    final dashboard = _Dashboard()..failIP = true;
    await dashboard.mount(tester);
    await tester.pumpAndSettle();
    dashboard.failIP = false;
    dashboard.autoQuality = true;
    dashboard.changeRoute();
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 100));
    dashboard.events.add({'type': 'groups', 'value': [{'tag': 'Proxy', 'selected': 'New node'}]});
    await tester.pump();
    expect(dashboard.ids, hasLength(1));
    await tester.pump(const Duration(milliseconds: 250));
    await tester.pumpAndSettle();
    expect(dashboard.ids, hasLength(2));
    expect(find.text('203.0.113.9'), findsOneWidget);
    await tester.pump(const Duration(seconds: 6));
    await tester.pump();
    expect(dashboard.ids, hasLength(2));
    expect(tester.takeException(), isNull);
  });

  testWidgets('restored group order and delays keep the IP but a different selection refreshes it', (tester) async {
    final dashboard = _Dashboard()..autoQuality = true;
    await dashboard.mount(tester);
    dashboard.events.add({'type': 'groups', 'value': [
      {'tag': 'Proxy', 'selected': 'A'}, {'tag': 'Auto', 'selected': 'B'},
    ]});
    await tester.pumpAndSettle();
    final queries = dashboard.ids.length;
    addTearDown(() => tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed));
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    await tester.pump();
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    dashboard.events.add({'type': 'groups', 'value': [
      {'tag': 'Auto', 'selected': 'B', 'items': [{'tag': 'B', 'delay': 30}]},
      {'tag': 'Proxy', 'selected': 'A'},
    ]});
    await tester.pumpAndSettle();
    expect(dashboard.ids, hasLength(queries));
    dashboard.ip = '198.51.100.2';
    dashboard.events.add({'type': 'groups', 'value': [
      {'tag': 'Auto', 'selected': 'C'}, {'tag': 'Proxy', 'selected': 'A'},
    ]});
    await tester.pumpAndSettle();
    expect(dashboard.ids, hasLength(queries + 1));
    expect(find.text('198.51.100.2'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}
