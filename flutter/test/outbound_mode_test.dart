import 'dart:async';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/components/connected_buttons.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'support/errors.dart';

void main() {
  for (final (provided, locale, scale, cachedMode) in [
    (['direct', 'global', 'rule'], 'en', 1.0, 'Rule'),
    (['Direct', 'Global', 'Rule'], 'zh', 1.4, 'rule'),
  ]) {
    testWidgets('outbound mode glyphs match Android while preserving core order and live selection: $provided', (tester) async {
      reportFlutterErrors();
      tester.view.physicalSize = const Size(360, 800);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final events = StreamController<dynamic>.broadcast();
      const channel = MethodChannel('test.outbound-mode');
      final calls = <MethodCall>[];
      final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
      messenger.setMockMethodCallHandler(channel, (call) async {
        calls.add(call);
        if (call.method == 'ready') return {
          'version': 'test', 'coreVersion': 'test',
          'preferences': {'locale': locale, 'fontScale': scale},
          'dashboard': {'status': 'Started', 'modes': provided, 'mode': cachedMode},
        };
        if (call.method == 'dashboard.egress') return {'ip': '203.0.113.1', 'country': 'CN'};
        return null;
      });
      addTearDown(() => messenger.setMockMethodCallHandler(channel, null));
      await tester.pumpWidget(BoxApp(controller: BoxController(channel: channel, events: events.stream)));
      await tester.pumpAndSettle();
      final labels = locale == 'zh' ? ['规则', '全局', '直连'] : ['Rule', 'Global', 'Direct'];
      Finder button(int index) => find.byWidgetPredicate((widget) =>
        widget is DashboardConnectedButton && widget.semanticLabel == labels[index]);
      bool selected(int index) => tester.widget<DashboardConnectedButton>(button(index)).selected;
      expect(tester.widgetList<DashboardConnectedButton>(find.byType(DashboardConnectedButton))
        .map((widget) => widget.semanticLabel), [labels[2], labels[1], labels[0]]);
      for (final (index, glyph) in [AppGlyphs.rules, AppGlyphs.proxies, AppGlyphs.target].indexed) {
        expect(find.descendant(of: button(index), matching: find.byWidgetPredicate((widget) =>
          widget is GlyphIcon && widget.glyph == glyph)), findsOneWidget);
      }
      expect(selected(0), true, reason: 'The cached mode can have different casing from the supported values.');
      await Scrollable.ensureVisible(tester.element(button(1)), alignment: .5);
      await tester.pumpAndSettle();
      await tester.tap(button(0));
      await tester.pumpAndSettle();
      expect(calls.where((call) => call.method == 'mode.set'), isEmpty);
      await tester.tap(button(1));
      await tester.pumpAndSettle();
      final changes = calls.where((call) => call.method == 'mode.set').toList();
      expect(changes, hasLength(1));
      expect((changes.single.arguments as Map)['mode'], provided[1],
        reason: 'The native bridge requires the exact value provided by the core.');
      expect(selected(0), true, reason: 'Selection follows the core event confirming the change.');
      expect(selected(1), false);
      events.add({'type': 'dashboard', 'value': {'status': 'Started', 'modes': provided, 'mode': 'global'}});
      await tester.pumpAndSettle();
      expect(selected(0), false);
      expect(selected(1), true);
      await tester.tap(button(1));
      await tester.pumpAndSettle();
      expect(calls.where((call) => call.method == 'mode.set'), hasLength(1));

      events.add({'type': 'dashboard', 'value': {'status': 'Started', 'modes': ['Maintenance', 'Direct'], 'mode': 'direct'}});
      await tester.pumpAndSettle();
      expect(tester.widgetList<DashboardConnectedButton>(find.byType(DashboardConnectedButton))
        .map((widget) => widget.semanticLabel), ['Maintenance', labels[2]]);
      expect(selected(2), true);
      expect(button(0), findsNothing);
      expect(button(1), findsNothing);
      events.add({'type': 'dashboard', 'value': {'status': 'Started', 'modes': []}});
      await tester.pumpAndSettle();
      expect(find.byType(DashboardConnectedButton), findsNothing);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
      await events.close();
    });
  }
}
