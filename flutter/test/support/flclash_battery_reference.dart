// Frozen FlClash v0.9.3 battery prerequisite presentation oracle (GPL-3.0).
// FlClash: be84736605527034689c95bf27828ff27d912c5e.
//   lib/views/config/on_demand.dart (button/item extraction):
//     ff0589492b45c088fc5af13e52a43879280506ecdf2cd7e6b2994168cda5011f
//   arb/intl_en.arb and arb/intl_zh_CN.arb (three literal labels):
//     df1944a2f56803cb934be3488140fbb3c499e2e08ef5d4c622c814f076ab3659
//     a468bdd94f3f4d7917413dc4c746150b136982c738254c04b11e5831663e0080
// Prism submodule: 63450c0ac0e8e1b2c0f3ed4ad7926dc94e083185.
//   lib/list_selected.dart (DecorationListItem extraction):
//     4a1c3e10bb2ef62949fe9fb16e1e0a79683c6389d5fc73e8f230d1c2ccb2d07c
//   lib/list_row_separator.dart:
//     b1c09367c59536a0e213d61d2c0f01d3eedf26a3a7ca35eaec26d702abbc8853
//   lib/list_row.dart (complete original renderer):
//     d0668749464cf24a59691de7b3a89b45468a137bce3a95aa80031f384ef548fa
// Provider state, permission action and localization inputs become fixtures.
// Class names/imports change to isolate the retained layout and render objects.
// This file does not import the B-box battery component or its modified ListRow.
// Shared runtime dependencies and the frozen metrics have separate source pins
// in the provenance manifest.
// Licenses: ../../packages/prism_ui/LICENSE and ../../assets/licenses/LICENSE.

// Preserve the original constructors after isolating upstream types as private.
// ignore_for_file: unused_element_parameter

import 'dart:math' as math;

import 'package:b_box_ui/widgets.dart' hide ListRow, ListRowAlignment;
import 'package:flutter/rendering.dart';
import 'package:flutter/scheduler.dart';

class FlClashBatteryOptimizationItem extends StatefulWidget {
  const FlClashBatteryOptimizationItem({
    super.key,
    this.authorized = false,
    this.loading = false,
  });

  final bool authorized;
  final bool loading;

  @override
  State<FlClashBatteryOptimizationItem> createState() =>
      _FlClashBatteryOptimizationItemState();
}

class _FlClashBatteryOptimizationItemState
    extends State<FlClashBatteryOptimizationItem> {
  static const _authorizeButtonPadding = 12.0;
  static const _minAuthorizeButtonWidth = 80.0;

  void _handleRequestIgnoreBatteryOptimization() {}

  @override
  Widget build(BuildContext context) => _buildBatteryOptimizationItem();

  Widget _buildAuthorizeButton({
    required bool authorized,
    required VoidCallback onPressed,
  }) {
    final appLocalizations = context.appLocalizations;
    return MinFilledButtonTheme(
      child: ElasticButton(
        child: FilledButton(
          style: ButtonStyle.from(
            backgroundColor: authorized
                ? null
                : context.colorScheme.errorContainer,
            foregroundColor: authorized
                ? null
                : context.colorScheme.onErrorContainer,
            padding: const EdgeInsets.symmetric(
              horizontal: _authorizeButtonPadding,
            ),
            minimumSize: const Size(_minAuthorizeButtonWidth, 40),
          ),
          onPressed: onPressed,
          child: Stack(
            alignment: Alignment.center,
            children: [
              ExcludeSemantics(
                child: Opacity(
                  opacity: 0,
                  child: Text(
                    authorized
                        ? appLocalizations.tapToAuthorize
                        : appLocalizations.authorized,
                  ),
                ),
              ),
              Text(
                authorized
                    ? appLocalizations.authorized
                    : appLocalizations.tapToAuthorize,
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildPrerequisiteItem({
    required String title,
    required Widget action,
  }) {
    return _FrozenDecorationListItem(title: TooltipLabel(title), trailing: action);
  }

  Widget _buildBatteryOptimizationItem() {
    final appLocalizations = context.appLocalizations;
    final ignored = _BatteryIgnoredFixture(widget.authorized, widget.loading);
    final isLoading = ignored.isLoading;
    return _buildPrerequisiteItem(
      title: appLocalizations.ignoreBatteryOptimization,
      action: Stack(
        alignment: Alignment.centerRight,
        children: [
          Visibility(
            visible: !isLoading,
            maintainSize: true,
            maintainAnimation: true,
            maintainState: true,
            child: _buildAuthorizeButton(
              authorized: ignored.value ?? false,
              onPressed: _handleRequestIgnoreBatteryOptimization,
            ),
          ),
          if (isLoading) const ActivityIndicator(size: 24),
        ],
      ),
    );
  }

}

class _BatteryIgnoredFixture {
  const _BatteryIgnoredFixture(this.value, this.isLoading);

  final bool? value;
  final bool isLoading;
}

class _BatteryLocalizations {
  const _BatteryLocalizations(this.chinese);

  final bool chinese;

  String get ignoreBatteryOptimization =>
      chinese ? '忽略电池优化' : 'Ignore battery optimization';
  String get tapToAuthorize => chinese ? '点击授权' : 'Tap to authorize';
  String get authorized => chinese ? '已授权' : 'Authorized';
}

extension _FlClashBatteryContext on BuildContext {
  _BatteryLocalizations get appLocalizations =>
      _BatteryLocalizations(Localizations.localeOf(this).languageCode == 'zh');
}

bool _isEnd(ItemPosition? position) =>
    position == ItemPosition.end || position == ItemPosition.startAndEnd;

BorderRadius _frozenListItemRadiusOf(ItemPosition? position) {
  final isStart =
      position == ItemPosition.start || position == ItemPosition.startAndEnd;
  return AppRadius.vertical(
    top: isStart ? AppCorner.xl : AppCorner.none,
    bottom: _isEnd(position) ? AppCorner.xl : AppCorner.none,
  );
}

class _FrozenDecorationListItem extends StatefulWidget {
  final Widget title;
  final Widget? subtitle;
  final Widget? leading;
  final Widget? trailing;
  final bool? isSelected;
  final double? horizontalTitleGap;
  final EdgeInsetsGeometry? contentPadding;
  final VoidCallback? onPressed;
  final double? minVerticalPadding;
  final bool invalid;

  const _FrozenDecorationListItem({
    super.key,
    this.contentPadding,
    required this.title,
    this.leading,
    this.trailing,
    this.subtitle,
    this.isSelected,
    this.onPressed,
    this.horizontalTitleGap,
    this.minVerticalPadding,
    this.invalid = false,
  });

  @override
  State<_FrozenDecorationListItem> createState() => _FrozenDecorationListItemState();
}

class _FrozenDecorationListItemState extends State<_FrozenDecorationListItem> {
  final _states = WidgetStatesController();

  @override
  void dispose() {
    _states.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final proxy = ProxyDecoratorProvider.of(context);
    final proxyDecorator = proxy?.isProxyDecorator ?? false;
    final sortable = SortableItem.maybeOf(context);
    final sorting = sortable?.sorting ?? false;
    final position = ItemPositionProvider.of(context)?.position;
    final isEnd = _isEnd(position);
    final row = _ListRowSeparator(
      separated: !proxyDecorator && !isEnd,
      emphasized: widget.invalid,
      states: _states,
      endIndent: 14,
      child: Card(
        shape: AppShape.of(_frozenListItemRadiusOf(position)),
        isError: widget.invalid,
        isSelected: widget.isSelected,
        padding: EdgeInsets.zero,
        statesController: _states,
        inert: sorting,
        onPressed: proxyDecorator ? null : widget.onPressed,
        child: LayoutBuilder(
          builder: (_, constraints) {
            final tile = _FrozenListRow(
              leading: widget.leading == null
                  ? null
                  : IgnorePointer(ignoring: sorting, child: widget.leading),
              padding:
                  widget.contentPadding ??
                  const EdgeInsets.only(right: 16, left: 16),
              title: _ListRowSeparatorStart(child: widget.title),
              subtitle: widget.subtitle,
              minVerticalPadding:
                  widget.minVerticalPadding ?? _frozenRowVerticalPadding,
              horizontalTitleGap: widget.horizontalTitleGap,
              trailing: sortable == null
                  ? widget.trailing
                  : SortableTrailing(item: sortable, child: widget.trailing),
            );
            if (constraints.maxHeight >= double.infinity) {
              return tile;
            }
            return SizedBox(height: constraints.maxHeight, child: tile);
          },
        ),
      ),
    );
    if (!proxyDecorator) {
      return row;
    }
    // A change of the card's shape eases in over the button's own animation
    // duration, which would trail the lift; the clip follows it frame by frame.
    return ClipRSuperellipse(
      borderRadius: AppRadius.all(AppCorner.xl * proxy!.lift),
      child: row,
    );
  }
}


/// Draws a row's bottom separator from where its title starts, and drops it
/// whenever this row or the one below is highlighted, as iOS grouped lists
/// do. The row below is found through the render tree, so a list only has to
/// stack its rows for them to see each other.
class _ListRowSeparator extends SingleChildRenderObjectWidget {
  final bool separated;
  final bool emphasized;
  final WidgetStatesController states;
  final double endIndent;

  const _ListRowSeparator({
    required this.separated,
    required this.emphasized,
    required this.states,
    required this.endIndent,
    required super.child,
  });

  Color _color(BuildContext context) => Divider.colorOf(context);

  @override
  _RenderListRowSeparator createRenderObject(BuildContext context) =>
      _RenderListRowSeparator(
        separated: separated,
        emphasized: emphasized,
        states: states,
        remote: RemoteFocusAdapter.isEnabledOf(context),
        endIndent: endIndent,
        color: _color(context),
        textDirection: Directionality.of(context),
        devicePixelRatio: MediaQuery.devicePixelRatioOf(context),
      );

  @override
  void updateRenderObject(
    BuildContext context,
    _RenderListRowSeparator renderObject,
  ) {
    renderObject
      ..separated = separated
      ..emphasized = emphasized
      ..states = states
      ..remote = RemoteFocusAdapter.isEnabledOf(context)
      ..endIndent = endIndent
      ..color = _color(context)
      ..textDirection = Directionality.of(context)
      ..devicePixelRatio = MediaQuery.devicePixelRatioOf(context);
    // A reorder hands a row a new neighbour below without changing any of
    // its own properties.
    if (separated) {
      renderObject.markNeedsPaint();
    }
  }
}

class _ListRowSeparatorStart extends SingleChildRenderObjectWidget {
  const _ListRowSeparatorStart({required super.child});

  @override
  RenderObject createRenderObject(BuildContext context) =>
      _RenderListRowSeparatorStart();
}

class _RenderListRowSeparatorStart extends RenderProxyBox {
  _RenderListRowSeparator? _row;

  @override
  void attach(PipelineOwner owner) {
    super.attach(owner);
    var node = parent;
    while (node != null && node is! _RenderListRowSeparator) {
      node = node.parent;
    }
    _row = node as _RenderListRowSeparator?;
    _row?._start = this;
  }

  @override
  void detach() {
    if (_row?._start == this) {
      _row!._start = null;
    }
    _row = null;
    super.detach();
  }
}

class _RenderListRowSeparator extends RenderProxyBox {
  _RenderListRowSeparator({
    required bool separated,
    required bool emphasized,
    required WidgetStatesController states,
    required bool remote,
    required double endIndent,
    required Color color,
    required TextDirection textDirection,
    required double devicePixelRatio,
  }) : _separated = separated,
       _emphasized = emphasized,
       _states = states,
       _remote = remote,
       _endIndent = endIndent,
       _color = color,
       _textDirection = textDirection,
       _devicePixelRatio = devicePixelRatio {
    _highlighted = _resolveHighlighted();
  }

  static const _maxSearchDepth = 32;

  _RenderListRowSeparatorStart? _start;
  late bool _highlighted;
  double? _paintedOverhang;
  double? _settlingOverhang;
  bool _realignRequested = false;

  bool _separated;

  set separated(bool value) {
    if (_separated == value) {
      return;
    }
    _separated = value;
    markNeedsPaint();
  }

  bool _emphasized;

  set emphasized(bool value) {
    if (_emphasized == value) {
      return;
    }
    _emphasized = value;
    _updateHighlighted();
  }

  WidgetStatesController _states;

  set states(WidgetStatesController value) {
    if (_states == value) {
      return;
    }
    if (attached) {
      _states.removeListener(_updateHighlighted);
      value.addListener(_updateHighlighted);
    }
    _states = value;
    _updateHighlighted();
  }

  bool _remote;

  set remote(bool value) {
    if (_remote == value) {
      return;
    }
    _remote = value;
    _updateHighlighted();
  }

  double _endIndent;

  set endIndent(double value) {
    if (_endIndent == value) {
      return;
    }
    _endIndent = value;
    markNeedsPaint();
  }

  Color _color;

  set color(Color value) {
    if (_color == value) {
      return;
    }
    _color = value;
    markNeedsPaint();
  }

  TextDirection _textDirection;

  set textDirection(TextDirection value) {
    if (_textDirection == value) {
      return;
    }
    _textDirection = value;
    markNeedsPaint();
  }

  double _devicePixelRatio;

  set devicePixelRatio(double value) {
    if (_devicePixelRatio == value) {
      return;
    }
    _devicePixelRatio = value;
    markNeedsPaint();
  }

  double get _thickness {
    final pixels = (_devicePixelRatio * 0.75).round();
    return (pixels < 1 ? 1 : pixels) / _devicePixelRatio;
  }

  bool _resolveHighlighted() {
    if (_emphasized) {
      return true;
    }
    final states = _states.value;
    return states.contains(WidgetState.pressed) ||
        (states.contains(WidgetState.focused) &&
            (_remote || FocusMovedByKey.instance.value));
  }

  void _updateHighlighted() {
    final highlighted = _resolveHighlighted();
    if (_highlighted == highlighted) {
      return;
    }
    _highlighted = highlighted;
    markNeedsPaint();
    _neighbour(below: false)?.markNeedsPaint();
  }

  _RenderListRowSeparator? _neighbour({required bool below}) {
    RenderObject node = this;
    for (
      var depth = 0;
      node.parentData is! ContainerParentDataMixin<RenderObject>;
      depth++
    ) {
      final parent = node.parent;
      if (parent == null || depth == _maxSearchDepth) {
        return null;
      }
      node = parent;
    }
    final siblings = node.parentData! as ContainerParentDataMixin<RenderObject>;
    RenderObject? candidate = below
        ? siblings.nextSibling
        : siblings.previousSibling;
    for (var depth = 0; candidate != null; depth++) {
      if (candidate is _RenderListRowSeparator) {
        return candidate;
      }
      if (depth == _maxSearchDepth) {
        return null;
      }
      RenderObject? onlyChild;
      var childCount = 0;
      candidate.visitChildren((child) {
        onlyChild = child;
        childCount++;
      });
      candidate = childCount == 1 ? onlyChild : null;
    }
    return null;
  }

  @override
  void attach(PipelineOwner owner) {
    super.attach(owner);
    _states.addListener(_updateHighlighted);
    FocusMovedByKey.instance.addListener(_updateHighlighted);
    _highlighted = _resolveHighlighted();
    _PixelRealignment.watch(this);
  }

  @override
  void detach() {
    _PixelRealignment.unwatch(this);
    _states.removeListener(_updateHighlighted);
    FocusMovedByKey.instance.removeListener(_updateHighlighted);
    super.detach();
  }

  /// Null while an ancestor has stopped painting the row, such as a sliver
  /// that moved it into its keep-alive bucket: its paint transform is then
  /// all zeros, which [localToGlobal] turns into NaN.
  double? _pixelOverhang() {
    final deviceBottom =
        localToGlobal(Offset(0, size.height)).dy * _devicePixelRatio;
    if (!deviceBottom.isFinite) {
      return null;
    }
    return deviceBottom - (deviceBottom + 1e-3).floorToDouble();
  }

  /// Returns whether the row needs another frame, either to see it stop
  /// moving or to repaint: this runs after the frame has painted, when a
  /// [markNeedsPaint] no longer schedules one.
  bool _realign() {
    final painted = _paintedOverhang;
    if (painted == null) {
      return false;
    }
    final overhang = _pixelOverhang();
    if (overhang == null) {
      return false;
    }
    bool same(double? other) =>
        other != null && (overhang - other).abs() < 0.01;
    if (same(painted)) {
      _settlingOverhang = null;
      return false;
    }
    if (!same(_settlingOverhang)) {
      _settlingOverhang = overhang;
      return true;
    }
    if (_realignRequested) {
      return false;
    }
    _realignRequested = true;
    markNeedsPaint();
    return true;
  }

  @override
  void performLayout() {
    super.performLayout();
    // A lazily built row arrives below a row that has already painted.
    if (_highlighted) {
      _neighbour(below: false)?.markNeedsPaint();
    }
  }

  @override
  void paint(PaintingContext context, Offset offset) {
    super.paint(context, offset);
    _paintedOverhang = null;
    _realignRequested = false;
    if (!_separated || _highlighted) {
      return;
    }
    if (_neighbour(below: true)?._highlighted ?? false) {
      return;
    }
    final start = _start;
    final title = start != null && start.hasSize
        ? MatrixUtils.transformRect(
            start.getTransformTo(this),
            Offset.zero & start.size,
          )
        : null;
    final (left, right) = switch (_textDirection) {
      TextDirection.ltr => (title?.left ?? 0.0, size.width - _endIndent),
      TextDirection.rtl => (_endIndent, title?.right ?? size.width),
    };
    // Layout is not snapped to device pixels, and a line straddling pixel rows
    // renders with faint, blurred edges.
    final overhang = _pixelOverhang() ?? 0;
    _paintedOverhang = overhang;
    final bottom = size.height - overhang / _devicePixelRatio;
    context.canvas.drawRect(
      Rect.fromLTRB(left, bottom - _thickness, right, bottom).shift(offset),
      Paint()..color = _color,
    );
  }
}

/// Scrolling, sheets and transitions move a painted row without repainting
/// it, which can leave its line between device pixels. Once nothing is moving
/// any more, a row whose line drifted off the pixel grid paints again.
abstract final class _PixelRealignment {
  static final _rows = <_RenderListRowSeparator>{};
  static var _listening = false;

  static void watch(_RenderListRowSeparator row) {
    _rows.add(row);
    if (!_listening) {
      _listening = true;
      SchedulerBinding.instance.addPersistentFrameCallback(_check);
    }
  }

  static void unwatch(_RenderListRowSeparator row) => _rows.remove(row);

  static void _check(Duration _) {
    if (_rows.isEmpty || SchedulerBinding.instance.hasScheduledFrame) {
      return;
    }
    var needsFrame = false;
    for (final row in _rows) {
      needsFrame = row._realign() || needsFrame;
    }
    if (needsFrame) {
      SchedulerBinding.instance.scheduleFrame();
    }
  }
}

enum _FrozenListRowAlignment { center, top }

const _frozenRowVerticalPadding = 10.0;
const _frozenRowMinHeight = 56.0;

/// A row of leading, title, subtitle and trailing laid out as a Material 3
/// one- or two-line list tile, without the tile's ink, focus or semantics:
/// the card or row around it answers presses.
class _FrozenListRow extends StatelessWidget {
  const _FrozenListRow({
    super.key,
    this.leading,
    required this.title,
    this.subtitle,
    this.trailing,
    this.padding = const EdgeInsets.symmetric(horizontal: 16),
    this.horizontalTitleGap,
    this.minVerticalPadding = _frozenRowVerticalPadding,
    this.minHeight = _frozenRowMinHeight,
    this.alignment = _FrozenListRowAlignment.center,
  });

  final Widget? leading;
  final Widget title;
  final Widget? subtitle;
  final Widget? trailing;
  final EdgeInsetsGeometry padding;
  final double? horizontalTitleGap;
  final double minVerticalPadding;
  final double minHeight;
  final _FrozenListRowAlignment alignment;

  static const _defaultTitleGap = 16.0;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final colorScheme = theme.colorScheme;
    final textDirection = Directionality.of(context);
    final safe = MediaQuery.paddingOf(context);
    final inset = padding.resolve(textDirection);
    final titleStyle = AppTextStyle.body.of(context).copyWith(color: colorScheme.onSurface);
    final subtitleStyle = AppTextStyle.subheadline.of(context).copyWith(color: colorScheme.onSurfaceVariant);
    final sideStyle = AppTextStyle.caption2
        .of(context, emphasized: true)
        .copyWith(color: colorScheme.onSurfaceVariant);
    final iconColor = colorScheme.onSurfaceVariant;
    // As ListTile does: an icon theme a slot sets must not recolor a button.
    final iconButtonColor =
        ButtonTheme.of(context).icon?.foregroundColor?.resolve(const {}) ??
        iconColor;
    final leading = this.leading;
    final subtitle = this.subtitle;
    final trailing = this.trailing;
    return IconTheme.merge(
      data: IconThemeData(color: iconColor),
      child: ButtonTheme.merge(
        data: ButtonThemeData(
          icon: ButtonStyle(
            foregroundColor: WidgetStatePropertyAll(iconButtonColor),
          ),
        ),
        child: _ListRowLayout(
          padding: EdgeInsets.fromLTRB(
            math.max(safe.left, inset.left),
            math.max(0, inset.top),
            math.max(safe.right, inset.right),
            math.max(0, inset.bottom),
          ),
          visualDensity: theme.visualDensity,
          textDirection: textDirection,
          titleBaseline: titleStyle.textBaseline ?? TextBaseline.alphabetic,
          subtitleBaseline:
              subtitleStyle.textBaseline ?? TextBaseline.alphabetic,
          horizontalTitleGap: horizontalTitleGap ?? _defaultTitleGap,
          minVerticalPadding: minVerticalPadding,
          minHeight: minHeight,
          alignment: alignment,
          leading: leading == null
              ? null
              : DefaultTextStyle(style: sideStyle, child: leading),
          title: DefaultTextStyle(style: titleStyle, child: title),
          subtitle: subtitle == null
              ? null
              : DefaultTextStyle(style: subtitleStyle, child: subtitle),
          trailing: trailing == null
              ? null
              : DefaultTextStyle(style: sideStyle, child: trailing),
        ),
      ),
    );
  }
}

enum _Slot { leading, title, subtitle, trailing }

class _ListRowLayout
    extends SlottedMultiChildRenderObjectWidget<_Slot, RenderBox> {
  const _ListRowLayout({
    required this.padding,
    required this.visualDensity,
    required this.textDirection,
    required this.titleBaseline,
    required this.subtitleBaseline,
    required this.horizontalTitleGap,
    required this.minVerticalPadding,
    required this.minHeight,
    required this.alignment,
    this.leading,
    required this.title,
    this.subtitle,
    this.trailing,
  });

  final EdgeInsets padding;
  final VisualDensity visualDensity;
  final TextDirection textDirection;
  final TextBaseline titleBaseline;
  final TextBaseline subtitleBaseline;
  final double horizontalTitleGap;
  final double minVerticalPadding;
  final double minHeight;
  final _FrozenListRowAlignment alignment;
  final Widget? leading;
  final Widget title;
  final Widget? subtitle;
  final Widget? trailing;

  @override
  Iterable<_Slot> get slots => _Slot.values;

  @override
  Widget? childForSlot(_Slot slot) => switch (slot) {
    _Slot.leading => leading,
    _Slot.title => title,
    _Slot.subtitle => subtitle,
    _Slot.trailing => trailing,
  };

  @override
  _RenderListRow createRenderObject(BuildContext context) => _RenderListRow(
    padding: padding,
    visualDensity: visualDensity,
    textDirection: textDirection,
    titleBaseline: titleBaseline,
    subtitleBaseline: subtitleBaseline,
    horizontalTitleGap: horizontalTitleGap,
    minVerticalPadding: minVerticalPadding,
    minHeight: minHeight,
    alignment: alignment,
  );

  @override
  void updateRenderObject(BuildContext context, _RenderListRow renderObject) {
    renderObject
      ..padding = padding
      ..visualDensity = visualDensity
      ..textDirection = textDirection
      ..titleBaseline = titleBaseline
      ..subtitleBaseline = subtitleBaseline
      ..horizontalTitleGap = horizontalTitleGap
      ..minVerticalPadding = minVerticalPadding
      ..minHeight = minHeight
      ..alignment = alignment;
  }
}

typedef _Sizes = ({double titleY, BoxConstraints text, Size size});

// The geometry of a Material 3 ListTile that is neither dense nor three-line.
class _RenderListRow extends RenderBox
    with SlottedContainerRenderObjectMixin<_Slot, RenderBox> {
  _RenderListRow({
    required EdgeInsets padding,
    required VisualDensity visualDensity,
    required TextDirection textDirection,
    required TextBaseline titleBaseline,
    required TextBaseline subtitleBaseline,
    required double horizontalTitleGap,
    required double minVerticalPadding,
    required double minHeight,
    required _FrozenListRowAlignment alignment,
  }) : _padding = padding,
       _visualDensity = visualDensity,
       _textDirection = textDirection,
       _titleBaseline = titleBaseline,
       _subtitleBaseline = subtitleBaseline,
       _horizontalTitleGap = horizontalTitleGap,
       _minVerticalPadding = minVerticalPadding,
       _minHeight = minHeight,
       _alignment = alignment;

  static const _minLeadingWidth = 24.0;
  static const _minTrailingWidth = 32.0;
  static const _maxSideHeight = 56.0;
  static const _titleBaselineY = 32.0;
  static const _subtitleBaselineY = 52.0;

  RenderBox? get _leading => childForSlot(_Slot.leading);
  RenderBox get _title => childForSlot(_Slot.title)!;
  RenderBox? get _subtitle => childForSlot(_Slot.subtitle);
  RenderBox? get _trailing => childForSlot(_Slot.trailing);

  @override
  Iterable<RenderBox> get children => [
    ?_leading,
    ?childForSlot(_Slot.title),
    ?_subtitle,
    ?_trailing,
  ];

  EdgeInsets _padding;

  set padding(EdgeInsets value) {
    if (_padding == value) {
      return;
    }
    _padding = value;
    markNeedsLayout();
  }

  VisualDensity _visualDensity;

  set visualDensity(VisualDensity value) {
    if (_visualDensity == value) {
      return;
    }
    _visualDensity = value;
    markNeedsLayout();
  }

  TextDirection _textDirection;

  set textDirection(TextDirection value) {
    if (_textDirection == value) {
      return;
    }
    _textDirection = value;
    markNeedsLayout();
  }

  TextBaseline _titleBaseline;

  set titleBaseline(TextBaseline value) {
    if (_titleBaseline == value) {
      return;
    }
    _titleBaseline = value;
    markNeedsLayout();
  }

  TextBaseline _subtitleBaseline;

  set subtitleBaseline(TextBaseline value) {
    if (_subtitleBaseline == value) {
      return;
    }
    _subtitleBaseline = value;
    markNeedsLayout();
  }

  double _horizontalTitleGap;

  set horizontalTitleGap(double value) {
    if (_horizontalTitleGap == value) {
      return;
    }
    _horizontalTitleGap = value;
    markNeedsLayout();
  }

  double _minVerticalPadding;

  set minVerticalPadding(double value) {
    if (_minVerticalPadding == value) {
      return;
    }
    _minVerticalPadding = value;
    markNeedsLayout();
  }

  double _minHeight;

  set minHeight(double value) {
    if (_minHeight == value) {
      return;
    }
    _minHeight = value;
    markNeedsLayout();
  }

  _FrozenListRowAlignment _alignment;

  set alignment(_FrozenListRowAlignment value) {
    if (_alignment == value) {
      return;
    }
    _alignment = value;
    markNeedsLayout();
  }

  Rect _content = Rect.zero;

  double get _titleGap => _horizontalTitleGap + _visualDensity.horizontal * 2;

  double _sideY(double height, double rowHeight) => switch (_alignment) {
    _FrozenListRowAlignment.center => (rowHeight - height) / 2,
    _FrozenListRowAlignment.top => _minVerticalPadding,
  };

  double _intrinsicWidth(
    double height,
    double Function(RenderBox box, double height) measure,
  ) {
    final inner = math.max(0.0, height - _padding.vertical);
    final leading = _leading;
    final subtitle = _subtitle;
    final trailing = _trailing;
    final leadingWidth = leading == null
        ? 0.0
        : math.max(measure(leading, inner), _minLeadingWidth) + _titleGap;
    return leadingWidth +
        math.max(
          measure(_title, inner),
          subtitle == null ? 0.0 : measure(subtitle, inner),
        ) +
        (trailing == null ? 0.0 : trailing.getMaxIntrinsicWidth(inner)) +
        _padding.horizontal;
  }

  @override
  double computeMinIntrinsicWidth(double height) => _intrinsicWidth(
    height,
    (box, height) => box.getMinIntrinsicWidth(height),
  );

  @override
  double computeMaxIntrinsicWidth(double height) => _intrinsicWidth(
    height,
    (box, height) => box.getMaxIntrinsicWidth(height),
  );

  @override
  double computeMinIntrinsicHeight(double width) {
    final inner = math.max(0.0, width - _padding.horizontal);
    final content =
        _title.getMinIntrinsicHeight(inner) +
        (_subtitle?.getMinIntrinsicHeight(inner) ?? 0) +
        2 * _minVerticalPadding;
    return math.max(_minHeight, content) + _padding.vertical;
  }

  @override
  double computeMaxIntrinsicHeight(double width) =>
      computeMinIntrinsicHeight(width);

  @override
  double? computeDistanceToActualBaseline(TextBaseline baseline) {
    final parentData = _title.parentData! as BoxParentData;
    return (BaselineOffset(_title.getDistanceToActualBaseline(baseline)) +
            parentData.offset.dy)
        .offset;
  }

  _Sizes _computeSizes(
    ChildBaselineGetter getBaseline,
    ChildLayouter layoutChild,
    BoxConstraints constraints, {
    void Function(RenderBox child, Offset offset)? position,
  }) {
    final loose = constraints.deflate(_padding).loosen();
    final width = loose.maxWidth;
    final sideConstraints = loose.enforce(
      BoxConstraints(
        maxHeight: _maxSideHeight + _visualDensity.baseSizeAdjustment.dy,
      ),
    );
    final leading = _leading;
    final subtitle = _subtitle;
    final trailing = _trailing;
    final leadingSize = leading == null
        ? null
        : layoutChild(leading, sideConstraints);
    final trailingSize = trailing == null
        ? null
        : layoutChild(trailing, sideConstraints);
    final titleStart = leadingSize == null
        ? 0.0
        : math.max(_minLeadingWidth, leadingSize.width) + _titleGap;
    final trailingWidth = trailingSize == null
        ? 0.0
        : math.max(trailingSize.width + _titleGap, _minTrailingWidth);
    final text = loose.tighten(width: width - titleStart - trailingWidth);
    final titleHeight = layoutChild(_title, text).height;
    final ltr = _textDirection == TextDirection.ltr;
    final textX = ltr ? titleStart : trailingWidth;
    final double titleY;
    final double height;
    if (subtitle == null) {
      height = math.max(_minHeight, titleHeight + 2 * _minVerticalPadding);
      titleY = (height - titleHeight) / 2;
    } else {
      final subtitleHeight = layoutChild(subtitle, text).height;
      final titleBaseline =
          getBaseline(_title, text, _titleBaseline) ?? titleHeight;
      final subtitleBaseline =
          getBaseline(subtitle, text, _subtitleBaseline) ?? subtitleHeight;
      final targetTitleY = _titleBaselineY - titleBaseline;
      final targetSubtitleY =
          _subtitleBaselineY + _visualDensity.vertical * 2 - subtitleBaseline;
      final halfOverlap =
          math.max(targetTitleY + titleHeight - targetSubtitleY, 0) / 2;
      final idealTitleY = targetTitleY - halfOverlap;
      final idealSubtitleY = targetSubtitleY + halfOverlap;
      final compact =
          idealTitleY < _minVerticalPadding ||
          idealSubtitleY + subtitleHeight + _minVerticalPadding > _minHeight;
      position?.call(
        subtitle,
        Offset(
          textX,
          compact ? _minVerticalPadding + titleHeight : idealSubtitleY,
        ),
      );
      height = compact
          ? 2 * _minVerticalPadding + titleHeight + subtitleHeight
          : _minHeight;
      titleY = compact ? _minVerticalPadding : idealTitleY;
    }
    if (position != null) {
      position(_title, Offset(textX, titleY));
      if (leading != null && leadingSize != null) {
        position(
          leading,
          Offset(
            ltr ? 0 : width - leadingSize.width,
            _sideY(leadingSize.height, height),
          ),
        );
      }
      if (trailing != null && trailingSize != null) {
        position(
          trailing,
          Offset(
            ltr ? width - trailingSize.width : 0,
            _sideY(trailingSize.height, height),
          ),
        );
      }
    }
    final inner = constraints.deflate(_padding).constrain(Size(width, height));
    return (
      titleY: titleY,
      text: text,
      size: constraints.constrain(
        Size(
          inner.width + _padding.horizontal,
          inner.height + _padding.vertical,
        ),
      ),
    );
  }

  @override
  double? computeDryBaseline(
    covariant BoxConstraints constraints,
    TextBaseline baseline,
  ) {
    final sizes = _computeSizes(
      ChildLayoutHelper.getDryBaseline,
      ChildLayoutHelper.dryLayoutChild,
      constraints,
    );
    return (BaselineOffset(_title.getDryBaseline(sizes.text, baseline)) +
            sizes.titleY +
            _padding.top)
        .offset;
  }

  @override
  Size computeDryLayout(covariant BoxConstraints constraints) => _computeSizes(
    ChildLayoutHelper.getDryBaseline,
    ChildLayoutHelper.dryLayoutChild,
    constraints,
  ).size;

  @override
  void performLayout() {
    final origin = Offset(_padding.left, _padding.top);
    size = _computeSizes(
      ChildLayoutHelper.getBaseline,
      ChildLayoutHelper.layoutChild,
      constraints,
      position: (child, offset) =>
          (child.parentData! as BoxParentData).offset = origin + offset,
    ).size;
    _content = _padding.deflateRect(Offset.zero & size);
  }

  @override
  void paint(PaintingContext context, Offset offset) {
    for (final child in children) {
      context.paintChild(
        child,
        offset + (child.parentData! as BoxParentData).offset,
      );
    }
  }

  @override
  bool hitTestSelf(Offset position) => _content.contains(position);

  @override
  bool hitTestChildren(BoxHitTestResult result, {required Offset position}) {
    for (final child in children) {
      final parentData = child.parentData! as BoxParentData;
      final hit = result.addWithPaintOffset(
        offset: parentData.offset,
        position: position,
        hitTest: (result, transformed) =>
            child.hitTest(result, position: transformed),
      );
      if (hit) {
        return true;
      }
    }
    return false;
  }
}
