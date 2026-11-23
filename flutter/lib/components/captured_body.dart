import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math' as math;
import 'dart:typed_data';
import 'package:flutter/services.dart';
import '../common/body_content.dart';
import '../common/http_message.dart';
import '../widgets.dart';

enum _BodyView { content, hex, base64 }
enum _PageMove { next, previous, refresh }

int _readerSequence = 0;

class CapturedBody extends StatefulWidget {
  const CapturedBody({super.key, required this.message, required this.preview, this.loadPage,
    this.captureId, this.direction = 'response'})
    : _fullPage = false, _initialSession = null, _initialOffsets = const [], _initialView = null, _initialFormatted = true;
  const CapturedBody._viewer({required this.message, required this.preview, this.loadPage, this.captureId,
    required this.direction, required int session, required List<int> previousOffsets, required _BodyView? view,
    required bool formatted})
    : _fullPage = true, _initialSession = session, _initialOffsets = previousOffsets, _initialView = view, _initialFormatted = formatted;
  final Json message, preview;
  final String? captureId;
  final String direction;
  // Retained for single-page widget fixtures. Production readers belong to
  // this State, so an independent viewer cannot share the card's reader.
  final Future<Json> Function(int offset, String source)? loadPage;
  final bool _fullPage, _initialFormatted;
  final int? _initialSession;
  final List<int> _initialOffsets;
  final _BodyView? _initialView;
  @override State<CapturedBody> createState() => _CapturedBodyState();
}

class _CapturedBodyState extends State<CapturedBody> with WidgetsBindingObserver {
  final _scroll = ScrollController();
  final _horizontal = ScrollController();
  final _transform = TransformationController();
  final _previousOffsets = <int>[];
  final _derivedOffsets = <int>[];
  bool _formatted = true, _loading = false, _wrap = true;
  bool _visible = false, _pageActive = false, _workQueued = false, _refreshQueued = false;
  bool _detailLoading = false, _detailQueued = false;
  int _generation = 0, _visibilityGeneration = 0;
  int _hexColumns = 16;
  int? _session;
  BoxController? _box;
  Timer? _pollTimer;
  String? _readerId;
  Json _page = {}, _message = {};
  Json? _derivedPage;
  String? _imagePath, _nativeViewFormat, _pendingDerived, _failedDerivedFormat, _resumeDerivedFormat, _explicitFormat;
  int _resumeDerivedOffset = 0;
  late CapturedBodyContent _content;
  _BodyView? _view;
  String _text = '';
  String? _pretty, _error, _pollError;
  String _contentType = '', _grpcEncoding = '';
  bool? _bodyComplete, _bodyTruncated;
  bool _pageCurrent = true;
  int get _offset => (_page['offset'] as num? ?? 0).toInt();
  bool get _canLoad => widget.loadPage != null || widget.captureId != null;
  bool get _hasDerived => _imagePath != null || _derivedPage != null;
  bool get _usingDerived => _activeView == _BodyView.content && _hasDerived &&
    (_nativeViewFormat != 'json' || _formatted);
  bool get _showingImage => _activeView == _BodyView.content &&
    (_usingDerived ? _imagePath != null : _content.kind == CapturedBodyKind.image);
  _BodyView get _activeView {
    if (_view == _BodyView.hex || _view == _BodyView.base64) return _view!;
    final derived = _hasDerived && (_nativeViewFormat != 'json' || _formatted);
    return derived || _content.kind != CapturedBodyKind.binary ? _BodyView.content : _BodyView.hex;
  }

  bool get _nativeViewsAvailable => widget._fullPage && widget.captureId != null &&
    _message['bodyComplete'] == true && _message['bodyTruncated'] != true;

  String? get _nativeFormat {
    if (!_nativeViewsAvailable) return null;
    if (_explicitFormat != null) return _explicitFormat;
    final mime = _contentType.split(';').first.trim().toLowerCase();
    if (mime.startsWith('image/') && !mime.contains('svg')) return 'image';
    if (mime.startsWith('application/grpc')) return 'grpc';
    if (mime.contains('proto') || _content.format == CapturedBodyFormat.protobuf) return 'protobuf';
    if (mime.contains('json') || _pretty != null) return 'json';
    return null;
  }

  String? get _wantedDerived {
    if (_view == _BodyView.hex || _view == _BodyView.base64) return null;
    final format = _nativeFormat;
    if (format == 'json' && (!_formatted || _explicitFormat != 'json' && _content.complete && _pretty != null)) return null;
    return format;
  }

  @override void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _session = widget._initialSession;
    _previousOffsets.addAll(widget._initialOffsets);
    _view = widget._initialView;
    _formatted = widget._initialFormatted;
    _message = Map<String, dynamic>.of(widget.message);
    _update(widget.preview);
  }
  @override void didChangeDependencies() {
    super.didChangeDependencies();
    _box = context.box;
    _session ??= _box!.sessionRevision;
    _pageActive = PageActivityScope.isActiveOf(context) && TickerMode.of(context);
    _syncVisibility();
  }
  @override void didUpdateWidget(CapturedBody oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.captureId != widget.captureId || oldWidget.direction != widget.direction) {
      _visibilityGeneration++;
      _closeReader();
      _previousOffsets.clear();
      _derivedOffsets.clear();
      _resumeDerivedFormat = null;
      _resumeDerivedOffset = 0;
      _view = widget._initialView;
      _formatted = widget._initialFormatted;
      _explicitFormat = null;
      _failedDerivedFormat = null;
      _error = null;
      _pollError = null;
      _message = Map<String, dynamic>.of(widget.message);
      _update(widget.preview);
      _scheduleWork();
      if (_visible && widget._fullPage) _refreshDetail();
      return;
    }
    final latest = _messageChanged(oldWidget.message, widget.message) ? _freshMessage(widget.message) : _message;
    final before = _message;
    final sourceMatches = (_page['source'] ?? 'decoded') == (widget.preview['source'] ?? 'decoded');
    final snapshot = _snapshotMessage(widget.preview, widget.message);
    final replace = _offset == 0 && !_loading && sourceMatches && widget.preview.isNotEmpty &&
      !_messageBehind(snapshot, latest);
    _update(replace ? {...widget.preview, 'message': snapshot} : _page, message: latest);
    // Metadata is live on every page. Only the current last page needs to be
    // reread when a stream grows; its offset and previous-page history stay.
    if (_page['hasMore'] != true && (!_pageCurrent || !replace && _messageChanged(before, _message))) _queueCurrentRefresh();
    _scheduleWork();
  }
  static bool _messageChanged(Json before, Json after) =>
    ['capturedBytes', 'bodyBytes', 'bodyComplete', 'bodyTruncated'].any((field) => before[field] != after[field]) ||
    capturedHeader(rows(before['headers']), 'content-type') != capturedHeader(rows(after['headers']), 'content-type') ||
    capturedHeader(rows(before['headers']), 'grpc-encoding') != capturedHeader(rows(after['headers']), 'grpc-encoding');

  static Json _snapshotMessage(Json page, Json fallback) {
    final snapshot = json(page['message']);
    return snapshot.isEmpty ? fallback : snapshot;
  }

  static bool _messageBehind(Json candidate, Json current) {
    for (final field in ['capturedBytes', 'bodyBytes']) {
      final previous = current[field], next = candidate[field];
      if (previous is num && next is num && next < previous) return true;
    }
    return current['bodyComplete'] == true && candidate['bodyComplete'] == false;
  }

  Json _freshMessage(Json incoming) {
    // A slower detail or page must not replace a newer direction snapshot.
    // Truncation can clear when an in-flight disk write catches up with the
    // forwarded-byte counter, so only completion is a final flag.
    if (_messageBehind(incoming, _message)) return Map<String, dynamic>.of(_message);
    final latest = {..._message, ...incoming};
    if (_message['bodyComplete'] == true) latest['bodyComplete'] = true;
    return latest;
  }

  void _update(Json page, {Json? message}) {
    final keptPage = identical(page, _page);
    final snapshot = _snapshotMessage(page, message ?? _message);
    if (message != null) _message = _freshMessage(message);
    if (!keptPage) _message = _freshMessage(snapshot);
    final pageCurrent = !_messageBehind(snapshot, _message);
    final contentType = capturedHeader(rows(_message['headers']), 'content-type');
    final grpcEncoding = capturedHeader(rows(_message['headers']), 'grpc-encoding');
    final same = _page.isNotEmpty && contentType == _contentType && grpcEncoding == _grpcEncoding &&
      _message['bodyComplete'] == _bodyComplete && _message['bodyTruncated'] == _bodyTruncated && pageCurrent == _pageCurrent &&
      ['text', 'encoding', 'source', 'offset', 'nextOffset', 'hasMore', 'truncated', 'error', 'charset', 'displayText']
        .every((field) => page[field] == _page[field]);
    _page = {...page, 'message': Map<String, dynamic>.of(snapshot)};
    _pageCurrent = pageCurrent;
    if (same) return;
    _contentType = contentType;
    _grpcEncoding = grpcEncoding;
    _bodyComplete = _message['bodyComplete'] as bool?;
    _bodyTruncated = _message['bodyTruncated'] as bool?;
    _text = page['text']?.toString() ?? '';
    _content = readCapturedBody(_message, {...page, 'message': {
      ..._message, if (!pageCurrent) 'bodyComplete': false,
    }});
    _pretty = _content.kind == CapturedBodyKind.text && _content.format == CapturedBodyFormat.plain && _content.complete
      ? formatCapturedJson(_content.text, maxOutputLength: 65536) : null;
  }
  @override void dispose() {
    _visible = false;
    _visibilityGeneration++;
    _closeReader();
    WidgetsBinding.instance.removeObserver(this);
    _scroll.dispose();
    _horizontal.dispose();
    _transform.dispose();
    super.dispose();
  }

  @override void didChangeAppLifecycleState(AppLifecycleState state) {
    if (mounted && _syncVisibility()) setState(() {});
  }

  bool _syncVisibility() {
    final lifecycle = WidgetsBinding.instance.lifecycleState;
    final visible = _box != null && !_box!.remote && _box!.sessionRevision == _session && _pageActive &&
      (lifecycle == null || lifecycle == AppLifecycleState.resumed);
    if (visible == _visible) return false;
    _visible = visible;
    _visibilityGeneration++;
    if (!visible) {
      _closeReader();
    } else {
      if (widget._fullPage && widget.captureId != null) _refreshDetail();
      else if ((_offset > 0 || !_pageCurrent) && _page['hasMore'] != true) _queueCurrentRefresh();
      _scheduleWork();
    }
    return true;
  }

  String _reader() => _readerId ??= 'body-${DateTime.now().microsecondsSinceEpoch}-${++_readerSequence}';

  void _closeReader() {
    _generation++;
    _pollTimer?.cancel();
    _pollTimer = null;
    _loading = false;
    _refreshQueued = false;
    _pendingDerived = null;
    if (_nativeViewFormat != null) {
      _resumeDerivedFormat = _nativeViewFormat;
      _resumeDerivedOffset = (_derivedPage?['offset'] as num? ?? 0).toInt();
    }
    _nativeViewFormat = null;
    _derivedPage = null;
    _imagePath = null;
    final reader = _readerId, box = _box;
    _readerId = null;
    if (reader != null && box != null) unawaited(() async {
      try { await box.call('capture.body.close', {'readerId': reader}); }
      catch (_) { /* Closing a canceled or already-removed reader is harmless. */ }
    }());
  }

  bool _current() => mounted && _visible && _box != null && !_box!.remote && _box!.sessionRevision == _session;

  void _queueCurrentRefresh() {
    if (!_current() || !_canLoad) return;
    _refreshQueued = true;
    _scheduleWork();
  }

  void _scheduleWork() {
    if (_workQueued) return;
    _workQueued = true;
    scheduleMicrotask(() {
      _workQueued = false;
      if (!_current() || _loading) return;
      if (_refreshQueued) {
        _refreshQueued = false;
        unawaited(_load(_offset, move: _PageMove.refresh));
      } else {
        final format = _wantedDerived;
        if (format != null && !_hasDerived && _failedDerivedFormat != format) unawaited(_openDerived(format));
      }
    });
  }

  void _refreshDetail() {
    _pollTimer?.cancel();
    if (_detailLoading) { _detailQueued = true; return; }
    if (_current() && widget.captureId != null) unawaited(_pollDetail());
  }

  Future<void> _pollDetail() async {
    if (!_current() || _detailLoading || widget.captureId == null) return;
    _detailLoading = true;
    final epoch = _visibilityGeneration, box = _box!;
    try {
      final detail = json(await box.call('capture.detail', {'id': widget.captureId}));
      if (!_current() || epoch != _visibilityGeneration) return;
      final incoming = json(detail[widget.direction]);
      final message = _freshMessage(incoming);
      if (incoming.isNotEmpty) {
        final preview = json(detail['${widget.direction}Preview']);
        final before = _message;
        final sourceMatches = (_page['source'] ?? 'decoded') == (preview['source'] ?? 'decoded');
        final snapshot = _snapshotMessage(preview, incoming);
        final replace = _offset == 0 && !_loading && preview.isNotEmpty && sourceMatches &&
          !_messageBehind(snapshot, message);
        setState(() {
          _update(replace ? {...preview, 'message': snapshot} : _page, message: message);
          _pollError = null;
        });
        if (_page['hasMore'] != true && (!_pageCurrent || !replace && _messageChanged(before, _message))) _queueCurrentRefresh();
        _scheduleWork();
      }
    } catch (failure) {
      if (_current() && epoch == _visibilityGeneration) setState(() => _pollError = BoxController.describe(failure));
    } finally {
      _detailLoading = false;
      if (_current()) {
        if (_detailQueued) { _detailQueued = false; _refreshDetail(); }
        else if (_message['bodyComplete'] != true) _pollTimer = Timer(const Duration(seconds: 2), _pollDetail);
      }
    }
  }
  String get _shownText => _activeView == _BodyView.base64 ?
    (_page['encoding'] == 'base64' ? _text : base64Encode(_content.bytes)) : _usingDerived && _derivedPage != null
      ? _derivedText(_derivedPage!) : _formatted ? _pretty ?? _content.text : _content.text;

  static String _derivedText(Json page) {
    final offset = page['offset'], next = page['nextOffset'];
    if (offset is! num || offset < 0 || next is! num || next < offset || page['hasMore'] == true && next <= offset) {
      throw const FormatException('Invalid body view page offsets');
    }
    if (page['displayText'] is String) return page['displayText'] as String;
    final text = page['text'];
    if (text is! String) throw const FormatException('Body view text is unavailable');
    if (page['encoding'] == 'base64') return utf8.decode(base64Decode(text));
    if (page['encoding'] != 'text') throw const FormatException('Unsupported body view page encoding');
    return text;
  }

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
    setState(() {
      if (_pendingDerived != null && value != _BodyView.content) _closeReader();
      _view = value;
      if (value == _BodyView.content) _failedDerivedFormat = null;
    });
    _resetScroll();
    _scheduleWork();
  }

  void _open() {
    if (!_current() || widget._fullPage) return;
    // Preserve the original page/history, then give the viewer its own reader
    // and live metadata lifecycle.
    openPage(context, CapturedBody._viewer(
      message: Map<String, dynamic>.unmodifiable(_message), preview: Map<String, dynamic>.unmodifiable(_page),
      loadPage: widget.loadPage, captureId: widget.captureId, direction: widget.direction,
      session: _session!, previousOffsets: List<int>.unmodifiable(_previousOffsets), view: _view, formatted: _formatted,
    ));
  }

  Future<void> _copyDisplayed() => _copy(!_usingDerived && _content.error != null ? _text : _activeView == _BodyView.hex ? [
    for (var offset = 0; offset < _content.bytes.length; offset += _hexColumns)
      capturedHexLine(_content.bytes, offset, _hexColumns, baseOffset: _offset),
  ].join('\n') : _shownText);

  List<IconButtonData> _actions({required bool expanded}) => [
    if (_activeView == _BodyView.content && (_pretty != null || _nativeFormat == 'json')) IconButtonData(glyph: AppGlyphs.code,
      tooltip: context.tr(_formatted ? 'Show original' : 'Format JSON'), isSelected: _formatted,
      onPressed: _loading ? null : () { setState(() => _formatted = !_formatted); _resetScroll(); _scheduleWork(); }),
    if (expanded && (_activeView == _BodyView.base64 || _activeView == _BodyView.content && (_derivedPage != null || _content.kind == CapturedBodyKind.text)))
      IconButtonData(glyph: AppGlyphs.wrap, tooltip: context.tr('Wrap lines'), isSelected: _wrap,
        onPressed: () { setState(() => _wrap = !_wrap); _resetScroll(); }),
    if (expanded && _showingImage)
      IconButtonData(glyph: AppGlyphs.reset, tooltip: context.tr('Reset zoom'),
        onPressed: () => _transform.value = Matrix4.identity()),
    if (!expanded) IconButtonData(glyph: AppGlyphs.openExternal, tooltip: context.tr('View body'),
      onPressed: _text.isEmpty && (_message['capturedBytes'] as num? ?? 0) == 0 ? null : _open),
    IconButtonData(glyph: AppGlyphs.copy, tooltip: context.tr('Copy current body page'), onPressed: _text.isEmpty ? null : () => _copy()),
  ];

  String get _contentLabel => switch (_usingDerived ? _nativeViewFormat : _nativeFormat) {
    'image' => 'Image', 'protobuf' => 'Protobuf', 'grpc' => 'gRPC', 'json' when _usingDerived || _content.kind == CapturedBodyKind.binary => 'JSON',
    _ => _content.kind == CapturedBodyKind.image ? 'Image' : switch (_content.format) {
    CapturedBodyFormat.protobuf => 'Protobuf',
    CapturedBodyFormat.grpc => 'gRPC',
    CapturedBodyFormat.plain => 'Text',
  }};

  Widget _body(double maxHeight) {
    if (_showingImage) {
      return _BodyImage(bytes: _usingDerived ? null : _content.bytes, path: _usingDerived ? _imagePath : null,
        height: math.min(maxHeight, widget._fullPage ? maxHeight : 240.0),
        transform: widget._fullPage ? _transform : null, onOpen: widget._fullPage ? null : _open,
        onHex: () => _select(_BodyView.hex));
    }
    if (_text.isEmpty && !_usingDerived) return Text(context.tr('No body captured'), style: context.listSubtitleStyle);
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
            itemBuilder: (context, index) => Directionality(textDirection: TextDirection.ltr, child: SelectableText(
              capturedHexLine(_content.bytes, index * _hexColumns, _hexColumns, baseOffset: _offset),
              key: ValueKey('captured-body-hex-line-${_offset + index * _hexColumns}'),
              style: style, minLines: 1, maxLines: 1,
            ))),
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
      Widget child = SizedBox(width: width, child: Directionality(textDirection: TextDirection.ltr, child: SelectableText(text,
        key: const ValueKey('captured-body-text'), style: style)));
      if (widget._fullPage && !_wrap) child = Scrollbar(controller: _horizontal,
        child: SingleChildScrollView(controller: _horizontal, primary: false, scrollDirection: Axis.horizontal, child: child));
      return ConstrainedBox(constraints: BoxConstraints(maxHeight: maxHeight), child: Scrollbar(controller: _scroll,
        child: SingleChildScrollView(controller: _scroll, primary: false, child: child)));
    });
  }

  Future<void> _load(int offset, {required _PageMove move, bool derived = false}) async {
    final box = _box!, loader = widget.loadPage;
    if (_loading || !_current() || !derived && !_canLoad || derived && _readerId == null) return;
    final generation = ++_generation;
    final oldOffset = derived ? (_derivedPage?['offset'] as num? ?? 0).toInt() : _offset;
    setState(() { _loading = true; _error = null; });
    try {
      final source = _page['source'] as String? ?? 'decoded';
      final page = derived ? json(await box.call('capture.body.view.page', {'readerId': _readerId, 'offset': offset})) :
        loader != null ? await loader(offset, source) : json(await box.call('capture.body', {
          'readerId': _reader(), 'id': widget.captureId, 'direction': widget.direction, 'offset': offset, 'source': source,
        }));
      if (!_current() || generation != _generation) return;
      if (derived) _derivedText(page);
      setState(() {
        final history = derived ? _derivedOffsets : _previousOffsets;
        if (move == _PageMove.previous && history.isNotEmpty) history.removeLast();
        else if (move == _PageMove.next) history.add(oldOffset);
        if (derived) {
          _derivedPage = page;
          _resumeDerivedOffset = (page['offset'] as num? ?? 0).toInt();
        } else {
          _update(page);
          // A detail can advance while this page is being read. Reread a
          // stale tail once the reply arrives, keeping its offset/history.
          _refreshQueued = !_pageCurrent && _page['hasMore'] != true;
        }
      });
      if (move != _PageMove.refresh) _resetScroll();
    } catch (failure) {
      if (_current() && generation == _generation) {
        setState(() => _error = BoxController.describe(failure));
      }
    } finally {
      if (mounted && generation == _generation) { setState(() => _loading = false); _scheduleWork(); }
    }
  }

  Future<void> _openDerived(String format) async {
    if (!_current() || _loading || widget.captureId == null) return;
    final generation = ++_generation, box = _box!;
    setState(() { _loading = true; _error = null; _pendingDerived = format; _failedDerivedFormat = null; });
    try {
      final reader = _reader();
      final view = json(await box.call('capture.body.view', {
        'readerId': reader, 'id': widget.captureId, 'direction': widget.direction, 'format': format,
      }));
      if (!_current() || generation != _generation) return;
      if (view['format'] != format) throw const FormatException('Unexpected body view format');
      Json? page;
      String? path;
      if (format == 'image') {
        path = view['path'] as String?;
        if (path == null || path.isEmpty) throw const FormatException('Image view file is unavailable');
      } else {
        page = json(view['page']);
        if (page.isEmpty) throw const FormatException('Body view page is unavailable');
        final offset = _resumeDerivedFormat == format ? _resumeDerivedOffset : 0;
        if (offset > 0) page = json(await box.call('capture.body.view.page', {'readerId': reader, 'offset': offset}));
        if (!_current() || generation != _generation) return;
        _derivedText(page);
      }
      setState(() {
        if (_resumeDerivedFormat != format) _derivedOffsets.clear();
        _resumeDerivedFormat = format;
        _resumeDerivedOffset = (page?['offset'] as num? ?? 0).toInt();
        _nativeViewFormat = format;
        _derivedPage = page;
        _imagePath = path;
        final message = json(view['message']);
        if (message.isNotEmpty) _update(_page, message: message);
      });
      _resetScroll();
    } catch (failure) {
      if (_current() && generation == _generation) setState(() {
        _error = BoxController.describe(failure);
        _failedDerivedFormat = format;
      });
    } finally {
      if (mounted && generation == _generation) {
        setState(() { _loading = false; _pendingDerived = null; });
        _scheduleWork();
      }
    }
  }

  void _chooseDerived(String format) {
    if (!_current() || _loading || !_nativeViewsAvailable) return;
    setState(() {
      _explicitFormat = format;
      _view = _BodyView.content;
      _formatted = true;
      _failedDerivedFormat = null;
      if (_nativeViewFormat != format) {
        _nativeViewFormat = null;
        _derivedPage = null;
        _imagePath = null;
        _resumeDerivedFormat = null;
        _resumeDerivedOffset = 0;
        _derivedOffsets.clear();
      }
    });
    if (!_hasDerived) unawaited(_openDerived(format));
    _resetScroll();
  }

  @override Widget build(BuildContext context) {
    final box = context.watchBox(BoxAspect.connections);
    if (box.remote || box.sessionRevision != _session) return widget._fullPage
      ? BoxPage(title: 'Body', children: const [EmptyState('Session changed; reopen this activity from the current list')])
      : const SizedBox.shrink();
    final message = _message;
    final derived = _usingDerived && _derivedPage != null;
    final preview = derived ? _derivedPage! : _page;
    final history = derived ? _derivedOffsets : _previousOffsets;
    final paged = preview['hasMore'] == true || history.isNotEmpty;
    final card = Card(shape: AppShape.xl, padding: const EdgeInsets.all(16), child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, spacing: 12, children: [
        Text([
          if (_contentType.isNotEmpty) _contentType,
          if (_content.charset.isNotEmpty) _content.charset,
          '${bytes(message['capturedBytes'] as num? ?? 0)} / ${bytes(message['bodyBytes'] as num? ?? 0)}',
          if (_activeView == _BodyView.base64) 'Base64',
          if (_usingDerived) context.tr('Derived view'),
          if (_activeView == _BodyView.content && _formatted && _pretty != null && !_usingDerived) 'JSON',
          if (_activeView == _BodyView.content && (_usingDerived || _content.format != CapturedBodyFormat.plain)) _contentLabel,
        ].join(' · '), style: context.listCaptionStyle),
        if ((_text.isNotEmpty || _hasDerived) && (_content.error == null || _usingDerived)) SegmentedControl<_BodyView>(
          segments: [
            if (_content.kind != CapturedBodyKind.binary || _nativeFormat != null) (_BodyView.content, context.tr(_contentLabel)),
            (_BodyView.hex, context.tr('Hex')), (_BodyView.base64, 'Base64'),
          ], value: _activeView, onChanged: _select,
        ),
        if (_usingDerived)
          Text(context.tr('The original body page is kept for copying and byte views.'), style: context.listCaptionStyle),
        if (_activeView == _BodyView.content && (_usingDerived
            ? _nativeViewFormat == 'protobuf' || _nativeViewFormat == 'grpc'
            : _content.format != CapturedBodyFormat.plain))
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
        if (_content.error == null || _usingDerived) _body(MediaQuery.sizeOf(context).height * (widget._fullPage ? .7 : .5))
        else SelectableText(_text, key: const ValueKey('captured-body-text'), style: context.listSubtitleStyle),
        if (_error != null) Text(_error!, style: context.listSubtitleStyle),
        if (_pendingDerived != null) Text(context.tr('Loading'), style: context.listCaptionStyle),
        if (_failedDerivedFormat != null) TextButton(onPressed: _loading ? null : () => _openDerived(_failedDerivedFormat!),
          child: Text(context.tr('Retry body view'))),
        if (_pollError != null) ...[
          Text(_pollError!, style: context.listSubtitleStyle),
          TextButton(onPressed: _refreshDetail, child: Text(context.tr('Refresh'))),
        ],
        if (paged) ...[
          Text('${context.tr(derived ? 'Derived body page' : 'Body page')}: ${history.length + 1}', style: context.listCaptionStyle),
          Wrap(spacing: 8, children: [
            if (history.isNotEmpty) TextButton(
              onPressed: _loading ? null : () => _load(history.last, move: _PageMove.previous, derived: derived),
              child: Text(context.tr('Previous page'))),
            if (preview['hasMore'] == true) TextButton(
              onPressed: _loading || !derived && !_canLoad ? null : () => _load((preview['nextOffset'] as num).toInt(), move: _PageMove.next, derived: derived),
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
          onPressed: _text.isEmpty && !_usingDerived || _showingImage ? null : _copyDisplayed),
        if (_nativeViewsAvailable) for (final format in ['json', 'protobuf', 'grpc', 'image']) PopupMenuItem(
          label: context.tr(switch (format) {
            'json' => 'View as JSON', 'protobuf' => 'View as Protobuf', 'grpc' => 'View as gRPC', _ => 'View as image',
          }), section: format == 'json' ? PopupMenuSection(title: context.tr('Display format')) : null,
          checked: _usingDerived && _nativeViewFormat == format,
          onPressed: _loading ? null : () => _chooseDerived(format)),
      ], body: Builder(builder: (context) {
        final inset = context.appBarInset, gap = context.contentTopPadding - inset;
        return Padding(padding: EdgeInsets.only(top: inset), child: ListView(
          padding: EdgeInsets.fromLTRB(16, math.max(0.0, gap), 16, 24 + context.bottomInset), children: [card],
        ));
      }));
  }
}

class _BodyImage extends StatefulWidget {
  const _BodyImage({this.bytes, this.path, required this.height, required this.onHex, this.transform, this.onOpen})
    : assert((bytes == null) != (path == null));
  final Uint8List? bytes;
  final String? path;
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
    if (!identical(oldWidget.bytes, widget.bytes) || oldWidget.path != widget.path) { _evict(); _provider = null; }
  }
  @override void dispose() { _evict(); super.dispose(); }
  @override Widget build(BuildContext context) => LayoutBuilder(builder: (context, constraints) {
    final ratio = MediaQuery.devicePixelRatioOf(context);
    final width = (constraints.maxWidth * ratio).ceil().clamp(1, 2048).toInt();
    final height = (widget.height * ratio).ceil().clamp(1, 2048).toInt();
    if (_provider == null || width != _width || height != _height) {
      _evict();
      _width = width; _height = height;
      final ImageProvider<Object> provider = widget.path != null ? FileImage(File(widget.path!)) : MemoryImage(widget.bytes!);
      _provider = ResizeImage(provider, width: width, height: height, policy: ResizeImagePolicy.fit);
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
