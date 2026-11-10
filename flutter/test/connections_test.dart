import 'dart:async';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/pages/requests.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('the unified page releases connection subscriptions and capture polling when closed', (tester) async {
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
    openPage(context, const RequestsPage());
    await tester.pumpAndSettle();
    expect(visibility, [true]);
    expect(captureCalls.map((call) => call.method), ['capture.snapshot']);
    expect(find.byKey(const ValueKey('capture-toggle')), findsOneWidget);
    final pageContext = tester.element(find.byType(RequestsPage));
    pageContext.safeNestedPop();
    await tester.pumpAndSettle();
    await tester.pump();
    expect(find.byType(RequestsPage), findsNothing);
    expect(visibility, [true, false]);
    final reads = captureCalls.length;
    await tester.pump(const Duration(seconds: 8));
    expect(captureCalls.length, reads);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
    await events.close();
  });
}
