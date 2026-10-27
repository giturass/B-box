import 'dart:async';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/pages/terminal_settings.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'support/errors.dart';

void main() {
  testWidgets('Ghostty settings are reachable while stopped and persist native configuration', (tester) async {
    reportFlutterErrors();
    tester.view.physicalSize = const Size(360, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final events = StreamController<dynamic>.broadcast();
    const channel = MethodChannel('test.ghostty-settings');
    final settings = <String, dynamic>{'lightTheme': 'Alabaster', 'darkTheme': 'Afterglow', 'lightConfig': '', 'darkConfig': '',
      'fontFollowTheme': true, 'fontFamily': '', 'fontPath': '', 'fontSize': 14, 'defaultLightTheme': 'Alabaster', 'defaultDarkTheme': 'Afterglow'};
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger.setMockMethodCallHandler(channel, (call) async {
      if (call.method == 'ready') return {'version': 'test', 'coreVersion': 'test', 'dashboard': {'status': 'Stopped'}, 'preferences': {'locale': 'en'}};
      if (call.method == 'terminal.settings') return settings;
      if (call.method == 'terminal.set') { final args = call.arguments as Map; settings[args['key'] as String] = args['value']; return settings; }
      return null;
    });
    await tester.pumpWidget(BoxApp(controller: BoxController(channel: channel, events: events.stream)));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Tools').last);
    await tester.pumpAndSettle();
    await Scrollable.ensureVisible(tester.element(find.text('Ghostty settings')), alignment: .5);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Ghostty settings'));
    await tester.pumpAndSettle();
    expect(find.byType(TerminalSettingsPage), findsOneWidget);
    await tester.tap(find.text('Custom configuration').first);
    await tester.pumpAndSettle();
    expect(settings['lightTheme'], '');
    await Scrollable.ensureVisible(tester.element(find.text('Edit Ghostty configuration')), alignment: .5);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Edit Ghostty configuration'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField), 'background = #123456\nfont-family = monospace');
    await tester.tap(find.byTooltip('Save'));
    await tester.pumpAndSettle();
    expect(settings['lightConfig'], 'background = #123456\nfont-family = monospace');
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
    await events.close();
  });
}
