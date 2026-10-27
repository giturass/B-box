import '../widgets.dart';
import '../components/group_tabs.dart';

class ProxiesPage extends StatefulWidget {
  const ProxiesPage({super.key});
  @override State<ProxiesPage> createState() => _ProxiesPageState();
}
class _ProxiesPageState extends State<ProxiesPage> {
  String _query = '';
  final _testing = <String>{};
  final _folded = <String>{};
  final _scrolls = <String, ScrollController>{};
  final _selectedOffsets = <String, double>{};
  String? _currentGroup;

  @override void dispose() {
    for (final scroll in _scrolls.values) { scroll.dispose(); }
    super.dispose();
  }

  Future<void> _locate() async {
    final scroll = _scrolls[_currentGroup], offset = _selectedOffsets[_currentGroup];
    if (scroll == null || !scroll.hasClients || offset == null) return;
    await scroll.animateTo(offset.clamp(scroll.position.minScrollExtent, scroll.position.maxScrollExtent),
      duration: context.motionDuration(const Duration(milliseconds: 300)), curve: Curves.easeOutCubic);
  }

  CommonPopupMenuItem _options(String label, Glyph glyph, String key, List<String> values, String fallback) => CommonPopupMenuItem(
    glyph: glyph, label: context.tr(label), subItems: [for (final value in values)
      CommonPopupMenuItem(label: context.tr(value), checked: (context.box.preferences[key] ?? fallback) == value,
        onPressed: () => context.box.setPreference(key, value)),
    ],
  );

  Future<void> _test(String tag) async {
    if (_testing.contains(tag)) return;
    setState(() => _testing.add(tag));
    await context.box.act('groups.test', {'tag': tag});
    if (mounted) setState(() => _testing.remove(tag));
  }

  Future<void> _select(Json group, Json node) async {
    final box = context.box;
    if (!box.running || group['selectable'] != true || node['tag'] == group['selected']) return;
    if (await box.act('groups.select', {'group': group['tag'], 'tag': node['tag']})) {
      if (box.preferences['closeConnectionsOnSelect'] == true) await box.act('connections.closeAll');
    }
  }

  List<Json> _nodes(Json group) {
    final words = _query.toLowerCase().trim().split(RegExp(r'\s+'));
    final nodes = rows(group['items']).where((node) => words.every(node['tag'].toString().toLowerCase().contains) &&
      !(context.box.preferences['hideTimeoutProxies'] == true && (node['testedAt'] as num? ?? 0) > 0 && (node['delay'] as num? ?? 0) <= 0)).toList();
    final sort = context.box.preferences['proxySort'] ?? 'Default';
    if (sort == 'Name') nodes.sort((a, b) => a['tag'].toString().compareTo(b['tag'].toString()));
    if (sort == 'Latency') {
      int delay(Json node) => (node['delay'] as num? ?? 0) > 0 ? (node['delay'] as num).toInt() : 0x7fffffff;
      nodes.sort((a, b) => delay(a).compareTo(delay(b)));
    }
    return nodes;
  }

  Widget _grid(Json group, {bool nested = false}) {
    final nodes = _nodes(group);
    if (nodes.isEmpty) {
      _selectedOffsets.remove(group['tag']);
      return NullStatus(label: context.tr('No results'), illustration: NullStatusIllustration.proxies);
    }
    final prefs = context.box.preferences;
    final compact = prefs['proxyCardSize'] == 'Compact' || prefs['proxyCardSize'] == null && prefs['proxyLayout'] == 'Compact';
    final loose = prefs['proxyCardSize'] == 'Loose';
    return LayoutBuilder(builder: (context, constraints) {
      final standard = ((constraints.maxWidth - 32) / 250).ceil().clamp(2, 8);
      final density = prefs['proxyDensity'] ?? 'Standard';
      final columns = (standard + (density == 'Tight' ? 1 : density == 'Loose' ? -1 : 0)).clamp(1, 8);
      final measure = MeasureScope.of(context);
      final nameHeight = measure.textHeight(AppTextStyle.subheadline) * (compact ? 1 : 2);
      final detailHeight = measure.textHeight(AppTextStyle.footnote);
      final extent = 28 + nameHeight + detailHeight + (loose ? measure.textHeight(AppTextStyle.caption2) + 6 : 0);
      final selectedIndex = nodes.indexWhere((node) => node['tag'] == group['selected']);
      if (!nested) {
        if (selectedIndex < 0) _selectedOffsets.remove(group['tag']);
        else _selectedOffsets[group['tag'].toString()] = (selectedIndex ~/ columns) * (extent + 8);
      }
      return GridView.builder(
        key: PageStorageKey('proxy-grid:${group['tag']}:$nested'),
        controller: nested ? null : _scrolls.putIfAbsent(group['tag'].toString(), () => sheetScrollController(context)),
        shrinkWrap: nested, physics: nested ? const NeverScrollableScrollPhysics() : null,
        padding: EdgeInsets.fromLTRB(16, 0, 16, nested ? 10 : 16 + context.bottomInset),
        gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
          crossAxisCount: columns, mainAxisSpacing: 8, crossAxisSpacing: 8,
          mainAxisExtent: extent,
        ),
        itemCount: nodes.length,
        itemBuilder: (context, index) {
          final node = nodes[index];
          final selected = group['selected'] == node['tag'];
          final delay = (node['delay'] as num?)?.toInt() ?? 0;
          final tested = (node['testedAt'] as num? ?? 0) > 0;
          final delayText = delay > 0 ? delay.toString() + ' ms' : tested ? context.tr('Unreachable') : '—';
          final delayColor = !tested ? context.colorScheme.onSurfaceVariant : delay <= 0
              ? context.colorScheme.error : delay < 300 ? context.colorScheme.success : context.colorScheme.warning;
          final menu = [
            if (group['selectable'] == true)
              CommonPopupMenuItem(glyph: AppGlyphs.checkCircle, label: context.tr('Select'), checked: selected, onPressed: () => _select(group, node)),
            CommonPopupMenuItem(glyph: AppGlyphs.bolt, label: context.tr('Test latency'), onPressed: () => _test(node['tag'] as String)),
            CommonPopupMenuItem(glyph: AppGlyphs.info, label: context.tr('Details'), onPressed: () => openForm(context, BoxPage(title: 'Details', children: [
              DetailRows({'Name': node['tag'], 'Protocol': node['type'], 'Latency': delayText, 'Proxy groups': group['tag']}),
            ]))),
          ];
          return ContextMenuRegion(
            key: ValueKey('${group['tag']}:${node['tag']}'),
            menuItems: menu,
            child: CommonCard(
              radius: AppCorner.lg, enterActionsOnRight: true,
              isSelected: selected,
              onPressed: group['selectable'] == true ? () => _select(group, node) : null,
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 12),
                child: Column(mainAxisAlignment: MainAxisAlignment.center, crossAxisAlignment: CrossAxisAlignment.start, children: [
                  Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
                    Expanded(child: SizedBox(height: nameHeight, child: EmojiText(node['tag'].toString(), maxLines: compact ? 1 : 2, overflow: TextOverflow.ellipsis, style: AppTextStyle.subheadline.of(context)))),
                    if (selected) const Padding(padding: EdgeInsetsDirectional.only(start: 4), child: SelectionBadge()),
                  ]),
                  const SizedBox(height: 8),
                  if (loose) SizedBox(height: detailHeight, child: Text(node['type'].toString(), maxLines: 1,
                    overflow: TextOverflow.ellipsis, style: AppTextStyle.footnote.of(context).copyWith(color: context.colorScheme.onSurface.opacity80))),
                  if (loose) const SizedBox(height: 6),
                  SizedBox(height: loose ? measure.textHeight(AppTextStyle.caption2) : detailHeight, child: Row(children: [
                    if (!loose)
                    Expanded(child: TooltipText(text: Text(node['type'].toString(), maxLines: 1, overflow: TextOverflow.ellipsis,
                      style: AppTextStyle.footnote.of(context).copyWith(color: context.colorScheme.onSurface.opacity80)))),
                    if (!loose) const SizedBox(width: 4),
                    Flexible(child: Semantics(button: true, label: context.tr('Test latency'), child: GestureDetector(
                      behavior: HitTestBehavior.opaque,
                      onTap: () => _test(node['tag'] as String),
                      child: FadeBox(alignment: loose ? Alignment.centerLeft : Alignment.centerRight, child:
                        _testing.contains(node['tag']) || _testing.contains(group['tag'])
                          ? CommonCircleLoading(size: measure.textHeight(AppTextStyle.caption2))
                          : !tested ? GlyphIcon(AppGlyphs.bolt, size: measure.textHeight(AppTextStyle.caption2))
                          : Text(delayText, maxLines: 1, overflow: TextOverflow.ellipsis,
                              style: AppTextStyle.caption2.of(context, emphasized: true).copyWith(color: delayColor)),
                      ),
                    ))),
                  ])),
                ]),
              ),
            ),
          );
        },
      );
    });
  }

  @override Widget build(BuildContext context) {
    final box = context.box;
    final groups = box.groups;
    final liveTags = groups.map((group) => group['tag'].toString()).toSet();
    _folded.removeWhere((tag) => !liveTags.contains(tag));
    _selectedOffsets.removeWhere((tag, _) => !liveTags.contains(tag));
    if (_scrolls.keys.any((tag) => !liveTags.contains(tag))) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!mounted) return;
        final current = context.box.groups.map((group) => group['tag'].toString()).toSet();
        for (final tag in _scrolls.keys.where((tag) => !current.contains(tag)).toList()) {
          _scrolls.remove(tag)?.dispose();
        }
      });
    }
    final list = box.preferences['proxyLayout'] == 'List';
    if (!groups.any((group) => group['tag'] == _currentGroup)) _currentGroup = groups.firstOrNull?['tag'] as String?;
    return CommonScaffold(
      title: context.tr('Proxies'),
      searchState: AppBarSearchState(onSearch: (query) => setState(() => _query = query)),
      primaryAction: list ? null : IconButtonData(glyph: AppGlyphs.bolt, tooltip: context.tr('Test latency'),
        isLoading: _testing.contains(_currentGroup),
        onPressed: box.running && _currentGroup != null ? () => _test(_currentGroup!) : null),
      iconActions: [
        if (!list) IconButtonData(glyph: AppGlyphs.locate, tooltip: context.tr('Scroll to selected'), onPressed: groups.isEmpty ? null : _locate),
      ],
      menuItems: [
        _options('Style', AppGlyphs.appsList, 'proxyLayout', const ['Tabs', 'List'], 'Tabs'),
        _options('Sort', AppGlyphs.sort, 'proxySort', const ['Default', 'Latency', 'Name'], 'Default'),
        _options('Layout', AppGlyphs.dashboard, 'proxyDensity', const ['Tight', 'Standard', 'Loose'], 'Standard'),
        _options('Size', AppGlyphs.textSize, 'proxyCardSize', const ['Compact', 'Standard', 'Loose'], 'Standard'),
        CommonPopupMenuItem(glyph: AppGlyphs.eyeOff, label: context.tr('Hide timeout proxies'), checked: box.preferences['hideTimeoutProxies'] == true,
          section: const CommonPopupMenuSection(), onPressed: () => box.setPreference('hideTimeoutProxies', box.preferences['hideTimeoutProxies'] != true)),
        CommonPopupMenuItem(glyph: AppGlyphs.bolt, label: context.tr('Test all'), onPressed: () async {
          if (!box.running) return;
          for (final group in groups) { await _test(group['tag'] as String); }
        }),
        CommonPopupMenuItem(glyph: AppGlyphs.connections, label: context.tr('Close connections after switching'), checked: box.preferences['closeConnectionsOnSelect'] == true,
          onPressed: () => box.setPreference('closeConnectionsOnSelect', box.preferences['closeConnectionsOnSelect'] != true)),
      ],
      body: groups.isEmpty ? NullStatus(
        label: context.tr(box.running ? 'No proxy groups in this configuration' : 'Start the service to view proxy groups'),
        illustration: NullStatusIllustration.proxies,
      ) : list ? ListView(
        padding: EdgeInsets.only(top: context.contentTopPadding, bottom: 24 + context.bottomInset),
        children: [for (final group in groups) Column(children: [
          ListItem(leading: const GlyphIcon(AppGlyphs.proxies), title: Text(group['tag'].toString()), subtitle: Text(group['selected'].toString()),
            onTap: () => setState(() { final tag = group['tag'].toString(); _folded.contains(tag) ? _folded.remove(tag) : _folded.add(tag); }),
            trailing: IconButton(icon: const GlyphIcon(AppGlyphs.bolt), tooltip: context.tr('Test latency'), onPressed: () => _test(group['tag'] as String))),
          if (!_folded.contains(group['tag'])) _grid(group, nested: true),
        ])],
      ) : DefaultTabController(
        key: ValueKey(groups.map((group) => '${group['tag'].toString().length}:${group['tag']}').join()),
        initialIndex: groups.indexWhere((group) => group['tag'] == _currentGroup).clamp(0, groups.length - 1),
        length: groups.length,
        child: Padding(
          padding: EdgeInsets.only(top: context.contentTopPadding),
          child: Column(children: [
            Padding(padding: const EdgeInsets.only(bottom: 16), child: Builder(builder: (context) => GroupTabs(
              controller: DefaultTabController.of(context), groups: groups,
              onSelected: (index) { if (mounted) setState(() => _currentGroup = groups[index]['tag'] as String); },
            ))),
            Expanded(child: TabBarView(children: [for (final group in groups) Column(children: [
              if (group['selectable'] != true) Padding(padding: const EdgeInsets.only(top: 8), child: Text(context.tr('Automatic selection'), style: context.listCaptionStyle)),
              Expanded(child: _grid(group)),
            ])])),
          ]),
        ),
      ),
    );
  }
}
