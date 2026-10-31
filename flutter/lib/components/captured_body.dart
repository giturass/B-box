import 'package:flutter/services.dart';
import '../widgets.dart';
import '../common/http_message.dart';

class CapturedBody extends StatefulWidget {
  const CapturedBody({super.key, required this.message, required this.preview});
  final Json message, preview;
  @override State<CapturedBody> createState() => _CapturedBodyState();
}

class _CapturedBodyState extends State<CapturedBody> {
  static const _pageSize = 16384;
  final _scroll = ScrollController();
  int _limit = _pageSize;
  bool _formatted = true;
  String _text = '';
  String? _pretty;

  @override void initState() { super.initState(); _update(); }
  @override void didUpdateWidget(CapturedBody oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.preview['text'] != widget.preview['text'] || oldWidget.preview['encoding'] != widget.preview['encoding'] ||
        oldWidget.preview['truncated'] != widget.preview['truncated'] || oldWidget.message['bodyComplete'] != widget.message['bodyComplete'] ||
        oldWidget.message['bodyTruncated'] != widget.message['bodyTruncated']) _update();
  }
  void _update() {
    _text = widget.preview['text']?.toString() ?? '';
    _pretty = widget.preview['encoding'] == 'text' && widget.preview['truncated'] != true &&
        widget.message['bodyComplete'] == true && widget.message['bodyTruncated'] != true ? formatCapturedJson(_text) : null;
  }
  @override void dispose() { _scroll.dispose(); super.dispose(); }

  Future<void> _copy() async {
    await Clipboard.setData(ClipboardData(text: _text));
    if (mounted) context.box.messages.add(context.tr('Copied'));
  }

  @override Widget build(BuildContext context) {
    final message = widget.message, preview = widget.preview;
    final contentType = capturedHeader(rows(message['headers']), 'content-type');
    final text = _formatted ? _pretty ?? _text : _text;
    final visible = capturedTextPrefix(text, _limit);
    return Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
      ListHeader(title: context.tr('Body'), actions: [
        if (_pretty != null) IconButtonData(glyph: AppGlyphs.code,
          tooltip: context.tr(_formatted ? 'Show original' : 'Format JSON'), isSelected: _formatted,
          onPressed: () { setState(() { _formatted = !_formatted; _limit = _pageSize; }); if (_scroll.hasClients) _scroll.jumpTo(0); }),
        IconButtonData(glyph: AppGlyphs.copy, tooltip: context.tr('Copy body preview'), onPressed: _text.isEmpty ? null : _copy),
      ]),
      CommonCard(shape: AppShape.xl, padding: const EdgeInsets.all(16), child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, spacing: 12, children: [
        Text([
          if (contentType.isNotEmpty) contentType,
          '${bytes(message['capturedBytes'] as num? ?? 0)} / ${bytes(message['bodyBytes'] as num? ?? 0)}',
          if (preview['encoding'] == 'base64') 'Base64',
          if (_formatted && _pretty != null) 'JSON',
        ].join(' · '), style: context.listCaptionStyle),
        if (message['bodyTruncated'] == true || preview['truncated'] == true)
          Text(context.tr('Only a limited body preview was saved or decoded.'), style: context.listSubtitleStyle),
        if (message['bodyComplete'] != true) Text(context.tr('Body stream is incomplete'), style: context.listSubtitleStyle),
        if (text.isEmpty) Text(context.tr('No body captured'), style: context.listSubtitleStyle)
        else ConstrainedBox(
          constraints: BoxConstraints(maxHeight: MediaQuery.sizeOf(context).height * .5),
          child: Scrollbar(controller: _scroll, child: SingleChildScrollView(controller: _scroll, primary: false,
            child: SizedBox(width: double.infinity, child: SelectableText(visible,
              key: const ValueKey('captured-body-text'),
              style: AppTextStyle.footnote.of(context).toJetBrainsMono.copyWith(height: 1.5, color: context.colorScheme.onSurface))),
          )),
        ),
        if (text.length > visible.length || _limit > _pageSize) Wrap(spacing: 8, children: [
          if (text.length > visible.length) TextButton(onPressed: () => setState(() => _limit += _pageSize), child: Text(context.tr('Show more'))),
          if (_limit > _pageSize) TextButton(onPressed: () { setState(() => _limit = _pageSize); if (_scroll.hasClients) _scroll.jumpTo(0); }, child: Text(context.tr('Show less'))),
        ]),
      ])),
    ]);
  }
}
