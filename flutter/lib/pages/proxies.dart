import '../widgets.dart';

class ProxiesPage extends StatefulWidget {
  const ProxiesPage({super.key});
  @override State<ProxiesPage> createState() => _ProxiesPageState();
}
class _ProxiesPageState extends State<ProxiesPage> {
  String _query = '';
  final _testing = <String>{};

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
    final nodes = rows(group['items']).where((node) => node['tag'].toString().toLowerCase().contains(_query.toLowerCase())).toList();
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
    if (nodes.isEmpty) return const EmptyState('No results');
    final prefs = context.box.preferences;
    final compact = prefs['proxyLayout'] == 'Compact';
    return LayoutBuilder(builder: (context, constraints) {
      final preferred = (prefs['proxyColumns'] as num?)?.toInt() ?? 2;
      final columns = constraints.maxWidth < 300 ? 1 : preferred.clamp(1, 4);
      final measure = MeasureScope.of(context);
      final nameHeight = measure.textHeight(AppTextStyle.subheadline) * (compact ? 1 : 2);
      final detailHeight = measure.textHeight(AppTextStyle.footnote);
      return GridView.builder(
        shrinkWrap: nested, physics: nested ? const NeverScrollableScrollPhysics() : null,
        padding: EdgeInsets.fromLTRB(16, 12, 16, nested ? 12 : 24 + context.bottomInset),
        gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
          crossAxisCount: columns, mainAxisSpacing: 8, crossAxisSpacing: 8,
          mainAxisExtent: 28 + nameHeight + detailHeight,
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
                  SizedBox(height: detailHeight, child: Row(children: [
                    Expanded(child: TooltipText(text: Text(node['type'].toString(), maxLines: 1, overflow: TextOverflow.ellipsis,
                      style: AppTextStyle.footnote.of(context).copyWith(color: context.colorScheme.onSurface.opacity80)))),
                    const SizedBox(width: 4),
                    Flexible(child: Semantics(button: true, label: context.tr('Test latency'), child: GestureDetector(
                      behavior: HitTestBehavior.opaque,
                      onTap: () => _test(node['tag'] as String),
                      child: FadeBox(alignment: Alignment.centerRight, child:
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
    final list = box.preferences['proxyLayout'] == 'List';
    return CommonScaffold(
      title: context.tr('Proxies'),
      searchState: AppBarSearchState(onSearch: (query) => setState(() => _query = query)),
      iconActions: [
        IconButtonData(glyph: AppGlyphs.bolt, tooltip: context.tr('Test all'), onPressed: box.running && groups.isNotEmpty ? () async {
          for (final group in groups) { await _test(group['tag'] as String); }
        } : null, isLoading: _testing.isNotEmpty),
        IconButtonData(glyph: AppGlyphs.settings, tooltip: context.tr('Settings'), onPressed: () => openForm(context, const ProxySettingsPage())),
      ],
      body: groups.isEmpty ? NullStatus(
        label: context.tr(box.running ? 'No proxy groups in this configuration' : 'Start the service to view proxy groups'),
        illustration: NullStatusIllustration.proxies,
      ) : list ? ListView(
        padding: EdgeInsets.only(top: context.contentTopPadding, bottom: 24 + context.bottomInset),
        children: [for (final group in groups) Column(children: [
          ListItem(title: Text(group['tag'].toString()), subtitle: Text(group['selected'].toString()),
            trailing: IconButton(icon: const GlyphIcon(AppGlyphs.bolt), tooltip: context.tr('Test latency'), onPressed: () => _test(group['tag'] as String))),
          _grid(group, nested: true),
        ])],
      ) : DefaultTabController(
        key: ValueKey(groups.map((group) => group['tag']).join('|')),
        length: groups.length,
        child: Padding(
          padding: EdgeInsets.only(top: context.contentTopPadding),
          child: Column(children: [
            TabBar(isScrollable: true, tabAlignment: TabAlignment.start, tabs: [
              for (final group in groups) Tab(
                height: MeasureScope.of(context).textHeight(AppTextStyle.subheadline) + MeasureScope.of(context).textHeight(AppTextStyle.caption1) + 8,
                child: Column(mainAxisAlignment: MainAxisAlignment.center, children: [
                Text(group['tag'].toString(), maxLines: 1, style: AppTextStyle.subheadline.of(context)),
                Text(group['selected'].toString(), maxLines: 1, style: context.listCaptionStyle),
              ])),
            ]),
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

class ProxySettingsPage extends StatelessWidget {
  const ProxySettingsPage({super.key});
  @override Widget build(BuildContext context) {
    final box = context.box;
    return BoxPage(title: 'Proxies', children: [
      generateSection(title: context.tr('View'), items: [
        ListItem.options(title: Text(context.tr('Layout')), options: const ['Tabs', 'List', 'Compact'],
          value: box.preferences['proxyLayout'] as String? ?? 'Tabs', textBuilder: context.tr,
          onChanged: (value) => box.setPreference('proxyLayout', value)),
        ListItem<int>.options(title: Text(context.tr('Columns')), options: const [1, 2, 3, 4],
          value: (box.preferences['proxyColumns'] as num?)?.toInt() ?? 2, textBuilder: (value) => value.toString(),
          onChanged: (value) => box.setPreference('proxyColumns', value)),
        ListItem.options(title: Text(context.tr('Sort')), options: const ['Default', 'Name', 'Latency'],
          value: box.preferences['proxySort'] as String? ?? 'Default', textBuilder: context.tr,
          onChanged: (value) => box.setPreference('proxySort', value)),
        ListItem.toggle(title: Text(context.tr('Close connections after switching')),
          value: box.preferences['closeConnectionsOnSelect'] == true, onChanged: (value) => box.setPreference('closeConnectionsOnSelect', value)),
      ]),
    ]);
  }
}
