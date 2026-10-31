import 'dart:async';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/pages/requests.dart';
import 'package:b_box_ui/pages/request_settings.dart';
import 'package:b_box_ui/pages/capture_certificate.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  late BoxController box;
  late StreamController<dynamic> events;
  late List<MethodCall> calls;
  late Json snapshot;
  Future<dynamic> Function(MethodCall)? extra;

  Future<void> mount(WidgetTester tester, {bool remote = false, double scale = 1, Size size = const Size(360, 900)}) async {
    tester.binding.platformDispatcher.localesTestValue = const [Locale('en')];
    addTearDown(tester.binding.platformDispatcher.clearLocalesTestValue);
    tester.view.physicalSize = size;
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    calls = [];
    snapshot = {'state': {'active': false}, 'config': {}, 'ca': {'exists': false}, 'page': {'items': [], 'total': 0, 'storedBytes': 0}};
    const channel = MethodChannel('test.requests');
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(channel, (call) async {
      calls.add(call);
      if (call.method == 'ready') return {
        'version': 'test', 'coreVersion': 'test', 'preferences': {'fontScale': scale},
        'dashboard': {'status': 'Started', if (remote) ...{'remote': 'Server', 'remoteConnected': true}},
      };
      if (extra != null) return extra!(call);
      if (call.method == 'capture.snapshot' || call.method == 'capture.ca.snapshot') return snapshot;
      if (call.method == 'capture.start') { snapshot['state'] = {'active': true}; return snapshot['state']; }
      if (call.method == 'capture.stop') { snapshot['state'] = {'active': false}; return snapshot['state']; }
      if (call.method == 'apps.list') return [{'package': 'com.example', 'name': 'Example', 'internet': true}];
      return null;
    });
    events = StreamController<dynamic>.broadcast();
    box = BoxController(channel: channel, events: events.stream);
    await tester.pumpWidget(BoxApp(controller: box));
    await tester.pumpAndSettle();
    addTearDown(() async {
      extra = null;
      await tester.pumpWidget(const SizedBox());
      await events.close();
      messenger.setMockMethodCallHandler(channel, null);
    });
  }

  Future<void> open(WidgetTester tester, Widget page) async {
    openPage(tester.element(find.byType(HomePage)), page);
    await tester.pumpAndSettle();
  }

  testWidgets('Requests precedes Connections in Tools and is isolated from remote sessions', (tester) async {
    await mount(tester, remote: true);
    await tester.tap(find.text('Tools').last);
    await tester.pumpAndSettle();
    expect(tester.getTopLeft(find.text('Requests')).dy, lessThan(tester.getTopLeft(find.text('Connections')).dy));
    await tester.tap(find.text('Requests'));
    await tester.pumpAndSettle();
    expect(find.text('HTTP debugging is only available for the local service'), findsOneWidget);
    expect(calls.where((call) => call.method.startsWith('capture.')), isEmpty);
    expect(tester.takeException(), isNull);
  });

  testWidgets('empty HTTPS list is saved as unrestricted and filters survive', (tester) async {
    await mount(tester);
    await open(tester, const RequestSettingsPage(initial: {
      'https': true, 'decryptHosts': [], 'excludeHosts': ['private.example'], 'includeHosts': ['*.example'], 'packages': ['com.example'],
    }));
    expect(find.text('Leave empty to decrypt all domains. Use exact names or *.example.com; bypass rules take priority.'), findsOneWidget);
    await tester.tap(find.byTooltip('Save').last);
    await tester.pumpAndSettle();
    final config = json(json(calls.singleWhere((call) => call.method == 'capture.configure').arguments)['config']);
    expect(config['https'], isTrue);
    expect(config['decryptHosts'], isEmpty);
    expect(config['excludeHosts'], ['private.example']);
    expect(config['includeHosts'], ['*.example']);
    expect(config['packages'], ['com.example']);
    expect(config['bodyLimit'], 65536);
    expect(tester.takeException(), isNull);
  });

  testWidgets('start waits for native confirmation and service status gates it', (tester) async {
    await mount(tester);
    final started = Completer<dynamic>();
    extra = (call) async {
      if (call.method == 'capture.snapshot') return snapshot;
      if (call.method == 'capture.start') return started.future;
      return null;
    };
    await open(tester, const RequestsPage());
    await tester.tap(find.text('Capture requests'));
    await tester.pump();
    expect(calls.where((call) => call.method == 'capture.start'), hasLength(1));
    expect(find.text('Capture stopped'), findsOneWidget);
    snapshot['state'] = {'active': true};
    started.complete(null);
    await tester.pumpAndSettle();
    expect(find.text('Capturing new connections'), findsOneWidget);
    snapshot['state'] = {'active': false};
    events.add({'type': 'dashboard', 'value': {'status': 'Stopped'}});
    await tester.pumpAndSettle();
    await tester.pump(const Duration(seconds: 2));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Capture requests'));
    await tester.pumpAndSettle();
    expect(calls.where((call) => call.method == 'capture.start'), hasLength(1));
    expect(tester.takeException(), isNull);
  });

  testWidgets('polling stops in the background and late replies do not replace filters', (tester) async {
    await mount(tester);
    await open(tester, const RequestsPage());
    final firstCount = calls.where((call) => call.method == 'capture.snapshot').length;
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    await tester.pump(const Duration(seconds: 8));
    expect(calls.where((call) => call.method == 'capture.snapshot'), hasLength(firstCount));
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pumpAndSettle();
    expect(calls.where((call) => call.method == 'capture.snapshot').length, greaterThan(firstCount));
    final slow = Completer<dynamic>();
    extra = (call) async {
      if (call.method != 'capture.snapshot') return null;
      final query = json(call.arguments)['query'];
      if (query == 'old') return slow.future;
      return {...snapshot, 'page': {'items': [{'id': 'new', 'url': 'https://new.example/', 'method': 'GET', 'status': 200}], 'total': 1}};
    };
    CommonScaffold scaffold() => tester.widget<CommonScaffold>(find.descendant(of: find.byType(RequestsPage), matching: find.byType(CommonScaffold)).first);
    scaffold().onKeywordsUpdate!(['old']);
    await tester.pump();
    scaffold().onKeywordsUpdate!(['new']);
    await tester.pump();
    slow.complete({...snapshot, 'page': {'items': [{'id': 'old', 'url': 'https://old.example/', 'method': 'GET'}], 'total': 1}});
    await tester.pumpAndSettle();
    expect(find.text('https://new.example/'), findsOneWidget);
    expect(find.text('https://old.example/'), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('CA import remains pending until checked, with public-only export', (tester) async {
    await mount(tester);
    final ca = {'exists': true, 'fingerprint': 'ab' * 32, 'expiresAt': 2000000000000, 'filename': '1234abcd.0', 'pendingReboot': true, 'checkedAt': 0};
    snapshot['ca'] = ca;
    await open(tester, CaptureCertificatePage(initial: ca, active: false));
    expect(find.text('Not checked'), findsOneWidget);
    expect(find.text('Certificate change pending; reboot and verify again.'), findsOneWidget);
    await tester.tap(find.text('Export public CA'));
    await tester.pumpAndSettle();
    expect(calls.where((call) => call.method == 'capture.ca.export'), hasLength(1));
    expect(tester.takeException(), isNull);
  });

  for (final (size, scale) in [(const Size(360, 800), 1.4), (const Size(800, 1000), 1.0)]) {
    testWidgets('request list and detail fit $size at $scale', (tester) async {
      await mount(tester, size: size, scale: scale);
      snapshot['page'] = {'items': [{'id': 'long', 'url': 'https://example.test/${'long-path/' * 30}',
        'method': 'POST', 'status': 200, 'protocol': 'HTTP/2.0', 'packages': ['com.example.long.app.name'], 'bytes': 65536, 'durationMs': 120}], 'total': 1};
      await open(tester, const RequestsPage());
      expect(tester.takeException(), isNull);
      extra = (call) async => call.method == 'capture.detail' ? {
        'id': 'long', 'startedAt': 1700000000000, 'durationMs': 120,
        'request': {'url': 'https://example.test/', 'method': 'POST', 'protocol': 'HTTP/2.0', 'bodyComplete': true, 'bodyTruncated': true},
        'response': {'status': 200, 'headers': [{'name': 'X-Long', 'value': 'a' * 300}], 'capturedBytes': 64, 'bodyBytes': 999, 'bodyTruncated': true, 'bodyComplete': false},
        'responsePreview': {'text': 'data: live\n', 'encoding': 'text', 'truncated': true},
        'metadata': {'outbound': 'selected-node', 'rule': 'selected-rule', 'packages': ['com.example']},
      } : snapshot;
      await open(tester, const RequestDetailPage(id: 'long'));
      final scaffold = tester.widget<CommonScaffold>(find.descendant(of: find.byType(RequestDetailPage), matching: find.byType(CommonScaffold)).first);
      expect(scaffold.iconActions.last.onPressed, isNull, reason: 'A truncated request must not be offered as replayable cURL.');
      expect(tester.takeException(), isNull);
    });
  }
}
