import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:ui' as ui;
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/pages/configuration.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:flutter/foundation.dart' show TargetPlatform, debugDefaultTargetPlatformOverride;
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'support/errors.dart';

const _scriptConfig = <String, dynamic>{
  'dns': {'servers': [{'type': 'local', 'tag': 'system'}]},
  'route': {'final': 'direct'},
  'outbounds': [{'type': 'direct', 'tag': 'direct'}],
  'experimental': {
    'cache_file': {'enabled': true},
    'mitm': {'enabled': false},
    'scripts': {
      'enabled': true,
      'http': [{'name': 'HTTP', 'pattern': '.', 'phase': 'response', 'source': r'$done();'}],
      'cron': [{'name': 'Scheduled task', 'cron': '0 2 * * *', 'source': r'$done();'}],
      'event': [{'name': 'Event task', 'event': 'network-changed', 'source': r'$done();'}],
      'generic': [{'name': 'Generic task', 'source': r'$done({title: "Report", content: "Ready"});'}],
    },
  },
};

String _title(String type) => switch (type) {
  'cron' => 'Cron tasks', 'event' => 'Event scripts', 'generic' => 'Generic scripts', _ => 'HTTP scripts',
};

class _ScriptsHarness {
  _ScriptsHarness(this.content, this.handler);
  String content, revision = 'revision-1';
  bool rejectWrite = false;
  final Future<dynamic> Function(MethodCall)? handler;
  final events = StreamController<dynamic>.broadcast();
  final calls = <MethodCall>[], writes = <Json>[];
  late final BoxController box;
  late final ProfileConfigStore store;
}

Future<_ScriptsHarness> _mount(WidgetTester tester, {Json config = _scriptConfig,
  Json dashboard = const {}, Size size = const Size(360, 800), double scale = 1,
  Locale locale = const Locale('en'), GlobalKey? boundaryKey,
  Future<dynamic> Function(MethodCall)? handler}) async {
  reportFlutterErrors();
  tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
  tester.binding.platformDispatcher.localesTestValue = [locale];
  addTearDown(tester.binding.platformDispatcher.clearLocalesTestValue);
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  tester.platformDispatcher.textScaleFactorTestValue = scale;
  addTearDown(tester.view.reset);
  addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
  final harness = _ScriptsHarness(jsonEncode(config), handler);
  const channel = MethodChannel('test.script-configuration');
  final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  Json wire() => {'formatted': harness.content, 'content': harness.content, 'revision': harness.revision};
  messenger.setMockMethodCallHandler(channel, (call) async {
    harness.calls.add(call);
    if (call.method == 'ready') return {
      'version': 'test', 'coreVersion': 'test',
      'profiles': [{'id': 7, 'name': 'Profile'}, {'id': 8, 'name': 'Another profile'}],
      'dashboard': {'status': 'Started', 'selectedProfileId': 7,
        'processingGeneration': 1, 'processingApplying': false, ...dashboard},
    };
    if (call.method == 'profiles.config') return wire();
    if (call.method == 'profiles.write') {
      final args = json(call.arguments);
      expect(args['id'], 7);
      expect(args['revision'], harness.revision);
      if (harness.rejectWrite) throw PlatformException(code: 'config', message: 'Unsupported script event');
      harness.content = args['content'] as String;
      harness.writes.add(json(jsonDecode(harness.content)));
      harness.revision = 'saved-${harness.writes.length}';
      return wire();
    }
    return await harness.handler?.call(call);
  });
  harness.box = BoxController(channel: channel, events: harness.events.stream);
  harness.store = ProfileConfigStore(harness.box, {'id': 7, 'name': 'Profile'});
  await harness.store.load();
  final app = BoxApp(controller: harness.box);
  await tester.pumpWidget(boundaryKey == null ? app : RepaintBoundary(key: boundaryKey, child: app));
  await tester.pumpAndSettle();
  addTearDown(() async {
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pumpWidget(const SizedBox());
    harness.store.dispose();
    await harness.events.close();
    messenger.setMockMethodCallHandler(channel, null);
  });
  openPage(tester.element(find.byType(HomePage)), ScriptsConfigPage(store: harness.store));
  await tester.pumpAndSettle();
  return harness;
}

Future<void> _openList(WidgetTester tester, _ScriptsHarness harness, String type) async {
  final entry = find.byKey(ValueKey('$type-scripts'));
  if (type == 'http') {
    openPage(tester.element(find.byType(ScriptsConfigPage)), ConfigListPage(store: harness.store,
      title: _title(type), path: [...ScriptsConfigPage.path, type], initial: const {}));
  } else {
    await tester.ensureVisible(entry);
    await tester.pumpAndSettle();
    await tester.tap(entry.hitTestable());
  }
  await tester.pumpAndSettle();
}

List<PopupMenuItem> _items(WidgetTester tester, {int index = 0}) =>
  tester.widget<ContextMenuRegion>(find.byKey(ValueKey('config-entry-$index'))).menuItems!;

PopupMenuItem _runItem(WidgetTester tester, {int index = 0}) =>
  _items(tester, index: index).singleWhere((item) => item.label == 'Run script');

Future<void> _runFromMenu(WidgetTester tester, {int index = 0, bool settle = true}) async {
  final entry = find.byKey(ValueKey('config-entry-$index'));
  await tester.ensureVisible(entry);
  await tester.pumpAndSettle();
  await tester.tap(find.descendant(of: entry, matching: find.byTooltip('More')).hitTestable());
  await tester.pumpAndSettle();
  await tester.tap(find.text('Run script').hitTestable());
  if (settle) { await tester.pumpAndSettle(); }
  else { await tester.pump(const Duration(milliseconds: 400)); }
}

Future<void> _capture(WidgetTester tester, GlobalKey key, String label) async {
  await tester.runAsync(() async {
    final boundary = key.currentContext!.findRenderObject()! as RenderRepaintBoundary;
    final bitmap = await boundary.toImage();
    try {
      final data = await bitmap.toByteData(format: ui.ImageByteFormat.png);
      final file = File('build/screenshots/scripts/$label.png');
      await file.parent.create(recursive: true);
      await file.writeAsBytes(data!.buffer.asUint8List());
    } finally { bitmap.dispose(); }
  });
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUpAll(() async {
    final cjk = await File('test/support/fonts/NotoSansSC-scripts.ttf').readAsBytes();
    await (FontLoader('Ubuntu')..addFont(Future.value(ByteData.sublistView(cjk)))).load();
    final mono = await File('assets/fonts/JetBrainsMono-Regular.ttf').readAsBytes();
    await (FontLoader('JetBrainsMono')..addFont(Future.value(ByteData.sublistView(mono)))).load();
    final root = Platform.environment['FLUTTER_ROOT'];
    if (root == null) return;
    final font = File('$root/bin/cache/artifacts/material_fonts/Roboto-Regular.ttf');
    if (font.existsSync()) {
      await (FontLoader('Roboto')..addFont(Future.value(ByteData.sublistView(await font.readAsBytes())))).load();
    }
  });

  test('script output preserves errors and logs, decodes panels and treats empty results as no output', () {
    for (final result in ['', 'null', '""', '{}']) {
      expect(scriptOutputText({'result': result, 'error': '', 'logs': []}), 'No output');
    }
    expect(scriptOutputText({'result': '{}'}, noOutput: '无输出'), '无输出');
    expect(scriptOutputText({'result': '"plain text"'}), 'plain text');
    expect(scriptOutputText({'result': 'unencoded text'}), 'unencoded text');
    expect(scriptOutputText({'result': '[1,2]'}), '[1,2]');
    expect(scriptOutputText({'error': 'HTTP failure', 'result': '{"title":"Report","content":"Offline"}',
      'logs': ['first log', 'second log']}), 'HTTP failure\nReport\nOffline\nfirst log\nsecond log');
  });

  test('script runtime gates saved local running configuration and deduplicates by array index', () async {
    const channel = MethodChannel('test.script-store');
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    final calls = <MethodCall>[];
    final pending = Completer<Json>();
    messenger.setMockMethodCallHandler(channel, (call) async {
      calls.add(call);
      return call.method == 'scripts.run' ? await pending.future : null;
    });
    final box = BoxController(channel: channel, events: const Stream.empty())
      ..dashboard = {'status': 'Started', 'selectedProfileId': 7, 'processingGeneration': 1, 'processingApplying': false};
    final store = ProfileConfigStore(box, {'id': 7})
      ..root = json(jsonDecode(jsonEncode(_scriptConfig)))..revision = 'saved'..ready = true;
    addTearDown(() { store.dispose(); box.dispose(); messenger.setMockMethodCallHandler(channel, null); });
    expect(store.canRunScript('generic', 0), isTrue);
    for (final patch in [
      {'status': 'Stopped'}, {'status': 'Starting'}, {'status': 'Stopping'},
      {'selectedProfileId': 8}, {'processingGeneration': 0}, {'processingApplying': true},
      {'remote': 'Server', 'remoteConnected': true},
    ]) {
      final original = Map<String, dynamic>.of(box.dashboard);
      box.dashboard = {...original, ...patch};
      expect(store.canRunScript('generic', 0), isFalse, reason: '$patch');
      box.dashboard = original;
    }
    store.saving = true;
    expect(store.canRunScript('generic', 0), isFalse);
    store.saving = false;
    store.loading = true;
    expect(store.canRunScript('generic', 0), isFalse);
    store.loading = false;
    store.revision = '';
    expect(store.canRunScript('generic', 0), isFalse);
    store.revision = 'saved';
    expect(store.canRunScript('http', 0), isFalse);
    expect(store.canRunScript('generic', -1), isFalse);
    expect(store.canRunScript('generic', 1), isFalse);
    final run = store.runScript('generic', 0, requestId: 'first');
    expect(store.isScriptRunning('generic', 0), isTrue);
    expect(await store.runScript('generic', 0, requestId: 'duplicate'), isNull);
    await store.cancelScript('first');
    await store.cancelScript('first');
    expect(calls.where((call) => call.method == 'scripts.cancel').length, 1);
    pending.complete({'result': '', 'error': '', 'logs': []});
    await run;
    expect(store.isScriptRunning('generic', 0), isFalse);
    expect(json(calls.singleWhere((call) => call.method == 'scripts.run').arguments),
      {'profileId': 7, 'revision': 'saved', 'type': 'generic', 'index': 0, 'requestId': 'first'});
  });

  testWidgets('event and generic examples save independently, event selection preserves rejected drafts', (tester) async {
    final harness = await _mount(tester, config: const {
      'dns': {'servers': [{'type': 'local', 'tag': 'system'}]},
      'route': {'final': 'direct'}, 'outbounds': [{'type': 'direct', 'tag': 'direct'}],
      'experimental': {'cache_file': {'enabled': true}, 'mitm': {'enabled': false}},
    });
    for (final type in ['event', 'generic']) {
      await _openList(tester, harness, type);
      await tester.tap(find.byTooltip('Add').last);
      await tester.pumpAndSettle();
      final field = find.descendant(of: find.byType(ConfigValueEditor), matching: find.byType(TextField));
      var draft = json(jsonDecode(tester.widget<TextField>(field).controller!.text));
      expect(draft['disabled'], isTrue);
      expect(draft['source'], isNotEmpty);
      expect(find.text('Run script').hitTestable(), findsNothing, reason: 'Unsaved scripts cannot run.');
      if (type == 'event') {
        expect(draft['event'], 'network-changed');
        await tester.tap(find.descendant(of: find.byKey(const ValueKey('script-event-trigger')), matching: find.text('Network changes')));
        await tester.pumpAndSettle();
        await tester.tap(find.text('Engine startup').hitTestable());
        await tester.pumpAndSettle();
        draft = json(jsonDecode(tester.widget<TextField>(field).controller!.text));
        expect(draft['event'], 'engine-started');
        harness.rejectWrite = true;
        await tester.tap(find.byTooltip('Save').hitTestable());
        await tester.pumpAndSettle();
        expect(find.text('Unsupported script event'), findsOneWidget);
        expect(json(jsonDecode(tester.widget<TextField>(field).controller!.text)), draft);
        expect(harness.writes, isEmpty);
        harness.rejectWrite = false;
      }
      await tester.tap(find.byTooltip('Save').hitTestable());
      await tester.pumpAndSettle();
      expect(harness.store.entries([...ScriptsConfigPage.path, type]), [draft]);
      expect(_items(tester).where((item) => item.label == 'Run script'), isEmpty, reason: 'New scripts remain disabled after saving.');
      Navigator.pop(tester.element(find.byType(ConfigListPage)));
      await tester.pumpAndSettle();
    }
    final saved = harness.writes.last;
    expect(saved['dns'], {'servers': [{'type': 'local', 'tag': 'system'}]});
    expect(saved['route'], {'final': 'direct'});
    expect(saved['outbounds'], [{'type': 'direct', 'tag': 'direct'}]);
    expect(json(saved['experimental'])['mitm'], {'enabled': false});
    expect(json(saved['experimental'])['cache_file'], {'enabled': true});
    expect(harness.calls.where((call) => call.method == 'scripts.run'), isEmpty);
    expect(tester.takeException(), isNull);
  });

  for (final type in ['cron', 'event', 'generic']) {
    testWidgets('$type saved scripts run without MITM and expose copyable panel output', (tester) async {
      final harness = await _mount(tester, handler: (call) async => call.method == 'scripts.run'
        ? {'result': '{"title":"Report","content":"Ready"}', 'error': '', 'logs': ['completed']} : null);
      await _openList(tester, harness, type);
      await _runFromMenu(tester);
      final run = json(harness.calls.singleWhere((call) => call.method == 'scripts.run').arguments);
      expect({...run}..remove('requestId'), {'profileId': 7, 'revision': 'revision-1', 'type': type, 'index': 0});
      expect(run['requestId'], isA<String>());
      expect(run['requestId'], isNotEmpty);
      final output = tester.widget<SelectableText>(find.byKey(const ValueKey('script-output-text'))).data;
      expect(output, 'Report\nReady\ncompleted');
      String? copied;
      final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
      messenger.setMockMethodCallHandler(SystemChannels.platform, (call) async {
        if (call.method == 'Clipboard.setData') copied = json(call.arguments)['text'] as String?;
        return null;
      });
      addTearDown(() => messenger.setMockMethodCallHandler(SystemChannels.platform, null));
      await tester.tap(find.byKey(const ValueKey('copy-script-output')));
      await tester.pumpAndSettle();
      expect(copied, output);
      expect(tester.takeException(), isNull);
    });
  }

  for (final response in [
    {'error': 'Script threw an error', 'result': '{}', 'logs': ['before error']},
    PlatformException(code: 'scripts', message: 'Configuration changed; reload it before running scripts'),
  ]) {
    testWidgets('script failure $response remains readable and can be retried', (tester) async {
      final harness = await _mount(tester, handler: (call) async {
        if (call.method != 'scripts.run') return null;
        if (response is PlatformException) throw response;
        return response;
      });
      await _openList(tester, harness, 'generic');
      await _runFromMenu(tester);
      final output = tester.widget<SelectableText>(find.byKey(const ValueKey('script-output-text'))).data!;
      expect(output, response is PlatformException ? response.message : 'Script threw an error\nbefore error');
      expect(harness.store.isScriptRunning('generic', 0), isFalse);
      await tester.tap(find.text('Close').hitTestable());
      await tester.pumpAndSettle();
      expect(_runItem(tester).onPressed, isNotNull);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('same names and unnamed scripts use their own indexes, different tasks run in parallel', (tester) async {
    final first = Completer<Json>(), second = Completer<Json>(), unnamed = Completer<Json>();
    final config = json(jsonDecode(jsonEncode(_scriptConfig)));
    config['experimental'] = {...json(config['experimental']), 'scripts': {
      'enabled': true, 'generic': [{'name': 'Same', 'source': r'$done();'},
        {'name': 'Same', 'source': r'$done();'}, {'source': r'$done();'}],
    }};
    final harness = await _mount(tester, config: config, handler: (call) async {
      if (call.method != 'scripts.run') return null;
      return switch (json(call.arguments)['index']) {
        0 => await first.future, 1 => await second.future, _ => await unnamed.future,
      };
    });
    await _openList(tester, harness, 'generic');
    expect(find.text('Same'), findsNWidgets(2));
    expect(find.text('Generic scripts 3'), findsOneWidget);
    final runFirst = _runItem(tester).onPressed!;
    runFirst();
    runFirst();
    _runItem(tester, index: 1).onPressed!();
    _runItem(tester, index: 2).onPressed!();
    await tester.pump(const Duration(milliseconds: 400));
    final runs = harness.calls.where((call) => call.method == 'scripts.run').toList();
    expect(runs.length, 3);
    expect(runs.map((call) => json(call.arguments)['index']), [0, 1, 2]);
    expect(runs.map((call) => json(call.arguments)['requestId']).toSet().length, 3);
    expect(_items(tester).where((item) => item.label == 'Run script'), isEmpty);
    expect(_items(tester, index: 1).where((item) => item.label == 'Run script'), isEmpty);
    expect(_items(tester, index: 2).where((item) => item.label == 'Run script'), isEmpty);
    first.complete({'result': '', 'error': '', 'logs': []});
    second.complete({'result': 'null', 'error': '', 'logs': []});
    unnamed.complete({'result': '{}', 'error': '', 'logs': []});
    await tester.pumpAndSettle();
    expect(harness.store.isScriptRunning('generic', 0), isFalse);
    expect(harness.store.isScriptRunning('generic', 1), isFalse);
    expect(harness.store.isScriptRunning('generic', 2), isFalse);
    expect(tester.takeException(), isNull);
  });

  testWidgets('cancel menu targets the current request, waits for its return and ignores a stale cancel callback', (tester) async {
    final first = Completer<Json>(), second = Completer<Json>();
    final config = json(jsonDecode(jsonEncode(_scriptConfig)));
    config['experimental'] = {...json(config['experimental']), 'scripts': {
      ...json(json(config['experimental'])['scripts']), 'generic': [
        {...rows(json(json(config['experimental'])['scripts'])['generic']).single, 'timeout_ms': -1},
      ],
    }};
    var runs = 0;
    final harness = await _mount(tester, config: config, handler: (call) async {
      if (call.method != 'scripts.run') return null;
      return ++runs == 1 ? await first.future : await second.future;
    });
    await _openList(tester, harness, 'generic');
    _runItem(tester).onPressed!();
    await tester.pump(const Duration(milliseconds: 200));
    final oldCancel = _items(tester).singleWhere((item) => item.label == 'Cancel script').onPressed!;
    final entry = find.byKey(const ValueKey('config-entry-0'));
    await tester.tap(find.descendant(of: entry, matching: find.byTooltip('More')).hitTestable());
    await tester.pump(const Duration(milliseconds: 400));
    await tester.tap(find.text('Cancel script').hitTestable());
    await tester.pump(const Duration(milliseconds: 400));
    // Popup actions run after their returning animation completes.
    await tester.pump();
    final request = json(harness.calls.singleWhere((call) => call.method == 'scripts.run').arguments);
    final canceled = harness.calls.where((call) => call.method == 'scripts.cancel').toList();
    expect(canceled, hasLength(1));
    expect(json(canceled.single.arguments), {'requestId': request['requestId']});
    expect(harness.store.isScriptRunning('generic', 0), isTrue);
    expect(harness.store.isScriptCanceling('generic', 0), isTrue);
    expect(find.byKey(const ValueKey('script-running-0')), findsOneWidget);
    expect(_items(tester).where((item) => item.label == 'Run script' || item.label == 'Cancel script'), isEmpty);
    first.complete({'result': '"canceled result"', 'error': '', 'logs': []});
    await tester.pumpAndSettle();
    expect(find.byType(ScriptOutputDialog), findsNothing);
    _runItem(tester).onPressed!();
    await tester.pump(const Duration(milliseconds: 200));
    oldCancel();
    await tester.pump(const Duration(milliseconds: 200));
    expect(harness.calls.where((call) => call.method == 'scripts.cancel').length, 1,
      reason: 'A retained menu cannot cancel a later run at the same index.');
    expect(harness.store.isScriptRunning('generic', 0), isTrue);
    second.complete({'result': '"second result"', 'error': '', 'logs': []});
    await tester.pumpAndSettle();
    expect(tester.widget<SelectableText>(find.byKey(const ValueKey('script-output-text'))).data, 'second result');
    expect(tester.takeException(), isNull);
  });

  testWidgets('a retained run menu cannot execute the replacement at its index after a configuration reload', (tester) async {
    final harness = await _mount(tester, handler: (call) async => call.method == 'scripts.run'
      ? {'result': '', 'error': '', 'logs': []} : null);
    await _openList(tester, harness, 'generic');
    final oldRun = _runItem(tester).onPressed!;
    final changed = json(jsonDecode(harness.content));
    changed['experimental'] = {...json(changed['experimental']), 'scripts': {
      ...json(json(changed['experimental'])['scripts']),
      'generic': [{'name': 'Replacement task', 'source': r'$done();'}],
    }};
    harness.content = jsonEncode(changed);
    harness.revision = 'replacement-revision';
    await harness.store.load();
    await tester.pumpAndSettle();
    oldRun();
    await tester.pumpAndSettle();
    expect(harness.calls.where((call) => call.method == 'scripts.run'), isEmpty);
    _runItem(tester).onPressed!();
    await tester.pumpAndSettle();
    final request = json(harness.calls.singleWhere((call) => call.method == 'scripts.run').arguments);
    expect(request['revision'], 'replacement-revision');
    expect(request['index'], 0);
    expect(find.text('Replacement task'), findsWidgets);
    expect(tester.takeException(), isNull);
  });

  testWidgets('displayed local script output closes when the session changes', (tester) async {
    final harness = await _mount(tester, handler: (call) async => call.method == 'scripts.run'
      ? {'result': '"local script output"', 'error': '', 'logs': []} : null);
    await _openList(tester, harness, 'generic');
    await _runFromMenu(tester);
    expect(find.byType(ScriptOutputDialog), findsOneWidget);
    harness.events.add({'type': 'dashboard', 'value': {...harness.box.dashboard,
      'remote': 'Server', 'remoteId': 9, 'remoteConnected': true}});
    await tester.pumpAndSettle();
    expect(find.byType(ScriptOutputDialog), findsNothing);
    expect(find.text('local script output'), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('invalidating displayed output removes its route without popping the editor above it', (tester) async {
    final harness = await _mount(tester, handler: (call) async => call.method == 'scripts.run'
      ? {'result': '"previous output"', 'error': '', 'logs': []} : null);
    await _openList(tester, harness, 'generic');
    final edit = _items(tester).singleWhere((item) => item.label == 'Edit').onPressed!;
    await _runFromMenu(tester);
    expect(find.byType(ScriptOutputDialog), findsOneWidget);
    edit();
    await tester.pumpAndSettle();
    expect(find.byType(ScriptOutputDialog), findsNothing);
    expect(find.byType(ConfigValueEditor), findsOneWidget);
    final field = find.descendant(of: find.byType(ConfigValueEditor), matching: find.byType(TextField));
    expect(json(jsonDecode(tester.widget<TextField>(field).controller!.text))['name'], 'Generic task');
    expect(tester.takeException(), isNull);
  });

  testWidgets('clipboard failure is reported while the script output remains usable', (tester) async {
    final harness = await _mount(tester, handler: (call) async => call.method == 'scripts.run'
      ? {'result': '"copy me"', 'error': '', 'logs': []} : null);
    final messages = <String>[];
    final subscription = harness.box.messages.stream.listen(messages.add);
    addTearDown(subscription.cancel);
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(SystemChannels.platform, (call) async {
      if (call.method == 'Clipboard.setData') throw PlatformException(code: 'clipboard', message: 'Clipboard denied');
      return null;
    });
    addTearDown(() => messenger.setMockMethodCallHandler(SystemChannels.platform, null));
    await _openList(tester, harness, 'generic');
    await _runFromMenu(tester);
    await tester.tap(find.byKey(const ValueKey('copy-script-output')));
    await tester.pumpAndSettle();
    expect(messages, contains('Clipboard denied'));
    expect(find.byType(ScriptOutputDialog), findsOneWidget);
    expect(tester.widget<SelectableText>(find.byKey(const ValueKey('script-output-text'))).data, 'copy me');
    await tester.tap(find.text('Close').hitTestable());
    await tester.pumpAndSettle();
    expect(find.byType(ScriptOutputDialog), findsNothing);
    expect(tester.takeException(), isNull);
  });

  for (final (label, dashboard, type, enabled, disabled) in [
    ('stopped', {'status': 'Stopped'}, 'generic', true, false),
    ('starting', {'status': 'Starting'}, 'generic', true, false),
    ('stopping', {'status': 'Stopping'}, 'generic', true, false),
    ('another profile', {'selectedProfileId': 8}, 'generic', true, false),
    ('applying', {'processingApplying': true}, 'generic', true, false),
    ('disabled scripts', <String, dynamic>{}, 'generic', false, false),
    ('disabled entry', <String, dynamic>{}, 'generic', true, true),
    ('HTTP', <String, dynamic>{}, 'http', true, false),
  ]) {
    testWidgets('$label does not offer immediate script execution', (tester) async {
      final config = json(jsonDecode(jsonEncode(_scriptConfig)));
      config['experimental'] = {...json(config['experimental']), 'scripts': {
        ...json(json(config['experimental'])['scripts']), 'enabled': enabled,
        type: [{...rows(json(json(config['experimental'])['scripts'])[type]).single, 'disabled': disabled}],
      }};
      final harness = await _mount(tester, config: config, dashboard: dashboard);
      await _openList(tester, harness, type);
      expect(_items(tester).where((item) => item.label == 'Run script'), isEmpty);
      expect(harness.calls.where((call) => call.method == 'scripts.run'), isEmpty);
      expect(tester.takeException(), isNull);
    });
  }

  for (final change in ['session', 'profile', 'service', 'generation', 'reload', 'background', 'editor', 'page']) {
    testWidgets('$change invalidates and cancels an outstanding run before its late result arrives', (tester) async {
      final pending = Completer<Json>();
      final harness = await _mount(tester, handler: (call) async => call.method == 'scripts.run' ? await pending.future : null);
      await _openList(tester, harness, 'generic');
      _runItem(tester).onPressed!();
      await tester.pump(const Duration(milliseconds: 200));
      final request = json(harness.calls.singleWhere((call) => call.method == 'scripts.run').arguments);
      final local = Map<String, dynamic>.of(harness.box.dashboard);
      void dashboard(Json value) => harness.events.add({'type': 'dashboard', 'value': value});
      switch (change) {
        case 'session':
          dashboard({...local, 'remote': 'Server', 'remoteId': 9, 'remoteConnected': true});
          dashboard(local);
        case 'profile':
          dashboard({...local, 'selectedProfileId': 8});
          dashboard(local);
        case 'service':
          dashboard({...local, 'status': 'Stopping'});
          dashboard(local);
        case 'generation':
          dashboard({...local, 'processingApplying': true});
          dashboard({...local, 'processingGeneration': 2});
        case 'reload':
          harness.revision = 'reloaded';
          await harness.store.load();
        case 'background':
          tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
          tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
        case 'editor':
          _items(tester).singleWhere((item) => item.label == 'Edit').onPressed!();
        case 'page':
          Navigator.pop(tester.element(find.byType(ConfigListPage)));
          await tester.pump();
      }
      await tester.pump(const Duration(milliseconds: 400));
      final cancellations = harness.calls.where((call) => call.method == 'scripts.cancel').toList();
      expect(cancellations, hasLength(1));
      expect(json(cancellations.single.arguments), {'requestId': request['requestId']});
      pending.complete({'result': '{"title":"Stale","content":"old service"}', 'error': '', 'logs': []});
      await tester.pumpAndSettle();
      expect(find.byType(ScriptOutputDialog), findsNothing);
      expect(find.text('Stale\nold service'), findsNothing);
      expect(harness.store.isScriptRunning('generic', 0), isFalse);
      expect(tester.takeException(), isNull);
    });
  }

  for (final (locale, size, scale, screenshots) in [
    (const Locale('en'), const Size(320, 568), 1.5, false),
    (const Locale('zh'), const Size(320, 568), 1.8, false),
    (const Locale('en'), const Size(800, 600), 2.0, false),
    (const Locale('zh'), const Size(320, 568), 1.8, true),
  ]) {
    testWidgets('long script output scrolls inside fixed Prism actions at $locale $size $scale${screenshots ? ' with readable CI fonts' : ''}', (tester) async {
      final previousPlatform = debugDefaultTargetPlatformOverride;
      debugDefaultTargetPlatformOverride = screenshots ? TargetPlatform.linux : TargetPlatform.android;
      try {
        final boundaryKey = screenshots ? GlobalKey() : null;
        final harness = await _mount(tester, locale: locale, size: size, scale: scale, boundaryKey: boundaryKey);
        final text = List.generate(200, (index) => '日志 $index: a long line of script output and details').join('\n');
        showBoxDialog<void>(context: tester.element(find.byType(ScriptsConfigPage)), builder: (_) => ScriptOutputDialog(
          name: List.filled(12, 'A long script name 很长的脚本名称').join(' '),
          output: {'result': jsonEncode({'title': 'Report 报告', 'content': text}), 'error': '', 'logs': []},
        ));
        await tester.pumpAndSettle();
        final dialog = find.byKey(const ValueKey('script-output-dialog'));
        final copy = find.byKey(const ValueKey('copy-script-output'));
        expect(copy.hitTestable(), findsOneWidget);
        final rect = tester.getRect(dialog);
        expect(rect.width, lessThanOrEqualTo(size.width));
        expect(rect.height, lessThanOrEqualTo(size.height));
        if (boundaryKey != null) {
          final dialogContext = tester.element(find.byType(ScriptOutputDialog));
          expect(Theme.of(dialogContext).bodyTextStyle.fontFamilyFallback?.first, 'Ubuntu');
          await _capture(tester, boundaryKey, 'output-zh-320-1.8-before-scroll');
        }
        final scroller = find.descendant(of: dialog, matching: find.byType(Scrollable)).first;
        final position = tester.state<ScrollableState>(scroller).position;
        expect(position.maxScrollExtent, greaterThan(0));
        await tester.drag(scroller, const Offset(0, -180));
        await tester.pumpAndSettle();
        expect(position.pixels, greaterThan(0));
        expect(copy.hitTestable(), findsOneWidget);
        if (boundaryKey != null) {
          await Scrollable.ensureVisible(tester.element(find.byKey(const ValueKey('script-output-text'))), alignment: 0);
          await tester.pumpAndSettle();
          expect(copy.hitTestable(), findsOneWidget);
          await _capture(tester, boundaryKey, 'output-zh-320-1.8-after-scroll');
        }
        expect(harness.calls.where((call) => call.method == 'scripts.run'), isEmpty);
        expect(tester.takeException(), isNull);
      } finally { debugDefaultTargetPlatformOverride = previousPlatform; }
    });
  }

  for (final screenshots in [false, true]) {
    testWidgets('event editor keeps the trigger and saved JSON accessible on a narrow screen with large Chinese text${screenshots ? ' with readable CI fonts' : ''}', (tester) async {
      final previousPlatform = debugDefaultTargetPlatformOverride;
      debugDefaultTargetPlatformOverride = screenshots ? TargetPlatform.linux : TargetPlatform.android;
      try {
        final boundaryKey = screenshots ? GlobalKey() : null;
        final harness = await _mount(tester, locale: const Locale('zh'), size: const Size(320, 568), scale: 1.8, boundaryKey: boundaryKey);
        if (boundaryKey != null) {
          final configScroll = find.descendant(of: find.byType(ScriptsConfigPage), matching: find.byType(Scrollable)).first;
          final generic = find.byKey(const ValueKey('generic-scripts'));
          await tester.scrollUntilVisible(generic, 140, scrollable: configScroll);
          await Scrollable.ensureVisible(tester.element(generic), alignment: 1);
          await tester.pumpAndSettle();
          expect(find.descendant(of: find.byType(ScriptsConfigPage), matching: find.text('HTTP 脚本')).hitTestable(), findsOneWidget);
          for (final type in ['cron', 'event', 'generic']) {
            expect(find.byKey(ValueKey('$type-scripts')).hitTestable(), findsOneWidget);
          }
          await _capture(tester, boundaryKey, 'configuration-zh-320-1.8');
        }
        await _openList(tester, harness, 'event');
        await tester.tap(find.text('Event task').hitTestable());
        await tester.pumpAndSettle();
        final trigger = find.byKey(const ValueKey('script-event-trigger'));
        final triggerTitle = find.descendant(of: trigger, matching: find.text('触发事件'));
        await Scrollable.ensureVisible(tester.element(triggerTitle), alignment: .5);
        await tester.pumpAndSettle();
        expect(triggerTitle.hitTestable(), findsOneWidget);
        if (boundaryKey != null) {
          final editorContext = tester.element(find.byType(ConfigValueEditor));
          expect(Theme.of(editorContext).bodyTextStyle.fontFamilyFallback?.first, 'Ubuntu');
          await _capture(tester, boundaryKey, 'event-editor-zh-320-1.8');
        }
        // The whole options row opens its menu, including its readable title.
        await tester.tap(triggerTitle.hitTestable());
        await tester.pumpAndSettle();
        if (boundaryKey != null) await _capture(tester, boundaryKey, 'event-triggers-zh-320-1.8');
        await tester.tap(find.text('配置重载').hitTestable());
        await tester.pumpAndSettle();
        final field = find.descendant(of: find.byType(ConfigValueEditor), matching: find.byType(TextField));
        final editorScroll = find.descendant(of: find.byType(ConfigValueEditor), matching: find.byType(Scrollable)).first;
        await tester.scrollUntilVisible(field, 150, scrollable: editorScroll);
        await tester.pumpAndSettle();
        expect(json(jsonDecode(tester.widget<TextField>(field).controller!.text))['event'], 'profile-reloaded');
        if (boundaryKey != null) {
          await _capture(tester, boundaryKey, 'event-json-zh-320-1.8');
        }
        final save = find.byTooltip('保存').hitTestable();
        expect(save, findsOneWidget);
        await tester.tap(save);
        await tester.pumpAndSettle();
        expect(harness.store.entries([...ScriptsConfigPage.path, 'event']).single['event'], 'profile-reloaded');
        expect(tester.takeException(), isNull);
      } finally { debugDefaultTargetPlatformOverride = previousPlatform; }
    });
  }
}
