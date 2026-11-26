import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/pages/configuration.dart';
import 'package:b_box_ui/pages/surge_modules.dart';
import 'package:b_box_ui/pages/surge_panels.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/foundation.dart' show listEquals;
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

class _ModuleListReference extends StatelessWidget {
  const _ModuleListReference();
  Widget row(BuildContext context, int index) => SortableItem(key: ValueKey(index), index: index,
    child: ContextMenuRegion(menuItems: const [], child: ItemPositionProvider(position: ItemPosition.get(index, 2), child: DecorationListItem(
      contentPadding: const EdgeInsets.only(left: 16), title: Text(index == 0 ? 'First' : 'Second', maxLines: 1, overflow: TextOverflow.ellipsis),
      subtitle: Text(index == 0 ? 'Local' : 'Remote', style: context.listCaptionStyle),
      trailing: Padding(padding: const EdgeInsetsDirectional.only(end: 8), child: Switch(value: index == 1, onChanged: (_) {})),
      onPressed: () => BaseNavigator.push(context, const _ModuleDetailReference()),
    ))));
  @override Widget build(BuildContext context) => Scaffold(title: 'Modules', canSort: true,
    actions: [PopupAnchor(targetBuilder: (open) => IconButton(tooltip: 'Add', onPressed: open, icon: const GlyphIcon(AppGlyphs.addCircle)),
      popupBuilder: (_) => PopupMenu(items: const []))],
    body: NullStatusSwitcher(isEmpty: false, nullStatus: const NullStatus(illustration: NullStatusIllustration.scripts, label: 'No module yet'),
      child: PrismReorderableListView(padding: const EdgeInsets.all(16).copyWith(top: context.contentTopPadding, bottom: 16 + context.bottomInset),
        itemCount: 2, itemBuilder: (_, index) => row(context, index), proxyDecorator: (_, index, animation) => commonProxyDecorator(row(context, index), index, animation),
        onReorderItem: (_, _) {})));
}

class _ModuleDetailReference extends StatelessWidget {
  const _ModuleDetailReference();
  @override Widget build(BuildContext context) => Scaffold(title: 'First', body: NullStatusSwitcher(child: ListView(
    padding: const EdgeInsets.symmetric(horizontal: 16).copyWith(top: context.sectionsTopPadding, bottom: 16 + context.bottomInset), children: [
      generateSection(items: [ListItem.toggle(title: const Text('Enabled'), value: false, onChanged: (_) {})], footer: 'Test module'),
      generateSection(title: 'Source', items: [const ListItem(title: Text('Source'), subtitle: Text('Local')),
        ListItem(title: const Text('Last updated'), trailing: Text('', style: context.listCaptionStyle)), ListItem(title: const Text('Edit'), onTap: () {})]),
      generateSection(title: 'Attachments', items: [ListItem(title: const Text('Add local file'), onTap: () {})],
        footer: 'Lines refer to attachments by file name, as in script-path=a.js.'),
      generateSection(title: 'Rules', items: [ListItem.open(title: const Text('Routing rules'), subtitle: const Text('1 item'), widget: const _ModuleLinesReference())],
        footer: 'Routing rules go in front of the profile’s own.'),
    ])));
}

class _ModuleLinesReference extends StatelessWidget {
  const _ModuleLinesReference();
  @override Widget build(BuildContext context) => Scaffold(title: 'Routing rules', body: PrismListView(
    padding: const EdgeInsets.symmetric(horizontal: 16).copyWith(top: context.contentTopPadding, bottom: 16 + context.bottomInset), itemCount: 1,
    itemBuilder: (_, index) => ItemPositionProvider(position: ItemPosition.get(index, 1), child: const ContextMenuRegion(child: DecorationListItem(title: Text('DOMAIN,example.test,REJECT'))))));
}

Future<Uint8List> _capture(WidgetTester tester, GlobalKey frame, String name) async => (await tester.runAsync(() async {
  final image = await (frame.currentContext!.findRenderObject()! as RenderRepaintBoundary).toImage();
  try {
    final file = File('build/screenshots/surge-modules/$name.png');
    await file.parent.create(recursive: true);
    await file.writeAsBytes((await image.toByteData(format: ui.ImageByteFormat.png))!.buffer.asUint8List());
    return (await image.toByteData(format: ui.ImageByteFormat.rawRgba))!.buffer.asUint8List();
  } finally { image.dispose(); }
}))!;

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUpAll(() async {
    final root = Platform.environment['FLUTTER_ROOT']!;
    final bytes = await File('$root/bin/cache/artifacts/material_fonts/Roboto-Regular.ttf').readAsBytes();
    await (FontLoader('Roboto')..addFont(Future.value(ByteData.sublistView(bytes)))).load();
  });
  test('Surge installation links only resolve to HTTP(S) modules', () {
    const link = 'https://example.test/module?version=2';
    expect(surgeModuleUrls('surge:///install-module?url=${Uri.encodeComponent(link)} $link'), [link]);
    expect(surgeModuleUrls('surge://install-module?url=${Uri.encodeComponent(link)}'), [link]);
    expect(() => surgeModuleUrls('surge:///install/-module?url=${Uri.encodeComponent(link)}'), throwsFormatException);
    expect(() => surgeModuleUrls('surge:///install-module?url=file%3A%2F%2F%2Fconfig'), throwsFormatException);
  });
  test('Modules follows Scripts on the profile Edit page', () {
    expect(ProfileConfigSection.values.skipWhile((section) => section != ProfileConfigSection.scripts).take(2),
      [ProfileConfigSection.scripts, ProfileConfigSection.modules]);
  });

  testWidgets('script sources stay off until switched on; batch imports and stale or failed updates never partially write', (tester) async {
    const channel = MethodChannel('test.surge-updates');
    const old = '[Rule]\nDOMAIN,example.test,REJECT', updated = '[Script]\nhelper=type=generic,script-path=helper.js';
    var revision = 'r1', writes = 0;
    var rejectWrite = false;
    var content = jsonEncode({'experimental':{'modules':[{'id':'m','url':'https://example.test/source','enabled':true,'name':'One','content':old}]}});
    Json wire() => {'formatted':content,'revision':revision};
    var fetch = Future<String>.value(updated);
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(channel, (call) async {
      switch (call.method) {
        case 'ready': return {'version':'test', 'coreVersion':'test', 'preferences':{'locale':'en'}, 'dashboard':{'status':'Stopped'}};
        case 'profiles.config': return wire();
        case 'profiles.moduleFetch':
          if (json(call.arguments)['url'] == 'https://example.test/bad') throw PlatformException(code:'http',message:'HTTP 404');
          if (json(call.arguments)['url'] == 'https://example.test/rules') return old;
          return await fetch;
        case 'profiles.moduleDescribe':
          final item = json(jsonDecode(json(call.arguments)['module'] as String));
          final body = item['content'] as String;
          return jsonEncode({'name':'One', 'hasScripts':body.startsWith('[Script]'), 'lines':{
            if (body == updated) 'script':[{'text':'helper'}]
            else if (body == old) 'rule':[{'text':'DOMAIN,example.test,REJECT'}]
            else 'ignored':[{'text':body, 'issue':body.startsWith('[Script]') ? 'missing module attachment or unsupported URL: local.js' : 'unsupported route condition or modifier'}],
          }});
        case 'profiles.write':
          final args = json(call.arguments);
          if (args['revision'] != revision) throw PlatformException(code:'conflict',message:'Stale module revision');
          if (rejectWrite) throw PlatformException(code:'apply',message:'Configuration was not applied');
          content = args['content'] as String; revision = 'write-${++writes}'; return wire();
      }
      return null;
    });
    final box = BoxController(channel:channel,events:const Stream.empty()), store = ProfileConfigStore(box, {'id':7});
    await store.load(); await tester.pumpWidget(BoxApp(controller:box)); await tester.pumpAndSettle();
    final actions = ModuleActions(store), context = tester.element(find.byType(HomePage));
    final errors = <String>[];
    final capture = box.messages.stream.listen(errors.add);
    addTearDown(() async { await tester.pumpWidget(const SizedBox()); await capture.cancel(); actions.dispose(); store.dispose(); messenger.setMockMethodCallHandler(channel, null); });
    final updating = actions.update(context, actions.find('m')!, revision);
    await tester.pumpAndSettle(); expect(find.byType(AlertDialog), findsNothing); await updating;
    expect(writes,1,reason: errors.join('\n')); expect(actions.find('m')?['enabled'],false); expect(actions.find('m')?['content'],updated);
    for (final enabled in [true, false, true]) {
      expect(await actions.toggle(context, actions.find('m')!, enabled, revision), isTrue);
      expect(actions.find('m')?['enabled'], enabled);
    }
    await actions.update(context, actions.find('m')!, revision);
    expect(writes, 5); expect(actions.find('m')?['enabled'], true);
    final pending = Completer<String>(); fetch = pending.future;
    final stale = actions.update(context, actions.find('m')!, revision); await tester.pump(); revision = 'external'; await store.load();
    pending.complete(old); await tester.pumpAndSettle(); await stale; expect(writes,5); expect(actions.find('m')?['content'],updated);
    final failed = Completer<String>(); fetch = failed.future;
    final failure = actions.update(context, actions.find('m')!, revision); await tester.pump(); failed.completeError(PlatformException(code:'http',message:'HTTP 404'));
    await tester.pumpAndSettle(); await failure; expect(writes,5); expect(actions.find('m')?['content'],updated);
    const rules = 'https://example.test/rules', scripts = 'https://example.test/scripts';
    fetch = Future.value('[Rule]\nnot a rule');
    await actions.update(context, actions.find('m')!, revision);
    expect(writes, 5); expect(actions.find('m')?['content'], updated);
    await expectLater(actions.importUrls(context, '$rules https://example.test/unusable', revision), throwsFormatException);
    expect(writes, 5); expect(actions.modules, hasLength(1));
    fetch = Future.value(updated);
    await expectLater(actions.importUrls(context, '$rules https://example.test/bad', revision), throwsA(isA<PlatformException>()));
    expect(writes, 5); expect(actions.modules, hasLength(1));
    await actions.importUrls(context, 'https://example.test/source $rules $scripts $rules', revision);
    expect(writes, 6); expect(actions.modules.map((item) => item['enabled']), [true, true, false]);
    expect(actions.modules.map((item) => item['url']), ['https://example.test/source', rules, scripts]);
    rejectWrite = true;
    expect(await actions.toggle(context, actions.find('m')!, false, revision), false);
    expect(writes, 6); expect(actions.find('m')?['enabled'], true);
    rejectWrite = false;
    expect(await actions.toggle(context, actions.find('m')!, false, revision), true); expect(writes, 7);
    await actions.save(context, {'content':'[Script]\nhelper=type=generic,script-path=local.js'}, revision);
    final draft = actions.modules.last;
    expect(draft['enabled'], false); expect(writes, 8);
    expect(await actions.toggle(context, draft, true, revision), false); expect(writes, 8);
  });

  testWidgets('panels show static then last success, on-entry intervals and manual failures stay separate', (tester) async {
    const channel = MethodChannel('test.module-panels');
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger, calls = <Json>[];
    final cached = <String, Json>{};
    messenger.setMockMethodCallHandler(channel, (call) async {
      final args = json(call.arguments);
      switch (call.method) {
        case 'ready': return {'version':'test','coreVersion':'test','preferences':{'locale':'en'},'profiles':[{'id':7,'name':'Local'}],
          'dashboard':{'status':'Started','selectedProfileId':7,'processingGeneration':1}};
        case 'profiles.config': return {'revision':'r1','formatted':'{"experimental":{"scripts":{"enabled":true}}}'};
        case 'profiles.modulePanels': return jsonEncode([for (final (index, name) in ['Manual','Interval'].indexed)
          {'moduleId':'m', 'name':name, 'index':index, 'title':'Static $name', if (index == 1) 'updateInterval':3600,
            if (cached.containsKey(name)) 'cached':cached[name]}]);
        case 'scripts.run':
          calls.add(args);
          final name = args['panelName'] as String;
          if (cached.containsKey(name)) return {'error':'Fixture unavailable'};
          cached[name] = {'title':'Updated $name','content':'One\nTwo','updatedAt':DateTime.now().millisecondsSinceEpoch};
          return {'panel':jsonEncode(cached[name])};
      }
      return null;
    });
    final box = BoxController(channel:channel,events:const Stream.empty());
    await tester.pumpWidget(BoxApp(controller:box)); await tester.pumpAndSettle();
    addTearDown(() async { await tester.pumpWidget(const SizedBox()); messenger.setMockMethodCallHandler(channel,null); });
    void open() { openPage<void>(tester.element(find.byType(HomePage)), const SurgePanelsPage()); }
    open(); await tester.pumpAndSettle();
    expect(calls.map((entry) => (entry['panelName'], entry['automatic'])), [('Interval', true)]);
    expect(find.text('Static Manual'), findsOneWidget); expect(find.text('Updated Interval'), findsOneWidget);
    final refresh = find.byTooltip('Refresh').hitTestable().first;
    await tester.tap(refresh); await tester.pumpAndSettle();
    expect(calls.last['moduleId'],'m'); expect(calls.last['index'],0); expect(calls.last['revision'],'r1'); expect(calls.last['automatic'],false);
    await tester.tap(refresh); await tester.pumpAndSettle();
    expect(find.text('Fixture unavailable'),findsOneWidget); expect(find.text('Updated Manual'),findsOneWidget);
    Navigator.of(tester.element(find.text('Updated Manual'))).pop(); await tester.pumpAndSettle();
    open(); await tester.pumpAndSettle(); expect(calls,hasLength(3)); expect(find.text('Updated Manual'),findsOneWidget);
    expect(find.text('Fixture unavailable'),findsNothing);
  });

  testWidgets('remote module menu copies and replaces its URL; detail retries and argument switches preserve activation', (tester) async {
    const channel = MethodChannel('test.module-source');
    const original = 'https://example.test/original?version=1', replacement = 'https://example.test/new?version=2';
    const source = '#!name=Remote tools\n[Unknown]\nRates=script-name=helper\n[Script]\nhelper=type=generic,script-path=helper.js';
    var content = jsonEncode({'experimental': {'modules': [
      {'id':'remote', 'url':original, 'name':'Remote tools', 'content':source, 'enabled':true},
      {'id':'local', 'name':'Local rules', 'content':'[Rule]\nDOMAIN,example.test,REJECT'},
    ]}}), revision = 'initial';
    var writes = 0, reads = 0;
    final fetched = <String>[], copied = <String>[];
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    Json wire() => {'formatted':content, 'revision':revision};
    messenger.setMockMethodCallHandler(SystemChannels.platform, (call) async {
      if (call.method == 'Clipboard.setData') copied.add(json(call.arguments)['text'] as String);
      return null;
    });
    messenger.setMockMethodCallHandler(channel, (call) async {
      final args = json(call.arguments);
      switch (call.method) {
        case 'ready': return {'version':'test','coreVersion':'test','dashboard':{'status':'Stopped'}};
        case 'profiles.config': return wire();
        case 'profiles.moduleFetch':
          fetched.add(args['url'] as String);
          if (fetched.last != replacement) throw PlatformException(code:'http', message:'HTTP 404');
          return source;
        case 'profiles.moduleFile': return {'name':'file.sgmodule', 'content':'[Rule]\nDOMAIN,local.test,REJECT'};
        case 'profiles.moduleDescribe':
          final script = json(jsonDecode(args['module'] as String))['content'] == source;
          return jsonEncode({'name':script ? 'Remote tools' : '', 'hasScripts':script, 'lines':{if (script) ...{
            'script':[{'text':'helper'}], 'ignored':[{'text':'Rates=script-name=helper','issue':'unsupported section [unknown]'}],
          } else 'rule':[{'text':'DOMAIN,local.test,REJECT'}]}});
        case 'profiles.moduleDetails':
          if (++reads == 1) throw PlatformException(code:'read', message:'Unavailable');
          return jsonEncode({'hasScripts':true, 'arguments':[{'name':'Feature flag','default':'false'}], 'lines':{
            'script':[{'text':'helper'}], 'ignored':[{'text':'Rates=script-name=helper','issue':'unsupported section [unknown]'}]}});
        case 'profiles.write':
          expect(args['revision'], revision);
          content = args['content'] as String; revision = 'write-${++writes}'; return wire();
      }
      return null;
    });
    final box = BoxController(channel:channel,events:const Stream.empty()), store = ProfileConfigStore(box, {'id':7});
    Json remote() => store.entries(['experimental','modules']).first;
    tester.view.physicalSize = const Size(390, 844); tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize); addTearDown(tester.view.resetDevicePixelRatio);
    addTearDown(() async {
      await tester.pumpWidget(const SizedBox()); store.dispose();
      messenger.setMockMethodCallHandler(channel, null); messenger.setMockMethodCallHandler(SystemChannels.platform, null);
    });
    await store.load(); await tester.pumpWidget(BoxApp(controller:box)); await tester.pumpAndSettle();
    openPage(tester.element(find.byType(HomePage)), SurgeModulesPage(store:store)); await tester.pumpAndSettle();
    Future<void> menu(String name) async { await tester.longPress(find.text(name).hitTestable()); await tester.pumpAndSettle(); }
    Future<void> tap(String label) async { await tester.tap(find.text(label).hitTestable().last); await tester.pumpAndSettle(); }
    Future<void> editUrl(String value) async {
      await menu('Remote tools'); await tap('Edit URL');
      final input = find.descendant(of: find.byType(InputDialog), matching: find.byType(EditableText));
      expect(tester.widget<EditableText>(input).controller.text, original);
      await tester.enterText(input, value); await tap('Submit');
    }
    await menu('Local rules');
    expect(find.text('Edit').hitTestable(), findsOneWidget); expect(find.text('Copy URL'), findsNothing);
    await tap('Delete'); await tap('Cancel'); expect(writes, 0);
    await menu('Remote tools'); await tap('Copy URL'); expect(copied, [original]); expect(writes, 0);
    await tester.pump(const Duration(seconds: 5)); await tester.pumpAndSettle();
    await editUrl('https://example.test/missing'); expect(remote()['url'], original); expect(writes, 0);
    await tester.pump(const Duration(seconds: 5)); await tester.pumpAndSettle();
    await editUrl(replacement); expect(fetched, ['https://example.test/missing', replacement]);
    expect(writes, 1); expect(remote()['url'], replacement); expect(remote()['enabled'], false);
    await tester.pump(const Duration(seconds: 5)); await tester.pumpAndSettle();
    await tap('Remote tools'); await tap('Retry'); expect(reads, 2);
    expect(find.textContaining('Scripts are off in this profile.'), findsOneWidget);
    expect(find.textContaining('unsupported section [unknown]'), findsOneWidget);
    await tap('Enabled'); expect(writes, 2); expect(remote()['enabled'], true); expect(find.byType(AlertDialog), findsNothing);
    final argument = find.text('Feature flag');
    final detailScroll = find.ancestor(of: find.text('Enabled').hitTestable(), matching: find.byType(Scrollable)).first;
    await tester.scrollUntilVisible(argument, 160, scrollable: detailScroll); await tap('Feature flag');
    expect(writes, 3); expect(remote()['arguments'], {'Feature flag':'true'}); expect(remote()['enabled'], true);
    expect(find.byType(AlertDialog), findsNothing);
    Navigator.of(tester.element(argument)).pop(); await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Add').hitTestable()); await tester.pumpAndSettle(); await tap('Import from file');
    final files = store.entries(['experimental','modules']);
    expect(writes, 4); expect(files.last['name'], 'file.sgmodule'); expect(files.last['enabled'], true);
    tester.widget<PrismReorderableListView>(find.byType(PrismReorderableListView)).onReorderItem(2, 0);
    await tester.pumpAndSettle(); expect(store.entries(['experimental','modules']).first['id'], files.last['id']);
    await menu('file.sgmodule'); await tap('Delete'); await tap('Delete');
    expect(writes, 6); expect(store.entries(['experimental','modules']).map((item) => item['id']), ['remote','local']);
    expect(remote()['arguments'], {'Feature flag':'true'}); expect(tester.takeException(), isNull);
  });

  testWidgets('module list pixels match the FlClash v0.9.3 fixture and edits keep the base profile', (tester) async {
    const channel = MethodChannel('test.surge-modules');
    const source = '#!name=First\n[Rule]\nDOMAIN,example.test,REJECT';
    var content = jsonEncode({'route': {'final':'kept'}, 'experimental': {'modules': [
      {'id':'one', 'name':'First', 'content':source},
      {'id':'two', 'name':'Second', 'url':'https://example.test/module', 'content':'[Rule]\nFINAL,DIRECT', 'enabled':true},
    ]}}), revision = 'initial';
    int writes = 0;
    Json wire() => {'formatted':content, 'content':content, 'revision':revision};
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(channel, (call) async {
      switch (call.method) {
        case 'ready': return {'version':'test', 'coreVersion':'test', 'preferences':{'locale':'en'}, 'dashboard':{'status':'Stopped'}};
        case 'profiles.config': return wire();
        case 'profiles.moduleDescribe': return jsonEncode({'name':'First', 'hasScripts':false, 'lines':{'rule':[{'text':'DOMAIN,example.test,REJECT'}]}});
        case 'profiles.moduleDetails': return jsonEncode({'description':'Test module', 'lines':{'rule':[{'text':'DOMAIN,example.test,REJECT'}]}});
        case 'profiles.write':
          final args = json(call.arguments);
          if (args['revision'] != revision) throw PlatformException(code:'conflict',message:'Stale module revision');
          content = args['content'] as String; revision = 'write-${++writes}'; return wire();
      }
      return null;
    });
    final box = BoxController(channel: channel, events: const Stream.empty()), store = ProfileConfigStore(box, {'id':7});
    final reference = ValueNotifier(true), frame = GlobalKey();
    addTearDown(() async { await tester.pumpWidget(const SizedBox()); reference.dispose(); store.dispose(); messenger.setMockMethodCallHandler(channel, null); });
    await store.load();
    tester.view.physicalSize = const Size(390, 844); tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize); addTearDown(tester.view.resetDevicePixelRatio);
    await tester.pumpWidget(RepaintBoundary(key: frame, child: BoxApp(controller: box)));
    await tester.pumpAndSettle();
    openPage(tester.element(find.byType(HomePage)), ValueListenableBuilder<bool>(valueListenable: reference, builder: (_, upstream, _) =>
      upstream ? const _ModuleListReference() : ListenableBuilder(listenable: store, builder: (_, _) => SurgeModulesPage(store: store))));
    await tester.pumpAndSettle();
    final expected = await _capture(tester, frame, 'reference-list');
    Future<(Uint8List, Uint8List)> drillDown(String prefix) async {
      await tester.tap(find.text('First').hitTestable()); await tester.pumpAndSettle();
      expect(find.text('Test module'), findsOneWidget); expect(find.text('Add local file'), findsOneWidget);
      final detail = await _capture(tester, frame, '$prefix-detail');
      final route = find.text('Routing rules').last;
      await tester.ensureVisible(route); await tester.tap(route); await tester.pumpAndSettle();
      final line = find.text('DOMAIN,example.test,REJECT'); expect(line, findsOneWidget);
      final lines = await _capture(tester, frame, '$prefix-lines');
      Navigator.of(tester.element(line)).pop(); await tester.pumpAndSettle();
      Navigator.of(tester.element(find.text('Test module'))).pop(); await tester.pumpAndSettle();
      return (detail, lines);
    }
    final (referenceDetail, referenceLines) = await drillDown('reference');
    reference.value = false; await tester.pumpAndSettle();
    final actual = await _capture(tester, frame, 'bbox-list');
    expect(listEquals(actual, expected), isTrue, reason: 'Full RGBA list images must match; both PNGs are in the Actions artifacts.');
    final (detail, lines) = await drillDown('bbox');
    expect(listEquals(detail, referenceDetail), isTrue, reason: 'Module detail pixels differ from the reference');
    expect(listEquals(lines, referenceLines), isTrue, reason: 'Module rule line pixels differ from the reference');
    await tester.tap(find.byType(Switch).hitTestable().first); await tester.pumpAndSettle();
    expect(writes, 1); expect(store.entries(['experimental','modules']).first['enabled'], true); expect(store.value(['route']), {'final':'kept'});
  });
}
