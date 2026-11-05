import 'package:flutter/services.dart';
import '../widgets.dart';
import '../components/record.dart';
import '../components/data_subscription.dart';
import '../common/connection_list.dart';

class ConnectionsPage extends StatefulWidget {
  const ConnectionsPage({super.key});
  @override State<ConnectionsPage> createState() => _ConnectionsPageState();
}
class _ConnectionsPageState extends State<ConnectionsPage> {
  final _list = ConnectionList();
  late final _keywords = RecordKeywords()..addListener(_keywordsChanged);
  String _query = '', _filter = 'Active', _sort = 'Speed';
  void _keywordsChanged() => setState(() {});
  @override void dispose() { _keywords.dispose(); super.dispose(); }
  Future<void> _closeAll() async {
    final box = context.box;
    if (await confirm(context, 'Close all active connections?', action: 'Close')) await box.act('connections.closeAll');
  }
  @override Widget build(BuildContext context) {
    final box = context.watchBox(BoxAspect.connections);
    final query = _keywords.queryWith(_query);
    final list = _list.select(box.connections, query: query, filter: _filter, sort: _sort);
    return BoxDataSubscription(data: BoxData.connections, child: Scaffold(
      title: context.tr('Connections'),
      keywords: _keywords,
      searchState: AppBarSearchState(onSearch: (query) => setState(() => _query = query)),
      iconActions: [IconButtonData(glyph: AppGlyphs.clearAll, tooltip: context.tr('Close all connections'), onPressed: box.running ? _closeAll : null)],
      menuItems: [
        for (final filter in ['Active', 'Closed', 'All'])
          PopupMenuItem(label: context.tr(filter), checked: filter == _filter,
            section: filter == 'Active' ? PopupMenuSection(title: context.tr('Connections')) : null,
            onPressed: () => setState(() => _filter = filter)),
        for (final sort in ['Speed', 'Created', 'Upload', 'Download'])
          PopupMenuItem(glyph: AppGlyphs.sort, label: context.tr(sort), checked: sort == _sort, onPressed: () => setState(() => _sort = sort)),
      ],
      body: NullStatusSwitcher(
        isEmpty: list.isEmpty, isSearching: query.trim().isNotEmpty,
        nullStatus: NullStatus(label: context.tr('No connections'), illustration: NullStatusIllustration.connections),
        child: ListView.separated(
            padding: EdgeInsets.only(top: context.contentTopPadding, bottom: 16 + context.bottomInset),
            itemCount: list.length,
            separatorBuilder: (_, _) => const SizedBox(height: recordListGap),
            itemBuilder: (context, index) {
              final connection = list[index];
              return ConnectionRecord(key: ValueKey(connection['id']), connection: connection, onKeyword: _keywords.add);
            },
          ),
      ),
    ));
  }
}

class ConnectionRecord extends StatelessWidget {
  const ConnectionRecord({super.key, required this.connection, this.onKeyword});
  final Json connection;
  final ValueChanged<String>? onKeyword;
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
          RecordLabel(label: c['network'].toString().toUpperCase(), onPressed: () => onKeyword?.call(c['network'].toString())),
          if (closed) RecordLabel(label: context.tr('Closed')),
        ],
        trailing: !closed && box.running ? Tooltip(
          message: context.tr('Close this connection'), child: Semantics(button: true, child: PressRegion(
            shape: AppShape.circle, onTap: () => box.act('connections.close', {'id': c['id']}),
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
    return BoxDataSubscription(data: BoxData.connections, child: BoxPage(
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
    ));
  }
}
