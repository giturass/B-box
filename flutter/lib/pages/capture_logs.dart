import 'package:flutter/services.dart';
import '../widgets.dart';
import '../components/capture_poll.dart';
import '../components/record.dart';

class CaptureLogsPage extends StatefulWidget {
  const CaptureLogsPage({super.key});
  @override State<CaptureLogsPage> createState() => _CaptureLogsPageState();
}

class _CaptureLogsPageState extends State<CaptureLogsPage> {
  String _query = '';
  bool _busy = false;
  bool _statistics = false;
  int _revision = 0;

  String _line(Json log) {
    final time = DateTime.fromMillisecondsSinceEpoch((log['receivedAt'] as num? ?? 0).toInt()).toUtc().toIso8601String();
    return '$time [${log['level']}] ${log['message']}';
  }

  Future<void> _copy(String text) async {
    await Clipboard.setData(ClipboardData(text: text));
    if (mounted) context.box.messages.add(context.tr('Copied'));
  }

  @override Widget build(BuildContext context) {
    if (context.box.remote) return const BoxPage(title: 'eBPF logs', children: [EmptyState('HTTP debugging is only available for the local service')]);
    return CapturePoll(method: 'capture.logs', arguments: const {}, revision: _revision,
      builder: (context, snapshot, error, refresh) {
        final state = json(snapshot?['state']);
        final wire = json(state['wire']);
        final active = state['active'] == true && state['backend'] == 'eBPF';
        final words = _query.toLowerCase().trim().split(RegExp(r'\s+'));
        final logs = rows(snapshot?['logs']).where((log) => words.every(
          '${log['level']} ${log['message']}'.toLowerCase().contains)).toList()
          ..sort((a, b) {
            final order = (a['receivedAt'] as num? ?? 0).compareTo(b['receivedAt'] as num? ?? 0);
            return order != 0 ? order : (a['id'] as num? ?? 0).compareTo(b['id'] as num? ?? 0);
          });
        final text = logs.map(_line).join('\n');
        return CommonScaffold(
          title: context.tr('eBPF logs'), isLoading: _busy,
          searchState: AppBarSearchState(onSearch: (query) => setState(() => _query = query)),
          onKeywordsUpdate: (words) => setState(() => _query = words.join(' ')),
          iconActions: [IconButtonData(glyph: AppGlyphs.refresh, tooltip: context.tr('Refresh'), onPressed: refresh)],
          menuItems: [
            CommonPopupMenuItem(glyph: AppGlyphs.copy, label: context.tr('Copy logs'), onPressed: logs.isEmpty ? null : () => _copy(text)),
            CommonPopupMenuItem(glyph: AppGlyphs.export, label: context.tr('Export'), onPressed: logs.isEmpty ? null : () => context.box.act('document.save', {
              'name': 'B-box-ebpf.log', 'content': text,
            })),
            CommonPopupMenuItem(glyph: AppGlyphs.delete, label: context.tr('Clear'), onPressed: _busy ? null : () async {
              setState(() => _busy = true);
              await context.box.act('capture.logs.clear');
              if (mounted) setState(() { _busy = false; _revision++; });
            }),
          ],
          body: ListView.builder(
            padding: EdgeInsets.only(top: context.contentTopPadding, bottom: 24 + context.bottomInset),
            itemCount: logs.length + 1,
            itemBuilder: (context, index) {
              if (index == 0) return Padding(padding: const EdgeInsets.symmetric(horizontal: 16), child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch, spacing: 12, children: [
                  if (snapshot != null) generateGroup([
                    ListItem(title: Text(context.tr('Status')), subtitle: Text(context.tr(active ? 'Capturing new connections' : 'Capture stopped'))),
                    if (active) ListItem(title: Text('eBPF · ${state['libraries'] ?? 0} ${context.tr('TLS libraries attached')}'),
                      subtitle: Text(context.tr(state['scanning'] == true
                        ? 'Preparing capture for more applications. Reconnect the target app when preparation finishes.'
                        : 'Capture preparation finished. Reconnect the target app to capture new HTTPS connections.'))),
                    if ((state['skipped'] as num? ?? 0) > 0) DetailRow.text(title: context.tr('Connections skipped or interrupted'), value: state['skipped'].toString()),
                    if (state['backend'] == 'eBPF' || (wire['packets'] as num? ?? 0) > 0 || (wire['secrets'] as num? ?? 0) > 0)
                      ListItem(title: Text(context.tr('Capture statistics')),
                        subtitle: Text('TLS ${state['tlsOpened'] ?? 0} · ${context.tr('Plaintext')} ${state['plaintextConnections'] ?? 0} · ${context.tr('Parsed')} ${state['parsedRequests'] ?? 0}'),
                        onTap: () => setState(() => _statistics = !_statistics)),
                    if (_statistics) ...[
                      DetailRow.text(title: context.tr('TLS connections observed'), value: '${state['tlsOpened'] ?? 0}'),
                      DetailRow.text(title: context.tr('Go TLS connections observed'), value: '${state['goTlsOpened'] ?? 0}'),
                      DetailRow.text(title: context.tr('Connections with plaintext'), value: '${state['plaintextConnections'] ?? 0} · ${bytes(state['plaintextBytes'] as num? ?? 0)}'),
                      DetailRow.text(title: context.tr('Parsed HTTPS requests'), value: '${state['parsedRequests'] ?? 0}'),
                      DetailRow.text(title: context.tr('Requests excluded by filters'), value: '${state['filteredRequests'] ?? 0}'),
                      DetailRow.text(title: context.tr('HTTPS requests saved'), value: '${state['recordedRequests'] ?? 0}'),
                      DetailRow.text(title: context.tr('Connections with socket addresses'), value: '${state['socketConnections'] ?? 0}'),
                      DetailRow.text(title: context.tr('Saved network packets'), value: '${wire['packets'] ?? 0} · ${bytes(wire['bytes'] as num? ?? 0)} / 32 MiB'),
                      DetailRow.text(title: context.tr('Saved TLS keys'), value: '${wire['secrets'] ?? 0}'),
                    ],
                    if ((wire['error'] as String? ?? '').isNotEmpty) DetailRow.text(title: context.tr('Raw capture status'), value: wire['error'].toString(), stacked: true),
                    if ((state['error'] as String? ?? '').isNotEmpty) DetailRow.text(title: context.tr('Latest error'), value: state['error'].toString(), stacked: true),
                  ]),
                  ListFooter(text: context.tr('Recent logs are kept until the app process exits. Export logs to keep them.')),
                  if (error != null) ErrorState(error, retry: refresh),
                  if (snapshot == null && error == null) const Center(child: CircularProgressIndicator()),
                  if (snapshot != null && logs.isEmpty) EmptyState(_query.trim().isEmpty ? 'No eBPF logs' : 'No results'),
                ],
              ));
              final log = logs[logs.length - index];
              final level = log['level']?.toString() ?? 'info';
              return Padding(padding: const EdgeInsets.only(bottom: recordListGap), child: RecordListItem(
                key: ValueKey(log['id']), onTap: () => _copy(_line(log)),
                header: RecordHeader(children: [
                  RecordTimestamp(recordTime(log['receivedAt'])),
                  RecordLabel(label: level, tone: level == 'error' ? RecordTone.error : level == 'warn' ? RecordTone.warning : RecordTone.neutral),
                ]),
                body: SelectableText(log['message']?.toString() ?? '', style: RecordTextStyles.of(context).primary),
              ));
            },
          ),
        );
      });
  }
}
