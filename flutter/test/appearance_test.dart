import 'dart:async';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/pages/appearance.dart';
import 'package:b_box_ui/theme_settings.dart';
import 'package:b_box_ui/components/page_switcher.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('appearance choices update the running app and survive reopening', (tester) async {
    final events = StreamController<dynamic>.broadcast();
    const channel = MethodChannel('test.appearance');
    final stored = <String, dynamic>{'locale': 'en', 'theme': 'light'};
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger.setMockMethodCallHandler(channel, (call) async {
      if (call.method == 'preferences.set') { stored.addAll(Map<String, dynamic>.from((call.arguments as Map)['value'] as Map)); return stored; }
      if (call.method == 'ready') return {'version': 'test', 'coreVersion': 'test', 'preferences': stored, 'dashboard': {'status': 'Stopped'}};
      return null;
    });
    final box = BoxController(channel: channel, events: events.stream);
    await tester.pumpWidget(BoxApp(controller: box));
    await tester.pumpAndSettle();
    openPage(tester.element(find.byType(HomePage)), const AppearancePage());
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.text('Dark'));
    await tester.tap(find.text('Dark'));
    await tester.pumpAndSettle();
    expect(stored['theme'], 'dark');
    expect(Theme.of(tester.element(find.byType(AppearancePage))).brightness, Brightness.dark);
    await tester.ensureVisible(find.text('Pure black'));
    await tester.tap(find.text('Pure black'));
    await tester.pumpAndSettle();
    expect(Theme.of(tester.element(find.byType(AppearancePage))).colorScheme.surface, Colors.black);
    await tester.ensureVisible(find.text('Fade'));
    await tester.tap(find.text('Fade'));
    await tester.pumpAndSettle();
    expect(tester.widget<PageSwitcher>(find.byType(PageSwitcher, skipOffstage: false)).animation, TabAnimation.fade);
    tester.element(find.byType(AppearancePage)).safeNestedPop();
    await tester.pumpAndSettle();
    openPage(tester.element(find.byType(HomePage)), const AppearancePage());
    await tester.pumpAndSettle();
    expect(box.preferences['pureBlack'], true);
    expect(box.preferences['tabAnimation'], 'fade');
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
    await events.close();
  });

  test('custom text size replaces system scaling instead of multiplying it', () {
    expect(appTextScale({'customFontScale': true, 'fontScale': 1.2}, 1.4), 1.2);
    expect(appTextScale({'customFontScale': false, 'fontScale': 1.2}, 1.4), 1.4);
  });
}
