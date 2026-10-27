import '../widgets.dart';

// Metrics and card presentation from FlClash v0.9.2 proxies/common.dart and card.dart.
class ProxyCardMetrics {
  ProxyCardMetrics.of(BuildContext context, Json preferences)
      : compact = preferences['proxyCardSize'] == 'Compact' ||
            preferences['proxyCardSize'] == null && preferences['proxyLayout'] == 'Compact',
        loose = preferences['proxyCardSize'] == 'Loose',
        measure = MeasureScope.of(context);

  final bool compact, loose;
  final Measure measure;
  double get nameHeight => measure.textHeight(AppTextStyle.subheadline) * (compact ? 1 : 2);
  double get detailHeight => measure.textHeight(AppTextStyle.footnote);
  double get delayHeight => measure.textHeight(AppTextStyle.caption2);
  double get extent => 28 + nameHeight + detailHeight + (loose ? delayHeight + 6 : 0);
  double get rowExtent => extent + 8;
}

int proxyColumns(double width, Json preferences) {
  final standard = (width / 250).ceil().clamp(2, 8);
  final density = preferences['proxyDensity'] ?? 'Standard';
  return (standard + (density == 'Tight' ? 1 : density == 'Loose' ? -1 : 0)).clamp(1, 8);
}

class ProxyNodeCard extends StatelessWidget {
  const ProxyNodeCard({super.key, required this.group, required this.node,
    required this.testing, required this.onSelect, required this.onTest});
  final Json group, node;
  final bool testing;
  final VoidCallback onSelect, onTest;

  @override Widget build(BuildContext context) {
    final metrics = ProxyCardMetrics.of(context, context.box.preferences);
    final selected = group['selected'] == node['tag'];
    final delay = (node['delay'] as num?)?.toInt() ?? 0;
    final tested = (node['testedAt'] as num? ?? 0) > 0;
    final delayText = delay > 0 ? '$delay ms' : tested ? context.tr('Unreachable') : '—';
    final delayColor = !tested ? context.colorScheme.onSurfaceVariant : delay <= 0
        ? context.colorScheme.error : delay < 300 ? context.colorScheme.success : context.colorScheme.warning;
    return ContextMenuRegion(
      key: ValueKey((group['tag'], node['tag'])),
      menuItems: [
        if (group['selectable'] == true)
          CommonPopupMenuItem(glyph: AppGlyphs.checkCircle, label: context.tr('Select'), checked: selected,
            onPressed: context.box.running ? onSelect : null),
        CommonPopupMenuItem(glyph: AppGlyphs.bolt, label: context.tr('Test latency'),
          onPressed: context.box.running && !testing ? onTest : null),
        CommonPopupMenuItem(glyph: AppGlyphs.info, label: context.tr('Details'), onPressed: () => openForm(context, BoxPage(title: 'Details', children: [
          DetailRows({'Name': node['tag'], 'Protocol': node['type'], 'Latency': delayText, 'Proxy groups': group['tag']}),
        ]))),
      ],
      child: CommonCard(
        radius: AppCorner.lg, enterActionsOnRight: true, isSelected: selected,
        onPressed: group['selectable'] == true && context.box.running ? onSelect : null,
        child: Padding(padding: const EdgeInsets.symmetric(horizontal: 12), child: Column(
          mainAxisAlignment: MainAxisAlignment.center, crossAxisAlignment: CrossAxisAlignment.start, children: [
            Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
              Expanded(child: SizedBox(height: metrics.nameHeight, child: EmojiText(node['tag'].toString(),
                maxLines: metrics.compact ? 1 : 2, overflow: TextOverflow.ellipsis, style: AppTextStyle.subheadline.of(context)))),
              if (selected) const Padding(padding: EdgeInsetsDirectional.only(start: 4), child: SelectionBadge()),
            ]),
            const SizedBox(height: 8),
            if (metrics.loose) SizedBox(height: metrics.detailHeight, child: Text(node['type'].toString(), maxLines: 1,
              overflow: TextOverflow.ellipsis, style: AppTextStyle.footnote.of(context).copyWith(color: context.colorScheme.onSurface.opacity80))),
            if (metrics.loose) const SizedBox(height: 6),
            SizedBox(height: metrics.loose ? metrics.delayHeight : metrics.detailHeight, child: Row(children: [
              if (!metrics.loose) Expanded(child: TooltipText(text: Text(node['type'].toString(), maxLines: 1,
                overflow: TextOverflow.ellipsis, style: AppTextStyle.footnote.of(context).copyWith(color: context.colorScheme.onSurface.opacity80)))),
              if (!metrics.loose) const SizedBox(width: 4),
              Flexible(child: Semantics(button: true, label: context.tr('Test latency'), child: GestureDetector(
                behavior: HitTestBehavior.opaque, onTap: context.box.running && !testing ? onTest : null,
                child: FadeBox(alignment: metrics.loose ? Alignment.centerLeft : Alignment.centerRight, child: testing
                  ? CommonCircleLoading(size: metrics.delayHeight)
                  : !tested ? GlyphIcon(AppGlyphs.bolt, size: metrics.delayHeight)
                  : Text(delayText, maxLines: 1, overflow: TextOverflow.ellipsis,
                    style: AppTextStyle.caption2.of(context, emphasized: true).copyWith(color: delayColor))),
              ))),
            ])),
          ],
        )),
      ),
    );
  }
}
