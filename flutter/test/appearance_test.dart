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
    await Scrollable.ensureVisible(tester.element(find.text('Dark')), alignment: .5);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Dark'));
    await tester.pumpAndSettle();
    expect(stored['theme'], 'dark');
    expect(Theme.of(tester.element(find.byType(AppearancePage))).brightness, Brightness.dark);
    await Scrollable.ensureVisible(tester.element(find.text('Pure black')), alignment: .5);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Pure black'));
    await tester.pumpAndSettle();
    expect(stored['pureBlack'], true);
    expect(tester.widget<PrismApp>(find.byType(PrismApp)).darkTheme!.colorScheme.surface, Colors.black);
    await tester.scrollUntilVisible(find.text('Fade'), 200, scrollable: find.descendant(
      of: find.byType(AppearancePage), matching: find.byType(Scrollable)).first);
    await tester.pumpAndSettle();
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

  testWidgets('retired font choices do not override the default font or live system scaling', (tester) async {
    tester.platformDispatcher.textScaleFactorTestValue = 1.2;
    addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
    final events = StreamController<dynamic>.broadcast();
    const channel = MethodChannel('test.appearance.system-font');
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(channel, (call) async => call.method == 'ready' ? {
      'version': 'test', 'coreVersion': 'test', 'dashboard': {'status': 'Stopped'},
      'preferences': {'fontFamily': 'JetBrainsMono', 'customFontScale': true, 'fontScale': .8},
    } : null);
    addTearDown(() async {
      await tester.pumpWidget(const SizedBox());
      await events.close();
      messenger.setMockMethodCallHandler(channel, null);
    });
    await tester.pumpWidget(BoxApp(controller: BoxController(channel: channel, events: events.stream)));
    await tester.pumpAndSettle();
    final context = tester.element(find.byType(HomePage));
    expect(Theme.of(context).bodyTextStyle.fontFamily, ThemeData().bodyTextStyle.fontFamily);
    expect(MediaQuery.textScalerOf(context).scale(10), closeTo(12, 1e-9));
    for (final (system, expected) in [(1.3, 13.0), (2.0, 14.0), (.5, 8.0)]) {
      tester.platformDispatcher.textScaleFactorTestValue = system;
      await tester.pumpAndSettle();
      expect(MediaQuery.textScalerOf(context).scale(10), closeTo(expected, 1e-9));
    }
    expect(tester.takeException(), isNull);
  });
}
