import 'dart:async';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/components/dashboard_editor.dart';
import 'package:b_box_ui/components/status_panel.dart';
import 'package:b_box_ui/pages/tools.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'support/errors.dart';

void main() {
  for (final (card, scenario) in [
    for (final card in ['Network speed', 'System HTTP proxy'])
      for (final scenario in ['add-remove', 'reorder', 'failed-save']) (card, scenario),
  ]) {
    testWidgets('dashboard edits $card in place: $scenario', (tester) async {
      reportFlutterErrors();
      tester.view.physicalSize = const Size(360, 800);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final events = StreamController<dynamic>.broadcast();
      const channel = MethodChannel('test.dashboard-edit');
      final preferences = <String, dynamic>{'locale': 'en'};
      if (scenario == 'add-remove') preferences.addAll({
        'dashboardCards': ['Status', 'Network speed', 'Outbound mode', 'Current profile', 'Connections', 'Proxy groups'],
        'hiddenCards': ['Connections', 'Memory', 'Intranet IP'],
      });
      final systemProxy = card == 'System HTTP proxy';
      if (systemProxy && scenario != 'add-remove') preferences['dashboardCards'] = [
        'Status', card, 'Network speed', 'Outbound mode', 'Current profile', 'Proxy groups',
      ];
      final dashboard = <String, dynamic>{
        'status': 'Started', 'selectedProfileId': 1, 'modes': ['rule', 'global', 'direct'], 'mode': 'rule',
        'systemProxyAvailable': systemProxy, 'systemProxy': true,
      };
      final calls = <MethodCall>[];
      var reject = scenario == 'failed-save';
      final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
      messenger.setMockMethodCallHandler(channel, (call) async {
        calls.add(call);
        switch (call.method) {
          case 'ready': return {'version': 'test', 'coreVersion': 'test', 'preferences': preferences,
            'profiles': [{'id': 1, 'name': 'Local profile', 'remote': false}],
            'dashboard': dashboard};
          case 'dashboard.egress': return {'ip': '203.0.113.1', 'country': 'CN', 'type': 'Residential'};
          case 'systemProxy.set':
            dashboard['systemProxy'] = json(call.arguments)['enabled'];
            events.add({'type': 'dashboard', 'value': dashboard});
            return null;
          case 'preferences.set':
            if (reject) throw PlatformException(code: 'disk', message: 'Save failed');
            preferences.addAll(json((call.arguments as Map)['value']));
            return preferences;
        }
        return null;
      });
      addTearDown(() => messenger.setMockMethodCallHandler(channel, null));
      await tester.pumpWidget(BoxApp(controller: BoxController(channel: channel, events: events.stream)));
      await tester.pumpAndSettle();
      if (systemProxy) expect(find.text(card), findsOneWidget);
      final status = tester.state(find.byType(StatusPanel));
      final egressCalls = calls.where((call) => call.method == 'dashboard.egress').length;
      Future<void> advance() async {
        await tester.pump();
        await tester.pump(const Duration(milliseconds: 800));
        await tester.pump();
      }
      await tester.tap(find.byTooltip('Dashboard cards'));
      await advance();
      expect(find.byType(DashboardEditor), findsOneWidget);
      expect(tester.state(find.byType(StatusPanel)), same(status));
      expect(find.byTooltip('Remove Status'), findsNothing);
      expect(find.byTooltip('Remove Connections'), findsNothing);
      expect(find.byTooltip('Add Connections'), findsNothing);
      if (scenario == 'reorder') {
        if (systemProxy) {
          await tester.tap(find.text(card));
          await advance();
          expect(calls.where((call) => call.method == 'systemProxy.set'), isEmpty);
        }
        final handle = find.byType(ReorderableDelayedDragStartListener).at(1);
        // The centre of this tall card can sit behind the navigation bar.
        // Start on its visible upper part, as a finger would on a phone.
        final start = tester.getTopLeft(handle) + const Offset(80, 40);
        final destination = tester.getTopLeft(find.byType(StatusPanel)) + const Offset(80, 40);
        final listener = tester.renderObject(find.descendant(of: handle, matching: find.byType(Listener)).first);
        expect(tester.hitTestOnBinding(start).path.map((entry) => entry.target), contains(listener),
          reason: 'The long press must reach the drag listener at $start (target $destination).');
        final gesture = await tester.startGesture(start);
        await tester.pump();
        await tester.pump(const Duration(milliseconds: 600));
        await tester.pump();
        expect(find.byKey(const ValueKey('dashboard-drag-proxy')), findsOneWidget,
          reason: 'A long press must lift the card before moving it.');
        await gesture.moveBy(const Offset(0, -20));
        await tester.pump();
        await tester.pump(const Duration(milliseconds: 300));
        final liftedTop = tester.getTopLeft(find.byKey(const ValueKey('dashboard-drag-proxy')));
        for (var step = 1; step <= 10; step++) {
          await gesture.moveTo(Offset.lerp(start - const Offset(0, 20), destination, step / 10)!);
          await tester.pump(const Duration(milliseconds: 30));
        }
        expect(tester.getTopLeft(find.byKey(const ValueKey('dashboard-drag-proxy'))).dy,
          lessThan(liftedTop.dy - 80), reason: 'The held card must follow the finger to the first row.');
        await tester.pump(const Duration(milliseconds: 600));
        await gesture.up();
        await advance();
        expect(calls.where((call) => call.method == 'preferences.set'), isNotEmpty);
        final order = preferences['dashboardCards'] as List;
        expect(order.indexOf(card), lessThan(order.indexOf('Status')));
      } else {
        if (systemProxy && scenario == 'add-remove') {
          await tester.scrollUntilVisible(find.byTooltip('Remove $card'), 250,
            scrollable: find.ancestor(of: find.byType(DashboardEditor), matching: find.byType(Scrollable)).first);
          await advance();
        }
        await tester.tap(find.byTooltip('Remove $card'));
        await advance();
        expect(find.byTooltip('Remove $card'), findsNothing);
        if (scenario == 'failed-save') {
          expect(preferences['hiddenCards'], isNull);
          expect(find.byType(DashboardEditor), findsOneWidget);
          reject = false;
        } else {
          expect(preferences['hiddenCards'], contains(card));
          expect(preferences['dashboardCards'], isNot(contains('Connections')));
          expect(preferences['hiddenCards'], isNot(contains('Connections')));
          if (systemProxy) {
            events.add({'type': 'dashboard', 'value': {...dashboard, 'systemProxyAvailable': false}});
            await advance();
            expect(find.byTooltip('Add $card'), findsNothing);
            await tester.ensureVisible(find.byTooltip('Remove Network speed'));
            await advance();
            await tester.tap(find.byTooltip('Remove Network speed'));
            await advance();
            expect(preferences['dashboardCards'], contains(card));
            expect(preferences['hiddenCards'], contains(card));
            events.add({'type': 'dashboard', 'value': dashboard});
            await advance();
          }
          final addCard = systemProxy ? card : 'Memory';
          await tester.scrollUntilVisible(find.byTooltip('Add $addCard'), 250,
            scrollable: find.ancestor(of: find.byType(DashboardEditor), matching: find.byType(Scrollable)).first);
          await advance();
          await tester.tap(find.byTooltip('Add $addCard'));
          await advance();
          expect(preferences['hiddenCards'], isNot(contains(addCard)));
        }
      }
      await tester.tap(find.byTooltip('Done'));
      await tester.pumpAndSettle();
      expect(find.byType(DashboardEditor), findsNothing);
      if (scenario == 'failed-save') expect(preferences['hiddenCards'], contains(card));
      if (systemProxy) {
        expect(find.text(card), scenario == 'failed-save' ? findsNothing : findsOneWidget);
        if (scenario == 'reorder') {
          await tester.tap(find.text(card));
          await tester.pumpAndSettle();
          final commands = calls.where((call) => call.method == 'systemProxy.set');
          expect(commands, hasLength(1));
          expect(json(commands.single.arguments), {'enabled': false});
        }
      }
      expect(tester.state(find.byType(StatusPanel)), same(status));
      expect(calls.where((call) => call.method == 'dashboard.egress'), hasLength(egressCalls));
      if (scenario == 'add-remove') {
        await tester.tap(find.text('Tools').last);
        await tester.pumpAndSettle();
        await tester.scrollUntilVisible(find.text('Remote control'), 300,
          scrollable: find.descendant(of: find.byType(ToolsPage), matching: find.byType(Scrollable)).first);
        await tester.pumpAndSettle();
        expect(find.text('Ghostty settings'), findsNothing);
        expect(find.text('Dashboard cards'), findsNothing);
        await tester.tap(find.text('Dashboard').last);
        await tester.pumpAndSettle();
        await tester.tap(find.byTooltip('Dashboard cards'));
        await advance();
        expect(find.byType(DashboardEditor), findsOneWidget);
        expect(find.byTooltip('Done').hitTestable(), findsOneWidget);
        expect(find.byType(HomePage), findsOneWidget);
        expect(tester.state(find.byType(StatusPanel)), same(status));
        await tester.tap(find.byTooltip('Done'));
        await tester.pumpAndSettle();
      }
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
      await events.close();
    });
  }
}
