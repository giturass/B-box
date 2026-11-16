import 'dart:async';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/components/profile_card.dart';
import 'package:b_box_ui/components/proxy_card.dart';
import 'package:b_box_ui/components/proxy_list.dart';
import 'package:b_box_ui/components/page_switcher.dart';
import 'package:b_box_ui/pages/nodes.dart';
import 'package:b_box_ui/pages/profiles.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

const _remote = {'id': 1, 'name': 'Remote A', 'remote': true,
  'url': 'https://example.org/config.json', 'autoUpdate': true, 'interval': 120, 'updatedAt': 1700000000000};
const _local = {'id': 2, 'name': 'Local B', 'remote': false};
const _default = {'id': 3, 'name': 'Default profile', 'remote': false, 'default': true};

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
  await tester.tap(find.text('Profiles').last);
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
    expect(find.descendant(of: find.byType(ProfileCard), matching: find.byTooltip('Edit')), findsOneWidget);
    expect(tester.widgetList<ProfileCard>(find.byType(ProfileCard)).map((card) => card.onEdit != null), [true, false]);
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
    await tester.tap(find.text('New node'));
    await tester.pumpAndSettle();
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
      await _mount(tester, profiles: const [_remote, _default], handler: (call) async {
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
      expect(find.text('Default profile').hitTestable(), findsOneWidget);
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
