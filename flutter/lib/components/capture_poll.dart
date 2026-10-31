import 'dart:async';
import 'package:flutter/foundation.dart';
import '../widgets.dart';

/// Capture lists/details poll only while their page is visible in the foreground.
/// Request generations keep a slow reply from replacing a newer filter or session.
class CapturePoll extends StatefulWidget {
  const CapturePoll({super.key, required this.method, required this.arguments,
    required this.builder, this.revision = 0});
  final String method;
  final Json arguments;
  final int revision;
  final Widget Function(BuildContext, Json?, Object?, VoidCallback) builder;
  @override State<CapturePoll> createState() => _CapturePollState();
}

class _CapturePollState extends State<CapturePoll> with WidgetsBindingObserver {
  Timer? _timer;
  BoxController? _box;
  Json? _value;
  Object? _error;
  bool _visible = false, _loading = false, _queued = false;
  int _generation = 0;

  @override void initState() { super.initState(); WidgetsBinding.instance.addObserver(this); }
  @override void didChangeDependencies() {
    super.didChangeDependencies();
    _box = context.box;
    _sync();
  }
  @override void didUpdateWidget(CapturePoll oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.method != widget.method || !mapEquals(oldWidget.arguments, widget.arguments) || oldWidget.revision != widget.revision) _refresh();
  }
  @override void didChangeAppLifecycleState(AppLifecycleState state) => _sync();

  void _sync() {
    final lifecycle = WidgetsBinding.instance.lifecycleState;
    final visible = !_box!.remote && PageActivityScope.isActiveOf(context) && TickerMode.of(context) &&
        (lifecycle == null || lifecycle == AppLifecycleState.resumed);
    if (visible == _visible) return;
    _visible = visible;
    _generation++;
    _timer?.cancel();
    if (visible) _refresh();
  }

  void _refresh() {
    _generation++;
    _timer?.cancel();
    if (_loading) { _queued = true; return; }
    if (_visible) unawaited(_load());
  }

  Future<void> _load() async {
    if (!_visible || _loading) return;
    _loading = true;
    final generation = _generation;
    try {
      final value = json(await _box!.call(widget.method, Map.of(widget.arguments)));
      if (mounted && _visible && generation == _generation) setState(() { _value = value; _error = null; });
    } catch (error) {
      if (mounted && _visible && generation == _generation) setState(() => _error = error);
    } finally {
      _loading = false;
      if (mounted && _visible) {
        if (_queued) { _queued = false; unawaited(_load()); }
        else { _timer = Timer(const Duration(seconds: 2), _load); }
      }
    }
  }

  @override void dispose() {
    _generation++;
    _timer?.cancel();
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }
  @override Widget build(BuildContext context) => widget.builder(context, _value, _error, _refresh);
}
