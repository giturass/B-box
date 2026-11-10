import 'dart:async';
import 'dart:io';
import 'dart:typed_data';
import 'dart:ui' as ui;
import 'package:flutter/rendering.dart';
import 'package:b_box_ui/box.dart';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/pages/requests.dart';
import 'package:b_box_ui/pages/logs.dart';
import 'package:b_box_ui/pages/settings.dart';
import 'package:b_box_ui/pages/tools.dart';
import 'package:b_box_ui/pages/diagnostics.dart';
import 'package:b_box_ui/pages/profiles.dart';
import 'package:b_box_ui/pages/proxies.dart';
import 'package:b_box_ui/components/navigation_bar.dart';
import 'package:b_box_ui/components/page_switcher.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'support/toolbar_paint.dart';

void main() {
  setUpAll(() async {
    final root = Platform.environment['FLUTTER_ROOT'];
    if (root != null) {
      final font = File('$root/bin/cache/artifacts/material_fonts/Roboto-Regular.ttf');
      if (font.existsSync()) await (FontLoader('Roboto')..addFont(Future.value(ByteData.sublistView(await font.readAsBytes())))).load();
    }
  });
  for (final (dark, pureBlack, scale) in [(false, false, 1.0), (true, false, 1.4), (true, true, 1.4)]) {
    testWidgets('fixed navigation keeps safe area, background and tab selection dark=$dark black=$pureBlack', (tester) async {
      tester.view.physicalSize = const Size(320, 800);
      tester.view.devicePixelRatio = 1;
      tester.view.padding = const FakeViewPadding(top: 24, bottom: 24);
      tester.platformDispatcher.textScaleFactorTestValue = scale;
      addTearDown(tester.view.reset);
      addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
      const channel = MethodChannel('test.navigation.fixed');
      final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
      messenger.setMockMethodCallHandler(channel, (call) async => call.method == 'ready' ? {
        'version': 'test', 'coreVersion': 'test', 'dashboard': {'status': 'Stopped'},
        'preferences': {'theme': dark ? 'dark' : 'light', 'pureBlack': pureBlack, 'floatingNavigation': false},
      } : null);
      final events = StreamController<dynamic>.broadcast();
      addTearDown(() async {
        await tester.pumpWidget(const SizedBox());
        await events.close();
        messenger.setMockMethodCallHandler(channel, null);
      });
      final boundary = GlobalKey();
      await tester.pumpWidget(RepaintBoundary(key: boundary,
        child: BoxApp(controller: BoxController(channel: channel, events: events.stream))));
      await tester.pumpAndSettle();
      final bar = find.byType(BoxNavigationBar);
      expect(tester.getSize(bar).height, 64 + 24);
      expect(tester.getBottomRight(bar).dy, 800);
      expect(tester.getBottomRight(find.byType(PageSwitcher)).dy, tester.getTopLeft(bar).dy);
      final surface = tester.widget<Surface>(find.descendant(of: bar, matching: find.byType(Surface)).first);
      expect(surface.color, Theme.of(tester.element(bar)).scaffoldBackgroundColor);
      if (pureBlack) expect(surface.color, Colors.black);
      await tester.tap(find.descendant(of: bar, matching: find.text('Proxies')));
      await tester.pumpAndSettle();
      expect(tester.widget<BoxNavigationBar>(bar).selectedIndex, 1);
      expect(find.byType(ProxiesPage), findsOneWidget);
      expect(find.ancestor(of: find.descendant(of: bar, matching: find.text('Proxies')),
        matching: find.byWidgetPredicate((widget) => widget is Semantics && widget.properties.selected == true)), findsWidgets);
      expect(tester.takeException(), isNull);
      await tester.runAsync(() async {
        final bitmap = await (boundary.currentContext!.findRenderObject()! as RenderRepaintBoundary).toImage();
        final data = await bitmap.toByteData(format: ui.ImageByteFormat.png);
        final file = File('build/screenshots/navigation/fixed-${pureBlack ? 'black' : dark ? 'dark' : 'light'}.png');
        await file.parent.create(recursive: true);
        await file.writeAsBytes(data!.buffer.asUint8List());
        bitmap.dispose();
      });
    });
  }
  for (final (size, dark, fontScale) in [(const Size(360, 800), false, 1.0), (const Size(360, 800), true, 1.4), (const Size(800, 1000), false, 1.0)]) {
    testWidgets('all primary routes render at $size dark=$dark scale=$fontScale', (tester) async {
      tester.binding.platformDispatcher.localesTestValue = const [Locale('en')];
      addTearDown(tester.binding.platformDispatcher.clearLocalesTestValue);
      tester.platformDispatcher.textScaleFactorTestValue = fontScale;
      addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
      tester.view.physicalSize = size;
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      const channel = MethodChannel('test.navigation');
      final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
      messenger.setMockMethodCallHandler(channel, (call) async => call.method == 'ready' ? {
        'version': 'test', 'coreVersion': 'test', 'dashboard': {'status': 'Stopped', 'modes': []},
        'profiles': [], 'preferences': {'locale': 'zh', 'theme': dark ? 'dark' : 'light'},
      } : null);
      addTearDown(() => messenger.setMockMethodCallHandler(channel, null));
      final events = StreamController<dynamic>.broadcast();
      final box = BoxController(channel: channel, events: events.stream);
      final boundaryKey = GlobalKey();
      await tester.pumpWidget(RepaintBoundary(key: boundaryKey, child: BoxApp(controller: box)));
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull);
      expect(Localizations.localeOf(tester.element(find.byType(HomePage))).languageCode, 'en',
        reason: 'A saved language choice must not override the system locale.');
      Future<void> capture(String label) async {
        await tester.runAsync(() async {
          final boundary = boundaryKey.currentContext!.findRenderObject()! as RenderRepaintBoundary;
          final bitmap = await boundary.toImage();
          final data = await bitmap.toByteData(format: ui.ImageByteFormat.png);
          final file = File('build/screenshots/${size.width.toInt()}-${dark ? 'dark' : 'light'}-$fontScale-$label.png');
          await file.parent.create(recursive: true);
          await file.writeAsBytes(data!.buffer.asUint8List());
          bitmap.dispose();
        });
      }
      Future<void> selectTab(String label) async {
        final sidebar = find.byType(NavigationSidebar);
        final target = sidebar.evaluate().isEmpty ? find.text(label).last
            : find.descendant(of: sidebar, matching: find.byKey(ValueKey(label)));
        expect(target.hitTestable(), findsOneWidget);
        await tester.tap(target);
        await tester.pumpAndSettle();
        expect(tester.widget<PageSwitcher>(find.byType(PageSwitcher)).index,
          ['Dashboard', 'Proxies', 'Profiles', 'Tools'].indexOf(label));
        expect(tester.takeException(), isNull, reason: label);
      }
      await capture('Dashboard');
      for (final label in ['Proxies', 'Profiles', 'Tools', 'Dashboard']) {
        await selectTab(label);
        await capture(label);
      }
      events.add({'type': 'profiles', 'value': [
        {'id': 1, 'name': 'Primary sing-box profile', 'remote': true, 'updatedAt': 1793030400000},
        {'id': 2, 'name': 'Local configuration', 'remote': false},
      ]});
      events.add({'type': 'dashboard', 'value': {'status': 'Started', 'selectedProfileId': 1,
        'modes': ['rule', 'global', 'direct'], 'mode': 'rule', 'uploadTotal': '1.2 MB', 'downloadTotal': '5.4 MB'}});
      events.add({'type': 'lan', 'value': {'addresses': ['192.168.1.20', 'fd00::20'], 'bindings': [{'type': 'mixed', 'listen': '127.0.0.1', 'port': 2080}]}});
      events.add({'type': 'memory', 'value': {'total': 120 * 1024 * 1024, 'rss': 120 * 1024 * 1024, 'app': 80 * 1024 * 1024,
        'core': 40 * 1024 * 1024, 'running': true, 'remote': false, 'heapInuse': 24 * 1024 * 1024, 'heapIdle': 8 * 1024 * 1024, 'stack': 4 * 1024 * 1024, 'runtimeOther': 4 * 1024 * 1024, 'goroutines': 47}});
      events.add({'type': 'groups', 'value': [{'tag': 'Select', 'selected': 'Node A', 'selectable': true, 'items': [
        {'tag': 'Node A', 'type': 'Shadowsocks', 'delay': 43, 'testedAt': 1},
        {'tag': 'Node B with a long name', 'type': 'WireGuard', 'delay': 0, 'testedAt': 1},
      ]}]});
      events.add({'type': 'rates', 'value': {'upload': 1024, 'download': 8192}});
      await tester.pumpAndSettle();
      for (final label in ['Dashboard', 'Profiles', 'Proxies']) {
        await selectTab(label);
        await capture('$label-populated');
      }
      await selectTab('Dashboard');
      final rootContext = tester.element(find.byType(HomePage));
      events.add({'type': 'connections', 'value': [
        {'id': 'live-1', 'displayDestination': 'example.org:443', 'network': 'tcp', 'source': '192.0.2.1:51234',
          'outbound': 'Node A', 'rule': 'domain_suffix=example.org', 'createdAt': 1793030400000,
          'upload': 1024, 'download': 8192, 'uploadTotal': 32768, 'downloadTotal': 65536},
      ]});
      events.add({'type': 'logs', 'value': {'defaultLogLevel': 'Info', 'entries': [
        {'id': 1, 'level': 'Info', 'receivedAt': 1793030400000, 'message': 'inbound/tun[0]: inbound connection from 192.0.2.1:51234'},
        {'id': 2, 'level': 'Warn', 'receivedAt': 1793030401000, 'message': 'outbound/shadowsocks[Node A]: connection timed out'},
      ]}});
      await tester.pumpAndSettle();
      for (final (label, page) in <(String, Widget)>[
        ('Requests', const RequestsPage()), ('Logs', const LogsPage()),
        ('Remote', const RemotePage()),
      ]) {
        final closed = openPage(rootContext, page);
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull, reason: label);
        await capture(label);
        tester.element(find.byWidget(page)).safeNestedPop();
        await tester.pumpAndSettle();
        await closed;
        expect(find.byWidget(page), findsNothing, reason: '$label closes within the new UI');
      }
      await selectTab('Tools');
      final diagnosticsEntry = find.descendant(of: find.byType(ToolsPage), matching: find.text('Network diagnostics'));
      await Scrollable.ensureVisible(tester.element(diagnosticsEntry), alignment: .5);
      await tester.pumpAndSettle();
      expect(diagnosticsEntry.hitTestable(), findsOneWidget);
      await tester.tap(diagnosticsEntry);
      await tester.pumpAndSettle();
      expect(find.byType(NetworkDiagnosticsPage), findsOneWidget, reason: 'Network diagnostics opens from Tools');
      expect(tester.takeException(), isNull, reason: 'Network-diagnostics');
      await capture('Network-diagnostics');
      for (final (diagnosticLabel, diagnosticTitle, quality) in [('Quality', 'Network quality', true), ('STUN', 'STUN test', false)]) {
        await tester.tap(find.descendant(of: find.byType(NetworkDiagnosticsPage), matching: find.text(diagnosticTitle)));
        await tester.pumpAndSettle();
        expect(tester.widget<DiagnosticsPage>(find.byType(DiagnosticsPage)).quality, quality);
        expect(tester.takeException(), isNull, reason: diagnosticLabel);
        await capture(diagnosticLabel);
        tester.element(find.byType(DiagnosticsPage)).safeNestedPop();
        await tester.pumpAndSettle();
        expect(find.byType(NetworkDiagnosticsPage), findsOneWidget);
      }
      tester.element(find.byType(NetworkDiagnosticsPage)).safeNestedPop();
      await tester.pumpAndSettle();
      expect(find.byType(ToolsPage), findsOneWidget, reason: 'Network diagnostics returns to Tools');
      await selectTab('Dashboard');
      await tester.tap(find.byTooltip('Settings'));
      await tester.pumpAndSettle();
      expect(find.byType(SettingsPage), findsOneWidget);
      expect(find.text('General'), findsNothing);
      expect(find.text('Allow VPN bypass'), findsNothing);
      expect(find.text('Core version'), findsNothing);
      await capture('Settings');
      for (final (label, title, page) in <(String, String, Widget)>[
        ('Appearance', 'Appearance', const AppearancePage()),
        ('Service', 'Service', const ServiceSettingsPage()), ('Core', 'Core', const CoreSettingsPage()),
        ('Access', 'Access control', const AccessPage()),
        ('Backup', 'Backup and restore', const BackupPage()), ('About', 'About', const AboutPage()),
      ]) {
        final entry = find.descendant(of: find.byType(SettingsPage), matching: find.text(title));
        final destination = find.byType(page.runtimeType);
        await Scrollable.ensureVisible(tester.element(entry), alignment: .5);
        await tester.pumpAndSettle();
        expect(entry.hitTestable(), findsOneWidget);
        await tester.tap(entry);
        await tester.pumpAndSettle();
        expect(destination, findsOneWidget, reason: '$title opens from Settings');
        expect(tester.takeException(), isNull, reason: label);
        await capture(label);
        if (label == 'Appearance' && size.width < 600) {
          await expectClearTrailingToolbar(tester, boundaryKey, destination);
        }
        if (page is CoreSettingsPage) expect(find.text('Power reports'), findsNothing);
        tester.element(destination).safeNestedPop();
        await tester.pumpAndSettle();
        expect(find.byType(SettingsPage), findsOneWidget, reason: '$label returns to Settings');
      }
      tester.element(find.byType(SettingsPage)).safeNestedPop();
      await tester.pumpAndSettle();
      expect(find.byType(SettingsPage), findsNothing);
      expect(find.byTooltip('Settings').hitTestable(), findsOneWidget);
      for (final (label, page) in <(String, Widget)>[
        ('Profile-edit', const ProfileForm(initial: {'id': 1, 'name': 'Primary sing-box profile',
          'remote': true, 'url': 'https://example.org/config.json', 'autoUpdate': true, 'interval': 60})),
        ('Profile-reorder', const ProfileOrderPage()),
      ]) {
        final closed = openForm(rootContext, page);
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull, reason: label);
        await capture(label);
        tester.element(find.byWidget(page)).safeNestedPop();
        await tester.pumpAndSettle();
        await closed;
      }
      events.add({'type': 'preferences', 'value': {'locale': 'en', 'theme': dark ? 'dark' : 'light'}});
      tester.binding.platformDispatcher.localesTestValue = const [Locale('zh', 'CN')];
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull, reason: 'Chinese locale');
      expect(Localizations.localeOf(tester.element(find.byType(HomePage))).languageCode, 'zh',
        reason: 'System language changes must update the running app.');
      await capture('Dashboard-Chinese');
      await tester.pumpWidget(const SizedBox());
      await events.close();
    });
  }
}
