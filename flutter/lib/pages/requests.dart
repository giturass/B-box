import 'package:flutter/services.dart';
import '../widgets.dart';
import '../components/capture_poll.dart';
import '../components/record.dart';
import 'request_settings.dart';
import 'capture_certificate.dart';

class RequestsPage extends StatefulWidget {
  const RequestsPage({super.key});
  @override State<RequestsPage> createState() => _RequestsPageState();
}

class _RequestsPageState extends State<RequestsPage> {
  String _query = '';
  int _limit = 50, _revision = 0;
  bool _busy = false;

  Future<void> _act(String method, [Json arguments = const {}]) async {
    if (_busy) return;
    setState(() => _busy = true);
    await context.box.act(method, arguments);
    if (mounted) setState(() { _busy = false; _revision++; });
  }

  @override Widget build(BuildContext context) {
    final box = context.box;
    if (box.remote) return const BoxPage(title: 'Requests', children: [EmptyState('HTTP debugging is only available for the local service')]);
    return CapturePoll(method: 'capture.snapshot', arguments: {'query': _query, 'limit': _limit}, revision: _revision,
      builder: (context, snapshot, error, refresh) {
        final state = json(snapshot?['state']), page = json(snapshot?['page']);
        final active = state['active'] == true, entries = rows(page['items']);
        return CommonScaffold(
          title: context.tr('Requests'), isLoading: _busy,
          searchState: AppBarSearchState(onSearch: (query) => setState(() { _query = query; _limit = 50; })),
          onKeywordsUpdate: (words) => setState(() { _query = words.join(' '); _limit = 50; }),
          iconActions: [IconButtonData(glyph: AppGlyphs.refresh, tooltip: context.tr('Refresh'), onPressed: refresh)],
          menuItems: [
            CommonPopupMenuItem(glyph: AppGlyphs.sliders, label: context.tr('Capture settings'), onPressed: _busy || active ? null : () async {
              await openPage(context, RequestSettingsPage(initial: json(snapshot?['config'])));
              if (mounted) setState(() => _revision++);
            }),
            CommonPopupMenuItem(glyph: AppGlyphs.lock, label: context.tr('CA certificate'), onPressed: _busy ? null : () async {
              await openPage(context, CaptureCertificatePage(initial: json(snapshot?['ca']), active: active));
              if (mounted) setState(() => _revision++);
            }),
            CommonPopupMenuItem(glyph: AppGlyphs.arrowDown, label: context.tr('Export HAR'), onPressed: _busy || entries.isEmpty ? null : () => _act('capture.export', {'format': 'har', 'query': _query})),
            CommonPopupMenuItem(glyph: AppGlyphs.delete, label: context.tr('Clear requests'), onPressed: _busy ? null : () async {
              if (await confirm(context, 'Clear captured requests?') && mounted) await _act('capture.clear');
            }),
          ],
          body: ListView.builder(
            padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 24 + context.bottomInset),
            itemCount: entries.length + 2,
            itemBuilder: (context, index) {
              if (index == 0) return Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
                generateGroup([
                  ListItem.toggle(key: const ValueKey('capture-toggle'), title: Text(context.tr('Capture requests')),
                    subtitle: Text(context.tr(active ? 'Capturing new connections' : 'Capture stopped')), value: active,
                    onChanged: _busy || snapshot == null || !box.running || box.transitioning ? null : (_) => _act(active ? 'capture.stop' : 'capture.start')),
                ]),
                if (!box.running) Padding(padding: const EdgeInsets.all(12), child: Text(context.tr('Start the local service before capturing requests'))),
                if (snapshot != null) Padding(padding: const EdgeInsets.all(12), child: Text(
                  '${page['total'] ?? 0} ${context.tr('Requests')} · ${bytes(page['storedBytes'] as num? ?? 0)} / 32 MiB',
                  style: context.listSubtitleStyle,
                )),
                if ((state['skipped'] as num? ?? 0) > 0) Text('${context.tr('Connections skipped at capacity')}: ${state['skipped']}'),
                if ((state['blockedQUIC'] as num? ?? 0) > 0) Text('${context.tr('QUIC connections blocked')}: ${state['blockedQUIC']}'),
                if ((page['error'] as String? ?? '').isNotEmpty) Text(page['error'].toString(), style: TextStyle(color: context.colorScheme.error)),
                if (error != null) ErrorState(error, retry: refresh),
                if (snapshot == null && error == null) const Center(child: CircularProgressIndicator()),
                if (snapshot != null && entries.isEmpty) EmptyState(_query.isEmpty ? 'No captured requests' : 'No results',
                  message: _query.isEmpty ? 'Only new connections through the local service are captured. Reconnect the target app after changing capture settings.' : null),
              ]);
              if (index > entries.length) return (page['total'] as num? ?? 0) > entries.length
                ? TextButton(onPressed: () => setState(() => _limit = (_limit + 50).clamp(1, 300).toInt()), child: Text(context.tr('Show more')))
                : const SizedBox.shrink();
              final item = entries[index - 1];
              final styles = RecordTextStyles.of(context);
              return Padding(padding: const EdgeInsets.only(bottom: recordListGap), child: RecordListItem(
                key: ValueKey(item['id']), onTap: () => openPage(context, RequestDetailPage(id: item['id'].toString())),
                header: RecordHeader(children: [
                  RecordTimestamp(recordTime(item['startedAt'])),
                  RecordLabel(label: item['method']?.toString() ?? ''),
                  RecordLabel(label: (item['status'] as num? ?? 0) > 0 ? item['status'].toString() : context.tr(item['complete'] == true ? 'Error' : 'Active')),
                ]),
                body: Column(crossAxisAlignment: CrossAxisAlignment.start, spacing: 4, children: [
                  Text(item['url']?.toString() ?? '', maxLines: 2, overflow: TextOverflow.ellipsis, style: styles.title),
                  Text('${item['protocol'] ?? ''} · ${item['durationMs'] ?? 0} ms · ${bytes(item['bytes'] as num? ?? 0)}', style: styles.secondary),
                  if ((item['packages'] as List? ?? []).isNotEmpty) Text((item['packages'] as List).join(', '), maxLines: 1, overflow: TextOverflow.ellipsis, style: styles.secondary),
                  if ((item['error'] as String? ?? '').isNotEmpty) Text(item['error'].toString(), maxLines: 2, overflow: TextOverflow.ellipsis, style: styles.secondary.copyWith(color: context.colorScheme.error)),
                ]),
              ));
            },
          ),
        );
      });
  }
}

class RequestDetailPage extends StatefulWidget {
  const RequestDetailPage({super.key, required this.id});
  final String id;
  @override State<RequestDetailPage> createState() => _RequestDetailPageState();
}

class _RequestDetailPageState extends State<RequestDetailPage> {
  String _part = 'Response';
  int _previewLimit = 16384;
  bool _busy = false;
  @override Widget build(BuildContext context) {
    if (context.box.remote) return const BoxPage(title: 'Requests', children: [EmptyState('HTTP debugging is only available for the local service')]);
    return CapturePoll(method: 'capture.detail', arguments: {'id': widget.id}, builder: (context, detail, error, refresh) {
      final metadata = json(detail?['metadata']), request = json(detail?['request']);
      final response = _part == 'Response';
      final message = json(detail?[response ? 'response' : 'request']);
      final preview = json(detail?[response ? 'responsePreview' : 'requestPreview']);
      final text = preview['text']?.toString() ?? '';
      return BoxPage(title: 'Request details', loading: _busy, actions: [
        IconButtonData(glyph: AppGlyphs.copy, tooltip: context.tr('Copy URL'), onPressed: detail == null ? null : () async {
          await Clipboard.setData(ClipboardData(text: request['url']?.toString() ?? ''));
        }),
        IconButtonData(glyph: AppGlyphs.arrowDown, tooltip: context.tr('Export cURL'), onPressed: detail == null || _busy || request['bodyComplete'] != true || request['bodyTruncated'] == true || request['headersTruncated'] == true || request['method'] == 'TLS' ? null : () async {
          setState(() => _busy = true);
          await context.box.act('capture.export', {'format': 'curl', 'id': widget.id});
          if (mounted) setState(() => _busy = false);
        }),
      ], children: [
        if (error != null) ErrorState(error, retry: refresh),
        if (detail == null && error == null) const Center(child: CircularProgressIndicator()),
        if (detail != null) ...[
          DetailRows({'URL': request['url'], 'Method': request['method'], 'Created': localDate(detail['startedAt']),
            'Duration': '${detail['durationMs']} ms', 'Status': json(detail['response'])['status'],
            'Protocol': request['protocol'], 'Outbound': metadata['outbound'], 'Rule matched': metadata['rule'],
            'App': (metadata['packages'] as List? ?? []).join(', '), 'UID': metadata['uid'],
            'Source': metadata['source'], 'Destination': metadata['destination'],
            if (metadata['originalDestination'] != null) 'Original destination': metadata['originalDestination'],
            if ((detail['error'] as String? ?? '').isNotEmpty) 'Error': detail['error'],
            if ((detail['note'] as String? ?? '').isNotEmpty) 'Note': detail['note']}),
          ListItem.options(title: Text(context.tr('Message')), value: _part, options: const ['Request', 'Response'],
            textBuilder: context.tr, onChanged: (value) => setState(() { _part = value; _previewLimit = 16384; })),
          generateSection(title: context.tr('Headers'), items: [
            for (final header in rows(message['headers'])) ListItem(title: Text(header['name'].toString()), subtitle: SelectableText(header['value'].toString())),
          ]),
          if (message['headersTruncated'] == true) Text(context.tr('Headers truncated')),
          Padding(padding: const EdgeInsets.symmetric(vertical: 12), child: Text(
            '${context.tr('Body')} · ${bytes(message['capturedBytes'] as num? ?? 0)} / ${bytes(message['bodyBytes'] as num? ?? 0)}'
            '${preview['encoding'] == 'base64' ? ' · Base64' : ''}', style: context.listTitleStyle,
          )),
          if (message['bodyTruncated'] == true || preview['truncated'] == true) Text(context.tr('Only a limited body preview was saved or decoded.')),
          if (message['bodyComplete'] != true) Text(context.tr('Body stream is incomplete')),
          SelectableText(text.length > _previewLimit ? text.substring(0, _previewLimit) : text,
            style: const TextStyle(fontFamily: 'JetBrainsMono', fontSize: 12)),
          if (text.length > _previewLimit) TextButton(onPressed: () => setState(() => _previewLimit += 16384), child: Text(context.tr('Show more'))),
        ],
      ]);
    });
  }
}
