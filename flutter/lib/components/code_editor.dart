import 'dart:math' as math;
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

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
  String? _content;
  int _lineCount = 1;
  Object? _layoutKey;
  List<({int number, double top})> _lines = [];

  @override void initState() {
    super.initState();
    widget.controller.addListener(_changed);
    _changed();
  }

  void _changed() {
    if (_content == widget.controller.text) return;
    setState(() {
      _content = widget.controller.text;
      _lineCount = '\n'.allMatches(_content!).length + 1;
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
    super.dispose();
  }

  @override Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final style = TextStyle(inherit: false, fontFamily: 'JetBrainsMono', fontSize: 13,
      fontWeight: FontWeight.normal, height: 1.6, color: scheme.onSurface);
    final strut = StrutStyle.fromTextStyle(style, forceStrutHeight: true);
    final scaler = MediaQuery.textScalerOf(context);
    final locale = Localizations.maybeLocaleOf(context);
    final number = TextPainter(text: TextSpan(text: '$_lineCount', style: style),
      textDirection: TextDirection.ltr, textScaler: scaler, strutStyle: strut, locale: locale)..layout();
    final gutterWidth = number.width + 24;
    final numberBaseline = number.computeLineMetrics().single.baseline;
    number.dispose();
    return LayoutBuilder(builder: (context, constraints) {
      // RenderEditable in the pinned Flutter version reserves cursorWidth + 1
      // pixels after its text. Keep the same wrapping width as the text field.
      final textWidth = math.max(0.0, constraints.maxWidth - gutterWidth - 12 - 16 - 3);
      final layoutKey = (_content, textWidth, style, scaler, locale);
      if (_layoutKey != layoutKey) {
        final text = TextPainter(text: TextSpan(text: _content, style: style),
          textDirection: TextDirection.ltr, textScaler: scaler, strutStyle: strut, locale: locale)
          ..layout(minWidth: textWidth, maxWidth: textWidth);
        var logicalLine = 1;
        var start = true;
        _lines = [];
        for (final line in text.computeLineMetrics()) {
          if (start) _lines.add((number: logicalLine, top: line.baseline - numberBaseline));
          start = line.hardBreak;
          if (start) logicalLine++;
        }
        text.dispose();
        _layoutKey = layoutKey;
      }
      return Row(textDirection: TextDirection.ltr, crossAxisAlignment: CrossAxisAlignment.stretch, children: [
        ExcludeSemantics(child: SizedBox(width: gutterWidth, child: ColoredBox(
          color: scheme.surfaceContainerLow,
          child: ClipRect(child: ListenableBuilder(listenable: _scroll, builder: (context, _) {
            final offset = _scroll.hasClients ? _scroll.offset : 0.0;
            // Only materialize visible numbers; scrolling does not lay out the document again.
            var low = 0, high = _lines.length;
            while (low < high) {
              final middle = (low + high) ~/ 2;
              if (_lines[middle].top < offset - 16) { low = middle + 1; }
              else { high = middle; }
            }
            final first = math.max(0, low - 1);
            return Stack(fit: StackFit.expand, children: [
              for (var index = first; index < _lines.length && _lines[index].top < offset + constraints.maxHeight; index++)
                Positioned(top: 16 + _lines[index].top - offset, left: 0, right: 12,
                  child: Text('${_lines[index].number}', key: ValueKey('code-line-${_lines[index].number}'),
                    textAlign: TextAlign.right, textDirection: TextDirection.ltr,
                    style: style.copyWith(color: scheme.onSurfaceVariant), strutStyle: strut)),
            ]);
          })),
        ))),
        Expanded(child: NotificationListener<ScrollMetricsNotification>(onNotification: (_) {
          // A shorter document can clamp the offset during layout without a scroll event.
          WidgetsBinding.instance.addPostFrameCallback((_) { if (mounted) setState(() {}); });
          return false;
        }, child: TextField(
          controller: widget.controller, undoController: widget.undoController, scrollController: _scroll,
          expands: true, maxLines: null, minLines: null, readOnly: widget.readOnly,
          autocorrect: false, enableSuggestions: false,
          smartDashesType: SmartDashesType.disabled, smartQuotesType: SmartQuotesType.disabled,
          keyboardType: TextInputType.multiline, textDirection: TextDirection.ltr,
          textAlignVertical: TextAlignVertical.top, style: style, strutStyle: strut, cursorWidth: 2,
          decoration: const InputDecoration(border: InputBorder.none, isDense: true,
            contentPadding: EdgeInsets.fromLTRB(12, 16, 16, 16)),
        ))),
      ]);
    });
  }
}
