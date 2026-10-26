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
      tester.view.physicalSize = size;
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      const channel = MethodChannel('test.navigation');
      final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
      messenger.setMockMethodCallHandler(channel, (call) async => call.method == 'ready' ? {
        'version': 'test', 'coreVersion': 'test', 'dashboard': {'status': 'Stopped', 'modes': []},
        'profiles': [], 'preferences': {'locale': 'en', 'theme': dark ? 'dark' : 'light', 'fontScale': fontScale},
      } : null);
      addTearDown(() => messenger.setMockMethodCallHandler(channel, null));
      final events = StreamController<dynamic>.broadcast();
      final box = BoxController(channel: channel, events: events.stream);
      final boundaryKey = GlobalKey();
      await tester.pumpWidget(RepaintBoundary(key: boundaryKey, child: BoxApp(controller: box)));
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull);
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
        'modes': ['rule', 'global'], 'mode': 'rule', 'uploadTotal': '1.2 MB', 'downloadTotal': '5.4 MB'}});
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
      events.add({'type': 'preferences', 'value': {'locale': 'zh', 'theme': dark ? 'dark' : 'light', 'fontScale': fontScale}});
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull, reason: 'Chinese locale');
      await capture('Dashboard-Chinese');
      await tester.pumpWidget(const SizedBox());
      await events.close();
    });
  }
}
