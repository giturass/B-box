import 'package:flutter/services.dart';
import '../widgets.dart';

class ConnectionsPage extends StatefulWidget {
  const ConnectionsPage({super.key});
  @override State<ConnectionsPage> createState() => _ConnectionsPageState();
}
class _ConnectionsPageState extends State<ConnectionsPage> {
  BoxController? _box;
  String _query = '', _filter = 'Active', _sort = 'Created';
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
    final box = context.box;
    final list = box.connections.where((connection) {
      final closed = (connection['closedAt'] as num? ?? 0) > 0;
      if (_filter == 'Active' && closed || _filter == 'Closed' && !closed) return false;
      final search = [
        connection['destination'], connection['domain'], connection['outbound'],
        connection['network'], connection['rule'], connection['source'], connection['process'],
      ].join(' ').toLowerCase();
      return _query.toLowerCase().split(' ').every(search.contains);
    }).toList();
    final key = {'Created': 'createdAt', 'Upload': 'uploadTotal', 'Download': 'downloadTotal'}[_sort]!;
    list.sort((a, b) => (b[key] as num? ?? 0).compareTo(a[key] as num? ?? 0));
    return CommonScaffold(
      title: context.tr('Connections'),
      searchState: AppBarSearchState(onSearch: (query) => setState(() => _query = query), query: _query),
      iconActions: [IconButtonData(glyph: AppGlyphs.close, tooltip: context.tr('Close all connections'), onPressed: box.running ? _closeAll : null)],
      menuItems: [
        for (final sort in ['Created', 'Upload', 'Download'])
          CommonPopupMenuItem(glyph: AppGlyphs.sort, label: context.tr(sort), checked: sort == _sort, onPressed: () => setState(() => _sort = sort)),
      ],
      body: Padding(
        padding: EdgeInsets.only(top: context.contentTopPadding),
        child: Column(children: [
          Padding(padding: const EdgeInsets.symmetric(horizontal: 16), child: SegmentedControl<String>(
            segments: [for (final filter in ['Active', 'Closed', 'All']) (filter, context.tr(filter))],
            value: _filter, onChanged: (value) => setState(() => _filter = value),
          )),
          const SizedBox(height: 12),
          Expanded(child: list.isEmpty ? const Center(child: EmptyState('No connections', glyph: AppGlyphs.connections)) : ListView.separated(
            padding: EdgeInsets.fromLTRB(16, 0, 16, 24 + context.bottomInset),
            itemCount: list.length,
            separatorBuilder: (_, _) => const SizedBox(height: 10),
            itemBuilder: (context, index) {
              final connection = list[index];
              return CommonCard(
                radius: AppCorner.xl,
                onPressed: () => openPage(context, ConnectionDetail(initial: connection)),
                child: Padding(padding: const EdgeInsets.all(16), child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                  Row(children: [
                    Expanded(child: Text(connection['displayDestination'].toString(), maxLines: 1, overflow: TextOverflow.ellipsis, style: AppTextStyle.headline.of(context))),
                    const SizedBox(width: 8),
                    Text(connection['network'].toString().toUpperCase(), style: context.listCaptionStyle),
                  ]),
                  const SizedBox(height: 8),
                  Text(connection['outbound'].toString(), maxLines: 1, overflow: TextOverflow.ellipsis, style: context.listSubtitleStyle),
                  const SizedBox(height: 8),
                  Row(children: [
                    const GlyphIcon(AppGlyphs.arrowUp, size: 14),
                    Text(bytes(connection['uploadTotal'] as num? ?? 0), style: context.listCaptionStyle),
                    const SizedBox(width: 18),
                    const GlyphIcon(AppGlyphs.arrowDown, size: 14),
                    Text(bytes(connection['downloadTotal'] as num? ?? 0), style: context.listCaptionStyle),
                    const Spacer(),
                    if ((connection['closedAt'] as num? ?? 0) > 0) Text(context.tr('Closed'), style: context.listCaptionStyle),
                  ]),
                ])),
              );
            },
          )),
        ]),
      ),
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
    final box = context.box;
    for (final connection in box.connections) {
      if (connection['id'] == widget.initial['id']) { _cached = connection; break; }
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
          onPressed: box.running && (c['closedAt'] as num? ?? 0) == 0 ? () => box.act('connections.close', {'id': c['id']}) : null),
      ],
      children: [DetailRows(values)],
    );
  }
}
