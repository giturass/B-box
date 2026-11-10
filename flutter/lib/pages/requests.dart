import 'package:flutter/services.dart';
import '../widgets.dart';
import '../common/http_message.dart';
import '../common/network_activity.dart';
import '../components/capture_poll.dart';
import '../components/captured_body.dart';
import '../components/connection_record.dart';
import '../components/data_subscription.dart';
import '../components/record.dart';
import 'request_settings.dart';

class RequestsPage extends StatefulWidget {
  const RequestsPage({super.key});
  @override State<RequestsPage> createState() => _RequestsPageState();
}

class _RequestsPageState extends State<RequestsPage> {
  final _list = ActivityList();
  late final _keywords = RecordKeywords()..addListener(_keywordsChanged);
  String _query = '', _filter = 'All', _sort = 'Created';
  int _limit = 50, _revision = 0;
  bool _busy = false;
  Json? _snapshot;
  List<Json> _captures = [];

  void _keywordsChanged() => setState(() => _limit = 50);
  @override void dispose() { _keywords.dispose(); super.dispose(); }

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

  @override Widget build(BuildContext context) {
    final box = context.watchBox(BoxAspect.connections);
    return BoxDataSubscription(data: BoxData.connections, child: box.remote
      ? _buildList(context, null, null, null)
      : CapturePoll(method: 'capture.snapshot', arguments: const {'query': '', 'limit': 0}, revision: _revision,
          builder: _buildList));
  }

  Widget _buildList(BuildContext context, Json? snapshot, Object? error, VoidCallback? refresh) {
    final box = context.watchBox(BoxAspect.connections), session = context.box.sessionRevision;
    final state = json(snapshot?['state']), page = json(snapshot?['page']);
    if (!identical(snapshot, _snapshot)) {
      _snapshot = snapshot;
      _captures = rows(page['items']);
    }
    final query = _keywords.queryWith(_query);
    final filter = box.remote && _filter == 'Captured' ? 'All' : _filter;
    final list = _list.select(box.connections, box.remote ? const [] : _captures, query: query, filter: filter, sort: _sort);
    final entries = list.take(_limit).toList();
    final harIds = [for (final entry in list) if (entry.supportsHAR) entry.capture!['id'].toString()];
    final active = state['active'] == true;
    return Scaffold(
      title: context.tr('Connections and requests'), isLoading: _busy, keywords: _keywords,
      searchState: AppBarSearchState(onSearch: (query) => setState(() { _query = query; _limit = 50; })),
      iconActions: [if (refresh != null) IconButtonData(glyph: AppGlyphs.refresh, tooltip: context.tr('Refresh'), onPressed: refresh)],
      menuItems: [
        PopupMenuItem(glyph: AppGlyphs.sort, label: context.tr('Sort'), subtitle: context.tr(_sort), subItems: [
          for (final sort in ['Created', 'Speed', 'Upload', 'Download'])
            PopupMenuItem(label: context.tr(sort), checked: sort == _sort,
              onPressed: () => setState(() { _sort = sort; _limit = 50; })),
        ]),
        PopupMenuItem(glyph: AppGlyphs.clearAll, label: context.tr('Close all connections'), onPressed: box.running ? _closeAll : null),
        if (!box.remote) ...[
          PopupMenuItem(glyph: AppGlyphs.sliders, label: context.tr('Capture settings'), onPressed: _busy || active || snapshot == null ? null : _settings),
          PopupMenuItem(glyph: AppGlyphs.arrowDown, label: context.tr('Export HAR'), onPressed: _busy || harIds.isEmpty ? null :
            () => _act('capture.export', {'format': 'har', 'ids': harIds})),
          PopupMenuItem(glyph: AppGlyphs.delete, label: context.tr('Clear captured requests'), onPressed: _busy || snapshot == null ? null : () async {
            if (await confirm(context, 'Clear captured requests?', action: 'Clear') && mounted && session == box.sessionRevision) await _act('capture.clear');
          }),
        ],
      ],
      // Join and filter all summaries first; paginate the unified result.
      body: Builder(builder: (context) {
        final inset = context.appBarInset, gap = context.contentTopPadding - inset;
        return Padding(padding: EdgeInsets.only(top: inset), child: ListView.builder(
          padding: EdgeInsets.only(top: gap > 0 ? gap : 0, bottom: 24 + context.bottomInset),
          itemCount: entries.length + 2,
          itemBuilder: (context, index) {
            if (index == 0) return Padding(padding: const EdgeInsets.symmetric(horizontal: 16), child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
              Padding(padding: const EdgeInsets.only(bottom: 12), child: Wrap(spacing: 8, runSpacing: 8, children: [
                for (final option in ['All', 'Active', if (!box.remote) 'Captured', 'Finished']) Semantics(selected: option == filter, button: true,
                  child: PressSurface(key: ValueKey('activity-filter-$option'), shape: AppShape.full,
                    fill: option == filter ? context.colorScheme.primary : context.raisedSurfaceLevel.colorOf(context.colorScheme),
                    onPressed: () => setState(() { _filter = option; _limit = 50; }),
                    child: Padding(padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
                      child: Text(context.tr(option), style: context.listSubtitleStyle.copyWith(
                        color: option == filter ? context.colorScheme.onPrimary : context.colorScheme.onSurface))),
                  )),
              ])),
              if (box.remote) Padding(padding: const EdgeInsets.only(bottom: 12),
                child: Text(context.tr('HTTP debugging is only available for the local service'), style: context.listSubtitleStyle))
              else ...[
                generateGroup([
                  ListItem.toggle(key: const ValueKey('capture-toggle'), title: Text(context.tr('Capture requests')),
                    subtitle: Text(context.tr(active ? 'Capturing new requests and streams' : 'Capture stopped')), value: active,
                    onChanged: _busy || snapshot == null || !box.running || box.transitioning ? null : (_) => _act(active ? 'capture.stop' : 'capture.start')),
                ]),
                if (!box.running) Padding(padding: const EdgeInsets.all(12), child: Text(context.tr('Start the local service before capturing requests'))),
                if (snapshot != null) Padding(padding: const EdgeInsets.all(12), child: Text(
                  '${page['total'] ?? 0} ${context.tr('Captured')} · ${bytes(page['storedBytes'] as num? ?? 0)}', style: context.listSubtitleStyle)),
                if ((page['error'] as String? ?? '').isNotEmpty) Text(page['error'].toString(), style: TextStyle(color: context.colorScheme.error)),
                if (error != null) ErrorState(error, retry: refresh),
                if (snapshot == null && error == null) const Center(child: ActivityIndicator()),
              ],
              if ((box.remote || snapshot != null) && entries.isEmpty) EmptyState(query.isNotEmpty ? 'No results' : _filter == 'Captured' ? 'No captured requests' : 'No network activity',
                message: box.remote || query.isNotEmpty ? null : _filter == 'Captured'
                  ? 'Configure MITM domains and trust the CA to record HTTPS or TLS/TCP. Recording starts with new requests or streams. Reconnect the target app if its connection was not already intercepted. Stopping recording keeps existing connections running.'
                  : 'Recent connections appear while the service is running. Capture saves HTTP messages and decrypted TLS streams separately.'),
            ]));
            if (index > entries.length) return list.length > entries.length
              ? TextButton(onPressed: () => setState(() => _limit += 50), child: Text(context.tr('Show more')))
              : const SizedBox.shrink();
            final entry = entries[index - 1];
            void open() => openPage(context, RequestDetailPage(id: entry.capture?['id']?.toString(), connection: entry.connection));
            return Padding(key: ValueKey(entry.key), padding: const EdgeInsets.only(bottom: recordListGap), child: entry.capture != null
              ? _RequestRecord(item: entry.capture!, onOpen: open, onKeyword: _keywords.add)
              : ConnectionRecord(connection: entry.connection!, onOpen: open, onKeyword: _keywords.add,
                  onClose: entry.active && box.running ? () {
                    if (session == box.sessionRevision) box.act('connections.close', {'id': entry.connectionId});
                  } : null));
          },
        ));
      }),
    );
  }
}

class _RequestRecord extends StatelessWidget {
  const _RequestRecord({required this.item, required this.onOpen, this.onKeyword});
  final Json item;
  final VoidCallback onOpen;
  final ValueChanged<String>? onKeyword;
  @override Widget build(BuildContext context) {
    final styles = RecordTextStyles.of(context);
    return RecordListItem(onTap: onOpen, header: RecordHeader(children: [
      RecordTimestamp(recordTime(item['startedAt'])),
      RecordLabel(label: item['method']?.toString() ?? '', onPressed: () => onKeyword?.call(item['method']?.toString() ?? '')),
      RecordLabel(label: item['domainOnly'] == true ? context.tr('Domain only') : (item['status'] as num? ?? 0) > 0 ? item['status'].toString() :
        context.tr(item['complete'] != true ? 'Active' : item['protocol'] == 'TLS/TCP' && (item['error'] as String? ?? '').isEmpty ? 'Complete' : 'Error')),
      if (item['complete'] != true && (item['status'] as num? ?? 0) > 0) RecordLabel(label: context.tr('Active')),
    ]), body: Column(crossAxisAlignment: CrossAxisAlignment.start, spacing: 4, children: [
      Text(item['url']?.toString() ?? '', maxLines: 2, overflow: TextOverflow.ellipsis, style: styles.title),
      Text('${item['protocol'] ?? ''} · ${item['durationMs'] ?? 0} ms · ${context.tr('Body')} ${bytes(item['bytes'] as num? ?? 0)}', style: styles.secondary),
      if ((item['rule'] as String? ?? '').isNotEmpty || (item['outbound'] as String? ?? '').isNotEmpty) Wrap(spacing: 6, crossAxisAlignment: WrapCrossAlignment.center, children: [
        if (item['originalUrl'] != null) Text(context.tr('Original connection'), style: styles.secondary),
        if ((item['rule'] as String? ?? '').isNotEmpty) ...[Text(item['rule'].toString(), style: styles.secondary), const RecordArrow()],
        Text(item['outbound']?.toString() ?? '—', style: styles.secondary.copyWith(color: context.colorScheme.primary)),
      ]),
      if ((item['packages'] as List? ?? []).isNotEmpty) Text((item['packages'] as List).join(', '), maxLines: 1, overflow: TextOverflow.ellipsis, style: styles.secondary),
      if ((item['error'] as String? ?? '').isNotEmpty) Text(item['error'].toString(), maxLines: 2, overflow: TextOverflow.ellipsis, style: styles.secondary.copyWith(color: context.colorScheme.error)),
    ]));
  }
}

class RequestDetailPage extends StatefulWidget {
  const RequestDetailPage({super.key, this.id, this.connection}) : assert(id != null || connection != null);
  final String? id;
  final Json? connection;
  @override State<RequestDetailPage> createState() => _RequestDetailPageState();
}

class _RequestDetailPageState extends State<RequestDetailPage> {
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

  Future<void> _closeConnection(String id, {bool shared = false}) async {
    final box = context.box;
    if (shared && !await confirm(context, 'Close the underlying connection? All requests using it will be interrupted.', action: 'Close')) return;
    if (!mounted || _session != box.sessionRevision || !box.running) return;
    if (box.connections.any((connection) => connection['id'] == id && (connection['closedAt'] as num? ?? 0) <= 0)) {
      await box.act('connections.close', {'id': id});
    }
  }

  Future<void> _copy(String text) async {
    await Clipboard.setData(ClipboardData(text: text));
    if (mounted) context.box.messages.add(context.tr('Copied'));
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

  @override Widget build(BuildContext context) {
    final box = context.watchBox(BoxAspect.connections);
    if (widget.id != null && box.remote) return _page(children: const [EmptyState('HTTP debugging is only available for the local service')]);
    if (_session != box.sessionRevision) return _page(children: const [EmptyState('Session changed; reopen this activity from the current list')]);
    if (widget.id == null) {
      final id = widget.connection!['id'].toString();
      final present = _resolveConnection(id), connection = _cachedConnection!;
      return BoxDataSubscription(data: BoxData.connections, child: _page(actions: [
        IconButtonData(glyph: AppGlyphs.copy, tooltip: context.tr('Copy'), onPressed: () => _copy(
          connectionValues(connection).entries.map((e) => '${context.tr(e.key)}: ${e.value ?? '—'}').join('\n'))),
        IconButtonData(glyph: AppGlyphs.close, tooltip: context.tr('Close this connection'),
          onPressed: box.running && present && (connection['closedAt'] as num? ?? 0) <= 0 ? () => _closeConnection(id) : null),
      ], children: [
        ConnectionInfo(connection: connection, present: present),
        _RelatedRequests(connectionId: id, connection: connection),
      ]));
    }
    return BoxDataSubscription(data: BoxData.connections, child: CapturePoll(method: 'capture.detail', arguments: {'id': widget.id}, untilComplete: true, builder: (context, detail, error, refresh) {
      final metadata = json(detail?['metadata']), request = json(detail?['request']);
      final socket = json(metadata['socket']);
      final domainOnly = detail?['domainOnly'] == true || request['method'] == 'TLS';
      final connectionId = metadata['connectionId']?.toString() ?? '';
      final present = connectionId.isNotEmpty && _resolveConnection(connectionId);
      final connection = connectionId.isNotEmpty ? _cachedConnection : null;
      final response = _part == 'Response';
      final message = json(detail?[response ? 'response' : 'request']);
      final preview = json(detail?[response ? 'responsePreview' : 'requestPreview']);
      final values = <String, dynamic>{
        'URL': request['url'], 'Method': request['method'], 'Created': localDate(detail?['startedAt']),
        if (detail?['originalUrl'] != null) 'Original URL': detail?['originalUrl'],
        'Duration': '${detail?['durationMs'] ?? 0} ms',
        if (!domainOnly && metadata['rawTCP'] != true) 'Status': json(detail?['response'])['status'],
        'Protocol': request['protocol'],
        if ((metadata['connectionId'] as String? ?? '').isNotEmpty) 'Connection ID': metadata['connectionId'],
        (detail?['originalUrl'] != null ? 'Original outbound' : 'Outbound'): metadata['outbound'],
        (detail?['originalUrl'] != null ? 'Original rule matched' : 'Rule matched'): metadata['rule'],
        'App': (metadata['packages'] as List? ?? []).join(', '), 'UID': metadata['uid'],
        if (metadata['pid'] != null) 'PID': metadata['pid'],
        if (socket.isNotEmpty) 'Socket FD': socket['fd'],
        if (socket.isNotEmpty) 'Socket inode': socket['inode'],
        'Source': metadata['source'], 'Destination': metadata['destination'],
        if ((detail?['error'] as String? ?? '').isNotEmpty) 'Error': detail?['error'],
        if ((detail?['note'] as String? ?? '').isNotEmpty) 'Note': detail?['note'],
      };
      return _page(loading: _busy, actions: [
        IconButtonData(glyph: AppGlyphs.copy, tooltip: context.tr('Copy URL'), onPressed: detail == null ? null : () => _copy(request['url']?.toString() ?? '')),
        IconButtonData(glyph: AppGlyphs.arrowDown, tooltip: context.tr('Export cURL'), onPressed: detail == null || _busy || request['bodyComplete'] != true || request['bodyTruncated'] == true || request['headersTruncated'] == true || rows(request['trailers']).isNotEmpty || request['trailersTruncated'] == true || domainOnly || metadata['rawTCP'] == true ? null : () async {
          setState(() => _busy = true);
          await context.box.act('capture.export', {'format': 'curl', 'id': widget.id});
          if (mounted) setState(() => _busy = false);
        }),
      ], menuItems: [
        if (connectionId.isNotEmpty) PopupMenuItem(label: context.tr('Connection'), checked: _part == 'Connection', onPressed: () => setState(() => _part = 'Connection')),
        if (!domainOnly) for (final part in ['Request', 'Response', if (metadata['rawTCP'] != true) 'Cookies']) PopupMenuItem(label: context.tr(part),
          section: part == 'Request' ? PopupMenuSection(title: context.tr('Message type')) : null,
          checked: part == _part, onPressed: () => setState(() => _part = part)),
        if (connectionId.isNotEmpty) PopupMenuItem(glyph: AppGlyphs.block, label: context.tr('Close underlying connection'),
          onPressed: box.running && present && (connection?['closedAt'] as num? ?? 0) <= 0 ? () => _closeConnection(connectionId, shared: true) : null),
      ], children: [
        if (error != null) ErrorState(error, retry: refresh),
        if (detail == null && error == null) const Center(child: ActivityIndicator()),
        if (detail != null) ...[
          if (_part == 'Connection') ...[
            if (connection != null) ConnectionInfo(connection: connection, present: present)
            else generateSection(title: context.tr('Connection'), items: [
              for (final value in values.entries) DetailRow.text(title: context.tr(value.key), value: value.value?.toString() ?? '—', stacked: true),
            ], footer: context.tr('The original connection is no longer in recent history. Saved request information remains available.')),
            _RelatedRequests(connectionId: connectionId, connection: connection, currentId: widget.id),
          ] else ...[
            generateGroup([
              for (final value in values.entries) DetailRow.text(title: context.tr(value.key),
                value: value.value?.toString() ?? '—', stacked: const ['URL', 'Original URL', 'Connection ID', 'App', 'Error', 'Note'].contains(value.key)),
              if (connectionId.isNotEmpty) ListItem(title: Text(context.tr('View connection')),
                onTap: () => setState(() => _part = 'Connection')),
            ]),
            if (domainOnly) Padding(padding: const EdgeInsets.all(12), child: Text(context.tr('No HTTP message was captured for this connection')))
            else if (_part == 'Cookies') ...[
              _cookies(request, false),
              _cookies(json(detail['response']), true),
            ] else ...[
              if (metadata['rawTCP'] != true) _headers(message, response),
              CapturedBody(key: ValueKey('body-${widget.id}-$_part'), message: message, preview: preview,
                loadPage: (offset, source) async => json(await box.call('capture.body', {
                  'id': widget.id, 'direction': response ? 'response' : 'request', 'offset': offset, 'source': source,
                }))),
              if (rows(message['trailers']).isNotEmpty) generateSection(title: context.tr('Trailers'), items: [
                for (final trailer in rows(message['trailers'])) DetailRow.text(title: trailer['name']?.toString() ?? '', value: trailer['value']?.toString() ?? '', stacked: true),
              ], footer: message['trailersTruncated'] == true ? context.tr('Headers truncated') : null),
            ],
          ],
        ],
      ]);
    }));
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
  int _limit = 50;
  @override Widget build(BuildContext context) {
    if (widget.connectionId.isEmpty) return const SizedBox.shrink();
    if (context.box.remote) return Padding(padding: const EdgeInsets.all(12),
      child: Text(context.tr('HTTP debugging is only available for the local service'), style: context.listSubtitleStyle));
    return CapturePoll(method: 'capture.snapshot', arguments: const {'query': '', 'limit': 0}, builder: (context, snapshot, error, refresh) {
      final items = rows(json(snapshot?['page'])['items']).where((item) => item['connectionId'] == widget.connectionId).toList();
      return generateSection(title: context.tr('Related captured requests'), items: [
        if (error != null) ErrorState(error, retry: refresh),
        if (snapshot == null && error == null) const Center(child: ActivityIndicator()),
        if (snapshot != null && items.isEmpty) ListItem(title: Text(context.tr('No captured requests for this connection'))),
        for (final item in items.take(_limit)) ListItem(
          key: ValueKey('related:${item['id']}'),
          title: Text(item['url']?.toString() ?? '', maxLines: 2, overflow: TextOverflow.ellipsis),
          subtitle: Text('${item['method'] ?? ''} · ${item['protocol'] ?? ''}${item['id'] == widget.currentId ? ' · ${context.tr('Current request')}' : ''}'),
          onTap: item['id'] == widget.currentId ? null : () => openPage(context, RequestDetailPage(id: item['id'].toString(), connection: widget.connection)),
        ),
        if (items.length > _limit) TextButton(onPressed: () => setState(() => _limit += 50), child: Text(context.tr('Show more'))),
      ]);
    });
  }
}
