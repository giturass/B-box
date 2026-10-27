import 'dart:async';
import 'dart:math' as math;
import 'package:flutter/services.dart';
import 'package:material_new_shapes/material_new_shapes.dart';
import '../widgets.dart';
import '../pages/profiles.dart';
import 'start_toggle.dart';
import 'shape_badge.dart';
import 'dashboard_chip.dart';

/// Mobile layout from FlClash's current_outbound.dart, with sing-box data.
class StatusPanel extends StatefulWidget {
  const StatusPanel({super.key});
  @override State<StatusPanel> createState() => _StatusPanelState();
}
class _StatusPanelState extends State<StatusPanel> {
  bool _busy = false, _checking = false;
  Json? _egress;
  String? _error, _stamp;
  int _generation = 0;
  @override void didChangeDependencies() {
    super.didChangeDependencies();
    final box = context.box;
    final stamp = '${box.running}:${box.remote}:${box.dashboard['remote']}:${box.dashboard['startedAt']}:${box.selectedProfileId}:${box.dashboard['mode']}:${box.groups.map((g) => '${g['tag']}:${g['selected']}').join(',')}:${box.lan['addresses']}';
    if (_stamp == stamp) return;
    _stamp = stamp;
    _generation++;
    _egress = null; _error = null; _checking = false;
    if (box.running && !box.remote) WidgetsBinding.instance.addPostFrameCallback((_) { if (mounted) _refresh(); });
  }
  Future<void> _refresh() async {
    final box = context.box;
    if (_checking || !box.running || box.remote) return;
    final generation = ++_generation;
    setState(() { _checking = true; _error = null; });
    try {
      final result = json(await box.call('dashboard.egress'));
      if (mounted && generation == _generation) setState(() => _egress = result.isEmpty ? null : result);
    } catch (error) { if (mounted && generation == _generation) setState(() => _error = BoxController.describe(error)); }
    if (mounted && generation == _generation) setState(() => _checking = false);
  }
  Future<void> _toggle() async {
    final box = context.box;
    if (_busy || box.transitioning) return;
    if (box.selectedProfile == null) { await openForm(context, const AddProfilePage()); return; }
    setState(() => _busy = true);
    await box.act(box.running ? 'service.stop' : 'service.start');
    if (mounted) setState(() => _busy = false);
  }
  @override Widget build(BuildContext context) {
    final box = context.box, color = context.colorScheme;
    final status = box.transitioning ? box.dashboard['status'].toString() : box.running ? 'Running' : 'Not running';
    final info = _egress;
    final ip = info?['ip'] as String?;
    final foreground = box.running ? color.onSurface : color.onSurfaceVariant;
    return BoxPanel(padding: EdgeInsets.zero, child: Stack(children: [
      PositionedDirectional(end: -60, top: -66, child: TweenAnimationBuilder<double>(
        tween: Tween(end: box.running ? 1 : 0), duration: context.motionDuration(const Duration(milliseconds: 450)), curve: Curves.easeOutBack,
        builder: (_, progress, _) => Transform.rotate(angle: math.pi / 6 * progress, child: Transform.scale(scale: 1 + .12 * progress,
          child: ShapeBadge(shape: MaterialShapes.cookie12Sided, size: 240,
            color: Color.lerp(SurfaceLevel.block.colorOf(color), Color.alphaBlend(color.primary.withValues(alpha: .1), SurfaceLevel.card.colorOf(color)), progress.clamp(0, 1))!))),
      )),
      Padding(padding: const EdgeInsetsDirectional.fromSTEB(20, 18, 16, 18), child: Row(spacing: 10, children: [
        Expanded(child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          SizedBox(height: 26 * MediaQuery.textScalerOf(context).scale(1), child: Row(spacing: 8, children: [
            GlyphIcon(AppGlyphs.status, size: 18, color: color.onSurfaceVariant),
            Flexible(child: Text(context.tr('Status'), maxLines: 1, overflow: TextOverflow.ellipsis, style: AppTextStyle.subheadline.of(context, emphasized: true))),
            DashboardChip(label: context.tr(status), tone: box.running ? DashboardChipTone.ok : DashboardChipTone.idle, dot: box.running),
          ])),
          const SizedBox(height: 12),
          Row(spacing: 12, children: [
            _CountryBadge(country: info?['country'] as String?, active: box.running, checking: _checking,
              onRefresh: box.running && !box.remote && !_checking ? _refresh : null),
            Expanded(child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
              SizedBox(height: AppTextStyle.title2.leading * MediaQuery.textScalerOf(context).scale(1), child: Align(alignment: AlignmentDirectional.centerStart,
                child: _checking && ip == null ? SkeletonText(width: 140, style: AppTextStyle.title2.of(context))
                  : InkWell(onTap: ip == null ? null : () => openForm(context, EgressDetails(info: info!)),
                    child: FittedBox(fit: BoxFit.scaleDown, alignment: AlignmentDirectional.centerStart,
                      child: Text(ip ?? (_error == null ? '—' : context.tr('Check failed')), maxLines: 1,
                        style: AppTextStyle.title2.of(context, emphasized: true).toTabular.copyWith(color: _error == null ? foreground : color.error, letterSpacing: 0))))),
              ),
              const SizedBox(height: 4),
              SizedBox(height: 28, child: ClipRect(child: _error != null && ip == null
                ? Tooltip(message: _error!, child: Text(context.tr('Tap icon to retry'), maxLines: 1, overflow: TextOverflow.ellipsis, style: context.listCaptionStyle))
                : Wrap(spacing: 6, runSpacing: 28, children: [
                    if (_checking && ip == null) ...[const SkeletonPill(width: 48, height: 28), const SkeletonPill(width: 72, height: 28)]
                    else ...[
                      DashboardChip(label: context.tr(info?['type'] as String? ?? 'Unknown type'), large: true),
                      if (info?['quality'] != null) DashboardChip(label: '${context.tr('Quality')} ${context.tr(info!['quality'] as String)}', large: true,
                        tone: info['quality'] == 'Good' ? DashboardChipTone.ok : info['quality'] == 'Risky' ? DashboardChipTone.warn : DashboardChipTone.idle),
                    ],
                  ]))),
            ])),
          ]),
          const SizedBox(height: 16),
          Row(spacing: 24, children: [
            _StatusCell(label: 'Run time', child: _RuntimeValue(startedAt: box.running ? (box.dashboard['startedAt'] as num?)?.toInt() : null)),
            Flexible(child: _StatusCell(label: 'Organization', child: _checking && info == null
              ? SkeletonText(width: 96, style: AppTextStyle.subheadline.of(context))
              : Text((info?['organization'] as String?)?.isNotEmpty == true ? info!['organization'] as String : info?['asn'] as String? ?? '—',
                maxLines: 1, overflow: TextOverflow.ellipsis, style: AppTextStyle.subheadline.of(context, emphasized: true)))),
          ]),
        ])),
        SizedBox(width: 88, child: Center(child: box.remote ? const GlyphIcon(AppGlyphs.proxies, size: 60) : StartToggle(
          size: 88, enabled: !_busy && !box.transitioning, running: box.running, suspended: false, onTap: _toggle))),
      ])),
    ]));
  }
}

class _CountryBadge extends StatefulWidget {
  const _CountryBadge({this.country, required this.active, required this.checking, this.onRefresh});
  final String? country;
  final bool active, checking;
  final VoidCallback? onRefresh;
  @override State<_CountryBadge> createState() => _CountryBadgeState();
}
class _CountryBadgeState extends State<_CountryBadge> with SingleTickerProviderStateMixin {
  late final _turn = AnimationController(vsync: this, duration: const Duration(milliseconds: 1500));
  @override void didChangeDependencies() { super.didChangeDependencies(); _spin(); }
  @override void didUpdateWidget(_CountryBadge oldWidget) { super.didUpdateWidget(oldWidget); _spin(); }
  void _spin() { if (widget.checking && !context.disableAnimations) { if (!_turn.isAnimating) _turn.repeat(); } else { _turn.stop(); } }
  @override void dispose() { _turn.dispose(); super.dispose(); }
  @override Widget build(BuildContext context) {
    final color = widget.active ? context.colorScheme.onSurface : context.colorScheme.onSurfaceVariant;
    return Tooltip(message: context.tr('Retry'), child: Semantics(button: true, label: context.tr('Retry'), enabled: widget.onRefresh != null,
      child: InkResponse(onTap: widget.onRefresh, child: AnimatedBuilder(animation: _turn, builder: (_, child) => ShapeBadge(
        shape: MaterialShapes.clover4Leaf, color: SurfaceLevel.inset.colorOf(context.colorScheme), size: 48,
        rotation: _turn.value * 2 * math.pi, child: child), child: widget.country?.isNotEmpty == true
        ? Text(widget.country!.toUpperCase(), style: AppTextStyle.footnote.of(context, emphasized: true).copyWith(color: color, letterSpacing: .52))
        : GlyphIcon(AppGlyphs.proxies, size: 48 * .42, color: color)))));
  }
}

class _StatusCell extends StatelessWidget {
  const _StatusCell({required this.label, required this.child});
  final String label;
  final Widget child;
  @override Widget build(BuildContext context) => Column(crossAxisAlignment: CrossAxisAlignment.start, mainAxisSize: MainAxisSize.min, children: [
    Text(context.tr(label), maxLines: 1, overflow: TextOverflow.ellipsis, style: context.listCaptionStyle), const SizedBox(height: 2), child,
  ]);
}
class _RuntimeValue extends StatefulWidget {
  const _RuntimeValue({this.startedAt});
  final int? startedAt;
  @override State<_RuntimeValue> createState() => _RuntimeValueState();
}
class _RuntimeValueState extends State<_RuntimeValue> {
  Timer? _timer;
  @override void initState() { super.initState(); _timer = Timer.periodic(const Duration(seconds: 1), (_) { if (mounted && widget.startedAt != null) setState(() {}); }); }
  @override void dispose() { _timer?.cancel(); super.dispose(); }
  @override Widget build(BuildContext context) {
    final seconds = widget.startedAt == null ? 0 : math.max(0, (DateTime.now().millisecondsSinceEpoch - widget.startedAt!) ~/ 1000);
    final text = widget.startedAt == null ? '—' : [seconds ~/ 3600, seconds ~/ 60 % 60, seconds % 60].map((n) => n.toString().padLeft(2, '0')).join(':');
    return Text(text, maxLines: 1, style: AppTextStyle.subheadline.of(context, emphasized: true).toTabular);
  }
}

class EgressDetails extends StatelessWidget {
  const EgressDetails({super.key, required this.info});
  final Json info;
  @override Widget build(BuildContext context) => BoxPage(title: 'Outbound IP', actions: [
    IconButtonData(glyph: AppGlyphs.copy, tooltip: context.tr('Copy'), onPressed: () => Clipboard.setData(ClipboardData(text: info['ip'] as String))),
  ], children: [DetailRows({'IP': info['ip'], 'Country': info['country'], 'City': info['city'], 'Organization': info['organization'],
    'ASN': info['asn'], 'Type': context.tr(info['type'] as String), if (info['quality'] != null) 'Quality': context.tr(info['quality'] as String),
    'Source': info['source'], 'Last checked': localDate(info['checkedAt'])})]);
}
