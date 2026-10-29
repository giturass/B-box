import 'dart:async';
import 'dart:math' as math;
import 'dart:ui' show lerpDouble;
import 'package:flutter/foundation.dart';
import 'package:flutter/rendering.dart';
import '../widgets.dart';
import 'system_proxy_glyph.dart';

// In-place editor and motion adapted from FlClash v0.9.2 dashboard/edit.dart.
const _rowHeight = 64.0;
const _rowGap = 8.0;
const _addableTop = 14.0;
const _swayLoop = Duration(milliseconds: 3600);
const _swaysPerLoop = [6, 7, 5];
const _swayPhaseStep = 1.7;
const _swayTurn = .7 * math.pi / 180;
const _swayBob = 1.25;
const _roomDuration = Duration(milliseconds: 260);
const _appearDuration = Duration(milliseconds: 200);
const _appearScale = .92;
const _liftGrowth = .03;

const dashboardGlyphs = <String, Glyph>{
  'Status': AppGlyphs.status, 'Network speed': AppGlyphs.speed, 'Outbound mode': AppGlyphs.split,
  'Current profile': AppGlyphs.profiles, 'Proxy groups': AppGlyphs.layers,
  'Memory': AppGlyphs.cpu, 'Intranet IP': AppGlyphs.devices, 'System HTTP proxy': systemProxyGlyph,
};
const _descriptions = <String, String>{
  'Memory': 'App and core memory', 'Intranet IP': 'Device network addresses',
  'Network speed': 'Upload and download traffic', 'Current profile': 'Selected sing-box configuration', 'Proxy groups': 'Groups and selected nodes',
  'Outbound mode': 'Available routing modes',
};

class DashboardEditor extends StatefulWidget {
  const DashboardEditor({super.key, required this.shown, required this.available, required this.cardKeyOf,
    required this.cardBuilder, required this.padding, required this.gap,
    required this.onChanged, required this.onBusyChanged, required this.onDone});
  final List<String> shown, available;
  final GlobalKey Function(String) cardKeyOf;
  final Widget Function(String) cardBuilder;
  final EdgeInsets padding;
  final double gap;
  final Future<bool> Function(List<String>) onChanged;
  final ValueChanged<bool> onBusyChanged;
  final VoidCallback onDone;
  @override DashboardEditorState createState() => DashboardEditorState();
}

class DashboardEditorState extends State<DashboardEditor> with SingleTickerProviderStateMixin {
  late final _sway = AnimationController(vsync: this, duration: _swayLoop);
  late var _cards = List<String>.of(widget.shown);
  final _leaving = <String>{}, _entering = <String>{};
  bool _saving = false, _dirty = false;
  List<String> get _shown => [for (final card in _cards) if (!_leaving.contains(card)) card];

  @override void didChangeDependencies() {
    super.didChangeDependencies();
    if (context.disableAnimations) _sway.stop();
    else if (!_sway.isAnimating) _sway.repeat();
  }
  @override void didUpdateWidget(DashboardEditor oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (!listEquals(widget.available, oldWidget.available)) {
      _cards.removeWhere((card) => !widget.available.contains(card));
      _leaving.removeWhere((card) => !widget.available.contains(card));
      _entering.removeWhere((card) => !widget.available.contains(card));
    }
    if (!_saving && !_dirty && !listEquals(widget.shown, _shown)) {
      _cards = List.of(widget.shown); _leaving.clear(); _entering.clear();
    }
  }
  @override void dispose() { _sway.dispose(); super.dispose(); }

  Future<bool> _save() async {
    if (_saving) return false;
    setState(() => _saving = true);
    widget.onBusyChanged(true);
    final success = await widget.onChanged(_shown);
    if (!mounted) return success;
    setState(() { _saving = false; if (success) _dirty = false; });
    widget.onBusyChanged(false);
    return success;
  }
  Future<void> finish() async {
    if (_saving) return;
    if (_dirty && !await _save()) return;
    if (mounted) widget.onDone();
  }
  void _remove(String card) {
    if (_saving || card == 'Status') return;
    setState(() { _leaving.add(card); _dirty = true; });
    unawaited(_save());
  }
  void _add(String card) {
    if (_saving) return;
    setState(() {
      if (!_leaving.remove(card)) { _entering.add(card); _cards = [..._cards, card]; }
      _dirty = true;
    });
    unawaited(_save());
  }
  void _reorder(int from, int to) {
    if (_saving) return;
    setState(() {
      final reordered = List<String>.of(_cards);
      reordered.insert(to, reordered.removeAt(from));
      _cards = reordered; _dirty = true;
    });
    unawaited(_save());
  }
  void _settle(String card) {
    _entering.remove(card);
    if (mounted && _leaving.remove(card)) setState(() => _cards = [..._cards]..remove(card));
  }

  @override Widget build(BuildContext context) {
    final addable = [for (final card in widget.available) if (!_shown.contains(card)) card];
    final sway = context.disableAnimations ? null : _sway;
    final revealPadding = EdgeInsets.only(top: widget.padding.top, bottom: widget.padding.bottom);
    Widget spaced(Widget child) => Padding(padding: EdgeInsets.only(bottom: widget.gap), child: child);
    return BackLayerScope(onBack: finish, child: SliverPadding(padding: widget.padding, sliver: SliverMainAxisGroup(slivers: [
      SliverReorderableList(itemCount: _cards.length, itemBuilder: (context, index) {
        final card = _cards[index];
        return _Presence(key: ValueKey(card), entering: _entering.contains(card), leaving: _leaving.contains(card),
          revealPadding: revealPadding, onSettled: () => _settle(card), child: spaced(_EditableCard(
            name: card, card: widget.cardBuilder(card), cardKey: widget.cardKeyOf(card), index: index,
            seed: dashboardGlyphs.keys.toList().indexOf(card), sway: sway,
            onRemove: _saving || card == 'Status' ? null : () => _remove(card),
          )));
      }, proxyDecorator: (child, index, animation) => spaced(_LiftedCard(key: const ValueKey('dashboard-drag-proxy'), animation: animation,
        child: AbsorbPointer(child: RepaintBoundary(key: widget.cardKeyOf(_cards[index]), child: widget.cardBuilder(_cards[index]))))),
        onReorderItem: _reorder,
      ),
      if (addable.isNotEmpty) SliverPadding(padding: const EdgeInsets.only(top: _addableTop), sliver: SliverList.list(children: [
        _SectionLabel(context.tr('Available cards')),
        for (final card in addable) Padding(padding: const EdgeInsets.only(bottom: _rowGap), child: _AddRow(
          name: card, glyph: dashboardGlyphs[card]!, description: _descriptions[card] == null ? null : context.tr(_descriptions[card]!),
          onAdd: _saving ? null : () => _add(card))),
      ])),
    ])));
  }
}

class _Presence extends StatefulWidget {
  const _Presence({
    super.key,
    required this.entering,
    required this.leaving,
    required this.revealPadding,
    required this.onSettled,
    required this.child,
  });

  final bool entering;
  final bool leaving;

  /// What floats over the viewport's edges, kept clear of an opening room.
  final EdgeInsets revealPadding;
  final VoidCallback onSettled;
  final Widget child;

  @override
  State<_Presence> createState() => _PresenceState();
}

class _PresenceState extends State<_Presence>
    with SingleTickerProviderStateMixin {
  final _content = GlobalKey();
  late final _controller = AnimationController(
    vsync: this,
    value: widget.entering ? 0 : 1,
  );
  late final _roomShare =
      _roomDuration.inMicroseconds /
      (_roomDuration + _appearDuration).inMicroseconds;
  late final _room = CurvedAnimation(
    parent: _controller,
    curve: Interval(0, _roomShare, curve: Curves.easeOutCubic),
    reverseCurve: Interval(0, _roomShare, curve: Curves.easeInCubic),
  );
  late final _appear = CurvedAnimation(
    parent: _controller,
    curve: Interval(_roomShare, 1, curve: Curves.easeOutCubic),
  );
  VoidCallback? _follow;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _controller.duration = context.motionDuration(
      _roomDuration + _appearDuration,
    );
    if (widget.entering && _controller.isDismissed) {
      _run();
      WidgetsBinding.instance.addPostFrameCallback((_) => _reveal());
    }
  }

  @override
  void didUpdateWidget(_Presence oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (widget.leaving != oldWidget.leaving) {
      _run();
    }
  }

  @override
  void dispose() {
    _stopFollowing();
    _room.dispose();
    _appear.dispose();
    _controller.dispose();
    super.dispose();
  }

  void _run() {
    final run = widget.leaving ? _controller.reverse() : _controller.forward();
    run.then((_) {
      if (mounted) {
        widget.onSettled();
      }
    });
  }

  void _reveal() {
    final box = _content.currentContext?.findRenderObject();
    final position = Scrollable.maybeOf(context)?.position;
    if (!mounted || box is! RenderBox || !box.hasSize || position == null) {
      return;
    }
    final viewport = RenderAbstractViewport.maybeOf(box);
    if (viewport == null) {
      return;
    }
    final rect = widget.revealPadding.inflateRect(Offset.zero & box.size);
    final leading = viewport.getOffsetToReveal(box, 0, rect: rect).offset;
    final trailing = viewport.getOffsetToReveal(box, 1, rect: rect).offset;
    final from = position.pixels;
    final to = clampDouble(
      trailing > leading ? leading : clampDouble(from, trailing, leading),
      position.minScrollExtent,
      position.maxScrollExtent + box.size.height * (1 - _room.value),
    );
    if ((to - from).abs() < precisionErrorTolerance) {
      return;
    }
    // Moved once each frame has laid the grown room out: a jump past the old
    // scroll end would send the position bouncing back to it.
    void follow() {
      final room = _room.value;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted && position.hasPixels) {
          position.jumpTo(lerpDouble(from, to, room)!);
        }
      });
      if (room == 1) {
        _stopFollowing();
      }
    }

    _follow = follow;
    _room.addListener(follow);
  }

  void _stopFollowing() {
    final follow = _follow;
    if (follow != null) {
      _room.removeListener(follow);
      _follow = null;
    }
  }

  @override
  Widget build(BuildContext context) {
    return IgnorePointer(
      ignoring: widget.leaving,
      child: AnimatedBuilder(
        animation: _controller,
        builder: (_, child) => Align(
          alignment: AlignmentDirectional.topStart,
          heightFactor: _room.value,
          child: KeyedSubtree(
            key: _content,
            child: Opacity(
              opacity: _appear.value,
              child: Transform.scale(
                scale: lerpDouble(_appearScale, 1, _appear.value)!,
                child: child,
              ),
            ),
          ),
        ),
        child: widget.child,
      ),
    );
  }
}

class _EditableCard extends StatelessWidget {
  const _EditableCard({
    required this.name,
    required this.card,
    required this.seed,
    required this.cardKey,
    required this.index,
    required this.sway,
    required this.onRemove,
  });

  final String name;
  final Widget card;
  final int seed;
  final GlobalKey cardKey;
  final int index;
  final Animation<double>? sway;
  final VoidCallback? onRemove;

  @override
  Widget build(BuildContext context) {
    final colorScheme = context.colorScheme;
    final card = AbsorbPointer(
      child: RepaintBoundary(key: cardKey, child: this.card),
    );
    final onRemove = this.onRemove;
    final content = Stack(
      fit: StackFit.passthrough,
      children: [
        ReorderableDelayedDragStartListener(index: index, child: card),
        if (onRemove != null)
          PositionedDirectional(
            top: 0,
            end: 0,
            child: _RoundButton(
              glyph: AppGlyphs.remove,
              tooltip:
                  '${context.tr('Remove')} ${context.tr(name)}',
              background: colorScheme.errorContainer,
              foreground: colorScheme.onErrorContainer,
              onPressed: onRemove,
            ),
          ),
      ],
    );
    final sway = this.sway;
    // The sway repaints every frame; the boundary keeps that to this card.
    return RepaintBoundary(
      child: sway == null
          ? content
          : _Sway(sway: sway, seed: seed, child: content),
    );
  }
}

class _Sway extends AnimatedWidget {
  const _Sway({
    required Animation<double> sway,
    required this.seed,
    required this.child,
  }) : super(listenable: sway);

  final int seed;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final swings = _swaysPerLoop[seed % _swaysPerLoop.length];
    final turn =
        (listenable as Animation<double>).value * 2 * math.pi * swings +
        seed * _swayPhaseStep;
    return Transform.translate(
      offset: Offset(0, math.cos(turn) * _swayBob),
      child: Transform.rotate(angle: math.sin(turn) * _swayTurn, child: child),
    );
  }
}

class _LiftedCard extends StatelessWidget {
  const _LiftedCard({super.key, required this.animation, required this.child});

  final Animation<double> animation;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final shadows = DashboardShadows.e2(context);
    final shape = AppShape.all(34);
    return AnimatedBuilder(
      animation: animation,
      builder: (context, child) {
        final t = Curves.easeOut.transform(animation.value);
        return Transform.scale(
          scale: 1 + _liftGrowth * t,
          child: DecoratedBox(
            decoration: ShapeDecoration(
              shape: shape,
              shadows: [
                for (final shadow in shadows)
                  shadow.copyWith(
                    color: shadow.color.withValues(alpha: shadow.color.a * t),
                  ),
              ],
            ),
            child: child,
          ),
        );
      },
      child: Material(type: MaterialType.transparency, child: child),
    );
  }
}

class _SectionLabel extends StatelessWidget {
  const _SectionLabel(this.text);

  final String text;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(8, 4, 8, 10),
      child: Text(
        text,
        style: AppTextStyle.footnote
            .of(context, emphasized: true)
            .copyWith(color: context.colorScheme.onSurfaceVariant),
      ),
    );
  }
}

class _AddRow extends StatelessWidget {
  const _AddRow({required this.name, required this.glyph, required this.description, required this.onAdd});

  final String name;
  final Glyph glyph;
  final String? description;
  final VoidCallback? onAdd;

  @override
  Widget build(BuildContext context) {
    final colorScheme = context.colorScheme;
    final cardColor = SurfaceLevel.card.colorOf(colorScheme);
    final label = context.tr(name);
    final description = this.description;
    return Container(
      height: _rowHeight,
      padding: const EdgeInsetsDirectional.fromSTEB(12, 0, 8, 0),
      decoration: ShapeDecoration(
        color: cardColor.withValues(alpha: 0.55),
        shape: AppShape.all(22),
      ),
      child: Row(
        spacing: 12,
        children: [
          _RoundButton(
            glyph: AppGlyphs.add,
            tooltip: '${context.tr('Add')} $label',
            background: colorScheme.primary,
            foreground: colorScheme.onPrimary,
            onPressed: onAdd,
          ),
          Container(
            width: 40,
            height: 40,
            decoration: ShapeDecoration(
              color: cardColor,
              shape: AppShape.all(14),
            ),
            child: Center(
              child: GlyphIcon(
                glyph,
                size: 20,
                color: colorScheme.onSurfaceVariant,
              ),
            ),
          ),
          Expanded(
            child: Column(
              mainAxisAlignment: MainAxisAlignment.center,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  label,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: AppTextStyle.subheadline.of(context, emphasized: true),
                ),
                if (description != null)
                  Text(
                    description,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: AppTextStyle.caption1.of(context).toTabular.copyWith(color: colorScheme.onSurfaceVariant),
                  ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _RoundButton extends StatelessWidget {
  const _RoundButton({
    required this.glyph,
    required this.tooltip,
    required this.background,
    required this.foreground,
    required this.onPressed,
  });

  final Glyph glyph;
  final String tooltip;
  final Color background;
  final Color foreground;
  final VoidCallback? onPressed;

  @override
  Widget build(BuildContext context) {
    return SizedBox.square(
      dimension: 28,
      child: IconButton(
        tooltip: tooltip,
        padding: EdgeInsets.zero,
        style: IconButton.styleFrom(
          backgroundColor: background,
          foregroundColor: foreground,
          minimumSize: const Size.square(28),
          fixedSize: const Size.square(28),
          tapTargetSize: MaterialTapTargetSize.shrinkWrap,
        ),
        onPressed: onPressed,
        icon: GlyphIcon(glyph, size: 16, color: foreground),
      ),
    );
  }
}

abstract final class DashboardShadows {
  static List<BoxShadow> e1(BuildContext context) =>
      context.colorScheme.brightness == Brightness.dark ? _e1Dark : _e1Light;

  static List<BoxShadow> e2(BuildContext context) =>
      context.colorScheme.brightness == Brightness.dark ? _e2Dark : _e2Light;

  static BoxShadow _shadow(
    int alpha,
    double y,
    double cssBlur, [
    double spread = 0,
  ]) => BoxShadow(
    color: Color.fromARGB(alpha, 0, 0, 0),
    offset: Offset(0, y),
    blurRadius: _blur(cssBlur),
    spreadRadius: spread,
  );

  static double _blur(double cssBlur) =>
      cssBlur <= 1 ? 0 : (cssBlur / 2 - 0.5) / 0.57735;

  static final _e1Light = [
    _shadow(0x0D, 0.5, 1),
    _shadow(0x0A, 2, 6),
    _shadow(0x17, 12, 32, -6),
  ];

  static final _e1Dark = [_shadow(0x80, 1, 2), _shadow(0x8C, 10, 28, -6)];

  static final _e2Light = [
    _shadow(0x0F, 1, 3),
    _shadow(0x14, 10, 24),
    _shadow(0x38, 32, 64, -12),
  ];

  static final _e2Dark = [_shadow(0x80, 2, 6), _shadow(0xB3, 32, 64, -12)];
}

