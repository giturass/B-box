import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';

import '../box.dart';
import '../common/body_content.dart';
import '../common/http_message.dart';

enum CapturedBodyView { content, hex, base64 }
enum _PageMove { next, previous, refresh }

/// Reads one page of a single-page fixture; production readers use the bridge.
typedef CapturedBodyPageLoader = Future<Json> Function(int offset, String source);

int _readerSequence = 0;

int _offsetOf(Json? page) => (page?['offset'] as num? ?? 0).toInt();

bool _messageChanged(Json before, Json after) =>
  ['capturedBytes', 'bodyBytes', 'bodyComplete', 'bodyTruncated'].any((field) => before[field] != after[field]) ||
  capturedHeader(rows(before['headers']), 'content-type') != capturedHeader(rows(after['headers']), 'content-type') ||
  capturedHeader(rows(before['headers']), 'grpc-encoding') != capturedHeader(rows(after['headers']), 'grpc-encoding');

const _pageFields = ['text', 'encoding', 'source', 'offset', 'nextOffset', 'hasMore', 'truncated', 'error', 'charset', 'displayText'];

/// A Dart-side failure keeps its untranslated message; a bridge failure is described as it arrived.
String _failureText(Object failure) => failure is FormatException ? failure.message : BoxController.describe(failure);

bool _healthyPage(Json page, CapturedBodyContent content) => content.error == null &&
  page['source'] != 'raw' && page['truncated'] != true && (page['error']?.toString() ?? '').isEmpty;

/// The direction snapshot a page was read with, or [fallback] for a page without one.
Json _snapshotMessage(Json page, Json fallback) {
  final snapshot = json(page['message']);
  return snapshot.isEmpty ? fallback : snapshot;
}

/// Whether [candidate] was taken before [current]: its counters are lower or it lost completion.
bool _messageBehind(Json candidate, Json current) {
  for (final field in ['capturedBytes', 'bodyBytes']) {
    final previous = current[field], next = candidate[field];
    if (previous is num && next is num && next < previous) return true;
  }
  return current['bodyComplete'] == true && candidate['bodyComplete'] == false;
}

/// Checks a derived view page and returns its text.
String _derivedText(Json page) {
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

/// Reads one direction of a captured exchange for [CapturedBody].
///
/// It keeps the current original byte page and, for a complete body, the
/// current page of its native image/JSON/Protobuf/gRPC view. Both belong to
/// one native reader that exists only while the body is visible in the session
/// it was opened in; [notifyListeners] reports every change of what is shown.
class CapturedBodyReader extends ChangeNotifier {
  CapturedBodyReader({required Json message, required Json preview, String? captureId, String direction = 'response',
    CapturedBodyPageLoader? loadPage, required this.onReplaced})
      : _detail = message, _preview = preview, _captureId = captureId, _direction = direction, _loadPage = loadPage {
    _message = Map<String, dynamic>.of(message);
    _update(preview);
  }

  /// Called when other content takes the place of the shown one, before it is laid out.
  final VoidCallback onReplaced;

  // The detail poll's latest direction snapshot and first page.
  Json _detail, _preview;
  String? _captureId;
  String _direction;
  CapturedBodyPageLoader? _loadPage;

  BoxController? _box;
  int? _session;
  bool _disposed = false, _visible = false;

  // The native reader and the single read that may use it.
  String? _readerId;
  int _generation = 0;
  bool _loading = false, _workQueued = false, _refreshQueued = false;
  String? _error;

  // The original byte page, with the newest direction metadata seen from any source.
  final _previousOffsets = <int>[];
  Json _page = {}, _message = {};
  late CapturedBodyContent _content;
  String _text = '';
  String? _pretty;
  String _contentType = '', _grpcEncoding = '';
  bool? _bodyComplete, _bodyTruncated;
  bool _pageCurrent = true;

  // The detail's first page as last classified beside a later shown page, and what it was read from.
  List<Object?>? _previewKey;
  CapturedBodyContent? _previewContent;
  // The text forms derived from the pages, kept while those pages stay the same.
  Json? _derivedTextPage;
  String _derivedTextValue = '';
  CapturedBodyContent? _base64Content;
  String _base64Value = '';

  // The complete-file format recognised from the first page, and the snapshot that named it.
  String? _automaticFormat;
  Json _formatMessage = {};

  // The native view of the complete body. A closed reader remembers where to resume.
  final _derivedOffsets = <int>[];
  Json? _derivedPage;
  String? _imagePath, _nativeViewFormat, _pendingDerived, _failedDerivedFormat, _resumeDerivedFormat;
  int _resumeDerivedOffset = 0;

  CapturedBodyView? _view;
  bool _formatted = true;

  /// The newest direction metadata.
  Json get message => _message;
  CapturedBodyContent get content => _content;
  String get contentType => _contentType;
  /// The last failure: an untranslated key from this reader, or the bridge's own text.
  String? get error => _error;
  /// The original page as the bridge returned it: text, or Base64 of its bytes.
  String get pageText => _text;
  int get offset => _offsetOf(_page);
  bool get loading => _loading;
  bool get formatted => _formatted;
  /// Whether results and actions still belong to a visible body of the session it was opened in.
  bool get current => !_disposed && _visible && _sessionCurrent;
  /// Whether [box] is still the local session this body was opened in.
  bool belongsTo(BoxController box) => !box.remote && box.sessionRevision == _session;
  bool get _sessionCurrent => _box != null && belongsTo(_box!);
  bool get _canLoad => _loadPage != null || _captureId != null;

  bool get hasDerived => _nativeViewsAvailable && _nativeViewFormat == _automaticFormat &&
    (_imagePath != null || _derivedPage != null);
  bool get usingDerived => view == CapturedBodyView.content && hasDerived &&
    (_nativeViewFormat != 'json' || _formatted);
  bool get _hasPageContent => _content.kind != CapturedBodyKind.binary &&
    (_automaticFormat != 'image' || _content.kind == CapturedBodyKind.image);
  bool get showingImage => view == CapturedBodyView.content &&
    (usingDerived ? _imagePath != null : _content.kind == CapturedBodyKind.image);
  /// The image file of the native view, when that is what is shown.
  String? get imagePath => usingDerived ? _imagePath : null;

  /// The view in use: the chosen byte view, or content whenever there is some to show.
  CapturedBodyView get view {
    if (_view == CapturedBodyView.hex || _view == CapturedBodyView.base64) return _view!;
    final derived = hasDerived && (_nativeViewFormat != 'json' || _formatted);
    return derived || _hasPageContent ? CapturedBodyView.content : CapturedBodyView.hex;
  }

  /// Whether a content view exists or can still be opened next to the byte views.
  bool get offersContent => _hasPageContent || hasDerived ||
    _nativeFormat != null && _nativeFormat != _failedDerivedFormat;

  bool get _nativeViewsAvailable => _captureId != null &&
    (_message['capturedBytes'] as num? ?? 0) > 0 &&
    _message['bodyComplete'] == true && _message['bodyTruncated'] != true;

  String? get _nativeFormat {
    if (!_nativeViewsAvailable) return null;
    // A fresh decoded first page can recover a formerly raw/error byte view.
    // Complete-file interpretation starts at the beginning; the byte page and
    // its offsets continue to belong to their original source.
    if (!_healthyPage(_page, _content) && _messageBehind(_formatMessage, _message)) return null;
    return _automaticFormat;
  }

  String? get _wantedDerived {
    if (_view == CapturedBodyView.hex || _view == CapturedBodyView.base64) return null;
    final format = _nativeFormat;
    if (format == 'json' && (!_formatted || _content.complete && _pretty != null)) return null;
    return format;
  }

  bool get canFormatJson => _pretty != null ||
    _nativeViewFormat == 'json' && _derivedPage != null ||
    _failedDerivedFormat != 'json' && (_resumeDerivedFormat == 'json' && _nativeFormat == 'json' ||
      !_formatted && _content.complete && _content.viewFormat == 'json');

  /// Whether the original page itself is shown as formatted JSON.
  bool get showsFormattedPage => view == CapturedBodyView.content && _formatted && _pretty != null && !usingDerived;

  bool get showsProtobufFields => usingDerived
    ? _nativeViewFormat == 'protobuf' || _nativeViewFormat == 'grpc' && !_content.mime.endsWith('+json')
    : _content.format == CapturedBodyFormat.protobuf ||
      _content.format == CapturedBodyFormat.grpc && !_content.mime.endsWith('+json');

  /// Whether a text page of the native view is shown instead of the original page.
  bool get showsDerivedPage => usingDerived && _derivedPage != null;
  bool get hasDerivedPage => _derivedPage != null;
  /// The page being shown; its offsets and history are separate for the two sources.
  Json get shownPage => showsDerivedPage ? _derivedPage! : _page;
  List<int> get _shownHistory => showsDerivedPage ? _derivedOffsets : _previousOffsets;
  int get pageNumber => _shownHistory.length + 1;
  bool get hasPreviousPage => _shownHistory.isNotEmpty;
  bool get hasNextPage => shownPage['hasMore'] == true;
  bool get canTurnBack => !_loading;
  bool get canTurnForward => !_loading && (showsDerivedPage || _canLoad);

  /// Whether the native view is being opened.
  bool get opening => _pendingDerived != null;
  /// Whether the native view failed and the same format can be tried again.
  bool get canRetryView => _failedDerivedFormat != null && _nativeFormat == _failedDerivedFormat;

  String get shownText => view == CapturedBodyView.base64 ?
    (_page['encoding'] == 'base64' ? _text : _pageBase64) : usingDerived && _derivedPage != null
      ? _shownDerivedText(_derivedPage!) : _formatted ? _pretty ?? _content.text : _content.text;

  String get _pageBase64 {
    if (!identical(_base64Content, _content)) {
      _base64Value = base64Encode(_content.bytes);
      _base64Content = _content;
    }
    return _base64Value;
  }

  String _shownDerivedText(Json page) {
    if (!identical(_derivedTextPage, page)) {
      _derivedTextValue = _derivedText(page);
      _derivedTextPage = page;
    }
    return _derivedTextValue;
  }

  /// Classifies the detail's first page only when it or the metadata it is read with changed.
  CapturedBodyContent _readPreview(Json preview, Json snapshot) {
    final merged = {..._message, ...snapshot}, headers = rows(merged['headers']);
    final key = [for (final field in _pageFields) preview[field], merged['bodyComplete'], merged['bodyTruncated'],
      capturedHeader(headers, 'content-type'), capturedHeader(headers, 'grpc-encoding')];
    if (_previewContent == null || !listEquals(key, _previewKey)) {
      _previewContent = readCapturedBody(_message, {...preview, 'message': snapshot});
      _previewKey = key;
    }
    return _previewContent!;
  }

  String get contentLabel => switch (usingDerived ? _nativeViewFormat :
      _nativeFormat != _failedDerivedFormat ? _nativeFormat : null) {
    'image' => 'Image', 'protobuf' => 'Protobuf', 'grpc' => 'gRPC', 'json' => 'JSON',
    _ => _content.kind == CapturedBodyKind.image ? 'Image' : switch (_content.format) {
    CapturedBodyFormat.protobuf => 'Protobuf',
    CapturedBodyFormat.grpc => 'gRPC',
    CapturedBodyFormat.plain => _pretty != null || _content.complete && _content.viewFormat == 'json' ? 'JSON' : 'Text',
  }};

  /// Binds the session this body was opened in.
  void attach(BoxController box) {
    _box = box;
    _session ??= box.sessionRevision;
  }

  /// Accepts the detail poll's live direction snapshot and first page.
  void update({required Json message, required Json preview, String? captureId, required String direction,
      CapturedBodyPageLoader? loadPage}) {
    final rebound = captureId != _captureId || direction != _direction;
    final previousDetail = _detail;
    _detail = message;
    _preview = preview;
    _captureId = captureId;
    _direction = direction;
    _loadPage = loadPage;
    if (rebound) {
      _closeReader();
      _previousOffsets.clear();
      _derivedOffsets.clear();
      _resumeDerivedFormat = null;
      _resumeDerivedOffset = 0;
      _view = null;
      _formatted = true;
      _automaticFormat = null;
      _formatMessage = {};
      _failedDerivedFormat = null;
      _error = null;
      _message = Map<String, dynamic>.of(message);
      _update(preview);
      _scheduleWork();
      return;
    }
    final latest = _messageChanged(previousDetail, message) ? _freshMessage(message) : _message;
    final before = _message;
    final sourceMatches = (_page['source'] ?? 'decoded') == (preview['source'] ?? 'decoded');
    final snapshot = _snapshotMessage(preview, message);
    final freshPreview = preview.isNotEmpty && !_messageBehind(snapshot, latest);
    final recovered = _decodedRecovery(_page, latest);
    // A fallback reader stays bound to raw bytes. Replace it before accepting
    // decoded offsets, and reject any raw read that is still in flight.
    if (recovered != null) _closeReader();
    final replace = offset == 0 && !_loading && (sourceMatches || recovered != null) && freshPreview;
    _update(replace ? recovered ?? {...preview, 'message': snapshot} : _page, message: latest);
    if (!replace && preview.isNotEmpty && !_messageBehind(snapshot, _message)) {
      _rememberFormat(preview, _readPreview(preview, snapshot));
    }
    // Metadata is live on every page. Only the current last page needs to be
    // reread when a stream grows; its offset and previous-page history stay.
    if (_page['hasMore'] != true && (!_pageCurrent || !replace && _messageChanged(before, _message))) _queueCurrentRefresh();
    _scheduleWork();
  }

  /// Applies whether the body's page is in front. Returns whether visibility
  /// changed: a hidden body releases its reader and a shown one resumes.
  bool setActive(bool active) {
    final visible = _sessionCurrent && active;
    if (visible == _visible) return false;
    _visible = visible;
    if (!visible) {
      _closeReader();
    } else {
      if ((offset > 0 || !_pageCurrent) && _page['hasMore'] != true) _queueCurrentRefresh();
      _scheduleWork();
    }
    return true;
  }

  void select(CapturedBodyView value) {
    if (!current) return;
    _change(() {
      if (_pendingDerived != null && value != CapturedBodyView.content) _closeReader();
      _view = value;
      if (value == CapturedBodyView.content) _failedDerivedFormat = null;
      if (value == CapturedBodyView.content && _content.kind == CapturedBodyKind.binary) _formatted = true;
    });
    onReplaced();
    _scheduleWork();
  }

  void toggleFormatted() {
    _change(() => _formatted = !_formatted);
    onReplaced();
    _scheduleWork();
  }

  Future<void> previousPage() async {
    if (hasPreviousPage) await _load(_shownHistory.last, move: _PageMove.previous, derived: showsDerivedPage);
  }

  Future<void> nextPage() async {
    if (hasNextPage) await _load((shownPage['nextOffset'] as num).toInt(), move: _PageMove.next, derived: showsDerivedPage);
  }

  Future<void> retryView() async {
    final format = _failedDerivedFormat;
    if (format != null) await _openDerived(format);
  }

  @override void dispose() {
    _disposed = true;
    _visible = false;
    _closeReader();
    super.dispose();
  }

  void _change(VoidCallback change) {
    change();
    notifyListeners();
  }

  Json? _decodedRecovery(Json page, Json message) {
    final preview = _preview;
    if (page['source'] != 'raw' || _offsetOf(page) != 0 || preview.isEmpty || _offsetOf(preview) != 0) return null;
    final snapshot = _snapshotMessage(preview, _detail);
    if (_messageBehind(snapshot, message)) return null;
    final decoded = {...preview, 'message': snapshot};
    return _healthyPage(decoded, readCapturedBody(message, decoded)) ? decoded : null;
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
      _pageFields.every((field) => page[field] == _page[field]);
    _page = {...page, 'message': Map<String, dynamic>.of(snapshot)};
    _pageCurrent = pageCurrent;
    if (same) {
      if (pageCurrent) _rememberFormat(page, _content);
      return;
    }
    _contentType = contentType;
    _grpcEncoding = grpcEncoding;
    _bodyComplete = _message['bodyComplete'] as bool?;
    _bodyTruncated = _message['bodyTruncated'] as bool?;
    _text = page['text']?.toString() ?? '';
    _content = readCapturedBody(_message, {...page, 'message': {
      ..._message, if (!pageCurrent) 'bodyComplete': false,
    }});
    _pretty = _content.kind == CapturedBodyKind.text && _content.format == CapturedBodyFormat.plain &&
      _content.complete && _content.viewFormat == 'json'
      ? formatCapturedJson(_content.text, maxOutputLength: 65536) : null;
    if (pageCurrent) _rememberFormat(page, _content);
  }

  void _rememberFormat(Json page, CapturedBodyContent content) {
    // A later byte page can begin with arbitrary text or part of a field.
    // Only the first page identifies the direction's complete-file view.
    if (_offsetOf(page) != 0) return;
    final snapshot = _snapshotMessage(page, _message);
    if (_messageBehind(snapshot, _formatMessage)) return;
    // Reading an already-kept raw page cannot erase a decoded hint from the
    // same or a newer snapshot supplied by the detail page.
    if (page['source'] == 'raw' && _automaticFormat != null && !_messageBehind(_formatMessage, snapshot)) return;
    _formatMessage = Map<String, dynamic>.of(snapshot);
    if (content.viewFormat == _automaticFormat) return;
    if (_pendingDerived != null || _nativeViewFormat != null) _closeReader();
    _automaticFormat = content.viewFormat;
    _resumeDerivedFormat = null;
    _resumeDerivedOffset = 0;
    _derivedOffsets.clear();
    if (_failedDerivedFormat != null) _error = null;
    _failedDerivedFormat = null;
    _scheduleWork();
  }

  String _reader() => _readerId ??= 'body-${DateTime.now().microsecondsSinceEpoch}-${++_readerSequence}';

  /// Cancels work in flight and releases the native reader with its temporary view file.
  void _closeReader() {
    _generation++;
    _loading = false;
    _refreshQueued = false;
    _pendingDerived = null;
    if (_nativeViewFormat != null) {
      _resumeDerivedFormat = _nativeViewFormat;
      _resumeDerivedOffset = _offsetOf(_derivedPage);
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

  void _queueCurrentRefresh() {
    if (!current || !_canLoad) return;
    _refreshQueued = true;
    _scheduleWork();
  }

  /// Runs at most one read after the current changes: a queued tail refresh
  /// first, otherwise the native view the present state asks for.
  void _scheduleWork() {
    if (_workQueued) return;
    _workQueued = true;
    scheduleMicrotask(() {
      _workQueued = false;
      if (!current || _loading) return;
      if (_refreshQueued) {
        _refreshQueued = false;
        unawaited(_load(offset, move: _PageMove.refresh));
      } else {
        final format = _wantedDerived;
        if (format != null && !hasDerived && _failedDerivedFormat != format) unawaited(_openDerived(format));
      }
    });
  }

  Future<void> _load(int target, {required _PageMove move, bool derived = false}) async {
    final box = _box!, loader = _loadPage;
    if (_loading || !current || !derived && !_canLoad || derived && _readerId == null) return;
    var generation = ++_generation;
    final oldOffset = derived ? _offsetOf(_derivedPage) : offset;
    _change(() { _loading = true; _error = null; });
    try {
      final source = _page['source'] as String? ?? 'decoded';
      var page = derived ? json(await box.call('capture.body.view.page', {'readerId': _readerId, 'offset': target})) :
        loader != null ? await loader(target, source) : json(await box.call('capture.body', {
          'readerId': _reader(), 'id': _captureId, 'direction': _direction, 'offset': target, 'source': source,
        }));
      if (!current || generation != _generation) return;
      if (derived) _derivedText(page);
      if (!derived) {
        final recovered = _decodedRecovery(page, _freshMessage(_snapshotMessage(page, _message)));
        if (recovered != null) {
          _closeReader();
          generation = _generation;
          page = recovered;
        }
      }
      _change(() {
        final history = derived ? _derivedOffsets : _previousOffsets;
        if (move == _PageMove.previous && history.isNotEmpty) {
          history.removeLast();
        } else if (move == _PageMove.next) {
          history.add(oldOffset);
        }
        if (derived) {
          _derivedPage = page;
          _resumeDerivedOffset = _offsetOf(page);
        } else {
          _update(page);
          // A detail can advance while this page is being read. Reread a
          // stale tail once the reply arrives, keeping its offset/history.
          _refreshQueued = !_pageCurrent && _page['hasMore'] != true;
        }
      });
      if (move != _PageMove.refresh) onReplaced();
    } catch (failure) {
      if (current && generation == _generation) _change(() => _error = _failureText(failure));
    } finally {
      if (!_disposed && generation == _generation) {
        _change(() => _loading = false);
        _scheduleWork();
      }
    }
  }

  Future<void> _openDerived(String format) async {
    if (!current || _loading || _nativeFormat != format) return;
    final generation = ++_generation, box = _box!;
    _change(() { _loading = true; _error = null; _pendingDerived = format; _failedDerivedFormat = null; });
    try {
      final reader = _reader();
      final view = json(await box.call('capture.body.view', {
        'readerId': reader, 'id': _captureId, 'direction': _direction, 'format': format,
      }));
      if (!current || generation != _generation || _nativeFormat != format) return;
      if (view['format'] != format) throw const FormatException('Unexpected body view format');
      Json? page;
      String? path;
      if (format == 'image') {
        path = view['path'] as String?;
        if (path == null || path.isEmpty) throw const FormatException('Image view file is unavailable');
      } else {
        page = json(view['page']);
        if (page.isEmpty) throw const FormatException('Body view page is unavailable');
        final resumed = _resumeDerivedFormat == format ? _resumeDerivedOffset : 0;
        if (resumed > 0) page = json(await box.call('capture.body.view.page', {'readerId': reader, 'offset': resumed}));
        if (!current || generation != _generation || _nativeFormat != format) return;
        _derivedText(page);
      }
      _change(() {
        if (_resumeDerivedFormat != format) _derivedOffsets.clear();
        _resumeDerivedFormat = format;
        _resumeDerivedOffset = _offsetOf(page);
        _nativeViewFormat = format;
        _derivedPage = page;
        _imagePath = path;
        final message = json(view['message']);
        if (message.isNotEmpty) _update(_page, message: message);
      });
      onReplaced();
    } catch (failure) {
      if (current && generation == _generation) {
        _change(() {
          _error = _failureText(failure);
          _failedDerivedFormat = format;
        });
      }
    } finally {
      if (!_disposed && generation == _generation) {
        _change(() { _loading = false; _pendingDerived = null; });
        _scheduleWork();
      }
    }
  }
}
