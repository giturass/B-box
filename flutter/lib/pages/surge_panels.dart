import 'dart:async';
import 'dart:convert';

import '../widgets.dart';
import 'configuration.dart';

class SurgePanelsPage extends StatelessWidget {
  const SurgePanelsPage({super.key, this.moduleId, this.profileId});
  final String? moduleId;
  final int? profileId;

  @override Widget build(BuildContext context) {
    final box = context.watchBox(BoxAspect.state), profile = box.selectedProfile;
    if (box.remote || profile == null || (profileId != null && profileId != box.selectedProfileId)) {
      return Scaffold(title: context.tr('Information panels'), body: NullStatus(illustration: NullStatusIllustration.scripts,
        label: context.tr(box.remote ? 'Disconnect remote session to edit local profiles' : 'No enabled panels')));
    }
    return _Panels(key: ValueKey((box.sessionRevision, box.selectedProfileId, box.profilesRevision, moduleId,
      box.dashboard['processingGeneration'], box.dashboard['processingApplying'], box.serviceSettingsChanging, box.dashboard['status'])),
      box: box, profile: profile, moduleId: moduleId);
  }
}

class _Panels extends StatefulWidget {
  const _Panels({super.key, required this.box, required this.profile, this.moduleId});
  final BoxController box;
  final Json profile;
  final String? moduleId;
  @override State<_Panels> createState() => _PanelsState();
}

class _PanelsState extends State<_Panels> with WidgetsBindingObserver {
  late final store = ProfileConfigStore(widget.box, widget.profile);
  List<Json>? panels;
  Object? loadError;
  final errors = <(String, String), String>{};
  final requests = <(String, String), String>{};
  int epoch = 0;
  static int sequence = 0;
  bool visible = false;

  @override void initState() {
    super.initState(); WidgetsBinding.instance.addObserver(this); unawaited(_load());
  }

  Future<void> _load() async {
    setState(() { panels = null; loadError = null; });
    try {
      await store.load();
      if (!mounted) return;
      if (!store.ready || store.error != null) throw StateError(store.error ?? 'No profile');
      final result = await store.box.call('profiles.modulePanels', {'id': store.profile['id'], 'revision': store.revision});
      if (!mounted) return;
      final parsed = rows(jsonDecode(result as String)).where((panel) => widget.moduleId == null || panel['moduleId'] == widget.moduleId).toList();
      setState(() => panels = parsed); _autoOnEntry();
    } catch (failure) { if (mounted) setState(() => loadError = failure); }
  }

  void _visibility() {
    final lifecycle = WidgetsBinding.instance.lifecycleState;
    final active = PageActivityScope.isActiveOf(context) && TickerMode.of(context) && (lifecycle == null || lifecycle == AppLifecycleState.resumed);
    if (active == visible) return;
    visible = active; epoch++;
    if (visible) { _autoOnEntry(); } else {
      for (final id in requests.values.toList()) { unawaited(store.cancelScript(id).catchError((Object _) {})); }
    }
  }

  @override void didChangeDependencies() { super.didChangeDependencies(); _visibility(); }
  @override void didChangeAppLifecycleState(AppLifecycleState state) { _visibility(); if (mounted) setState(() {}); }
  @override void dispose() { visible = false; epoch++; WidgetsBinding.instance.removeObserver(this); store.dispose(); super.dispose(); }

  void _autoOnEntry() {
    final current = panels, generation = epoch;
    if (!visible || current == null) return;
    WidgetsBinding.instance.addPostFrameCallback((_) async {
      for (final panel in current) {
        if (!mounted || !visible || generation != epoch) return;
        final interval = panel['updateInterval'] as int? ?? 0, last = json(panel['cached'])['updatedAt'] as int? ?? 0;
        if (interval > 0 && DateTime.now().millisecondsSinceEpoch - last >= interval * 1000) {
          await _refresh(panel, automatic: true);
        }
      }
    });
  }

  Future<void> _refresh(Json panel, {bool automatic = false}) async {
    final index = panel['index'] as int, id = panel['moduleId'] as String, name = panel['name'] as String;
    final key = (id, name), generation = epoch;
    if (!visible || requests.containsKey(key) || !store.canRunScript('generic', index, moduleRevision: store.revision)) return;
    final request = 'panel-${DateTime.now().microsecondsSinceEpoch}-${++sequence}';
    setState(() { requests[key] = request; errors.remove(key); });
    try {
      final output = await store.runScript('generic', index, requestId: request, moduleRevision: store.revision,
        moduleId: id, panelName: name, automatic: automatic);
      if (!mounted || !visible || generation != epoch || output == null) return;
      if (output['error'] case final String error when error.isNotEmpty) { errors[key] = error; return; }
      final value = json(jsonDecode(output['panel'] as String));
      if (value['title'] is! String || (value['title'] as String).trim().isEmpty) { errors[key] = 'Panel script returned no title'; return; }
      panel['cached'] = value;
    } catch (failure) { if (mounted && visible && generation == epoch) errors[key] = BoxController.describe(failure); }
    finally { requests.remove(key); if (mounted) setState(() {}); }
  }

  Widget? _icon(Json fields) {
    final style = switch (fields['style']) { 'good' => AppGlyphs.checkCircle, 'alert' => AppGlyphs.warning,
      'error' => AppGlyphs.error, 'info' => AppGlyphs.info, _ => null };
    if (style != null) return GlyphIcon(style);
    final name = fields['icon']; if (name is! String || name.isEmpty) return null;
    final hex = fields['icon-color'];
    final color = hex is String && RegExp(r'^#[0-9a-fA-F]{6}$').hasMatch(hex) ? Color(0xff000000 | int.parse(hex.substring(1), radix: 16)) : null;
    final symbol = const {'bitcoinsign.circle':'₿', 'yensign.circle':'¥', 'car':'🚗', 'car.fill':'🚗', 'film':'🎞',
      'music.note.list':'♫', 'headphones':'♫', 'calendar':'▦', 'book.fill':'▤', 'appletv':'▣', 'questionmark':'?'}[name];
    return symbol != null ? Text(symbol, style: TextStyle(fontSize: 24, color: color)) : GlyphIcon(switch (name) {
      'sun.max.fill' => AppGlyphs.sun, 'text.badge.star' => AppGlyphs.star, 'bolt.horizontal.circle.fill' => AppGlyphs.bolt, _ => AppGlyphs.info}, color: color);
  }

  @override Widget build(BuildContext context) {
    final current = panels, error = loadError;
    final title = context.tr('Information panels');
    if (error != null) return Scaffold(title: title, body: ErrorState(error, retry: _load));
    if (current == null) return Scaffold(title: title, body: const Center(child: ActivityIndicator.page()));
    if (current.isEmpty) return Scaffold(title: title, body: NullStatus(illustration: NullStatusIllustration.scripts, label: context.tr('No enabled panels')));
    return BoxPage(title: 'Information panels', children: [for (final panel in current) _card(context, panel)]);
  }

  Widget _card(BuildContext context, Json panel) {
    final fields = panel['cached'] == null ? panel : json(panel['cached']);
    final key = (panel['moduleId'] as String, panel['name'] as String), index = panel['index'] as int;
    final running = requests.containsKey(key), canRun = visible && store.canRunScript('generic', index, moduleRevision: store.revision);
    final title = fields['title'] as String? ?? key.$2;
    return generateSection(title: title == key.$2 ? null : key.$2, items: [ListItem(leading: _icon(fields), title: Text(title),
      subtitle: fields['content'] == null ? null : Text(fields['content'] as String),
      trailing: index < 0 ? null : running ? const ActivityIndicator(size: 24) : IconButton(tooltip: context.tr('Refresh'),
        icon: const GlyphIcon(AppGlyphs.refresh), onPressed: canRun ? () => _refresh(panel) : null))],
      footer: errors[key] != null ? context.tr(errors[key]!) : index >= 0 && !canRun && !store.isScriptRunning('generic', index)
        ? context.tr('Start the local service and enable scripts to refresh this panel.') : null);
  }
}
