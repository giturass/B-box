import 'package:flutter/services.dart';
import '../widgets.dart';
import '../components/capture_poll.dart';
import '../components/record.dart';
import 'request_settings.dart';
import 'requests.dart';

class ConnectionsPage extends StatefulWidget {
  const ConnectionsPage({super.key});
  @override State<ConnectionsPage> createState() => _ConnectionsPageState();
}
class _ConnectionsPageState extends State<ConnectionsPage> {
  BoxController? _box;
  String _query = '', _filter = 'All', _sort = 'Speed';
  int _limit = 50, _revision = 0;
  bool _busy = false;

  @override void didChangeDependencies() {
    super.didChangeDependencies();
    if (_box == null) {
      _box = context.box;
      _box!.act('connections.visible', {'visible': true});
    }
  }
  @override void dispose() {
    _box?.act('connections.visible', {'visible': false});
    super.dispose();
  }
  Future<void> _closeAll() async {
    final box = context.box;
    if (await confirm(context, 'Close all active connections?', action: 'Close')) await box.act('connections.closeAll');
  }

  Future<void> _captureAction(String method, [Json arguments = const {}]) async {
    if (_busy || context.box.remote) return;
    setState(() => _busy = true);
    await context.box.act(method, arguments);
    if (mounted) setState(() { _busy = false; _revision++; });
  }

  Future<void> _captureSettings() async {
    if (_busy || context.box.remote) return;
    setState(() => _busy = true);
    try {
      final source = json(await context.box.call('capture.settings'));
      if (mounted && !context.box.remote) {
        await openPage(context, RequestSettingsPage(initial: json(source['config']), source: source));
      }
    } catch (error) {
      if (mounted) context.box.messages.add(BoxController.describe(error));
    } finally {
      if (mounted) setState(() { _busy = false; _revision++; });
    }
  }

  void _search(String query) => setState(() { _query = query; _limit = 50; });

  Widget _buildList(BuildContext context, Json? snapshot, Object? error, VoidCallback? refresh) {
    final box = context.watchBox(BoxAspect.connections);
    final state = json(snapshot?['state']), page = json(snapshot?['page']);
    final active = !box.remote && state['active'] == true;
    // A remote session always shows its connections, even after viewing local history.
    final filter = box.remote && _filter == 'Requests' ? 'All' : _filter;
    final showConnections = filter != 'Requests';
    final showRequests = !box.remote && (filter == 'Requests' || filter == 'All' && active);
    final requests = rows(page['items']);
    final words = _query.toLowerCase().trim().split(RegExp(r'\s+'));
    final connections = box.connections.where((connection) {
      if (!showConnections) return false;
      final closed = (connection['closedAt'] as num? ?? 0) > 0;
      if (filter == 'Active' && closed || filter == 'Closed' && !closed) return false;
      final search = [
        connection['displayDestination'], connection['destination'], connection['domain'], connection['outbound'],
        connection['network'], connection['protocol'], connection['rule'], connection['source'], connection['process'],
      ].join(' ').toLowerCase();
      return words.every(search.contains);
    }).toList();
    final key = {'Created': 'createdAt', 'Upload': 'uploadTotal', 'Download': 'downloadTotal'}[_sort];
    num rank(Json c) => key == null ? (c['upload'] as num? ?? 0) + (c['download'] as num? ?? 0) : c[key] as num? ?? 0;
    connections.sort((a, b) => rank(b).compareTo(rank(a)));
    final recordCount = connections.length + (showRequests ? requests.length : 0);
    return CommonScaffold(
      title: context.tr('Connections and requests'), isLoading: _busy,
      onKeywordsUpdate: (words) => _search(words.join(' ')),
      searchState: AppBarSearchState(onSearch: _search),
      iconActions: [
        if (!box.remote) IconButtonData(glyph: AppGlyphs.refresh, tooltip: context.tr('Refresh'), onPressed: refresh),
        IconButtonData(glyph: AppGlyphs.clearAll, tooltip: context.tr('Close all connections'), onPressed: box.running ? _closeAll : null),
      ],
      menuItems: [
        CommonPopupMenuItem(glyph: AppGlyphs.filter, label: context.tr('Show records'), subItems: [
          for (final value in ['All', 'Connections', 'Active', 'Closed', if (!box.remote) 'Requests'])
            CommonPopupMenuItem(label: context.tr(switch (value) {
              'Active' => 'Active connections', 'Closed' => 'Closed connections', _ => value,
            }), checked: value == filter, onPressed: () => setState(() => _filter = value)),
        ]),
        if (showConnections) CommonPopupMenuItem(glyph: AppGlyphs.sort, label: context.tr('Connection sorting'), subItems: [
          for (final sort in ['Speed', 'Created', 'Upload', 'Download'])
            CommonPopupMenuItem(label: context.tr(sort), checked: sort == _sort, onPressed: () => setState(() => _sort = sort)),
        ]),
        if (!box.remote) ...[
          CommonPopupMenuItem(glyph: AppGlyphs.sliders, label: context.tr('Capture settings'),
            section: CommonPopupMenuSection(title: context.tr('Requests')),
            onPressed: _busy || active || snapshot == null ? null : _captureSettings),
          CommonPopupMenuItem(glyph: AppGlyphs.arrowDown, label: context.tr('Export HAR'),
            onPressed: _busy || requests.isEmpty ? null : () => _captureAction('capture.export', {'format': 'har', 'query': _query})),
          CommonPopupMenuItem(glyph: AppGlyphs.delete, label: context.tr('Clear requests'), onPressed: _busy || snapshot == null ? null : () async {
            if (await confirm(context, 'Clear captured requests?') && mounted) await _captureAction('capture.clear');
          }),
        ],
      ],
      // Read the insets below the scaffold, including its docked search field.
      body: Builder(builder: (context) => ListView.builder(
        padding: EdgeInsets.only(top: context.contentTopPadding, bottom: 24 + context.bottomInset),
        itemCount: recordCount + 2,
        itemBuilder: (context, index) {
          if (index == 0) return Padding(padding: const EdgeInsets.symmetric(horizontal: 16), child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              if (!box.remote) ...[
                generateGroup([
                  ListItem.toggle(key: const ValueKey('capture-toggle'), title: Text(context.tr('Capture requests')),
                    subtitle: Text(context.tr(active ? 'Capturing new connections' : 'Capture stopped')), value: active,
                    onChanged: _busy || snapshot == null || !box.running || box.transitioning ? null : (_) => _captureAction(active ? 'capture.stop' : 'capture.start')),
                ]),
                if (!box.running) Padding(padding: const EdgeInsets.all(12), child: Text(context.tr('Start the local service before capturing requests'))),
              ],
              Padding(padding: const EdgeInsets.all(12), child: Text([
                if (showConnections) '${connections.length} ${context.tr('Connections')}',
                if (showRequests && snapshot != null) '${page['total'] ?? 0} ${context.tr('Requests')} · ${bytes(page['storedBytes'] as num? ?? 0)} / 32 MiB',
              ].join(' · '), style: context.listSubtitleStyle)),
              if (!box.remote) ...[
                if (!showRequests && (page['total'] as num? ?? 0) > 0) Align(alignment: Alignment.centerLeft, child: TextButton(
                  onPressed: () => setState(() => _filter = 'Requests'),
                  child: Text('${context.tr('View captured requests')} (${page['total']})'),
                )),
                if ((page['error'] as String? ?? '').isNotEmpty) Text(page['error'].toString(), style: TextStyle(color: context.colorScheme.error)),
                if (error != null) ErrorState(error, retry: refresh),
                if (snapshot == null && error == null) const Center(child: CircularProgressIndicator()),
              ],
              if (recordCount == 0 && (!showRequests || snapshot != null)) EmptyState(
                _query.trim().isNotEmpty ? 'No results' : showRequests
                  ? showConnections ? 'No connections or captured requests' : 'No captured requests'
                  : 'No connections',
                message: showRequests && _query.trim().isEmpty
                  ? 'Capture follows the local service. Configure MITM domains and trust the CA to record HTTPS or TLS/TCP. Start capture, then reconnect the target app.' : null,
              ),
            ],
          ));
          if (index > recordCount) return showRequests && (page['total'] as num? ?? 0) > requests.length && _limit < 300
            ? TextButton(onPressed: () => setState(() => _limit = (_limit + 50).clamp(1, 300).toInt()), child: Text(context.tr('Show more')))
            : const SizedBox.shrink();
          final connectionIndex = index - 1;
          final isRequest = connectionIndex >= connections.length;
          final item = isRequest ? requests[connectionIndex - connections.length] : connections[connectionIndex];
          return Padding(
            key: ValueKey('${isRequest ? 'request' : 'connection'}-${item['id']}'),
            padding: const EdgeInsets.only(bottom: recordListGap),
            child: isRequest ? RequestRecord(request: item) : ConnectionRecord(connection: item),
          );
        },
      )),
    );
  }

  @override Widget build(BuildContext context) {
    if (context.box.remote) return _buildList(context, null, null, null);
    return CapturePoll(method: 'capture.snapshot', arguments: {'query': _query, 'limit': _limit}, revision: _revision,
      builder: (context, snapshot, error, refresh) => _buildList(context, snapshot, error, refresh));
  }
}

class ConnectionRecord extends StatelessWidget {
  const ConnectionRecord({super.key, required this.connection});
  final Json connection;
  @override Widget build(BuildContext context) {
    final c = connection, box = context.box;
    final styles = RecordTextStyles.of(context);
    final closed = (c['closedAt'] as num? ?? 0) > 0;
    return RecordListItem(
      key: ValueKey(c['id']),
      onTap: () => openPage(context, ConnectionDetail(initial: c)),
      header: RecordHeader(
        children: [
          RecordTimestamp(recordTime(c['createdAt'])),
          RecordLabel(label: context.tr('Connection')),
          RecordLabel(label: c['network'].toString().toUpperCase(), onPressed: () => context.commonScaffoldState?.addKeyword(c['network'].toString())),
          if (closed) RecordLabel(label: context.tr('Closed')),
        ],
        trailing: !closed && box.running ? Tooltip(
          message: context.tr('Close this connection'), child: Semantics(button: true, child: InkWell(
            customBorder: AppShape.circle, onTap: () => box.act('connections.close', {'id': c['id']}),
            child: SizedBox.square(dimension: 28, child: Center(child: GlyphIcon(AppGlyphs.block, size: 16, color: context.colorScheme.onSurfaceVariant))),
          )),
        ) : null,
      ),
      body: Column(crossAxisAlignment: CrossAxisAlignment.start, spacing: 4, children: [
        Text(c['displayDestination']?.toString() ?? c['destination']?.toString() ?? '—', style: styles.title, maxLines: 2, overflow: TextOverflow.ellipsis),
        Wrap(crossAxisAlignment: WrapCrossAlignment.center, spacing: 6, children: [
          if ((c['rule'] as String? ?? '').isNotEmpty) ...[Text(c['rule'].toString(), style: styles.secondary), const RecordArrow()],
          Text(c['outbound']?.toString() ?? '—', style: AppTextStyle.footnote.of(context, emphasized: true).copyWith(color: context.colorScheme.primary)),
        ]),
        Text(c['source']?.toString() ?? '—', style: styles.secondary, maxLines: 1, overflow: TextOverflow.ellipsis),
        Wrap(spacing: 16, runSpacing: 4, children: [for (final upload in [true, false]) Row(mainAxisSize: MainAxisSize.min, spacing: 4, children: [
          GlyphIcon(upload ? AppGlyphs.arrowUp : AppGlyphs.arrowDown, size: 12, color: context.colorScheme.onSurfaceVariant),
          Text('${bytes(c[upload ? 'uploadTotal' : 'downloadTotal'] as num? ?? 0)} · ${bytes(c[upload ? 'upload' : 'download'] as num? ?? 0)}/s', style: styles.secondary.toTabular),
        ])]),
      ]),
    );
  }
}

class ConnectionDetail extends StatefulWidget {
  const ConnectionDetail({super.key, required this.initial});
  final Json initial;
  @override State<ConnectionDetail> createState() => _ConnectionDetailState();
}
class _ConnectionDetailState extends State<ConnectionDetail> {
  late Json _cached = widget.initial;
  @override Widget build(BuildContext context) {
    final box = context.watchBox(BoxAspect.connections);
    var present = false;
    for (final connection in box.connections) {
      if (connection['id'] == widget.initial['id']) { _cached = connection; present = true; break; }
    }
    final c = _cached;
    final values = <String, dynamic>{
      'Destination': c['displayDestination'], 'Source': c['source'], 'Network': c['network'],
      'Protocol': c['protocol'], 'Inbound': c['inbound'], 'Outbound': c['outbound'],
      'Rule matched': c['rule'], 'Chain': (c['chain'] as List? ?? []).join(' → '),
      'Created': localDate(c['createdAt']),
      if ((c['closedAt'] as num? ?? 0) > 0) 'Closed': localDate(c['closedAt']),
      'Upload': bytes(c['uploadTotal'] as num? ?? 0), 'Download': bytes(c['downloadTotal'] as num? ?? 0),
      if (c['process'] != null) 'Process': json(c['process']).entries.map((e) => e.key + ': ' + e.value.toString()).join('\n'),
    };
    return BoxPage(
      title: 'Details',
      actions: [
        IconButtonData(glyph: AppGlyphs.copy, tooltip: context.tr('Copy'), onPressed: () async {
          await Clipboard.setData(ClipboardData(text: values.entries.map((e) => e.key + ': ' + e.value.toString()).join('\n')));
          box.messages.add(context.tr('Copied'));
        }),
        IconButtonData(glyph: AppGlyphs.close, tooltip: context.tr('Close this connection'),
          onPressed: box.running && present && (c['closedAt'] as num? ?? 0) == 0 ? () => box.act('connections.close', {'id': c['id']}) : null),
      ],
      children: [generateGroup([for (final value in values.entries) DetailRow.text(title: context.tr(value.key), value: value.value?.toString() ?? '—')])],
    );
  }
}
