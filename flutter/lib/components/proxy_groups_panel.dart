import 'package:material_new_shapes/material_new_shapes.dart';
import '../widgets.dart';
import '../pages/proxies.dart';
import 'shape_badge.dart';
import 'dashboard_chip.dart';

class ProxyGroupsPanel extends StatelessWidget {
  const ProxyGroupsPanel({super.key});
  @override Widget build(BuildContext context) {
    final box = context.box, groups = context.box.groups;
    final timeout = <String>{for (final group in groups) for (final node in rows(group['items']))
      if ((node['testedAt'] as num? ?? 0) > 0 && (node['delay'] as num? ?? 0) <= 0) node['tag'].toString()};
    return BoxPanel(onTap: groups.isEmpty ? null : () => openForm(context, const ProxiesPage()), child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
      PanelTitle('Proxy groups', AppGlyphs.layers, trailing: Row(mainAxisSize: MainAxisSize.min, spacing: 6, children: [
        if (timeout.isNotEmpty) DashboardChip(label: '${context.tr('Timeout')} ${timeout.length}', tone: DashboardChipTone.warn),
        if (groups.isNotEmpty) const GlyphIcon(AppGlyphs.chevronForward, size: 18),
      ])),
      const SizedBox(height: 12),
      if (groups.isEmpty) Padding(padding: const EdgeInsets.fromLTRB(24, 12, 24, 24), child: Column(spacing: 6, children: [
        ShapeBadge(shape: MaterialShapes.cookie9Sided, color: SurfaceLevel.block.colorOf(context.colorScheme), size: 64,
          child: GlyphIcon(AppGlyphs.layers, size: 26, color: context.colorScheme.onSurfaceVariant)),
        Text(context.tr(box.running ? 'No proxy groups in this configuration' : 'Start the service to view proxy groups'), textAlign: TextAlign.center,
          style: AppTextStyle.subheadline.of(context, emphasized: true)),
      ]))
      else Row(spacing: 12, children: [
        Expanded(child: Text.rich(TextSpan(children: [
          TextSpan(text: '${groups.length}', style: AppTextStyle.display.of(context, emphasized: true).toTabular.copyWith(letterSpacing: 0)),
          TextSpan(text: ' ${context.tr('Groups')}', style: AppTextStyle.subheadline.of(context).copyWith(color: context.colorScheme.onSurfaceVariant)),
        ]), maxLines: 1, overflow: TextOverflow.clip)),
        _GroupIconStack(groups: groups),
      ]),
    ]));
  }
}

class _GroupIconStack extends StatelessWidget {
  const _GroupIconStack({required this.groups});
  final List<Json> groups;
  @override Widget build(BuildContext context) {
    final shown = groups.take(4).toList(), rest = groups.length - groups.take(4).length;
    final count = shown.length + (rest > 0 ? 1 : 0);
    final colors = context.colorScheme;
    return SizedBox(width: count * 40 - (count - 1) * 8, height: 40, child: Stack(clipBehavior: Clip.none, children: [
      for (final (index, group) in shown.indexed) PositionedDirectional(start: index * 32, child: Container(
        width: 40, height: 40, padding: const EdgeInsets.all(6),
        decoration: ShapeDecoration(color: SurfaceLevel.inset.colorOf(colors), shape: AppShape.all(14).copyWith(
          side: BorderSide(color: SurfaceLevel.card.colorOf(colors), width: 3, strokeAlign: BorderSide.strokeAlignOutside))),
        child: Center(child: EmojiText(String.fromCharCodes(group['tag'].toString().runes.take(2)).toUpperCase(), maxLines: 1,
          style: AppTextStyle.caption1.of(context, emphasized: true).copyWith(color: colors.onSurfaceVariant))),
      )),
      if (rest > 0) PositionedDirectional(start: shown.length * 32, child: Container(width: 40, height: 40,
        decoration: ShapeDecoration(color: SurfaceLevel.block.colorOf(colors), shape: AppShape.all(14).copyWith(
          side: BorderSide(color: SurfaceLevel.card.colorOf(colors), width: 3, strokeAlign: BorderSide.strokeAlignOutside))),
        child: Center(child: Text('+$rest', style: AppTextStyle.caption1.of(context, emphasized: true).toTabular.copyWith(color: colors.onSurfaceVariant))))),
    ]));
  }
}
