import 'dart:async';
import 'dart:io';
import 'dart:typed_data';
import 'dart:ui' as ui;
import 'package:flutter/rendering.dart';
import 'package:b_box_ui/box.dart';
import 'package:b_box_ui/main.dart';
import 'package:flutter/services.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  setUpAll(() async {
    final root = Platform.environment['FLUTTER_ROOT'];
    if (root != null) {
      final font = File('$root/bin/cache/artifacts/material_fonts/Roboto-Regular.ttf');
      if (font.existsSync()) await (FontLoader('Roboto')..addFont(Future.value(ByteData.sublistView(await font.readAsBytes())))).load();
    }
  });
  for (final size in [const Size(360, 800), const Size(800, 1000)]) {
    testWidgets('all primary routes render at $size', (tester) async {
      tester.view.physicalSize = size;
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      const channel = MethodChannel('test.navigation');
      final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
      messenger.setMockMethodCallHandler(channel, (call) async => call.method == 'ready' ? {
        'version': 'test', 'coreVersion': 'test', 'dashboard': {'status': 'Stopped', 'modes': []},
        'profiles': [], 'preferences': {'locale': 'en'},
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
          final file = File('build/screenshots/${size.width.toInt()}-$label.png');
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
      await tester.pumpWidget(const SizedBox());
      await events.close();
    });
  }
}
