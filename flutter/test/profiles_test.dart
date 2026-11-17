import 'dart:async';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/components/profile_card.dart';
import 'package:b_box_ui/components/proxy_card.dart';
import 'package:b_box_ui/components/proxy_list.dart';
import 'package:b_box_ui/components/page_switcher.dart';
import 'package:b_box_ui/pages/nodes.dart';
import 'package:b_box_ui/pages/profiles.dart';
import 'package:b_box_ui/pages/profile_details.dart';
import 'package:b_box_ui/pages/subscriptions.dart';
import 'package:flutter/semantics.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

const _remote = {'id': 1, 'name': 'Remote A', 'remote': true,
  'url': 'https://example.org/config.json', 'autoUpdate': true, 'interval': 120, 'updatedAt': 1700000000000};
const _local = {'id': 2, 'name': 'Local B', 'remote': false};
const _default = {'id': 3, 'name': 'Profile file', 'remote': false, 'default': true};

Future<BoxController> _mount(WidgetTester tester, {List<Json> profiles = const [_remote, _local],
  int selected = 1, bool remoteSession = false, StreamController<dynamic>? eventController,
  Json dashboard = const {}, Json preferences = const {}, Json nodes = const {},
  Size size = const Size(360, 800),
  Future<dynamic> Function(MethodCall)? handler}) async {
  tester.binding.platformDispatcher.localesTestValue = const [Locale('en')];
  addTearDown(tester.binding.platformDispatcher.clearLocalesTestValue);
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  const channel = MethodChannel('test.profiles');
  final events = eventController ?? StreamController<dynamic>.broadcast();
  final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  messenger.setMockMethodCallHandler(channel, (call) async => call.method == 'ready' ? {
    'version': 'test', 'coreVersion': 'test', 'profiles': profiles,
    'preferences': preferences, 'nodes': nodes,
    'dashboard': {'status': 'Stopped', 'selectedProfileId': selected,
      if (remoteSession) ...{'remote': 'Server', 'remoteConnected': true}, ...dashboard},
  } : await handler?.call(call));
  final box = BoxController(channel: channel, events: events.stream);
  await tester.pumpWidget(BoxApp(controller: box));
  await tester.pumpAndSettle();
  addTearDown(() async {
    await tester.pumpWidget(const SizedBox());
    await events.close();
    messenger.setMockMethodCallHandler(channel, null);
  });
  return box;
}

Future<void> _profiles(WidgetTester tester) async {
  final sidebar = find.byType(NavigationSidebar);
  final target = sidebar.evaluate().isEmpty ? find.text('Profiles').last
    : find.descendant(of: sidebar, matching: find.byKey(const ValueKey('Profiles')));
  expect(target.hitTestable(), findsOneWidget);
  await tester.tap(target);
  await tester.pumpAndSettle();
  expect(tester.widget<PageSwitcher>(find.byType(PageSwitcher)).index, 1);
}

Future<void> _revealSwitchProfile(WidgetTester tester) async {
  final tooltip = find.byWidgetPredicate((widget) => widget is Tooltip && widget.message == 'Switch profile');
  final button = find.descendant(of: tooltip, matching: find.byType(IconButton));
  expect(button, findsOneWidget);
  await Scrollable.ensureVisible(tester.element(button), alignment: .5);
  await tester.pumpAndSettle();
}

void main() {
  test('compact update ages preserve unknown values and reference thresholds', () {
    final now = DateTime.utc(2026, 10, 29, 12);
    for (final (age, en, zh) in [
      (const Duration(seconds: 59), 'Just now', '刚刚'),
      (const Duration(minutes: 1), '1m ago', '1 分钟前'),
      (const Duration(hours: 1), '1h ago', '1 小时前'),
      (const Duration(days: 1), '1d ago', '1 天前'),
      (const Duration(days: 30), '1mo ago', '1 个月前'),
      (const Duration(days: 365), '1y ago', '1 年前'),
    ]) {
      final updated = now.subtract(age).millisecondsSinceEpoch;
      expect(profileUpdateLabel(updated, now, chinese: false), en);
      expect(profileUpdateLabel(updated, now, chinese: true), zh);
    }
    expect(profileUpdateLabel(0, now, chinese: false), 'Unknown');
    expect(profileUpdateLabel(null, now, chinese: true), '未知');
    expect(profileUpdateLabel(now.add(const Duration(days: 1)).millisecondsSinceEpoch, now, chinese: false), 'Just now');
  });

  testWidgets('empty state opens the add menu and canceled file import stays on profiles', (tester) async {
    final calls = <String>[];
    await _mount(tester, profiles: [], handler: (call) async { calls.add(call.method); return null; });
    await _profiles(tester);
    await tester.tap(find.text('Add profile').last);
    await tester.pumpAndSettle();
    expect(find.byType(AddProfilePage), findsNothing);
    for (final label in ['Scan QR code', 'File', 'Import from URL', 'Clipboard', 'New profile', 'New node']) {
      expect(find.text(label), findsOneWidget);
    }
    expect(find.text('Node subscription'), findsNothing);
    await tester.tap(find.text('File'));
    await tester.pumpAndSettle();
    expect(calls.where((method) => method == 'profiles.importFile'), hasLength(1));
    expect(find.byType(ProfileForm), findsNothing);
    expect(calls, isNot(contains('profiles.save')));
    expect(tester.takeException(), isNull);
  });

  testWidgets('remote and local menus keep sing-box operations and source link semantics', (tester) async {
    String? copied;
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(SystemChannels.platform, (call) async {
      if (call.method == 'Clipboard.setData') copied = json(call.arguments)['text'] as String;
      return null;
    });
    addTearDown(() => messenger.setMockMethodCallHandler(SystemChannels.platform, null));
    await _mount(tester);
    await _profiles(tester);
    final remoteCard = find.byWidgetPredicate((w) => w is ProfileCard && w.profile['id'] == 1);
    final localCard = find.byWidgetPredicate((w) => w is ProfileCard && w.profile['id'] == 2);
    final remote = tester.widget<ProfileCard>(remoteCard);
    final local = tester.widget<ProfileCard>(localCard);
    expect(remote.menuItems.map((item) => item.label), ['Edit', 'Update', 'Copy link', 'QR code', 'Share', 'Export', 'Delete']);
    expect(local.menuItems.map((item) => item.label), ['Edit', 'Share', 'Export', 'Delete']);
    expect(remote.menuItems.last.danger, isTrue);
    expect(remote.menuItems.last.onPressed, isNull);
    expect(local.menuItems.last.onPressed, isNotNull);
    remote.menuItems.singleWhere((item) => item.label == 'Copy link').onPressed!();
    await tester.pump();
    expect(copied, _remote['url']);
    expect(find.descendant(of: localCard, matching: find.text('Unknown')), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('local update times are visible on the dashboard and refresh with native metadata', (tester) async {
    final events = StreamController<dynamic>.broadcast();
    await _mount(tester, selected: 2, eventController: events);
    expect(find.text('Updated Unknown'), findsOneWidget);
    final updatedAt = DateTime.now().millisecondsSinceEpoch;
    events.add({'type': 'profiles', 'value': [_remote, {..._local, 'updatedAt': updatedAt}]});
    await tester.pumpAndSettle();
    expect(find.text('Updated Just now'), findsOneWidget);
    expect(find.byTooltip('Last updated: ${localDate(updatedAt)}'), findsOneWidget);
    final updateTime = find.text('Updated Just now');
    await Scrollable.ensureVisible(tester.element(updateTime), alignment: .5);
    await tester.pumpAndSettle();
    await tester.tap(updateTime);
    await tester.pumpAndSettle();
    expect(find.text('Last updated: ${localDate(updatedAt)}'), findsOneWidget);
    await _profiles(tester);
    final localCard = find.byWidgetPredicate((w) => w is ProfileCard && w.profile['id'] == 2);
    expect(find.descendant(of: localCard, matching: find.text('Just now')), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('dashboard switch profile has an independent semantic tap target', (tester) async {
    final semantics = tester.ensureSemantics();
    try {
      final selections = <dynamic>[];
      await _mount(tester, selected: 2, handler: (call) async {
        if (call.method == 'profiles.select') selections.add(json(call.arguments)['id']);
        return null;
      });
      await _revealSwitchProfile(tester);
      final target = find.bySemanticsLabel('Switch profile').hitTestable();
      expect(target, findsOneWidget);
      final node = tester.getSemantics(target);
      expect(node.getSemanticsData().label, 'Switch profile');
      expect(node.rect.size, const Size.square(32));
      expect(node.getSemanticsData().hasAction(SemanticsAction.tap), isTrue);
      expect(node.parent!.getSemanticsData().label, isNot(contains('Switch profile')));
      tester.renderObject(target).owner!.semanticsOwner!.performAction(node.id, SemanticsAction.tap);
      await tester.pumpAndSettle();
      expect(find.byWidgetPredicate((widget) => widget is ProfilesPage && widget.picker), findsOneWidget);
      await tester.tap(find.text('Remote A').hitTestable());
      await tester.pumpAndSettle();
      expect(selections, [1]);
      expect(find.byWidgetPredicate((widget) => widget is ProfilesPage && widget.picker), findsNothing);
      expect(tester.takeException(), isNull);
    } finally { semantics.dispose(); }
  });

  testWidgets('dashboard switch profile semantic action waits for stable service state', (tester) async {
    final semantics = tester.ensureSemantics();
    try {
      final events = StreamController<dynamic>.broadcast();
      await _mount(tester, selected: 2, eventController: events);
      await _revealSwitchProfile(tester);
      for (final status in ['Starting', 'Stopped', 'Stopping', 'Stopped']) {
        events.add({'type': 'dashboard', 'value': {'status': status, 'selectedProfileId': 2}});
        await tester.pump();
        await tester.pump(const Duration(milliseconds: 300));
        final target = find.bySemanticsLabel('Switch profile').hitTestable();
        expect(target, findsOneWidget);
        final node = tester.getSemantics(target);
        expect(node.rect.size, const Size.square(32));
        expect(node.getSemanticsData().hasAction(SemanticsAction.tap), status == 'Stopped');
        expect(find.byWidgetPredicate((widget) => widget is ProfilesPage && widget.picker), findsNothing);
      }
      expect(tester.takeException(), isNull);
    } finally { semantics.dispose(); }
  });

  testWidgets('only enabled automatic updates validate the minimum interval', (tester) async {
    Json? saved;
    await _mount(tester, handler: (call) async {
      if (call.method == 'profiles.save') saved = json(call.arguments);
      return null;
    });
    openForm(tester.element(find.byType(HomePage)), const ProfileForm(initial: _remote));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextFormField).at(2), '14');
    await tester.tap(find.byTooltip('Save').last);
    await tester.pumpAndSettle();
    expect(saved, isNull);
    expect(find.byType(ProfileForm), findsOneWidget);
    await tester.tap(find.text('Auto update'));
    await tester.pumpAndSettle();
    expect(find.text('Update interval'), findsNothing);
    await tester.tap(find.byTooltip('Save').last);
    await tester.pumpAndSettle();
    expect(saved, {..._remote, 'autoUpdate': false});
    expect(find.byType(ProfileForm), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('normal page reveals selection while picker starts at the top and waits for success', (tester) async {
    var fail = true;
    await _mount(tester, selected: 39,
      profiles: [for (var id = 1; id <= 40; id++) {'id': id, 'name': 'Profile $id', 'remote': false}],
      handler: (call) async {
        if (call.method == 'profiles.select' && fail) throw PlatformException(code: 'apply', message: 'Apply failed');
        return null;
      });
    await _profiles(tester);
    expect(find.text('Profile 39').hitTestable(), findsOneWidget);
    expect(find.text('Profile 1').hitTestable(), findsNothing);
    openForm(tester.element(find.byType(HomePage)), const ProfilesPage(picker: true));
    await tester.pumpAndSettle();
    expect(find.text('Profile 1').hitTestable(), findsOneWidget);
    final picker = find.byWidgetPredicate((w) => w is ProfilesPage && w.picker);
    await tester.tap(find.text('Profile 1'));
    await tester.pumpAndSettle();
    expect(picker, findsOneWidget);
    fail = false;
    await tester.tap(find.text('Profile 1'));
    await tester.pumpAndSettle();
    expect(picker, findsNothing);
    expect(tester.takeException(), isNull);
  });

  for (final layout in ['Tabs', 'List']) {
    testWidgets('horizontal profiles keep the primary page and live $layout proxy groups', (tester) async {
      final events = StreamController<dynamic>.broadcast();
      final selections = <dynamic>[], tests = <dynamic>[];
      final preferences = <String, dynamic>{'proxyLayout': layout};
      final box = await _mount(tester, eventController: events, dashboard: {'status': 'Started'},
        preferences: preferences, handler: (call) async {
          if (call.method == 'preferences.set') {
            preferences.addAll(json(json(call.arguments)['value']));
            return preferences;
          }
          if (call.method == 'profiles.select') {
            final id = json(call.arguments)['id'];
            selections.add(id);
            events.add({'type': 'dashboard', 'value': {'status': 'Started', 'selectedProfileId': id}});
          }
          if (call.method == 'groups.test') tests.add(json(call.arguments)['tag']);
          return null;
        });
      events.add({'type': 'groups', 'value': [
        {'tag': 'Live selector', 'selected': 'Node A', 'selectable': true, 'items': [
          {'tag': 'Node A', 'type': 'direct'}, {'tag': 'Node B', 'type': 'direct'},
        ]},
      ]});
      await tester.pumpAndSettle();
      await _profiles(tester);
      final strip = find.byKey(const PageStorageKey('profile-strip'));
      expect(tester.widget<ListView>(strip).scrollDirection, Axis.horizontal);
      final proxies = layout == 'List' ? find.byType(ProxyListView) : find.byType(TabBarView);
      expect(tester.getBottomLeft(strip).dy, lessThan(tester.getTopLeft(proxies).dy));
      await tester.drag(strip, const Offset(-280, 0));
      await tester.pumpAndSettle();
      expect(tester.widget<PageSwitcher>(find.byType(PageSwitcher)).index, 1);
      expect(box.selectedProfileId, 1);
      expect(selections, isEmpty);
      expect(find.text('Local B').hitTestable(), findsOneWidget);
      expect(find.text('Live selector').hitTestable(), findsOneWidget);
      if (layout == 'List') {
        await tester.tap(find.byTooltip('Show more'));
        await tester.pumpAndSettle();
      }
      // The group header tests in the list, the toolbar under the tabs.
      await tester.tap(find.byTooltip('Test latency').first);
      await tester.pumpAndSettle();
      expect(tests, ['Live selector']);
      await tester.tap(find.text('Local B').hitTestable());
      await tester.pumpAndSettle();
      expect(selections, [2]);
      expect(box.selectedProfileId, 2);
      expect(tester.widget<PageSwitcher>(find.byType(PageSwitcher)).index, 1);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('stopped service shows only selected-profile nodes and edits them with revisions', (tester) async {
    final events = StreamController<dynamic>.broadcast();
    final calls = <MethodCall>[];
    await _mount(tester, eventController: events, handler: (call) async {
      calls.add(call);
      return call.method == 'nodes.get'
          ? {'origin': 'profile', 'content': '{"type":"vless","tag":"Profile node"}',
            'revision': 'node-r', 'profileId': 1, 'profileRevision': 'profile-r'} : null;
    });
    await _profiles(tester);
    expect(find.text('Add nodes'), findsOneWidget);
    expect(find.descendant(of: find.byType(ProfileCard), matching: find.byTooltip('Edit')), findsNothing);
    final selectedCard = find.byWidgetPredicate((widget) => widget is ProfileCard && widget.selected);
    expect(find.descendant(of: selectedCard, matching: find.byType(SelectionBadge)), findsOneWidget);
    events.add({'type': 'nodes', 'value': {
      'profileId': 2, 'profileRevision': 'other-r',
      'nodes': [{'tag': 'Other profile node', 'type': 'vless'}],
    }});
    await tester.pumpAndSettle();
    expect(find.text('Other profile node'), findsNothing);
    events.add({'type': 'nodes', 'value': {
      'profileId': 1, 'profileRevision': 'profile-r',
      'nodes': [{'tag': 'Profile node', 'type': 'vless', 'server': 'example.org', 'port': 443}],
    }});
    await tester.pumpAndSettle();
    expect(find.text('Add nodes'), findsNothing);
    expect(find.text('Nodes').hitTestable(), findsOneWidget);
    expect(tester.widget<ProxyNodeCard>(find.byType(ProxyNodeCard)).onTest, isNull);
    await tester.tap(find.byTooltip('More').last);
    await tester.pumpAndSettle();
    expect(find.text('Shared nodes'), findsNothing);
    await tester.tap(find.text('Update all'));
    await tester.pumpAndSettle();
    expect(calls.map((call) => call.method).where((method) => method == 'profiles.update' || method.startsWith('nodes.update')),
      ['profiles.update']);
    await tester.tap(find.text('Profile node'));
    await tester.pumpAndSettle();
    expect(find.byType(NodeEditorPage), findsOneWidget);
    expect(calls.where((call) => call.method == 'nodes.get').map((call) => json(call.arguments)),
      [{'tag': 'Profile node', 'profileId': 1}]);
    await tester.enterText(find.byType(TextField).last, '{"type":"vless","tag":"Renamed"}');
    await tester.tap(find.byTooltip('Save').last);
    await tester.pumpAndSettle();
    expect(json(calls.singleWhere((call) => call.method == 'nodes.set').arguments), {
      'tag': 'Profile node', 'content': '{"type":"vless","tag":"Renamed"}',
      'revision': 'node-r', 'profileId': 1, 'profileRevision': 'profile-r',
    });
    expect(tester.takeException(), isNull);
  });

  testWidgets('node imports retain their target and preview after a revision conflict', (tester) async {
    final events = StreamController<dynamic>.broadcast();
    final additions = <Json>[];
    var fail = true;
    await _mount(tester, eventController: events,
      nodes: {'profileId': 1, 'profileRevision': 'before-r', 'nodes': []}, handler: (call) async {
        if (call.method == 'nodes.add') {
          additions.add(json(call.arguments));
          if (fail) throw PlatformException(code: 'conflict', message: 'Configuration changed; reload it before saving');
          return {'added': 1};
        }
        return null;
      });
    const parsed = '{"nodes":[{"type":"vless","tag":"Imported","server":"example.org"}]}';
    openForm(tester.element(find.byType(HomePage)), const NodeImportPage(parsed: parsed));
    await tester.pumpAndSettle();
    expect(find.textContaining('Profile: Remote A'), findsOneWidget);
    expect(find.textContaining('Imported nodes are appended to this profile.'), findsOneWidget);
    events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'selectedProfileId': 2}});
    events.add({'type': 'nodes', 'value': {'profileId': 2, 'profileRevision': 'other-r', 'nodes': []}});
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Save').last);
    await tester.pumpAndSettle();
    expect(find.byType(NodeImportPage), findsOneWidget);
    expect(additions, [{'nodes': parsed, 'profileId': 1, 'profileRevision': 'before-r'}]);
    fail = false;
    events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'selectedProfileId': 1}});
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Save').last);
    await tester.pumpAndSettle();
    expect(additions.last, additions.first);
    expect(find.byType(NodeImportPage), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('new node saves into the selected file and runtime groups have no extra node section', (tester) async {
    final events = StreamController<dynamic>.broadcast();
    final writes = <Json>[];
    await _mount(tester, eventController: events,
      nodes: {'profileId': 1, 'profileRevision': 'file-r', 'nodes': [
        {'type': 'vless', 'tag': 'File node'},
      ]}, handler: (call) async {
        if (call.method == 'nodes.set') writes.add(json(call.arguments));
        return null;
      });
    await _profiles(tester);
    await tester.tap(find.byTooltip('Add profile').last);
    await tester.pumpAndSettle();
    final newNode = find.text('New node');
    await Scrollable.ensureVisible(tester.element(newNode), alignment: .5);
    await tester.pumpAndSettle();
    expect(newNode.hitTestable(), findsOneWidget);
    await tester.tap(newNode);
    await tester.pumpAndSettle();
    expect(find.byType(NodeEditorPage), findsOneWidget);
    expect(find.byType(TextField).hitTestable(), findsOneWidget);
    await tester.enterText(find.byType(TextField).last, '{"type":"vless","tag":"New","server":"example.org"}');
    await tester.tap(find.byTooltip('Save').last);
    await tester.pumpAndSettle();
    expect(writes, [{
      'tag': '', 'content': '{"type":"vless","tag":"New","server":"example.org"}',
      'revision': '', 'profileId': 1, 'profileRevision': 'file-r',
    }]);
    events.add({'type': 'dashboard', 'value': {'status': 'Started', 'selectedProfileId': 1}});
    events.add({'type': 'groups', 'value': [
      {'tag': 'Live selector', 'selected': 'Live node', 'selectable': true, 'items': [
        {'tag': 'Live node', 'type': 'vless'},
      ]},
    ]});
    await tester.pumpAndSettle();
    expect(find.text('Live selector').hitTestable(), findsOneWidget);
    expect(find.text('File node'), findsNothing);
    expect(find.text('Nodes').hitTestable(), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('node delete confirmation keeps the original profile and revision', (tester) async {
    final events = StreamController<dynamic>.broadcast();
    final deletions = <Json>[];
    final box = await _mount(tester, eventController: events,
      nodes: {'profileId': 1, 'profileRevision': 'file-r', 'nodes': [
        {'type': 'vless', 'tag': 'Delete me'},
      ]}, handler: (call) async {
        if (call.method == 'nodes.remove') {
          deletions.add(json(call.arguments));
          throw PlatformException(code: 'conflict', message: 'Selected profile changed');
        }
        return null;
      });
    await _profiles(tester);
    final removed = removeNodes(tester.element(find.byType(ProfilesPage)), ['Delete me']);
    await tester.pumpAndSettle();
    events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'selectedProfileId': 2}});
    events.add({'type': 'nodes', 'value': {'profileId': 2, 'profileRevision': 'other-r', 'nodes': []}});
    await tester.pumpAndSettle();
    await tester.tap(find.text('Delete'));
    await tester.pumpAndSettle();
    expect(await removed, isFalse);
    expect(box.selectedProfileId, 2);
    expect(deletions, [{'tags': ['Delete me'], 'profileId': 1, 'profileRevision': 'file-r'}]);
    expect(tester.takeException(), isNull);
  });

  for (final picker in [false, true]) {
    testWidgets('default profile remains available alongside existing files (picker: $picker)', (tester) async {
      Json? saved;
      await _mount(tester, profiles: [_remote, {..._default, if (picker) 'name': 'Default profile'}], handler: (call) async {
        if (call.method == 'profiles.save') saved = json(call.arguments);
        return null;
      });
      if (picker) {
        openForm(tester.element(find.byType(HomePage)), const ProfilesPage(picker: true));
        await tester.pumpAndSettle();
      } else {
        await _profiles(tester);
      }
      final defaultCard = find.byWidgetPredicate((w) => w is ProfileCard && w.profile['default'] == true);
      await Scrollable.ensureVisible(tester.element(defaultCard), alignment: .5);
      await tester.pumpAndSettle();
      expect(find.text('Profile file').hitTestable(), findsOneWidget);
      expect(tester.widget<ProfileCard>(defaultCard).menuItems.last.onPressed, isNull);
      tester.widget<ProfileCard>(defaultCard).menuItems.first.onPressed!();
      await tester.pumpAndSettle();
      await tester.tap(find.byTooltip('Edit').last);
      await tester.pumpAndSettle();
      expect(find.byType(ProfileForm), findsOneWidget);
      expect(find.text('Remote').hitTestable(), findsNothing);
      await tester.enterText(find.byType(TextFormField).first, 'My default');
      await tester.tap(find.byTooltip('Save').last);
      await tester.pumpAndSettle();
      expect(saved?['name'], 'My default');
      expect(saved?['default'], isTrue);
      expect(saved?['remote'], isFalse);
      expect(tester.takeException(), isNull);
    });
  }

  for (final (size, scale) in [(const Size(360, 800), 1.0), (const Size(360, 800), 1.4), (const Size(800, 1000), 1.0)]) {
    testWidgets('profile card borders align with proxy group edges at $size scale=$scale', (tester) async {
      tester.platformDispatcher.textScaleFactorTestValue = scale;
      addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
      final events = StreamController<dynamic>.broadcast();
      await _mount(tester, size: size, eventController: events, dashboard: {'status': 'Started'});
      events.add({'type': 'groups', 'value': [
        {'tag': 'Selector', 'selected': 'Node 0', 'selectable': true, 'items': [
          for (var i = 0; i < 12; i++) {'tag': 'Node $i', 'type': 'vless'},
        ]},
      ]});
      await tester.pumpAndSettle();
      await _profiles(tester);
      final selectedCard = find.byWidgetPredicate((w) => w is ProfileCard && w.selected);
      final profileBounds = tester.getRect(selectedCard);
      final proxies = find.byType(ProxyNodeCard);
      final proxyBounds = proxies.evaluate().map((element) => tester.getRect(find.byWidget(element.widget)))
        .reduce((before, next) => before.expandToInclude(next));
      expect(profileBounds.left, closeTo(proxyBounds.left, .01));
      expect(profileBounds.right, closeTo(proxyBounds.right, .01));
      expect(profileBounds.width, tester.getSize(find.byKey(const PageStorageKey('profile-strip'))).width - 32);
      expect(tester.takeException(), isNull);
    });
  }

  for (final picker in [false, true]) {
    testWidgets('selected profiles cannot be deleted, including stale menus and confirmations (picker: $picker)', (tester) async {
      final events = StreamController<dynamic>.broadcast();
      final deletions = <dynamic>[];
      await _mount(tester, eventController: events, handler: (call) async {
        if (call.method == 'profiles.delete') deletions.add(json(call.arguments)['id']);
        return null;
      });
      if (picker) {
        openForm(tester.element(find.byType(HomePage)), const ProfilesPage(picker: true));
        await tester.pumpAndSettle();
      } else {
        await _profiles(tester);
      }
      ProfileCard card(int id) => tester.widget<ProfileCard>(
        find.byWidgetPredicate((w) => w is ProfileCard && w.profile['id'] == id));
      final staleDelete = card(2).menuItems.last.onPressed!;
      expect(card(1).menuItems.last.onPressed, isNull);
      for (final status in ['Starting', 'Started', 'Stopping', 'Stopped']) {
        events.add({'type': 'dashboard', 'value': {'status': status, 'selectedProfileId': 2}});
        await tester.pump();
        await tester.pump(const Duration(milliseconds: 300));
        expect(card(2).menuItems.last.onPressed, isNull);
        expect(card(1).menuItems.last.onPressed, isNotNull);
        staleDelete();
        await tester.pump();
        expect(find.text('Delete this profile?'), findsNothing);
        expect(deletions, isEmpty);
      }

      events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'selectedProfileId': 1}});
      await tester.pumpAndSettle();
      card(2).menuItems.last.onPressed!();
      await tester.pumpAndSettle();
      expect(find.text('Delete this profile?'), findsOneWidget);
      events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'selectedProfileId': 2}});
      await tester.pumpAndSettle();
      await tester.tap(find.text('Delete'));
      await tester.pumpAndSettle();
      expect(deletions, isEmpty);

      events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'selectedProfileId': 1}});
      await tester.pumpAndSettle();
      card(2).menuItems.last.onPressed!();
      await tester.pumpAndSettle();
      await tester.tap(find.text('Delete'));
      await tester.pumpAndSettle();
      expect(deletions, [2]);
      expect(tester.takeException(), isNull);
    });

    for (final (blockedStatus, resumedStatus) in [('Starting', 'Started'), ('Starting', 'Stopped'), ('Stopping', 'Stopped')]) {
      testWidgets('profile selection waits for stable service state (picker: $picker, $blockedStatus to $resumedStatus)', (tester) async {
        final events = StreamController<dynamic>.broadcast();
        final selections = <dynamic>[];
        final box = await _mount(tester, eventController: events, handler: (call) async {
          if (call.method == 'profiles.select') selections.add(json(call.arguments)['id']);
          return null;
        });
        if (picker) {
          openForm(tester.element(find.byType(HomePage)), const ProfilesPage(picker: true));
          await tester.pumpAndSettle();
        } else {
          await _profiles(tester);
        }
        final localCard = find.byWidgetPredicate((w) => w is ProfileCard && w.profile['id'] == 2);
        await Scrollable.ensureVisible(tester.element(localCard), alignment: .5);
        await tester.pumpAndSettle();
        final stalePress = tester.widget<ProfileCard>(localCard).onPressed!;
        events.add({'type': 'dashboard', 'value': {'status': blockedStatus, 'selectedProfileId': 1}});
        // The starting indicator animates continuously while the picker is open.
        await tester.pump();
        await tester.pump(const Duration(milliseconds: 300));
        await tester.tap(find.text('Local B').hitTestable());
        stalePress();
        await tester.pump();
        expect(selections, isEmpty);
        expect(box.selectedProfileId, 1);
        expect(localCard, findsOneWidget);
        events.add({'type': 'dashboard', 'value': {'status': resumedStatus, 'selectedProfileId': 1}});
        await tester.pumpAndSettle();
        await tester.tap(find.text('Local B').hitTestable());
        await tester.pumpAndSettle();
        expect(selections, [2]);
        if (picker) expect(find.byWidgetPredicate((w) => w is ProfilesPage && w.picker), findsNothing);
        expect(tester.takeException(), isNull);
      });
    }
  }

  testWidgets('sort uses final indices and keeps the draft after a native conflict', (tester) async {
    var fail = true;
    final orders = <dynamic>[];
    await _mount(tester, handler: (call) async {
      if (call.method == 'profiles.reorder') {
        orders.add(json(call.arguments)['ids']);
        if (fail) throw PlatformException(code: 'conflict', message: 'Profile list changed; refresh and retry');
      }
      return null;
    });
    await _profiles(tester);
    await tester.tap(find.byTooltip('More').last);
    await tester.pumpAndSettle();
    expect(find.text('Node sort'), findsOneWidget);
    await tester.tap(find.text('Reorder profiles'));
    await tester.pumpAndSettle();
    tester.widget<PrismReorderableListView>(find.byType(PrismReorderableListView)).onReorderItem(0, 1);
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Save').last);
    await tester.pumpAndSettle();
    expect(orders, [[2, 1]]);
    expect(find.byType(ProfileOrderPage), findsOneWidget);
    fail = false;
    await tester.tap(find.byTooltip('Save').last);
    await tester.pumpAndSettle();
    expect(orders, [[2, 1], [2, 1]]);
    expect(find.byType(ProfileOrderPage), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('remote sessions expose remote groups without local profile actions', (tester) async {
    final events = StreamController<dynamic>.broadcast();
    final selections = <Json>[];
    await _mount(tester, remoteSession: true, eventController: events,
      dashboard: {'profileName': 'Private local profile'}, profiles: [{'id': 1, 'name': 'Private local profile'}],
      handler: (call) async {
        if (call.method == 'groups.select') selections.add(json(call.arguments));
        return null;
      });
    expect(find.text('Remote configuration'), findsOneWidget);
    expect(find.text('Private local profile'), findsNothing);
    await _profiles(tester);
    expect(find.text('Disconnect remote session to edit local profiles'), findsOneWidget);
    expect(find.byType(ProfileCard), findsNothing);
    expect(find.byType(AddProfileMenu), findsNothing);
    expect(find.byTooltip('Add profile'), findsNothing);
    expect(find.byTooltip('Update all'), findsNothing);
    events.add({'type': 'groups', 'value': [
      {'tag': 'Remote selector', 'selected': 'Remote A', 'selectable': true, 'items': [
        {'tag': 'Remote A', 'type': 'direct'}, {'tag': 'Remote B', 'type': 'direct'},
      ]},
    ]});
    await tester.pumpAndSettle();
    expect(find.text('Remote selector').hitTestable(), findsOneWidget);
    expect(find.text('Private local profile'), findsNothing);
    await tester.tap(find.text('Remote B'));
    await tester.pumpAndSettle();
    expect(selections, [{'group': 'Remote selector', 'tag': 'Remote B'}]);
    expect(tester.takeException(), isNull);
  });

  test('remote profile links preserve names in the sing-box fragment', () {
    const name = '工作 #1 & home';
    const url = 'https://example.com/profile.json?token=a%26b';
    final link = Uri.parse(remoteProfileLink({'name': name, 'url': url}));
    expect(link.scheme, 'sing-box');
    expect(link.host, 'import-remote-profile');
    expect(Uri.decodeComponent(link.fragment), name);
    expect(link.queryParameters, {'url': url});
  });

  testWidgets('new profiles save the entered name and original sing-box content', (tester) async {
    Json? saved;
    await _mount(tester, handler: (call) async {
      if (call.method == 'profiles.save') saved = json(call.arguments);
      return null;
    });
    openForm(tester.element(find.byType(HomePage)),
      const ProfileForm(initial: {'content': '{"log":{"level":"info"}}'}));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextFormField).first, 'My profile');
    await tester.tap(find.byTooltip('Save').last);
    await tester.pumpAndSettle();
    expect(saved?['name'], 'My profile');
    expect(saved?['content'], '{"log":{"level":"info"}}');
    expect(find.byType(ProfileForm), findsNothing);
  });

  testWidgets('URL imports keep a source in the selected file without creating a profile', (tester) async {
    Json? saved;
    final calls = <String>[];
    await _mount(tester, profiles: [_remote, _local, _default], selected: 3,
      nodes: {'profileId': 3, 'profileRevision': 'default-r', 'nodes': []}, handler: (call) async {
      calls.add(call.method);
      if (call.method == 'subscriptions.import') {
        saved = json(call.arguments);
        return {'added': 2, 'source': {'id': 'source-a'}};
      }
      return null;
    });
    await _profiles(tester);
    await tester.tap(find.byTooltip('Add profile'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Import from URL'));
    await tester.pumpAndSettle();
    expect(find.byType(SubscriptionForm), findsOneWidget);
    expect(find.byType(ProfileForm), findsNothing);
    expect(find.descendant(of: find.byType(SubscriptionForm), matching: find.text('Profile file')), findsOneWidget);
    expect(find.text('Supports Clash, sing-box JSON, node links and Base64. Only proxy nodes are imported from the URL; updates keep other profile settings.'), findsOneWidget);
    expect(find.text('This URL must serve a sing-box JSON configuration.'), findsNothing);
    const url = 'https://example.org/subscribe?token=a%26b';
    await tester.enterText(find.byType(TextFormField).at(0), '  Node subscription  ');
    await tester.enterText(find.byType(TextFormField).at(1), ' $url ');
    await tester.enterText(find.byType(TextFormField).at(2), '120');
    await tester.tap(find.byTooltip('Save').last);
    await tester.pumpAndSettle();
    expect(saved, {'profileId': 3, 'profileRevision': 'default-r', 'name': 'Node subscription', 'url': url,
      'autoUpdate': true, 'interval': 120});
    expect(saved?.containsKey('content'), isFalse);
    expect(calls, isNot(contains('profiles.save')));
    expect(find.byType(SubscriptionForm), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('failed URL imports preserve subscription fields and allow retry', (tester) async {
    var fail = true;
    final saves = <Json>[];
    await _mount(tester, selected: 2, nodes: {'profileId': 2, 'profileRevision': 'local-r', 'nodes': []}, handler: (call) async {
      if (call.method == 'subscriptions.import') {
        saves.add(json(call.arguments));
        if (fail) throw PlatformException(code: 'download', message: 'HTTP 503 Service Unavailable');
        return {'added': 1, 'source': {'id': 'retry-source'}};
      }
      return null;
    });
    openSubscriptionImport(tester.element(find.byType(HomePage)));
    await tester.pumpAndSettle();
    const url = 'https://example.org/nodes';
    await tester.enterText(find.byType(TextFormField).at(0), 'Retry subscription');
    await tester.enterText(find.byType(TextFormField).at(1), url);
    await tester.enterText(find.byType(TextFormField).at(2), '180');
    await tester.tap(find.text('Auto update'));
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Save').last);
    await tester.pumpAndSettle();
    const expected = {'profileId': 2, 'profileRevision': 'local-r', 'name': 'Retry subscription', 'url': url,
      'autoUpdate': false, 'interval': 180};
    expect(saves, [expected]);
    expect(find.byType(SubscriptionForm), findsOneWidget);
    expect(tester.widget<TextFormField>(find.byType(TextFormField).at(0)).controller!.text, 'Retry subscription');
    expect(tester.widget<TextFormField>(find.byType(TextFormField).at(1)).controller!.text, url);
    expect(find.text('Update interval'), findsNothing);
    fail = false;
    await tester.tap(find.byTooltip('Save').last);
    await tester.pumpAndSettle();
    expect(saves, [expected, expected]);
    expect(find.byType(SubscriptionForm), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('URL intents retain the opening file while resolving its revision and on conflict', (tester) async {
    final events = StreamController<dynamic>.broadcast();
    final snapshot = Completer<Json>();
    final reads = <Json>[], imports = <Json>[];
    await _mount(tester, selected: 2, eventController: events, handler: (call) async {
      if (call.method == 'nodes.list') { reads.add(json(call.arguments)); return snapshot.future; }
      if (call.method == 'subscriptions.import') {
        imports.add(json(call.arguments));
        throw PlatformException(code: 'conflict', message: 'Selected profile changed; reopen the import');
      }
      return null;
    });
    openImport(tester.element(find.byType(HomePage)), {'name': 'Pinned source', 'url': 'https://example.org/source'});
    await tester.pump();
    events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'selectedProfileId': 1}});
    events.add({'type': 'nodes', 'value': {'profileId': 1, 'profileRevision': 'remote-r', 'nodes': []}});
    await tester.pump();
    snapshot.complete({'profileId': 2, 'profileRevision': 'opening-r', 'nodes': []});
    await tester.pumpAndSettle();
    expect(reads, [{'profileId': 2}]);
    expect(find.descendant(of: find.byType(SubscriptionForm), matching: find.text('Local B')), findsOneWidget);
    await tester.tap(find.byTooltip('Save').last);
    await tester.pumpAndSettle();
    expect(imports, [{'profileId': 2, 'profileRevision': 'opening-r', 'name': 'Pinned source',
      'url': 'https://example.org/source', 'autoUpdate': true, 'interval': 60}]);
    expect(find.byType(SubscriptionForm), findsOneWidget);
    expect(tester.widget<TextFormField>(find.byType(TextFormField).first).controller!.text, 'Pinned source');
    expect(tester.takeException(), isNull);
  });

  testWidgets('update all processes sources in local and default files alongside legacy remote profiles', (tester) async {
    final updates = <MethodCall>[];
    await _mount(tester, profiles: [_remote,
      {..._local, 'subscriptions': [
        {'id': 'source-a'}, {'id': 'source-b'},
      ]}, {..._default, 'subscriptions': [{'id': 'source-c'}]}], selected: 3, handler: (call) async {
      if (call.method == 'profiles.update' || call.method == 'subscriptions.update') updates.add(call);
      return null;
    });
    await _profiles(tester);
    final defaultCard = tester.widget<ProfileCard>(find.byWidgetPredicate((w) => w is ProfileCard && w.profile['id'] == 3));
    expect(defaultCard.menuItems.map((item) => item.label), contains('Update'));
    await tester.tap(find.byTooltip('More').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Update all'));
    await tester.pumpAndSettle();
    expect(updates.map((call) => call.method), ['profiles.update', 'subscriptions.update', 'subscriptions.update', 'subscriptions.update']);
    expect(updates.map((call) => json(call.arguments)), [
      {'id': 1}, {'profileId': 2, 'id': 'source-a'}, {'profileId': 2, 'id': 'source-b'}, {'profileId': 3, 'id': 'source-c'},
    ]);
    expect(tester.takeException(), isNull);
  });

  testWidgets('subscription management edits metadata and updates or removes only the chosen source', (tester) async {
    var sources = <Json>[
      {'id': 'source-a', 'name': 'Source A', 'url': 'https://example.org/a', 'autoUpdate': true,
        'interval': 180, 'updatedAt': 0, 'tags': ['A node']},
      {'id': 'source-b', 'name': 'Source B', 'url': 'https://example.org/b', 'autoUpdate': false,
        'interval': 60, 'updatedAt': 0, 'tags': ['B node']},
    ];
    final calls = <MethodCall>[];
    await _mount(tester, profiles: [_local, _default], selected: 3, handler: (call) async {
      calls.add(call);
      switch (call.method) {
        case 'subscriptions.list': return sources;
        case 'subscriptions.save':
          final metadata = json(call.arguments);
          sources = [for (final source in sources) if (source['id'] == metadata['id']) {...source, ...metadata} else source];
        case 'subscriptions.delete':
          sources = sources.where((source) => source['id'] != json(call.arguments)['id']).toList();
      }
      return null;
    });
    openForm(tester.element(find.byType(HomePage)), const ProfileDetailsPage(profile: _default));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Node subscriptions'));
    await tester.pumpAndSettle();
    expect(find.byType(SubscriptionsPage), findsOneWidget);
    expect(find.text('Source A'), findsOneWidget);
    expect(find.text('Source B'), findsOneWidget);
    expect(json(calls.singleWhere((call) => call.method == 'subscriptions.list').arguments), {'profileId': 3});
    BoxMenu sourceMenu() => tester.widget<BoxMenu>(find.descendant(of: find.byType(SubscriptionsPage), matching: find.byType(BoxMenu)).first);
    sourceMenu().items.singleWhere((item) => item.label == 'Edit').onPressed!();
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextFormField).at(0), 'Edited A');
    await tester.enterText(find.byType(TextFormField).at(2), '14');
    await tester.tap(find.byTooltip('Save').last);
    await tester.pumpAndSettle();
    expect(calls.where((call) => call.method == 'subscriptions.save'), isEmpty);
    await tester.tap(find.text('Auto update').last);
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Save').last);
    await tester.pumpAndSettle();
    expect(json(calls.singleWhere((call) => call.method == 'subscriptions.save').arguments), {
      'profileId': 3, 'id': 'source-a', 'name': 'Edited A', 'url': 'https://example.org/a', 'autoUpdate': false, 'interval': 180,
    });
    expect(find.byType(SubscriptionForm), findsNothing);
    expect(find.text('Edited A'), findsOneWidget);
    sourceMenu().items.singleWhere((item) => item.label == 'Update').onPressed!();
    await tester.pumpAndSettle();
    expect(json(calls.singleWhere((call) => call.method == 'subscriptions.update').arguments), {'profileId': 3, 'id': 'source-a'});
    sourceMenu().items.singleWhere((item) => item.label == 'Delete').onPressed!();
    await tester.pumpAndSettle();
    expect(find.text('Its nodes will stay in this profile as local nodes.'), findsOneWidget);
    expect(calls.where((call) => call.method == 'subscriptions.delete'), isEmpty);
    await tester.tap(find.text('Delete').hitTestable());
    await tester.pumpAndSettle();
    expect(json(calls.singleWhere((call) => call.method == 'subscriptions.delete').arguments), {'profileId': 3, 'id': 'source-a'});
    expect(find.text('Edited A'), findsNothing);
    expect(find.text('Source B'), findsOneWidget);
    expect(calls.map((call) => call.method), isNot(contains('nodes.remove')));
    expect(calls.map((call) => call.method), isNot(contains('profiles.save')));
    expect(tester.takeException(), isNull);
  });

  testWidgets('subscription lists refresh after native metadata events and hide during remote sessions', (tester) async {
    final events = StreamController<dynamic>.broadcast();
    var sources = <Json>[];
    var reads = 0;
    await _mount(tester, profiles: [_default], selected: 3, eventController: events, handler: (call) async {
      if (call.method == 'subscriptions.list') { reads++; return sources; }
      return null;
    });
    openForm(tester.element(find.byType(HomePage)), const SubscriptionsPage(profile: _default));
    await tester.pumpAndSettle();
    expect(find.text('No node subscriptions'), findsOneWidget);
    sources = [{'id': 'automatic-source', 'name': 'Updated source', 'url': 'https://example.org/nodes', 'tags': []}];
    events.add({'type': 'profiles', 'value': [{..._default, 'subscriptions': sources}]});
    await tester.pumpAndSettle();
    expect(find.text('Updated source'), findsOneWidget);
    expect(reads, 2);
    events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'selectedProfileId': 3, 'remote': 'Server', 'remoteConnected': true}});
    await tester.pumpAndSettle();
    expect(find.text('Updated source'), findsNothing);
    expect(find.descendant(of: find.byType(SubscriptionsPage), matching: find.byTooltip('Import from URL')), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('subscription lists discard late refreshes and session responses while retaining their file', (tester) async {
    final events = StreamController<dynamic>.broadcast();
    final oldRefresh = Completer<List<Json>>(), oldSession = Completer<List<Json>>();
    final reads = <Json>[];
    final latest = [{'id': 'latest', 'name': 'Latest source', 'url': 'https://example.org/latest', 'tags': []}];
    await _mount(tester, profiles: [_local, _default], selected: 3, eventController: events, handler: (call) async {
      if (call.method == 'subscriptions.list') {
        reads.add(json(call.arguments));
        if (reads.length == 2) return oldRefresh.future;
        if (reads.length == 4) return oldSession.future;
        return latest;
      }
      return null;
    });
    openForm(tester.element(find.byType(HomePage)), const SubscriptionsPage(profile: _default));
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Refresh').last);
    await tester.pump();
    events.add({'type': 'profiles', 'value': [_local, {..._default, 'subscriptions': latest}]});
    await tester.pumpAndSettle();
    oldRefresh.complete([{'id': 'stale', 'name': 'Stale refresh', 'tags': []}]);
    await tester.pumpAndSettle();
    expect(find.text('Latest source'), findsOneWidget);
    expect(find.text('Stale refresh'), findsNothing);
    await tester.tap(find.byTooltip('Refresh').last);
    await tester.pump();
    events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'selectedProfileId': 2, 'remote': 'Server'}});
    events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'selectedProfileId': 2}});
    oldSession.complete([{'id': 'stale-session', 'name': 'Stale session', 'tags': []}]);
    await tester.pumpAndSettle();
    expect(reads, List.filled(5, {'profileId': 3}));
    expect(find.text('Latest source'), findsOneWidget);
    expect(find.text('Stale session'), findsNothing);
    final page = tester.widget<BoxPage>(find.descendant(of: find.byType(SubscriptionsPage), matching: find.byType(BoxPage)));
    expect(page.actions.singleWhere((action) => action.tooltip == 'Import from URL').onPressed, isNull);
    expect(tester.takeException(), isNull);
  });

  testWidgets('subscription writes serialize across sources and late completion after leaving cannot refresh another file', (tester) async {
    final events = StreamController<dynamic>.broadcast();
    final first = Completer<void>(), second = Completer<void>();
    final updates = <Json>[], reads = <Json>[];
    const sources = [
      {'id': 'source-a', 'name': 'Source A', 'url': 'https://example.org/a', 'tags': []},
      {'id': 'source-b', 'name': 'Source B', 'url': 'https://example.org/b', 'tags': []},
    ];
    await _mount(tester, profiles: [_local, _default], selected: 3, eventController: events, handler: (call) async {
      if (call.method == 'subscriptions.list') { reads.add(json(call.arguments)); return sources; }
      if (call.method == 'subscriptions.update') {
        updates.add(json(call.arguments));
        return updates.length == 1 ? first.future : second.future;
      }
      return null;
    });
    openForm(tester.element(find.byType(HomePage)), const SubscriptionsPage(profile: _default));
    await tester.pumpAndSettle();
    List<BoxMenu> menus() => tester.widgetList<BoxMenu>(find.descendant(of: find.byType(SubscriptionsPage), matching: find.byType(BoxMenu))).toList();
    final staleMenus = menus();
    staleMenus.first.items.singleWhere((item) => item.label == 'Update').onPressed!();
    await tester.pump();
    expect(menus().expand((menu) => menu.items).every((item) => item.onPressed == null), isTrue);
    final page = tester.widget<BoxPage>(find.descendant(of: find.byType(SubscriptionsPage), matching: find.byType(BoxPage)));
    expect(page.actions.singleWhere((action) => action.tooltip == 'Import from URL').onPressed, isNull);
    staleMenus.last.items.singleWhere((item) => item.label == 'Update').onPressed!();
    await tester.pump();
    expect(updates, [{'profileId': 3, 'id': 'source-a'}]);
    events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'selectedProfileId': 2}});
    first.complete();
    await tester.pumpAndSettle();
    expect(reads, [{'profileId': 3}, {'profileId': 3}]);
    menus().last.items.singleWhere((item) => item.label == 'Update').onPressed!();
    await tester.pump();
    tester.element(find.byType(SubscriptionsPage)).safeNestedPop();
    await tester.pumpAndSettle();
    expect(find.byType(SubscriptionsPage), findsNothing);
    second.complete();
    await tester.pumpAndSettle();
    expect(updates, [{'profileId': 3, 'id': 'source-a'}, {'profileId': 3, 'id': 'source-b'}]);
    expect(reads, [{'profileId': 3}, {'profileId': 3}]);
    expect(tester.takeException(), isNull);
  });

  testWidgets('profile detail updates reject repeated presses and stop queued sources after a session change', (tester) async {
    final events = StreamController<dynamic>.broadcast();
    final pending = Completer<void>();
    final updates = <Json>[];
    await _mount(tester, profiles: [{..._default, 'subscriptions': [{'id': 'source-a'}, {'id': 'source-b'}]}],
      selected: 3, eventController: events, handler: (call) async {
      if (call.method == 'subscriptions.update') {
        updates.add(json(call.arguments));
        if (updates.length == 1) return pending.future;
      }
      return null;
    });
    openForm(tester.element(find.byType(HomePage)), const ProfileDetailsPage(profile: _default));
    await tester.pumpAndSettle();
    IconButtonData updateAction() => tester.widget<BoxPage>(find.descendant(of: find.byType(ProfileDetailsPage),
      matching: find.byType(BoxPage))).actions.singleWhere((action) => action.tooltip == 'Update');
    final staleAction = updateAction();
    staleAction.onPressed!();
    staleAction.onPressed!();
    await tester.pump();
    expect(updateAction().onPressed, isNull);
    expect(updateAction().isLoading, isTrue);
    expect(updates, [{'profileId': 3, 'id': 'source-a'}]);
    events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'selectedProfileId': 3, 'remote': 'Server'}});
    events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'selectedProfileId': 3}});
    pending.complete();
    await tester.pumpAndSettle();
    expect(updates, [{'profileId': 3, 'id': 'source-a'}]);
    expect(updateAction().isLoading, isFalse);
    updateAction().onPressed!();
    await tester.pumpAndSettle();
    expect(updates, [
      {'profileId': 3, 'id': 'source-a'}, {'profileId': 3, 'id': 'source-a'}, {'profileId': 3, 'id': 'source-b'},
    ]);
    expect(tester.takeException(), isNull);
  });

  testWidgets('update all stops queued files when a remote session was entered and left', (tester) async {
    final events = StreamController<dynamic>.broadcast();
    final pending = Completer<void>();
    final updates = <String>[];
    await _mount(tester, profiles: [_remote, {..._local, 'subscriptions': [{'id': 'source-a'}]}],
      eventController: events, handler: (call) async {
      if (call.method == 'profiles.update') { updates.add(call.method); return pending.future; }
      if (call.method == 'subscriptions.update') updates.add(call.method);
      return null;
    });
    await _profiles(tester);
    await tester.tap(find.byTooltip('More').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Update all'));
    await tester.pump();
    events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'selectedProfileId': 1, 'remote': 'Server'}});
    events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'selectedProfileId': 1}});
    pending.complete();
    await tester.pumpAndSettle();
    expect(updates, ['profiles.update']);
    expect(tester.takeException(), isNull);
  });

  testWidgets('subscription drafts can be canceled without writes and saving blocks dismissal until completion', (tester) async {
    final pending = Completer<Json>();
    final imports = <Json>[];
    await _mount(tester, selected: 2, nodes: {'profileId': 2, 'profileRevision': 'local-r', 'nodes': []}, handler: (call) async {
      if (call.method == 'subscriptions.import') { imports.add(json(call.arguments)); return pending.future; }
      return null;
    });
    const initial = {'name': 'Initial source', 'url': 'https://example.org/nodes'};
    openSubscriptionImport(tester.element(find.byType(HomePage)), initial);
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextFormField).first, 'Unsaved draft');
    await tester.pump();
    final discard = SheetDismissScope.of(tester.element(find.byType(SubscriptionForm)))!.value!();
    await tester.pumpAndSettle();
    expect(find.text('Discard changes?'), findsOneWidget);
    expect(imports, isEmpty);
    await tester.tap(find.text('Discard').hitTestable());
    await tester.pumpAndSettle();
    await discard;
    expect(find.byType(SubscriptionForm), findsNothing);
    expect(imports, isEmpty);
    openSubscriptionImport(tester.element(find.byType(HomePage)), initial);
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Save').last);
    await tester.pump();
    final guard = tester.widget<BoxEditGuard>(find.descendant(of: find.byType(SubscriptionForm), matching: find.byType(BoxEditGuard)));
    expect(guard.busy, isTrue);
    await SheetDismissScope.of(tester.element(find.byType(SubscriptionForm)))!.value!();
    await tester.pump();
    expect(find.byType(SubscriptionForm), findsOneWidget);
    expect(find.text('Discard changes?'), findsNothing);
    pending.complete({'added': 1, 'source': {'id': 'source-a'}});
    await tester.pumpAndSettle();
    expect(find.byType(SubscriptionForm), findsNothing);
    expect(imports, [{'profileId': 2, 'profileRevision': 'local-r', 'name': 'Initial source',
      'url': 'https://example.org/nodes', 'autoUpdate': true, 'interval': 60}]);
    expect(tester.takeException(), isNull);
  });

  testWidgets('binary subscription metadata survives the import form', (tester) async {
    Json? saved;
    await _mount(tester, handler: (call) async {
      if (call.method == 'profiles.save') saved = json(call.arguments);
      return null;
    });
    const imported = {'name': 'Imported subscription', 'content': '{}', 'remote': true,
      'url': 'https://example.com/config.json', 'autoUpdate': false, 'interval': 120, 'updatedAt': 1700000000000};
    openForm(tester.element(find.byType(HomePage)), const ProfileForm(initial: imported));
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Save').last);
    await tester.pumpAndSettle();
    expect(saved, imported);
    expect(find.byType(ProfileForm), findsNothing);
  });
}
