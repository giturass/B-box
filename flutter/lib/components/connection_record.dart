import '../widgets.dart';
import 'record.dart';

class ConnectionRecord extends StatelessWidget {
  const ConnectionRecord({super.key, required this.connection, required this.onOpen, this.onClose, this.onKeyword});
  final Json connection;
  final VoidCallback onOpen;
  final VoidCallback? onClose;
  final ValueChanged<String>? onKeyword;
  @override Widget build(BuildContext context) {
    final c = connection;
    final styles = RecordTextStyles.of(context);
    final closed = (c['closedAt'] as num? ?? 0) > 0;
    final packages = json(c['process'])['packages'] as List? ?? const [];
    return RecordListItem(
      onTap: onOpen,
      header: RecordHeader(
        children: [
          RecordTimestamp(recordTime(c['createdAt'])),
          RecordLabel(label: c['network'].toString().toUpperCase(), onPressed: () => onKeyword?.call(c['network'].toString())),
          RecordLabel(label: context.tr(closed ? 'Closed' : 'Active')),
        ],
        trailing: onClose == null ? null : Tooltip(
          message: context.tr('Close this connection'), child: Semantics(button: true, child: PressRegion(
            shape: AppShape.circle, onTap: onClose,
            child: SizedBox.square(dimension: 28, child: Center(child: GlyphIcon(AppGlyphs.block, size: 16, color: context.colorScheme.onSurfaceVariant))),
          )),
        ),
      ),
      body: Column(crossAxisAlignment: CrossAxisAlignment.start, spacing: 4, children: [
        Text(c['displayDestination']?.toString() ?? c['destination']?.toString() ?? '—', style: styles.title, maxLines: 2, overflow: TextOverflow.ellipsis),
        Wrap(crossAxisAlignment: WrapCrossAlignment.center, spacing: 6, children: [
          if ((c['rule'] as String? ?? '').isNotEmpty) ...[Text(c['rule'].toString(), style: styles.secondary), const RecordArrow()],
          Text(c['outbound']?.toString() ?? '—', style: AppTextStyle.footnote.of(context, emphasized: true).copyWith(color: context.colorScheme.primary)),
        ]),
        Text(packages.isNotEmpty ? packages.join(', ') : c['source']?.toString() ?? '—', style: styles.secondary, maxLines: 1, overflow: TextOverflow.ellipsis),
        Wrap(spacing: 16, runSpacing: 4, children: [for (final upload in [true, false]) Row(mainAxisSize: MainAxisSize.min, spacing: 4, children: [
          GlyphIcon(upload ? AppGlyphs.arrowUp : AppGlyphs.arrowDown, size: 12, color: context.colorScheme.onSurfaceVariant),
          Text('${bytes(c[upload ? 'uploadTotal' : 'downloadTotal'] as num? ?? 0)} · ${bytes(c[upload ? 'upload' : 'download'] as num? ?? 0)}/s', style: styles.secondary.toTabular),
        ])]),
      ]),
    );
  }
}

Map<String, dynamic> connectionValues(Json c) {
  final packages = json(c['process'])['packages'] as List? ?? const [];
  return {
    'Connection ID': c['id'],
    'Destination': c['displayDestination'] ?? c['destination'], 'Source': c['source'], 'Network': c['network'],
    'Protocol': c['protocol'], 'Inbound': c['inbound'], 'Outbound': c['outbound'],
    'Rule matched': c['rule'], 'Chain': (c['chain'] as List? ?? []).join(' → '),
    'Created': localDate(c['createdAt']),
    if ((c['closedAt'] as num? ?? 0) > 0) 'Closed': localDate(c['closedAt']),
    'Upload': bytes(c['uploadTotal'] as num? ?? 0), 'Download': bytes(c['downloadTotal'] as num? ?? 0),
    'App': packages.isEmpty ? null : packages.join(', '),
  };
}

class ConnectionInfo extends StatelessWidget {
  const ConnectionInfo({super.key, required this.connection, required this.present});
  final Json connection;
  final bool present;
  @override Widget build(BuildContext context) => generateSection(title: context.tr('Connection'), items: [
    DetailRow.text(title: context.tr('Status'), value: context.tr(!present ? 'No longer in recent connections' :
      (connection['closedAt'] as num? ?? 0) > 0 ? 'Closed' : 'Active')),
    for (final value in connectionValues(connection).entries) DetailRow.text(title: context.tr(value.key),
      value: value.value?.toString() ?? '—', stacked: const ['Connection ID', 'App'].contains(value.key)),
  ], footer: present ? null : context.tr('Last observed connection information. Captured requests are retained separately.'));
}
