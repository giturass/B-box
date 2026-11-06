import 'package:collection/collection.dart';
import '../widgets.dart';
import 'access.dart' show PackageIcon;

class PrivilegePage extends StatefulWidget {
  const PrivilegePage({super.key});
  @override State<PrivilegePage> createState() => _PrivilegePageState();
}

class _PrivilegePageState extends State<PrivilegePage> with WidgetsBindingObserver {
  Json? _state, _saved, _draft;
  String? _error, _revision;
  bool _loading = false, _saving = false;
  bool? _remote;
  int _epoch = 0;
  bool get _dirty => _draft != null && !const DeepCollectionEquality().equals(_saved, _draft);
  bool get _editable => !_loading && !_saving && _state?['compatible'] == true && !context.box.remote && !context.box.transitioning;

  @override void initState() { super.initState(); WidgetsBinding.instance.addObserver(this); }
  @override void dispose() { WidgetsBinding.instance.removeObserver(this); super.dispose(); }
  @override void didChangeDependencies() {
    super.didChangeDependencies();
    final remote = context.box.remote;
    if (_remote == remote) return;
    _remote = remote;
    _epoch++;
    _state = _saved = _draft = null;
    _error = _revision = null;
    _loading = _saving = false;
    if (!remote) WidgetsBinding.instance.addPostFrameCallback((_) { if (mounted && !context.box.remote) _refresh(); });
  }
  @override void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed && mounted && !context.box.remote && !_saving && !_loading) _refresh();
  }

  void _accept(Json value, {required bool replaceDraft}) {
    _state = value;
    if (replaceDraft) {
      _saved = json(value['settings']);
      _draft = {..._saved!, 'packages': List<String>.from(_saved!['packages'] as List? ?? [])};
      _revision = value['revision'] as String?;
    }
  }

  Future<void> _refresh({bool discard = false}) async {
    if (_loading || _saving || context.box.remote) return;
    if (discard && _dirty && !await confirm(context, 'Discard changes?', action: 'Discard')) return;
    if (!mounted || context.box.remote) return;
    final epoch = ++_epoch;
    setState(() { _loading = true; _error = null; });
    try {
      final value = json(await context.box.call('privilege.read'));
      if (mounted && epoch == _epoch && !context.box.remote) setState(() => _accept(value, replaceDraft: discard || !_dirty));
    } catch (error) {
      if (mounted && epoch == _epoch) setState(() => _error = BoxController.describe(error));
    } finally {
      if (mounted && epoch == _epoch) setState(() => _loading = false);
    }
  }

  void _set(String key, dynamic value) { if (_editable) setState(() => _draft![key] = value); }

  Future<void> _save() async {
    if (!_editable || !_dirty || _revision == null) return;
    final box = context.box, epoch = _epoch;
    setState(() { _saving = true; _error = null; });
    try {
      final value = json(await box.call('privilege.save', {'revision': _revision, 'value': _draft}));
      if (mounted && epoch == _epoch && !box.remote) {
        if (value['synced'] != true) throw StateError(value['error'] as String? ?? context.tr('Module settings have not been confirmed'));
        setState(() => _accept(value, replaceDraft: true));
        box.messages.add(context.tr('Privilege settings applied'));
      }
    } catch (error) {
      if (mounted && epoch == _epoch && !box.remote) {
        setState(() => _error = BoxController.describe(error));
        box.report(error);
      }
    } finally { if (mounted && epoch == _epoch) setState(() => _saving = false); }
  }

  Future<void> _packages() async {
    if (!_editable) return;
    final selected = await openPage<List<String>>(context, PrivilegeAppsPage(selected: List<String>.from(_draft!['packages'] as List)));
    if (mounted && selected != null) _set('packages', selected);
  }

  Future<void> _export() async {
    if (_saving || _loading || context.box.remote) return;
    if (!await confirm(context, 'Export LSPosed diagnostics?', action: 'Export',
      message: 'The ZIP contains system logs, LSPosed logs, installed package names and Android framework files. Root permission is required.')) return;
    if (!mounted || context.box.remote) return;
    final box = context.box, epoch = _epoch;
    setState(() => _saving = true);
    try {
      final exported = await box.call('privilege.export');
      if (mounted && epoch == _epoch && exported == true) box.messages.add(context.tr('LSPosed diagnostics exported'));
    } catch (error) { if (mounted && epoch == _epoch) box.report(error); }
    finally { if (mounted && epoch == _epoch) setState(() => _saving = false); }
  }

  @override Widget build(BuildContext context) {
    final box = context.box;
    if (box.remote) return const BoxPage(title: 'Privilege settings', children: [
      EmptyState('Local device only', message: 'Exit remote control to configure LSPosed on this Android device.'),
    ]);
    final state = _state, draft = _draft;
    final active = state?['active'] == true, compatible = state?['compatible'] == true;
    final status = !active ? 'Module not active' : !compatible ? 'Reboot required' : state?['synced'] == true ? 'Module active' : 'Settings not synchronized';
    return BoxEditGuard(dirty: _dirty, busy: _saving, child: BoxPage(title: 'Privilege settings', loading: _loading || _saving,
      actions: [IconButtonData(glyph: AppGlyphs.refresh, tooltip: context.tr('Refresh'), onPressed: _saving || _loading ? null : () => _refresh(discard: true))],
      confirmationAction: _dirty ? IconButtonData(glyph: AppGlyphs.check, tooltip: context.tr('Save'), onPressed: _editable ? _save : null) : null,
      children: [
        if (_error != null) ErrorState(_error!, retry: _saving ? null : () => _refresh(discard: true)),
        if (state != null) ...[
          generateSection(items: [
            ListItem(leading: GlyphIcon(compatible ? AppGlyphs.check : AppGlyphs.info), title: const Text('LSPosed'),
              subtitle: Text(context.tr(status))),
            if (!active) ListItem(title: Text(context.tr('Activate the module')),
              subtitle: Text(context.tr('Enable B-box in LSPosed, select the Android system scope, then reboot Android and refresh this page.'))),
            if (active && !compatible) ListItem(title: Text(context.tr('Module version')),
              subtitle: Text('${state['version']} → ${state['expectedVersion']}\n${context.tr('Reboot Android after updating or downgrading the module.')}')),
            if (state['error'] != null) ListItem(title: Text(context.tr('Module error')), subtitle: SelectableText(state['error'].toString())),
            if (active) ListItem(title: Text(context.tr('Hook logs')), trailing: const GlyphIcon(AppGlyphs.chevronForward),
              onTap: _saving ? null : () => openPage(context, const PrivilegeDiagnosticsPage(logs: true))),
          ]),
          if (draft != null) ...[
            generateSection(title: context.tr('VPN hiding'), items: [
              ListItem.toggle(title: Text(context.tr('Hide VPN')), value: draft['enabled'] == true,
                subtitle: Text(context.tr('Hide VPN status, HTTP proxy information and VPN apps from selected applications.')),
                onChanged: _editable ? (value) => _set('enabled', value) : null),
              ListItem(title: Text(context.tr('Apps to hide VPN from')),
                subtitle: Text('${context.tr('Selected')} ${(draft['packages'] as List).length}'),
                trailing: const GlyphIcon(AppGlyphs.chevronForward), onTap: _editable ? _packages : null),
            ]),
            generateSection(title: context.tr('VPN interface'), items: [
              ListItem.toggle(title: Text(context.tr('Rename VPN interface')), value: draft['renameInterface'] == true,
                subtitle: Text(context.tr('Changes apply when a VPN interface is created. Saving restarts the running local VPN. This affects newly created VPN interfaces on this device.')),
                onChanged: _editable ? (value) => _set('renameInterface', value) : null),
              if (draft['renameInterface'] == true) DisabledMask(status: !_editable, child: ListItem.input(title: Text(context.tr('Interface prefix')),
                dialogTitle: context.tr('Interface prefix'),
                value: draft['interfacePrefix'] as String? ?? 'wlan',
                validator: (value) => RegExp(r'^[A-Za-z_][A-Za-z0-9_]{0,12}$').hasMatch(value ?? '') ? null : context.tr('Use 1–13 ASCII letters, digits or underscores; start with a letter or underscore.'),
                onChanged: (value) { if (value != null) _set('interfacePrefix', value); })),
            ]),
          ],
          generateSection(title: context.tr('Diagnostics'), items: [
            ListItem(title: Text(context.tr('VPN detection test')), trailing: const GlyphIcon(AppGlyphs.chevronForward),
              onTap: _saving ? null : () => openPage(context, const PrivilegeDiagnosticsPage(logs: false))),
            ListItem(title: Text(context.tr('Export diagnostics (Root)')), trailing: const GlyphIcon(AppGlyphs.export),
              onTap: _saving || _loading ? null : _export),
          ]),
        ],
      ],
    ));
  }
}

class PrivilegeAppsPage extends StatefulWidget {
  const PrivilegeAppsPage({super.key, required this.selected});
  final List<String> selected;
  @override State<PrivilegeAppsPage> createState() => _PrivilegeAppsPageState();
}
class _PrivilegeAppsPageState extends State<PrivilegeAppsPage> {
  late final Set<String> _selected = widget.selected.toSet();
  List<Json>? _apps;
  String _query = '';
  String? _error;
  bool _system = false, _selectedOnly = false, _loading = false;
  int _epoch = 0;
  bool? _remote;
  @override void didChangeDependencies() {
    super.didChangeDependencies();
    if (_remote == context.box.remote) return;
    _remote = context.box.remote;
    _epoch++;
    if (!_remote!) WidgetsBinding.instance.addPostFrameCallback((_) { if (mounted) _load(); });
  }
  Future<void> _load() async {
    if (context.box.remote) return;
    final epoch = ++_epoch;
    setState(() { _loading = true; _error = null; });
    try {
      final apps = rows(await context.box.call('privilege.apps', {'refresh': true, 'system': true, 'nonInternet': true}));
      if (mounted && epoch == _epoch && !context.box.remote) setState(() => _apps = apps);
    } catch (error) { if (mounted && epoch == _epoch) setState(() => _error = BoxController.describe(error)); }
    finally { if (mounted && epoch == _epoch) setState(() => _loading = false); }
  }
  @override Widget build(BuildContext context) {
    if (context.box.remote) return const BoxPage(title: 'Apps to hide VPN from', children: [EmptyState('Local device only')]);
    final words = _query.toLowerCase().trim().split(RegExp(r'\s+'));
    final apps = (_apps ?? []).where((app) => (_system || app['system'] != true) &&
      (!_selectedOnly || _selected.contains(app['package'])) && words.every('${app['name']} ${app['package']}'.toLowerCase().contains)).toList()
      ..sort((a, b) {
        final selected = (_selected.contains(b['package']) ? 1 : 0) - (_selected.contains(a['package']) ? 1 : 0);
        return selected != 0 ? selected : a['name'].toString().toLowerCase().compareTo(b['name'].toString().toLowerCase());
      });
    return Scaffold(title: context.tr('Apps to hide VPN from'), isLoading: _loading,
      searchState: AppBarSearchState(onSearch: (value) => setState(() => _query = value)),
      confirmationAction: IconButtonData(glyph: AppGlyphs.check, tooltip: context.tr('Done'),
        onPressed: () => context.safeNestedPop(_selected.toList()..sort())),
      menuItems: [
        PopupMenuItem(label: context.tr('Show system apps'), checked: _system, onPressed: () => setState(() => _system = !_system)),
        PopupMenuItem(label: context.tr('Selected apps'), checked: _selectedOnly, onPressed: () => setState(() => _selectedOnly = !_selectedOnly)),
        PopupMenuItem(label: context.tr('Select all'), onPressed: () => setState(() => _selected.addAll(apps.map((app) => app['package'] as String)))),
        PopupMenuItem(label: context.tr('Deselect all'), onPressed: () => setState(() => _selected.removeAll(apps.map((app) => app['package'] as String)))),
        PopupMenuItem(label: context.tr('Refresh'), onPressed: _loading ? null : _load),
      ],
      body: _error != null ? ErrorState(_error!, retry: _load) : _apps == null ? const Center(child: ActivityIndicator.page()) : ListView.builder(
        padding: EdgeInsets.only(top: context.contentTopPadding, bottom: context.bottomInset), itemCount: apps.length + 1,
        itemBuilder: (context, index) {
          if (index == 0) return ListItem(title: Text('${context.tr('Selected')} ${_selected.length}'),
            subtitle: Text(context.tr('Select B-box too when checking VPN hiding with the built-in detection test. Hidden and uninstalled selections are retained.')));
          final app = apps[index - 1], package = app['package'] as String;
          return ListItem.checkbox(key: ValueKey(package), leading: PackageIcon(package: package),
            title: Text(app['name'].toString(), maxLines: 1, overflow: TextOverflow.ellipsis),
            subtitle: Text(package, maxLines: 1, overflow: TextOverflow.ellipsis), value: _selected.contains(package),
            onChanged: (_) => setState(() { _selected.contains(package) ? _selected.remove(package) : _selected.add(package); }));
        },
      ),
    );
  }
}

class PrivilegeDiagnosticsPage extends StatefulWidget {
  const PrivilegeDiagnosticsPage({super.key, required this.logs});
  final bool logs;
  @override State<PrivilegeDiagnosticsPage> createState() => _PrivilegeDiagnosticsPageState();
}
class _PrivilegeDiagnosticsPageState extends State<PrivilegeDiagnosticsPage> {
  Object? _data;
  String? _error;
  bool _loading = false;
  bool? _remote;
  int _epoch = 0;
  @override void didChangeDependencies() {
    super.didChangeDependencies();
    if (_remote == context.box.remote) return;
    _remote = context.box.remote;
    _epoch++;
    _data = null;
    if (!_remote!) WidgetsBinding.instance.addPostFrameCallback((_) { if (mounted) _load(); });
  }
  Future<void> _load() async {
    if (context.box.remote) return;
    final epoch = ++_epoch;
    setState(() { _loading = true; _error = null; });
    try {
      final value = await context.box.call(widget.logs ? 'privilege.logs' : 'privilege.test');
      if (mounted && epoch == _epoch && !context.box.remote) setState(() => _data = value);
    } catch (error) { if (mounted && epoch == _epoch) setState(() => _error = BoxController.describe(error)); }
    finally { if (mounted && epoch == _epoch) setState(() => _loading = false); }
  }
  @override Widget build(BuildContext context) {
    final data = json(widget.logs ? null : _data);
    final framework = (data['frameworkDetected'] as List? ?? []).join(', ');
    return BoxPage(title: widget.logs ? 'Hook logs' : 'VPN detection test', loading: _loading && !context.box.remote,
      actions: [IconButtonData(glyph: AppGlyphs.refresh, tooltip: context.tr('Refresh'), onPressed: _loading || context.box.remote ? null : _load)],
      children: context.box.remote ? [const EmptyState('Local device only')] : [
        if (_error != null) ErrorState(_error!, retry: _load),
        if (widget.logs && _data != null) ...[
          if (rows(_data).isEmpty) const EmptyState('No Hook logs'),
          for (final log in rows(_data)) generateSection(items: [ListItem(
            title: Text('${log['source']} · ${['DEBUG', 'INFO', 'WARN', 'ERROR'][(log['level'] as num? ?? 1).toInt().clamp(0, 3)]}'),
            subtitle: SelectableText('${DateTime.fromMillisecondsSinceEpoch((log['timestamp'] as num? ?? 0).toInt()).toLocal()}\n${log['message']}${log['stackTrace'] == null ? '' : '\n${log['stackTrace']}'}'),
          )]),
        ] else if (!widget.logs && _data != null) ...[
          ListItem(title: Text(context.tr('Detection scope')),
            subtitle: Text(context.tr('This test observes the B-box process. Start a VPN and save your hiding settings first. Results do not represent other applications.'))),
          DetailRows({
            'Framework': framework.isEmpty ? context.tr('Not detected') : framework,
            'Network interfaces': data['nativeDetected'] == true ? context.tr('VPN detected') : context.tr('Not detected'),
            'Framework interfaces': (data['frameworkInterfaces'] as List? ?? []).join(', '),
            'Interface names': (data['nativeInterfaces'] as List? ?? []).join(', '),
            'HTTP proxy': data['httpProxy'], 'Application': data['package'],
          }),
        ],
      ],
    );
  }
}
