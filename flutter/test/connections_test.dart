import 'dart:async';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/pages/connections.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('connection subscriptions are released when their page closes', (tester) async {
    final visibility = <bool>[];
    final captureCalls = <MethodCall>[];
    final events = StreamController<dynamic>.broadcast();
    const channel = MethodChannel('test.connections');
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger.setMockMethodCallHandler(channel, (call) async {
      if (call.method == 'connections.visible') visibility.add((call.arguments as Map)['visible'] == true);
      if (call.method.startsWith('capture.')) captureCalls.add(call);
      return call.method == 'ready' ? {'version': 'test', 'coreVersion': 'test', 'dashboard': {'status': 'Started'}} : null;
    });
    final box = BoxController(channel: channel, events: events.stream);
    await tester.pumpWidget(BoxApp(controller: box));
    await tester.pumpAndSettle();
    final context = tester.element(find.byType(HomePage));
    openPage(context, const ConnectionsPage());
    await tester.pumpAndSettle();
    expect(visibility, [true]);
    expect(captureCalls, isEmpty);
    expect(find.byKey(const ValueKey('capture-toggle')), findsNothing);
    final pageContext = tester.element(find.byType(ConnectionsPage));
    pageContext.safeNestedPop();
    await tester.pumpAndSettle();
    await tester.pump();
    expect(find.byType(ConnectionsPage), findsNothing);
    expect(visibility, [true, false]);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
    await events.close();
  });
}
