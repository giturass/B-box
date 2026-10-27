import 'dart:async';
import 'package:b_box_ui/main.dart';
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
}
