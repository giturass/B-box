import 'package:flutter/services.dart';
import '../widgets.dart';
import '../common/http_message.dart';
import '../common/network_activity.dart';
import '../common/capture_page.dart';
import '../components/capture_poll.dart';
import '../components/captured_body.dart';
import '../components/connection_record.dart';
import '../components/data_subscription.dart';
import '../components/record.dart';
import 'request_settings.dart';

const _pageSize = 50;

class RequestsPage extends StatefulWidget {
  const RequestsPage({super.key});
  @override State<RequestsPage> createState() => _RequestsPageState();
}

/// What one build of the activity list shows.
class _Activity {
  const _Activity({required this.box, required this.session, required this.snapshot, required this.page,
    required this.capturing, required this.error, required this.refresh, required this.query, required this.filter,
    required this.entries, required this.hasMore});
  final BoxController box;
  final int session;
  /// The latest capture reply. A remote session has none, and a local one has none before the first reply.
  final Json? snapshot;
  final Json page;
  final bool capturing;
  final Object? error;
  final VoidCallback? refresh;
  final String query, filter;
  final List<ActivityEntry> entries;
  final bool hasMore;

  bool get loaded => snapshot != null;
}

class _RequestsPageState extends State<RequestsPage> {
  static const _captureHint = 'Configure MITM domains and trust the CA to record HTTPS or TLS/TCP. '
      'Recording starts with new requests or streams. '
      'Reconnect the target app if its connection was not already intercepted. '
      'Stopping recording keeps existing connections running.';
  static const _activityHint = 'Recent connections appear while the service is running. '
      'Capture saves HTTP messages and decrypted TLS streams separately.';

  final _list = ActivityList();
  late final _keywords = RecordKeywords()..addListener(_restart);
  String _query = '', _filter = 'All', _sort = 'Created';
  int _limit = _pageSize, _revision = 0;
  bool _busy = false;

  // A different search, filter or order starts again from the first page.
  void _restart([VoidCallback? change]) => setState(() { change?.call(); _limit = _pageSize; });
  @override void dispose() { _keywords.dispose(); super.dispose(); }

  Map<String, String> _parents(BoxController box, String query) =>
      _list.parents(box.connections, includeText: query.trim().isNotEmpty);

  Future<void> _act(String method, [Json arguments = const {}]) async {
    if (_busy || context.box.remote) return;
    setState(() => _busy = true);
    await context.box.act(method, arguments);
    if (mounted) setState(() { _busy = false; _revision++; });
  }

  Future<void> _closeAll() async {
    final box = context.box;
    final session = box.sessionRevision;
    if (await confirm(context, 'Close all active connections?', action: 'Close') &&
        mounted && session == box.sessionRevision && box.running) await box.act('connections.closeAll');
  }

  Future<void> _settings() async {
    final box = context.box, session = context.box.sessionRevision;
    setState(() => _busy = true);
    try {
      final source = json(await box.call('capture.settings'));
      if (mounted && !box.remote && session == box.sessionRevision) {
        await openPage(context, RequestSettingsPage(initial: json(source['config']), source: source));
      }
    } catch (error) { if (mounted) box.messages.add(BoxController.describe(error)); }
    finally { if (mounted) setState(() { _busy = false; _revision++; }); }
  }

  Future<void> _exportHar(_Activity view) => _act('capture.export', {'format': 'har', 'selection': {
    'query': view.query, 'filter': view.filter, 'sort': _sort, 'parents': _parents(view.box, view.query),
  }});

  Future<void> _clearCaptures(_Activity view) async {
    if (await confirm(context, 'Clear captured requests?', action: 'Clear') &&
        mounted && view.session == view.box.sessionRevision) await _act('capture.clear');
  }

  @override Widget build(BuildContext context) {
    final box = context.watchBox(BoxAspect.connections);
    final query = _keywords.queryWith(_query);
    return BoxDataSubscription(data: BoxData.connections, child: box.remote
      ? _buildList(context, null, null, null)
      : CapturePoll(method: 'capture.query', load: loadCapturePage, identity: (query, _filter, _sort),
          arguments: {'query': query, 'filter': _filter, 'sort': _sort, 'limit': _limit, 'parents': _parents(box, query)},
          revision: _revision, builder: _buildList));
  }

  Widget _buildList(BuildContext context, Json? snapshot, Object? error, VoidCallback? refresh) {
    final box = context.watchBox(BoxAspect.connections);
    final page = json(snapshot?['page']);
    final query = _keywords.queryWith(_query);
    // A remote session has no captures to filter by.
    final filter = box.remote && _filter == 'Captured' ? 'All' : _filter;
    // The backend selects a bounded capture window and reports all matching
    // parent IDs; merging its first N captures yields the first N activity rows.
    final matches = _list.select(box.connections, box.remote ? const [] : page['items'] as List<Json>? ?? const [],
      query: query, filter: filter, sort: _sort,
      represented: (page['represented'] as List? ?? const []).cast<String>().toSet());
    final view = _Activity(box: box, session: box.sessionRevision, snapshot: snapshot, page: page,
      capturing: json(snapshot?['state'])['active'] == true, error: error, refresh: refresh, query: query, filter: filter,
      entries: matches.take(_limit).toList(), hasMore: matches.length > _limit || page['hasMore'] == true);
    return Scaffold(
      title: context.tr('Connections and requests'), isLoading: _busy, keywords: _keywords,
      searchState: AppBarSearchState(onSearch: (query) => _restart(() => _query = query)),
      iconActions: [
        if (refresh != null) IconButtonData(glyph: AppGlyphs.refresh, tooltip: context.tr('Refresh'), onPressed: refresh),
      ],
      menuItems: _menuItems(context, view),
      body: Builder(builder: (context) {
        final inset = context.appBarInset, gap = context.contentTopPadding - inset;
        return Padding(padding: EdgeInsets.only(top: inset), child: ListView.builder(
          padding: EdgeInsets.only(top: gap > 0 ? gap : 0, bottom: 24 + context.bottomInset),
          // The header and the footer surround the entries.
          itemCount: view.entries.length + 2,
          itemBuilder: (context, index) {
            if (index == 0) return _header(context, view);
            if (index <= view.entries.length) return _row(context, view, view.entries[index - 1]);
            if (!view.hasMore) return const SizedBox.shrink();
            return TextButton(onPressed: () => setState(() => _limit += _pageSize), child: Text(context.tr('Show more')));
          },
        ));
      }),
    );
  }

  List<PopupMenuItem> _menuItems(BuildContext context, _Activity view) => [
    PopupMenuItem(glyph: AppGlyphs.sort, label: context.tr('Sort'), subtitle: context.tr(_sort), subItems: [
      for (final sort in ['Created', 'Speed', 'Upload', 'Download'])
        PopupMenuItem(label: context.tr(sort), checked: sort == _sort, onPressed: () => _restart(() => _sort = sort)),
    ]),
    PopupMenuItem(glyph: AppGlyphs.clearAll, label: context.tr('Close all connections'),
      onPressed: view.box.running ? _closeAll : null),
    if (!view.box.remote) ...[
      PopupMenuItem(glyph: AppGlyphs.sliders, label: context.tr('Capture settings'),
        onPressed: _busy || view.capturing || !view.loaded ? null : _settings),
      PopupMenuItem(glyph: AppGlyphs.arrowDown, label: context.tr('Export HAR'),
        onPressed: _busy || (view.page['harCount'] as num? ?? 0) == 0 ? null : () => _exportHar(view)),
      PopupMenuItem(glyph: AppGlyphs.delete, label: context.tr('Clear captured requests'),
        onPressed: _busy || !view.loaded ? null : () => _clearCaptures(view)),
    ],
  ];

  Widget _header(BuildContext context, _Activity view) {
    final remote = view.box.remote;
    return Padding(padding: const EdgeInsets.symmetric(horizontal: 16), child: Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(padding: const EdgeInsets.only(bottom: 12), child: Wrap(spacing: 8, runSpacing: 8, children: [
          for (final option in ['All', 'Active', if (!remote) 'Captured', 'Finished'])
            _filterChip(context, option, selected: option == view.filter),
        ])),
        if (remote) Padding(padding: const EdgeInsets.only(bottom: 12),
          child: Text(context.tr('HTTP debugging is only available for the local service'), style: context.listSubtitleStyle))
        else ..._captureStatus(context, view),
        if ((remote || view.loaded) && view.entries.isEmpty) _emptyState(view),
      ],
    ));
  }

  Widget _filterChip(BuildContext context, String option, {required bool selected}) {
    final colors = context.colorScheme;
    return Semantics(selected: selected, button: true, child: PressSurface(
      key: ValueKey('activity-filter-$option'), shape: AppShape.full,
      fill: selected ? colors.primary : context.raisedSurfaceLevel.colorOf(colors),
      onPressed: () => _restart(() => _filter = option),
      child: Padding(padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
        child: Text(context.tr(option), style: context.listSubtitleStyle.copyWith(
          color: selected ? colors.onPrimary : colors.onSurface))),
    ));
  }

  List<Widget> _captureStatus(BuildContext context, _Activity view) {
    final box = view.box, page = view.page, capturing = view.capturing, error = view.error;
    final pageError = page['error'] as String? ?? '';
    final canToggle = !_busy && view.loaded && box.running && !box.transitioning;
    return [
      generateGroup([
        ListItem.toggle(key: const ValueKey('capture-toggle'), title: Text(context.tr('Capture requests')),
          subtitle: Text(context.tr(capturing ? 'Capturing new requests and streams' : 'Capture stopped')), value: capturing,
          onChanged: canToggle ? (_) => _act(capturing ? 'capture.stop' : 'capture.start') : null),
      ]),
      if (!box.running) Padding(padding: const EdgeInsets.all(12),
        child: Text(context.tr('Start the local service before capturing requests'))),
      if (view.loaded) Padding(padding: const EdgeInsets.all(12), child: Text(
        '${page['capturedTotal'] ?? 0} ${context.tr('Captured')} · ${bytes(page['storedBytes'] as num? ?? 0)}',
        style: context.listSubtitleStyle)),
      if (pageError.isNotEmpty) Text(pageError, style: TextStyle(color: context.colorScheme.error)),
      if (error != null) ErrorState(error, retry: view.refresh),
      if (!view.loaded && error == null) const Center(child: ActivityIndicator()),
    ];
  }

  Widget _emptyState(_Activity view) {
    if (view.query.isNotEmpty) return const EmptyState('No results');
    final captured = _filter == 'Captured';
    return EmptyState(captured ? 'No captured requests' : 'No network activity',
      message: view.box.remote ? null : captured ? _captureHint : _activityHint);
  }

  Widget _row(BuildContext context, _Activity view, ActivityEntry entry) {
    final box = view.box, capture = entry.capture;
    void open() => openPage(context, RequestDetailPage(id: capture?['id']?.toString(), connection: entry.connection));
    void close() {
      if (view.session == box.sessionRevision) box.act('connections.close', {'id': entry.connectionId});
    }
    return Padding(key: ValueKey(entry.key), padding: const EdgeInsets.only(bottom: recordListGap), child: capture != null
      ? _RequestRecord(item: capture, onOpen: open, onKeyword: _keywords.add)
      : ConnectionRecord(connection: entry.connection!, onOpen: open, onKeyword: _keywords.add,
          onClose: entry.active && box.running ? close : null));
  }
}

class _RequestRecord extends StatelessWidget {
  const _RequestRecord({required this.item, required this.onOpen, this.onKeyword});
  final Json item;
  final VoidCallback onOpen;
  final ValueChanged<String>? onKeyword;

  bool get _hasStatus => (item['status'] as num? ?? 0) > 0;

  String _state(BuildContext context) {
    if (item['domainOnly'] == true) return context.tr('Domain only');
    if (_hasStatus) return item['status'].toString();
    if (item['complete'] != true) return context.tr('Active');
    // A TLS/TCP stream ends without a status code.
    final ended = item['protocol'] == 'TLS/TCP' && (item['error'] as String? ?? '').isEmpty;
    return context.tr(ended ? 'Complete' : 'Error');
  }

  @override Widget build(BuildContext context) {
    final styles = RecordTextStyles.of(context);
    final method = item['method']?.toString() ?? '';
    final rule = item['rule'] as String? ?? '', error = item['error'] as String? ?? '';
    final packages = item['packages'] as List? ?? const [];
    return RecordListItem(onTap: onOpen, header: RecordHeader(children: [
      RecordTimestamp(recordTime(item['startedAt'])),
      RecordLabel(label: method, onPressed: () => onKeyword?.call(method)),
      RecordLabel(label: _state(context)),
      if (item['complete'] != true && _hasStatus) RecordLabel(label: context.tr('Active')),
    ]), body: Column(crossAxisAlignment: CrossAxisAlignment.start, spacing: 4, children: [
      Text(item['url']?.toString() ?? '', maxLines: 2, overflow: TextOverflow.ellipsis, style: styles.title),
      Text('${item['protocol'] ?? ''} · ${item['durationMs'] ?? 0} ms · '
          '${context.tr('Body')} ${bytes(item['bytes'] as num? ?? 0)}', style: styles.secondary),
      if (rule.isNotEmpty || (item['outbound'] as String? ?? '').isNotEmpty)
        Wrap(spacing: 6, crossAxisAlignment: WrapCrossAlignment.center, children: [
          if (item['originalUrl'] != null) Text(context.tr('Original connection'), style: styles.secondary),
          if (rule.isNotEmpty) ...[Text(rule, style: styles.secondary), const RecordArrow()],
          Text(item['outbound']?.toString() ?? '—', style: styles.secondary.copyWith(color: context.colorScheme.primary)),
        ]),
      if (packages.isNotEmpty)
        Text(packages.join(', '), maxLines: 1, overflow: TextOverflow.ellipsis, style: styles.secondary),
      if (error.isNotEmpty)
        Text(error, maxLines: 2, overflow: TextOverflow.ellipsis,
          style: styles.secondary.copyWith(color: context.colorScheme.error)),
    ]));
  }
}

class RequestDetailPage extends StatefulWidget {
  const RequestDetailPage({super.key, this.id, this.connection}) : assert(id != null || connection != null);
  final String? id;
  final Json? connection;
  @override State<RequestDetailPage> createState() => _RequestDetailPageState();
}

/// A reading of one capture detail. Every part is empty until the first reply arrives.
class _Capture {
  _Capture(this.detail) : metadata = json(detail?['metadata']), request = json(detail?['request']);
  final Json? detail;
  final Json metadata, request;

  /// A domain-only observation holds no HTTP message.
  bool get domainOnly => detail?['domainOnly'] == true || request['method'] == 'TLS';
  /// A TLS/TCP stream holds a body in each direction, without headers or cookies.
  bool get rawTCP => metadata['rawTCP'] == true;
  String get connectionId => metadata['connectionId']?.toString() ?? '';

  Json message({required bool response}) => json(detail?[response ? 'response' : 'request']);
  Json preview({required bool response}) => json(detail?[response ? 'responsePreview' : 'requestPreview']);

  /// cURL export needs the whole request and cannot express trailers.
  bool get replayable => detail != null && !domainOnly && !rawTCP && request['bodyComplete'] == true &&
      request['bodyTruncated'] != true && request['headersTruncated'] != true &&
      rows(request['trailers']).isEmpty && request['trailersTruncated'] != true;

  /// The summary rows in display order, under their untranslated titles.
  Map<String, dynamic> get summary {
    final original = detail?['originalUrl'], socket = json(metadata['socket']);
    final error = detail?['error'] as String? ?? '', note = detail?['note'] as String? ?? '';
    return {
      'URL': request['url'],
      'Method': request['method'],
      'Created': localDate(detail?['startedAt']),
      if (original != null) 'Original URL': original,
      'Duration': '${detail?['durationMs'] ?? 0} ms',
      if (!domainOnly && !rawTCP) 'Status': message(response: true)['status'],
      'Protocol': request['protocol'],
      if (connectionId.isNotEmpty) 'Connection ID': connectionId,
      (original != null ? 'Original outbound' : 'Outbound'): metadata['outbound'],
      (original != null ? 'Original rule matched' : 'Rule matched'): metadata['rule'],
      'App': (metadata['packages'] as List? ?? const []).join(', '),
      'UID': metadata['uid'],
      if (metadata['pid'] != null) 'PID': metadata['pid'],
      if (socket.isNotEmpty) ...{'Socket FD': socket['fd'], 'Socket inode': socket['inode']},
      'Source': metadata['source'],
      'Destination': metadata['destination'],
      if (error.isNotEmpty) 'Error': error,
      if (note.isNotEmpty) 'Note': note,
    };
  }
}

class _RequestDetailPageState extends State<RequestDetailPage> {
  // The summary rows whose values may be too long to share a line with the title.
  static const _stackedRows = ['URL', 'Original URL', 'Connection ID', 'App', 'Error', 'Note'];

  String _part = 'Response';
  bool _busy = false;
  int? _session;
  late Json? _cachedConnection = widget.connection;

  @override void didChangeDependencies() {
    super.didChangeDependencies();
    _session ??= context.box.sessionRevision;
  }

  bool _resolveConnection(String id) {
    if (_cachedConnection?['id'] != id) _cachedConnection = null;
    for (final connection in context.box.connections) {
      if (connection['id'] == id) { _cachedConnection = connection; return true; }
    }
    return false;
  }

  // Only a connection that the running service still holds open can be closed.
  static bool _canClose(BoxController box, Json? connection, bool present) =>
      box.running && present && (connection?['closedAt'] as num? ?? 0) <= 0;

  Future<void> _closeConnection(String id, {bool shared = false}) async {
    final box = context.box;
    if (shared && !await confirm(context, 'Close the underlying connection? All requests using it will be interrupted.',
        action: 'Close')) return;
    if (!mounted || _session != box.sessionRevision || !box.running) return;
    if (box.connections.any((connection) => connection['id'] == id && (connection['closedAt'] as num? ?? 0) <= 0)) {
      await box.act('connections.close', {'id': id});
    }
  }

  Future<void> _copy(String text) async {
    await Clipboard.setData(ClipboardData(text: text));
    if (mounted) context.box.messages.add(context.tr('Copied'));
  }

  Future<void> _exportCurl() async {
    setState(() => _busy = true);
    await context.box.act('capture.export', {'format': 'curl', 'id': widget.id});
    if (mounted) setState(() => _busy = false);
  }

  Widget _page({required List<Widget> children, List<IconButtonData> actions = const [],
      List<PopupMenuItem> menuItems = const [], bool loading = false}) => Scaffold(
    title: context.tr('Request details'), iconActions: actions, menuItems: menuItems, isLoading: loading,
    body: Builder(builder: (context) {
      final inset = context.appBarInset, gap = context.contentTopPadding - inset;
      // Scrolling connection rows must not paint beneath the floating title.
      return Padding(padding: EdgeInsets.only(top: inset), child: ListView(
        padding: EdgeInsets.fromLTRB(16, gap > 0 ? gap : 0, 16, 24 + context.bottomInset), children: children,
      ));
    }),
  );

  PopupMenuItem _partItem(String part, {PopupMenuSection? section}) => PopupMenuItem(
    label: context.tr(part), section: section, checked: part == _part, onPressed: () => setState(() => _part = part));

  Widget _headers(Json message, bool response) {
    final headers = rows(message['headers']);
    return generateSection(title: context.tr(response ? 'Response headers' : 'Request headers'), actions: [
      IconButtonData(glyph: AppGlyphs.copy, tooltip: context.tr('Copy headers'), onPressed: headers.isEmpty ? null : () => _copy(
        headers.map((header) => '${header['name']}: ${header['value']}').join('\n'))),
    ], items: [
      if (headers.isEmpty) ListItem(title: Text(context.tr('No headers captured'))),
      for (final header in headers) DetailRow.text(title: header['name']?.toString() ?? '',
        value: header['value']?.toString() ?? '', stacked: true),
    ], footer: message['headersTruncated'] == true ? context.tr('Headers truncated') : null);
  }

  Widget _cookies(Json message, bool response) {
    final cookies = capturedCookies(rows(message['headers']), response: response);
    return generateSection(title: context.tr(response ? 'Response cookies' : 'Request cookies'), actions: [
      IconButtonData(glyph: AppGlyphs.copy, tooltip: context.tr('Copy cookies'), onPressed: cookies.isEmpty ? null : () => _copy(
        cookies.map((cookie) => cookie.raw).join('\n'))),
    ], items: [
      if (cookies.isEmpty) ListItem(title: Text(context.tr('No cookies captured'))),
      for (final cookie in cookies) DetailRow(title: cookie.name, copyText: cookie.raw, stacked: true,
        value: Column(crossAxisAlignment: CrossAxisAlignment.start, spacing: 6, children: [
          Text(cookie.value.isEmpty ? context.tr('Empty value') : cookie.value),
          if (cookie.attributes.isNotEmpty) Text(cookie.attributes.join('\n'), style: context.listCaptionStyle),
        ])),
    ], footer: message['headersTruncated'] == true ? context.tr('Headers truncated') : null);
  }

  Widget _trailers(Json message) => generateSection(title: context.tr('Trailers'), items: [
    for (final trailer in rows(message['trailers']))
      DetailRow.text(title: trailer['name']?.toString() ?? '', value: trailer['value']?.toString() ?? '', stacked: true),
  ], footer: message['trailersTruncated'] == true ? context.tr('Headers truncated') : null);

  @override Widget build(BuildContext context) {
    final box = context.watchBox(BoxAspect.connections);
    if (widget.id != null && box.remote) {
      return _page(children: const [EmptyState('HTTP debugging is only available for the local service')]);
    }
    if (_session != box.sessionRevision) {
      return _page(children: const [EmptyState('Session changed; reopen this activity from the current list')]);
    }
    return BoxDataSubscription(data: BoxData.connections, child: widget.id == null
      ? _connectionDetail(box)
      : CapturePoll(method: 'capture.detail', arguments: {'id': widget.id}, untilComplete: true,
          builder: (context, detail, error, refresh) => _captureDetail(box, _Capture(detail), error, refresh)));
  }

  /// The page of an ordinary connection that was opened without a captured request.
  Widget _connectionDetail(BoxController box) {
    final id = widget.connection!['id'].toString();
    final present = _resolveConnection(id), connection = _cachedConnection!;
    return _page(actions: [
      IconButtonData(glyph: AppGlyphs.copy, tooltip: context.tr('Copy'), onPressed: () => _copy(
        connectionValues(connection).entries.map((e) => '${context.tr(e.key)}: ${e.value ?? '—'}').join('\n'))),
      IconButtonData(glyph: AppGlyphs.close, tooltip: context.tr('Close this connection'),
        onPressed: _canClose(box, connection, present) ? () => _closeConnection(id) : null),
    ], children: [
      ConnectionInfo(connection: connection, present: present),
      _RelatedRequests(connectionId: id, connection: connection),
    ]);
  }

  Widget _captureDetail(BoxController box, _Capture capture, Object? error, VoidCallback refresh) {
    final loaded = capture.detail != null;
    final connectionId = capture.connectionId, linked = connectionId.isNotEmpty;
    final present = linked && _resolveConnection(connectionId);
    final connection = linked ? _cachedConnection : null;
    return _page(loading: _busy, actions: [
      IconButtonData(glyph: AppGlyphs.copy, tooltip: context.tr('Copy URL'),
        onPressed: loaded ? () => _copy(capture.request['url']?.toString() ?? '') : null),
      IconButtonData(glyph: AppGlyphs.arrowDown, tooltip: context.tr('Export cURL'),
        onPressed: capture.replayable && !_busy ? _exportCurl : null),
    ], menuItems: [
      if (linked) _partItem('Connection'),
      if (!capture.domainOnly) for (final part in ['Request', 'Response', if (!capture.rawTCP) 'Cookies'])
        _partItem(part, section: part == 'Request' ? PopupMenuSection(title: context.tr('Message type')) : null),
      if (linked) PopupMenuItem(glyph: AppGlyphs.block, label: context.tr('Close underlying connection'),
        onPressed: _canClose(box, connection, present) ? () => _closeConnection(connectionId, shared: true) : null),
    ], children: [
      if (error != null) ErrorState(error, retry: refresh),
      if (!loaded && error == null) const Center(child: ActivityIndicator()),
      if (loaded) ...(_part == 'Connection'
        ? _connectionPart(capture, connection, present)
        : _messagePart(box, capture)),
    ]);
  }

  List<Widget> _connectionPart(_Capture capture, Json? connection, bool present) => [
    if (connection != null) ConnectionInfo(connection: connection, present: present)
    else generateSection(title: context.tr('Connection'), items: [
      for (final row in capture.summary.entries)
        DetailRow.text(title: context.tr(row.key), value: row.value?.toString() ?? '—', stacked: true),
    ], footer: context.tr('The original connection is no longer in recent history. Saved request information remains available.')),
    _RelatedRequests(connectionId: capture.connectionId, connection: connection, currentId: widget.id),
  ];

  List<Widget> _messagePart(BoxController box, _Capture capture) {
    final response = _part == 'Response';
    final message = capture.message(response: response);
    return [
      generateGroup([
        for (final row in capture.summary.entries) DetailRow.text(title: context.tr(row.key),
          value: row.value?.toString() ?? '—', stacked: _stackedRows.contains(row.key)),
        if (capture.connectionId.isNotEmpty) ListItem(title: Text(context.tr('View connection')),
          onTap: () => setState(() => _part = 'Connection')),
      ]),
      if (capture.domainOnly)
        Padding(padding: const EdgeInsets.all(12), child: Text(context.tr('No HTTP message was captured for this connection')))
      else if (_part == 'Cookies') ...[
        _cookies(capture.request, false),
        _cookies(capture.message(response: true), true),
      ] else ...[
        if (!capture.rawTCP) _headers(message, response),
        CapturedBody(key: ValueKey('body-${widget.id}-$_part'), message: message, preview: capture.preview(response: response),
          loadPage: (offset, source) async => json(await box.call('capture.body', {
            'id': widget.id, 'direction': response ? 'response' : 'request', 'offset': offset, 'source': source,
          }))),
        if (rows(message['trailers']).isNotEmpty) _trailers(message),
      ],
    ];
  }
}

class _RelatedRequests extends StatefulWidget {
  const _RelatedRequests({required this.connectionId, this.connection, this.currentId});
  final String connectionId;
  final String? currentId;
  final Json? connection;
  @override State<_RelatedRequests> createState() => _RelatedRequestsState();
}

class _RelatedRequestsState extends State<_RelatedRequests> {
  int _limit = _pageSize;

  Widget _item(BuildContext context, Json item) {
    final current = item['id'] == widget.currentId;
    final currentMark = current ? ' · ${context.tr('Current request')}' : '';
    return ListItem(
      key: ValueKey('related:${item['id']}'),
      title: Text(item['url']?.toString() ?? '', maxLines: 2, overflow: TextOverflow.ellipsis),
      subtitle: Text('${item['method'] ?? ''} · ${item['protocol'] ?? ''}$currentMark'),
      onTap: current ? null
        : () => openPage(context, RequestDetailPage(id: item['id'].toString(), connection: widget.connection)),
    );
  }

  @override Widget build(BuildContext context) {
    if (widget.connectionId.isEmpty) return const SizedBox.shrink();
    if (context.box.remote) return Padding(padding: const EdgeInsets.all(12),
      child: Text(context.tr('HTTP debugging is only available for the local service'), style: context.listSubtitleStyle));
    return CapturePoll(method: 'capture.query', load: loadCapturePage, identity: widget.connectionId,
      arguments: {'query': '', 'connectionId': widget.connectionId, 'limit': _limit},
      builder: (context, snapshot, error, refresh) {
        final page = json(snapshot?['page']);
        final items = page['items'] as List<Json>? ?? const [];
        return generateSection(title: context.tr('Related captured requests'), items: [
          if (error != null) ErrorState(error, retry: refresh),
          if (snapshot == null && error == null) const Center(child: ActivityIndicator()),
          if (snapshot != null && items.isEmpty) ListItem(title: Text(context.tr('No captured requests for this connection'))),
          for (final item in items.take(_limit)) _item(context, item),
          if (page['hasMore'] == true)
            TextButton(onPressed: () => setState(() => _limit += _pageSize), child: Text(context.tr('Show more'))),
        ]);
      });
  }
}
