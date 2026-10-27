import 'dart:math';
import 'package:flutter/foundation.dart' show listEquals, setEquals;
import '../widgets.dart';
import 'group_monogram.dart';
import 'proxy_card.dart';

// Slivers, pinned headers and folding motion adapted from FlClash v0.9.2
// views/proxies/list.dart. Nodes and actions come from the sing-box session.
const _headerInset = 10.0;
const _headerButtonInset = 4.0;
const _groupInset = 16.0;
const _liftDistance = 16.0;
const _lastRowGrowth = 2.0;

double _headerHeight(BuildContext context) {
  final measure = MeasureScope.of(context);
  return 20 + measure.textHeight(AppTextStyle.callout) + 2 + measure.textHeight(AppTextStyle.caption1) + 2;
}

class ProxyListGroup {
  const ProxyListGroup(this.group, this.nodes);
  final Json group;
  final List<Json> nodes;
  String get name => group['tag'].toString();
}

class ProxyListView extends StatefulWidget {
  const ProxyListView({super.key, required this.profileKey, required this.groups,
    required this.testing, required this.cardBuilder, required this.onTest});
  final String profileKey;
  final List<ProxyListGroup> groups;
  final Set<String> testing;
  final Widget Function(BuildContext, Json, Json) cardBuilder;
  final Future<void> Function(String) onTest;
  @override State<ProxyListView> createState() => _ProxyListViewState();
}

class _ProxyListViewState extends State<ProxyListView> {
  late final _controller = sheetScrollController(context);
  Set<String>? _unfolded;
  bool _saving = false;
  int _columns = 1, _groupColumns = 1;
  double _gap = 8;

  @override void didChangeDependencies() {
    super.didChangeDependencies();
    if (!_saving) {
      final saved = json(context.box.preferences['proxyUnfolded'])[widget.profileKey] as List? ?? const [];
      _unfolded = saved.whereType<String>().toSet();
    }
  }
  @override void dispose() { _controller.dispose(); super.dispose(); }

  double _offsetOf(String name) {
    final offsets = List.filled(_groupColumns, 0.0);
    final extent = ProxyCardMetrics.of(context, context.box.preferences).rowExtent;
    final header = _headerHeight(context);
    for (final (index, group) in widget.groups.indexed) {
      final column = index % _groupColumns;
      if (group.name == name) return offsets[column];
      offsets[column] += header + _gap;
      if (_unfolded!.contains(group.name) && group.nodes.isNotEmpty) {
        offsets[column] += ((group.nodes.length + _columns - 1) ~/ _columns) * extent + _lastRowGrowth;
      }
    }
    return 0;
  }

  Future<void> _toggle(String name) async {
    if (_saving) return;
    if (_controller.hasClients) {
      final position = _controller.position, start = _offsetOf(name);
      final visibleHeight = max(position.viewportDimension - context.contentTopPadding, 0.0);
      final end = start + _headerHeight(context);
      if (start < position.pixels || end > position.pixels + visibleHeight) {
        final target = start < position.pixels ? start : end - visibleHeight + 24;
        _controller.jumpTo(target.clamp(position.minScrollExtent, position.maxScrollExtent));
      }
    }
    final old = Set<String>.of(_unfolded!);
    setState(() {
      _saving = true;
      if (!_unfolded!.remove(name)) _unfolded!.add(name);
    });
    final saved = json(context.box.preferences['proxyUnfolded'])..remove(widget.profileKey);
    saved[widget.profileKey] = _unfolded!.toList();
    while (saved.length > 128) { saved.remove(saved.keys.first); }
    final success = await context.box.setPreference('proxyUnfolded', saved);
    if (mounted) setState(() { _saving = false; if (!success) _unfolded = old; });
  }

  void _locate(String name) {
    if (!_controller.hasClients) return;
    final group = widget.groups.firstWhere((group) => group.name == name);
    final index = group.nodes.indexWhere((node) => node['tag'] == group.group['selected']);
    if (index < 0) return;
    final extent = ProxyCardMetrics.of(context, context.box.preferences).rowExtent;
    final offset = _offsetOf(name) + (index ~/ _columns) * extent;
    _controller.animateTo(offset.clamp(_controller.position.minScrollExtent, _controller.position.maxScrollExtent),
      duration: context.motionDuration(const Duration(milliseconds: 300)), curve: Curves.easeOutCubic);
  }

  @override Widget build(BuildContext context) => LayoutBuilder(builder: (context, constraints) {
    _gap = context.isMobileView ? 8 : constraints.maxWidth >= 1024 ? 16 : 12;
    final width = max(constraints.maxWidth - _groupInset * 2, 0.0);
    _groupColumns = context.isMobileView ? 1 : ((width + _gap) / (360 + _gap)).floor().clamp(1, 3);
    final groupWidth = (width - _gap * (_groupColumns - 1)) / _groupColumns;
    _columns = proxyColumns(max(groupWidth - _headerInset * 2, 0), context.box.preferences);
    Widget buildGroup(ProxyListGroup group) => _GroupSliver(
      key: ValueKey(group.name), group: group, columns: _columns, gap: _gap,
      extent: ProxyCardMetrics.of(context, context.box.preferences).rowExtent,
      expanded: _unfolded!.contains(group.name), testing: widget.testing,
      cardBuilder: widget.cardBuilder, onToggle: _toggle, onLocate: _locate, onTest: widget.onTest,
    );
    return CommonScrollBar(controller: _controller, thumbVisibility: true, child: CustomScrollView(
      key: PageStorageKey('proxy-list:${widget.profileKey}'), controller: _controller,
      slivers: [
        PinnedHeaderSliver(child: SizedBox(height: context.contentTopPadding)),
        SliverPadding(padding: const EdgeInsets.symmetric(horizontal: _groupInset),
          sliver: _groupColumns == 1 ? SliverMainAxisGroup(slivers: widget.groups.map(buildGroup).toList())
            : SliverCrossAxisGroup(slivers: [for (var column = 0; column < _groupColumns; column++) ...[
              if (column > 0) SliverConstrainedCrossAxis(maxExtent: _gap, sliver: const SliverToBoxAdapter()),
              SliverCrossAxisExpanded(flex: 1, sliver: SliverMainAxisGroup(slivers: [
                for (var index = column; index < widget.groups.length; index += _groupColumns) buildGroup(widget.groups[index]),
              ])),
            ]]),
        ),
        SliverToBoxAdapter(child: SizedBox(height: 16 + context.bottomInset)),
      ],
    ));
  });
}

class _GroupSliver extends StatefulWidget {
  const _GroupSliver({super.key, required this.group, required this.columns, required this.gap,
    required this.extent, required this.expanded, required this.testing, required this.cardBuilder,
    required this.onToggle, required this.onLocate, required this.onTest});
  final ProxyListGroup group;
  final int columns;
  final double gap, extent;
  final bool expanded;
  final Set<String> testing;
  final Widget Function(BuildContext, Json, Json) cardBuilder;
  final void Function(String) onToggle, onLocate;
  final Future<void> Function(String) onTest;
  @override State<_GroupSliver> createState() => _GroupSliverState();
}

class _GroupSliverState extends State<_GroupSliver> with SingleTickerProviderStateMixin {
  Widget? _rows;
  late final _controller = AnimationController(vsync: this, value: widget.expanded ? 1 : 0);
  late final _unfold = UnfoldEntryTransition.curveOf(_controller);
  int get _rowCount => (widget.group.nodes.length + widget.columns - 1) ~/ widget.columns;
  double get _rowsExtent => _rowCount == 0 ? 0 : _rowCount * widget.extent + _lastRowGrowth;

  @override void initState() {
    super.initState();
    _controller.addStatusListener((_) { if (mounted) setState(() {}); });
  }
  @override void dispose() { _unfold.dispose(); _controller.dispose(); super.dispose(); }
  @override void didUpdateWidget(_GroupSliver oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (widget.expanded != oldWidget.expanded) {
      _controller.duration = context.motionDuration(UnfoldEntryTransition.duration);
      widget.expanded ? _controller.forward() : _controller.reverse();
    }
    if (widget.group.group != oldWidget.group.group || !listEquals(widget.group.nodes, oldWidget.group.nodes) ||
        widget.columns != oldWidget.columns || widget.extent != oldWidget.extent || !setEquals(widget.testing, oldWidget.testing)) {
      _rows = null;
    }
  }

  Widget _buildRow(BuildContext context, int row) => SurfaceScope(level: context.raisedSurfaceLevel,
    child: Padding(padding: const EdgeInsets.only(left: _headerInset, right: _headerInset, bottom: 8),
      child: Row(children: [for (var column = 0; column < widget.columns; column++) ...[
        if (column > 0) const SizedBox(width: 8),
        Expanded(child: row * widget.columns + column < widget.group.nodes.length
          ? SizedBox(height: widget.extent - 8, child: widget.cardBuilder(context, widget.group.group,
              widget.group.nodes[row * widget.columns + column])) : const SizedBox()),
      ]]),
    ),
  );

  Widget _unfoldingRows(ShapeDecoration decoration) {
    final visible = min(_rowCount.toDouble(), MediaQuery.sizeOf(context).height / widget.extent);
    final count = min(_rowCount, visible.floor() + 1);
    return Stack(clipBehavior: Clip.none, children: [
      Positioned(top: -AppCorner.xl.ap(context), left: 0, right: 0, bottom: 0, child: DecoratedBox(decoration: decoration)),
      SizeTransition(sizeFactor: _unfold, alignment: Alignment.topCenter, child: Column(children: [
        for (var index = 0; index < count; index++) UnfoldEntryTransition(animation: _unfold,
          index: index, visibleCount: visible, child: SizedBox(height: widget.extent, child: _buildRow(context, index))),
        if (count == _rowCount) const SizedBox(height: _lastRowGrowth),
      ])),
    ]);
  }

  @override Widget build(BuildContext context) {
    final unfolded = _controller.isCompleted;
    Widget? rows;
    if (!_controller.isDismissed && widget.group.nodes.isNotEmpty) {
      final decoration = ShapeDecoration(color: context.raisedSurfaceLevel.colorOf(context.colorScheme),
        shape: AppShape.vertical(top: AppCorner.none, bottom: AppCorner.xl.ap(context)));
      if (!unfolded) {
        _rows = null;
        rows = SliverToBoxAdapter(child: _unfoldingRows(decoration));
      } else {
        _rows ??= SliverFixedExtentList(itemExtent: widget.extent, delegate: SliverChildBuilderDelegate(
          (context, index) => _buildRow(context, index), childCount: _rowCount, addAutomaticKeepAlives: false));
        rows = DecoratedSliver(decoration: decoration, sliver: SliverMainAxisGroup(slivers: [
          _rows!, const SliverToBoxAdapter(child: SizedBox(height: _lastRowGrowth)),
        ]));
      }
    } else { _rows = null; }
    return SliverPadding(padding: EdgeInsets.only(bottom: widget.gap), sliver: SliverMainAxisGroup(slivers: [
      _pinnedHeader(unfolded), if (rows != null) rows,
    ]));
  }

  Widget _pinnedHeader(bool unfolded) {
    final height = _headerHeight(context);
    final groupExtent = height + (_controller.isDismissed ? 0 : _rowsExtent);
    (double, double, double, Color)? previous;
    Widget? header;
    return SliverLayoutBuilder(builder: (context, constraints) {
      final beyondCache = constraints.remainingCacheExtent <= 0 || constraints.scrollOffset + constraints.cacheOrigin >= groupExtent;
      final under = widget.expanded ? constraints.scrollOffset + constraints.overlap : 0.0;
      final state = beyondCache ? null : (
        (under / _liftDistance).clamp(0.0, 1.0),
        (min(under, (widget.expanded ? _rowsExtent : 0) - under) / _liftDistance).clamp(0.0, 1.0),
        widget.expanded ? constraints.overlap : 0.0,
        context.surfaceLevel.colorOf(context.colorScheme),
      );
      if (header != null && state == previous) return header!;
      previous = state;
      if (state == null) return header = PinnedHeaderSliver(child: SizedBox(height: height));
      final (floating, lift, cover, pageColor) = state;
      return header = PinnedHeaderSliver(child: RepaintBoundary(child: Stack(clipBehavior: Clip.none, children: [
        Positioned(top: -cover, left: 0, right: 0, height: cover + height / 2, child: ColoredBox(color: pageColor)),
        SizedBox(height: height, child: ProxyGroupHeader(group: widget.group.group, expanded: widget.expanded,
          unfolded: unfolded, floating: floating, lift: lift, testing: widget.testing.contains(widget.group.name),
          onToggle: () => widget.onToggle(widget.group.name), onLocate: () => widget.onLocate(widget.group.name),
          onTest: () => widget.onTest(widget.group.name))),
      ])));
    });
  }
}

class ProxyGroupHeader extends StatelessWidget {
  const ProxyGroupHeader({super.key, required this.group, required this.expanded, required this.unfolded,
    required this.floating, required this.lift, required this.testing, required this.onToggle, required this.onLocate, required this.onTest});
  final Json group;
  final bool expanded, unfolded, testing;
  final double floating, lift;
  final VoidCallback onToggle, onLocate, onTest;

  @override Widget build(BuildContext context) {
    final colors = context.colorScheme;
    final level = floating > 0 ? SurfaceLevel.floatingOf(colors) : context.raisedSurfaceLevel;
    final radius = AppCorner.xl.ap(context);
    final strength = colors.brightness == Brightness.dark ? 3.0 : 1.0;
    return DecoratedBox(decoration: ShapeDecoration(color: level.colorOf(colors),
      shape: AppShape.vertical(top: radius, bottom: unfolded ? radius * floating : radius),
      shadows: lift == 0 ? null : [
        BoxShadow(color: colors.shadow.withValues(alpha: .08 * strength * lift), blurRadius: 16, offset: const Offset(0, 4)),
        BoxShadow(color: colors.shadow.withValues(alpha: .04 * strength * lift), blurRadius: 3, offset: const Offset(0, 1)),
      ]),
      child: Padding(padding: const EdgeInsets.all(_headerButtonInset), child: SurfaceScope(
        level: SurfaceLevel.values[level.index - 1], child: CommonCard(enterActionsOnRight: true, enterAnimated: false,
          radius: radius - _headerButtonInset, onPressed: onToggle,
          child: Padding(padding: const EdgeInsets.all(_headerInset - _headerButtonInset), child: Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween, children: [
              Flexible(child: Row(children: [
                _GroupIcon(name: group['tag'].toString()),
                Flexible(child: Column(mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.start, children: [
                  EmojiText(group['tag'].toString(), maxLines: 1, overflow: TextOverflow.ellipsis, style: AppTextStyle.callout.of(context, emphasized: true)),
                  const SizedBox(height: 2),
                  Flexible(child: EmojiText(group['selected'] as String? ?? '', maxLines: 1, overflow: TextOverflow.ellipsis,
                    style: AppTextStyle.caption1.of(context, emphasized: true).toLight)),
                ])),
              ])),
              TonalButtonTheme(size: TonalButtonSize.compact, child: Row(children: [
                if (expanded) TonalButtonGroup(size: TonalButtonSize.compact, children: [
                  IconButton(tooltip: context.tr('Scroll to selected'), onPressed: onLocate, iconSize: 19, icon: const GlyphIcon(AppGlyphs.locate)),
                  IconButton(tooltip: context.tr('Test latency'), onPressed: testing || !context.box.running ? null : onTest,
                    icon: testing ? CommonCircleLoading(size: TonalButtonSize.compact.icon) : const GlyphIcon(AppGlyphs.bolt)),
                ]) else Text(group['type'] as String? ?? (group['selectable'] == true ? 'Selector' : 'URLTest'),
                  maxLines: 1, overflow: TextOverflow.ellipsis, style: AppTextStyle.caption1.of(context, emphasized: true).toLight),
                const SizedBox(width: 6),
                ElasticPress(child: IconButton(tooltip: context.tr(expanded ? 'Show less' : 'Show more'),
                  onPressed: onToggle, icon: CommonExpandIcon(expand: expanded))),
              ])),
            ],
          )),
        ),
      )),
    );
  }
}

class _GroupIcon extends StatelessWidget {
  const _GroupIcon({required this.name});
  final String name;
  @override Widget build(BuildContext context) => switch (context.box.preferences['proxyIconStyle'] ?? 'Filled') {
    'Filled' => LayoutBuilder(builder: (context, constraints) => Container(margin: const EdgeInsets.only(right: 12),
      child: AspectRatio(aspectRatio: 1, child: Container(alignment: Alignment.center,
        padding: EdgeInsets.all(6.ap(context)), clipBehavior: Clip.antiAlias,
        decoration: ShapeDecoration(color: context.raisedSurfaceLevel.colorOf(context.colorScheme),
          shape: AppShape.all(AppCorner.xl.ap(context) - _headerInset)),
        child: IconTheme.merge(data: IconThemeData(size: constraints.maxHeight - 12.ap(context),
          color: context.colorScheme.onSurfaceVariant), child: GroupMonogram(name)),
      )))),
    'Plain' => Container(margin: const EdgeInsets.only(left: 2, right: 10), child: LayoutBuilder(
      builder: (context, constraints) => IconTheme.merge(data: IconThemeData(size: constraints.maxHeight,
        color: context.colorScheme.primary), child: GroupMonogram(name)))),
    _ => const SizedBox(width: 4),
  };
}
