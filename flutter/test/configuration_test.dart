import 'dart:async';
import 'dart:convert';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/pages/configuration.dart';
import 'package:b_box_ui/pages/profile_editor.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'support/errors.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('config changes retain unrelated sing-box fields and rejected writes retain the loaded config', () async {
    const channel = MethodChannel('test.config-store');
    final original = <String, dynamic>{
      'dns': {'servers': [{'type': 'local', 'tag': 'system'}]},
      'route': {'final': 'direct', 'rules': [{'domain_suffix': ['example.org'], 'outbound': 'direct'}]},
      'outbounds': [{'type': 'direct', 'tag': 'direct'}],
      'experimental': {'cache_file': {'enabled': true}},
    };
    String content = jsonEncode(original), revision = 'revision-1';
    Json? written;
    bool reject = false;
    Json wire() => {'content': content, 'formatted': content, 'revision': revision, 'stats': {}};
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(channel, (call) async {
      if (call.method == 'profiles.config') return wire();
      if (call.method == 'profiles.write') {
        final args = json(call.arguments);
        expect(args['revision'], revision);
        if (reject) throw PlatformException(code: 'config', message: 'DNS server does not exist');
        content = args['content'] as String;
        written = json(jsonDecode(content));
        revision = 'revision-2';
        return wire();
      }
      return null;
    });
    final box = BoxController(channel: channel, events: const Stream.empty());
    final store = ProfileConfigStore(box, {'id': 7});
    addTearDown(() { store.dispose(); box.dispose(); messenger.setMockMethodCallHandler(channel, null); });
    await store.load();
    expect(await store.set(['dns', 'disable_cache'], true), isTrue);
    expect(written?['route'], original['route']);
    expect(written?['experimental'], original['experimental']);
    expect(written?['outbounds'], original['outbounds']);
    expect(store.revision, 'revision-2');
    reject = true;
    expect(await store.set(['dns', 'final'], 'missing'), isFalse);
    expect(store.value(['dns', 'final']), isNull);
    expect(store.revision, 'revision-2');
    expect(store.error, 'DNS server does not exist');
    expect(store.saving, isFalse);
  });

  test('a draft opened before a reload cannot overwrite newer configuration', () async {
    const channel = MethodChannel('test.config-revision');
    var revision = 'old', writes = 0;
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(channel, (call) async {
      if (call.method == 'profiles.config') return {'formatted': '{}', 'revision': revision};
      if (call.method == 'profiles.write') writes++;
      return null;
    });
    final box = BoxController(channel: channel, events: const Stream.empty());
    final store = ProfileConfigStore(box, {'id': 7});
    addTearDown(() { store.dispose(); box.dispose(); messenger.setMockMethodCallHandler(channel, null); });
    await store.load();
    final draftRevision = store.revision;
    revision = 'new';
    await store.load();
    await expectLater(store.set(['outbounds'], [], expectedRevision: draftRevision, rethrowFailure: true), throwsStateError);
    expect(writes, 0);
    expect(store.revision, 'new');
  });

  testWidgets('config sheet keeps edits after canceling dismissal and a failed save', (tester) async {
    reportFlutterErrors();
    const channel = MethodChannel('test.config-sheet');
    final events = StreamController<dynamic>.broadcast();
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(channel, (call) async => call.method == 'ready' ? {
      'version': 'test', 'coreVersion': 'test', 'preferences': {'locale': 'en'}, 'dashboard': {'status': 'Stopped'},
    } : null);
    addTearDown(() => messenger.setMockMethodCallHandler(channel, null));
    final box = BoxController(channel: channel, events: events.stream);
    await tester.pumpWidget(BoxApp(controller: box));
    await tester.pumpAndSettle();
    var reject = true;
    dynamic saved;
    openForm(tester.element(find.byType(HomePage)), ConfigValueEditor(title: 'Edit', initial: const {'type': 'local'}, onSave: (value) async {
      if (reject) throw PlatformException(code: 'config', message: 'Configuration changed; reload it before saving');
      saved = value;
      return true;
    }));
    await tester.pumpAndSettle();
    const draft = '{"type":"local","tag":"system"}';
    await tester.enterText(find.byType(TextField).last, draft);
    await tester.pumpAndSettle();
    expect(tester.widget<BoxEditGuard>(find.byType(BoxEditGuard)).dirty, isTrue);
    expect(SheetProvider.of(tester.element(find.descendant(of: find.byType(ConfigValueEditor), matching: find.byType(CommonScaffold))))?.onClose, isNotNull);
    await tester.tap(find.text('Cancel').last);
    await tester.pumpAndSettle();
    expect(find.text('Discard changes?'), findsOneWidget);
    await tester.tap(find.text('Cancel').last);
    await tester.pumpAndSettle();
    expect(find.byType(ConfigValueEditor), findsOneWidget);
    await tester.tap(find.text('Save').last);
    await tester.pumpAndSettle();
    expect(find.text('Configuration changed; reload it before saving'), findsOneWidget);
    expect(tester.widget<TextField>(find.byType(TextField).last).controller!.text, draft);
    reject = false;
    await tester.tap(find.text('Save').last);
    await tester.pumpAndSettle();
    expect(saved, {'type': 'local', 'tag': 'system'});
    expect(find.byType(ConfigValueEditor), findsNothing);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
    await events.close();
  });

  testWidgets('raw editing writes content with a revision without changing profile metadata', (tester) async {
    const channel = MethodChannel('test.raw-config');
    final events = StreamController<dynamic>.broadcast();
    final calls = <MethodCall>[];
    Json wire(String content, String revision) => {'content': content, 'formatted': content, 'revision': revision};
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(channel, (call) async {
      calls.add(call);
      if (call.method == 'ready') return {'version': 'test', 'coreVersion': 'test', 'preferences': {'locale': 'en'}, 'dashboard': {'status': 'Stopped'}};
      if (call.method == 'profiles.config') return wire('{}', 'loaded-revision');
      if (call.method == 'profiles.write') return wire(json(call.arguments)['content'] as String, 'saved-revision');
      return null;
    });
    addTearDown(() => messenger.setMockMethodCallHandler(channel, null));
    final box = BoxController(channel: channel, events: events.stream);
    await tester.pumpWidget(BoxApp(controller: box));
    await tester.pumpAndSettle();
    openPage(tester.element(find.byType(HomePage)), const ProfileEditor(profile: {'id': 7, 'name': 'Remote', 'remote': true, 'url': 'https://example.org/profile.json'}));
    await tester.pumpAndSettle();
    const content = '{"outbounds":[{"type":"direct","tag":"direct"}]}';
    await tester.enterText(find.byType(TextField).last, content);
    await tester.tap(find.byTooltip('Save'));
    await tester.pumpAndSettle();
    expect(json(calls.singleWhere((call) => call.method == 'profiles.write').arguments), {
      'id': 7, 'content': content, 'revision': 'loaded-revision',
    });
    expect(calls.where((call) => call.method == 'profiles.save'), isEmpty);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
    await events.close();
  });
}
