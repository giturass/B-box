import 'dart:math' as math;
import 'package:flutter/rendering.dart' show ScrollCacheExtent;
import '../widgets.dart';
import '../components/dashboard_editor.dart';

import '../components/connected_buttons.dart';
import 'connections.dart';
import '../components/status_panel.dart';
import '../components/memory_panel.dart';
import '../components/intranet_panel.dart';
import '../components/profile_panel.dart';
import '../components/proxy_groups_panel.dart';

export '../components/dashboard_editor.dart' show DashboardEditor;

const dashboardCards = ['Status', 'Network speed', 'Outbound mode', 'Current profile', 'Connections', 'Proxy groups', 'Memory', 'Intranet IP'];
const defaultDashboardCards = ['Status', 'Network speed', 'Outbound mode', 'Current profile', 'Proxy groups'];
const defaultHiddenCards = ['Connections', 'Memory', 'Intranet IP'];

List<String> dashboardOrder(Json prefs) {
  final saved = (prefs['dashboardCards'] as List? ?? defaultDashboardCards).whereType<String>().where(dashboardCards.contains).toList();
  return {...saved, ...dashboardCards}.toList();
}

class DashboardPage extends StatefulWidget {
  const DashboardPage({super.key});
  @override State<DashboardPage> createState() => _DashboardPageState();
}

class _DashboardPageState extends State<DashboardPage> {
  final _cardKeys = <String, GlobalKey>{};
  final _editor = GlobalKey<DashboardEditorState>();
  late final _scroll = sheetScrollController(context);
  bool _editing = false, _saving = false;
  int _editRevision = 0;
  GlobalKey _keyOf(String card) => _cardKeys.putIfAbsent(card, GlobalKey.new);

  @override void didChangeDependencies() {
    super.didChangeDependencies();
    if (_editRevision != context.box.dashboardEditRevision) {
      _editRevision = context.box.dashboardEditRevision;
      _editing = true;
    }
  }
  @override void dispose() { _scroll.dispose(); super.dispose(); }

  Map<String, Widget> _cards() {
    final box = context.box;
    return <String, Widget>{
      'Status': const StatusPanel(),
      'Intranet IP': const IntranetPanel(),
      'Network speed': const _SpeedPanel(),
      if ((box.dashboard['modes'] as List? ?? []).isNotEmpty) 'Outbound mode': const _ModePanel(),
      'Current profile': const ProfilePanel(),
      'Connections': BoxPanel(padding: const EdgeInsets.all(8), onTap: () => openPage(context, const ConnectionsPage()),
        child: Padding(padding: const EdgeInsets.symmetric(vertical: 10), child: Column(spacing: 2, children: [
          Text((box.dashboard['connections'] ?? 0).toString(), maxLines: 1, style: AppTextStyle.title2.of(context, emphasized: true).toTabular.copyWith(letterSpacing: 0)),
          Text(context.tr('Connections'), maxLines: 1, style: AppTextStyle.caption1.of(context).copyWith(color: context.colorScheme.onSurfaceVariant)),
        ]))),
      'Proxy groups': const ProxyGroupsPanel(),
      'Memory': const MemoryPanel(),
    };
  }

  Future<bool> _saveLayout(List<String> shown) async {
    final box = context.box, available = _cards().keys.toSet();
    final order = dashboardOrder(box.preferences), result = List<String>.of(shown);
    final hidden = (box.preferences['hiddenCards'] as List? ?? defaultHiddenCards).whereType<String>().toSet();
    var at = 0;
    for (final card in order) {
      if (available.contains(card)) {
        final index = result.indexOf(card);
        if (index >= 0) at = index + 1;
      } else { result.insert(at++, card); }
    }
    for (final card in order) { if (!result.contains(card)) result.add(card); }
    for (final card in available) {
      shown.contains(card) ? hidden.remove(card) : hidden.add(card);
    }
    hidden.remove('Status');
    return box.setPreferences({'dashboardCards': result, 'hiddenCards': hidden.toList()});
  }

  @override Widget build(BuildContext context) {
    final box = context.box, cards = _cards();
    final hidden = box.preferences['hiddenCards'] as List? ?? defaultHiddenCards;
    final shown = [for (final card in dashboardOrder(box.preferences))
      if (cards.containsKey(card) && (card == 'Status' || !hidden.contains(card))) card];
    return CommonScaffold(title: context.tr(_editing ? 'Edit dashboard' : 'Dashboard'),
      isLoading: _saving, backgroundColor: SurfaceLevel.page.colorOf(context.colorScheme),
      confirmationAction: _editing ? IconButtonData(glyph: AppGlyphs.check, tooltip: context.tr('Done'),
        onPressed: _saving ? null : () { _editor.currentState?.finish(); }) : null,
      iconActions: [if (!_editing) IconButtonData(glyph: AppGlyphs.edit, tooltip: context.tr('Dashboard cards'),
        onPressed: () => setState(() => _editing = true))],
      body: LayoutBuilder(builder: (context, constraints) {
        final side = math.max(16.0, (constraints.maxWidth - 560) / 2), gap = context.isMobileView ? 8.0 : 12.0;
        final padding = EdgeInsets.fromLTRB(side, context.contentTopPadding, side, 16 + context.bottomInset);
        return CustomScrollView(key: const PageStorageKey('dashboard'), controller: _scroll,
          scrollCacheExtent: _editing ? ScrollCacheExtent.viewport(dashboardCards.length.toDouble()) : null,
          slivers: [
            if (_editing) DashboardEditor(key: _editor, shown: shown,
              available: [for (final card in dashboardCards) if (cards.containsKey(card)) card],
              cardKeyOf: _keyOf, cardBuilder: (card) => cards[card]!, padding: padding, gap: gap,
              onChanged: _saveLayout, onBusyChanged: (busy) { if (mounted) setState(() => _saving = busy); },
              onDone: () { if (mounted) setState(() => _editing = false); })
            else SliverPadding(padding: padding, sliver: SliverToBoxAdapter(child: Column(spacing: gap, children: [
              if (box.remote) ListItem(title: Text(box.dashboard['remote'].toString()), subtitle: Text(context.tr('Remote control')),
                trailing: TextButton(onPressed: () => box.act('remote.disconnect'), child: Text(context.tr('Disconnect')))),
              if (box.applyRequired != null) ListItem(title: Text(context.tr('Restart required')), trailing: TextButton(
                onPressed: () async { if (await box.act('service.reload', {'restart': true})) box.clearApplyRequired(); },
                child: Text(context.tr('Apply')))),
              for (final card in shown) RepaintBoundary(key: _keyOf(card), child: cards[card]),
              if (box.dashboard['systemProxyAvailable'] == true) BoxPanel(child: ListItem.toggle(
                title: Text(context.tr('System HTTP proxy')), value: box.dashboard['systemProxy'] == true,
                onChanged: (value) => box.act('systemProxy.set', {'enabled': value}))),
              for (final note in rows(box.dashboard['deprecated'])) ListItem(title: Text(note['message'].toString()),
                onTap: note['url'] == null ? null : () => box.act('url.open', {'url': note['url']})),
            ]))),
          ],
        );
      }),
    );
  }
}

// Adapted from FlClash v0.9.2 dashboard/widgets/traffic.dart.
class _SpeedPanel extends StatelessWidget {
  const _SpeedPanel();
  @override Widget build(BuildContext context) => BoxPanel(child: Column(children: [
    const PanelTitle('Network speed', AppGlyphs.speed),
    const SizedBox(height: 12),
    Row(spacing: 10, children: [for (final upload in [true, false]) Expanded(child: _SpeedTile(upload: upload))]),
  ]));
}

class _SpeedTile extends StatelessWidget {
  const _SpeedTile({required this.upload});
  final bool upload;
  @override Widget build(BuildContext context) {
    final box = context.box, colors = context.colorScheme;
    final color = upload ? colors.onSurfaceVariant : colors.primary;
    final show = bytes(box.rates[upload ? 'upload' : 'download'] as num? ?? 0).split(' ');
    final scale = math.max(1.0, MediaQuery.textScalerOf(context).scale(1));
    final textHeight = 14 + 4 + (AppTextStyle.footnote.leading + AppTextStyle.title1.leading + AppTextStyle.caption1.leading) * scale;
    return LayoutBuilder(builder: (context, constraints) {
      final chartHeight = math.max(36.0, constraints.maxWidth / 1.06 - textHeight - 12 + 2);
      return Container(
        clipBehavior: Clip.antiAlias,
        decoration: ShapeDecoration(color: SurfaceLevel.block.colorOf(colors), shape: AppShape.all(22)),
        child: Stack(children: [
          Positioned(left: -2, right: -2, bottom: -2, height: chartHeight, child: LineChart(
            values: List.of(upload ? box.uploadHistory : box.downloadHistory), revision: box.sampleRevision,
            capacity: 30, minScale: 8 * 1024, baselineRatio: 1,
            color: upload ? Color.alphaBlend(colors.primary.withValues(alpha: .45), SurfaceLevel.card.colorOf(colors)) : colors.primary,
          )),
          Padding(padding: EdgeInsets.fromLTRB(16, 14, 16, chartHeight - 2 + 12), child: Column(
            crossAxisAlignment: CrossAxisAlignment.start, children: [
              Row(spacing: 4, children: [
                GlyphIcon(upload ? AppGlyphs.arrowUp : AppGlyphs.arrowDown, size: 16, color: color),
                Flexible(child: Text(context.tr(upload ? 'Upload' : 'Download'), maxLines: 1, overflow: TextOverflow.ellipsis,
                  style: AppTextStyle.footnote.of(context, emphasized: true).copyWith(color: colors.onSurfaceVariant))),
              ]),
              const SizedBox(height: 4),
              Text.rich(TextSpan(children: [
                TextSpan(text: show.first, style: AppTextStyle.title1.of(context, emphasized: true).toTabular.copyWith(letterSpacing: -.02 * AppTextStyle.title1.size)),
                TextSpan(text: ' ${show.last}/s', style: AppTextStyle.footnote.of(context, emphasized: true).copyWith(color: colors.onSurfaceVariant)),
              ]), maxLines: 1, overflow: TextOverflow.clip),
              Text('${context.tr('Total')} ${box.dashboard[upload ? 'uploadTotal' : 'downloadTotal'] ?? '0 B'}',
                maxLines: 1, overflow: TextOverflow.ellipsis,
                style: AppTextStyle.caption1.of(context).toTabular.copyWith(color: colors.onSurfaceVariant)),
            ],
          )),
        ]),
      );
    });
  }
}

class _ModePanel extends StatelessWidget {
  const _ModePanel();
  @override Widget build(BuildContext context) {
    final box = context.box;
    final modes = (box.dashboard['modes'] as List).cast<String>();
    return BoxPanel(child: Column(children: [
      const PanelTitle('Outbound mode', AppGlyphs.split),
      const SizedBox(height: 12),
      DashboardConnectedGroup(children: [
        for (final (index, mode) in modes.indexed) Expanded(child: DashboardConnectedButton(
          selected: mode == box.dashboard['mode'], first: index == 0, last: index == modes.length - 1,
          outer: 26, inner: 12, background: SurfaceLevel.block.colorOf(context.colorScheme),
          semanticLabel: context.tr({'rule': 'Rule', 'global': 'Global', 'direct': 'Direct'}[mode] ?? mode),
          onTap: () { if (mode != box.dashboard['mode']) box.act('mode.set', {'mode': mode}); },
          builder: (foreground) => SizedBox(height: 52, child: Column(
            mainAxisAlignment: MainAxisAlignment.center, spacing: 2, children: [
              GlyphIcon({'rule': AppGlyphs.rules, 'global': AppGlyphs.proxies, 'direct': AppGlyphs.target}[mode] ?? AppGlyphs.split, size: 18, color: foreground),
              Text(context.tr({'rule': 'Rule', 'global': 'Global', 'direct': 'Direct'}[mode] ?? mode),
                maxLines: 1, overflow: TextOverflow.ellipsis,
                style: AppTextStyle.footnote.of(context, emphasized: true).copyWith(color: foreground)),
            ],
          )),
        )),
      ]),
    ]));
  }
}
