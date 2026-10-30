import 'dart:math' as math;
import '../widgets.dart';
import 'data_subscription.dart';

/// FlClash's memory summary with same-process accounting from libbox.
class MemoryPanel extends StatelessWidget {
  const MemoryPanel({super.key});
  @override Widget build(BuildContext context) => BoxDataSubscription(data: BoxData.memory,
    child: BoxPanel(onTap: () => openForm(context, const MemoryDetails()), child: Column(
    crossAxisAlignment: CrossAxisAlignment.stretch, children: [
      const PanelTitle('Memory', AppGlyphs.memory, trailing: GlyphIcon(AppGlyphs.chevronForward, size: 18)),
      const SizedBox(height: 12), MemorySummary(snapshot: context.watchBox(BoxAspect.memory).memory),
    ])));
}

class MemorySummary extends StatelessWidget {
  const MemorySummary({super.key, required this.snapshot});
  final Json snapshot;
  @override Widget build(BuildContext context) {
    final color = context.colorScheme;
    final total = bytes(snapshot['total'] as num? ?? 0).split(' ');
    final parts = <(String, num, Color)>[
      if (snapshot['remote'] != true) ('App', snapshot['app'] as num? ?? 0, color.primary),
      ('Core', snapshot['core'] as num? ?? 0, Color.alphaBlend(color.primary.withValues(alpha: .45), SurfaceLevel.card.colorOf(color))),
    ];
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      Text.rich(TextSpan(children: [
        TextSpan(text: snapshot.isEmpty ? '—' : total.first, style: AppTextStyle.title1.of(context, emphasized: true).toTabular),
        if (snapshot.isNotEmpty) TextSpan(text: ' ${total.last}', style: AppTextStyle.footnote.of(context).copyWith(color: color.onSurfaceVariant)),
      ]), maxLines: 1),
      const SizedBox(height: 14), MemoryBar(parts: parts), const SizedBox(height: 10),
      Wrap(spacing: 16, runSpacing: 4, children: [for (final part in parts) Row(mainAxisSize: MainAxisSize.min, spacing: 6, children: [
        DecoratedBox(decoration: ShapeDecoration(color: part.$3, shape: AppShape.circle), child: const SizedBox.square(dimension: 8)),
        Text(context.tr(part.$1), style: context.listCaptionStyle),
        Text(snapshot.isEmpty ? '—' : bytes(part.$2), style: AppTextStyle.footnote.of(context, emphasized: true).toTabular),
      ])]),
    ]);
  }
}

class MemoryBar extends StatelessWidget {
  const MemoryBar({super.key, required this.parts});
  final List<(String, num, Color)> parts;
  @override Widget build(BuildContext context) {
    final total = parts.fold<num>(0, (sum, part) => sum + math.max(0, part.$2));
    final positive = parts.where((part) => part.$2 > 0).toList();
    return ClipRSuperellipse(borderRadius: AppRadius.full, child: SizedBox(height: 8, child: total == 0
      ? ColoredBox(color: SurfaceLevel.inset.colorOf(context.colorScheme))
      : Row(crossAxisAlignment: CrossAxisAlignment.stretch, children: [for (final (index, part) in positive.indexed) ...[
          if (index > 0) const SizedBox(width: 2),
          TweenAnimationBuilder<double>(tween: Tween(end: (part.$2 * 100 / total).roundToDouble()), duration: context.motionDuration(commonDuration), curve: Easing.standard,
            builder: (_, percent, child) => Expanded(flex: math.max(1, (percent * 100).round()), child: child!), child: ColoredBox(color: part.$3)),
        ]])));
  }
}

class MemoryDetails extends StatefulWidget {
  const MemoryDetails({super.key});
  @override State<MemoryDetails> createState() => _MemoryDetailsState();
}
class _MemoryDetailsState extends State<MemoryDetails> {
  bool _releasing = false;
  Future<void> _release() async {
    setState(() => _releasing = true);
    final box = context.box;
    if (await box.act('dashboard.releaseMemory') && mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(context.tr('Memory released'))));
    if (mounted) setState(() => _releasing = false);
  }
  @override Widget build(BuildContext context) {
    final memory = context.watchBox(BoxAspect.memory).memory, total = (memory['core'] as num?)?.toDouble() ?? 0;
    return BoxDataSubscription(data: BoxData.memory, child: BoxPage(title: 'Memory', loading: _releasing, actions: [
      if (memory['remote'] != true && memory['running'] == true) IconButtonData(glyph: AppGlyphs.broom,
        tooltip: context.tr('Release memory'), onPressed: _releasing ? null : _release),
    ], children: [
      const SizedBox(height: 8), Text(context.tr('Total'), style: AppTextStyle.headline.of(context)), const SizedBox(height: 4),
      MemorySummary(snapshot: memory), const SizedBox(height: 12),
      Text(context.tr(memory['remote'] == true ? 'Memory reported by the remote core. Detailed accounting is unavailable.'
        : 'App and core share one process. Core runtime memory is separated from process RSS, not added twice.'), style: context.listCaptionStyle),
      if (memory['remote'] != true) generateSection(title: context.tr('App'), items: [
        ListItem(title: Text(context.tr('Process RSS')), trailing: Text(bytes(memory['rss'] as num? ?? 0))),
        ListItem(title: Text(context.tr('App and shared memory')), trailing: Text(bytes(memory['app'] as num? ?? 0))),
      ]),
      generateSection(title: context.tr('Core'), items: [
        if (memory['running'] != true && memory['remote'] != true) ListItem(title: Text(context.tr('Not running'))),
        for (final (key, label) in const [('heapInuse', 'Heap in use'), ('heapIdle', 'Retained idle heap'), ('stack', 'Stack'), ('runtimeOther', 'Other runtime memory')])
          if (memory[key] is num) ListItem(title: Text(context.tr(label)), subtitle: Text(bytes(memory[key] as num)),
            trailing: Text('${total == 0 ? '0.0' : ((memory[key] as num) * 100 / total).toStringAsFixed(1)}%')),
        if (memory['goroutines'] != null) ListItem(title: Text(context.tr('Goroutines')), trailing: Text(memory['goroutines'].toString())),
      ]),
    ]));
  }
}
