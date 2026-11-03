import 'package:flutter/services.dart';
import '../widgets.dart';
import '../common/http_message.dart';
import '../components/capture_poll.dart';
import '../components/captured_body.dart';
import '../components/record.dart';

class RequestRecord extends StatelessWidget {
  const RequestRecord({super.key, required this.request});
  final Json request;

  @override Widget build(BuildContext context) {
    final item = request, styles = RecordTextStyles.of(context);
    return RecordListItem(
      onTap: () => openPage(context, RequestDetailPage(id: item['id'].toString())),
      header: RecordHeader(children: [
        RecordTimestamp(recordTime(item['startedAt'])),
        RecordLabel(label: context.tr('Request')),
        RecordLabel(label: item['method']?.toString() ?? ''),
        RecordLabel(label: item['domainOnly'] == true ? context.tr('Domain only') : (item['status'] as num? ?? 0) > 0 ? item['status'].toString() : context.tr(item['complete'] != true ? 'Active' : item['protocol'] == 'TLS/TCP' && (item['error'] as String? ?? '').isEmpty ? 'Complete' : 'Error')),
      ]),
      body: Column(crossAxisAlignment: CrossAxisAlignment.start, spacing: 4, children: [
        Text(item['url']?.toString() ?? '', maxLines: 2, overflow: TextOverflow.ellipsis, style: styles.title),
        Text('${item['protocol'] ?? ''} · ${item['durationMs'] ?? 0} ms · ${bytes(item['bytes'] as num? ?? 0)}', style: styles.secondary),
        if ((item['packages'] as List? ?? []).isNotEmpty) Text((item['packages'] as List).join(', '), maxLines: 1, overflow: TextOverflow.ellipsis, style: styles.secondary),
        if ((item['error'] as String? ?? '').isNotEmpty) Text(item['error'].toString(), maxLines: 2, overflow: TextOverflow.ellipsis, style: styles.secondary.copyWith(color: context.colorScheme.error)),
      ]),
    );
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
      final socket = json(metadata['socket']);
      final tls = json(metadata['tls']);
      final response = _part == 'Response';
      final message = json(detail?[response ? 'response' : 'request']);
      final preview = json(detail?[response ? 'responsePreview' : 'requestPreview']);
      final values = <String, dynamic>{
        'URL': request['url'], 'Method': request['method'], 'Created': localDate(detail?['startedAt']),
        if (detail?['originalUrl'] != null) 'Original URL': detail?['originalUrl'],
        'Duration': '${detail?['durationMs'] ?? 0} ms',
        if (metadata['rawTCP'] != true) 'Status': json(detail?['response'])['status'],
        'Protocol': request['protocol'],
        (detail?['originalUrl'] != null ? 'Original outbound' : 'Outbound'): metadata['outbound'],
        (detail?['originalUrl'] != null ? 'Original rule matched' : 'Rule matched'): metadata['rule'],
        'App': (metadata['packages'] as List? ?? []).join(', '), 'UID': metadata['uid'],
        if (metadata['captureSource'] != null) 'Capture source': metadata['captureSource'],
        if (metadata['pid'] != null) 'PID': metadata['pid'],
        if ((metadata['tlsAdapter'] as String? ?? '').isNotEmpty) 'TLS implementation': metadata['tlsAdapter'],
        if (tls.isNotEmpty) 'TLS server name': tls['serverName'],
        if (tls.isNotEmpty) 'Client TLS': tls['clientVersion'],
        if (tls.isNotEmpty) 'Upstream TLS': tls['upstreamVersion'],
        if ((tls['upstreamServerName'] as String? ?? '').isNotEmpty) 'Upstream TLS server name': tls['upstreamServerName'],
        if (tls.isNotEmpty) 'ALPN': tls['alpn']?.toString().isNotEmpty == true ? tls['alpn'] : context.tr('Not negotiated'),
        if (tls.isNotEmpty) 'Upstream certificate SHA-256': tls['upstreamCertificate'],
        if (socket.isNotEmpty) 'Socket FD': socket['fd'],
        if (socket.isNotEmpty) 'Socket inode': socket['inode'],
        'Source': metadata['source'], 'Destination': metadata['destination'],
        if (metadata['originalDestination'] != null) 'Original destination': metadata['originalDestination'],
        if ((detail?['error'] as String? ?? '').isNotEmpty) 'Error': detail?['error'],
        if ((detail?['note'] as String? ?? '').isNotEmpty) 'Note': detail?['note'],
      };
      return BoxPage(title: 'Request details', loading: _busy, actions: [
        IconButtonData(glyph: AppGlyphs.copy, tooltip: context.tr('Copy URL'), onPressed: detail == null ? null : () => _copy(request['url']?.toString() ?? '')),
        IconButtonData(glyph: AppGlyphs.arrowDown, tooltip: context.tr('Export cURL'), onPressed: detail == null || _busy || request['bodyComplete'] != true || request['bodyTruncated'] == true || request['headersTruncated'] == true || rows(request['trailers']).isNotEmpty || request['trailersTruncated'] == true || request['method'] == 'TLS' || metadata['rawTCP'] == true ? null : () async {
          setState(() => _busy = true);
          await context.box.act('capture.export', {'format': 'curl', 'id': widget.id});
          if (mounted) setState(() => _busy = false);
        }),
      ], menuItems: [
        for (final part in ['Request', 'Response', if (metadata['rawTCP'] != true) 'Cookies']) CommonPopupMenuItem(label: context.tr(part),
          section: part == 'Request' ? CommonPopupMenuSection(title: context.tr('Message type')) : null,
          checked: part == _part, onPressed: () => setState(() => _part = part)),
      ], children: [
        if (error != null) ErrorState(error, retry: refresh),
        if (detail == null && error == null) const Center(child: CircularProgressIndicator()),
        if (detail != null) ...[
          generateGroup([for (final value in values.entries) DetailRow.text(title: context.tr(value.key),
            value: value.value?.toString() ?? '—', stacked: const ['URL', 'Original URL', 'App', 'Error', 'Note', 'Upstream certificate SHA-256'].contains(value.key))]),
          if (_part == 'Cookies') ...[
            _cookies(request, false),
            _cookies(json(detail['response']), true),
          ] else ...[
            if (metadata['rawTCP'] != true) _headers(message, response),
            CapturedBody(key: ValueKey('body-$_part'), message: message, preview: preview),
            if (rows(message['trailers']).isNotEmpty) generateSection(title: context.tr('Trailers'), items: [
              for (final trailer in rows(message['trailers'])) DetailRow.text(title: trailer['name']?.toString() ?? '', value: trailer['value']?.toString() ?? '', stacked: true),
            ], footer: message['trailersTruncated'] == true ? context.tr('Headers truncated') : null),
          ],
        ],
      ]);
    });
  }
}
