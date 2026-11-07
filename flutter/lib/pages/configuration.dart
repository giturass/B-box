import 'dart:convert';
import '../widgets.dart';
import 'profiles.dart';
import 'profile_editor.dart';
import 'mitm_certificate.dart';
import 'capture_domains.dart';
import '../components/form.dart';
import '../common/config_form.dart';
import 'configuration_form.dart';

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
    if (_disposed || saving || box.remote) return;
    final generation = ++_generation;
    loading = true;
    error = null;
    notifyListeners();
    try {
      final data = json(await box.call('profiles.config', {'id': profile['id']}));
      if (!_disposed && generation == _generation && !box.remote) _receive(data);
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
  List<String> tags(List<String> path) => entries(path).expand((entry) => configStrings(entry['tag'])).toList();

  Future<bool> set(List<String> path, dynamic value, {bool remove = false, String? expectedRevision, bool rethrowFailure = false}) async {
    if (_disposed || !ready || saving || loading) return false;
    if (box.remote) {
      final failure = StateError('Disconnect remote session to edit local profiles');
      if (rethrowFailure) throw failure;
      box.report(failure);
      return false;
    }
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
    // Certificate access is independent of profile loading.
    if (widget.section == 'mitm') return MitmConfigPage(store: store);
    final title = widget.section == 'scripts' ? 'Scripts' : widget.resources ? 'Resources'
      : widget.section == 'advanced' ? 'Advanced settings' : 'Profile settings';
    if (store == null) return Scaffold(title: context.tr(title),
      body: NullStatus(label: context.tr(context.box.remote ? 'Disconnect remote session to edit local profiles' : 'No profile'),
        illustration: NullStatusIllustration.profile, action: context.box.remote ? null : TonalButton(size: TonalButtonSize.bar,
          glyph: AppGlyphs.profiles, label: context.tr('Profiles'), onPressed: () => openForm(context, const ProfilesPage(picker: true)))));
    return ListenableBuilder(listenable: store, builder: (context, _) {
      if (!store.ready) return Scaffold(title: context.tr(title),
        body: store.error == null ? const Center(child: ActivityIndicator.page()) : ErrorState(store.error!, retry: store.load));
      if (widget.section == 'scripts') return ScriptsConfigPage(store: store);
      if (widget.section == 'advanced') return AdvancedConfigPage(store: store);
      if (widget.resources) return ConfigListPage(store: store, title: 'Rule sets', path: const ['route', 'rule_set'],
        initial: const {'type': 'remote', 'tag': 'new-rules', 'format': 'binary'});
      return BoxPage(title: 'Profile settings', loading: store.saving,
        actions: [IconButtonData(glyph: AppGlyphs.refresh, tooltip: context.tr('Reload'), onPressed: store.saving ? null : store.load)],
        children: [
          if (store.profile['remote'] == true) ListFooter(text: context.tr('Configuration changes will replace the remote content until its next update.')),
          if (store.error != null) Padding(padding: const EdgeInsets.only(bottom: 12), child: SelectableText(store.error!, style: TextStyle(color: context.colorScheme.error))),
          generateSection(items: [
            ListItem.open(leading: const GlyphIcon(AppGlyphs.key), title: Text(context.tr('Network')), widget: NetworkConfigPage(store: store)),
            ListItem.open(leading: const GlyphIcon(AppGlyphs.dns), title: const Text('DNS'), widget: DnsConfigPage(store: store)),
            ListItem.open(leading: const GlyphIcon(AppGlyphs.rules), title: Text(context.tr('Routing rules')), widget: RouteConfigPage(store: store)),
            ListItem.open(leading: const GlyphIcon(AppGlyphs.resources), title: Text(context.tr('Rule sets')),
              widget: ConfigListPage(store: store, title: 'Rule sets', path: const ['route', 'rule_set'], initial: const {'type': 'remote', 'tag': 'new-rules', 'format': 'binary'})),
            ListItem.open(leading: const GlyphIcon(AppGlyphs.logs), title: Text(context.tr('Logs')), widget: LogConfigPage(store: store)),
          ], footer: store.profile['name'].toString()),
          generateSection(title: context.tr('Traffic processing'), items: [
            ListItem.open(leading: const GlyphIcon(AppGlyphs.lock), title: const Text('MITM'), widget: MitmPage(store: store)),
            ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('HTTP rewriting')), widget: HttpRewriteConfigPage(store: store)),
            ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Scripts')), widget: ScriptsConfigPage(store: store)),
          ]),
          generateSection(items: [ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Advanced settings')),
            subtitle: Text(context.tr('Raw JSON and all configuration fields')), widget: AdvancedConfigPage(store: store))]),
        ]);
    });
  }
}

class AdvancedConfigPage extends StatelessWidget {
  const AdvancedConfigPage({super.key, required this.store, this.sections});
  final ProfileConfigStore store;
  final List<(String, List<String>, dynamic)>? sections;
  @override Widget build(BuildContext context) => ListenableBuilder(listenable: store, builder: (context, _) {
    if (context.box.remote) return const BoxPage(title: 'Advanced settings', children: [EmptyState('Disconnect remote session to edit local profiles')]);
    final revision = store.revision;
    final entries = sections ?? [
      ('DNS', ['dns'], <String, dynamic>{}), ('Routing rules', ['route'], <String, dynamic>{}),
      ('Inbounds', ['inbounds'], <dynamic>[]), ('Outbounds', ['outbounds'], <dynamic>[]), ('Endpoints', ['endpoints'], <dynamic>[]),
      ('MITM', MitmConfigPage.path, <String, dynamic>{}), ('HTTP rewriting', HttpRewriteConfigPage.path, <String, dynamic>{}),
      ('Scripts', ScriptsConfigPage.path, <String, dynamic>{}), ('Logs', ['log'], <String, dynamic>{}),
    ];
    return BoxPage(title: 'Advanced settings', loading: store.saving, children: [
      ListFooter(text: context.tr('Raw JSON and all configuration fields')),
      if (store.profile['remote'] == true) ListFooter(text: context.tr('Configuration changes will replace the remote content until its next update.')),
      generateSection(items: [
        if (sections == null) ListItem(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Edit full JSON')),
          trailing: const GlyphIcon(AppGlyphs.chevronForward), onTap: () async {
            await openPage(context, ProfileEditor(profile: store.profile));
            await store.load();
          }),
        for (final (title, path, initial) in entries) ListItem.open(title: Text(context.tr(title)),
          subtitle: Text(context.tr('Edit JSON')), leading: const GlyphIcon(AppGlyphs.code),
          widget: ConfigValueEditor(title: title, initial: store.value(path) ?? initial,
            onSave: (value) => store.set(path, value, expectedRevision: revision, rethrowFailure: true))),
      ]),
    ]);
  });
}

class HttpRewriteConfigPage extends StatelessWidget {
  const HttpRewriteConfigPage({super.key, required this.store});
  final ProfileConfigStore store;
  static const path = ['experimental', 'http_rewrite'];

  @override Widget build(BuildContext context) => ListenableBuilder(listenable: store, builder: (context, _) {
    if (context.box.remote) return const BoxPage(title: 'HTTP rewriting', children: [EmptyState('Disconnect remote session to edit local profiles')]);
    const pattern = r'^https?://example\.com/(.*)';
    final sections = <(String, String, Json)>[
      ('URL Rewrite', 'url_rewrite', {'action': 'header', 'replacement': r'https://example.com/$1'}),
      ('Header Rewrite', 'header_rewrite', {'phase': 'request', 'operation': 'add', 'header': 'X-B-box', 'value': '1'}),
      ('Body Rewrite', 'body_rewrite', {'phase': 'response', 'regex': 'old', 'replacement': 'new'}),
      ('Map Local / API Mock', 'map_local', {'status': 200, 'data_type': 'text', 'data': '{}', 'headers': [{'name': 'Content-Type', 'value': 'application/json'}]}),
      ('Map Remote', 'map_remote', {'replacement': r'https://example.net/$1'}),
    ];
    // Keep scrolling rule names below the shared floating title.
    final inset = context.appBarInset;
    final contentGap = context.contentTopPadding - inset;
    return Scaffold(title: context.tr('HTTP rewriting'), isLoading: store.saving,
      body: Padding(padding: EdgeInsets.only(top: inset), child: ListView(
        padding: EdgeInsets.fromLTRB(16, contentGap > 0 ? contentGap : 0, 16, 24 + context.bottomInset),
        children: [
      ListFooter(text: context.tr('Rules run with this profile. Request recording is controlled separately.')),
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
      ], footer: context.tr('New rules start disabled. Configure them, then turn on Enabled.')),
      generateSection(items: [
        ListItem.open(leading: const GlyphIcon(AppGlyphs.resources), title: Text(context.tr('Embedded rewrite resources')),
          widget: ConfigResourcesPage(store: store, path: [...path, 'resources'])),
        processingScopeItem(context, store, path),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Advanced settings')),
          widget: AdvancedConfigPage(store: store, sections: [('HTTP rewriting', path, const {'enabled': false})])),
      ]),
    ])));
  });
}

class MitmPage extends StatelessWidget {
  const MitmPage({super.key, this.store});
  final ProfileConfigStore? store;
  @override Widget build(BuildContext context) => store == null
    ? const ProfileConfigurationPage(section: 'mitm') : MitmConfigPage(store: store);
}

class MitmConfigPage extends StatelessWidget {
  const MitmConfigPage({super.key, required this.store});
  final ProfileConfigStore? store;
  static const path = ['experimental', 'mitm'];
  @override Widget build(BuildContext context) {
    final store = this.store;
    return store == null ? _build(context, null)
      : ListenableBuilder(listenable: store, builder: (context, _) => _build(context, store));
  }

  Widget _build(BuildContext context, ProfileConfigStore? store) {
    if (context.box.remote) return const BoxPage(title: 'MITM', children: [EmptyState('Disconnect remote session to edit local profiles')]);
    final hosts = (store?.value([...path, 'hosts']) as List? ?? []).cast<String>();
    return ProcessingConfigScaffold(title: 'MITM', store: store, children: [
      ListFooter(text: context.tr('Decrypt only domains configured in the profile. An empty domain list decrypts nothing.')),
      if (store == null) EmptyState('No profile', glyph: AppGlyphs.profiles,
        action: TonalButton(size: TonalButtonSize.bar, glyph: AppGlyphs.profiles, label: context.tr('Profiles'),
          onPressed: () => openForm(context, const ProfilesPage(picker: true))))
      else if (!store.ready) Padding(padding: const EdgeInsets.symmetric(vertical: 24),
        child: store.error == null ? const Center(child: ActivityIndicator.page())
          : TextButton(onPressed: store.load, child: Text(context.tr('Reload'))))
      else generateSection(items: [
        ListItem.toggle(key: const ValueKey('mitm-enabled'), title: Text(context.tr('Decrypt HTTPS')),
          value: store.value([...path, 'enabled']) == true,
          onChanged: store.saving ? null : (value) => store.set([...path, 'enabled'], value)),
        ListItem.open(key: const ValueKey('mitm-hosts'), leading: const GlyphIcon(AppGlyphs.dns), title: Text(context.tr('Decryption domains')),
          subtitle: Text(hosts.isEmpty ? context.tr('No domains selected') : hosts.join(', ')), widget: MitmHostsEditor(store: store)),
        ListItem.toggle(key: const ValueKey('mitm-h2'), title: Text(context.tr('MITM over HTTP/2')),
          value: store.value([...path, 'h2']) == true,
          onChanged: store.saving ? null : (value) => store.set([...path, 'h2'], value)),
        ListItem.toggle(key: const ValueKey('mitm-skip-server-cert-verify'), title: Text(context.tr('Skip server certificate verification')),
          value: store.value([...path, 'skip_server_cert_verify']) == true,
          onChanged: store.saving ? null : (value) => store.set([...path, 'skip_server_cert_verify'], value)),
        ListItem.toggle(key: const ValueKey('mitm-tcp'), title: Text(context.tr('Decrypt non-HTTP TLS over TCP')),
          subtitle: Text(context.tr('HTTP requests are processed as HTTP, including TLS without ALPN. Other TLS payloads are forwarded and recorded as TCP streams.')),
          value: store.value([...path, 'tcp']) == true,
          onChanged: store.saving ? null : (value) => store.set([...path, 'tcp'], value)),
        ListItem.toggle(key: const ValueKey('mitm-block-quic'), title: Text(context.tr('Request TCP fallback for QUIC')), value: store.value([...path, 'block_quic']) != false,
          subtitle: Text(context.tr('Reject only observed QUIC connections whose names match the HTTPS filters. Apps may retry over TCP. Unknown names and other UDP stay unchanged.')),
          onChanged: store.saving ? null : (value) => store.set([...path, 'block_quic'], value)),
      ]),
      generateSection(items: [
        ListItem.open(leading: const GlyphIcon(AppGlyphs.key), title: Text(context.tr('MITM certificate')), widget: const MitmCertificatePage()),
        if (store != null && store.ready) processingScopeItem(context, store, path),
        if (store != null && store.ready) ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Advanced settings')),
          widget: AdvancedConfigPage(store: store, sections: [('MITM', path, const {'enabled': false, 'hosts': []})])),
      ]),
    ]);
  }
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
      ListFooter(text: context.tr('Supports * and ? wildcards, - exclusions and :port (:0 for all ports). First match wins; put exclusions first. Leave empty to decrypt nothing.')),
      TextField(key: const ValueKey('mitm-domain-input'), controller: _hosts, minLines: 4, maxLines: 8, readOnly: _busy,
        autocorrect: false, enableSuggestions: false, keyboardType: TextInputType.multiline,
        decoration: const InputDecoration(hintText: '-private.example.com\n*.example.com\napi.example.net:8443'),
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
          widget: ConfigResourcesPage(store: store, path: [...path, 'resources'])),
        processingScopeItem(context, store, path),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Advanced settings')),
          widget: AdvancedConfigPage(store: store, sections: [('Scripts', path, const {'enabled': false})])),
      ], footer: context.tr('New rules start disabled. Configure them, then turn on Enabled.')),
    ]);
  });
}

Widget processingScopeItem(BuildContext context, ProfileConfigStore store, List<String> path) => ListItem(
  leading: const GlyphIcon(AppGlyphs.filter), title: Text(context.tr('Ports and applications')),
  trailing: const GlyphIcon(AppGlyphs.chevronForward), onTap: store.saving ? null : () {
    final revision = store.revision;
    openForm(context, ConfigEntryForm(kind: ConfigKind.processingScope, initial: json(store.value(path)),
      onSave: (value) => store.set(path, value, expectedRevision: revision, rethrowFailure: true)));
  });

class ProcessingConfigScaffold extends StatelessWidget {
  const ProcessingConfigScaffold({super.key, required this.title, required this.store, required this.children});
  final String title;
  final ProfileConfigStore? store;
  final List<Widget> children;
  @override Widget build(BuildContext context) {
    final inset = context.appBarInset;
    final gap = context.contentTopPadding - inset;
    final profile = store?.profile;
    final error = store?.error;
    return Scaffold(title: context.tr(title), isLoading: store?.saving ?? false,
      body: Padding(padding: EdgeInsets.only(top: inset), child: ListView(
        padding: EdgeInsets.fromLTRB(16, gap > 0 ? gap : 0, 16, 24 + context.bottomInset), children: [
          if (profile != null) ListFooter(text: profile['name']?.toString() ?? ''),
          if (profile?['remote'] == true) ListFooter(text: context.tr('Configuration changes will replace the remote content until its next update.')),
          if (error != null) SelectableText(error, style: TextStyle(color: context.colorScheme.error)),
          ...children,
        ],
      )));
  }
}

class DnsConfigPage extends StatelessWidget {
  const DnsConfigPage({super.key, required this.store});
  final ProfileConfigStore store;
  @override Widget build(BuildContext context) => ListenableBuilder(listenable: store, builder: (context, _) => ConfigSectionPage(title: 'DNS', store: store, children: [
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
      ListItem.toggle(title: Text(context.tr('Cache DNS results')), value: store.value(const ['dns', 'disable_cache']) != true,
        onChanged: (value) => store.set(const ['dns', 'disable_cache'], !value)),
      ListItem.toggle(title: Text(context.tr('Refresh expired DNS records')), value: store.value(const ['dns', 'disable_expire']) != true,
        onChanged: (value) => store.set(const ['dns', 'disable_expire'], !value)),
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
    return ConfigSectionPage(title: 'Routing rules', store: store, children: [
    generateSection(items: [
      ListItem.open(leading: const GlyphIcon(AppGlyphs.rules), title: Text(context.tr('Routing rules')),
        subtitle: Text('${store.entries(const ['route', 'rules']).length}'), widget: ConfigListPage(store: store, title: 'Routing rules', path: const ['route', 'rules'], initial: const {})),
      ListItem<String>.options(title: Text(context.tr('Default outbound')), options: ['', ...store.tags(const ['outbounds']), ...store.tags(const ['endpoints'])],
        value: store.value(const ['route', 'final']) as String? ?? '', textBuilder: (value) => value.isEmpty ? context.tr('Default') : value,
        onChanged: (value) => store.set(const ['route', 'final'], value, remove: value.isEmpty)),
      ListItem.toggle(title: Text(context.tr('Find process')), value: store.value(const ['route', 'find_process']) == true,
        onChanged: (value) => store.set(const ['route', 'find_process'], value)),
    ]),
    generateSection(items: [ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Advanced settings')),
      widget: AdvancedConfigPage(store: store, sections: [('Routing rules', const ['route'], const {})]))]),
    ]);
  });
}

class NetworkConfigPage extends StatelessWidget {
  const NetworkConfigPage({super.key, required this.store});
  final ProfileConfigStore store;
  @override Widget build(BuildContext context) => ListenableBuilder(listenable: store, builder: (context, _) => ConfigSectionPage(title: 'Network', store: store, children: [
    generateSection(items: [
      ListItem.open(leading: const GlyphIcon(AppGlyphs.proxies), title: Text(context.tr('Proxy nodes and groups')),
        widget: ConfigListPage(store: store, title: 'Proxy nodes and groups', path: const ['outbounds'], initial: const {})),
      ListItem.open(leading: const GlyphIcon(AppGlyphs.devices), title: Text(context.tr('VPN and local proxy ports')),
        widget: ConfigListPage(store: store, title: 'VPN and local proxy ports', path: const ['inbounds'], initial: const {})),
      ListItem.open(leading: const GlyphIcon(AppGlyphs.devices), title: Text(context.tr('Tailscale and other networks')),
        widget: ConfigListPage(store: store, title: 'Endpoints', path: const ['endpoints'], initial: const {})),
    ]),
  ]));
}

class LogConfigPage extends StatelessWidget {
  const LogConfigPage({super.key, required this.store});
  final ProfileConfigStore store;
  @override Widget build(BuildContext context) => ListenableBuilder(listenable: store, builder: (context, _) => ConfigSectionPage(title: 'Logs', store: store, children: [
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

class ConfigSectionPage extends StatelessWidget {
  const ConfigSectionPage({super.key, required this.title, required this.store, required this.children});
  final String title;
  final ProfileConfigStore store;
  final List<Widget> children;
  @override Widget build(BuildContext context) {
    if (context.box.remote) return BoxPage(title: title, children: const [EmptyState('Disconnect remote session to edit local profiles')]);
    return BoxEditGuard(dirty: false, busy: store.saving, child: AbsorbPointer(absorbing: store.saving || store.loading,
      child: BoxPage(title: title, loading: store.saving || store.loading, children: [
        if (store.error != null) ErrorState(store.error!),
        ...children,
      ])));
  }
}

class ConfigListPage extends StatelessWidget {
  const ConfigListPage({super.key, required this.store, required this.title, required this.path, required this.initial});
  final ProfileConfigStore store;
  final String title;
  final List<String> path;
  final Json initial;

  ConfigKind get kind => switch (path.join('.')) {
    'dns.servers' => ConfigKind.dnsServer, 'dns.rules' => ConfigKind.dnsRule,
    'route.rules' => ConfigKind.routeRule, 'route.rule_set' => ConfigKind.ruleSet,
    'inbounds' => ConfigKind.inbound, 'outbounds' => ConfigKind.outbound, 'endpoints' => ConfigKind.endpoint,
    'experimental.http_rewrite.url_rewrite' => ConfigKind.urlRewrite,
    'experimental.http_rewrite.header_rewrite' => ConfigKind.headerRewrite,
    'experimental.http_rewrite.body_rewrite' => ConfigKind.bodyRewrite,
    'experimental.http_rewrite.map_local' => ConfigKind.mapLocal,
    'experimental.http_rewrite.map_remote' => ConfigKind.mapRemote,
    'experimental.scripts.http' => ConfigKind.httpScript, 'experimental.scripts.cron' => ConfigKind.cronScript,
    _ => throw StateError('Unsupported configuration list: ${path.join('.')}'),
  };
  List<String> get usedTags => [
    ...store.tags(path),
    if (kind == ConfigKind.outbound) ...store.tags(const ['endpoints']),
    if (kind == ConfigKind.endpoint) ...store.tags(const ['outbounds']),
  ];

  Future<void> _edit(BuildContext context, [int? index]) async {
    if (store.saving || store.loading || context.box.remote) return;
    final entries = List<Json>.of(store.entries(path));
    final revision = store.revision;
    final value = index == null ? initial.isEmpty ? newConfigEntry(kind) : copyConfig(initial) : entries[index];
    if (index == null && value['tag'] is String) {
      final base = value['tag'] as String, tags = usedTags.toSet();
      var suffix = 2;
      while (tags.contains(value['tag'])) { value['tag'] = '$base-${suffix++}'; }
    }
    await openForm(context, ConfigEntryForm(kind: kind, isNew: index == null, initial: value,
      outbounds: [...store.tags(const ['outbounds']), ...store.tags(const ['endpoints'])],
      servers: store.tags(const ['dns', 'servers']), ruleSets: store.tags(const ['route', 'rule_set']), usedTags: usedTags,
      onSave: (value) async {
      final next = List<Json>.of(entries);
      if (index == null) next.add(value); else next[index] = value;
      return store.set(path, next, expectedRevision: revision, rethrowFailure: true);
    }));
  }

  @override Widget build(BuildContext context) => ListenableBuilder(listenable: store, builder: (context, _) {
    if (context.box.remote) return BoxPage(title: title, children: const [EmptyState('Disconnect remote session to edit local profiles')]);
    final entries = store.entries(path);
    return Scaffold(title: context.tr(title), isLoading: store.saving,
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
              final label = entry['name'] ?? (entry['tag'] == null ? null : configStrings(entry['tag']).join(', '))
                ?? (entry['action'] == null ? null : configOptionLabel(context, 'action', entry['action'].toString()))
                ?? '${context.tr(title)} ${index + 1}';
              final menu = [
                PopupMenuItem(glyph: AppGlyphs.edit, label: context.tr('Edit'), onPressed: () => _edit(context, index)),
                PopupMenuItem(glyph: AppGlyphs.delete, label: context.tr('Delete'), danger: true, onPressed: () async {
                  if (await confirm(context, 'Delete', message: label.toString())) {
                    final next = List<Json>.of(entries)..removeAt(index);
                    await store.set(path, next, expectedRevision: revision);
                  }
                }),
              ];
              return ContextMenuRegion(menuItems: menu, child: Card(radius: AppCorner.xl, onPressed: () => _edit(context, index), child: ListItem(
                leading: const GlyphIcon(AppGlyphs.profiles), title: Text(label.toString(), maxLines: 1, overflow: TextOverflow.ellipsis),
                subtitle: Text(configEntrySummary(entry, context.tr), maxLines: 2, overflow: TextOverflow.ellipsis),
                trailing: BoxMenu(items: menu),
              )));
            },
          ),
    );
  });
}

class ConfigResourcesPage extends StatelessWidget {
  const ConfigResourcesPage({super.key, required this.store, required this.path});
  final ProfileConfigStore store;
  final List<String> path;
  Future<void> _edit(BuildContext context, [String? name]) async {
    if (store.saving || store.loading || context.box.remote) return;
    final resources = copyConfig(json(store.value(path))), revision = store.revision;
    await openForm(context, ConfigEntryForm(kind: ConfigKind.resource, isNew: name == null,
      initial: {...(name == null ? newConfigEntry(ConfigKind.resource) : json(resources[name])), 'name': name ?? ''},
      onSave: (value) async {
        final nextName = value['name'] as String;
        if (nextName != name && resources.containsKey(nextName)) throw const FormatException('This name is already in use');
        final next = copyConfig(resources);
        if (name != null) next.remove(name);
        next[nextName] = copyConfig(value)..remove('name');
        return store.set(path, next, expectedRevision: revision, rethrowFailure: true);
      }));
  }
  @override Widget build(BuildContext context) => ListenableBuilder(listenable: store, builder: (context, _) {
    if (context.box.remote) return const BoxPage(title: 'Resources', children: [EmptyState('Disconnect remote session to edit local profiles')]);
    final resources = json(store.value(path)), revision = store.revision;
    return BoxPage(title: 'Resources', loading: store.saving,
      primaryAction: IconButtonData(glyph: AppGlyphs.add, tooltip: context.tr('Add'), onPressed: store.saving ? null : () => _edit(context)), children: [
        for (final name in resources.keys) ListItem(title: Text(name),
          subtitle: Text(configOptionLabel(context, 'data_type', json(resources[name])['data_type']?.toString() ?? 'text')),
          onTap: () => _edit(context, name), trailing: IconButton(tooltip: context.tr('Delete'), icon: const GlyphIcon(AppGlyphs.delete),
            onPressed: store.saving ? null : () async {
              if (await confirm(context, 'Delete', message: name)) {
                await store.set(path, copyConfig(resources)..remove(name), expectedRevision: revision);
              }
            })),
        if (resources.isEmpty) const EmptyState('No resources yet'),
      ]);
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
    child: Scaffold(title: context.tr('Reorder'), isLoading: _saving,
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
      Expanded(child: AbsorbPointer(absorbing: _saving, child: PrismReorderableListView(
        padding: EdgeInsets.fromLTRB(16, _error == null ? context.contentTopPadding : 0, 16, 16 + context.bottomInset),
        itemCount: _entries.length,
        proxyDecorator: commonProxyDecorator,
        onReorderItem: (old, next) { if (!_saving) setState(() => _entries.insert(next, _entries.removeAt(old))); },
        itemBuilder: (context, index) => ReorderDragStartListener(key: ObjectKey(_entries[index]), index: index,
          child: ListItem(trailing: SortHandle(index: index),
            title: Text((_entries[index]['tag'] ?? _entries[index]['action'] ?? '${index + 1}').toString()))),
      ))),
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
      BoxField(label: context.tr('Configuration'), borderRadius: AppRadius.md,
        child: TextField(controller: _text, readOnly: _saving, maxLines: 16, minLines: 8, autocorrect: false, enableSuggestions: false,
        smartDashesType: SmartDashesType.disabled, smartQuotesType: SmartQuotesType.disabled,
        textAlignVertical: TextAlignVertical.top,
        style: const TextStyle(fontFamily: 'JetBrainsMono', fontSize: 13, height: 1.6),
      )),
    ],
  ));
}
