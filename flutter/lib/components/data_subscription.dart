import '../widgets.dart';

/// Shares native sampling between visible consumers of the same data.
class BoxDataSubscription extends StatefulWidget {
  const BoxDataSubscription({super.key, required this.data, required this.child});
  final BoxData data;
  final Widget child;
  @override State<BoxDataSubscription> createState() => _BoxDataSubscriptionState();
}

class _BoxDataSubscriptionState extends State<BoxDataSubscription> with WidgetsBindingObserver {
  BoxController? _box;
  bool _pageActive = false;

  @override void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override void didChangeDependencies() {
    super.didChangeDependencies();
    final box = context.box;
    if (!identical(_box, box)) {
      _box?.setDataVisible(widget.data, this, false);
      _box = box;
    }
    _pageActive = PageActivityScope.isActiveOf(context) && TickerMode.of(context);
    _sync();
  }

  @override void didUpdateWidget(BoxDataSubscription oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.data != widget.data) {
      _box?.setDataVisible(oldWidget.data, this, false);
      _sync();
    }
  }

  void _sync() {
    final lifecycle = WidgetsBinding.instance.lifecycleState;
    _box?.setDataVisible(widget.data, this,
      _pageActive && (lifecycle == null || lifecycle == AppLifecycleState.resumed));
  }

  @override void didChangeAppLifecycleState(AppLifecycleState state) => _sync();

  @override void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _box?.setDataVisible(widget.data, this, false);
    super.dispose();
  }

  @override Widget build(BuildContext context) => widget.child;
}
