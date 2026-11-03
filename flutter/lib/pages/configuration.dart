import 'dart:convert';
import '../widgets.dart';
import 'profiles.dart';
import 'profile_editor.dart';
import 'mitm_certificate.dart';
import 'capture_domains.dart';
import '../components/form.dart';

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
  const ProfileConfigurationPage({super.key, this.profile, this.resources = false, this.section});
  final Json? profile;
  final bool resources;
  final String? section;
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
    final store = context.box.remote ? null : _store;
    final title = widget.section == 'mitm' ? 'MITM' : widget.section == 'scripts' ? 'Scripts' : widget.resources ? 'Resources' : 'Advanced configuration';
    if (store == null) return CommonScaffold(title: context.tr(title),
      body: NullStatus(label: context.tr(context.box.remote ? 'Disconnect remote session to edit local profiles' : 'No profile'),
        illustration: NullStatusIllustration.profile, action: context.box.remote ? null : TonalButton(size: TonalButtonSize.bar,
          glyph: AppGlyphs.profiles, label: context.tr('Profiles'), onPressed: () => openForm(context, const ProfilesPage(picker: true)))));
    return ListenableBuilder(listenable: store, builder: (context, _) {
      if (!store.ready) return CommonScaffold(title: context.tr(title),
        body: store.error == null ? const Center(child: CommonCircleLoading.page()) : ErrorState(store.error!, retry: store.load));
      if (widget.section == 'mitm') return MitmConfigPage(store: store);
      if (widget.section == 'scripts') return ScriptsConfigPage(store: store);
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
            ListItem.open(leading: const GlyphIcon(AppGlyphs.lock), title: const Text('MITM'), widget: MitmPage(store: store)),
            ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Scripts')), widget: ScriptsConfigPage(store: store)),
            ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('HTTP rewriting')), widget: HttpRewriteConfigPage(store: store)),
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

class HttpRewriteConfigPage extends StatelessWidget {
  const HttpRewriteConfigPage({super.key, required this.store});
  final ProfileConfigStore store;
  static const path = ['experimental', 'http_rewrite'];

  @override Widget build(BuildContext context) => ListenableBuilder(listenable: store, builder: (context, _) {
    final revision = store.revision;
    const pattern = r'^https?://example\.com/(.*)';
    final sections = <(String, String, Json)>[
      ('Header Rewrite', 'header_rewrite', {'phase': 'request', 'operation': 'add', 'header': 'X-B-box', 'value': '1'}),
      ('URL Rewrite', 'url_rewrite', {'action': 'header', 'replacement': r'https://example.com/$1'}),
      ('Map Remote', 'map_remote', {'replacement': r'https://example.net/$1'}),
      ('Body Rewrite', 'body_rewrite', {'phase': 'response', 'regex': 'old', 'replacement': 'new'}),
      ('Map Local / API Mock', 'map_local', {'status': 200, 'data_type': 'text', 'data': '{}', 'headers': [{'name': 'Content-Type', 'value': 'application/json'}]}),
    ];
    // Keep scrolling rule names below the shared floating title.
    final inset = context.appBarInset;
    final contentGap = context.contentTopPadding - inset;
    return CommonScaffold(title: context.tr('HTTP rewriting'), isLoading: store.saving,
      body: Padding(padding: EdgeInsets.only(top: inset), child: ListView(
        padding: EdgeInsets.fromLTRB(16, contentGap > 0 ? contentGap : 0, 16, 24 + context.bottomInset),
        children: [
      ListFooter(text: context.tr('Rules belong to this JSON profile and run with the local service. Request recording is controlled separately.')),
      if (store.error != null) SelectableText(store.error!, style: TextStyle(color: context.colorScheme.error)),
      generateSection(items: [
        ListItem.toggle(key: const ValueKey('http-rewrite-enabled'), title: Text(context.tr('Enable HTTP rewriting')),
          value: store.value([...path, 'enabled']) == true,
          onChanged: store.saving ? null : (value) => store.set([...path, 'enabled'], value)),
      ]),
      generateSection(items: [
        for (final (title, key, initial) in sections)
          ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(title), subtitle: Text('${store.entries([...path, key]).length}'),
            widget: ConfigListPage(store: store, title: title, path: [...path, key], initial: {'pattern': pattern, 'disabled': true, ...initial})),
      ], footer: context.tr('New example rules are disabled. Edit their pattern and content, then set disabled to false.')),
      generateSection(items: [
        ListItem.open(leading: const GlyphIcon(AppGlyphs.resources), title: Text(context.tr('Embedded rewrite resources')),
          widget: ConfigValueEditor(title: 'Embedded rewrite resources', initial: store.value([...path, 'resources']) ?? const {},
            onSave: (value) => store.set([...path, 'resources'], value, expectedRevision: revision, rethrowFailure: true))),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Edit HTTP rewrite options')),
          widget: ConfigValueEditor(title: 'HTTP rewriting', initial: store.value(path) ?? const {'enabled': false},
            onSave: (value) => store.set(path, value, expectedRevision: revision, rethrowFailure: true))),
      ]),
    ])));
  });
}

class MitmPage extends StatelessWidget {
  const MitmPage({super.key, this.store});
  final ProfileConfigStore? store;
  @override Widget build(BuildContext context) => BoxPage(title: 'MITM', children: [
    ListFooter(text: context.tr('Decrypt only domains configured in the profile. An empty domain list decrypts nothing.')),
    generateSection(items: [
      ListItem.open(leading: const GlyphIcon(AppGlyphs.settings), title: Text(context.tr('MITM configuration')),
        widget: store == null ? const ProfileConfigurationPage(section: 'mitm') : MitmConfigPage(store: store!)),
      ListItem.open(leading: const GlyphIcon(AppGlyphs.key), title: Text(context.tr('MITM certificate')), widget: const MitmCertificatePage()),
    ]),
  ]);
}

class MitmConfigPage extends StatelessWidget {
  const MitmConfigPage({super.key, required this.store});
  final ProfileConfigStore store;
  static const path = ['experimental', 'mitm'];
  @override Widget build(BuildContext context) => ListenableBuilder(listenable: store, builder: (context, _) {
    final revision = store.revision;
    if (context.box.remote) return const BoxPage(title: 'MITM', children: [EmptyState('Disconnect remote session to edit local profiles')]);
    final hosts = (store.value([...path, 'hosts']) as List? ?? []).cast<String>();
    return ProcessingConfigScaffold(title: 'MITM configuration', store: store, children: [
      ListFooter(text: context.tr('Decrypt only domains configured in the profile. An empty domain list decrypts nothing.')),
      generateSection(items: [
        ListItem.toggle(key: const ValueKey('mitm-enabled'), title: Text(context.tr('Decrypt HTTPS')),
          value: store.value([...path, 'enabled']) == true,
          onChanged: store.saving ? null : (value) => store.set([...path, 'enabled'], value)),
        ListItem.open(key: const ValueKey('mitm-hosts'), leading: const GlyphIcon(AppGlyphs.dns), title: Text(context.tr('Decryption domains')),
          subtitle: Text(hosts.isEmpty ? context.tr('No domains selected') : hosts.join(', ')), widget: MitmHostsEditor(store: store)),
        ListItem.toggle(key: const ValueKey('mitm-tcp'), title: Text(context.tr('Decrypt non-HTTP TLS over TCP')),
          subtitle: Text(context.tr('TLS connections without a negotiated HTTP protocol are forwarded and recorded as TCP streams.')),
          value: store.value([...path, 'tcp']) == true,
          onChanged: store.saving ? null : (value) => store.set([...path, 'tcp'], value)),
        ListItem.toggle(title: Text(context.tr('Request TCP fallback for QUIC')), value: store.value([...path, 'block_quic']) == true,
          subtitle: Text(context.tr('Reject only observed QUIC connections whose names match the HTTPS filters. Apps may retry over TCP. Unknown names and other UDP stay unchanged.')),
          onChanged: store.saving ? null : (value) => store.set([...path, 'block_quic'], value)),
      ]),
      generateSection(items: [
        ListItem.open(leading: const GlyphIcon(AppGlyphs.key), title: Text(context.tr('MITM certificate')), widget: const MitmCertificatePage()),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Edit MITM options')),
          widget: ConfigValueEditor(title: 'MITM configuration', initial: store.value(path) ?? const {'enabled': false, 'hosts': [], 'tcp': false, 'block_quic': false},
            onSave: (value) => store.set(path, value, expectedRevision: revision, rethrowFailure: true))),
      ]),
    ]);
  });
}

class MitmHostsEditor extends StatefulWidget {
  const MitmHostsEditor({super.key, required this.store});
  final ProfileConfigStore store;
  @override State<MitmHostsEditor> createState() => _MitmHostsEditorState();
}
class _MitmHostsEditorState extends State<MitmHostsEditor> {
  late final _hosts = TextEditingController(text: (widget.store.value([...MitmConfigPage.path, 'hosts']) as List? ?? []).join('\n'));
  late final _revision = widget.store.revision;
  bool _busy = false, _dirty = false;
  String? _error;
  List<String> _lines() => _hosts.text.split(RegExp(r'[\s,;]+')).where((host) => host.isNotEmpty).toSet().toList();
  @override void dispose() { _hosts.dispose(); super.dispose(); }
  Future<void> _save() async {
    if (_busy || context.box.remote) return;
    setState(() { _busy = true; _error = null; });
    try {
      final saved = await widget.store.set([...MitmConfigPage.path, 'hosts'], _lines(), expectedRevision: _revision, rethrowFailure: true);
      if (saved && mounted) { setState(() { _dirty = false; _busy = false; }); Navigator.pop(context); }
    } catch (error) { if (mounted) setState(() => _error = BoxController.describe(error)); }
    finally { if (mounted) setState(() => _busy = false); }
  }
  @override Widget build(BuildContext context) => BoxEditGuard(dirty: _dirty, busy: _busy, child: FormPage(
    title: 'Decryption domains', saving: _busy, error: _error, onSave: _save, children: [
      ListFooter(text: context.tr('Exact names and *.example.com are accepted. Leave empty to decrypt nothing.')),
      TextField(key: const ValueKey('mitm-domain-input'), controller: _hosts, minLines: 4, maxLines: 8, readOnly: _busy,
        autocorrect: false, enableSuggestions: false, keyboardType: TextInputType.multiline,
        decoration: const InputDecoration(hintText: 'api.example.com\n*.example.net'),
        onChanged: (_) => setState(() => _dirty = true)),
      ListItem(key: const ValueKey('add-https-hosts'), title: Text(context.tr('Add captured domains')), onTap: _busy ? null : () async {
        final selected = await openPage<List<String>>(context, const CaptureDomainsPage(select: true));
        if (selected != null && selected.isNotEmpty && mounted) setState(() { _hosts.text = {..._lines(), ...selected}.join('\n'); _dirty = true; });
      }),
    ],
  ));
}

class ScriptsConfigPage extends StatelessWidget {
  const ScriptsConfigPage({super.key, required this.store});
  final ProfileConfigStore store;
  static const path = ['experimental', 'scripts'];
  @override Widget build(BuildContext context) => ListenableBuilder(listenable: store, builder: (context, _) {
    final revision = store.revision;
    if (context.box.remote) return const BoxPage(title: 'Scripts', children: [EmptyState('Disconnect remote session to edit local profiles')]);
    return ProcessingConfigScaffold(title: 'Scripts', store: store, children: [
      ListFooter(text: context.tr('HTTP scripts and cron tasks run independently of HTTP rewriting. HTTPS scripts use the MITM domain settings.')),
      generateSection(items: [
        ListItem.toggle(key: const ValueKey('scripts-enabled'), title: Text(context.tr('Enable scripts')),
          value: store.value([...path, 'enabled']) == true,
          onChanged: store.saving ? null : (value) => store.set([...path, 'enabled'], value)),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('HTTP scripts')),
          subtitle: Text('${store.entries([...path, 'http']).length}'),
          widget: ConfigListPage(store: store, title: 'HTTP scripts', path: [...path, 'http'], initial: const {
            'name': 'HTTP script', 'disabled': true, 'pattern': r'^https?://example\.com/', 'phase': 'response',
            'requires_body': true, 'source': r'$done({body: $response.body});',
          })),
        ListItem.open(key: const ValueKey('cron-scripts'), leading: const GlyphIcon(AppGlyphs.logs), title: Text(context.tr('Cron tasks')),
          subtitle: Text('${store.entries([...path, 'cron']).length}'),
          widget: ConfigListPage(store: store, title: 'Cron tasks', path: [...path, 'cron'], initial: const {
            'name': 'Scheduled task', 'disabled': true, 'cron': '0 2 * * *', 'timeout_ms': 5000,
            'source': r'console.log($cronexp); $done();',
          })),
      ], footer: context.tr('Cron accepts five fields, or six with seconds first. The default timezone follows the device when the service starts.')),
      ListFooter(text: context.tr('Cron runs while the local service is running. Missed intervals are not replayed.')),
      generateSection(items: [
        ListItem.open(leading: const GlyphIcon(AppGlyphs.resources), title: Text(context.tr('Embedded script resources')),
          widget: ConfigValueEditor(title: 'Embedded script resources', initial: store.value([...path, 'resources']) ?? const {},
            onSave: (value) => store.set([...path, 'resources'], value, expectedRevision: revision, rethrowFailure: true))),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Edit script options')),
          widget: ConfigValueEditor(title: 'Scripts', initial: store.value(path) ?? const {'enabled': false},
            onSave: (value) => store.set(path, value, expectedRevision: revision, rethrowFailure: true))),
      ], footer: context.tr('New example rules are disabled. Edit their pattern and content, then set disabled to false.')),
    ]);
  });
}

class ProcessingConfigScaffold extends StatelessWidget {
  const ProcessingConfigScaffold({super.key, required this.title, required this.store, required this.children});
  final String title;
  final ProfileConfigStore store;
  final List<Widget> children;
  @override Widget build(BuildContext context) {
    final inset = context.appBarInset;
    final gap = context.contentTopPadding - inset;
    return CommonScaffold(title: context.tr(title), isLoading: store.saving,
      body: Padding(padding: EdgeInsets.only(top: inset), child: ListView(
        padding: EdgeInsets.fromLTRB(16, gap > 0 ? gap : 0, 16, 24 + context.bottomInset), children: [
          ListFooter(text: store.profile['name']?.toString() ?? ''),
          if (store.error != null) SelectableText(store.error!, style: TextStyle(color: context.colorScheme.error)),
          ...children,
        ],
      )));
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
              final label = entry['name'] ?? entry['tag'] ?? entry['action'] ?? entry['type'] ?? '${context.tr(title)} ${index + 1}';
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
