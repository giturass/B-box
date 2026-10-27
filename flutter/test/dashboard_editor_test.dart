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
  for (final scenario in ['add-remove', 'reorder', 'failed-save']) {
    testWidgets('dashboard edits cards in place: $scenario', (tester) async {
      reportFlutterErrors();
      tester.view.physicalSize = const Size(360, 800);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final events = StreamController<dynamic>.broadcast();
      const channel = MethodChannel('test.dashboard-edit');
      final preferences = <String, dynamic>{'locale': 'en'};
      final calls = <MethodCall>[];
      var reject = scenario == 'failed-save';
      final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
      messenger.setMockMethodCallHandler(channel, (call) async {
        calls.add(call);
        switch (call.method) {
          case 'ready': return {'version': 'test', 'coreVersion': 'test', 'preferences': preferences,
            'profiles': [{'id': 1, 'name': 'Local profile', 'remote': false}],
            'dashboard': {'status': 'Started', 'selectedProfileId': 1, 'modes': ['rule', 'global', 'direct'], 'mode': 'rule'}};
          case 'dashboard.egress': return {'ip': '203.0.113.1', 'country': 'CN', 'type': 'Residential'};
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
      if (scenario == 'reorder') {
        final handle = find.byType(ReorderableDelayedDragStartListener).at(1);
        // The centre of this tall card can sit behind the navigation bar.
        // Start on its visible upper part, as a finger would on a phone.
        final start = tester.getTopLeft(handle) + const Offset(80, 40);
        final destination = tester.getTopLeft(find.byType(StatusPanel)) + const Offset(80, 40);
        final gesture = await tester.startGesture(start);
        await tester.pump();
        await tester.pump(const Duration(milliseconds: 600));
        await gesture.moveBy(const Offset(0, -20));
        await tester.pump();
        await tester.pump(const Duration(milliseconds: 300));
        await gesture.moveTo(destination);
        await tester.pump(const Duration(milliseconds: 600));
        await gesture.up();
        await advance();
        expect(calls.where((call) => call.method == 'preferences.set'), isNotEmpty);
        final order = preferences['dashboardCards'] as List;
        expect(order.indexOf('Network speed'), lessThan(order.indexOf('Status')));
      } else {
        await tester.tap(find.byTooltip('Remove Network speed'));
        await advance();
        expect(find.byTooltip('Remove Network speed'), findsNothing);
        if (scenario == 'failed-save') {
          expect(preferences['hiddenCards'], isNull);
          expect(find.byType(DashboardEditor), findsOneWidget);
          reject = false;
        } else {
          expect(preferences['hiddenCards'], contains('Network speed'));
          await tester.scrollUntilVisible(find.byTooltip('Add Memory'), 250,
            scrollable: find.ancestor(of: find.byType(DashboardEditor), matching: find.byType(Scrollable)).first);
          await advance();
          await tester.tap(find.byTooltip('Add Memory'));
          await advance();
          expect(preferences['hiddenCards'], isNot(contains('Memory')));
        }
      }
      await tester.tap(find.byTooltip('Done'));
      await tester.pumpAndSettle();
      expect(find.byType(DashboardEditor), findsNothing);
      expect(tester.state(find.byType(StatusPanel)), same(status));
      expect(calls.where((call) => call.method == 'dashboard.egress'), hasLength(egressCalls));
      if (scenario == 'add-remove') {
        await tester.tap(find.text('Tools').last);
        await tester.pumpAndSettle();
        await tester.scrollUntilVisible(find.text('Dashboard cards'), 300,
          scrollable: find.descendant(of: find.byType(ToolsPage), matching: find.byType(Scrollable)).first);
        await tester.pumpAndSettle();
        await tester.tap(find.text('Dashboard cards'));
        await advance();
        expect(find.byType(DashboardEditor).hitTestable(), findsOneWidget);
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
