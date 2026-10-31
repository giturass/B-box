import 'dart:async';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/pages/proxies.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/components/proxy_card.dart';
import 'package:b_box_ui/components/proxy_list.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'support/errors.dart';

void main() {
  testWidgets('list groups fold per profile, build nodes lazily and locate the selected row', (tester) async {
    reportFlutterErrors();
    tester.view.physicalSize = const Size(360, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final events = StreamController<dynamic>.broadcast();
    const channel = MethodChannel('test.proxy-list');
    final preferences = <String, dynamic>{'locale': 'en', 'proxyLayout': 'List'};
    final calls = <MethodCall>[];
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(channel, (call) async {
      calls.add(call);
      if (call.method == 'ready') return {'version': 'test', 'coreVersion': 'test', 'preferences': preferences,
        'dashboard': {'status': 'Started', 'selectedProfileId': 1}};
      if (call.method == 'preferences.set') {
        preferences.addAll(json((call.arguments as Map)['value']));
        return preferences;
      }
      return null;
    });
    addTearDown(() => messenger.setMockMethodCallHandler(channel, null));
    await tester.pumpWidget(BoxApp(controller: BoxController(channel: channel, events: events.stream)));
    await tester.pumpAndSettle();
    events.add({'type': 'groups', 'value': [{'tag': 'Large group', 'type': 'Selector', 'selectable': true, 'selected': 'Node 150',
      'items': [for (var index = 0; index < 200; index++) {'tag': 'Node $index', 'type': 'Direct'}]}]});
    await tester.pumpAndSettle();
    await tester.tap(find.text('Proxies').last);
    await tester.pumpAndSettle();
    expect(find.byType(ProxyListView), findsOneWidget);
    expect(find.byType(ProxyNodeCard), findsNothing);
    await tester.tap(find.byTooltip('Show more'));
    await tester.pumpAndSettle();
    expect(json(preferences['proxyUnfolded'])['local:1'], ['Large group']);
    expect(find.byType(ProxyNodeCard).evaluate().length, inInclusiveRange(1, 40));
    final selected = find.descendant(of: find.byType(ProxyNodeCard), matching: find.text('Node 150'));
    expect(selected, findsNothing);
    await tester.tap(find.byTooltip('Scroll to selected'));
    await tester.pumpAndSettle();
    expect(selected.hitTestable(), findsOneWidget);
    expect(find.byType(ProxyGroupHeader).hitTestable(), findsOneWidget);
    expect(find.text('Node 199'), findsNothing);
    await tester.tap(find.byTooltip('Test latency').first);
    await tester.pumpAndSettle();
    expect((calls.lastWhere((call) => call.method == 'groups.test').arguments as Map)['tag'], 'Large group');
    events.add({'type': 'dashboard', 'value': {'status': 'Started', 'selectedProfileId': 2}});
    await tester.pumpAndSettle();
    expect(find.byType(ProxyNodeCard), findsNothing);
    events.add({'type': 'dashboard', 'value': {'status': 'Started', 'selectedProfileId': 1}});
    await tester.pumpAndSettle();
    expect(find.byType(ProxyNodeCard).evaluate(), isNotEmpty);
    await tester.tap(find.byTooltip('Show less'));
    await tester.pumpAndSettle();
    expect(find.byType(ProxyNodeCard), findsNothing);
    expect(json(preferences['proxyUnfolded'])['local:1'], isEmpty);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
    await events.close();
  });

  testWidgets('automatic groups cannot send manual selection commands', (tester) async {
    final calls = <MethodCall>[];
    final events = StreamController<dynamic>.broadcast();
    const channel = MethodChannel('test.automatic');
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger.setMockMethodCallHandler(channel, (call) async {
      calls.add(call);
      return call.method == 'ready' ? {'version': 'test', 'coreVersion': 'test', 'dashboard': {'status': 'Started'}} : null;
    });
    final box = BoxController(channel: channel, events: events.stream);
    await tester.pumpWidget(BoxApp(controller: box));
    await tester.pumpAndSettle();
    events.add({'type': 'groups', 'value': [{'tag': 'Auto', 'selected': 'Node A', 'selectable': false, 'items': [
      {'tag': 'Node A', 'type': 'Shadowsocks', 'delay': 10}, {'tag': 'Node B', 'type': 'Shadowsocks', 'delay': 20},
    ]}]});
    await tester.pumpAndSettle();
    await tester.tap(find.text('Proxies').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Node B'));
    await tester.pumpAndSettle();
    expect(calls.where((call) => call.method == 'groups.select'), isEmpty);
    expect(find.byType(ProxiesPage), findsOneWidget);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
    await events.close();
  });

  testWidgets('the toolbar tests the displayed group and group swipes keep the primary page', (tester) async {
    reportFlutterErrors();
    tester.view.physicalSize = const Size(360, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final events = StreamController<dynamic>.broadcast();
    const channel = MethodChannel('test.group-toolbar');
    final calls = <MethodCall>[];
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger.setMockMethodCallHandler(channel, (call) async {
      calls.add(call);
      return call.method == 'ready' ? {'version': 'test', 'coreVersion': 'test', 'dashboard': {'status': 'Started'}} : null;
    });
    await tester.pumpWidget(BoxApp(controller: BoxController(channel: channel, events: events.stream)));
    await tester.pumpAndSettle();
    events.add({'type': 'groups', 'value': [for (final tag in ['Group A', 'Group B'])
      {'tag': tag, 'selected': 'Node $tag', 'selectable': true, 'items': [{'tag': 'Node $tag', 'type': 'direct'}]},
    ]});
    await tester.pumpAndSettle();
    await tester.tap(find.text('Proxies').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Group B'));
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Test latency').first);
    await tester.pumpAndSettle();
    expect((calls.lastWhere((call) => call.method == 'groups.test').arguments as Map)['tag'], 'Group B');
    await tester.drag(find.byType(TabBarView), const Offset(280, 0));
    await tester.pumpAndSettle();
    expect(find.byType(ProxiesPage).hitTestable(), findsOneWidget);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
    await events.close();
  });

  testWidgets('selected proxies are located outside the lazy grid and the active group survives group updates', (tester) async {
    reportFlutterErrors();
    tester.view.physicalSize = const Size(360, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final events = StreamController<dynamic>.broadcast();
    const channel = MethodChannel('test.proxy-locate');
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger.setMockMethodCallHandler(channel, (call) async => call.method == 'ready' ? {
      'version': 'test', 'coreVersion': 'test', 'dashboard': {'status': 'Started'}, 'preferences': {'locale': 'en'},
    } : null);
    await tester.pumpWidget(BoxApp(controller: BoxController(channel: channel, events: events.stream)));
    await tester.pumpAndSettle();
    Json group(String tag, int count, int selected) => {'tag': tag, 'selected': '$tag node $selected', 'selectable': true,
      'items': [for (var index = 0; index < count; index++) {'tag': '$tag node $index', 'type': 'direct'}]};
    final groups = [group('Group A', 1, 0), group('Group B', 200, 150)];
    events.add({'type': 'groups', 'value': groups});
    await tester.pumpAndSettle();
    await tester.tap(find.text('Proxies').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Group B'));
    await tester.pumpAndSettle();
    expect(find.text('Group B node 150'), findsNothing);
    if (find.byTooltip('Scroll to selected').evaluate().isNotEmpty) {
      await tester.tap(find.byTooltip('Scroll to selected'));
    } else {
      await tester.tap(find.byTooltip('More').last);
      await tester.pumpAndSettle();
      await tester.tap(find.text('Scroll to selected').last);
    }
    await tester.pumpAndSettle();
    expect(find.text('Group B node 150').hitTestable(), findsOneWidget);
    events.add({'type': 'groups', 'value': [group('New group', 1, 0), ...groups]});
    await tester.pumpAndSettle();
    final tabContext = tester.element(find.byType(TabBarView));
    expect(DefaultTabController.of(tabContext).index, 2);
    expect(find.text('Group B node 150').hitTestable(), findsOneWidget);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
    await events.close();
  });
}
