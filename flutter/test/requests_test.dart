import 'dart:async';
import 'dart:io';
import 'dart:typed_data';
import 'dart:ui' as ui;
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/pages/requests.dart';
import 'package:b_box_ui/pages/request_settings.dart';
import 'package:b_box_ui/pages/tls_offsets.dart';
import 'package:b_box_ui/pages/capture_domains.dart';
import 'package:b_box_ui/pages/capture_logs.dart';
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
      if (call.method == 'capture.snapshot') return snapshot;
      if (call.method == 'capture.logs') return {'state': snapshot['state'], 'logs': snapshot['logs'] ?? []};
      if (call.method == 'capture.logs.clear') { snapshot['logs'] = []; return null; }
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

  testWidgets('empty HTTPS list is saved as unrestricted and filters survive', (tester) async {
    await mount(tester);
    await open(tester, const RequestSettingsPage(initial: {
      'https': true, 'decryptHosts': [], 'excludeHosts': ['private.example'], 'includeHosts': ['*.example'], 'packages': ['com.example'],
    }));
    expect(find.text('Leave empty to capture all supported HTTPS domains. Exact names and *.example.com are accepted; exclusions take priority.'), findsOneWidget);
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
    expect(saved['tlsLibraries'], ['/data/app/test/libssl.so']);
    expect(saved['extractHosts'], isTrue);
    expect(saved.keys.toSet(), {'https', 'decryptHosts', 'includeHosts', 'excludeHosts', 'packages',
      'ports', 'bodyLimit', 'decryptBypassHosts', 'extractHosts', 'tlsLibraries', 'tlsOffsets', 'captureNetwork', 'captureTlsKeys'});
    expect(saved['captureNetwork'], isFalse);
    expect(saved['captureTlsKeys'], isFalse);
    expect(saved['tlsOffsets'], isEmpty);
  });

  Json offsetRecord({String path = '/data/app/com.example/base.apk'}) => {
    'path': path, 'entry': 'lib/arm64-v8a/libprivate.so', 'sha256': 'ab' * 32,
    'functions': {'SSL_new': '0x1000', 'SSL_free': '0x1100', 'SSL_read': '0x1200', 'SSL_write': '0x1300'},
  };

  Future<void> offsetField(WidgetTester tester, String name, String value) async {
    final scrollable = find.descendant(of: find.byType(TLSOffsetEditorPage), matching: find.byType(Scrollable)).first;
    tester.state<ScrollableState>(scrollable).position.jumpTo(0);
    await tester.pumpAndSettle();
    final field = find.byKey(ValueKey('tls-offset-$name'));
    await tester.scrollUntilVisible(field, 200, scrollable: scrollable);
    await tester.enterText(field, value);
    await tester.pumpAndSettle();
  }

  Future<void> openOffsets(WidgetTester tester) async {
    final tile = find.byKey(const ValueKey('manual-tls-offsets'));
    await tester.scrollUntilVisible(tile, 200,
      scrollable: find.descendant(of: find.byType(RequestSettingsPage), matching: find.byType(Scrollable)).first);
    await tester.tap(tile); await tester.pumpAndSettle();
  }

  testWidgets('manual offsets add through local revision settings and survive failed save', (tester) async {
    await mount(tester);
    await open(tester, const RequestSettingsPage(initial: {'https': true}, source: {'revision': 'offset-revision'}));
    await openOffsets(tester);
    await tester.tap(find.byKey(const ValueKey('add-tls-offset'))); await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Save').last); await tester.pumpAndSettle();
    expect(find.text('Enter an absolute ELF library or APK path.'), findsOneWidget);
    final record = offsetRecord();
    for (final name in ['path', 'entry', 'sha256']) { await offsetField(tester, name, record[name] as String); }
    for (final field in json(record['functions']).entries) { await offsetField(tester, field.key, field.value as String); }
    await offsetField(tester, 'SSL_read_ex', '0XFFFFFFFFFFFFFFF0');
    await tester.tap(find.byTooltip('Save').last); await tester.pumpAndSettle();
    expect(find.byType(TLSOffsetsPage), findsOneWidget);
    expect(calls.where((call) => call.method == 'capture.configure'), isEmpty);
    await tester.tap(find.byTooltip('Save').last); await tester.pumpAndSettle();
    extra = (call) async { if (call.method == 'capture.configure') throw PlatformException(code: 'conflict', message: 'Settings changed'); return null; };
    await tester.tap(find.byTooltip('Save').last); await tester.pumpAndSettle();
    expect(find.byType(RequestSettingsPage), findsOneWidget);
    extra = null;
    await tester.tap(find.byTooltip('Save').last); await tester.pumpAndSettle();
    final saves = calls.where((call) => call.method == 'capture.configure').map((call) => json(call.arguments)).toList();
    expect(saves.length, 2);
    expect(saves[0], saves[1]);
    expect(saves.last['revision'], 'offset-revision');
    final saved = rows(json(saves.last['config'])['tlsOffsets']).single;
    expect(saved['path'], record['path']);
    expect(saved['entry'], record['entry']);
    expect(saved['sha256'], record['sha256']);
    expect(json(saved['functions'])['SSL_read_ex'], '0xfffffffffffffff0');
    expect(calls.where((call) => call.method == 'capture.start'), isEmpty);
    expect(tester.takeException(), isNull);
  });

  testWidgets('manual offset editing validates hashes and distinct functions without changing the original', (tester) async {
    await mount(tester);
    final original = offsetRecord();
    await open(tester, TLSOffsetEditorPage(initial: original));
    await offsetField(tester, 'sha256', 'incorrect');
    await tester.tap(find.byTooltip('Save').last); await tester.pumpAndSettle();
    expect(find.text('Enter the 64 hexadecimal digits of the ELF SHA-256.'), findsOneWidget);
    await offsetField(tester, 'sha256', 'AB' * 32);
    await offsetField(tester, 'SSL_read', '0x1000');
    await tester.tap(find.byTooltip('Save').last); await tester.pumpAndSettle();
    expect(find.text('Function offsets must be distinct.'), findsOneWidget);
    await offsetField(tester, 'SSL_read', '0x00000000000001200');
    await tester.tap(find.byTooltip('Save').last); await tester.pumpAndSettle();
    expect(find.text('SSL_read: Enter a nonzero hexadecimal ELF file offset (0x...).'), findsOneWidget);
    expect(json(original['functions'])['SSL_read'], '0x1200');
    expect(original['sha256'], 'ab' * 32);
    expect(calls.where((call) => call.method == 'capture.configure'), isEmpty);
    await offsetField(tester, 'SSL_read', '0x1400');
    await tester.tap(find.byTooltip('Save').last); await tester.pumpAndSettle();
    expect(find.byType(TLSOffsetEditorPage), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('manual offsets can be edited and removed while unrelated settings survive', (tester) async {
    await mount(tester);
    final original = offsetRecord();
    await open(tester, RequestSettingsPage(initial: {'https': true, 'tlsOffsets': [original], 'excludeHosts': ['private.test']}));
    await openOffsets(tester);
    await tester.tap(find.byKey(const ValueKey('tls-offset-entry-0'))); await tester.pumpAndSettle();
    await offsetField(tester, 'SSL_write', '0x1500');
    await tester.tap(find.byTooltip('Save').last); await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('tls-offset-entry-0'))); await tester.pumpAndSettle();
    final scrollable = find.descendant(of: find.byType(TLSOffsetEditorPage), matching: find.byType(Scrollable)).first;
    final field = find.byKey(const ValueKey('tls-offset-SSL_write'));
    await tester.scrollUntilVisible(field, 200, scrollable: scrollable);
    expect(tester.widget<TextField>(field).controller!.text, '0x1500');
    expect(json(original['functions'])['SSL_write'], '0x1300');
    await tester.tap(find.byTooltip('Save').last); await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Delete').last); await tester.pumpAndSettle();
    expect(find.text('No manual TLS offsets'), findsOneWidget);
    await tester.tap(find.byTooltip('Save').last); await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Save').last); await tester.pumpAndSettle();
    final config = json(json(calls.singleWhere((call) => call.method == 'capture.configure').arguments)['config']);
    expect(config['tlsOffsets'], isEmpty);
    expect(config['excludeHosts'], ['private.test']);
    expect(tester.takeException(), isNull);
  });

  testWidgets('duplicate manual libraries are rejected and the library limit is visible', (tester) async {
    await mount(tester);
    final original = offsetRecord();
    await open(tester, TLSOffsetEditorPage(initial: original, others: [original]));
    await tester.tap(find.byTooltip('Save').last); await tester.pumpAndSettle();
    expect(find.text('This library already has manual offsets.'), findsOneWidget);
    Navigator.pop(tester.element(find.byType(TLSOffsetEditorPage)));
    await tester.pumpAndSettle();
    await open(tester, TLSOffsetsPage(initial: List.generate(16, (i) => offsetRecord(path: '/data/app/package$i/base.apk'))));
    expect(find.byKey(const ValueKey('add-tls-offset')), findsNothing);
    expect(tester.takeException(), isNull);
  });

  for (final dark in [false, true]) {
    testWidgets('manual offset forms fit narrow screens with large fonts dark=$dark', (tester) async {
      await mount(tester, dark: dark, scale: dark ? 1.5 : 1, size: const Size(360, 900));
      final original = offsetRecord(path: '/data/app/~~installed-package-version/com.example.long.package-name/base.apk');
      await open(tester, TLSOffsetsPage(initial: [original]));
      await capture(tester, 'manual-offset-list-${dark ? 'dark-large' : 'light'}');
      await tester.tap(find.byKey(const ValueKey('tls-offset-entry-0'))); await tester.pumpAndSettle();
      await capture(tester, 'manual-offset-editor-${dark ? 'dark-large' : 'light'}');
      final scrollable = find.descendant(of: find.byType(TLSOffsetEditorPage), matching: find.byType(Scrollable)).first;
      await tester.scrollUntilVisible(find.byKey(const ValueKey('tls-offset-SSL_write')), 200, scrollable: scrollable);
      await capture(tester, 'manual-offset-functions-${dark ? 'dark-large' : 'light'}');
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('raw packets and TLS keys are explicit local settings', (tester) async {
    await mount(tester);
    await open(tester, const RequestSettingsPage(initial: {'https': true}, source: {'revision': 'raw-revision'}));
    final scrollable = find.descendant(of: find.byType(RequestSettingsPage), matching: find.byType(Scrollable)).first;
    for (final key in ['capture-network', 'capture-tls-keys']) {
      final toggle = find.byKey(ValueKey(key));
      await tester.scrollUntilVisible(toggle, 240, scrollable: scrollable);
      await tester.tap(toggle); await tester.pumpAndSettle();
    }
    await Scrollable.ensureVisible(tester.element(find.byKey(const ValueKey('capture-network'))), alignment: 0.2);
    await tester.pumpAndSettle();
    await capture(tester, 'raw-capture-settings');
    await tester.tap(find.byTooltip('Save').last); await tester.pumpAndSettle();
    final args = json(calls.singleWhere((call) => call.method == 'capture.configure').arguments);
    expect(args['revision'], 'raw-revision');
    expect(json(args['config'])['captureNetwork'], isTrue);
    expect(json(args['config'])['captureTlsKeys'], isTrue);
    expect(calls.where((call) => call.method == 'capture.start'), isEmpty);
    expect(tester.takeException(), isNull);
  });

  testWidgets('pcapng and key exports use the current filter and retained data', (tester) async {
    await mount(tester);
    snapshot['state'] = {'active': false, 'wire': {'packets': 12, 'secrets': 4}};
    await open(tester, const RequestsPage());
    final scaffold = tester.widget<CommonScaffold>(find.descendant(of: find.byType(RequestsPage), matching: find.byType(CommonScaffold)).first);
    scaffold.onKeywordsUpdate!(['api.test']); await tester.pumpAndSettle();
    for (final label in ['Export pcapng', 'Export TLS keys']) {
      await tester.tap(find.byTooltip('More').last); await tester.pumpAndSettle();
      await tester.tap(find.text(label).last); await tester.pumpAndSettle();
    }
    final exports = calls.where((call) => call.method == 'capture.export').map((call) => json(call.arguments)).toList();
    expect(exports.map((value) => value['format']), ['pcapng', 'keys']);
    expect(exports.every((value) => value['query'] == 'api.test'), isTrue);
    expect(find.text('Capture statistics'), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('diagnostics distinguish observed plaintext parsed and filtered requests', (tester) async {
    await mount(tester);
    snapshot['state'] = {'active': false, 'backend': 'eBPF', 'tlsOpened': 8, 'goTlsOpened': 2,
      'plaintextConnections': 5, 'plaintextBytes': 4096, 'parsedRequests': 12, 'filteredRequests': 3,
      'recordedRequests': 9, 'socketConnections': 4, 'wire': {'packets': 30, 'secrets': 4, 'bytes': 1024}};
    await open(tester, const CaptureLogsPage());
    expect(find.text('TLS 8 · Plaintext 5 · Parsed 12'), findsOneWidget);
    expect(find.text('Capture stopped'), findsOneWidget);
    await tester.tap(find.text('Capture statistics')); await tester.pumpAndSettle();
    expect(find.text('Requests excluded by filters'), findsOneWidget);
    expect(find.text('HTTPS requests saved'), findsOneWidget);
    await capture(tester, 'capture-statistics');
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

  testWidgets('HTTPS exclusions can be selected from captured hostnames', (tester) async {
    await mount(tester);
    await open(tester, const RequestSettingsPage(initial: {'https': true, 'decryptBypassHosts': ['existing.test']}));
    final add = find.byKey(const ValueKey('add-bypass-hosts')).first;
    await tester.scrollUntilVisible(add, 250, scrollable: find.descendant(of: find.byType(RequestSettingsPage), matching: find.byType(Scrollable)).first);
    await tester.tap(add); await tester.pumpAndSettle();
    expect(find.byType(CaptureDomainsPage), findsOneWidget);
    await tester.tap(find.text('pinned.test')); await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Add domains').last); await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Save').last); await tester.pumpAndSettle();
    final config = json(json(calls.singleWhere((call) => call.method == 'capture.configure').arguments)['config']);
    expect(config['decryptBypassHosts'], ['existing.test', 'pinned.test']);
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

  testWidgets('TLS state and errors appear only in the eBPF logs menu destination', (tester) async {
    await mount(tester, size: const Size(320, 900), scale: 1.3);
    const preparing = 'Preparing capture for more applications. Reconnect the target app when preparation finishes.';
    const finished = 'Capture preparation finished. Reconnect the target app to capture new HTTPS connections.';
    const warning = 'libprivate.so: automatic SSL function resolution is incomplete';
    snapshot['state'] = {'active': true, 'backend': 'eBPF', 'libraries': 2, 'scanning': true, 'error': warning, 'skipped': 4};
    await open(tester, const RequestsPage());
    expect(find.text(preparing), findsNothing);
    expect(find.text(finished), findsNothing);
    expect(find.text(warning), findsNothing);
    expect(find.textContaining('TLS libraries attached'), findsNothing);
    expect(find.text('Connections skipped or interrupted'), findsNothing);
    expect(calls.where((call) => call.method == 'capture.logs'), isEmpty);
    await capture(tester, 'requests-without-tls-diagnostics');
    await tester.tap(find.byTooltip('More').last);
    await tester.pumpAndSettle();
    expect(find.text('Extract domains'), findsNothing);
    await tester.tap(find.text('eBPF logs').last);
    await tester.pumpAndSettle();
    expect(find.byType(CaptureLogsPage), findsOneWidget);
    expect(find.text(preparing), findsOneWidget);
    expect(find.text(warning), findsOneWidget);
    expect(find.text('Connections skipped or interrupted'), findsOneWidget);
    await capture(tester, 'tls-preparing-narrow-large-text');
    snapshot['state'] = {'active': true, 'backend': 'eBPF', 'libraries': 3, 'scanning': false, 'error': warning};
    await tester.pump(const Duration(seconds: 2));
    await tester.pumpAndSettle();
    expect(find.text(preparing), findsNothing);
    expect(find.text(finished), findsOneWidget);
    expect(find.text('eBPF · 3 TLS libraries attached'), findsOneWidget);
    expect(find.text(warning), findsOneWidget);
    expect(find.text('Capturing new connections'), findsOneWidget);
    await capture(tester, 'tls-prepared-narrow-large-text');
    Navigator.pop(tester.element(find.byType(CaptureLogsPage)));
    await tester.pumpAndSettle();
    expect(find.text(finished), findsNothing);
    expect(find.text(warning), findsNothing);
    expect(find.textContaining('TLS libraries attached'), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('eBPF history survives stopped capture and supports filtered copy export and clear', (tester) async {
    await mount(tester);
    snapshot['state'] = {'active': false, 'error': 'collector disconnected'};
    snapshot['logs'] = [
      {'id': 1, 'receivedAt': 1700000000100, 'level': 'info', 'message': 'collector ready'},
      // Startup diagnostics arrive after ready but retain their earlier time.
      {'id': 2, 'receivedAt': 1700000000000, 'level': 'debug', 'message': 'libssl.so SSL_read=0x1020'},
      {'id': 3, 'receivedAt': 1700000000200, 'level': 'error', 'message': 'TLS gap PID=42 UID=10001'},
    ];
    await open(tester, const CaptureLogsPage());
    expect(find.text('Capture stopped'), findsOneWidget);
    expect(find.text('collector disconnected'), findsOneWidget);
    await tester.tap(find.byTooltip('More').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Copy logs'));
    await tester.pumpAndSettle();
    expect(copied.last, contains('[debug] libssl.so SSL_read=0x1020'));
    expect(copied.last.indexOf('libssl.so'), lessThan(copied.last.indexOf('collector ready')));
    expect(copied.last, contains('[error] TLS gap PID=42 UID=10001'));
    final scaffold = tester.widget<CommonScaffold>(find.descendant(of: find.byType(CaptureLogsPage), matching: find.byType(CommonScaffold)).first);
    scaffold.onKeywordsUpdate!(['PID=42']);
    await tester.pumpAndSettle();
    expect(find.text('TLS gap PID=42 UID=10001'), findsOneWidget);
    expect(find.text('libssl.so SSL_read=0x1020'), findsNothing);
    await tester.tap(find.byTooltip('More').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Export').last);
    await tester.pumpAndSettle();
    final exported = json(calls.singleWhere((call) => call.method == 'document.save').arguments);
    expect(exported['name'], 'B-box-ebpf.log');
    expect(exported['content'], contains('[error] TLS gap PID=42 UID=10001'));
    expect(exported['content'], isNot(contains('libssl.so')));
    await tester.tap(find.byTooltip('More').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Clear').last);
    await tester.pumpAndSettle();
    expect(calls.where((call) => call.method == 'capture.logs.clear'), hasLength(1));
    expect(calls.where((call) => call.method == 'capture.clear'), isEmpty);
    expect(find.text('TLS gap PID=42 UID=10001'), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('eBPF log polling follows page visibility and refuses remote records', (tester) async {
    await mount(tester);
    await open(tester, const CaptureLogsPage());
    final count = calls.where((call) => call.method == 'capture.logs').length;
    openPage(tester.element(find.byType(CaptureLogsPage)), const BoxPage(title: 'Covered', children: []));
    await tester.pumpAndSettle();
    await tester.pump(const Duration(seconds: 8));
    expect(calls.where((call) => call.method == 'capture.logs'), hasLength(count));
    Navigator.pop(tester.element(find.text('Covered')));
    await tester.pumpAndSettle();
    expect(calls.where((call) => call.method == 'capture.logs').length, greaterThan(count));
    events.add({'type': 'dashboard', 'value': {'status': 'Started', 'remote': 'Server', 'remoteConnected': true}});
    await tester.pumpAndSettle();
    final beforeRemoteWait = calls.where((call) => call.method == 'capture.logs').length;
    await tester.pump(const Duration(seconds: 8));
    expect(calls.where((call) => call.method == 'capture.logs'), hasLength(beforeRemoteWait));
    expect(find.text('HTTP debugging is only available for the local service'), findsOneWidget);
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
      snapshot['state'] = {'active': true, 'backend': 'eBPF', 'libraries': 3, 'scanning': false};
      final diagnostic = 'TLS probes attached: path=/data/app/${'installed-library/' * 12}base.apk entry=lib/arm64-v8a/libssl.so method=symbols; SSL_read=0x1020 SSL_write=0x2040';
      snapshot['logs'] = [{'id': 1, 'receivedAt': 1700000000000, 'level': 'info', 'message': diagnostic}];
      await tester.tap(find.byTooltip('More').last);
      await tester.pumpAndSettle();
      await tester.tap(find.text('eBPF logs').last);
      await tester.pumpAndSettle();
      await capture(tester, '$label-ebpf-logs');
      await tester.scrollUntilVisible(find.text(diagnostic), 250,
        scrollable: find.descendant(of: find.byType(CaptureLogsPage), matching: find.byType(Scrollable)).first);
      await capture(tester, '$label-ebpf-log-details');
      expect(tester.takeException(), isNull);
      Navigator.pop(tester.element(find.byType(CaptureLogsPage)));
      await tester.pumpAndSettle();
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
