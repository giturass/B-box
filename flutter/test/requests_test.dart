import 'dart:async';
import 'dart:io';
import 'dart:typed_data';
import 'dart:ui' as ui;
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/pages/requests.dart';
import 'package:b_box_ui/pages/request_settings.dart';
import 'package:b_box_ui/pages/capture_certificate.dart';
import 'package:b_box_ui/components/captured_body.dart';
import 'package:flutter/services.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  late BoxController box;
  late StreamController<dynamic> events;
  late List<MethodCall> calls;
  late List<String> copied;
  late Json snapshot;
  late GlobalKey boundaryKey;
  Future<dynamic> Function(MethodCall)? extra;

  setUpAll(() async {
    final root = Platform.environment['FLUTTER_ROOT'];
    if (root != null) {
      final font = File('$root/bin/cache/artifacts/material_fonts/Roboto-Regular.ttf');
      if (font.existsSync()) await (FontLoader('Roboto')..addFont(Future.value(ByteData.sublistView(await font.readAsBytes())))).load();
    }
  });

  Future<void> capture(WidgetTester tester, String label) async {
    await tester.runAsync(() async {
      final boundary = boundaryKey.currentContext!.findRenderObject()! as RenderRepaintBoundary;
      final bitmap = await boundary.toImage();
      final data = await bitmap.toByteData(format: ui.ImageByteFormat.png);
      final file = File('build/screenshots/requests/$label.png');
      await file.parent.create(recursive: true);
      await file.writeAsBytes(data!.buffer.asUint8List());
      bitmap.dispose();
    });
  }

  Future<void> mount(WidgetTester tester, {bool remote = false, bool dark = false, double scale = 1, Size size = const Size(360, 900)}) async {
    tester.binding.platformDispatcher.localesTestValue = const [Locale('en')];
    addTearDown(tester.binding.platformDispatcher.clearLocalesTestValue);
    tester.view.physicalSize = size;
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    calls = [];
    copied = [];
    snapshot = {'state': {'active': false}, 'config': {}, 'ca': {'exists': false}, 'page': {'items': [], 'total': 0, 'storedBytes': 0}};
    const channel = MethodChannel('test.requests');
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(SystemChannels.platform, (call) async {
      if (call.method == 'Clipboard.setData') copied.add(json(call.arguments)['text'] as String);
      return null;
    });
    messenger.setMockMethodCallHandler(channel, (call) async {
      calls.add(call);
      if (call.method == 'ready') return {
        'version': 'test', 'coreVersion': 'test', 'preferences': {'fontScale': scale, 'theme': dark ? 'dark' : 'light'},
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
    boundaryKey = GlobalKey();
    await tester.pumpWidget(RepaintBoundary(key: boundaryKey, child: BoxApp(controller: box)));
    await tester.pumpAndSettle();
    addTearDown(() async {
      extra = null;
      await tester.pumpWidget(const SizedBox());
      await events.close();
      messenger.setMockMethodCallHandler(channel, null);
      messenger.setMockMethodCallHandler(SystemChannels.platform, null);
    });
  }

  Future<void> open(WidgetTester tester, Widget page) async {
    openPage(tester.element(find.byType(HomePage)), page);
    await tester.pumpAndSettle();
  }

  Future<void> showDetailItem(WidgetTester tester, Finder finder) async {
    final scrollable = find.descendant(of: find.byType(RequestDetailPage), matching: find.byType(Scrollable)).first;
    if (finder.evaluate().isEmpty) {
      tester.state<ScrollableState>(scrollable).position.jumpTo(0);
      await tester.pumpAndSettle();
    }
    await tester.scrollUntilVisible(finder, 240, scrollable: scrollable);
    await tester.pumpAndSettle();
  }

  Future<void> chooseMessage(WidgetTester tester, String part) async {
    await tester.tap(find.byTooltip('More').last);
    await tester.pumpAndSettle();
    expect(find.text('Message type'), findsOneWidget);
    await tester.tap(find.text(part).last);
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

  testWidgets('message menu switches headers and cookies, with copy feedback', (tester) async {
    await mount(tester);
    const responseCookie = 'session=server-one; Expires=Wed, 09 Jun 2027 10:18:14 GMT; Path=/; HttpOnly';
    extra = (call) async => call.method == 'capture.detail' ? {
      'id': 'copy', 'startedAt': 1700000000000, 'durationMs': 12,
      'request': {'url': 'https://example.test/path', 'method': 'POST', 'protocol': 'HTTP/2.0', 'bodyComplete': true,
        'headers': [{'name': 'X-Request', 'value': 'original==value'}, {'name': 'Cookie', 'value': 'client=client-token; empty='}]},
      'response': {'status': 200, 'bodyComplete': true, 'headers': [
        {'name': 'X-Response', 'value': 'response-value'},
        {'name': 'Set-Cookie', 'value': responseCookie},
        {'name': 'Set-Cookie', 'value': 'session=server-two; Path=/api'},
      ]},
      'responsePreview': {'text': '{"ok":true}', 'encoding': 'text'}, 'metadata': {},
    } : snapshot;
    await open(tester, const RequestDetailPage(id: 'copy'));
    await tester.tap(find.text('https://example.test/path'));
    await tester.pumpAndSettle();
    expect(copied.last, 'https://example.test/path');
    expect(find.text('Copied'), findsOneWidget);
    await chooseMessage(tester, 'Request');
    await showDetailItem(tester, find.text('X-Request'));
    await tester.tap(find.text('X-Request'));
    await tester.pumpAndSettle();
    expect(copied.last, 'original==value');
    await chooseMessage(tester, 'Cookies');
    await showDetailItem(tester, find.text('client-token'));
    await tester.tap(find.text('client-token'));
    await tester.pumpAndSettle();
    expect(copied.last, 'client=client-token');
    await showDetailItem(tester, find.text('server-one'));
    await tester.tap(find.text('server-one'));
    await tester.pumpAndSettle();
    expect(copied.last, responseCookie);
    expect(find.text('Expires=Wed, 09 Jun 2027 10:18:14 GMT\nPath=/\nHttpOnly'), findsOneWidget);
    await showDetailItem(tester, find.text('server-two'));
    expect(find.text('server-two'), findsOneWidget);
    await chooseMessage(tester, 'Response');
    await showDetailItem(tester, find.text('X-Response'));
    expect(find.text('X-Response'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('formatted bodies copy the original preview and retain the chosen view on updates', (tester) async {
    await mount(tester);
    const raw = '{"id":123456789012345678901234567890,"ok":true}';
    final data = ValueNotifier<Json>({'text': raw, 'encoding': 'text'});
    addTearDown(data.dispose);
    await open(tester, BoxPage(title: 'Request details', children: [ValueListenableBuilder<Json>(valueListenable: data,
      builder: (context, value, child) => CapturedBody(message: {'bodyComplete': true, 'capturedBytes': 128, 'bodyBytes': 128,
        'headers': [{'name': 'Content-Type', 'value': 'application/json'}]}, preview: value))]));
    String body() => tester.widget<SelectableText>(find.byKey(const ValueKey('captured-body-text'))).data!;
    expect(body(), contains('\n  "id": 123456789012345678901234567890,'));
    await tester.tap(find.byTooltip('Copy body preview'));
    await tester.pumpAndSettle();
    expect(copied.last, raw);
    await tester.tap(find.byTooltip('Show original'));
    await tester.pumpAndSettle();
    expect(body(), raw);
    data.value = {'text': '{"updated":true}', 'encoding': 'text'};
    await tester.pumpAndSettle();
    expect(body(), '{"updated":true}');
    await tester.tap(find.byTooltip('Format JSON'));
    await tester.pumpAndSettle();
    expect(body(), '{\n  "updated": true\n}');
    data.value = {'text': '{"partial":', 'encoding': 'text', 'truncated': true};
    await tester.pumpAndSettle();
    expect(body(), '{"partial":');
    expect(find.byTooltip('Show original'), findsNothing);
    expect(find.text('Only a limited body preview was saved or decoded.'), findsOneWidget);
    data.value = {'text': 'AAECAw==', 'encoding': 'base64'};
    await tester.pumpAndSettle();
    expect(body(), 'AAECAw==');
    expect(find.textContaining('Base64'), findsOneWidget);
    expect(find.byTooltip('Format JSON'), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('body expansion keeps Unicode intact and copies beyond the visible prefix', (tester) async {
    await mount(tester);
    final text = '${'a' * 16383}😀${'b' * 100}';
    await open(tester, BoxPage(title: 'Request details', children: [CapturedBody(
      message: const {'bodyComplete': false, 'bodyTruncated': true},
      preview: {'text': text, 'encoding': 'text', 'truncated': true},
    )]));
    String body() => tester.widget<SelectableText>(find.byKey(const ValueKey('captured-body-text'))).data!;
    expect(body(), 'a' * 16383);
    expect(find.text('Body stream is incomplete'), findsOneWidget);
    await tester.tap(find.byTooltip('Copy body preview'));
    await tester.pumpAndSettle();
    expect(copied.last, text);
    await tester.ensureVisible(find.text('Show more'));
    await tester.tap(find.text('Show more'));
    await tester.pumpAndSettle();
    expect(body(), text);
    await tester.ensureVisible(find.text('Show less'));
    await tester.tap(find.text('Show less'));
    await tester.pumpAndSettle();
    expect(body(), 'a' * 16383);
    expect(tester.takeException(), isNull);
  });

  testWidgets('polling stops in the background and late replies do not replace filters', (tester) async {
    await mount(tester);
    await open(tester, const RequestsPage());
    final firstCount = calls.where((call) => call.method == 'capture.snapshot').length;
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    await tester.pump(const Duration(seconds: 8));
    expect(calls.where((call) => call.method == 'capture.snapshot'), hasLength(firstCount));
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
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

  for (final action in ['install', 'status']) {
    testWidgets('CA $action refreshes saved state after a CertBridge error', (tester) async {
      await mount(tester);
      final ca = {'exists': true, 'fingerprint': 'ab' * 32, 'expiresAt': 2000000000000,
        'filename': action == 'install' ? '' : '1234abcd.0', 'pendingReboot': false, 'checkedAt': 0};
      snapshot['ca'] = ca;
      await open(tester, CaptureCertificatePage(initial: ca, active: false));
      extra = (call) async {
        if (call.method == 'capture.ca.$action') {
          snapshot['ca'] = {...ca, 'filename': '1234abcd.0', 'pendingReboot': true,
            if (action == 'status') ...{'trusted': false, 'checkedAt': 1700000000000}};
          throw PlatformException(code: 'capture', message: 'Invalid certificate fingerprint');
        }
        if (call.method == 'capture.ca.snapshot') return snapshot;
        return null;
      };
      final button = find.text(action == 'install' ? 'Import with CertBridge' : 'Check system trust');
      await tester.ensureVisible(button);
      await tester.tap(button);
      await tester.pumpAndSettle();
      await tester.scrollUntilVisible(find.text('Invalid certificate fingerprint'), -200,
        scrollable: find.descendant(of: find.byType(CaptureCertificatePage), matching: find.byType(Scrollable)).first);
      await tester.pumpAndSettle();
      expect(find.text('Invalid certificate fingerprint'), findsOneWidget);
      expect(find.text('Imported'), findsOneWidget);
      expect(find.text('Certificate change pending; reboot and verify again.'), findsOneWidget);
      expect(find.text(action == 'status' ? 'Not trusted' : 'Not checked'), findsOneWidget);
      expect(calls.where((call) => call.method == 'capture.ca.snapshot'), hasLength(1));
      expect(tester.takeException(), isNull);
    });
  }

  for (final (size, scale, dark) in [(const Size(360, 800), 1.0, false), (const Size(360, 800), 1.4, true), (const Size(800, 1000), 1.0, false)]) {
    testWidgets('request settings, list and message views fit $size at $scale dark=$dark', (tester) async {
      await mount(tester, size: size, scale: scale, dark: dark);
      final label = '${size.width.toInt()}-$scale-${dark ? 'dark' : 'light'}';
      await open(tester, const RequestSettingsPage(initial: {
        'https': true, 'decryptHosts': ['*.example.test', 'api.example.test'],
        'includeHosts': ['*.example.test'], 'excludeHosts': ['private.example.test'],
      }));
      await tester.enterText(find.byKey(const ValueKey('HTTPS domains')), '*.example.test\napi.example.test');
      await tester.pumpAndSettle();
      await capture(tester, '$label-settings');
      final ports = find.byKey(const ValueKey('Capture ports'));
      await tester.scrollUntilVisible(ports, 250,
        scrollable: find.descendant(of: find.byType(RequestSettingsPage), matching: find.byType(Scrollable)).first);
      await tester.enterText(ports, '80, 443, 8443');
      await tester.pumpAndSettle();
      await capture(tester, '$label-settings-ports');
      await tester.tap(find.byTooltip('Save').last);
      await tester.pumpAndSettle();
      final saved = json(json(calls.singleWhere((call) => call.method == 'capture.configure').arguments)['config']);
      expect(saved['decryptHosts'], ['*.example.test', 'api.example.test']);
      expect(saved['ports'], [80, 443, 8443]);
      expect(saved['excludeHosts'], ['private.example.test']);
      expect(tester.takeException(), isNull);
      snapshot['page'] = {'items': [{'id': 'long', 'url': 'https://example.test/${'long-path/' * 30}',
        'method': 'POST', 'status': 200, 'protocol': 'HTTP/2.0', 'packages': ['com.example.long.app.name'], 'bytes': 65536, 'durationMs': 120}], 'total': 1};
      await open(tester, const RequestsPage());
      expect(tester.takeException(), isNull);
      expect(find.textContaining('https://example.test/'), findsOneWidget);
      await capture(tester, '$label-list');
      extra = (call) async => call.method == 'capture.detail' ? {
        'id': 'long', 'startedAt': 1700000000000, 'durationMs': 120,
        'request': {'url': 'https://example.test/', 'method': 'POST', 'protocol': 'HTTP/2.0', 'bodyComplete': true, 'bodyTruncated': true,
          'headers': [{'name': 'Cookie', 'value': 'session=client-token; theme=dark'}]},
        'response': {'status': 200, 'headers': [
          {'name': 'Content-Type', 'value': 'text/event-stream; charset=utf-8'},
          {'name': 'X-${'Long-' * 20}Header', 'value': 'a' * 300},
          {'name': 'Set-Cookie', 'value': 'session=server-token; Expires=Wed, 09 Jun 2027 10:18:14 GMT; Path=/; HttpOnly; Secure'},
          {'name': 'Set-Cookie', 'value': 'session=api-token; Path=/api'},
        ], 'capturedBytes': 64, 'bodyBytes': 999, 'bodyTruncated': true, 'bodyComplete': false},
        'responsePreview': {'text': 'data: {"message":"${'long text ' * 30}"}\n\n' * 4, 'encoding': 'text', 'truncated': true},
        'metadata': {'outbound': 'selected-node', 'rule': 'selected-rule', 'packages': ['com.example'], 'originalDestination': '[2001:db8::1234]:443'},
      } : snapshot;
      await tester.tap(find.textContaining('https://example.test/'));
      await tester.pumpAndSettle();
      final scaffold = tester.widget<CommonScaffold>(find.descendant(of: find.byType(RequestDetailPage), matching: find.byType(CommonScaffold)).first);
      expect(scaffold.iconActions.last.onPressed, isNull, reason: 'A truncated request must not be offered as replayable cURL.');
      expect(tester.takeException(), isNull);
      await capture(tester, '$label-details');
      await showDetailItem(tester, find.text('Response headers'));
      await capture(tester, '$label-headers');
      await showDetailItem(tester, find.text('Body'));
      expect(find.text('Only a limited body preview was saved or decoded.'), findsOneWidget);
      expect(find.text('Body stream is incomplete'), findsOneWidget);
      await capture(tester, '$label-body');
      await tester.tap(find.byTooltip('More').last);
      await tester.pumpAndSettle();
      await capture(tester, '$label-message-menu');
      await tester.tap(find.text('Cookies').last);
      await tester.pumpAndSettle();
      await showDetailItem(tester, find.text('Request cookies'));
      await capture(tester, '$label-cookies');
      await showDetailItem(tester, find.text('api-token'));
      expect(tester.takeException(), isNull);
    });
  }
}
