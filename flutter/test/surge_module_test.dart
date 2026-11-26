import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/pages/configuration.dart';
import 'package:b_box_ui/pages/surge_modules.dart';
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
    expect(() => surgeModuleUrls('surge:///install-module?url=file%3A%2F%2F%2Fconfig'), throwsFormatException);
  });
  test('Modules follows Scripts on the profile Edit page', () {
    expect(ProfileConfigSection.values.skipWhile((section) => section != ProfileConfigSection.scripts).take(2),
      [ProfileConfigSection.scripts, ProfileConfigSection.modules]);
  });

  testWidgets('script-bearing updates require consent; failed or stale downloads preserve the stored module', (tester) async {
    const channel = MethodChannel('test.surge-updates');
    const old = '[Rule]\nDOMAIN,example.test,REJECT', updated = '[Script]\nhelper=type=generic,script-path=helper.js';
    var revision = 'r1', writes = 0;
    var content = jsonEncode({'experimental':{'modules':[{'id':'m','url':'https://example.test/source','enabled':true,'name':'One','content':old}]}});
    Json wire() => {'formatted':content,'revision':revision};
    var fetch = Future<String>.value(updated);
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(channel, (call) async {
      switch (call.method) {
        case 'ready': return {'version':'test', 'coreVersion':'test', 'preferences':{'locale':'en'}, 'dashboard':{'status':'Stopped'}};
        case 'profiles.config': return wire();
        case 'profiles.moduleFetch': return await fetch;
        case 'profiles.moduleDescribe': return jsonEncode({'name':'One', 'hasScripts':true});
        case 'profiles.write':
          final args = json(call.arguments);
          if (args['revision'] != revision) throw PlatformException(code:'conflict',message:'Stale module revision');
          writes++; content = args['content'] as String; revision = 'r2'; return wire();
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
    await tester.pumpAndSettle(); expect(writes,1,reason: errors.join('\n')); expect(actions.find('m')?['enabled'],false);
    expect(find.text('Run scripts from this module?'), findsOneWidget, reason: errors.join('\n'));
    await tester.tap(find.text('Cancel').last); await tester.pumpAndSettle(); await updating;
    expect(actions.find('m')?['content'],updated); expect(actions.find('m')?['enabled'],false);
    final pending = Completer<String>(); fetch = pending.future;
    final stale = actions.update(context, actions.find('m')!, revision); await tester.pump(); revision = 'external'; await store.load();
    pending.complete(old); await tester.pumpAndSettle(); await stale; expect(writes,1); expect(actions.find('m')?['content'],updated);
    final failed = Completer<String>(); fetch = failed.future;
    final failure = actions.update(context, actions.find('m')!, revision); await tester.pump(); failed.completeError(PlatformException(code:'http',message:'HTTP 404'));
    await tester.pumpAndSettle(); await failure; expect(writes,1); expect(actions.find('m')?['content'],updated);
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
        case 'profiles.moduleDescribe': return jsonEncode({'name':'First', 'hasScripts':false});
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
