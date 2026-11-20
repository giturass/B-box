import 'dart:math' as math;

import 'package:flutter/rendering.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:prism_ui/prism_ui.dart';

import '../helpers/host_scaffold.dart';
import '../helpers/test_app.dart';

const _unbroken =
    'script_with_a_very_long_name_and_no_spaces_at_all_'
    'that_goes_on_and_on_well_past_any_row_width.js';
const _rowInsets = 32.0;
const _titleGap = 16.0;
const _mixed =
    '订阅扩展脚本 Auto Select 规则重写 for streaming and 海外节点 v2 '
    '用于覆写分流规则 rewriting the DNS policy for every region';

void main() {
  Future<void> pumpRow(
    WidgetTester tester, {
    required double width,
    String title = '',
    Widget? titleWidget,
    required Widget trailing,
    double textScale = 1,
  }) async {
    tester.view.physicalSize = Size(width, 600);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pumpWidget(
      TestApp(
        child: MediaQuery.withClampedTextScaling(
          minScaleFactor: textScale,
          maxScaleFactor: textScale,
          child: HostScaffold(
            body: SizedBox(
              width: width,
              child: ListRow(
                title: titleWidget ?? Text(title, key: const Key('title')),
                trailing: trailing,
              ),
            ),
          ),
        ),
      ),
    );
  }

  RenderParagraph paragraphOf(WidgetTester tester, Finder finder) =>
      tester.renderObject<RenderParagraph>(
        find.descendant(of: finder, matching: find.byType(RichText)),
      );

  for (final width in [360.0, 1280.0]) {
    for (final textScale in [1.0, 2.0]) {
      for (final (name, text) in [('unbroken', _unbroken), ('mixed', _mixed)]) {
        testWidgets('a long $name value truncates on one line beside its title '
            'at ${width.toInt()} wide, text x$textScale', (tester) async {
          await pumpRow(
            tester,
            width: width,
            textScale: textScale,
            title: 'Extend script',
            trailing: Text(text, key: const Key('value')),
          );

          expect(tester.takeException(), isNull);
          final title = tester.getRect(find.byKey(const Key('title')));
          final value = tester.getRect(find.byKey(const Key('value')));
          final titleText = paragraphOf(tester, find.byKey(const Key('title')));
          final valueText = paragraphOf(tester, find.byKey(const Key('value')));
          expect(
            title.width,
            math.min(
              titleText.getMaxIntrinsicWidth(double.infinity),
              (width - _rowInsets - _titleGap) / 2,
            ),
          );
          expect(value.left, greaterThan(title.right));
          expect(value.right, lessThanOrEqualTo(width));
          expect(valueText.didExceedMaxLines, isTrue);
          expect(
            value.height,
            valueText.getMaxIntrinsicHeight(double.infinity),
          );
        });
      }
    }
  }

  testWidgets('a long title and a long value split the row in half', (
    tester,
  ) async {
    await pumpRow(
      tester,
      width: 360,
      title: _mixed,
      trailing: const Text(_unbroken, key: Key('value')),
    );

    expect(tester.takeException(), isNull);
    final title = tester.getRect(find.byKey(const Key('title')));
    final value = tester.getRect(find.byKey(const Key('value')));
    expect(title.width, greaterThan(100));
    expect(value.width, closeTo(title.width, 1));
    expect(value.left, greaterThan(title.right));
  });

  testWidgets(
    'a title that fills its width under a LayoutBuilder leaves a long value half',
    (tester) async {
      await pumpRow(
        tester,
        width: 360,
        titleWidget: Center(
          key: const Key('title'),
          child: LayoutBuilder(builder: (_, _) => const Text('Rule')),
        ),
        trailing: const Text(_unbroken, key: Key('value')),
      );

      expect(tester.takeException(), isNull);
      expect(
        tester.getSize(find.byKey(const Key('value'))).width,
        (360 - _rowInsets - _titleGap) / 2,
      );
    },
  );

  testWidgets('a long value that eases to its width settles', (tester) async {
    await pumpRow(
      tester,
      width: 360,
      title: 'Extend script',
      trailing: const AnimatedSize(
        duration: Duration(milliseconds: 200),
        child: Text(_unbroken, key: Key('value')),
      ),
    );

    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    expect(
      paragraphOf(tester, find.byKey(const Key('value'))).didExceedMaxLines,
      isTrue,
    );
  });

  testWidgets('a value that fits keeps its width', (tester) async {
    await pumpRow(
      tester,
      width: 360,
      title: 'Extend script',
      trailing: const Text('None', key: Key('value')),
    );

    final value = paragraphOf(tester, find.byKey(const Key('value')));
    expect(value.didExceedMaxLines, isFalse);
    expect(
      tester.getSize(find.byKey(const Key('value'))).width,
      value.getMaxIntrinsicWidth(double.infinity),
    );
  });
}
