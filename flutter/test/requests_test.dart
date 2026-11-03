import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';
import 'dart:ui' as ui;
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/pages/requests.dart';
import 'package:b_box_ui/pages/request_settings.dart';
import 'package:b_box_ui/pages/mitm_certificate.dart';
import 'package:b_box_ui/pages/capture_domains.dart';
import 'package:b_box_ui/pages/configuration.dart';
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
  late Json caState;
  late GlobalKey boundaryKey;
  Future<dynamic> Function(MethodCall)? extra;

  setUpAll(() async {
    final mono = await File('assets/fonts/JetBrainsMono-Regular.ttf').readAsBytes();
    await (FontLoader('JetBrainsMono')..addFont(Future.value(ByteData.sublistView(mono)))).load();
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
    snapshot = {'state': {'active': false}, 'config': {}, 'page': {'items': [], 'total': 0, 'storedBytes': 0}};
    caState = {'exists': false, 'revision': 'ca-empty'};
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
      if (call.method == 'capture.ca.snapshot') return caState;
      if (call.method == 'capture.ca.generate') {
        caState = {'exists': true, 'revision': 'ca-created', 'subject': 'CN=B-box MITM test', 'fingerprint': 'ab' * 32,
          'expiresAt': 1893456000000, 'valid': true, 'keyAvailable': true, 'systemTrusted': false, 'userTrusted': false};
        return caState;
      }
      if (call.method == 'capture.ca.export' || call.method == 'capture.ca.module') return false;
      if (call.method == 'capture.ca.install') return true;
      if (call.method == 'capture.snapshot') return snapshot;
      if (call.method == 'capture.settings') return {'revision': 'saved-revision', 'config': snapshot['config']};
      if (call.method == 'capture.domains') return ['api.test', 'pinned.test'];
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

  testWidgets('Requests only shows recording state without MITM or capture logs in its menu', (tester) async {
    await mount(tester);
    snapshot['state'] = {'active': false, 'rewriteActive': true, 'rewriteRules': 3};
    await open(tester, const RequestsPage());
    expect(find.text('Profile HTTP rewriting is active'), findsNothing);
    expect(find.text('Capture stopped'), findsOneWidget);
    await tester.tap(find.byTooltip('More').last);
    await tester.pumpAndSettle();
    expect(find.text('MITM'), findsNothing);
    expect(find.text('Capture logs'), findsNothing);
    Navigator.pop(tester.element(find.text('Export HAR').last));
    await tester.pumpAndSettle();
    expect(calls.where((call) => call.method == 'capture.start'), isEmpty);
    await capture(tester, 'requests-recording-off');
    expect(tester.takeException(), isNull);
  });

  testWidgets('profile rewrite configuration stays readable on a narrow screen with larger text', (tester) async {
    await mount(tester, dark: true, scale: 1.3, size: const Size(320, 900));
    extra = (call) async => call.method == 'profiles.config' ? {
      'formatted': r'{"experimental":{"http_rewrite":{"enabled":true,"map_local":[{"pattern":"/mock$","data":"{}"}]}}}',
      'revision': 'rewrite-screenshot',
    } : null;
    final store = ProfileConfigStore(box, {'id': 7, 'name': 'Profile'});
    addTearDown(store.dispose);
    await store.load();
    await open(tester, HttpRewriteConfigPage(store: store));
    final list = find.descendant(of: find.byType(HttpRewriteConfigPage), matching: find.byType(ListView)).first;
    final title = find.text('HTTP rewriting');
    expect(tester.getTopLeft(list).dy, greaterThanOrEqualTo(tester.getBottomLeft(title).dy));
    await capture(tester, 'profile-rewriting-config');
    final scrollable = find.descendant(of: find.byType(HttpRewriteConfigPage), matching: find.byType(Scrollable)).first;
    await tester.scrollUntilVisible(find.text('Edit HTTP rewrite options'), 260, scrollable: scrollable);
    await tester.pumpAndSettle();
    expect(tester.getTopLeft(list).dy, greaterThanOrEqualTo(tester.getBottomLeft(title).dy));
    await capture(tester, 'profile-rewriting-config-resources');
    expect(tester.takeException(), isNull);
  });

  testWidgets('independent MITM and cron configuration fit a narrow screen', (tester) async {
    await mount(tester, dark: true, scale: 1.3, size: const Size(320, 900));
    extra = (call) async => call.method == 'profiles.config' ? {
      'formatted': r'{"experimental":{"mitm":{"enabled":false,"hosts":[]},"scripts":{"enabled":false,"cron":[{"name":"Daily task","cron":"0 2 * * *","source":"$done();"}]},"http_rewrite":{"enabled":false}}}',
      'revision': 'independent-screenshot',
    } : null;
    final store = ProfileConfigStore(box, {'id': 7, 'name': 'Profile'});
    addTearDown(store.dispose);
    await store.load();
    await open(tester, MitmConfigPage(store: store));
    expect(find.text('No domains selected'), findsOneWidget);
    expect(find.text('HTTPS excluded domains'), findsNothing);
    await capture(tester, 'independent-mitm-config');
    Navigator.pop(tester.element(find.byType(MitmConfigPage))); await tester.pumpAndSettle();
    await open(tester, ScriptsConfigPage(store: store));
    await capture(tester, 'independent-scripts-cron');
    final list = find.descendant(of: find.byType(ScriptsConfigPage), matching: find.byType(ListView)).first;
    expect(tester.getTopLeft(list).dy, greaterThanOrEqualTo(tester.getBottomLeft(find.text('Scripts')).dy));
    expect(find.byKey(const ValueKey('cron-scripts')), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('a profile using MITM prevents certificate rotation while recording is off', (tester) async {
    await mount(tester);
    caState = {'exists': false, 'revision': 'ca-empty', 'captureActive': false, 'mitmActive': true};
    await open(tester, const MitmCertificatePage());
    expect(find.text('Stop capture and the local service before changing the MITM certificate'), findsOneWidget);
    await tester.tap(find.byKey(const ValueKey('mitm-ca-generate')));
    await tester.pumpAndSettle();
    expect(calls.where((call) => call.method == 'capture.ca.generate'), isEmpty);
  });

  testWidgets('capture settings cannot enable decryption and drop old HTTPS fields', (tester) async {
    await mount(tester);
    await open(tester, const RequestSettingsPage(initial: {
      'https': true, 'decryptHosts': [], 'excludeHosts': ['private.example'], 'includeHosts': ['*.example'], 'packages': ['com.example'],
      'decryptBypassHosts': ['private.example'],
    }));
    expect(find.text('Decrypt HTTPS'), findsNothing);
    expect(find.text('HTTPS excluded domains'), findsNothing);
    expect(find.text('MITM'), findsOneWidget);
    await tester.tap(find.byTooltip('Save').last);
    await tester.pumpAndSettle();
    final config = json(json(calls.singleWhere((call) => call.method == 'capture.configure').arguments)['config']);
    expect(config.containsKey('https'), isFalse);
    expect(config.containsKey('decryptHosts'), isFalse);
    expect(config.containsKey('excludeHosts'), isFalse);
    expect(config.containsKey('includeHosts'), isFalse);
    expect(config.containsKey('decryptBypassHosts'), isFalse);
    expect(config['packages'], ['com.example']);
    expect(config['bodyLimit'], 65536);
    expect(tester.takeException(), isNull);
  });

  testWidgets('capture settings use a local revision and save only capture fields', (tester) async {
    await mount(tester);
    await open(tester, const RequestSettingsPage(initial: {
      'https': true, 'tlsLibraries': ['/data/app/test/libssl.so'], 'extractHosts': true,
    }, source: {'revision': 'local-revision'}));
    await tester.tap(find.byTooltip('Save').last);
    await tester.pumpAndSettle();
    final args = json(calls.singleWhere((call) => call.method == 'capture.configure').arguments);
    expect(args['revision'], 'local-revision');
    expect(args.containsKey('profileId'), isFalse);
    final saved = json(args['config']);
    expect(saved.containsKey('tlsLibraries'), isFalse);
    expect(saved['extractHosts'], isTrue);
    expect(saved.keys.toSet(), {'version', 'packages', 'ports', 'bodyLimit', 'extractHosts'});
    expect(saved['version'], 3);
    expect(saved.containsKey('blockQUIC'), isFalse);
    expect(saved.containsKey('tlsOffsets'), isFalse);
    expect(calls.where((call) => call.method == 'capture.start'), isEmpty);
  });

  testWidgets('HAR export uses the current request filter', (tester) async {
    await mount(tester);
    snapshot['page'] = {'items': [{'id': 'saved', 'url': 'https://api.test/request',
      'method': 'GET', 'protocol': 'HTTP/1.1', 'status': 200, 'complete': true}], 'total': 1, 'storedBytes': 0};
    await open(tester, const RequestsPage());
    final scaffold = tester.widget<CommonScaffold>(find.descendant(of: find.byType(RequestsPage), matching: find.byType(CommonScaffold)).first);
    scaffold.onKeywordsUpdate!(['api.test']); await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('More').last); await tester.pumpAndSettle();
    await tester.tap(find.text('Export HAR').last); await tester.pumpAndSettle();
    final exports = calls.where((call) => call.method == 'capture.export').map((call) => json(call.arguments)).toList();
    expect(exports.single, {'format': 'har', 'query': 'api.test'});
    expect(find.text('Capture statistics'), findsNothing);
    expect(tester.takeException(), isNull);
  });


  testWidgets('request detail copies observed socket endpoints and retains FD zero', (tester) async {
    await mount(tester);
    extra = (call) async => call.method == 'capture.detail' ? {
      'id': 'socket', 'request': {'url': 'https://api.test/', 'method': 'GET', 'protocol': 'HTTP/2.0'},
      'metadata': {'captureSource': 'eBPF', 'tlsAdapter': 'Go TLS', 'pid': 42, 'uid': 10001,
        'source': '[::1]:1234', 'destination': '[::1]:443', 'outbound': '', 'rule': '',
        'socket': {'fd': 0, 'inode': 'abc', 'netns': 1}},
    } : snapshot;
    await open(tester, const RequestDetailPage(id: 'socket'));
    await showDetailItem(tester, find.text('Socket FD'));
    expect(find.text('Go TLS'), findsOneWidget);
    await showDetailItem(tester, find.text('[::1]:443'));
    await tester.tap(find.text('[::1]:443')); await tester.pumpAndSettle();
    expect(copied.last, '[::1]:443');
    await capture(tester, 'socket-metadata');
    expect(tester.takeException(), isNull);
  });

  testWidgets('MITM domains are saved in the profile and can be selected from captured names', (tester) async {
    await mount(tester);
    var content = jsonEncode({'experimental': {'mitm': {'hosts': ['existing.test']}, 'http_rewrite': {'enabled': false}}});
    extra = (call) async {
      if (call.method == 'profiles.config') return {'formatted': content, 'revision': 'before'};
      if (call.method == 'capture.domains') return ['api.test', 'pinned.test'];
      if (call.method == 'profiles.write') {
        expect(json(call.arguments)['revision'], 'before');
        content = json(call.arguments)['content'] as String;
        return {'formatted': content, 'revision': 'after'};
      }
      return null;
    };
    final store = ProfileConfigStore(box, {'id': 7, 'name': 'Profile'});
    addTearDown(store.dispose);
    await store.load();
    await open(tester, MitmHostsEditor(store: store));
    await tester.tap(find.text('Add captured domains')); await tester.pumpAndSettle();
    expect(find.byType(CaptureDomainsPage), findsOneWidget);
    await tester.tap(find.text('pinned.test')); await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Add domains').last); await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Save').last); await tester.pumpAndSettle();
    expect(store.value(['experimental', 'mitm', 'hosts']), ['existing.test', 'pinned.test']);
    expect(store.value(['experimental', 'http_rewrite', 'enabled']), isFalse);
    expect(calls.where((call) => call.method == 'capture.configure'), isEmpty);
  });

  testWidgets('domain extraction uses the search filter and copies complete hostnames', (tester) async {
    await mount(tester);
    await open(tester, const CaptureDomainsPage(query: 'pinned'));
    expect(json(calls.singleWhere((call) => call.method == 'capture.domains').arguments)['query'], 'pinned');
    await tester.tap(find.byTooltip('Copy domains').last); await tester.pumpAndSettle();
    expect(copied.last, 'api.test\npinned.test');
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




  testWidgets('TCP records show stream completion and disable HTTP exports and menus', (tester) async {
    await mount(tester);
    snapshot['page'] = {'items': [{'id': 'tcp', 'url': 'tls://stream.test:8883', 'method': 'TCP',
      'protocol': 'TLS/TCP', 'complete': true, 'bytes': 6}], 'total': 1};
    await open(tester, const RequestsPage());
    expect(find.text('Complete'), findsOneWidget);
    expect(find.text('Error'), findsNothing);
    extra = (call) async => call.method == 'capture.detail' ? {
      'id': 'tcp', 'metadata': {'rawTCP': true, 'host': 'stream.test', 'port': 8883},
      'request': {'url': 'tls://stream.test:8883', 'method': 'TCP', 'protocol': 'TLS/TCP', 'bodyComplete': true},
      'response': {'status': 0, 'bodyComplete': true, 'bodyBytes': 6, 'capturedBytes': 6},
      'responsePreview': {'encoding': 'base64', 'text': 'AAECAwQF'},
    } : null;
    await tester.tap(find.text('tls://stream.test:8883'));
    await tester.pumpAndSettle();
    final scaffold = tester.widget<CommonScaffold>(find.descendant(of: find.byType(RequestDetailPage), matching: find.byType(CommonScaffold)).first);
    expect(scaffold.iconActions.last.onPressed, isNull);
    await tester.tap(find.byTooltip('More').last);
    await tester.pumpAndSettle();
    expect(find.text('Cookies'), findsNothing);
    expect(find.text('Request'), findsOneWidget);
    expect(find.text('Response'), findsOneWidget);
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
    await capture(tester, 'json-formatted');
    await tester.tap(find.byTooltip('Copy body preview'));
    await tester.pumpAndSettle();
    expect(copied.last, raw);
    await tester.tap(find.byTooltip('Show original'));
    await tester.pumpAndSettle();
    expect(body(), raw);
    await capture(tester, 'json-original');
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

  for (final (size, scale, dark) in [(const Size(360, 800), 1.0, false), (const Size(360, 800), 1.4, true), (const Size(800, 1000), 1.0, false)]) {
    testWidgets('request settings, list and message views fit $size at $scale dark=$dark', (tester) async {
      await mount(tester, size: size, scale: scale, dark: dark);
      final label = '${size.width.toInt()}-$scale-${dark ? 'dark' : 'light'}';
      await open(tester, const RequestSettingsPage(initial: {
        'https': true, 'decryptHosts': ['*.example.test', 'api.example.test'],
        'decryptBypassHosts': ['private.example.test'],
      }));
      expect(find.text('Decrypt HTTPS'), findsNothing);
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
      expect(saved.containsKey('decryptHosts'), isFalse);
      expect(saved['ports'], [80, 443, 8443]);
      expect(saved.containsKey('decryptBypassHosts'), isFalse);
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
  testWidgets('MITM CA is generated explicitly and cancelled exports never imply trust', (tester) async {
    await mount(tester);
    await open(tester, const MitmCertificatePage());
    expect(calls.where((call) => call.method == 'capture.ca.generate'), isEmpty);
    expect(find.text('No MITM certificate'), findsOneWidget);
    await tester.tap(find.byKey(const ValueKey('mitm-ca-generate'))); await tester.pumpAndSettle();
    expect(json(calls.singleWhere((call) => call.method == 'capture.ca.generate').arguments)['revision'], 'ca-empty');
    expect(find.text('CA not installed'), findsOneWidget);
    final export = find.byKey(const ValueKey('mitm-ca-export'));
    await tester.scrollUntilVisible(export, 200, scrollable: find.descendant(of: find.byType(MitmCertificatePage), matching: find.byType(Scrollable)).first);
    await tester.tap(export); await tester.pumpAndSettle();
    expect(find.text('Public CA certificate exported'), findsNothing);
    expect(caState['systemTrusted'], isFalse);
    expect(caState['userTrusted'], isFalse);
    expect(calls.where((call) => call.method == 'capture.start'), isEmpty);
    await tester.tap(find.byTooltip('Refresh trust')); await tester.pumpAndSettle();
    expect(calls.where((call) => call.method == 'capture.ca.snapshot'), hasLength(2));
    expect(tester.takeException(), isNull);
  });

  testWidgets('MITM CA actions respect active capture and remote sessions', (tester) async {
    await mount(tester);
    caState = {'exists': true, 'revision': 'active-ca', 'subject': 'CN=B-box MITM test', 'fingerprint': 'cd' * 32,
      'valid': true, 'keyAvailable': true, 'systemTrusted': false, 'userTrusted': true, 'captureActive': true};
    await open(tester, const MitmCertificatePage());
    expect(find.text('Present in user CA store'), findsOneWidget);
    final generate = find.byKey(const ValueKey('mitm-ca-generate'));
    await tester.scrollUntilVisible(generate, 200, scrollable: find.descendant(of: find.byType(MitmCertificatePage), matching: find.byType(Scrollable)).first);
    await tester.tap(generate); await tester.pumpAndSettle();
    expect(calls.where((call) => call.method == 'capture.ca.generate'), isEmpty);
    events.add({'type': 'dashboard', 'value': {'status': 'Started', 'remote': 'Server', 'remoteConnected': true}});
    await tester.pumpAndSettle();
    expect(find.text('HTTP debugging is only available for the local service'), findsOneWidget);
    final count = calls.where((call) => call.method.startsWith('capture.ca.')).length;
    await tester.pump(const Duration(seconds: 5));
    expect(calls.where((call) => call.method.startsWith('capture.ca.')), hasLength(count));
    caState = {...caState, 'systemTrusted': true, 'captureActive': false};
    events.add({'type': 'dashboard', 'value': {'status': 'Started'}});
    await tester.pumpAndSettle();
    expect(find.text('Present in system CA store'), findsOneWidget);
    expect(calls.where((call) => call.method.startsWith('capture.ca.')), hasLength(count + 1));
    expect(tester.takeException(), isNull);
  });

  testWidgets('MITM CA ignores late snapshots from a previous local session', (tester) async {
    await mount(tester);
    final delayed = Completer<Json>();
    extra = (call) async => call.method == 'capture.ca.snapshot' ? delayed.future : null;
    openPage(tester.element(find.byType(HomePage)), const MitmCertificatePage());
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 500));
    events.add({'type': 'dashboard', 'value': {'status': 'Started', 'remote': 'Server'}});
    await tester.pumpAndSettle();
    extra = null;
    events.add({'type': 'dashboard', 'value': {'status': 'Started'}});
    await tester.pumpAndSettle();
    expect(find.text('No MITM certificate'), findsOneWidget);
    delayed.complete({'exists': true, 'revision': 'stale', 'systemTrusted': true});
    await tester.pumpAndSettle();
    expect(find.text('No MITM certificate'), findsOneWidget);
    expect(find.text('Present in system CA store'), findsNothing);
  });

  for (final dark in [false, true]) {
    testWidgets('MITM certificate layout fits narrow screens dark=$dark', (tester) async {
      await mount(tester, dark: dark, scale: dark ? 1.5 : 1, size: const Size(360, 900));
      caState = {'exists': true, 'revision': 'layout-ca', 'subject': 'CN=B-box MITM 0123456789abcdef', 'fingerprint': 'ab' * 32,
        'expiresAt': 1893456000000, 'valid': true, 'keyAvailable': true, 'systemTrusted': false, 'userTrusted': false};
      await open(tester, const MitmCertificatePage());
      await capture(tester, 'mitm-certificate-${dark ? 'dark-large' : 'light'}');
      final module = find.byKey(const ValueKey('mitm-ca-module'));
      await tester.scrollUntilVisible(module, 200, scrollable: find.descendant(of: find.byType(MitmCertificatePage), matching: find.byType(Scrollable)).first);
      await tester.pumpAndSettle();
      await capture(tester, 'mitm-certificate-export-${dark ? 'dark-large' : 'light'}');
      expect(tester.takeException(), isNull);
    });
  }

}
