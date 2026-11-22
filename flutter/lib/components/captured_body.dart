import 'dart:async';
import 'dart:convert';
import 'dart:math' as math;
import 'dart:typed_data';
import 'package:flutter/services.dart';
import '../common/body_content.dart';
import '../common/http_message.dart';
import '../widgets.dart';

enum _BodyView { content, hex, base64 }

class CapturedBody extends StatefulWidget {
  const CapturedBody({super.key, required this.message, required this.preview, this.loadPage})
    : _fullPage = false, _initialSession = null, _initialOffsets = const [], _initialView = null, _initialFormatted = true;
  const CapturedBody._viewer({required this.message, required this.preview, this.loadPage,
    required int session, required List<int> previousOffsets, required _BodyView view, required bool formatted})
    : _fullPage = true, _initialSession = session, _initialOffsets = previousOffsets, _initialView = view, _initialFormatted = formatted;
  final Json message, preview;
  final Future<Json> Function(int offset, String source)? loadPage;
  final bool _fullPage, _initialFormatted;
  final int? _initialSession;
  final List<int> _initialOffsets;
  final _BodyView? _initialView;
  @override State<CapturedBody> createState() => _CapturedBodyState();
}

class _CapturedBodyState extends State<CapturedBody> {
  final _scroll = ScrollController();
  final _horizontal = ScrollController();
  final _transform = TransformationController();
  final _previousOffsets = <int>[];
  bool _formatted = true, _loading = false, _wrap = true;
  int _generation = 0;
  int _hexColumns = 16;
  int? _session;
  Json _page = {};
  late CapturedBodyContent _content;
  _BodyView? _view;
  String _text = '';
  String? _pretty, _error;
  String _contentType = '', _grpcEncoding = '';
  bool? _bodyComplete, _bodyTruncated;
  int get _offset => (_page['offset'] as num? ?? 0).toInt();
  _BodyView get _activeView => _view == _BodyView.content && _content.kind == CapturedBodyKind.binary
    ? _BodyView.hex : _view ?? (_content.kind == CapturedBodyKind.binary ? _BodyView.hex : _BodyView.content);

  @override void initState() {
    super.initState();
    _session = widget._initialSession;
    _previousOffsets.addAll(widget._initialOffsets);
    _view = widget._initialView;
    _formatted = widget._initialFormatted;
    _update(widget.preview);
  }
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
    final contentType = capturedHeader(rows(widget.message['headers']), 'content-type');
    final grpcEncoding = capturedHeader(rows(widget.message['headers']), 'grpc-encoding');
    final same = _page.isNotEmpty && contentType == _contentType && grpcEncoding == _grpcEncoding &&
      widget.message['bodyComplete'] == _bodyComplete && widget.message['bodyTruncated'] == _bodyTruncated &&
      ['text', 'encoding', 'source', 'offset', 'hasMore', 'truncated', 'error'].every((field) => page[field] == _page[field]);
    _page = page;
    if (same) return;
    _contentType = contentType;
    _grpcEncoding = grpcEncoding;
    _bodyComplete = widget.message['bodyComplete'] as bool?;
    _bodyTruncated = widget.message['bodyTruncated'] as bool?;
    _text = page['text']?.toString() ?? '';
    _content = readCapturedBody(widget.message, page);
    _pretty = _content.kind == CapturedBodyKind.text && _content.format == CapturedBodyFormat.plain && _content.complete
      ? formatCapturedJson(_content.text, maxOutputLength: 65536) : null;
  }
  @override void dispose() {
    _generation++;
    _scroll.dispose();
    _horizontal.dispose();
    _transform.dispose();
    super.dispose();
  }

  bool _current() => mounted && !context.box.remote && context.box.sessionRevision == _session;
  String get _shownText => _activeView == _BodyView.base64 ?
    (_page['encoding'] == 'base64' ? _text : base64Encode(_content.bytes)) : _formatted ? _pretty ?? _content.text : _content.text;

  Future<void> _copy([String? text]) async {
    if (!_current()) return;
    try {
      await Clipboard.setData(ClipboardData(text: text ?? _text));
      if (_current()) context.box.messages.add(context.tr('Copied'));
    } catch (error) {
      if (_current()) context.box.report(error);
    }
  }

  void _resetScroll() {
    if (_scroll.hasClients) _scroll.jumpTo(0);
    if (_horizontal.hasClients) _horizontal.jumpTo(0);
    _transform.value = Matrix4.identity();
  }

  void _select(_BodyView value) {
    if (!_current()) return;
    setState(() => _view = value);
    _resetScroll();
  }

  void _open() {
    if (!_current() || widget._fullPage) return;
    // Inspect a snapshot of the current page. The viewer also holds only one
    // body page and keeps the original session and byte-offset history.
    openPage(context, CapturedBody._viewer(
      message: Map<String, dynamic>.unmodifiable(widget.message), preview: Map<String, dynamic>.unmodifiable(_page),
      loadPage: widget.loadPage, session: _session!, previousOffsets: List<int>.unmodifiable(_previousOffsets),
      view: _activeView, formatted: _formatted,
    ));
  }

  Future<void> _copyDisplayed() => _copy(_content.error != null ? _text : _activeView == _BodyView.hex ? [
    for (var offset = 0; offset < _content.bytes.length; offset += _hexColumns)
      capturedHexLine(_content.bytes, offset, _hexColumns, baseOffset: _offset),
  ].join('\n') : _shownText);

  List<IconButtonData> _actions({required bool expanded}) => [
    if (_activeView == _BodyView.content && _pretty != null) IconButtonData(glyph: AppGlyphs.code,
      tooltip: context.tr(_formatted ? 'Show original' : 'Format JSON'), isSelected: _formatted,
      onPressed: () { setState(() => _formatted = !_formatted); _resetScroll(); }),
    if (expanded && (_activeView == _BodyView.base64 || _activeView == _BodyView.content && _content.kind == CapturedBodyKind.text))
      IconButtonData(glyph: AppGlyphs.wrap, tooltip: context.tr('Wrap lines'), isSelected: _wrap,
        onPressed: () { setState(() => _wrap = !_wrap); _resetScroll(); }),
    if (expanded && _activeView == _BodyView.content && _content.kind == CapturedBodyKind.image)
      IconButtonData(glyph: AppGlyphs.reset, tooltip: context.tr('Reset zoom'),
        onPressed: () => _transform.value = Matrix4.identity()),
    if (!expanded) IconButtonData(glyph: AppGlyphs.openExternal, tooltip: context.tr('View body'),
      onPressed: _text.isEmpty ? null : _open),
    IconButtonData(glyph: AppGlyphs.copy, tooltip: context.tr('Copy current body page'), onPressed: _text.isEmpty ? null : () => _copy()),
  ];

  String get _contentLabel => _content.kind == CapturedBodyKind.image ? 'Image' : switch (_content.format) {
    CapturedBodyFormat.protobuf => 'Protobuf',
    CapturedBodyFormat.grpc => 'gRPC',
    CapturedBodyFormat.plain => 'Text',
  };

  Widget _body(double maxHeight) {
    if (_text.isEmpty) return Text(context.tr('No body captured'), style: context.listSubtitleStyle);
    if (_activeView == _BodyView.content && _content.kind == CapturedBodyKind.image) {
      return _BodyImage(bytes: _content.bytes, height: math.min(maxHeight, widget._fullPage ? maxHeight : 240.0),
        transform: widget._fullPage ? _transform : null, onOpen: widget._fullPage ? null : _open,
        onHex: () => _select(_BodyView.hex));
    }
    final style = AppTextStyle.footnote.of(context).toJetBrainsMono.copyWith(height: 1.5, color: context.colorScheme.onSurface);
    return LayoutBuilder(builder: (context, constraints) {
      if (_activeView == _BodyView.hex) {
        final measure = TextPainter(text: TextSpan(text: '0', style: style), textDirection: TextDirection.ltr,
          textScaler: MediaQuery.textScalerOf(context))..layout();
        final width = measure.width, lineHeight = measure.height + 2;
        measure.dispose();
        _hexColumns = [16, 8, 4, 2, 1].firstWhere((count) =>
          capturedHexLineLength(_content.bytes.length, count, baseOffset: _offset) * width <= constraints.maxWidth,
          orElse: () => 1);
        final count = (_content.bytes.length / _hexColumns).ceil();
        return SizedBox(height: math.min(maxHeight, count * lineHeight), child: Scrollbar(controller: _scroll,
          child: ListView.builder(controller: _scroll, primary: false, padding: EdgeInsets.zero, itemExtent: lineHeight, itemCount: count,
            itemBuilder: (context, index) => SelectableText(
              capturedHexLine(_content.bytes, index * _hexColumns, _hexColumns, baseOffset: _offset),
              key: ValueKey('captured-body-hex-line-${_offset + index * _hexColumns}'),
              textDirection: TextDirection.ltr, style: style, minLines: 1, maxLines: 1,
              strutStyle: StrutStyle.fromTextStyle(style, forceStrutHeight: true),
            )),
        ));
      }
      final text = _shownText;
      var width = constraints.maxWidth;
      if (widget._fullPage && !_wrap) {
        final measure = TextPainter(text: TextSpan(text: text, style: style), textDirection: TextDirection.ltr,
          textScaler: MediaQuery.textScalerOf(context))..layout();
        width = math.max(width, measure.width + 4);
        measure.dispose();
      }
      Widget child = SizedBox(width: width, child: SelectableText(text,
        key: const ValueKey('captured-body-text'), textDirection: TextDirection.ltr, style: style));
      if (widget._fullPage && !_wrap) child = Scrollbar(controller: _horizontal,
        child: SingleChildScrollView(controller: _horizontal, primary: false, scrollDirection: Axis.horizontal, child: child));
      return ConstrainedBox(constraints: BoxConstraints(maxHeight: maxHeight), child: Scrollbar(controller: _scroll,
        child: SingleChildScrollView(controller: _scroll, primary: false, child: child)));
    });
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
      _resetScroll();
    } catch (failure) {
      if (mounted && generation == _generation && !box.remote && box.sessionRevision == session) {
        setState(() => _error = BoxController.describe(failure));
      }
    } finally {
      if (mounted && generation == _generation) setState(() => _loading = false);
    }
  }

  @override Widget build(BuildContext context) {
    final box = context.watchBox(BoxAspect.connections);
    if (box.remote || box.sessionRevision != _session) return widget._fullPage
      ? BoxPage(title: 'Body', children: const [EmptyState('Session changed; reopen this activity from the current list')])
      : const SizedBox.shrink();
    final message = widget.message, preview = _page;
    final paged = preview['hasMore'] == true || _previousOffsets.isNotEmpty;
    final card = Card(shape: AppShape.xl, padding: const EdgeInsets.all(16), child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, spacing: 12, children: [
        Text([
          if (_contentType.isNotEmpty) _contentType,
          '${bytes(message['capturedBytes'] as num? ?? 0)} / ${bytes(message['bodyBytes'] as num? ?? 0)}',
          if (_activeView == _BodyView.base64) 'Base64',
          if (_activeView == _BodyView.content && _formatted && _pretty != null) 'JSON',
          if (_activeView == _BodyView.content && _content.format != CapturedBodyFormat.plain) _contentLabel,
        ].join(' · '), style: context.listCaptionStyle),
        if (_text.isNotEmpty && _content.error == null) SegmentedControl<_BodyView>(
          segments: [
            if (_content.kind != CapturedBodyKind.binary) (_BodyView.content, context.tr(_contentLabel)),
            (_BodyView.hex, context.tr('Hex')), (_BodyView.base64, 'Base64'),
          ], value: _activeView, onChanged: _select,
        ),
        if (_activeView == _BodyView.content && _content.format != CapturedBodyFormat.plain)
          Text(context.tr('Field numbers are shown without a Protobuf schema.'), style: context.listCaptionStyle),
        if (message['bodyTruncated'] == true)
          Text(context.tr('Captured body was truncated.'), style: context.listSubtitleStyle),
        if (preview['truncated'] == true && message['bodyTruncated'] != true)
          Text(context.tr('Body could not be fully decoded.'), style: context.listSubtitleStyle),
        if (preview['source'] == 'raw')
          Text(context.tr('Showing saved bytes without decoding.'), style: context.listSubtitleStyle),
        if ((preview['error'] as String? ?? '').isNotEmpty)
          Text(preview['error'] as String, style: context.listSubtitleStyle),
        if (_content.error != null) Text(context.tr('Body data could not be read.'), style: context.listSubtitleStyle),
        if (message['bodyComplete'] != true) Text(context.tr('Body stream is incomplete'), style: context.listSubtitleStyle),
        if (_content.error == null) _body(MediaQuery.sizeOf(context).height * (widget._fullPage ? .7 : .5))
        else SelectableText(_text, key: const ValueKey('captured-body-text'), style: context.listSubtitleStyle),
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
      ]));
    if (!widget._fullPage) return Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
      ListHeader(title: context.tr('Body'), actions: _actions(expanded: false)), card,
    ]);
    return Scaffold(key: const ValueKey('captured-body-viewer'), title: context.tr('Body'),
      iconActions: _actions(expanded: true), menuItems: [
        PopupMenuItem(glyph: AppGlyphs.copy, label: context.tr('Copy displayed body'),
          onPressed: _text.isEmpty || _activeView == _BodyView.content && _content.kind == CapturedBodyKind.image ? null : _copyDisplayed),
      ], body: Builder(builder: (context) {
        final inset = context.appBarInset, gap = context.contentTopPadding - inset;
        return Padding(padding: EdgeInsets.only(top: inset), child: ListView(
          padding: EdgeInsets.fromLTRB(16, math.max(0.0, gap), 16, 24 + context.bottomInset), children: [card],
        ));
      }));
  }
}

class _BodyImage extends StatefulWidget {
  const _BodyImage({required this.bytes, required this.height, required this.onHex, this.transform, this.onOpen});
  final Uint8List bytes;
  final double height;
  final TransformationController? transform;
  final VoidCallback onHex;
  final VoidCallback? onOpen;
  @override State<_BodyImage> createState() => _BodyImageState();
}

class _BodyImageState extends State<_BodyImage> {
  ResizeImage? _provider;
  int _width = 0, _height = 0;
  void _evict() { if (_provider != null) unawaited(_provider!.evict()); }
  @override void didUpdateWidget(_BodyImage oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (!identical(oldWidget.bytes, widget.bytes)) { _evict(); _provider = null; }
  }
  @override void dispose() { _evict(); super.dispose(); }
  @override Widget build(BuildContext context) => LayoutBuilder(builder: (context, constraints) {
    final ratio = MediaQuery.devicePixelRatioOf(context);
    final width = (constraints.maxWidth * ratio).ceil().clamp(1, 2048).toInt();
    final height = (widget.height * ratio).ceil().clamp(1, 2048).toInt();
    if (_provider == null || width != _width || height != _height) {
      _evict();
      _width = width; _height = height;
      _provider = ResizeImage(MemoryImage(widget.bytes), width: width, height: height, policy: ResizeImagePolicy.fit);
    }
    final image = Image(key: const ValueKey('captured-body-image'), image: _provider!, fit: BoxFit.contain,
      width: constraints.maxWidth, height: widget.height, semanticLabel: context.tr('Captured image'),
      errorBuilder: (context, error, stack) => Center(child: Column(mainAxisSize: MainAxisSize.min, children: [
        Text(context.tr('Image preview unavailable'), style: context.listSubtitleStyle),
        TextButton(onPressed: widget.onHex, child: Text(context.tr('Show hexadecimal'))),
      ])));
    return ClipRect(child: widget.transform != null
      ? InteractiveViewer(transformationController: widget.transform, minScale: 1, maxScale: 6, child: image)
      : GestureDetector(onTap: widget.onOpen, child: image));
  });
}
