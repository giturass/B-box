import 'package:flutter/services.dart';
import '../widgets.dart';
import '../common/http_message.dart';

class CapturedBody extends StatefulWidget {
  const CapturedBody({super.key, required this.message, required this.preview, this.loadPage});
  final Json message, preview;
  final Future<Json> Function(int offset, String source)? loadPage;
  @override State<CapturedBody> createState() => _CapturedBodyState();
}

class _CapturedBodyState extends State<CapturedBody> {
  final _scroll = ScrollController();
  final _previousOffsets = <int>[];
  bool _formatted = true, _loading = false;
  int _generation = 0;
  int? _session;
  Json _page = {};
  String _text = '';
  String? _pretty, _error;
  int get _offset => (_page['offset'] as num? ?? 0).toInt();

  @override void initState() { super.initState(); _update(widget.preview); }
  @override void didChangeDependencies() {
    super.didChangeDependencies();
    _session ??= context.box.sessionRevision;
  }
  @override void didUpdateWidget(CapturedBody oldWidget) {
    super.didUpdateWidget(oldWidget);
    // A live capture may append while a later page is being inspected. Keep
    // that page stable; returning to the first page reads its latest snapshot.
    if (_offset == 0) _update(widget.preview);
  }
  void _update(Json page) {
    _page = page;
    _text = page['text']?.toString() ?? '';
    _pretty = page['encoding'] == 'text' && page['truncated'] != true && page['hasMore'] != true && _offset == 0 &&
        widget.message['bodyComplete'] == true && widget.message['bodyTruncated'] != true ?
        formatCapturedJson(_text, maxOutputLength: 65536) : null;
  }
  @override void dispose() { _generation++; _scroll.dispose(); super.dispose(); }

  Future<void> _copy() async {
    await Clipboard.setData(ClipboardData(text: _text));
    if (mounted) context.box.messages.add(context.tr('Copied'));
  }

  Future<void> _load(int offset, {required bool previous}) async {
    final box = context.box, loader = widget.loadPage;
    if (_loading || loader == null || box.remote || box.sessionRevision != _session) return;
    final generation = ++_generation, session = box.sessionRevision, oldOffset = _offset;
    setState(() { _loading = true; _error = null; });
    try {
      final page = await loader(offset, _page['source'] as String? ?? 'decoded');
      if (!mounted || generation != _generation || box.remote || box.sessionRevision != session) return;
      setState(() {
        if (previous) { _previousOffsets.removeLast(); } else { _previousOffsets.add(oldOffset); }
        _update(page);
      });
      if (_scroll.hasClients) _scroll.jumpTo(0);
    } catch (failure) {
      if (mounted && generation == _generation && !box.remote && box.sessionRevision == session) {
        setState(() => _error = BoxController.describe(failure));
      }
    } finally {
      if (mounted && generation == _generation) setState(() => _loading = false);
    }
  }

  @override Widget build(BuildContext context) {
    final box = context.box;
    if (box.remote || box.sessionRevision != _session) return const SizedBox.shrink();
    final message = widget.message, preview = _page;
    final contentType = capturedHeader(rows(message['headers']), 'content-type');
    final text = _formatted ? _pretty ?? _text : _text;
    final paged = preview['hasMore'] == true || _previousOffsets.isNotEmpty;
    return Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
      ListHeader(title: context.tr('Body'), actions: [
        if (_pretty != null) IconButtonData(glyph: AppGlyphs.code,
          tooltip: context.tr(_formatted ? 'Show original' : 'Format JSON'), isSelected: _formatted,
          onPressed: () { setState(() => _formatted = !_formatted); if (_scroll.hasClients) _scroll.jumpTo(0); }),
        IconButtonData(glyph: AppGlyphs.copy, tooltip: context.tr('Copy current body page'), onPressed: _text.isEmpty ? null : _copy),
      ]),
      Card(shape: AppShape.xl, padding: const EdgeInsets.all(16), child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, spacing: 12, children: [
        Text([
          if (contentType.isNotEmpty) contentType,
          '${bytes(message['capturedBytes'] as num? ?? 0)} / ${bytes(message['bodyBytes'] as num? ?? 0)}',
          if (preview['encoding'] == 'base64') 'Base64',
          if (_formatted && _pretty != null) 'JSON',
        ].join(' · '), style: context.listCaptionStyle),
        if (message['bodyTruncated'] == true)
          Text(context.tr('Captured body was truncated.'), style: context.listSubtitleStyle),
        if (preview['truncated'] == true && message['bodyTruncated'] != true)
          Text(context.tr('Body could not be fully decoded.'), style: context.listSubtitleStyle),
        if (preview['source'] == 'raw')
          Text(context.tr('Showing saved bytes without decoding.'), style: context.listSubtitleStyle),
        if ((preview['error'] as String? ?? '').isNotEmpty)
          Text(preview['error'] as String, style: context.listSubtitleStyle),
        if (message['bodyComplete'] != true) Text(context.tr('Body stream is incomplete'), style: context.listSubtitleStyle),
        if (text.isEmpty) Text(context.tr('No body captured'), style: context.listSubtitleStyle)
        else ConstrainedBox(
          constraints: BoxConstraints(maxHeight: MediaQuery.sizeOf(context).height * .5),
          child: Scrollbar(controller: _scroll, child: SingleChildScrollView(controller: _scroll, primary: false,
            child: SizedBox(width: double.infinity, child: SelectableText(text,
              key: const ValueKey('captured-body-text'),
              style: AppTextStyle.footnote.of(context).toJetBrainsMono.copyWith(height: 1.5, color: context.colorScheme.onSurface))),
          )),
        ),
        if (_error != null) Text(_error!, style: context.listSubtitleStyle),
        if (paged) ...[
          Text('${context.tr('Body page')}: ${_previousOffsets.length + 1}', style: context.listCaptionStyle),
          Wrap(spacing: 8, children: [
            if (_previousOffsets.isNotEmpty) TextButton(
              onPressed: _loading ? null : () => _load(_previousOffsets.last, previous: true),
              child: Text(context.tr('Previous page'))),
            if (preview['hasMore'] == true) TextButton(
              onPressed: _loading || widget.loadPage == null ? null : () => _load((preview['nextOffset'] as num).toInt(), previous: false),
              child: Text(context.tr('Next page'))),
          ]),
        ],
      ])),
    ]);
  }
}
