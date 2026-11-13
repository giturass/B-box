import 'dart:math' as math;
import 'package:flutter/widgets.dart';
import 'package:prism_ui/prism_ui.dart';

/// A JSON text field with a gutter for logical lines, including wrapped lines.
class CodeEditor extends StatefulWidget {
  const CodeEditor({super.key, required this.controller, required this.undoController, this.readOnly = false});
  final TextEditingController controller;
  final UndoHistoryController undoController;
  final bool readOnly;

  @override State<CodeEditor> createState() => _CodeEditorState();
}

class _CodeEditorState extends State<CodeEditor> {
  final _scroll = ScrollController();
  final _gutterRevision = ValueNotifier(0);
  String? _content;
  List<int> _lineStarts = [0];

  @override void initState() {
    super.initState();
    widget.controller.addListener(_changed);
    _changed();
  }

  void _changed() {
    if (_content == widget.controller.text) return;
    setState(() {
      _content = widget.controller.text;
      _lineStarts = [0, for (final newline in '\n'.allMatches(_content!)) newline.end];
    });
  }

  @override void didUpdateWidget(CodeEditor oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.controller != widget.controller) {
      oldWidget.controller.removeListener(_changed);
      widget.controller.addListener(_changed);
      _changed();
    }
  }

  @override void dispose() {
    widget.controller.removeListener(_changed);
    _scroll.dispose();
    _gutterRevision.dispose();
    super.dispose();
  }

  EditableTextState? _editableForScroll(BuildContext? scrollContext) =>
    scrollContext?.findAncestorStateOfType<EditableTextState>();

  void _updateSelectionOverlay(EditableTextState? editable) {
    if (editable == null || !editable.mounted) return;
    // The pinned Flutter SDK has no public refresh method for handle endpoints.
    // ignore: invalid_use_of_visible_for_testing_member
    editable.selectionOverlay?.updateForScroll();
  }

  int _lineAt(int offset) {
    var low = 0, high = _lineStarts.length;
    while (low < high) {
      final middle = (low + high) ~/ 2;
      if (_lineStarts[middle] <= offset) { low = middle + 1; }
      else { high = middle; }
    }
    return math.max(0, low - 1);
  }

  @override Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final style = TextStyle(inherit: false, fontFamily: 'JetBrainsMono', fontSize: 13,
      fontWeight: FontWeight.normal, textBaseline: TextBaseline.alphabetic, height: 1.6, color: scheme.onSurface);
    final strut = StrutStyle.fromTextStyle(style, forceStrutHeight: true);
    final scaler = MediaQuery.textScalerOf(context);
    final locale = Localizations.maybeLocaleOf(context);
    final number = TextPainter(text: TextSpan(text: '${_lineStarts.length}', style: style),
      textDirection: TextDirection.ltr, textScaler: scaler, strutStyle: strut, locale: locale)..layout();
    final gutterWidth = number.width + 24;
    number.dispose();
    return LayoutBuilder(builder: (context, constraints) {
      return CustomMultiChildLayout(delegate: _CodeEditorLayout(
        gutterWidth, (_content, constraints, style, scaler, locale)), children: [
        LayoutId(id: _EditorPart.gutter, child: ExcludeSemantics(child: ColoredBox(
          color: scheme.surfaceContainerLow,
          child: ClipRect(child: ListenableBuilder(listenable: Listenable.merge([_scroll, _gutterRevision]),
            builder: (context, _) => LayoutBuilder(builder: (context, _) {
            final editable = _scroll.hasClients ? _editableForScroll(_scroll.position.context.notificationContext) : null;
            if (editable == null || !editable.renderEditable.hasSize) return const SizedBox.shrink();
            final render = editable.renderEditable;
            final first = _lineAt(render.getPositionForPoint(render.localToGlobal(Offset.zero)).offset);
            final last = _lineAt(render.getPositionForPoint(render.localToGlobal(render.size.bottomRight(Offset.zero))).offset);
            // Reuse the field's paragraph, including its actual wrapping and
            // font fallback. Collapsed endpoints expose unsnapped line offsets;
            // getLocalRectForCaret would round each row to physical pixels.
            return Stack(fit: StackFit.expand, children: [
              for (var index = first; index <= last; index++)
                Positioned(top: 16 + render.getEndpointsForSelection(TextSelection.collapsed(offset: _lineStarts[index])).single.point.dy - render.preferredLineHeight,
                  left: 0, right: 12,
                  child: Text('${index + 1}', key: ValueKey('code-line-${index + 1}'),
                    textAlign: TextAlign.right, textDirection: TextDirection.ltr,
                    style: style.copyWith(color: scheme.onSurfaceVariant), strutStyle: strut)),
            ]);
          }))),
        ))),
        LayoutId(id: _EditorPart.field, child: NotificationListener<ScrollMetricsNotification>(onNotification: (notification) {
          // A shorter document can clamp the offset during layout without a scroll event.
          final editable = _editableForScroll(notification.context);
          WidgetsBinding.instance.addPostFrameCallback((_) {
            if (!mounted) return;
            _updateSelectionOverlay(editable);
            _gutterRevision.value++;
          });
          return false;
        }, child: NotificationListener<ScrollNotification>(onNotification: (notification) {
          // Android moves the painted handles during scrolling without refreshing
          // the endpoints cached by TextSelectionOverlay for handle drag targets.
          _updateSelectionOverlay(_editableForScroll(notification.context));
          return false;
        }, child: TextField(
          controller: widget.controller, undoController: widget.undoController, scrollController: _scroll,
          expands: true, maxLines: null, minLines: null, readOnly: widget.readOnly,
          autocorrect: false, enableSuggestions: false,
          smartDashesType: SmartDashesType.disabled, smartQuotesType: SmartQuotesType.disabled,
          keyboardType: TextInputType.multiline, textDirection: TextDirection.ltr,
          textAlignVertical: TextAlignVertical.top, style: style, strutStyle: strut, cursorWidth: 2,
          decoration: const InputDecoration(border: const NoInputBorder(), isDense: true,
            contentPadding: EdgeInsets.fromLTRB(12, 16, 16, 16)),
        )))),
      ]);
    });
  }
}

enum _EditorPart { field, gutter }

class _CodeEditorLayout extends MultiChildLayoutDelegate {
  _CodeEditorLayout(this.gutterWidth, this.layoutKey);
  final double gutterWidth;
  final Object layoutKey;

  @override void performLayout(Size size) {
    final width = math.min(gutterWidth, size.width);
    // The gutter's LayoutBuilder reads RenderEditable only after the field has
    // laid out the new text with its current scroll offset in this same frame.
    layoutChild(_EditorPart.field, BoxConstraints.tight(Size(size.width - width, size.height)));
    positionChild(_EditorPart.field, Offset(width, 0));
    layoutChild(_EditorPart.gutter, BoxConstraints.tight(Size(width, size.height)));
    positionChild(_EditorPart.gutter, Offset.zero);
  }

  @override bool shouldRelayout(_CodeEditorLayout oldDelegate) =>
    oldDelegate.gutterWidth != gutterWidth || oldDelegate.layoutKey != layoutKey;
}
