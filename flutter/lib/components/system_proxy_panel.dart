// Adapted from FlClash v0.9.2 network_takeover.dart (VpnCard) and card.dart (GPL-3.0).
import 'package:material_new_shapes/material_new_shapes.dart';
import '../widgets.dart';
import 'shape_badge.dart';
import 'system_proxy_glyph.dart';

class SystemProxyPanel extends StatelessWidget {
  const SystemProxyPanel({super.key});

  @override
  Widget build(BuildContext context) {
    final box = context.box, colors = context.colorScheme;
    final enabled = box.dashboard['systemProxy'] == true;
    final title = context.tr('System HTTP proxy');
    void change(bool value) => box.act('systemProxy.set', {'enabled': value});
    return Semantics(
      container: true,
      label: title,
      child: BoxPanel(
        padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 16),
        clipBehavior: Clip.none,
        onTap: () => change(!enabled),
        child: Row(spacing: 14, children: [
          ShapeBadge(
            shape: enabled ? MaterialShapes.cookie6Sided : MaterialShapes.circle,
            color: enabled ? colors.primary : SurfaceLevel.block.colorOf(colors),
            size: 48,
            child: GlyphIcon(systemProxyGlyph, size: 22,
              color: enabled ? colors.onPrimary : colors.onSurfaceVariant),
          ),
          Expanded(child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Text(title, style: AppTextStyle.callout.of(context, emphasized: true)),
            Text(context.tr(enabled ? 'Enabled' : 'Disabled'),
              maxLines: 1, overflow: TextOverflow.ellipsis,
              style: AppTextStyle.footnote.of(context).copyWith(color: colors.onSurfaceVariant)),
          ])),
          SizedBox(
            width: CommonSwitch.trackWidth,
            height: CommonSwitch.trackHeight,
            child: OverflowBox(
              maxWidth: double.infinity,
              maxHeight: double.infinity,
              child: CommonSwitch(value: enabled, semanticLabel: title, onChanged: change),
            ),
          ),
        ]),
      ),
    );
  }
}
