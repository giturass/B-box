import '../widgets.dart';
import '../components/group_tabs.dart';
import '../components/proxy_card.dart';
import '../components/proxy_list.dart';

class ProxiesPage extends StatefulWidget {
  const ProxiesPage({super.key});
  @override State<ProxiesPage> createState() => _ProxiesPageState();
}
class _ProxiesPageState extends State<ProxiesPage> {
  String _query = '';
  final _testing = <String>{};
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
    if (_testing.contains(tag) || !context.box.running) return;
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

  Widget _card(BuildContext context, Json group, Json node) => ProxyNodeCard(
    group: group, node: node, testing: _testing.contains(group['tag']) || _testing.contains(node['tag']),
    onSelect: () => _select(group, node), onTest: () => _test(node['tag'] as String),
  );

  Widget _grid(Json group) {
    final nodes = _nodes(group);
    if (nodes.isEmpty) {
      _selectedOffsets.remove(group['tag']);
      return NullStatus(label: context.tr('No results'), illustration: NullStatusIllustration.proxies);
    }
    return LayoutBuilder(builder: (context, constraints) {
      final columns = proxyColumns(constraints.maxWidth - 32, context.box.preferences);
      final metrics = ProxyCardMetrics.of(context, context.box.preferences);
      final selected = nodes.indexWhere((node) => node['tag'] == group['selected']);
      if (selected < 0) _selectedOffsets.remove(group['tag']);
      else _selectedOffsets[group['tag'].toString()] = (selected ~/ columns) * metrics.rowExtent;
      return GridView.builder(
        key: PageStorageKey('proxy-grid:${group['tag']}'),
        controller: _scrolls.putIfAbsent(group['tag'].toString(), () => sheetScrollController(context)),
        padding: EdgeInsets.fromLTRB(16, 0, 16, 16 + context.bottomInset),
        gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(crossAxisCount: columns,
          mainAxisSpacing: 8, crossAxisSpacing: 8, mainAxisExtent: metrics.extent),
        itemCount: nodes.length, itemBuilder: (context, index) => _card(context, group, nodes[index]),
      );
    });
  }

  @override Widget build(BuildContext context) {
    final box = context.box;
    final groups = box.groups;
    final liveTags = groups.map((group) => group['tag'].toString()).toSet();
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
        if (list) _options('Icon style', AppGlyphs.iconTile, 'proxyIconStyle', const ['Filled', 'Plain', 'Hidden'], 'Filled'),
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
      ) : list ? ProxyListView(
        key: ValueKey(box.remote ? 'remote:${box.dashboard['remote']}' : 'local:${box.selectedProfileId}'),
        profileKey: box.remote ? 'remote:${box.dashboard['remote']}' : 'local:${box.selectedProfileId}',
        groups: [for (final group in groups) ProxyListGroup(group, _nodes(group))],
        testing: Set.of(_testing), cardBuilder: _card, onTest: _test,
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
