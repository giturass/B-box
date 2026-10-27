import 'dart:async';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/pages/access.dart';
import 'package:b_box_ui/pages/proxies.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('fresh startup opens the dashboard without accepting a disclaimer', (tester) async {
    final events = StreamController<dynamic>.broadcast();
    const channel = MethodChannel('test.fresh-start');
    final calls = <MethodCall>[];
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger.setMockMethodCallHandler(channel, (call) async {
      calls.add(call);
      return call.method == 'ready' ? {'version': 'test', 'coreVersion': 'test', 'dashboard': {'status': 'Stopped'}, 'preferences': {}} : null;
    });
    await tester.pumpWidget(BoxApp(controller: BoxController(channel: channel, events: events.stream)));
    await tester.pumpAndSettle();
    expect(find.byType(HomePage), findsOneWidget);
    expect(find.byType(AlertDialog), findsNothing);
    expect(find.text('Disclaimer'), findsNothing);
    expect(calls.where((call) => call.method.contains('accept') || call.method.contains('disclaimer')), isEmpty);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
    await events.close();
  });

  testWidgets('app selections are saved together and remain editable after a failed save', (tester) async {
    tester.view.physicalSize = const Size(360, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final events = StreamController<dynamic>.broadcast();
    const channel = MethodChannel('test.access-save');
    final calls = <MethodCall>[];
    var reject = true;
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger.setMockMethodCallHandler(channel, (call) async {
      calls.add(call);
      switch (call.method) {
        case 'ready': return {'version': 'test', 'coreVersion': 'test', 'dashboard': {'status': 'Stopped'},
          'settings': {'perAppEnabled': true, 'perAppMode': 1, 'perAppList': ['hidden.app']}};
        case 'apps.list': return [
          {'package': 'alpha.app', 'name': 'Alpha', 'internet': true},
          {'package': 'beta.app', 'name': 'Beta', 'internet': true},
        ];
        case 'settings.patch':
          if (reject) throw PlatformException(code: 'disk', message: 'Save failed');
          return (call.arguments as Map)['value'];
      }
      return null;
    });
    await tester.pumpWidget(BoxApp(controller: BoxController(channel: channel, events: events.stream)));
    await tester.pumpAndSettle();
    openPage(tester.element(find.byType(HomePage)), const AccessPage());
    await tester.pumpAndSettle();
    await tester.tap(find.text('Alpha'));
    await tester.pumpAndSettle();
    expect(calls.where((call) => call.method == 'settings.patch'), isEmpty);
    Finder saveAction() => find.text('Save').evaluate().isNotEmpty ? find.text('Save').last : find.byTooltip('Save').last;
    await tester.tap(saveAction());
    await tester.pumpAndSettle();
    expect(find.byType(AccessPage), findsOneWidget);
    expect(saveAction(), findsOneWidget);
    reject = false;
    await tester.tap(saveAction());
    await tester.pumpAndSettle();
    final values = ((calls.lastWhere((call) => call.method == 'settings.patch').arguments as Map)['value'] as Map);
    expect(values['perAppList'], containsAll(['alpha.app', 'hidden.app']));
    expect(values['perAppEnabled'], true);
    expect(find.byTooltip('Save'), findsNothing);
    expect(find.text('Save'), findsNothing);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
    await events.close();
  });

  testWidgets('the toolbar tests the displayed group and group swipes keep the primary page', (tester) async {
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
}
