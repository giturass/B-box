import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';
import 'dart:ui' as ui;

import 'package:b_box_ui/common/processing.dart';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/pages/configuration.dart';
import 'package:b_box_ui/pages/processing.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/errors.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const channel = MethodChannel('test.concord-processing');
  final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  late BoxController box;
  late ProfileConfigStore store;
  late StreamController<dynamic> events;
  late GlobalKey boundaryKey;
  late Json root, preview;
  late String revision;
  late List<Json> writes;
  bool rejectScriptPreview = false;

  Json wire() => {'formatted': jsonEncode(root), 'revision': revision, 'stats': {}};

  setUpAll(() async {
    final mono = await File('assets/fonts/JetBrainsMono-Regular.ttf').readAsBytes();
    await (FontLoader('JetBrainsMono')..addFont(Future.value(ByteData.sublistView(mono)))).load();
    final sdk = Platform.environment['FLUTTER_ROOT'];
    if (sdk != null) {
      final font = File('$sdk/bin/cache/artifacts/material_fonts/Roboto-Regular.ttf');
      if (font.existsSync()) await (FontLoader('Roboto')..addFont(Future.value(ByteData.sublistView(await font.readAsBytes())))).load();
    }
  });

  Future<void> mount(WidgetTester tester, {double scale = 1}) async {
    reportFlutterErrors();
    tester.view.physicalSize = const Size(360, 900);
    tester.view.devicePixelRatio = 1;
    tester.platformDispatcher.textScaleFactorTestValue = scale;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
    messenger.setMockMethodCallHandler(channel, (call) async {
      if (call.method == 'ready') return {
        'version': 'test', 'coreVersion': 'MiChongs test', 'preferences': {'locale': 'en'},
        'dashboard': {'status': 'Stopped'},
      };
      if (call.method == 'profiles.config') return wire();
      if (call.method == 'profiles.write') {
        final args = json(call.arguments);
        expect(args['revision'], revision);
        root = json(jsonDecode(args['content'] as String));
        writes.add(root);
        revision = 'saved-${writes.length}';
        return wire();
      }
      if (call.method == 'processing.readModule') return preview;
      if (call.method == 'processing.validate') {
        if (rejectScriptPreview) throw PlatformException(code: 'config', message: 'Invalid script line');
        return {'lines': {'script': []}};
      }
      return null;
    });
    events = StreamController<dynamic>.broadcast();
    box = BoxController(channel: channel, events: events.stream);
    store = ProfileConfigStore(box, {'id': 7, 'name': 'HTTP profile'});
    await store.load();
    boundaryKey = GlobalKey();
    await tester.pumpWidget(RepaintBoundary(key: boundaryKey, child: BoxApp(controller: box)));
    await tester.pumpAndSettle();
    addTearDown(() async {
      await tester.pumpWidget(const SizedBox());
      store.dispose();
      await events.close();
      messenger.setMockMethodCallHandler(channel, null);
    });
  }

  Future<void> capture(WidgetTester tester, String name) async {
    await tester.runAsync(() async {
      final boundary = boundaryKey.currentContext!.findRenderObject()! as RenderRepaintBoundary;
      final bitmap = await boundary.toImage();
      final data = await bitmap.toByteData(format: ui.ImageByteFormat.png);
      final file = File('build/screenshots/processing/$name.png');
      await file.parent.create(recursive: true);
      await file.writeAsBytes(data!.buffer.asUint8List());
      bitmap.dispose();
    });
  }

  setUp(() {
    root = {
      'dns': {'servers': [{'type': 'local', 'tag': 'system'}]},
      'route': {'final': 'kept'},
      'outbounds': [{'type': 'direct', 'tag': 'kept'}],
      'experimental': {'cache_file': {'enabled': true}},
    };
    preview = {'meta': {}, 'arguments': [], 'lines': {}, 'rules': [], 'ruleSets': [], 'network': {}, 'issues': {}};
    revision = 'loaded';
    writes = [];
    rejectScriptPreview = false;
  });

  test('module install links preserve their HTTP resource and reject credentials', () {
    const url = 'https://modules.test/ad.sgmodule';
    for (final link in [
      url,
      'surge:///install-module?url=${Uri.encodeComponent(url)}',
      'loon://import?plugin=${Uri.encodeComponent(url)}',
      'shadowrocket://install?module=${Uri.encodeComponent(url)}',
      'stash://install-override?url=${Uri.encodeComponent(url)}',
      'quantumult-x://add-resource?remote-resource=${Uri.encodeComponent(jsonEncode({'rewrite_remote': ['$url, tag=Ads']}))}',
    ]) {
      expect(processingModuleUrls(link), [url], reason: link);
    }
    expect(processingModuleUrls('https://user:password@modules.test/ad.sgmodule'), isEmpty);
    expect(processingModuleUrls('surge:///install-module?url=file%3A%2F%2Fprivate'), isEmpty);
    expect(processingScriptOutput({'result': '{"title":"Task","content":"done"}', 'logs': ['saved']}), 'Task\ndone\nsaved');
  });

  testWidgets('shared processing and a new MITM editor preserve other settings and the current revision', (tester) async {
    await mount(tester);
    openPage(tester.element(find.byType(HomePage)), ConcordMitmPage(store: store));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('processing-enabled')));
    await tester.pumpAndSettle();
    expect(writes.single['dns'], {'servers': [{'type': 'local', 'tag': 'system'}]});
    expect(store.value(['experimental', 'cache_file']), {'enabled': true});
    expect(store.value([...processingPath, 'enabled']), isTrue);
    await capture(tester, 'mitm');
    final editor = find.text('Edit MITM options');
    await tester.ensureVisible(editor);
    await tester.tap(editor);
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField).last, '{"decrypt-tls":false,"hostname":["*.example.test"]}');
    await tester.tap(find.text('Save').last);
    await tester.pumpAndSettle();
    expect(writes.length, 2);
    expect(store.value([...processingPath, 'mitm', 'hostname']), ['*.example.test']);
    expect(store.value(['route', 'final']), 'kept');
    expect(tester.takeException(), isNull);
  });

  testWidgets('module preview requires an explicit HTTP-only scope before enabling mixed module content', (tester) async {
    preview = {
      ...preview,
      'meta': {'name': 'Privacy', 'desc': 'HTTP processing with a separate routing rule'},
      'lines': {'url-rewrite': [{'text': '^http://ads.test/ _ reject-200'}]},
      'rules': [{'line': 2, 'type': 'DOMAIN', 'payload': 'ads.test', 'policy': 'REJECT'}],
    };
    await mount(tester, scale: 1.3);
    openForm(tester.element(find.byType(HomePage)), ConcordModuleEditor(store: store, text:
      '[Rule]\nDOMAIN,ads.test,REJECT\n[URL Rewrite]\n^http://ads.test/ _ reject-200\n'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const ValueKey('module-name')), 'Privacy');
    final previewButton = find.byKey(const ValueKey('module-preview'));
    await tester.ensureVisible(previewButton);
    await tester.tap(previewButton);
    await tester.pumpAndSettle();
    final enabled = find.byKey(const ValueKey('module-enabled'));
    await tester.ensureVisible(enabled);
    await tester.tap(enabled);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Save').last);
    await tester.pumpAndSettle();
    expect(writes, isEmpty);
    expect(find.text('Review unapplied DNS and routing settings, then choose HTTP processing only'), findsOneWidget);
    final scope = find.byKey(const ValueKey('module-http-only'));
    await tester.ensureVisible(scope);
    await tester.tap(scope);
    await tester.pumpAndSettle();
    await capture(tester, 'module-scope-scaled');
    await tester.tap(find.text('Save').last);
    await tester.pumpAndSettle();
    expect(store.value([...processingPath, 'http_only_modules']), isTrue);
    expect(store.entries([...processingPath, 'mitm', 'modules']).single['enabled'], isTrue);
    expect(store.value(['route', 'final']), 'kept');
    expect(tester.takeException(), isNull);
    openPage(tester.element(find.byType(HomePage)), ConcordModulesPage(store: store));
    await tester.pumpAndSettle();
    await capture(tester, 'modules');
  });

  testWidgets('a module draft opened before a reload retains its content and cannot overwrite the new revision', (tester) async {
    await mount(tester);
    const draft = '[URL Rewrite]\n^http://ads.test/ _ reject-200\n';
    openForm(tester.element(find.byType(HomePage)), ConcordModuleEditor(store: store, text: draft));
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const ValueKey('module-name')), 'Privacy');
    revision = 'changed-by-another-editor';
    root = {...root, 'route': {'final': 'changed'}};
    await store.load();
    await tester.pumpAndSettle();
    await tester.tap(find.text('Save').last);
    await tester.pumpAndSettle();
    expect(writes, isEmpty);
    expect(find.text('Configuration changed; reload it before saving'), findsOneWidget);
    expect(tester.widget<TextField>(find.byKey(const ValueKey('module-content'))).controller!.text, draft);
    expect(store.value(['route', 'final']), 'changed');
    expect(tester.takeException(), isNull);
  });

  testWidgets('a malformed disabled script opens an editable draft with the native diagnostic', (tester) async {
    root['experimental'] = {...json(root['experimental']), 'processing': {'enabled': false, 'mitm': {'script': ['# broken']}}};
    rejectScriptPreview = true;
    await mount(tester);
    openPage(tester.element(find.byType(HomePage)), ConcordLinesPage(store: store, list: ConcordList.script));
    await tester.pumpAndSettle();
    await tester.tap(find.text('# broken'));
    await tester.pumpAndSettle();
    expect(find.byType(ConcordLineEditor), findsOneWidget);
    expect(find.text('Invalid script line'), findsOneWidget);
    await tester.enterText(find.byKey(const ValueKey('processing-line')), '# task = type=generic,script-path=task.js');
    await tester.tap(find.text('Save').last);
    await tester.pumpAndSettle();
    expect(store.value([...processingPath, 'mitm', 'script']), ['# task = type=generic,script-path=task.js']);
    expect(tester.takeException(), isNull);
  });

  testWidgets('module tasks remain visible when the installed name differs from the declared name', (tester) async {
    await mount(tester);
    store.stats = {'processing': {
      'modules': [{'name': 'stored-name', 'origin': 'Display name (stored-name)'}],
      'lines': {'script': [
        {'name': 'Scheduled task', 'kind': 'generic', 'task': true, 'module': 'Display name (stored-name)'},
        {'name': 'Other module task', 'kind': 'generic', 'task': true, 'module': 'other'},
      ]},
    }};
    openPage(tester.element(find.byType(HomePage)), ConcordModuleScriptsPage(store: store, moduleName: 'stored-name'));
    await tester.pumpAndSettle();
    expect(find.text('Scheduled task'), findsOneWidget);
    expect(find.text('Other module task'), findsNothing);
    expect(tester.takeException(), isNull);
  });
}
