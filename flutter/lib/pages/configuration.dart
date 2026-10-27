import 'dart:convert';
import '../widgets.dart';
import 'profiles.dart';
import 'profile_editor.dart';

const configEncoder = JsonEncoder.withIndent('  ');

class ProfileConfigStore extends ChangeNotifier {
  ProfileConfigStore(this.box, this.profile);
  final BoxController box;
  final Json profile;
  Json root = {}, stats = {};
  String revision = '';
  String? error;
  bool ready = false, saving = false, loading = false, _disposed = false;
  int _generation = 0;

  void _receive(Json data) {
    root = json(jsonDecode(data['formatted'] as String));
    stats = json(data['stats']);
    revision = data['revision'] as String;
    ready = true;
  }

  Future<void> load() async {
    if (_disposed || saving) return;
    final generation = ++_generation;
    loading = true;
    error = null;
    notifyListeners();
    try {
      final data = json(await box.call('profiles.config', {'id': profile['id']}));
      if (!_disposed && generation == _generation) _receive(data);
    } catch (failure) {
      if (!_disposed && generation == _generation) error = BoxController.describe(failure);
    } finally {
      if (!_disposed && generation == _generation) { loading = false; notifyListeners(); }
    }
  }

  dynamic value(List<String> path) {
    dynamic result = root;
    for (final key in path) {
      if (result is! Map) return null;
      result = result[key];
    }
    return result;
  }

  List<Json> entries(List<String> path) => rows(value(path));
  List<String> tags(List<String> path) => entries(path).map((entry) => entry['tag']).whereType<String>().toList();

  Future<bool> set(List<String> path, dynamic value, {bool remove = false, String? expectedRevision, bool rethrowFailure = false}) async {
    if (_disposed || !ready || saving || loading) return false;
    if (expectedRevision != null && expectedRevision != revision) {
      final failure = StateError('Configuration changed; reload it before saving');
      error = BoxController.describe(failure);
      notifyListeners();
      if (rethrowFailure) throw failure;
      box.report(failure);
      return false;
    }
    final candidate = json(jsonDecode(jsonEncode(root)));
    Json parent = candidate;
    for (final key in path.take(path.length - 1)) {
      final next = json(parent[key]);
      parent[key] = next;
      parent = next;
    }
    if (remove) { parent.remove(path.last); } else { parent[path.last] = value; }
    saving = true;
    error = null;
    notifyListeners();
    try {
      final data = json(await box.call('profiles.write', {'id': profile['id'], 'revision': revision, 'content': configEncoder.convert(candidate)}));
      if (!_disposed) _receive(data);
      return true;
    } catch (failure) {
      error = BoxController.describe(failure);
      if (rethrowFailure) rethrow;
      box.report(failure);
      return false;
    }
    finally { saving = false; if (!_disposed) notifyListeners(); }
  }

  @override void dispose() { _disposed = true; super.dispose(); }
}

class ProfileConfigurationPage extends StatefulWidget {
  const ProfileConfigurationPage({super.key, this.profile, this.resources = false});
  final Json? profile;
  final bool resources;
  @override State<ProfileConfigurationPage> createState() => _ProfileConfigurationPageState();
}

class _ProfileConfigurationPageState extends State<ProfileConfigurationPage> {
  ProfileConfigStore? _store;
  @override void didChangeDependencies() {
    super.didChangeDependencies();
    final profile = widget.profile ?? context.box.selectedProfile;
    if (_store == null && profile != null && !context.box.remote) {
      _store = ProfileConfigStore(context.box, profile);
      _store!.load();
    }
  }
  @override void dispose() { _store?.dispose(); super.dispose(); }
  @override Widget build(BuildContext context) {
    final store = _store;
    if (store == null) return CommonScaffold(title: context.tr(widget.resources ? 'Resources' : 'Advanced configuration'),
      body: NullStatus(label: context.tr(context.box.remote ? 'Disconnect remote session to edit local profiles' : 'No profile'),
        illustration: NullStatusIllustration.profile, action: context.box.remote ? null : TonalButton(size: TonalButtonSize.bar,
          glyph: AppGlyphs.profiles, label: context.tr('Profiles'), onPressed: () => openForm(context, const ProfilesPage(picker: true)))));
    return ListenableBuilder(listenable: store, builder: (context, _) {
      if (!store.ready) return CommonScaffold(title: context.tr(widget.resources ? 'Resources' : 'Advanced configuration'),
        body: store.error == null ? const Center(child: CommonCircleLoading.page()) : ErrorState(store.error!, retry: store.load));
      if (widget.resources) return ConfigListPage(store: store, title: 'Rule sets', path: const ['route', 'rule_set'],
        initial: const {'type': 'inline', 'tag': 'new-rules', 'rules': []});
      return BoxPage(title: 'Advanced configuration', loading: store.saving,
        actions: [IconButtonData(glyph: AppGlyphs.refresh, tooltip: context.tr('Reload'), onPressed: store.saving ? null : store.load)],
        children: [
          if (store.error != null) Padding(padding: const EdgeInsets.only(bottom: 12), child: SelectableText(store.error!, style: TextStyle(color: context.colorScheme.error))),
          generateSection(items: [
            ListItem.open(leading: const GlyphIcon(AppGlyphs.key), title: Text(context.tr('Network')), widget: NetworkConfigPage(store: store)),
            ListItem.open(leading: const GlyphIcon(AppGlyphs.dns), title: const Text('DNS'), widget: DnsConfigPage(store: store)),
            ListItem.open(leading: const GlyphIcon(AppGlyphs.rules), title: Text(context.tr('Routing rules')), widget: RouteConfigPage(store: store)),
            ListItem.open(leading: const GlyphIcon(AppGlyphs.resources), title: Text(context.tr('Rule sets')),
              widget: ConfigListPage(store: store, title: 'Rule sets', path: const ['route', 'rule_set'], initial: const {'type': 'inline', 'tag': 'new-rules', 'rules': []})),
            ListItem.open(leading: const GlyphIcon(AppGlyphs.logs), title: Text(context.tr('Log settings')), widget: LogConfigPage(store: store)),
          ], footer: store.profile['name'].toString()),
          generateSection(items: [ListItem(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Open configuration')),
            trailing: const GlyphIcon(AppGlyphs.chevronForward), onTap: () async {
              await openPage(context, ProfileEditor(profile: store.profile));
              await store.load();
            })]),
        ]);
    });
  }
}

class DnsConfigPage extends StatelessWidget {
  const DnsConfigPage({super.key, required this.store});
  final ProfileConfigStore store;
  @override Widget build(BuildContext context) => ListenableBuilder(listenable: store, builder: (context, _) => BoxPage(title: 'DNS', loading: store.saving, children: [
    generateSection(items: [
      ListItem.open(leading: const GlyphIcon(AppGlyphs.dns), title: Text(context.tr('DNS servers')),
        subtitle: Text('${store.entries(const ['dns', 'servers']).length}'), widget: ConfigListPage(store: store, title: 'DNS servers', path: const ['dns', 'servers'], initial: const {'type': 'local', 'tag': 'new-dns'})),
      ListItem.open(leading: const GlyphIcon(AppGlyphs.rules), title: Text(context.tr('DNS rules')),
        widget: ConfigListPage(store: store, title: 'DNS rules', path: const ['dns', 'rules'], initial: const {})),
      ListItem<String>.options(title: Text(context.tr('Default DNS server')), options: ['', ...store.tags(const ['dns', 'servers'])],
        value: store.value(const ['dns', 'final']) as String? ?? '', textBuilder: (value) => value.isEmpty ? context.tr('Default') : value,
        onChanged: (value) => store.set(const ['dns', 'final'], value, remove: value.isEmpty)),
      ListItem.toggle(title: Text(context.tr('Reverse mapping')), value: store.value(const ['dns', 'reverse_mapping']) == true,
        onChanged: (value) => store.set(const ['dns', 'reverse_mapping'], value)),
      ListItem.toggle(title: Text(context.tr('Disable DNS cache')), value: store.value(const ['dns', 'disable_cache']) == true,
        onChanged: (value) => store.set(const ['dns', 'disable_cache'], value)),
      ListItem.toggle(title: Text(context.tr('Disable DNS cache expiration')), value: store.value(const ['dns', 'disable_expire']) == true,
        onChanged: (value) => store.set(const ['dns', 'disable_expire'], value)),
      ListItem.input(title: Text(context.tr('DNS cache capacity')), dialogTitle: context.tr('DNS cache capacity'),
        value: '${store.value(const ['dns', 'cache_capacity']) ?? 0}', keyboardType: TextInputType.number,
        validator: (value) { final number = int.tryParse(value ?? ''); return number != null && number >= 0 && number <= 0xffffffff ? null : context.tr('Invalid value'); },
        onChanged: (value) { if (value != null) store.set(const ['dns', 'cache_capacity'], int.parse(value)); }),
    ]),
  ]));
}

class RouteConfigPage extends StatelessWidget {
  const RouteConfigPage({super.key, required this.store});
  final ProfileConfigStore store;
  @override Widget build(BuildContext context) => ListenableBuilder(listenable: store, builder: (context, _) {
    final revision = store.revision;
    return BoxPage(title: 'Routing rules', loading: store.saving, children: [
    generateSection(items: [
      ListItem.open(leading: const GlyphIcon(AppGlyphs.rules), title: Text(context.tr('Routing rules')),
        subtitle: Text('${store.entries(const ['route', 'rules']).length}'), widget: ConfigListPage(store: store, title: 'Routing rules', path: const ['route', 'rules'], initial: const {})),
      ListItem<String>.options(title: Text(context.tr('Default outbound')), options: ['', ...store.tags(const ['outbounds']), ...store.tags(const ['endpoints'])],
        value: store.value(const ['route', 'final']) as String? ?? '', textBuilder: (value) => value.isEmpty ? context.tr('Default') : value,
        onChanged: (value) => store.set(const ['route', 'final'], value, remove: value.isEmpty)),
      ListItem.toggle(title: Text(context.tr('Find process')), value: store.value(const ['route', 'find_process']) == true,
        onChanged: (value) => store.set(const ['route', 'find_process'], value)),
    ]),
    generateSection(items: [ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Edit route options')),
      widget: ConfigValueEditor(title: 'Routing rules', initial: store.value(const ['route']) ?? {},
        onSave: (value) => store.set(const ['route'], value, expectedRevision: revision, rethrowFailure: true)))]),
    ]);
  });
}

class NetworkConfigPage extends StatelessWidget {
  const NetworkConfigPage({super.key, required this.store});
  final ProfileConfigStore store;
  @override Widget build(BuildContext context) => ListenableBuilder(listenable: store, builder: (context, _) => BoxPage(title: 'Network', loading: store.saving, children: [
    generateSection(items: [
      ListItem.open(leading: const GlyphIcon(AppGlyphs.devices), title: Text(context.tr('Inbounds')),
        widget: ConfigListPage(store: store, title: 'Inbounds', path: const ['inbounds'], initial: const {'type': 'socks', 'tag': 'new-socks', 'listen': '127.0.0.1', 'listen_port': 2080})),
      ListItem.open(leading: const GlyphIcon(AppGlyphs.proxies), title: Text(context.tr('Outbounds')),
        widget: ConfigListPage(store: store, title: 'Outbounds', path: const ['outbounds'], initial: const {'type': 'direct', 'tag': 'new-direct'})),
      ListItem.open(leading: const GlyphIcon(AppGlyphs.devices), title: Text(context.tr('Endpoints')),
        widget: ConfigListPage(store: store, title: 'Endpoints', path: const ['endpoints'], initial: const {})),
    ]),
  ]));
}

class LogConfigPage extends StatelessWidget {
  const LogConfigPage({super.key, required this.store});
  final ProfileConfigStore store;
  @override Widget build(BuildContext context) => ListenableBuilder(listenable: store, builder: (context, _) => BoxPage(title: 'Log settings', loading: store.saving, children: [
    generateSection(items: [
      ListItem.toggle(title: Text(context.tr('Enable logging')), value: store.value(const ['log', 'disabled']) != true,
        onChanged: (value) => store.set(const ['log', 'disabled'], !value)),
      ListItem<String>.options(title: Text(context.tr('Log level')), options: const ['trace', 'debug', 'info', 'warn', 'error', 'fatal', 'panic'],
        value: store.value(const ['log', 'level']) as String? ?? 'info', textBuilder: (value) => context.tr('${value[0].toUpperCase()}${value.substring(1)}'),
        onChanged: (value) => store.set(const ['log', 'level'], value)),
      ListItem.toggle(title: Text(context.tr('Log timestamps')), value: store.value(const ['log', 'timestamp']) == true,
        onChanged: (value) => store.set(const ['log', 'timestamp'], value)),
    ]),
  ]));
}

class ConfigListPage extends StatelessWidget {
  const ConfigListPage({super.key, required this.store, required this.title, required this.path, required this.initial});
  final ProfileConfigStore store;
  final String title;
  final List<String> path;
  final Json initial;

  Future<void> _edit(BuildContext context, [int? index]) async {
    final entries = List<Json>.of(store.entries(path));
    final revision = store.revision;
    final value = index == null ? Map<String, dynamic>.of(initial) : entries[index];
    if (index == null && value['tag'] is String) {
      final base = value['tag'] as String, tags = store.tags(path).toSet();
      var suffix = 2;
      while (tags.contains(value['tag'])) { value['tag'] = '$base-${suffix++}'; }
    }
    await openForm(context, ConfigValueEditor(title: index == null ? 'Add' : 'Edit', initial: value, onSave: (value) async {
      if (value is! Map) throw const FormatException('JSON object required');
      final next = List<Json>.of(entries);
      if (index == null) next.add(json(value)); else next[index] = json(value);
      return store.set(path, next, expectedRevision: revision, rethrowFailure: true);
    }));
  }

  @override Widget build(BuildContext context) => ListenableBuilder(listenable: store, builder: (context, _) {
    final entries = store.entries(path);
    return CommonScaffold(title: context.tr(title), isLoading: store.saving,
      primaryAction: IconButtonData(glyph: AppGlyphs.add, tooltip: context.tr('Add'), onPressed: store.saving ? null : () => _edit(context)),
      iconActions: [
        IconButtonData(glyph: AppGlyphs.refresh, tooltip: context.tr('Reload'), onPressed: store.saving ? null : store.load),
        if (entries.length > 1) IconButtonData(glyph: AppGlyphs.sort, tooltip: context.tr('Reorder'),
          onPressed: store.saving ? null : () => openForm(context, ConfigOrderPage(store: store, title: title, path: path))),
      ],
      body: entries.isEmpty ? NullStatus(label: context.tr('No results'), illustration: NullStatusIllustration.profile,
        action: TonalButton(size: TonalButtonSize.bar, glyph: AppGlyphs.add, label: context.tr('Add'), onPressed: () => _edit(context)))
        : ListView.separated(padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 16 + context.bottomInset),
            itemCount: entries.length, separatorBuilder: (_, _) => const SizedBox(height: 8),
            itemBuilder: (context, index) {
              final entry = entries[index];
              final revision = store.revision;
              final label = entry['tag'] ?? entry['action'] ?? entry['type'] ?? '${context.tr(title)} ${index + 1}';
              final menu = [
                CommonPopupMenuItem(glyph: AppGlyphs.edit, label: context.tr('Edit'), onPressed: () => _edit(context, index)),
                CommonPopupMenuItem(glyph: AppGlyphs.delete, label: context.tr('Delete'), danger: true, onPressed: () async {
                  if (await confirm(context, 'Delete', message: label.toString())) {
                    final next = List<Json>.of(entries)..removeAt(index);
                    await store.set(path, next, expectedRevision: revision);
                  }
                }),
              ];
              return ContextMenuRegion(menuItems: menu, child: CommonCard(radius: AppCorner.xl, onPressed: () => _edit(context, index), child: ListItem(
                leading: const GlyphIcon(AppGlyphs.profiles), title: Text(label.toString(), maxLines: 1, overflow: TextOverflow.ellipsis),
                subtitle: Text(entry['url']?.toString() ?? entry['path']?.toString() ?? entry['server']?.toString() ?? jsonEncode(entry), maxLines: 2, overflow: TextOverflow.ellipsis),
                trailing: BoxMenu(items: menu),
              )));
            },
          ),
    );
  });
}

class ConfigOrderPage extends StatefulWidget {
  const ConfigOrderPage({super.key, required this.store, required this.title, required this.path});
  final ProfileConfigStore store;
  final String title;
  final List<String> path;
  @override State<ConfigOrderPage> createState() => _ConfigOrderPageState();
}
class _ConfigOrderPageState extends State<ConfigOrderPage> {
  late final List<Json> _entries;
  late final String _original, _revision;
  bool _saving = false;
  String? _error;
  @override void initState() {
    super.initState();
    _entries = List<Json>.of(widget.store.entries(widget.path));
    _original = jsonEncode(_entries);
    _revision = widget.store.revision;
  }
  @override Widget build(BuildContext context) => BoxEditGuard(dirty: jsonEncode(_entries) != _original, busy: _saving,
    child: CommonScaffold(title: context.tr('Reorder'), isLoading: _saving,
    confirmationAction: IconButtonData(glyph: AppGlyphs.check, tooltip: context.tr('Save'), onPressed: _saving ? null : () async {
      setState(() => _saving = true);
      try {
        final saved = await widget.store.set(widget.path, _entries, expectedRevision: _revision, rethrowFailure: true);
        if (mounted && saved) context.safeNestedPop();
      } catch (failure) { if (mounted) setState(() => _error = BoxController.describe(failure)); }
      finally { if (mounted) setState(() => _saving = false); }
    }),
    body: Column(children: [
      if (_error != null) Padding(padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 8), child: SelectableText(_error!, style: TextStyle(color: context.colorScheme.error))),
      Expanded(child: ReorderableListView.builder(padding: EdgeInsets.fromLTRB(16, _error == null ? context.contentTopPadding : 0, 16, 16 + context.bottomInset), itemCount: _entries.length,
      onReorder: (old, next) { if (!_saving) setState(() { if (old < next) next--; _entries.insert(next, _entries.removeAt(old)); }); },
      itemBuilder: (context, index) => ListItem(key: ObjectKey(_entries[index]), trailing: const GlyphIcon(AppGlyphs.sort),
        title: Text((_entries[index]['tag'] ?? _entries[index]['action'] ?? '${index + 1}').toString())),
      )),
    ]),
  ));
}

class ConfigValueEditor extends StatefulWidget {
  const ConfigValueEditor({super.key, required this.title, required this.initial, required this.onSave});
  final String title;
  final dynamic initial;
  final Future<bool> Function(dynamic value) onSave;
  @override State<ConfigValueEditor> createState() => _ConfigValueEditorState();
}
class _ConfigValueEditorState extends State<ConfigValueEditor> {
  late final _original = configEncoder.convert(widget.initial);
  late final _text = JsonEditorController()..text = _original;
  bool _saving = false;
  String? _error;
  @override void initState() { super.initState(); _text.addListener(_changed); }
  void _changed() { if (mounted) setState(() {}); }
  @override void dispose() { _text.dispose(); super.dispose(); }
  @override Widget build(BuildContext context) => BoxEditGuard(dirty: _text.text != _original, busy: _saving,
    child: BoxPage(title: widget.title, loading: _saving,
    confirmationAction: IconButtonData(glyph: AppGlyphs.check, tooltip: context.tr('Save'), onPressed: _saving ? null : () async {
      dynamic value;
      try { value = jsonDecode(_text.text); } catch (failure) { setState(() => _error = BoxController.describe(failure)); return; }
      setState(() { _saving = true; _error = null; });
      try {
        final saved = await widget.onSave(value);
        if (mounted && saved) context.safeNestedPop();
      } catch (failure) { if (mounted) setState(() => _error = BoxController.describe(failure)); }
      finally { if (mounted) setState(() => _saving = false); }
    }),
    children: [
      if (_error != null) Padding(padding: const EdgeInsets.only(bottom: 12), child: SelectableText(_error!, style: TextStyle(color: context.colorScheme.error))),
      TextField(controller: _text, readOnly: _saving, maxLines: 16, minLines: 8, autocorrect: false, enableSuggestions: false,
        smartDashesType: SmartDashesType.disabled, smartQuotesType: SmartQuotesType.disabled,
        style: const TextStyle(fontFamily: 'JetBrainsMono', fontSize: 13, height: 1.6),
        decoration: InputDecoration(border: const OutlineInputBorder(), labelText: context.tr('Configuration'))),
    ],
  ));
}
