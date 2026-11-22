import '../widgets.dart';

/// FlClash v0.9.3's on-demand prerequisite row, backed by Android's allowlist.
class BatteryOptimizationItem extends StatefulWidget {
  const BatteryOptimizationItem({super.key});

  @override
  State<BatteryOptimizationItem> createState() => _BatteryOptimizationItemState();
}

class _BatteryOptimizationItemState extends State<BatteryOptimizationItem>
    with WidgetsBindingObserver {
  BoxController? _box;
  bool _loading = true;
  int _refresh = 0;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final box = context.box;
    if (identical(box, _box)) return;
    _box = box;
    _loading = true;
    _load(box);
  }

  Future<void> _load(BoxController box) async {
    final refresh = ++_refresh;
    await box.refreshBatteryOptimization();
    if (mounted && identical(box, _box) && refresh == _refresh) {
      setState(() => _loading = false);
    }
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    final box = _box;
    if (state == AppLifecycleState.resumed && box != null) _load(box);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  Widget _authorizeButton(BuildContext context, bool authorized) =>
      MinFilledButtonTheme(
        child: ElasticButton(
          child: FilledButton(
            style: ButtonStyle.from(
              backgroundColor: authorized ? null : context.colorScheme.errorContainer,
              foregroundColor: authorized ? null : context.colorScheme.onErrorContainer,
              padding: const EdgeInsets.symmetric(horizontal: 12),
              minimumSize: const Size(80, 40),
            ),
            onPressed: () => context.box.requestIgnoreBatteryOptimization(),
            child: Stack(
              alignment: Alignment.center,
              children: [
                ExcludeSemantics(
                  child: Opacity(
                    opacity: 0,
                    child: Text(context.tr(authorized ? 'Tap to authorize' : 'Authorized')),
                  ),
                ),
                Text(context.tr(authorized ? 'Authorized' : 'Tap to authorize')),
              ],
            ),
          ),
        ),
      );

  @override
  Widget build(BuildContext context) => DecorationListItem(
    title: TooltipLabel(context.tr('Ignore battery optimization')),
    // This prerequisite keeps the reference button's full natural width.
    constrainTrailing: false,
    trailing: Stack(
      alignment: Alignment.centerRight,
      children: [
        Visibility(
          visible: !_loading,
          maintainSize: true,
          maintainAnimation: true,
          maintainState: true,
          child: _authorizeButton(context, context.box.settings['batteryExempt'] == true),
        ),
        if (_loading) const ActivityIndicator(size: 24),
      ],
    ),
  );
}
