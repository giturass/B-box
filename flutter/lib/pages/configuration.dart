import 'dart:async';
import 'dart:convert';
import 'package:flutter/services.dart';
import '../widgets.dart';
import 'profiles.dart';
import 'profile_editor.dart' show JsonEditorController;
import 'mitm_certificate.dart';
import 'capture_domains.dart';
import '../components/form.dart';

const configEncoder = JsonEncoder.withIndent('  ');

const _httpRuleMatchGuidance = 'Use pattern, match, or both (AND). In match, all requires every condition and any requires at least one; groups can nest. Each condition sets subject and either equals or regex. Subjects: url, request.method, request.header; response rules also allow response.status and response.header. Header conditions need header; status equals is an integer.';
const _httpRuleMatchExample = r'''"match": {"all": [
  {"subject": "request.method", "equals": "GET"},
  {"subject": "request.header", "header": "X-Client", "regex": "(?i)^mobile$"}
]}''';

/// Removes only the leading B-box node metadata before Dart's strict JSON parser.
dynamic decodeConfigJson(String content) {
  final marker = RegExp(r'^\s*// b-box-local-nodes: (\[[^\r\n]*\])\r?\n').firstMatch(content);
  if (marker != null) {
    final tags = jsonDecode(marker.group(1)!);
    if (tags is List && tags.every((tag) => tag is String)) content = content.substring(marker.end);
  }
  return jsonDecode(content);
}

class ProfileConfigStore extends ChangeNotifier {
  ProfileConfigStore(this.box, this.profile);
  final BoxController box;
  final Json profile;
  Json root = {}, stats = {};
  String revision = '';
  String? error;
  bool ready = false, saving = false, loading = false, _disposed = false;
  int _generation = 0;
  final _runningScripts = <(String, int), String>{};
  final _cancelingScripts = <String>{};

  void _receive(Json data) {
    root = json(decodeConfigJson(data['formatted'] as String));
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

  bool isScriptRunning(String type, int index) => _runningScripts.containsKey((type, index));
  String? scriptRequestId(String type, int index) => _runningScripts[(type, index)];
  bool isScriptCanceling(String type, int index) => _cancelingScripts.contains(scriptRequestId(type, index));

  bool canRunScript(String type, int index) {
    if (_disposed || !ready || loading || saving || revision.isEmpty ||
        !const {'cron', 'event', 'generic'}.contains(type) || box.remote ||
        !box.running || box.transitioning || box.serviceSettingsChanging ||
        box.dashboard['processingApplying'] == true ||
        (box.dashboard['processingGeneration'] as num? ?? 0) <= 0 ||
        box.selectedProfileId != profile['id'] || value([...ScriptsConfigPage.path, 'enabled']) != true ||
        isScriptRunning(type, index)) return false;
    final scripts = entries([...ScriptsConfigPage.path, type]);
    return index >= 0 && index < scripts.length && scripts[index]['disabled'] != true;
  }

  Future<Json?> runScript(String type, int index, {required String requestId}) async {
    if (!canRunScript(type, index)) return null;
    final key = (type, index);
    _runningScripts[key] = requestId;
    notifyListeners();
    try {
      final output = await box.call('scripts.run', {
        'profileId': profile['id'], 'revision': revision, 'type': type, 'index': index, 'requestId': requestId,
      });
      return output == null ? null : json(output);
    } finally {
      _runningScripts.remove(key);
      _cancelingScripts.remove(requestId);
      if (!_disposed) notifyListeners();
    }
  }

  Future<void> cancelScript(String requestId) async {
    if (!_runningScripts.containsValue(requestId) || !_cancelingScripts.add(requestId)) return;
    if (!_disposed) notifyListeners();
    try { await box.call('scripts.cancel', {'requestId': requestId}); }
    catch (_) {
      _cancelingScripts.remove(requestId);
      if (!_disposed) notifyListeners();
      rethrow;
    }
  }

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
    // Copy only ancestors of the edited field. All other branches are read-only
    // during serialization, and a rejected write leaves the loaded root intact.
    final candidate = Map<String, dynamic>.of(root);
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

  Future<String?> importResource(String section) async {
    if (_disposed || !ready || saving || loading || box.remote) return null;
    final session = box.sessionRevision;
    saving = true;
    error = null;
    notifyListeners();
    try {
      final result = await box.call('profiles.importResource', {'id': profile['id'], 'revision': revision, 'section': section});
      if (result == null || _disposed || box.remote || box.sessionRevision != session) return null;
      final data = json(result);
      if (!_disposed) _receive(data);
      return json(data['importedResource'])['name'] as String?;
    } catch (failure) {
      if (!_disposed && !box.remote && box.sessionRevision == session) {
        error = BoxController.describe(failure);
        box.report(failure);
      }
      return null;
    } finally { saving = false; if (!_disposed) notifyListeners(); }
  }

  @override void dispose() {
    _disposed = true;
    for (final requestId in _runningScripts.values.toList()) {
      unawaited(cancelScript(requestId).then<void>((_) {}, onError: (Object _) {}));
    }
    super.dispose();
  }
}

enum ProfileConfigSection {
  logs('Logs', AppGlyphs.logs),
  network('Network', AppGlyphs.key),
  dns('DNS', AppGlyphs.dns),
  routing('Routing rules', AppGlyphs.rules),
  ruleSets('Rule sets', AppGlyphs.resources),
  mitm('MITM', AppGlyphs.lock),
  httpRewrite('HTTP rewriting', AppGlyphs.code),
  scripts('Scripts', AppGlyphs.code);

  const ProfileConfigSection(this.title, this.glyph);
  final String title;
  final Glyph glyph;
}

class ProfileConfigurationPage extends StatefulWidget {
  const ProfileConfigurationPage({super.key, this.profile, required this.section});
  final Json? profile;
  final ProfileConfigSection section;
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
    if (widget.section == ProfileConfigSection.mitm) return MitmConfigPage(store: store);
    final title = widget.section.title;
    if (store == null) return Scaffold(title: context.tr(title),
      body: NullStatus(label: context.tr(context.box.remote ? 'Disconnect remote session to edit local profiles' : 'No profile'),
        illustration: NullStatusIllustration.profile, action: context.box.remote ? null : TonalButton(size: TonalButtonSize.bar,
          glyph: AppGlyphs.profiles, label: context.tr('Profiles'), onPressed: () => openForm(context, const ProfilesPage(picker: true)))));
    return ListenableBuilder(listenable: store, builder: (context, _) {
      if (!store.ready) return Scaffold(title: context.tr(title),
        body: store.error == null ? const Center(child: ActivityIndicator.page()) : ErrorState(store.error!, retry: store.load));
      return switch (widget.section) {
        ProfileConfigSection.logs => LogConfigPage(store: store),
        ProfileConfigSection.network => NetworkConfigPage(store: store),
        ProfileConfigSection.dns => DnsConfigPage(store: store),
        ProfileConfigSection.routing => RouteConfigPage(store: store),
        ProfileConfigSection.ruleSets => ConfigListPage(store: store, title: 'Rule sets', path: const ['route', 'rule_set'],
          initial: const {'type': 'inline', 'tag': 'new-rules', 'rules': []}),
        ProfileConfigSection.mitm => MitmConfigPage(store: store),
        ProfileConfigSection.httpRewrite => HttpRewriteConfigPage(store: store),
        ProfileConfigSection.scripts => ScriptsConfigPage(store: store),
      };
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
      ListFooter(text: context.tr('Rules belong to this JSON profile and run with the local service. Request recording is controlled separately.')),
      if (store.error != null) SelectableText(store.error!, style: TextStyle(color: context.colorScheme.error)),
      generateSection(items: [
        ListItem.toggle(key: const ValueKey('http-rewrite-enabled'), title: Text(context.tr('Enable HTTP rewriting')),
          value: store.value([...path, 'enabled']) == true,
          onChanged: store.saving ? null : (value) => store.set([...path, 'enabled'], value)),
      ]),
      generateSection(items: [
        for (final (title, key, initial) in sections)
          ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr(title)), subtitle: Text('${store.entries([...path, key]).length}'),
            widget: ConfigListPage(store: store, title: title, path: [...path, key], initial: {'pattern': pattern, 'disabled': true, ...initial},
              showHttpMatchHelp: true)),
      ], footer: context.tr('New example rules are disabled. Edit their pattern and content, then set disabled to false.')),
      generateSection(items: [
        ListItem.open(leading: const GlyphIcon(AppGlyphs.resources), title: Text(context.tr('Rewrite resources')),
          widget: ProcessingResourcesPage(store: store, section: 'http_rewrite')),
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
  @override Widget build(BuildContext context) => store == null
    ? const ProfileConfigurationPage(section: ProfileConfigSection.mitm) : MitmConfigPage(store: store);
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

  // An option that the profile does not set shows [byDefault].
  Widget _toggle(BuildContext context, ProfileConfigStore store, String key, String title, String option,
      {bool byDefault = false}) {
    final current = store.value([...path, option]);
    return ListItem.toggle(key: ValueKey(key), title: Text(context.tr(title)),
      value: byDefault ? current != false : current == true,
      onChanged: store.saving ? null : (value) => store.set([...path, option], value));
  }

  Widget _build(BuildContext context, ProfileConfigStore? store) {
    final revision = store?.revision;
    if (context.box.remote) return const BoxPage(title: 'MITM', children: [EmptyState('Disconnect remote session to edit local profiles')]);
    return ProcessingConfigScaffold(title: 'MITM', store: store, showProfileName: false, children: [
      generateSection(items: [
        ListItem.open(leading: const GlyphIcon(AppGlyphs.key), title: Text(context.tr('MITM certificate')), widget: const MitmCertificatePage()),
      ]),
      if (store == null) EmptyState('No profile', glyph: AppGlyphs.profiles,
        action: TonalButton(size: TonalButtonSize.bar, glyph: AppGlyphs.profiles, label: context.tr('Profiles'),
          onPressed: () => openForm(context, const ProfilesPage(picker: true))))
      else if (!store.ready) Padding(padding: const EdgeInsets.symmetric(vertical: 24),
        child: store.error == null ? const Center(child: ActivityIndicator.page())
          : TextButton(onPressed: store.load, child: Text(context.tr('Reload'))))
      else generateSection(items: [
        _toggle(context, store, 'mitm-enabled', 'Decrypt HTTPS', 'enabled'),
        ListItem.open(key: const ValueKey('mitm-hosts'), leading: const GlyphIcon(AppGlyphs.dns), title: Text(context.tr('Decryption domains')),
          widget: MitmHostsEditor(store: store)),
        _toggle(context, store, 'mitm-h2', 'MITM over HTTP/2', 'h2', byDefault: true),
        _toggle(context, store, 'mitm-h3', 'MITM over HTTP/3', 'h3'),
        _toggle(context, store, 'mitm-skip-server-cert-verify', 'Skip server certificate verification', 'skip_server_cert_verify'),
        _toggle(context, store, 'mitm-tcp', 'Decrypt TCP', 'tcp'),
        _toggle(context, store, 'mitm-block-quic', 'Request TCP fallback for QUIC', 'block_quic', byDefault: true),
      ]),
      if (store != null && store.ready) generateSection(items: [
        ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Edit MITM options')),
          widget: ConfigValueEditor(title: 'MITM configuration', initial: store.value(path) ?? const {'enabled': false, 'hosts': [], 'tcp': false, 'h2': true, 'h3': false, 'skip_server_cert_verify': false, 'block_quic': true},
            onSave: (value) => store.set(path, value, expectedRevision: revision, rethrowFailure: true))),
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
  // Read when the editor opens: a lazy read at save time would always match the store.
  late final String _revision;
  bool _busy = false, _dirty = false;
  String? _error;
  @override void initState() { super.initState(); _revision = widget.store.revision; }
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
    final revision = store.revision;
    if (context.box.remote) return const BoxPage(title: 'Scripts', children: [EmptyState('Disconnect remote session to edit local profiles')]);
    return ProcessingConfigScaffold(title: 'Scripts', store: store, children: [
      ListFooter(text: context.tr('HTTP, stream, cron, event and generic scripts run independently of HTTP rewriting. Encrypted traffic uses the MITM domain settings.')),
      generateSection(items: [
        ListItem.toggle(key: const ValueKey('scripts-enabled'), title: Text(context.tr('Enable scripts')),
          value: store.value([...path, 'enabled']) == true,
          onChanged: store.saving ? null : (value) => store.set([...path, 'enabled'], value)),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('HTTP scripts')),
          subtitle: Text('${store.entries([...path, 'http']).length}'),
          widget: ConfigListPage(store: store, title: 'HTTP scripts', path: [...path, 'http'], initial: const {
            'name': 'HTTP script', 'disabled': true, 'pattern': r'^https?://example\.com/', 'phase': 'response',
            'requires_body': true, 'source': r'$done({body: $response.body});',
          }, showHttpMatchHelp: true)),
        ListItem.open(key: const ValueKey('stream-scripts'), leading: const GlyphIcon(AppGlyphs.connections), title: Text(context.tr('Stream scripts')),
          subtitle: Text('${store.entries([...path, 'stream']).length}'),
          widget: ConfigListPage(store: store, title: 'Stream scripts', path: [...path, 'stream'], showStreamScriptHelp: true, initial: const {
            'name': 'WebSocket script', 'disabled': true, 'type': 'websocket', 'pattern': r'^wss?://example\.com/',
            'direction': 'server', 'source': r'$done();',
          })),
        ListItem.open(key: const ValueKey('cron-scripts'), leading: const GlyphIcon(AppGlyphs.logs), title: Text(context.tr('Cron tasks')),
          subtitle: Text('${store.entries([...path, 'cron']).length}'),
          widget: ConfigListPage(store: store, title: 'Cron tasks', path: [...path, 'cron'], scriptType: 'cron', initial: const {
            'name': 'Scheduled task', 'disabled': true, 'cron': '0 2 * * *', 'timeout_ms': 5000,
            'source': r'console.log($cronexp); $done();',
          })),
        ListItem.open(key: const ValueKey('event-scripts'), leading: const GlyphIcon(AppGlyphs.connections), title: Text(context.tr('Event scripts')),
          subtitle: Text('${store.entries([...path, 'event']).length}'),
          widget: ConfigListPage(store: store, title: 'Event scripts', path: [...path, 'event'], scriptType: 'event', initial: const {
            'name': 'Network change task', 'disabled': true, 'event': 'network-changed', 'timeout_ms': 5000,
            'source': r'$done();',
          })),
        ListItem.open(key: const ValueKey('generic-scripts'), leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Generic scripts')),
          subtitle: Text('${store.entries([...path, 'generic']).length}'),
          widget: ConfigListPage(store: store, title: 'Generic scripts', path: [...path, 'generic'], scriptType: 'generic', initial: const {
            'name': 'Generic script', 'disabled': true, 'timeout_ms': 5000,
            'source': r'$done({title: "B-box", content: "Script completed"});',
          })),
      ], footer: context.tr('Cron accepts five fields, or six with seconds first. The default timezone follows the device when the service starts.')),
      ListFooter(text: context.tr('Cron runs while the local service is running. Missed intervals are not replayed.')),
      ListFooter(text: context.tr('Events: network-changed for network changes, engine-started for engine startup, and profile-reloaded after configuration reload. Generic scripts run only on request.')),
      ListFooter(text: context.tr('Save and enable a cron, event or generic script, then use Run script in its menu. The selected local profile must be running. MITM is not required.')),
      generateSection(items: [
        ListItem.open(leading: const GlyphIcon(AppGlyphs.resources), title: Text(context.tr('Script resources')),
          widget: ProcessingResourcesPage(store: store, section: 'scripts')),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Edit script options')),
          widget: ConfigValueEditor(title: 'Scripts', initial: store.value(path) ?? const {'enabled': false},
            onSave: (value) => store.set(path, value, expectedRevision: revision, rethrowFailure: true))),
      ], footer: context.tr('New example rules are disabled. Edit their pattern and content, then set disabled to false.')),
    ]);
  });
}

class ProcessingResourcesPage extends StatelessWidget {
  const ProcessingResourcesPage({super.key, required this.store, required this.section});
  final ProfileConfigStore store;
  final String section;

  @override Widget build(BuildContext context) => ListenableBuilder(listenable: store, builder: (context, _) {
    if (context.box.remote) return const BoxPage(title: 'Resources', children: [EmptyState('Disconnect remote session to edit local profiles')]);
    final path = ['experimental', section, 'resources'];
    final resources = json(store.value(path));
    final revision = store.revision;
    return ProcessingConfigScaffold(title: 'Resources', store: store, children: [
      generateSection(items: [
        ListItem(key: const ValueKey('import-processing-resource'), title: Text(context.tr('Import local resource')),
          onTap: store.saving ? null : () async {
            final name = await store.importResource(section);
            if (context.mounted && name != null) context.box.messages.add('${context.tr('Resource imported')}: $name');
          }),
      ], footer: context.tr('Profile exports and B-box backups include resource references only. Use the resource name in the resource field of a rule.')),
      if (resources.isNotEmpty) generateSection(items: [
        for (final entry in resources.entries)
          ListItem.open(title: Text(entry.key), subtitle: Text(json(entry.value)['data_type']?.toString() ?? 'text'),
            widget: ConfigValueEditor(title: entry.key, initial: entry.value,
              onSave: (value) => store.set([...path, entry.key], value, expectedRevision: revision, rethrowFailure: true))),
      ]),
      generateSection(items: [
        ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Edit resources JSON')),
          widget: ConfigValueEditor(title: 'Resources', initial: resources,
            onSave: (value) => store.set(path, value, expectedRevision: revision, rethrowFailure: true))),
      ]),
    ]);
  });
}

class ProcessingConfigScaffold extends StatelessWidget {
  const ProcessingConfigScaffold({super.key, required this.title, required this.store, required this.children,
    this.showProfileName = true});
  final String title;
  final ProfileConfigStore? store;
  final List<Widget> children;
  final bool showProfileName;
  @override Widget build(BuildContext context) {
    final inset = context.appBarInset;
    final gap = context.contentTopPadding - inset;
    final profile = store?.profile;
    final error = store?.error;
    return Scaffold(title: context.tr(title), isLoading: store?.saving ?? false,
      body: Padding(padding: EdgeInsets.only(top: inset), child: ListView(
        padding: EdgeInsets.fromLTRB(16, gap > 0 ? gap : 0, 16, 24 + context.bottomInset), children: [
          if (showProfileName && profile != null) ListFooter(text: profile['name']?.toString() ?? ''),
          if (error != null) SelectableText(error, style: TextStyle(color: context.colorScheme.error)),
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
        onChanged: store.saving ? null : (value) => store.set(const ['dns', 'reverse_mapping'], value)),
      ListItem.toggle(title: Text(context.tr('Disable DNS cache')), value: store.value(const ['dns', 'disable_cache']) == true,
        onChanged: store.saving ? null : (value) => store.set(const ['dns', 'disable_cache'], value)),
      ListItem.toggle(title: Text(context.tr('Disable DNS cache expiration')), value: store.value(const ['dns', 'disable_expire']) == true,
        onChanged: store.saving ? null : (value) => store.set(const ['dns', 'disable_expire'], value)),
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
        onChanged: store.saving ? null : (value) => store.set(const ['route', 'find_process'], value)),
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
  @override Widget build(BuildContext context) => ListenableBuilder(listenable: store, builder: (context, _) => BoxPage(title: 'Logs', loading: store.saving, children: [
    generateSection(items: [
      ListItem.toggle(title: Text(context.tr('Enable logging')), value: store.value(const ['log', 'disabled']) != true,
        onChanged: store.saving ? null : (value) => store.set(const ['log', 'disabled'], !value)),
      ListItem<String>.options(title: Text(context.tr('Log level')), options: const ['trace', 'debug', 'info', 'warn', 'error', 'fatal', 'panic'],
        value: store.value(const ['log', 'level']) as String? ?? 'info', textBuilder: (value) => context.tr('${value[0].toUpperCase()}${value.substring(1)}'),
        onChanged: (value) => store.set(const ['log', 'level'], value)),
      ListItem.toggle(title: Text(context.tr('Log timestamps')), value: store.value(const ['log', 'timestamp']) == true,
        onChanged: store.saving ? null : (value) => store.set(const ['log', 'timestamp'], value)),
    ]),
  ]));
}

class ConfigListPage extends StatefulWidget {
  const ConfigListPage({super.key, required this.store, required this.title, required this.path, required this.initial,
    this.showHttpMatchHelp = false, this.showStreamScriptHelp = false, this.scriptType});
  final ProfileConfigStore store;
  final String title;
  final List<String> path;
  final Json initial;
  final bool showHttpMatchHelp;
  final bool showStreamScriptHelp;
  final String? scriptType;
  @override State<ConfigListPage> createState() => _ConfigListPageState();
}

class _ConfigListPageState extends State<ConfigListPage> with WidgetsBindingObserver {
  static int _requestSequence = 0;
  final _requests = <String>{}, _canceledRequests = <String>{};
  final _outputInvalidation = ValueNotifier<int>(0);
  int _runEpoch = 0;
  bool _visible = false, _editing = false;
  Object? _runContext;
  ProfileConfigStore get store => widget.store;
  List<String> get path => widget.path;
  String get title => widget.title;

  @override void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _listen(store);
  }

  void _listen(ProfileConfigStore value) {
    value.addListener(_stateChanged);
    value.box.stateChanges.addListener(_stateChanged);
    _runContext = _contextIdentity();
  }

  void _unlisten(ProfileConfigStore value) {
    value.removeListener(_stateChanged);
    value.box.stateChanges.removeListener(_stateChanged);
  }

  Object _contextIdentity() {
    final box = store.box;
    return (store.revision, store.ready, store.loading, store.saving,
      store.value([...ScriptsConfigPage.path, 'enabled']), box.sessionRevision,
      box.selectedProfileId, box.dashboard['status'], box.profilesRevision,
      box.dashboard['processingGeneration'], box.dashboard['processingApplying'],
      box.serviceSettingsChanging, box.remote);
  }

  void _invalidateRuns() {
    _outputInvalidation.value = ++_runEpoch;
    for (final requestId in _requests) {
      if (_canceledRequests.add(requestId)) {
        unawaited(store.cancelScript(requestId).then<void>((_) {}, onError: (Object _) {}));
      }
    }
  }

  void _stateChanged() {
    final identity = _contextIdentity();
    if (identity != _runContext) {
      _runContext = identity;
      _invalidateRuns();
    }
    if (mounted) setState(() {});
  }

  @override void didChangeDependencies() {
    super.didChangeDependencies();
    _syncVisibility();
  }

  void _syncVisibility() {
    final lifecycle = WidgetsBinding.instance.lifecycleState;
    final visible = PageActivityScope.isActiveOf(context) && TickerMode.of(context) &&
      (lifecycle == null || lifecycle == AppLifecycleState.resumed);
    if (visible == _visible) return;
    _visible = visible;
    _invalidateRuns();
  }

  @override void didChangeAppLifecycleState(AppLifecycleState state) {
    _syncVisibility();
    if (mounted) setState(() {});
  }

  @override void didUpdateWidget(ConfigListPage oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (!identical(oldWidget.store, store)) {
      for (final requestId in _requests) {
        unawaited(oldWidget.store.cancelScript(requestId).then<void>((_) {}, onError: (Object _) {}));
      }
      _unlisten(oldWidget.store);
      _outputInvalidation.value = ++_runEpoch;
      _listen(store);
    } else if (oldWidget.scriptType != widget.scriptType || oldWidget.path.join('.') != path.join('.')) {
      _invalidateRuns();
    }
  }

  @override void dispose() {
    _visible = false;
    _unlisten(store);
    WidgetsBinding.instance.removeObserver(this);
    _invalidateRuns();
    _outputInvalidation.dispose();
    super.dispose();
  }

  String _label(Json entry, int index) => configEntryName(entry) ?? '${context.tr(title)} ${index + 1}';

  Future<void> _run(int index, {required ProfileConfigStore expectedStore, required String expectedRevision,
    required String expectedType, required int expectedEpoch}) async {
    final type = widget.scriptType;
    if (type == null || type != expectedType || !identical(store, expectedStore) || store.revision != expectedRevision ||
        expectedEpoch != _runEpoch || !_visible || _editing || !store.canRunScript(type, index)) return;
    final epoch = _runEpoch, targetStore = store;
    final label = _label(store.entries(path)[index], index);
    final requestId = '${DateTime.now().microsecondsSinceEpoch}-${++_requestSequence}';
    _requests.add(requestId);
    Json? output;
    var canceled = false;
    try {
      output = await targetStore.runScript(type, index, requestId: requestId);
    } catch (failure) {
      output = {'error': BoxController.describe(failure)};
    } finally {
      _requests.remove(requestId);
      canceled = _canceledRequests.contains(requestId);
      _canceledRequests.remove(requestId);
    }
    bool current() => mounted && _visible && !_editing && epoch == _runEpoch && identical(targetStore, store) && !store.box.remote;
    if (canceled || !current() || output == null) return;
    await showBoxDialog<void>(context: context, builder: (_) => ScriptOutputDialog(name: label, output: output!,
      invalidation: _outputInvalidation, isValid: current));
  }

  Future<void> _cancel(int index, {required ProfileConfigStore expectedStore, required String expectedRequestId}) async {
    final type = widget.scriptType;
    if (type == null || !_visible || _editing || !identical(store, expectedStore)) return;
    final requestId = store.scriptRequestId(type, index);
    if (requestId == null || requestId != expectedRequestId || store.isScriptCanceling(type, index)) return;
    final targetStore = store, epoch = _runEpoch;
    _canceledRequests.add(requestId);
    try { await targetStore.cancelScript(requestId); }
    catch (failure) {
      if (mounted && epoch == _runEpoch && identical(targetStore, store)) {
        _canceledRequests.remove(requestId);
        store.box.report(failure);
      }
    }
  }

  Future<void> _edit(BuildContext context, [int? index]) async {
    final entries = List<Json>.of(store.entries(path));
    final revision = store.revision;
    final value = index == null ? Map<String, dynamic>.of(widget.initial) : entries[index];
    if (index == null && value['tag'] is String) {
      final base = value['tag'] as String, tags = store.tags(path).toSet();
      var suffix = 2;
      while (tags.contains(value['tag'])) { value['tag'] = '$base-${suffix++}'; }
    }
    _invalidateRuns();
    setState(() => _editing = true);
    try {
      await openForm(context, ConfigValueEditor(title: index == null ? 'Add' : 'Edit', initial: value,
        showHttpMatchHelp: widget.showHttpMatchHelp, showStreamScriptHelp: widget.showStreamScriptHelp,
        scriptType: widget.scriptType, onSave: (value) async {
        if (value is! Map) throw const FormatException('JSON object required');
        final next = List<Json>.of(entries);
        if (index == null) next.add(json(value)); else next[index] = json(value);
        return store.set(path, next, expectedRevision: revision, rethrowFailure: true);
      }));
    } finally { if (mounted) setState(() => _editing = false); }
  }

  Future<void> _reorder() async {
    _invalidateRuns();
    setState(() => _editing = true);
    try { await openForm(context, ConfigOrderPage(store: store, title: title, path: path)); }
    finally { if (mounted) setState(() => _editing = false); }
  }

  @override Widget build(BuildContext context) {
    if (context.box.remote) return BoxPage(title: title, children: const [EmptyState('Disconnect remote session to edit local profiles')]);
    final entries = store.entries(path);
    final blocked = !store.ready || store.saving || store.loading || _editing;
    return PopScope(onPopInvokedWithResult: (didPop, _) {
      if (didPop) { _visible = false; _invalidateRuns(); }
    }, child: Scaffold(title: context.tr(title), isLoading: store.saving || store.loading,
      primaryAction: IconButtonData(glyph: AppGlyphs.add, tooltip: context.tr('Add'), onPressed: blocked ? null : () => _edit(context)),
      iconActions: [
        IconButtonData(glyph: AppGlyphs.refresh, tooltip: context.tr('Reload'), onPressed: blocked ? null : store.load),
        if (entries.length > 1) IconButtonData(glyph: AppGlyphs.sort, tooltip: context.tr('Reorder'),
          onPressed: blocked ? null : _reorder),
      ],
      body: entries.isEmpty ? NullStatus(label: context.tr('No results'), illustration: NullStatusIllustration.profile,
        action: TonalButton(size: TonalButtonSize.bar, glyph: AppGlyphs.add, label: context.tr('Add'), onPressed: blocked ? null : () => _edit(context)))
        : ListView.separated(padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 16 + context.bottomInset),
            itemCount: entries.length, separatorBuilder: (_, _) => const SizedBox(height: 8),
            itemBuilder: (context, index) {
              final entry = entries[index];
              final revision = store.revision, targetStore = store, epoch = _runEpoch;
              final label = _label(entry, index), type = widget.scriptType;
              final requestId = type == null ? null : store.scriptRequestId(type, index);
              final running = requestId != null;
              final menu = [
                if (type != null && _visible && !_editing && store.canRunScript(type, index))
                  PopupMenuItem(glyph: AppGlyphs.playPause(0), label: context.tr('Run script'), onPressed: () => _run(index,
                    expectedStore: targetStore, expectedRevision: revision, expectedType: type, expectedEpoch: epoch)),
                if (type != null && requestId != null && _visible && !_editing && !store.isScriptCanceling(type, index))
                  PopupMenuItem(glyph: AppGlyphs.close, label: context.tr('Cancel script'), onPressed: () => _cancel(index,
                    expectedStore: targetStore, expectedRequestId: requestId)),
                PopupMenuItem(glyph: AppGlyphs.edit, label: context.tr('Edit'), onPressed: blocked ? null : () => _edit(context, index)),
                PopupMenuItem(glyph: AppGlyphs.delete, label: context.tr('Delete'), danger: true, onPressed: blocked ? null : () async {
                  _invalidateRuns();
                  if (await confirm(context, 'Delete', message: label.toString())) {
                    final next = List<Json>.of(entries)..removeAt(index);
                    await store.set(path, next, expectedRevision: revision);
                  }
                }),
              ];
              return ContextMenuRegion(key: ValueKey('config-entry-$index'), menuItems: menu,
                child: Card(radius: AppCorner.xl, onPressed: blocked ? null : () => _edit(context, index), child: ListItem(
                leading: GlyphIcon(type == null ? AppGlyphs.profiles : AppGlyphs.code), title: Text(label, maxLines: 1, overflow: TextOverflow.ellipsis),
                subtitle: Text(configEntrySummary(entry), maxLines: 2, overflow: TextOverflow.ellipsis),
                trailing: Row(mainAxisSize: MainAxisSize.min, children: [
                  if (running) Padding(padding: const EdgeInsets.only(right: 8),
                    child: ActivityIndicator(key: ValueKey('script-running-$index'), size: 18, semanticLabel: context.tr('Running script'))),
                  BoxMenu(items: menu),
                ]),
              )));
            },
          ),
    ));
  }
}

/// Reads a panel script's text without exposing its JSON wrapper to the reader.
String? scriptResultText(String result) {
  if (result.isEmpty) return null;
  final Object? value;
  try { value = jsonDecode(result); } on FormatException { return result; }
  return switch (value) {
    null || '' => null,
    final String text => text,
    {'title': final Object title} => [title, if ((value as Map)['content'] case final Object content) content].join('\n'),
    final Map map when map.isEmpty => null,
    _ => result,
  };
}

String scriptOutputText(Json output, {String noOutput = 'No output'}) {
  final error = output['error'] as String? ?? '';
  final result = scriptResultText(output['result'] as String? ?? '');
  final parts = [if (error.isNotEmpty) error, if (result != null) result,
    ...(output['logs'] as List? ?? []).whereType<String>()];
  return parts.isEmpty ? noOutput : parts.join('\n');
}

class ScriptOutputDialog extends StatefulWidget {
  const ScriptOutputDialog({super.key, required this.name, required this.output, this.invalidation, this.isValid});
  final String name;
  final Json output;
  final Listenable? invalidation;
  final bool Function()? isValid;
  @override State<ScriptOutputDialog> createState() => _ScriptOutputDialogState();
}

class _ScriptOutputDialogState extends State<ScriptOutputDialog> {
  bool _closing = false;
  bool get _valid => widget.isValid?.call() != false;

  @override void initState() {
    super.initState();
    widget.invalidation?.addListener(_checkValidity);
  }

  @override void didChangeDependencies() {
    super.didChangeDependencies();
    _checkValidity();
  }

  @override void didUpdateWidget(ScriptOutputDialog oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (!identical(oldWidget.invalidation, widget.invalidation)) {
      oldWidget.invalidation?.removeListener(_checkValidity);
      widget.invalidation?.addListener(_checkValidity);
    }
    _checkValidity();
  }

  void _checkValidity() {
    if (_closing || _valid) return;
    _closing = true;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      final route = ModalRoute.of(context);
      // Remove this result route even if another result or editor is above it.
      if (route != null && route.isActive) route.navigator?.removeRoute(route);
    });
  }

  @override void dispose() {
    widget.invalidation?.removeListener(_checkValidity);
    super.dispose();
  }

  Future<void> _copy(String text) async {
    if (_closing || !_valid) return;
    final box = context.box;
    try {
      await Clipboard.setData(ClipboardData(text: text));
      if (mounted && _valid) box.messages.add(context.tr('Copied'));
    } catch (failure) { if (mounted && _valid) box.report(failure); }
  }

  @override Widget build(BuildContext context) {
    if (_closing || !_valid) return const SizedBox.shrink();
    final text = scriptOutputText(widget.output, noOutput: context.tr('No output'));
    // Prism alerts bound their content and scroll it while keeping actions fixed.
    return AlertDialog(key: const ValueKey('script-output-dialog'), title: context.tr('Script output'),
      child: Column(mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.stretch, children: [
        SelectableText(widget.name, style: AppTextStyle.headline.of(context)),
        const SizedBox(height: 12),
        SelectableText(text, key: const ValueKey('script-output-text')),
      ]), actions: [
        TextButton(key: const ValueKey('copy-script-output'), onPressed: () => _copy(text), child: Text(context.tr('Copy'))),
        TextButton(onPressed: () => Navigator.pop(context), child: Text(context.tr('Close'))),
      ]);
  }
}

/// The name a list entry is known by, when it carries one.
String? configEntryName(Json entry) {
  for (final key in ['name', 'tag', 'action', 'type']) {
    final value = entry[key]?.toString();
    if (value != null && value.isNotEmpty) return value;
  }
  return null;
}

String configEntrySummary(Json entry) =>
  entry['url']?.toString() ?? entry['path']?.toString() ?? entry['server']?.toString() ?? jsonEncode(entry);

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
            // A position would move with the drag, so an unnamed entry shows its content.
            title: Text(configEntryName(_entries[index]) ?? configEntrySummary(_entries[index]), maxLines: 2, overflow: TextOverflow.ellipsis))),
      ))),
    ]),
  ));
}

class ConfigValueEditor extends StatefulWidget {
  const ConfigValueEditor({super.key, required this.title, required this.initial, required this.onSave,
    this.showHttpMatchHelp = false, this.showStreamScriptHelp = false, this.scriptType});
  final String title;
  final dynamic initial;
  final Future<bool> Function(dynamic value) onSave;
  final bool showHttpMatchHelp;
  final bool showStreamScriptHelp;
  final String? scriptType;
  @override State<ConfigValueEditor> createState() => _ConfigValueEditorState();
}
class _ConfigValueEditorState extends State<ConfigValueEditor> {
  late final _original = configEncoder.convert(widget.initial);
  late final _text = JsonEditorController()..text = _original;
  late String _lastText = _original;
  bool _saving = false;
  String? _error;
  @override void initState() { super.initState(); _text.addListener(_changed); }
  void _changed() {
    if (_lastText == _text.text) return;
    _lastText = _text.text;
    if (mounted) setState(() {});
  }
  String _eventName() {
    try { return json(decodeConfigJson(_text.text))['event'] as String? ?? 'network-changed'; }
    catch (_) { return 'network-changed'; }
  }

  void _setEvent(String event) {
    try {
      final value = decodeConfigJson(_text.text);
      if (value is! Map) throw const FormatException('JSON object required');
      _text.text = configEncoder.convert({...json(value), 'event': event});
      setState(() => _error = null);
    } catch (failure) { setState(() => _error = BoxController.describe(failure)); }
  }
  @override void dispose() { _text.dispose(); super.dispose(); }
  @override Widget build(BuildContext context) => BoxEditGuard(dirty: _text.text != _original, busy: _saving,
    child: BoxPage(title: widget.title, loading: _saving,
    confirmationAction: IconButtonData(glyph: AppGlyphs.check, tooltip: context.tr('Save'), onPressed: _saving ? null : () async {
      dynamic value;
      try { value = decodeConfigJson(_text.text); } catch (failure) { setState(() => _error = BoxController.describe(failure)); return; }
      setState(() { _saving = true; _error = null; });
      try {
        final saved = await widget.onSave(value);
        if (mounted && saved) context.safeNestedPop();
      } catch (failure) { if (mounted) setState(() => _error = BoxController.describe(failure)); }
      finally { if (mounted) setState(() => _saving = false); }
    }),
    children: [
      if (_error != null) Padding(padding: const EdgeInsets.only(bottom: 12), child: SelectableText(_error!, style: TextStyle(color: context.colorScheme.error))),
      if (widget.scriptType == 'event') ...[
        AbsorbPointer(absorbing: _saving, child: ListItem<String>.options(key: const ValueKey('script-event-trigger'), title: Text(context.tr('Event trigger')),
          options: const ['network-changed', 'engine-started', 'profile-reloaded'], value: _eventName(),
          textBuilder: (event) => context.tr(switch (event) {
            'network-changed' => 'Network changes', 'engine-started' => 'Engine startup', 'profile-reloaded' => 'Configuration reload', _ => event,
          }), onChanged: (event) { if (!_saving) _setEvent(event); })),
        ListFooter(text: context.tr('Events: network-changed for network changes, engine-started for engine startup, and profile-reloaded after configuration reload. Generic scripts run only on request.')),
      ],
      if (widget.scriptType != null)
        ListFooter(text: context.tr('Save and enable a cron, event or generic script, then use Run script in its menu. The selected local profile must be running. MITM is not required.')),
      BoxField(label: context.tr('Configuration'), borderRadius: AppRadius.md,
        child: TextField(controller: _text, readOnly: _saving, maxLines: 16, minLines: 8, autocorrect: false, enableSuggestions: false,
        smartDashesType: SmartDashesType.disabled, smartQuotesType: SmartQuotesType.disabled,
        textAlignVertical: TextAlignVertical.top,
        style: const TextStyle(fontFamily: 'JetBrainsMono', fontSize: 13, height: 1.6),
      )),
      if (widget.showHttpMatchHelp) ...[
        ListFooter(text: context.tr(_httpRuleMatchGuidance)),
        Padding(padding: const EdgeInsets.fromLTRB(16, 12, 16, 0), child: SelectableText(_httpRuleMatchExample,
          style: const TextStyle(fontFamily: 'JetBrainsMono', fontSize: 12, height: 1.5))),
      ],
      if (widget.showStreamScriptHelp)
        ListFooter(text: context.tr(r'Stream scripts process TCP chunks or complete WebSocket messages. Set type to tcp or websocket, and direction to client or server; omit direction for both. Use pattern to match the stream URL. $message contains body, direction, opcode, type, url, id, streamId and parentId. With binary_body, body is a Uint8Array; otherwise it is UTF-8 text. $done() keeps the message, $done({body: "..."}) replaces it, and $done({drop: true}) drops it. Use $done({inject: [{direction: "server", body: "...", opcode: 1}]}) to inject a message after it.')),
    ],
  ));
}
