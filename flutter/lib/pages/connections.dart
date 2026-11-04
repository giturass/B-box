import 'package:flutter/services.dart';
import '../widgets.dart';
import '../components/record.dart';

class ConnectionsPage extends StatefulWidget {
  const ConnectionsPage({super.key});
  @override State<ConnectionsPage> createState() => _ConnectionsPageState();
}
class _ConnectionsPageState extends State<ConnectionsPage> {
  BoxController? _box;
  String _query = '', _filter = 'Active', _sort = 'Speed';
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
  @override Widget build(BuildContext context) {
    final box = context.watchBox(BoxAspect.connections);
    final list = box.connections.where((connection) {
      final closed = (connection['closedAt'] as num? ?? 0) > 0;
      if (_filter == 'Active' && closed || _filter == 'Closed' && !closed) return false;
      final search = [
        connection['destination'], connection['domain'], connection['outbound'],
        connection['network'], connection['rule'], connection['source'], connection['process'],
      ].join(' ').toLowerCase();
      return _query.toLowerCase().split(' ').every(search.contains);
    }).toList();
    final key = {'Created': 'createdAt', 'Upload': 'uploadTotal', 'Download': 'downloadTotal'}[_sort];
    num rank(Json c) => key == null ? (c['upload'] as num? ?? 0) + (c['download'] as num? ?? 0) : c[key] as num? ?? 0;
    list.sort((a, b) => rank(b).compareTo(rank(a)));
    return CommonScaffold(
      title: context.tr('Connections'),
      onKeywordsUpdate: (words) => setState(() => _query = words.join(' ')),
      searchState: AppBarSearchState(onSearch: (query) => setState(() => _query = query)),
      iconActions: [IconButtonData(glyph: AppGlyphs.clearAll, tooltip: context.tr('Close all connections'), onPressed: box.running ? _closeAll : null)],
      menuItems: [
        for (final filter in ['Active', 'Closed', 'All'])
          CommonPopupMenuItem(label: context.tr(filter), checked: filter == _filter,
            section: filter == 'Active' ? CommonPopupMenuSection(title: context.tr('Connections')) : null,
            onPressed: () => setState(() => _filter = filter)),
        for (final sort in ['Speed', 'Created', 'Upload', 'Download'])
          CommonPopupMenuItem(glyph: AppGlyphs.sort, label: context.tr(sort), checked: sort == _sort, onPressed: () => setState(() => _sort = sort)),
      ],
      body: NullStatusSwitcher(
        isEmpty: list.isEmpty, isSearching: _query.trim().isNotEmpty,
        nullStatus: NullStatus(label: context.tr('No connections'), illustration: NullStatusIllustration.connections),
        child: ListView.separated(
            padding: EdgeInsets.only(top: context.contentTopPadding, bottom: 16 + context.bottomInset),
            itemCount: list.length,
            separatorBuilder: (_, _) => const SizedBox(height: recordListGap),
            itemBuilder: (context, index) {
              final connection = list[index];
              return ConnectionRecord(connection: connection);
            },
          ),
      ),
    );
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
