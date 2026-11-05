import 'dart:ui' as ui;

import 'package:b_box_ui/widgets.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_test/flutter_test.dart';

/// Hidden root pages can paint lifted toolbar controls without accepting
/// taps. Inspect the action area itself, rather than just hit testing it.
Future<void> expectClearTrailingToolbar(WidgetTester tester,
    GlobalKey boundaryKey, Finder page) async {
  final context = tester.element(page);
  final rect = tester.getRect(page);
  final top = rect.top + MediaQuery.paddingOf(context).top;
  final color = Theme.of(context).scaffoldBackgroundColor.toARGB32();
  final expected = [(color >> 16) & 255, (color >> 8) & 255, color & 255, 255];
  await tester.runAsync(() async {
    final boundary = boundaryKey.currentContext!.findRenderObject()! as RenderRepaintBoundary;
    final bitmap = await boundary.toImage();
    try {
      final data = (await bitmap.toByteData(format: ui.ImageByteFormat.rawRgba))!;
      var painted = 0;
      for (var y = (top + 20).round(); y < (top + 52).round(); y++) {
        for (var x = (rect.right - 56).round(); x < (rect.right - 16).round(); x++) {
          final pixel = (y * bitmap.width + x) * 4;
          if (List.generate(4, (channel) =>
              (data.getUint8(pixel + channel) - expected[channel]).abs())
              .any((difference) => difference > 1)) painted++;
        }
      }
      expect(painted, 0,
        reason: 'A page without toolbar actions must not paint controls from hidden home tabs.');
    } finally {
      bitmap.dispose();
    }
  });
}
