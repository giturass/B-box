import 'package:super_sliver_list/super_sliver_list.dart';
import '../widgets.dart';
import '../components/record.dart';

class LogsPage extends StatefulWidget {
  const LogsPage({super.key});
  @override State<LogsPage> createState() => _LogsPageState();
}

class _LogsPageState extends State<LogsPage> {
  String _query = '', _level = 'Default';
  static const _priorities = {
    'panic': 0, 'fatal': 1, 'error': 2, 'warn': 3, 'warning': 3,
    'info': 4, 'debug': 5, 'trace': 6,
  };
  List<Json>? _paused;
  late final _scroll = ScrollController(initialScrollOffset: double.maxFinite);
  late final _physics = FollowEndScrollPhysics(isFollowing: () => _paused == null);
  @override void dispose() { _scroll.dispose(); super.dispose(); }

  void _pause() {
    if (_paused == null) setState(() => _paused = List.of(context.box.logs));
  }

  void _resume() {
    if (_paused != null) setState(() => _paused = null);
  }

  bool _matchesLevel(Json item, String? defaultLevel) {
    final level = item['level'].toString().toLowerCase();
    if (_level == 'Default') {
      final threshold = _priorities[defaultLevel?.toLowerCase()];
      // Before the core reports its level, keep received records available.
      if (threshold == null) return true;
      final priority = _priorities[level];
      return priority != null && priority <= threshold;
    }
    return _level == 'All' || level == _level.toLowerCase();
  }

  @override Widget build(BuildContext context) {
    final box = context.box;
    final words = _query.toLowerCase().trim().split(RegExp(r'\s+'));
    final logs = (_paused ?? box.logs).where((item) =>
      _matchesLevel(item, box.defaultLogLevel) &&
      words.every(([item['level'], item['message']].join(' ').toLowerCase()).contains),
    ).toList();
    return CommonScaffold(
      title: context.tr('Logs'),
      onKeywordsUpdate: (words) => setState(() => _query = words.join(' ')),
      searchState: AppBarSearchState(onSearch: (query) => setState(() => _query = query)),
      iconActions: [IconButtonData(glyph: AppGlyphs.export, tooltip: context.tr('Export'), onPressed: () => box.act('document.save', {
        'name': 'B-box.log', 'content': logs.map((line) => '${recordTime(line['receivedAt'])} [${line['level']}] ${line['message']}').join('\n'),
      }))],
      menuItems: [
        CommonPopupMenuItem(glyph: _paused == null ? AppGlyphs.playPause(1) : AppGlyphs.playPause(0),
          label: context.tr(_paused == null ? 'Pause' : 'Resume'), onPressed: _paused == null ? _pause : _resume),
        CommonPopupMenuItem(glyph: AppGlyphs.delete, label: context.tr('Clear'), onPressed: () async {
          if (await box.act('logs.clear') && mounted) setState(() => _paused = null);
        }),
        for (final level in ['Default', 'All', 'Trace', 'Debug', 'Info', 'Warn', 'Error', 'Fatal', 'Panic'])
          CommonPopupMenuItem(label: level == 'Default'
              ? '${context.tr('Follow configuration')}${box.defaultLogLevel == null ? '' : ' (${context.tr(box.defaultLogLevel!)})'}'
              : context.tr(level), checked: _level == level,
            section: level == 'Default' ? CommonPopupMenuSection(title: context.tr('Log level')) : null,
            onPressed: () => setState(() => _level = level)),
      ],
      body: NullStatusSwitcher(
        isEmpty: logs.isEmpty, isSearching: _query.trim().isNotEmpty || !['Default', 'All'].contains(_level),
        nullStatus: NullStatus(label: context.tr('No logs'), illustration: NullStatusIllustration.logs),
        child: logs.isEmpty ? const SizedBox.shrink() : Align(
          alignment: Alignment.topCenter,
          child: FloatingScrollbar(
            controller: _scroll,
            hintBuilder: (fraction) => recordTime(logs[(fraction * (logs.length - 1)).round()]['receivedAt']),
            child: ScrollToEndBox(
              controller: _scroll, enable: _paused == null,
              onCancelToEnd: _pause, onResumeToEnd: _resume,
              child: SuperListView.separated(
                controller: _scroll, physics: _physics, reverse: true, shrinkWrap: true,
                padding: EdgeInsets.only(top: context.contentTopPadding, bottom: 16 + context.bottomInset),
                extentEstimation: recordListExtentEstimation,
                itemCount: logs.length,
                separatorBuilder: (_, _) => const SizedBox(height: recordListGap),
                itemBuilder: (context, index) {
                  final log = logs[index];
                  final level = log['level'].toString().toLowerCase();
                  final tone = ['error', 'fatal', 'panic'].contains(level) ? RecordTone.error
                      : ['warn', 'warning'].contains(level) ? RecordTone.warning : RecordTone.neutral;
                  return RecordListItem(
                    key: ValueKey(log['id']), onTap: () {},
                    header: RecordHeader(children: [
                      if ((log['receivedAt'] as num? ?? 0) > 0) RecordTimestamp(recordTime(log['receivedAt'])),
                      RecordLabel(label: level, tone: tone, onPressed: () => context.commonScaffoldState?.addKeyword(level)),
                    ]),
                    body: SelectableText(log['message'].toString(), style: RecordTextStyles.of(context).primary),
                  );
                },
              ),
            ),
          ),
        ),
      ),
    );
  }
}
