import '../widgets.dart';
import '../theme_settings.dart';

/// Keeps visited pages in a viewport, as FlClash does. Offstage alone leaves
/// hidden toolbars measurable by Prism's route overlays.
class PageSwitcher extends StatefulWidget {
  const PageSwitcher({super.key, required this.index, required this.animation, required this.children});
  final int index;
  final TabAnimation animation;
  final List<Widget> children;
  @override State<PageSwitcher> createState() => _PageSwitcherState();
}
class _PageSwitcherState extends State<PageSwitcher> {
  late final _controller = PageController(initialPage: widget.index);
  List<int>? _order;
  int _slide = 0;

  @override void didUpdateWidget(PageSwitcher oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.index != widget.index) _toPage(widget.index);
  }

  Future<void> _toPage(int index) async {
    final slide = ++_slide;
    final page = _controller.page?.round() ?? index;
    final current = _order?[page] ?? page;
    if (_order != null) {
      _order = null;
      _controller.jumpToPage(current);
    }
    final fade = widget.animation == TabAnimation.fade;
    final duration = context.motionDuration(fade ? fadeTabDuration : kTabScrollDuration);
    if (!context.isMobileView || duration == Duration.zero) {
      _controller.jumpToPage(index);
      return;
    }
    // Keep distant destinations adjacent for this transition, without
    // building and painting every page in between.
    if ((index - current).abs() > 1) {
      final adjacent = index > current ? index - 1 : index + 1;
      _order = List.generate(widget.children.length, (item) => item)
        ..[adjacent] = current
        ..[current] = adjacent;
      _controller.jumpToPage(adjacent);
    }
    await _controller.animateToPage(index, duration: duration,
      curve: fade ? fadeTabCurve : Curves.easeOut);
    if (mounted && slide == _slide && _order != null) {
      setState(() => _order = null);
    }
  }

  @override void dispose() { _controller.dispose(); super.dispose(); }

  @override Widget build(BuildContext context) => PageView.builder(
    controller: _controller,
    physics: const NeverScrollableScrollPhysics(),
    itemCount: widget.children.length,
    findChildIndexCallback: (key) {
      if (key is! ValueKey<int>) return null;
      return _order?.indexOf(key.value) ?? key.value;
    },
    itemBuilder: (context, position) {
      final index = _order?[position] ?? position;
      return _FadeTabPage(
        key: ValueKey(index),
        controller: _controller,
        position: position,
        enabled: widget.animation == TabAnimation.fade,
        child: KeepScope(child: ExcludeSemantics(
          excluding: index != widget.index,
          child: IgnorePointer(ignoring: index != widget.index, child: widget.children[index]),
        )),
      );
    },
  );
}

/// Cancels the viewport's slide for a fade; moving pages use snapshots so
/// their contents and glass stay together throughout either transition.
class _FadeTabPage extends StatefulWidget {
  const _FadeTabPage({super.key, required this.controller, required this.position,
    required this.enabled, required this.child});
  final PageController controller;
  final int position;
  final bool enabled;
  final Widget child;
  @override State<_FadeTabPage> createState() => _FadeTabPageState();
}

class _FadeTabPageState extends State<_FadeTabPage> {
  final _snapshot = SnapshotController();

  @override void dispose() { _snapshot.dispose(); super.dispose(); }

  double get _delta {
    if (!widget.controller.hasClients) return 0;
    final metrics = widget.controller.position;
    if (!metrics.hasContentDimensions || metrics.viewportDimension == 0) return 0;
    return (widget.position - metrics.pixels / metrics.viewportDimension).clamp(-1.0, 1.0);
  }

  @override Widget build(BuildContext context) => AnimatedBuilder(
    animation: widget.controller,
    builder: (_, child) {
      final offset = _delta;
      _snapshot.allowSnapshotting = offset.abs() > 0.001;
      final delta = widget.enabled ? offset : 0.0;
      return FractionalTranslation(translation: Offset(-delta, 0),
        child: Opacity(opacity: 1 - delta.abs(), child: child));
    },
    child: SnapshotWidget(controller: _snapshot, mode: SnapshotMode.permissive,
      autoresize: true,
      child: LiquidGlassHost(enabled: LiquidGlassScope.of(context), child: widget.child)),
  );
}
