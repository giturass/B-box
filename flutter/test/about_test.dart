import 'dart:async';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/pages/about.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'support/errors.dart';

void main() {
  for (final available in [true, false]) {
    testWidgets('About checks B-box releases and handles available=$available', (tester) async {
      reportFlutterErrors();
      tester.view.physicalSize = const Size(360, 800);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final events = StreamController<dynamic>.broadcast();
      const channel = MethodChannel('test.about');
      final calls = <MethodCall>[];
      final update = Completer<Json>();
      final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
      messenger.setMockMethodCallHandler(channel, (call) async {
        calls.add(call);
        switch (call.method) {
          case 'ready': return {'version': '1.0', 'coreVersion': '1.15',
            'dashboard': {'status': 'Stopped'}, 'preferences': {'locale': 'en'}};
          case 'updates.check': return update.future;
        }
        return null;
      });
      addTearDown(() => messenger.setMockMethodCallHandler(channel, null));
      await tester.pumpWidget(BoxApp(controller: BoxController(channel: channel, events: events.stream)));
      await tester.pumpAndSettle();
      openPage(tester.element(find.byType(HomePage)), const AboutPage());
      await tester.pumpAndSettle();
      final check = find.widgetWithText(TonalButton, 'Check for updates');
      await tester.ensureVisible(check);
      await tester.tap(check);
      await tester.pump();
      expect(calls.where((call) => call.method == 'updates.check'), hasLength(1));
      expect(tester.widget<TonalButton>(check).onPressed, isNull);
      update.complete({'available': available, 'hasMetadata': true, 'version': '2.0',
        'notes': 'Release notes', 'url': 'https://github.com/giturass/B-box/releases/tag/v2.0',
        'downloadUrl': 'https://github.com/giturass/B-box/releases/download/v2.0/B-box-2.0-arm64-v8a.apk'});
      await tester.pumpAndSettle();
      expect(find.text(available ? 'Update available' : 'You are up to date'), findsOneWidget);
      if (available) {
        expect(find.text('Release notes'), findsOneWidget);
        await tester.tap(find.text('Download'));
        await tester.pumpAndSettle();
        expect((calls.lastWhere((call) => call.method == 'url.open').arguments as Map)['url'],
          'https://github.com/giturass/B-box/releases/download/v2.0/B-box-2.0-arm64-v8a.apk');
      } else {
        expect(find.text('Download'), findsNothing);
        await tester.tap(find.text('Close'));
        await tester.pumpAndSettle();
        expect(calls.where((call) => call.method == 'url.open'), isEmpty);
      }
      expect(tester.widget<TonalButton>(check).onPressed, isNotNull);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
      await events.close();
    });
  }
}
