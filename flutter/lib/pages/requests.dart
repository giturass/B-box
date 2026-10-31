import 'package:flutter/services.dart';
import '../widgets.dart';
import '../common/http_message.dart';
import '../components/capture_poll.dart';
import '../components/captured_body.dart';
import '../components/record.dart';
import 'request_settings.dart';
import 'capture_certificate.dart';
import 'capture_domains.dart';

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
              setState(() => _busy = true);
              try {
                final source = json(await context.box.call('capture.settings'));
                if (context.mounted) await openPage(context, RequestSettingsPage(initial: json(source['config']), source: source));
              } catch (error) { if (mounted) context.box.messages.add(BoxController.describe(error)); }
              finally { if (mounted) setState(() { _busy = false; _revision++; }); }
            }),
            CommonPopupMenuItem(glyph: AppGlyphs.lock, label: context.tr('CA certificate'), onPressed: _busy ? null : () async {
              await openPage(context, CaptureCertificatePage(initial: json(snapshot?['ca']), active: active));
              if (mounted) setState(() => _revision++);
            }),
            CommonPopupMenuItem(glyph: AppGlyphs.arrowDown, label: context.tr('Export HAR'), onPressed: _busy || entries.isEmpty ? null : () => _act('capture.export', {'format': 'har', 'query': _query})),
            CommonPopupMenuItem(glyph: AppGlyphs.copy, label: context.tr('Extract domains'), onPressed: _busy ? null : () => openPage(context, CaptureDomainsPage(query: _query))),
            CommonPopupMenuItem(glyph: AppGlyphs.delete, label: context.tr('Clear requests'), onPressed: _busy ? null : () async {
              if (await confirm(context, 'Clear captured requests?') && mounted) await _act('capture.clear');
            }),
          ],
          body: ListView.builder(
            padding: EdgeInsets.only(top: context.contentTopPadding, bottom: 24 + context.bottomInset),
            itemCount: entries.length + 2,
            itemBuilder: (context, index) {
              if (index == 0) return Padding(padding: const EdgeInsets.symmetric(horizontal: 16), child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
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
              ]));
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
                  RecordLabel(label: item['domainOnly'] == true ? context.tr('Domain only') : (item['status'] as num? ?? 0) > 0 ? item['status'].toString() : context.tr(item['complete'] == true ? 'Error' : 'Active')),
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
  bool _busy = false;

  Future<void> _copy(String text) async {
    await Clipboard.setData(ClipboardData(text: text));
    if (mounted) context.box.messages.add(context.tr('Copied'));
  }

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
    if (context.box.remote) return const BoxPage(title: 'Requests', children: [EmptyState('HTTP debugging is only available for the local service')]);
    return CapturePoll(method: 'capture.detail', arguments: {'id': widget.id}, builder: (context, detail, error, refresh) {
      final metadata = json(detail?['metadata']), request = json(detail?['request']);
      final response = _part == 'Response';
      final message = json(detail?[response ? 'response' : 'request']);
      final preview = json(detail?[response ? 'responsePreview' : 'requestPreview']);
      final values = <String, dynamic>{
        'URL': request['url'], 'Method': request['method'], 'Created': localDate(detail?['startedAt']),
        if (detail?['originalUrl'] != null) 'Original URL': detail?['originalUrl'],
        'Duration': '${detail?['durationMs'] ?? 0} ms', 'Status': json(detail?['response'])['status'],
        'Protocol': request['protocol'], 'Outbound': metadata['outbound'], 'Rule matched': metadata['rule'],
        'App': (metadata['packages'] as List? ?? []).join(', '), 'UID': metadata['uid'],
        'Source': metadata['source'], 'Destination': metadata['destination'],
        if (metadata['originalDestination'] != null) 'Original destination': metadata['originalDestination'],
        if ((detail?['error'] as String? ?? '').isNotEmpty) 'Error': detail?['error'],
        if ((detail?['note'] as String? ?? '').isNotEmpty) 'Note': detail?['note'],
      };
      return BoxPage(title: 'Request details', loading: _busy, actions: [
        IconButtonData(glyph: AppGlyphs.copy, tooltip: context.tr('Copy URL'), onPressed: detail == null ? null : () => _copy(request['url']?.toString() ?? '')),
        IconButtonData(glyph: AppGlyphs.arrowDown, tooltip: context.tr('Export cURL'), onPressed: detail == null || _busy || request['bodyComplete'] != true || request['bodyTruncated'] == true || request['headersTruncated'] == true || request['method'] == 'TLS' ? null : () async {
          setState(() => _busy = true);
          await context.box.act('capture.export', {'format': 'curl', 'id': widget.id});
          if (mounted) setState(() => _busy = false);
        }),
      ], menuItems: [
        for (final part in const ['Request', 'Response', 'Cookies']) CommonPopupMenuItem(label: context.tr(part),
          section: part == 'Request' ? CommonPopupMenuSection(title: context.tr('Message type')) : null,
          checked: part == _part, onPressed: () => setState(() => _part = part)),
      ], children: [
        if (error != null) ErrorState(error, retry: refresh),
        if (detail == null && error == null) const Center(child: CircularProgressIndicator()),
        if (detail != null) ...[
          generateGroup([for (final value in values.entries) DetailRow.text(title: context.tr(value.key),
            value: value.value?.toString() ?? '—', stacked: const ['URL', 'Original URL', 'App', 'Error', 'Note'].contains(value.key))]),
          if (_part == 'Cookies') ...[
            _cookies(request, false),
            _cookies(json(detail['response']), true),
          ] else ...[
            _headers(message, response),
            CapturedBody(key: ValueKey('body-$_part'), message: message, preview: preview),
          ],
        ],
      ]);
    });
  }
}
