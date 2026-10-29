import 'dart:async';
import 'dart:io';
import 'dart:typed_data';
import 'dart:ui' as ui;
import 'package:flutter/rendering.dart';
import 'package:b_box_ui/box.dart';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/pages/connections.dart';
import 'package:b_box_ui/pages/logs.dart';
import 'package:b_box_ui/pages/settings.dart';
import 'package:b_box_ui/pages/diagnostics.dart';
import 'package:b_box_ui/pages/reports.dart';
import 'package:b_box_ui/pages/profiles.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  setUpAll(() async {
    final root = Platform.environment['FLUTTER_ROOT'];
    if (root != null) {
      final font = File('$root/bin/cache/artifacts/material_fonts/Roboto-Regular.ttf');
      if (font.existsSync()) await (FontLoader('Roboto')..addFont(Future.value(ByteData.sublistView(await font.readAsBytes())))).load();
    }
  });
  for (final (size, dark, fontScale) in [(const Size(360, 800), false, 1.0), (const Size(360, 800), true, 1.4), (const Size(800, 1000), false, 1.0)]) {
    testWidgets('all primary routes render at $size dark=$dark scale=$fontScale', (tester) async {
      tester.binding.platformDispatcher.localesTestValue = const [Locale('en')];
      addTearDown(tester.binding.platformDispatcher.clearLocalesTestValue);
      tester.view.physicalSize = size;
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      const channel = MethodChannel('test.navigation');
      final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
      messenger.setMockMethodCallHandler(channel, (call) async => call.method == 'ready' ? {
        'version': 'test', 'coreVersion': 'test', 'dashboard': {'status': 'Stopped', 'modes': []},
        'profiles': [], 'preferences': {'locale': 'zh', 'theme': dark ? 'dark' : 'light', 'fontScale': fontScale},
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
      await capture('Dashboard');
      for (final label in ['Proxies', 'Profiles', 'Tools', 'Dashboard']) {
        await tester.tap(find.text(label).last);
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull, reason: label);
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
        await tester.tap(find.text(label).last);
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull, reason: '$label with live data');
        await capture('$label-populated');
      }
      await tester.tap(find.text('Dashboard').last);
      await tester.pumpAndSettle();
      final rootContext = tester.element(find.byType(HomePage));
      events.add({'type': 'connections', 'value': [
        {'id': 'live-1', 'displayDestination': 'example.org:443', 'network': 'tcp', 'source': '192.0.2.1:51234',
          'outbound': 'Node A', 'rule': 'domain_suffix=example.org', 'createdAt': 1793030400000,
          'upload': 1024, 'download': 8192, 'uploadTotal': 32768, 'downloadTotal': 65536},
      ]});
      events.add({'type': 'logs', 'value': [
        {'id': 1, 'level': 'Info', 'receivedAt': 1793030400000, 'message': 'inbound/tun[0]: inbound connection from 192.0.2.1:51234'},
        {'id': 2, 'level': 'Warn', 'receivedAt': 1793030401000, 'message': 'outbound/shadowsocks[Node A]: connection timed out'},
      ]});
      await tester.pumpAndSettle();
      for (final (label, page) in <(String, Widget)>[
        ('Connections', const ConnectionsPage()), ('Logs', const LogsPage()),
        ('Appearance', const AppearancePage()), ('Access', const AccessPage()),
        ('Service', const ServiceSettingsPage()), ('Core', const CoreSettingsPage()),
        ('Remote', const RemotePage()), ('Backup', const BackupPage()),
        ('Quality', const DiagnosticsPage(quality: true)), ('STUN', const DiagnosticsPage(quality: false)),
        ('Reports', const ReportsPage(kind: 'crash')), ('About', const AboutPage()),
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
      events.add({'type': 'preferences', 'value': {'locale': 'en', 'theme': dark ? 'dark' : 'light', 'fontScale': fontScale}});
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
