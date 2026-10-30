import 'package:b_box_ui/components/code_editor.dart';
import 'package:b_box_ui/pages/profile_editor.dart';
import 'package:material_ui/material_ui.dart';
import 'package:flutter_test/flutter_test.dart';

Finder _line(int number) => find.byKey(ValueKey('code-line-$number'));

Future<void> _mount(WidgetTester tester, JsonEditorController text, UndoHistoryController undo,
    {double width = 320, double scale = 1, double height = 260}) async {
  await tester.pumpWidget(MaterialApp(home: Scaffold(body: Center(child: SizedBox(width: width, height: height,
    child: MediaQuery(data: MediaQueryData(textScaler: TextScaler.linear(scale)),
      child: CodeEditor(controller: text, undoController: undo)),
  )))));
  await tester.pump();
}

double _textY(WidgetTester tester, int offset) {
  final render = tester.state<EditableTextState>(find.byType(EditableText)).renderEditable;
  // Selection boxes follow the glyph layout; the caret additionally snaps to physical pixels.
  final box = render.getBoxesForSelection(TextSelection(baseOffset: offset, extentOffset: offset + 1)).first;
  return render.localToGlobal(box.toRect().topLeft).dy;
}

void main() {
  for (final scale in [0.8, 1.0, 1.4]) {
    testWidgets('logical numbers stay aligned through wrapping and resize at scale $scale', (tester) async {
      final text = JsonEditorController()..text = '{\n  "url": "https://example.org/${'segment/' * 8}"\n}\n';
      final undo = UndoHistoryController();
      addTearDown(() { text.dispose(); undo.dispose(); });
      for (final width in [320.0, 540.0]) {
        await _mount(tester, text, undo, width: width, scale: scale, height: 500);
        expect(_line(4), findsOneWidget); // Trailing empty logical line.
        expect(_line(5), findsNothing); // Wrapped visual rows do not add numbers.
        final gutterDelta = tester.getTopLeft(_line(3)).dy - tester.getTopLeft(_line(1)).dy;
        final textDelta = _textY(tester, text.text.indexOf('}')) - _textY(tester, 0);
        expect(gutterDelta, closeTo(textDelta, 0.1));
        expect(tester.takeException(), isNull);
      }
      await tester.pumpWidget(const SizedBox());
    });
  }

  testWidgets('numbers track scrolling, inserted blank lines and replaced content', (tester) async {
    final text = JsonEditorController()..text = List.generate(100, (i) => '"line $i"').join('\n');
    final undo = UndoHistoryController();
    addTearDown(() { text.dispose(); undo.dispose(); });
    await _mount(tester, text, undo);
    final alignment = tester.getTopLeft(_line(1)).dy - _textY(tester, 0);
    final scroll = tester.widget<TextField>(find.byType(TextField)).scrollController!;
    scroll.jumpTo(420.5);
    await tester.pump();
    expect(_line(1), findsNothing);
    final visible = find.byWidgetPredicate((w) => w is Text && w.key is ValueKey<String>
      && (w.key! as ValueKey<String>).value.startsWith('code-line-'));
    expect(visible.evaluate().length, lessThan(20));
    final number = int.parse(tester.widget<Text>(visible.last).data!);
    final offset = text.text.indexOf('"line ${number - 1}"');
    expect(tester.getTopLeft(_line(number)).dy - _textY(tester, offset), closeTo(alignment, 0.1));
    scroll.jumpTo(scroll.position.maxScrollExtent);
    await tester.pump();
    expect(_line(100), findsOneWidget);

    // Formatting/reload and editing use the same controller notification path.
    text.text = '{\n\n}\n';
    await tester.pumpAndSettle();
    for (var line = 1; line <= 4; line++) { expect(_line(line), findsOneWidget); }
    expect(_line(5), findsNothing);
    expect(scroll.offset, 0);
    text.text = '{}';
    await tester.pump();
    expect(_line(1), findsOneWidget);
    expect(_line(2), findsNothing);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
  });
}
