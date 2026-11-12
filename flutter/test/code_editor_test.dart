import 'dart:io';
import 'dart:ui' show BoxHeightStyle;
import 'package:b_box_ui/components/code_editor.dart';
import 'package:b_box_ui/pages/profile_editor.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

Finder _line(int number) => find.byKey(ValueKey('code-line-$number'));

Future<void> _mount(WidgetTester tester, JsonEditorController text, UndoHistoryController undo,
    {double width = 320, double scale = 1, double height = 260}) async {
  await tester.pumpWidget(PrismApp(
    localizationsDelegates: const [BoxUiDelegate(), GlobalWidgetsLocalizations.delegate],
    builder: (context, child) => ViewScope(size: MediaQuery.sizeOf(context), isMobile: true, child: child!),
    home: Surface(child: Center(child: SizedBox(width: width, height: height,
    child: MediaQuery(data: MediaQueryData(textScaler: TextScaler.linear(scale)),
      child: CodeEditor(controller: text, undoController: undo)),
  )))));
  await tester.pump();
}

double _textY(WidgetTester tester, int offset) {
  final render = tester.state<EditableTextState>(find.byType(EditableText)).renderEditable;
  // Compare identical strut bounds on every row. The default selection boxes
  // include different leading on the first line; carets snap to physical pixels.
  final previous = render.selectionHeightStyle;
  render.selectionHeightStyle = BoxHeightStyle.strut;
  final box = render.getBoxesForSelection(TextSelection(baseOffset: offset, extentOffset: offset + 1)).first;
  render.selectionHeightStyle = previous;
  return render.localToGlobal(box.toRect().topLeft).dy;
}

String _row(int number) => '  "r${number.toString().padLeft(2, '0')}": "abcdefgh",';

int _offset(JsonEditorController text, int row, int column) => text.text.indexOf(_row(row)) + column;

Future<EditableTextState> _select(WidgetTester tester, JsonEditorController text, TextSelection selection) async {
  final editable = tester.state<EditableTextState>(find.byType(EditableText));
  editable.widget.focusNode.requestFocus();
  await tester.pump();
  editable.userUpdateTextEditingValue(text.value.copyWith(selection: selection), SelectionChangedCause.longPress);
  await tester.pumpAndSettle();
  editable.hideToolbar(false);
  expect(editable.selectionOverlay!.handlesAreVisible, isTrue);
  return editable;
}

Offset _handleCenter(WidgetTester tester, EditableTextState editable, {required bool start}) {
  final render = editable.renderEditable;
  final link = start ? render.startHandleLayerLink : render.endHandleLayerLink;
  final follower = find.byWidgetPredicate((widget) => widget is CompositedTransformFollower && widget.link == link);
  return tester.getCenter(find.descendant(of: follower, matching: find.byType(RawGestureDetector)));
}

void main() {
  setUpAll(() async {
    final mono = await File('assets/fonts/JetBrainsMono-Regular.ttf').readAsBytes();
    await (FontLoader('JetBrainsMono')..addFont(Future.value(ByteData.sublistView(mono)))).load();
  });

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

    for (final scrolled in [false, true]) {
      for (final start in [true, false]) {
        testWidgets('${start ? 'start' : 'end'} handle selects the correct text rows at scale $scale, scrolled=$scrolled', (tester) async {
          final text = JsonEditorController()..text = [
            if (scrolled) '  "url": "https://example.org/${'segment/' * 16}",',
            ...List.generate(scrolled ? 80 : 18, _row),
          ].join('\n');
          final undo = UndoHistoryController();
          String? copied;
          final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
          messenger.setMockMethodCallHandler(SystemChannels.platform, (call) async {
            if (call.method == 'Clipboard.setData') copied = (call.arguments as Map)['text'] as String;
            if (call.method == 'Clipboard.hasStrings') return {'value': false};
            return null;
          });
          addTearDown(() async {
            await tester.pumpWidget(const SizedBox());
            text.dispose();
            undo.dispose();
            messenger.setMockMethodCallHandler(SystemChannels.platform, null);
          });
          await _mount(tester, text, undo, scale: scale, height: scrolled ? 440 : 540);
          final baseRow = scrolled ? 35 : 7;
          final initial = TextSelection(baseOffset: _offset(text, baseRow, 11), extentOffset: _offset(text, baseRow + 4, 16));
          final expected = TextSelection(
            baseOffset: _offset(text, start ? baseRow - 3 : baseRow, 11),
            extentOffset: _offset(text, start ? baseRow + 4 : baseRow + 7, 16),
          );
          final editable = await _select(tester, text, initial);
          if (scrolled) {
            final scroll = tester.widget<TextField>(find.byType(TextField)).scrollController!;
            final caret = editable.renderEditable.getLocalRectForCaret(initial.base);
            scroll.jumpTo(scroll.offset + caret.top - 140);
            await tester.pumpAndSettle();
            expect(scroll.offset, greaterThan(0));
          }
          final origin = _handleCenter(tester, editable, start: start);
          final lineHeight = editable.renderEditable.preferredLineHeight;
          final direction = start ? -1 : 1;
          final gesture = await tester.startGesture(origin);
          await tester.pump();
          // Move three visual rows at the same column. The other boundary stays
          // fixed, and scroll offsets must not affect the selected text positions.
          await gesture.moveTo(origin + Offset(0, direction * lineHeight * 2.6));
          await tester.pump();
          await gesture.moveTo(origin + Offset(0, direction * lineHeight * 3.25));
          await tester.pump();
          expect(text.selection, expected);
          await gesture.up();
          await tester.pumpAndSettle();
          await tester.tap(find.text('Copy'));
          await tester.pumpAndSettle();
          expect(copied, expected.textInside(text.text));
          expect(tester.takeException(), isNull);
        });
      }
    }
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
