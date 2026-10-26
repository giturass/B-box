import '../widgets.dart';

class LogsPage extends StatefulWidget {
  const LogsPage({super.key});
  @override State<LogsPage> createState() => _LogsPageState();
}
class _LogsPageState extends State<LogsPage> {
  String _query = '', _level = 'All';
  List<Json>? _paused;
  final _scroll = ScrollController();
  int _length = 0;
  bool _follow = true;
  @override void initState() {
    super.initState();
    _scroll.addListener(() {
      if (_scroll.hasClients) _follow = _scroll.position.maxScrollExtent - _scroll.offset < 80;
    });
  }
  @override void dispose() { _scroll.dispose(); super.dispose(); }
  @override Widget build(BuildContext context) {
    final box = context.box;
    final source = _paused ?? box.logs;
    final logs = source.where((item) =>
      (_level == 'All' || item['level'].toString().toLowerCase() == _level.toLowerCase()) &&
      item['message'].toString().toLowerCase().contains(_query.toLowerCase()),
    ).toList();
    if (_follow && _paused == null && logs.length != _length) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted && _scroll.hasClients) _scroll.jumpTo(_scroll.position.maxScrollExtent);
      });
    }
    _length = logs.length;
    return CommonScaffold(
      title: context.tr('Logs'),
      searchState: AppBarSearchState(onSearch: (query) => setState(() => _query = query)),
      iconActions: [
        IconButtonData(glyph: _paused == null ? AppGlyphs.playPause(1) : AppGlyphs.playPause(0),
          tooltip: context.tr(_paused == null ? 'Pause' : 'Resume'), onPressed: () => setState(() => _paused = _paused == null ? List.of(box.logs) : null)),
        IconButtonData(glyph: AppGlyphs.delete, tooltip: context.tr('Clear'), onPressed: () async {
          if (await box.act('logs.clear') && mounted) setState(() => _paused = null);
        }),
        IconButtonData(glyph: AppGlyphs.upload, tooltip: context.tr('Export'), onPressed: () => box.act('document.save', {
          'name': 'B-box.log', 'content': logs.map((line) => '[' + line['level'].toString() + '] ' + line['message'].toString()).join('\n'),
        })),
      ],
      menuItems: [for (final level in ['All', 'Trace', 'Debug', 'Info', 'Warn', 'Error', 'Fatal', 'Panic'])
        CommonPopupMenuItem(label: level, checked: _level == level, onPressed: () => setState(() => _level = level)),
      ],
      body: logs.isEmpty ? const Center(child: EmptyState('No logs', glyph: AppGlyphs.logs)) : ListView.builder(
        controller: _scroll,
        padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 24 + context.bottomInset),
        itemCount: logs.length,
        itemBuilder: (context, index) {
          final log = logs[index];
          final level = log['level'].toString();
          final color = ['Error', 'Fatal', 'Panic'].contains(level) ? context.colorScheme.error
              : level == 'Warn' ? context.colorScheme.warning : context.colorScheme.primary;
          return Padding(
            padding: const EdgeInsets.symmetric(vertical: 8),
            child: Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
              SizedBox(width: 44, child: Text(level.toUpperCase(), style: TextStyle(fontSize: 10, fontWeight: FontWeight.w600, color: color))),
              const SizedBox(width: 8),
              Expanded(child: SelectableText(log['message'].toString(), style: const TextStyle(fontFamily: 'JetBrainsMono', fontSize: 12, height: 1.5))),
            ]),
          );
        },
      ),
    );
  }
}
