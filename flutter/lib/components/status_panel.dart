import 'dart:async';
import 'dart:math' as math;
import 'package:flutter/foundation.dart' show ValueListenable, ValueNotifier;
import 'package:flutter/services.dart';
import 'package:material_new_shapes/material_new_shapes.dart';
import '../widgets.dart';
import '../pages/profiles.dart';
import 'start_toggle.dart';
import 'shape_badge.dart';
import 'dashboard_chip.dart';
import '../common/ip_quality.dart';
import '../common/ip_quality_cache.dart';
import '../common/routed_ip_probe.dart';

/// Mobile layout from FlClash's current_outbound.dart, with sing-box data.
class StatusPanel extends StatefulWidget {
  const StatusPanel({super.key});
  @override State<StatusPanel> createState() => _StatusPanelState();
}
class _StatusPanelState extends State<StatusPanel> with WidgetsBindingObserver {
  bool _busy = false, _checking = false, _qualityChecking = false;
  Json? _egress;
  String? _error, _qualityError, _stamp;
  RoutedIpProbe? _probe;
  Timer? _qualityExpiry;
  final _detailsRevision = ValueNotifier(0);
  int _generation = 0;
  bool _foreground = true;
  @override void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _foreground = WidgetsBinding.instance.lifecycleState == null || WidgetsBinding.instance.lifecycleState == AppLifecycleState.resumed;
  }
  @override void didChangeAppLifecycleState(AppLifecycleState state) {
    _foreground = state == AppLifecycleState.resumed;
    if (_foreground) {
      if (mounted && context.box.running && !context.box.remote) _refresh();
    } else {
      _generation++;
      _probe?.cancel(); _probe = null;
      _qualityExpiry?.cancel();
      if (mounted) setState(() { _checking = false; _qualityChecking = false; });
    }
  }
  @override void didChangeDependencies() {
    super.didChangeDependencies();
    final box = context.box;
    final stamp = '${box.running}:${box.remote}:${box.dashboard['remote']}:${box.dashboard['startedAt']}:${box.selectedProfileId}:${box.dashboard['mode']}:${box.groups.map((g) => '${g['tag']}:${g['selected']}').join(',')}:${box.lan['addresses']}';
    if (_stamp == stamp) return;
    _stamp = stamp;
    _generation++;
    _probe?.cancel(); _probe = null;
    _qualityExpiry?.cancel();
    _egress = null; _error = null; _qualityError = null; _checking = false; _qualityChecking = false;
    if (_foreground && box.running && !box.remote) WidgetsBinding.instance.addPostFrameCallback((_) { if (mounted) _refresh(); });
  }
  Future<void> _refresh() async {
    final box = context.box;
    if (!_foreground || _checking || _qualityChecking || !box.running || box.remote) return;
    final retryQualityFor = _qualityError == null ? null : _egress?['ip'];
    final generation = ++_generation;
    _qualityExpiry?.cancel();
    final probe = RoutedIpProbe(box);
    _probe?.cancel(); _probe = probe;
    setState(() { _checking = true; _qualityChecking = false; _error = null; _qualityError = null; });
    try {
      final result = await probe.egress();
      if (!mounted || generation != _generation) return;
      final ip = result['ip'] as String?;
      setState(() { _egress = result.isEmpty ? null : result; _checking = false; _qualityChecking = ip != null; });
      if (ip == null) return;
      try {
        final record = await box.ipQualities.lookup(probe, ip, refresh: ip == retryQualityFor);
        if (!mounted || generation != _generation) return;
        setState(() { result.addAll(record.quality.toDashboard(record.checkedAt)); _qualityChecking = false; });
        _detailsRevision.value++;
        final remaining = record.checkedAt.add(IpQualityCache.maxAge).difference(DateTime.now());
        _qualityExpiry = Timer(remaining.isNegative ? Duration.zero : remaining, () {
          if (mounted && generation == _generation) _refresh();
        });
      } catch (error) {
        if (mounted && generation == _generation) {
          setState(() {
            _qualityChecking = false;
            _qualityError = BoxController.describe(error);
            result['qualityError'] = _qualityError;
            if (error is IpQualityLookupException) result['qualityFailures'] = [
              for (final failure in error.failures) {'source': failure.source.label, 'status': failure.status.name},
            ];
          });
          _detailsRevision.value++;
        }
      }
    } catch (error) { if (mounted && generation == _generation) setState(() => _error = BoxController.describe(error)); }
    finally {
      await probe.close();
      if (mounted && generation == _generation) setState(() { _checking = false; _qualityChecking = false; _probe = null; });
    }
  }
  @override void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _generation++; _probe?.cancel(); _qualityExpiry?.cancel(); _detailsRevision.dispose();
    super.dispose();
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
          SizedBox(height: 26 * MediaQuery.textScalerOf(context).scale(1), child: FittedBox(fit: BoxFit.scaleDown, alignment: AlignmentDirectional.centerStart, child: Row(mainAxisSize: MainAxisSize.min, spacing: 8, children: [
            GlyphIcon(AppGlyphs.status, size: 18, color: color.onSurfaceVariant),
            Text(context.tr('Status'), maxLines: 1, style: AppTextStyle.subheadline.of(context, emphasized: true)),
            DashboardChip(label: context.tr(status), tone: box.running ? DashboardChipTone.ok : DashboardChipTone.idle, dot: box.running),
          ]))),
          const SizedBox(height: 12),
          Row(spacing: 12, children: [
            _CountryBadge(country: info?['country'] as String?, active: box.running, checking: _checking || _qualityChecking,
              onRefresh: box.running && !box.remote && !_checking && !_qualityChecking ? _refresh : null),
            Expanded(child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
              SizedBox(height: AppTextStyle.title2.leading * MediaQuery.textScalerOf(context).scale(1), child: Align(alignment: AlignmentDirectional.centerStart,
                child: _checking && ip == null ? SkeletonText(width: 140, style: AppTextStyle.title2.of(context))
                  : InkWell(onTap: ip == null ? null : () => openForm(context, EgressDetails(info: info!, updates: _detailsRevision)),
                    child: FittedBox(fit: BoxFit.scaleDown, alignment: AlignmentDirectional.centerStart,
                      child: Text(ip ?? (_error == null ? '—' : context.tr('Check failed')), maxLines: 1,
                        style: AppTextStyle.title2.of(context, emphasized: true).toTabular.copyWith(color: _error == null ? foreground : color.error, letterSpacing: 0))))),
              ),
              const SizedBox(height: 4),
              SizedBox(height: 28, child: ClipRect(child: _error != null && ip == null
                ? Tooltip(message: _error!, child: Text(context.tr('Tap icon to retry'), maxLines: 1, overflow: TextOverflow.ellipsis, style: context.listCaptionStyle))
                : Wrap(spacing: 6, runSpacing: 28, children: [
                    if ((_checking && ip == null) || _qualityChecking) ...[const SkeletonPill(width: 48, height: 28), const SkeletonPill(width: 72, height: 28)]
                    else ...[
                      Tooltip(message: _qualityError == null ? '' : context.tr('IP quality unavailable'),
                        child: DashboardChip(label: context.tr(info?['type'] as String? ?? 'Unknown type'), large: true)),
                      if (info?['quality'] != null) DashboardChip(label: '${context.tr('Quality')} ${context.tr(info!['quality'] as String)}', large: true,
                        tone: info['quality'] == 'Good' ? DashboardChipTone.ok : info['quality'] == 'Risky' ? DashboardChipTone.warn : DashboardChipTone.idle),
                    ],
                  ]))),
            ])),
          ]),
          const SizedBox(height: 16),
          Row(spacing: 24, children: [
            Flexible(child: _StatusCell(label: 'Run time', child: FittedBox(fit: BoxFit.scaleDown, alignment: AlignmentDirectional.centerStart,
              child: _RuntimeValue(startedAt: box.running ? (box.dashboard['startedAt'] as num?)?.toInt() : null)))),
            Flexible(child: _StatusCell(label: 'Organization', child: (_checking && info == null) || _qualityChecking
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
  const EgressDetails({super.key, required this.info, this.updates});
  final Json info;
  final ValueListenable<int>? updates;
  @override Widget build(BuildContext context) => updates == null ? _page(context)
    : ValueListenableBuilder<int>(valueListenable: updates!, builder: (context, _, _) => _page(context));
  Widget _page(BuildContext context) => BoxPage(title: 'Outbound IP', actions: [
    IconButtonData(glyph: AppGlyphs.copy, tooltip: context.tr('Copy'), onPressed: () => Clipboard.setData(ClipboardData(text: info['ip'] as String))),
  ], children: [DetailRows({'IP': info['ip'], 'Country': info['country'], 'Organization': info['organization'],
    'ASN': info['asn'], 'Type': context.tr(info['type'] as String? ?? 'Unknown type'),
    if (info['quality'] != null) 'Quality': context.tr(info['quality'] as String),
    'IP query source': info['ipSource'], 'Quality query source': info['source'],
    'Last checked': localDate(info['qualityCheckedAt'] ?? info['checkedAt'])}),
    if (info['qualityError'] != null) generateSection(title: context.tr('IP quality unavailable'), items: [
      for (final failure in rows(info['qualityFailures'])) ListItem(title: Text(failure['source'].toString()),
        trailing: Text(context.tr({'noType': 'No type data', 'timeout': 'Timeout', 'rateLimited': 'Rate limited',
          'failed': 'Unavailable', 'ipMismatch': 'IP mismatch'}[failure['status']] ?? 'Unavailable'))),
    ]),
  ]);
}
