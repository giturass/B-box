import 'dart:math' as math;
import '../widgets.dart';
import '../components/connected_buttons.dart';
import 'profiles.dart';
import 'proxies.dart';
import 'connections.dart';
import '../components/status_panel.dart';
import '../components/memory_panel.dart';
import '../components/intranet_panel.dart';

const dashboardCards = ['Status', 'Network speed', 'Outbound mode', 'Current profile', 'Connections', 'Proxy groups', 'Memory', 'Intranet IP'];

List<String> dashboardOrder(Json prefs) {
  final saved = (prefs['dashboardCards'] as List? ?? dashboardCards).whereType<String>().where(dashboardCards.contains).toList();
  return {...saved, ...dashboardCards}.toList();
}

class DashboardPage extends StatelessWidget {
  const DashboardPage({super.key});
  @override Widget build(BuildContext context) {
    final box = context.box;
    final saved = dashboardOrder(box.preferences);
    final hidden = (box.preferences['hiddenCards'] as List?) ?? [];
    final cards = <String, Widget>{
      'Status': const StatusPanel(),
      'Intranet IP': const IntranetPanel(),
      'Network speed': const _SpeedPanel(),
      if ((box.dashboard['modes'] as List? ?? []).isNotEmpty) 'Outbound mode': const _ModePanel(),
      'Current profile': BoxPanel(
        onTap: () => openForm(context, const ProfilesPage(picker: true)),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          const PanelTitle('Current profile', AppGlyphs.profiles),
          const SizedBox(height: 16),
          Text(box.selectedProfile?['name'] as String? ?? context.tr('No profile'), maxLines: 2, overflow: TextOverflow.ellipsis, style: AppTextStyle.title3.of(context)),
          const SizedBox(height: 6),
          Text(context.tr(box.selectedProfile?['remote'] == true ? 'Remote' : 'Local'), style: context.listSubtitleStyle),
        ]),
      ),
      'Connections': _SummaryPanel('Connections', AppGlyphs.connections, (box.dashboard['connections'] ?? 0).toString(), () => openPage(context, const ConnectionsPage())),
      'Proxy groups': _SummaryPanel('Proxy groups', AppGlyphs.layers, box.groups.length.toString(), () => openPage(context, const ProxiesPage())),
      'Memory': const MemoryPanel(),
    };
    return CommonScaffold(
      title: context.tr('Dashboard'),
      iconActions: [IconButtonData(glyph: AppGlyphs.edit, tooltip: context.tr('Dashboard cards'), onPressed: () => openForm(context, const DashboardEditor()))],
      body: LayoutBuilder(builder: (context, constraints) {
        final side = math.max(16.0, (constraints.maxWidth - 560) / 2);
        return ListView(
          padding: EdgeInsets.fromLTRB(side, context.contentTopPadding, side, 16 + context.bottomInset),
          children: [
            if (box.remote) Padding(
              padding: const EdgeInsets.only(bottom: 12),
              child: ListItem(title: Text(box.dashboard['remote'].toString()), subtitle: Text(context.tr('Remote control')),
                trailing: TextButton(onPressed: () => box.act('remote.disconnect'), child: Text(context.tr('Disconnect')))),
            ),
            if (box.applyRequired != null) Padding(
              padding: const EdgeInsets.only(bottom: 12),
              child: ListItem(title: Text(context.tr('Restart required')), trailing: TextButton(
                onPressed: () async {
                  if (await box.act('service.reload', {'restart': true})) box.clearApplyRequired();
                },
                child: Text(context.tr('Apply')),
              )),
            ),
            for (final card in saved)
              if (cards.containsKey(card) && (card == 'Status' || !hidden.contains(card)))
                Padding(padding: const EdgeInsets.only(bottom: 16), child: cards[card]),
            if (box.dashboard['systemProxyAvailable'] == true)
              BoxPanel(child: ListItem.toggle(
                title: Text(context.tr('System HTTP proxy')),
                value: box.dashboard['systemProxy'] == true,
                onChanged: (value) => box.act('systemProxy.set', {'enabled': value}),
              )),
            for (final note in rows(box.dashboard['deprecated'])) Padding(
              padding: const EdgeInsets.only(top: 16),
              child: ListItem(title: Text(note['message'].toString()), onTap: note['url'] == null ? null : () => box.act('url.open', {'url': note['url']})),
            ),
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

class _SummaryPanel extends StatelessWidget {
  const _SummaryPanel(this.title, this.glyph, this.value, this.onTap);
  final String title, value;
  final Glyph glyph;
  final VoidCallback onTap;
  @override Widget build(BuildContext context) => BoxPanel(onTap: onTap, child: Column(children: [
    PanelTitle(title, glyph, trailing: const GlyphIcon(AppGlyphs.chevronForward, size: 18)),
    const SizedBox(height: 14),
    Align(alignment: AlignmentDirectional.centerStart, child: Text(value, style: AppTextStyle.largeTitle.of(context, emphasized: true).toTabular)),
  ]));
}

class DashboardEditor extends StatefulWidget {
  const DashboardEditor({super.key});
  @override State<DashboardEditor> createState() => _DashboardEditorState();
}
class _DashboardEditorState extends State<DashboardEditor> {
  List<String>? _order;
  Set<String>? _hidden;
  bool _saving = false;
  @override Widget build(BuildContext context) {
    final box = context.box;
    _order ??= dashboardOrder(box.preferences);
    _hidden ??= (box.preferences['hiddenCards'] as List?)?.cast<String>().toSet() ?? {};
    return CommonScaffold(
      title: context.tr('Dashboard cards'),
      isLoading: _saving,
      confirmationAction: IconButtonData(glyph: AppGlyphs.check, tooltip: context.tr('Done'), onPressed: _saving ? null : () async {
        setState(() => _saving = true);
        final saved = await box.setPreferences({'dashboardCards': List.of(_order!), 'hiddenCards': _hidden!.toList()});
        if (!context.mounted) return;
        setState(() => _saving = false);
        if (saved) context.safeNestedPop();
      }),
      body: ReorderableListView.builder(
        padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 24 + context.bottomInset),
        itemCount: _order!.length,
        onReorder: (old, next) { if (!_saving) setState(() { if (old < next) next--; _order!.insert(next, _order!.removeAt(old)); }); },
        itemBuilder: (context, index) {
          final name = _order![index];
          return ListItem.toggle(key: ValueKey(name), title: Text(context.tr(name)),
            value: !_hidden!.contains(name), onChanged: _saving || name == 'Status' ? null : (shown) => setState(() { shown ? _hidden!.remove(name) : _hidden!.add(name); }),
          );
        },
      ),
    );
  }
}
