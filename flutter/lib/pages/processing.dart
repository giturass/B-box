// Business adaptation of FlClash's MITM options, rule lists, scripts and module editor.
// GPL-3.0; see flclash-manifest.json for the independently pinned reference.
import 'dart:convert';

import '../widgets.dart';
import '../common/processing.dart';
import '../components/form.dart';
import 'configuration.dart';
import 'mitm_certificate.dart';
import 'capture_domains.dart';

enum ConcordList {
  hostname('hostname', 'Decryption domains', '# *.example.com'),
  url('url-rewrite', 'URL Rewrite', r'# ^https?://example\.com/ads _ reject-200'),
  header('header-rewrite', 'Header Rewrite', r'# http-request ^https?://example\.com/ header-add X-B-box 1'),
  body('body-rewrite', 'Body Rewrite', r'# http-response ^https?://example\.com/ "old" "new"'),
  local('map-local', 'Map Local / API Mock', r'# ^https?://example\.com/mock data="{}" data-type=text status-code=200'),
  requestMock('request-body-mock', 'Request body mock', r'# ^https?://example\.com/ data="{}" data-type=text'),
  script('script', 'Scripts', '');

  const ConcordList(this.keyName, this.title, this.example);
  final String keyName, title, example;
}

Json _config(ProfileConfigStore store) => processingConfig(store.root);

Widget? _legacyGate(BuildContext context, ProfileConfigStore store, String title) {
  final experimental = json(store.root['experimental']);
  const sections = ['mitm', 'http_rewrite', 'scripts'];
  if (!sections.any((key) => json(experimental[key])['enabled'] == true)) return null;
  final revision = store.revision;
  final candidate = Map<String, dynamic>.of(experimental);
  for (final key in sections) {
    if (candidate[key] is Map) candidate[key] = {...json(candidate[key]), 'enabled': false};
  }
  candidate['processing'] = {'enabled': false, 'mitm': {'decrypt-tls': false}};
  return ProcessingConfigScaffold(title: title, store: store, children: [
    ListFooter(text: context.tr('This profile uses the previous HTTP processor. Switching disables its MITM, rewrites and scripts and keeps their settings for manual migration.')),
    generateSection(items: [
      ListItem.open(key: const ValueKey('processing-migrate'), leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Review switch to Concord')),
        widget: ConfigValueEditor(title: 'Review switch to Concord', initial: candidate,
          onSave: (value) => store.set(['experimental'], value, expectedRevision: revision, rethrowFailure: true))),
      ListItem.open(title: Text(context.tr('Previous MITM settings')), widget: MitmConfigPage(store: store)),
      ListItem.open(title: Text(context.tr('Previous rewrite settings')), widget: HttpRewriteConfigPage(store: store)),
      ListItem.open(title: Text(context.tr('Previous script settings')), widget: ScriptsConfigPage(store: store)),
    ]),
  ]);
}

Future<void> _runProcessingScript(BuildContext context, ProfileConfigStore store, String name) async {
  final output = json(await context.box.call('processing.run', {'profileId': store.profile['id'], 'revision': store.revision, 'name': name}));
  if (!context.mounted) return;
  final text = processingScriptOutput(output);
  await showBoxDialog(context: context, builder: (context) => AlertDialog(title: name,
    child: SelectableText(text.isEmpty ? context.tr('Script produced no output') : text),
    actions: [TextButton(onPressed: () => Navigator.pop(context), child: Text(context.tr('Close')))]));
}

List<String> _lines(ProfileConfigStore store, ConcordList list) =>
  (store.value([...processingPath, 'mitm', list.keyName]) as List? ?? const []).whereType<String>().toList();
Json _candidate(ProfileConfigStore store, ConcordList list, List<String> lines) {
  final value = _config(store), mitm = json(_config(store)['mitm']);
  mitm[list.keyName] = lines;
  value['mitm'] = mitm;
  return value;
}

Widget _processingSwitch(BuildContext context, ProfileConfigStore store) => ListItem.toggle(
  key: const ValueKey('processing-enabled'), title: Text(context.tr('Enable HTTP processing')),
  value: _config(store)['enabled'] == true,
  onChanged: store.saving ? null : (value) => store.set([...processingPath, 'enabled'], value));

class ConcordMitmPage extends StatelessWidget {
  const ConcordMitmPage({super.key, required this.store});
  final ProfileConfigStore? store;

  @override Widget build(BuildContext context) {
    if (context.box.remote) return const BoxPage(title: 'MITM', children: [EmptyState('Disconnect remote session to edit local profiles')]);
    final store = this.store;
    Widget body() {
      final revision = store?.revision;
      return (store == null ? null : _legacyGate(context, store, 'MITM')) ?? ProcessingConfigScaffold(title: 'MITM', store: store, showProfileName: false, children: [
      generateSection(items: [
        ListItem.open(leading: const GlyphIcon(AppGlyphs.key), title: Text(context.tr('MITM certificate')), widget: const MitmCertificatePage()),
      ]),
      if (store == null) const EmptyState('No profile', glyph: AppGlyphs.profiles)
      else if (!store.ready) store.error == null ? const Center(child: ActivityIndicator.page()) : ErrorState(store.error!, retry: store.load)
      else ...[
        generateSection(items: [
          _processingSwitch(context, store),
          _toggle(context, store, 'decrypt-tls', 'Decrypt HTTPS', byDefault: true, key: 'mitm-enabled'),
          ListItem.open(key: const ValueKey('mitm-hosts'), leading: const GlyphIcon(AppGlyphs.dns), title: Text(context.tr('Decryption domains')),
            widget: ConcordLinesPage(store: store, list: ConcordList.hostname)),
          _toggle(context, store, 'h2', 'MITM over HTTP/2', key: 'mitm-h2'),
          _toggle(context, store, 'skip-server-cert-verify', 'Skip server certificate verification', key: 'mitm-skip-server-cert-verify'),
          _toggle(context, store, 'auto-quic-block', 'Request TCP fallback for QUIC', byDefault: true, key: 'mitm-block-quic'),
        ], footer: context.tr('MITM, rewrites, scripts and modules share the local HTTP processor. Request recording is controlled separately.')),
        generateSection(items: [
          ListItem.open(leading: const GlyphIcon(AppGlyphs.resources), title: Text(context.tr('Modules')), widget: ConcordModulesPage(store: store)),
          ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Edit MITM options')),
            widget: ConfigValueEditor(title: 'MITM configuration', initial: _config(store)['mitm'] ?? const {},
              onSave: (value) => store.set([...processingPath, 'mitm'], value, expectedRevision: revision, rethrowFailure: true))),
        ]),
      ],
      ]);
    }
    return store == null ? body() : ListenableBuilder(listenable: store, builder: (context, _) => body());
  }

  Widget _toggle(BuildContext context, ProfileConfigStore store, String field, String label, {bool byDefault = false, required String key}) =>
    ListItem.toggle(key: ValueKey(key), title: Text(context.tr(label)),
      value: byDefault ? store.value([...processingPath, 'mitm', field]) != false : store.value([...processingPath, 'mitm', field]) == true,
      onChanged: store.saving ? null : (value) => store.set([...processingPath, 'mitm', field], value));
}

class ConcordRewritePage extends StatelessWidget {
  const ConcordRewritePage({super.key, required this.store});
  final ProfileConfigStore store;
  @override Widget build(BuildContext context) => ListenableBuilder(listenable: store, builder: (context, _) {
    final legacy = _legacyGate(context, store, 'HTTP rewriting');
    if (legacy != null) return legacy;
    final revision = store.revision;
    return ProcessingConfigScaffold(title: 'HTTP rewriting', store: store, children: [
      generateSection(items: [_processingSwitch(context, store)]),
      generateSection(items: [
        for (final list in ConcordList.values.where((item) => item != ConcordList.hostname && item != ConcordList.script))
          ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr(list.title)), subtitle: Text('${_lines(store, list).length}'),
            widget: ConcordLinesPage(store: store, list: list)),
      ], footer: context.tr('Rules use Concord syntax. Commented lines are disabled. URL header rewrites route the new destination through sing-box.')),
      generateSection(items: [
        ListItem.open(leading: const GlyphIcon(AppGlyphs.resources), title: Text(context.tr('Rewrite resources')),
          widget: ProcessingResourcesPage(store: store, section: 'processing')),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Edit HTTP processing JSON')),
          widget: ConfigValueEditor(title: 'HTTP processing', initial: _config(store),
            onSave: (value) => store.set(processingPath, value, expectedRevision: revision, rethrowFailure: true))),
      ]),
    ]);
  });
}

class ConcordScriptsPage extends StatelessWidget {
  const ConcordScriptsPage({super.key, required this.store});
  final ProfileConfigStore store;
  @override Widget build(BuildContext context) => ListenableBuilder(listenable: store, builder: (context, _) =>
    _legacyGate(context, store, 'Scripts') ?? ProcessingConfigScaffold(title: 'Scripts', store: store, children: [
      generateSection(items: [
        _processingSwitch(context, store),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('HTTP and task scripts')),
          subtitle: Text('${_lines(store, ConcordList.script).length}'), widget: ConcordLinesPage(store: store, list: ConcordList.script)),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.resources), title: Text(context.tr('Script resources')),
          widget: ProcessingResourcesPage(store: store, section: 'processing')),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.resources), title: Text(context.tr('Modules')), widget: ConcordModulesPage(store: store)),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Module task scripts')), widget: ConcordModuleScriptsPage(store: store)),
      ], footer: context.tr('QuickJS runs HTTP request/response, cron, event and generic scripts. Task scripts can run now while this saved profile is active.')),
      ListFooter(text: context.tr('Cron runs while the local service is running. Missed intervals are not replayed.')),
    ]));
}

class ConcordLinesPage extends StatefulWidget {
  const ConcordLinesPage({super.key, required this.store, required this.list});
  final ProfileConfigStore store;
  final ConcordList list;
  @override State<ConcordLinesPage> createState() => _ConcordLinesPageState();
}

class _ConcordLinesPageState extends State<ConcordLinesPage> {
  final Set<String> _running = {};
  ProfileConfigStore get store => widget.store;
  ConcordList get list => widget.list;

  Future<void> _edit([int? index]) async {
    final lines = _lines(store, list), revision = store.revision;
    String? resource, source, previewError;
    var initial = index == null ? list.example : lines[index];
    if (list == ConcordList.script) {
      if (index == null) {
        final resources = json(_config(store)['resources']);
        var number = 1;
        while (resources.containsKey('script$number.js')) { number++; }
        resource = 'script$number.js';
        initial = '# script$number = type=http-response,pattern=^https?://example.com/,requires-body=true,script-path=$resource';
        source = r'$done({body: $response.body});';
      } else {
        try {
          final preview = json(await store.box.call('processing.validate', {'config': {
            'enabled': false, 'mitm': {'script': [initial.trimLeft().replaceFirst(RegExp(r'^#\s*'), '')]},
          }}));
          final view = rows(json(preview['lines'])['script']).firstOrNull;
          final name = view?['path'] as String?;
          final definition = json(json(_config(store)['resources'])[name]);
          if (name != null && definition['data_type'] == 'text') { resource = name; source = definition['data'] as String? ?? ''; }
        } catch (failure) {
          previewError = BoxController.describe(failure);
        }
      }
    }
    if (!mounted) return;
    await openForm(context, ConcordLineEditor(store: store, list: list, initial: initial, resource: resource, source: source, initialError: previewError,
      onSave: (line, resource, source) async {
        final next = List<String>.of(lines);
        if (index == null) { next.add(line); } else { next[index] = line; }
        final candidate = _candidate(store, list, next);
        if (resource != null && source != null) {
          final resources = json(candidate['resources']);
          resources[resource] = {'data_type': 'text', 'data': source};
          candidate['resources'] = resources;
        }
        return store.set(processingPath, candidate, expectedRevision: revision, rethrowFailure: true);
      }));
  }

  Future<void> _run(String name) async {
    if (_running.contains(name)) return;
    setState(() => _running.add(name));
    try {
      await _runProcessingScript(context, store, name);
    } catch (failure) { if (mounted) context.box.report(failure); }
    finally { if (mounted) setState(() => _running.remove(name)); }
  }

  @override Widget build(BuildContext context) => ListenableBuilder(listenable: store, builder: (context, _) {
    final lines = _lines(store, list), revision = store.revision;
    final scripts = rows(json(json(store.stats['processing'])['lines'])['script']);
    Widget row(int index) {
      final line = lines[index], enabled = !line.trimLeft().startsWith('#');
      final script = list == ConcordList.script ? scripts.where((view) => view['module'] == null && view['index'] == index).firstOrNull : null;
      final name = script?['name'] as String?;
      final menu = [
        PopupMenuItem(glyph: AppGlyphs.edit, label: context.tr('Edit'), onPressed: () => _edit(index)),
        if (name != null && script?['task'] == true && enabled && _config(store)['enabled'] == true && context.box.running && !context.box.remote)
          PopupMenuItem(glyph: AppGlyphs.playPause(0), label: context.tr('Run now'), onPressed: _running.contains(name) ? null : () => _run(name)),
        PopupMenuItem(glyph: AppGlyphs.delete, label: context.tr('Delete'), danger: true, onPressed: () async {
          if (await confirm(context, 'Delete', message: line)) await store.set([...processingPath, 'mitm', list.keyName],
            List<String>.of(lines)..removeAt(index), expectedRevision: revision);
        }),
      ];
      return SortableItem(key: ValueKey('$index:$line'), index: index, child: ContextMenuRegion(menuItems: store.saving ? null : menu,
        child: ItemPositionProvider(position: ItemPosition.get(index, lines.length), child: DecorationListItem(
          contentPadding: const EdgeInsets.only(left: 16), title: Text(name ?? line, maxLines: 2, overflow: TextOverflow.ellipsis),
          subtitle: script == null ? null : Text(script['kind']?.toString() ?? '', style: context.listCaptionStyle),
          trailing: Padding(padding: const EdgeInsetsDirectional.only(end: 8), child: _running.contains(name)
            ? const ActivityIndicator(size: 24) : Switch(value: enabled, onChanged: store.saving ? null : (value) {
                final next = List<String>.of(lines);
                next[index] = value ? line.trimLeft().replaceFirst(RegExp(r'^#\s*'), '') : '# $line';
                store.set([...processingPath, 'mitm', list.keyName], next, expectedRevision: revision);
              })),
          onPressed: store.saving ? null : () => _edit(index),
        ))));
    }
    return Scaffold(title: context.tr(list.title), canSort: lines.length > 1, isLoading: store.saving,
      primaryAction: IconButtonData(glyph: AppGlyphs.add, tooltip: context.tr('Add'), onPressed: store.saving ? null : () => _edit()),
      iconActions: [if (list == ConcordList.hostname) IconButtonData(glyph: AppGlyphs.addCircle, tooltip: context.tr('Add captured domains'),
        onPressed: store.saving ? null : () async {
          final selected = await openPage<List<String>>(context, const CaptureDomainsPage(select: true));
          if (selected != null && selected.isNotEmpty) await store.set([...processingPath, 'mitm', list.keyName],
            [...lines, ...selected.where((item) => !lines.contains(item))], expectedRevision: revision);
        })],
      body: lines.isEmpty ? NullStatus(label: context.tr('No results'), illustration: NullStatusIllustration.scripts)
        : AbsorbPointer(absorbing: store.saving, child: PrismReorderableListView(
          padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 16 + context.bottomInset),
          itemCount: lines.length, itemBuilder: (_, index) => row(index),
          proxyDecorator: (_, index, animation) => commonProxyDecorator(row(index), index, animation),
          onReorderItem: (old, next) {
            final reordered = List<String>.of(lines);
            final item = reordered.removeAt(old);
            reordered.insert(next, item);
            store.set([...processingPath, 'mitm', list.keyName], reordered, expectedRevision: revision);
          },
        )));
  });
}

class ConcordLineEditor extends StatefulWidget {
  const ConcordLineEditor({super.key, required this.store, required this.list, required this.initial, required this.onSave, this.resource, this.source, this.initialError});
  final ProfileConfigStore store;
  final ConcordList list;
  final String initial;
  final String? resource, source, initialError;
  final Future<bool> Function(String line, String? resource, String? source) onSave;
  @override State<ConcordLineEditor> createState() => _ConcordLineEditorState();
}
class _ConcordLineEditorState extends State<ConcordLineEditor> {
  late final _line = TextEditingController(text: widget.initial);
  late final _resource = TextEditingController(text: widget.resource ?? '');
  late final _source = TextEditingController(text: widget.source ?? '');
  bool _busy = false;
  late String? _error = widget.initialError;
  bool get _dirty => _line.text != widget.initial || _resource.text != (widget.resource ?? '') || _source.text != (widget.source ?? '');
  @override void dispose() { _line.dispose(); _resource.dispose(); _source.dispose(); super.dispose(); }
  Future<void> _save() async {
    setState(() { _busy = true; _error = null; });
    try {
      if (_line.text.trim().isEmpty || _line.text.contains('\n')) throw const FormatException('Enter one rule per item');
      if (widget.resource != null) {
        await context.box.call('processing.validateScript', {'source': _source.text});
        final preview = json(await widget.store.box.call('processing.validate', {'config': {
          'enabled': false, 'mitm': {'script': [_line.text.trimLeft().replaceFirst(RegExp(r'^#\s*'), '')]},
        }}));
        final parsed = rows(json(preview['lines'])['script']);
        if (parsed.length != 1 || parsed.single['path'] != _resource.text.trim()) {
          throw const FormatException('Resource name must match script-path');
        }
      }
      final saved = await widget.onSave(_line.text.trim(), widget.resource == null ? null : _resource.text.trim(), widget.resource == null ? null : _source.text);
      if (mounted && saved) context.safeNestedPop();
    } catch (failure) { if (mounted) setState(() => _error = BoxController.describe(failure)); }
    finally { if (mounted) setState(() => _busy = false); }
  }
  @override Widget build(BuildContext context) => BoxEditGuard(dirty: _dirty, busy: _busy, child: FormPage(
    title: widget.list.title, saving: _busy, error: _error, onSave: _save, children: [
      ListFooter(text: context.tr(widget.list == ConcordList.hostname
        ? 'Supports * and ? wildcards, - exclusions and :port (:0 for all ports). First match wins; put exclusions first. Leave empty to decrypt nothing.'
        : 'Rules use Concord syntax. Commented lines are disabled. URL header rewrites route the new destination through sing-box.')),
      BoxField(label: context.tr('Rule'), borderRadius: AppRadius.md, child: TextField(key: const ValueKey('processing-line'),
        controller: _line, readOnly: _busy, maxLines: 6, minLines: 2, autocorrect: false, enableSuggestions: false,
        smartDashesType: SmartDashesType.disabled, smartQuotesType: SmartQuotesType.disabled, onChanged: (_) => setState(() {}))),
      if (widget.resource != null) ...[
        BoxField(label: context.tr('Resource name'), child: TextField(controller: _resource, readOnly: _busy, onChanged: (_) => setState(() {}))),
        BoxField(label: context.tr('JavaScript source'), borderRadius: AppRadius.md, child: TextField(controller: _source, readOnly: _busy,
          maxLines: 20, minLines: 8, autocorrect: false, enableSuggestions: false, keyboardType: TextInputType.multiline,
          smartDashesType: SmartDashesType.disabled, smartQuotesType: SmartQuotesType.disabled, onChanged: (_) => setState(() {}))),
      ],
    ]));
}

class ConcordModulesPage extends StatefulWidget {
  const ConcordModulesPage({super.key, required this.store});
  final ProfileConfigStore store;
  @override State<ConcordModulesPage> createState() => _ConcordModulesPageState();
}
class _ConcordModulesPageState extends State<ConcordModulesPage> {
  bool _importing = false;
  ProfileConfigStore get store => widget.store;
  List<Json> get _modules => rows(json(_config(store)['mitm'])['modules']);
  Future<void> _edit({Json? module, String? text, String? url, String? name}) async {
    await openForm(context, ConcordModuleEditor(store: store, module: module, text: text, url: url, name: name));
  }
  Future<void> _import() async {
    setState(() => _importing = true);
    try {
      final imported = await context.box.call('processing.importModule');
      if (imported != null && mounted) {
        final data = json(imported);
        await _edit(text: data['text'] as String, name: data['name'] as String);
      }
    } catch (failure) { if (mounted) context.box.report(failure); }
    finally { if (mounted) setState(() => _importing = false); }
  }
  @override Widget build(BuildContext context) => ListenableBuilder(listenable: store, builder: (context, _) {
    final legacy = _legacyGate(context, store, 'Modules');
    if (legacy != null) return legacy;
    final modules = _modules, revision = store.revision, urls = json(_config(store)['module_urls']);
    Widget row(int index) {
      final module = modules[index], name = module['name'] as String, url = urls[name] as String?;
      return SortableItem(key: ValueKey(name), index: index, child: ContextMenuRegion(menuItems: store.saving ? null : [
        PopupMenuItem(glyph: AppGlyphs.edit, label: context.tr('Edit'), onPressed: () => _edit(module: module, url: url)),
        if (url != null) PopupMenuItem(glyph: AppGlyphs.sync, label: context.tr('Update'), onPressed: () => _edit(module: module, url: url)),
        PopupMenuItem(glyph: AppGlyphs.code, label: context.tr('Module task scripts'),
          onPressed: () => openPage(context, ConcordModuleScriptsPage(store: store, moduleName: name))),
        PopupMenuItem(glyph: AppGlyphs.delete, label: context.tr('Delete'), danger: true, onPressed: () async {
          if (!await confirm(context, 'Delete', message: name)) return;
          final value = _config(store), mitm = json(_config(store)['mitm']);
          mitm['modules'] = List<Json>.of(modules)..removeAt(index);
          value['mitm'] = mitm;
          value['module_urls'] = json(urls)..remove(name);
          await store.set(processingPath, value, expectedRevision: revision);
        }),
      ], child: ItemPositionProvider(position: ItemPosition.get(index, modules.length), child: DecorationListItem(
        contentPadding: const EdgeInsets.only(left: 16), title: Text(name, maxLines: 1, overflow: TextOverflow.ellipsis),
        subtitle: Text(context.tr(url == null ? 'Local' : 'Remote'), style: context.listCaptionStyle),
        trailing: Padding(padding: const EdgeInsetsDirectional.only(end: 8), child: Switch(value: module['enabled'] != false,
          onChanged: store.saving ? null : (enabled) async {
            if (enabled) { await _edit(module: {...module, 'enabled': true}, url: url); return; }
            final next = List<Json>.of(modules);
            next[index] = {...module, 'enabled': false};
            await store.set([...processingPath, 'mitm', 'modules'], next, expectedRevision: revision);
          })),
        onPressed: () => _edit(module: module, url: url),
      ))));
    }
    return Scaffold(title: context.tr('Modules'), canSort: modules.length > 1, isLoading: _importing || store.saving,
      menuItems: [
        PopupMenuItem(glyph: AppGlyphs.compose, label: context.tr('New module'), onPressed: () => _edit()),
        PopupMenuItem(glyph: AppGlyphs.cloudDownload, label: context.tr('Import URL'), onPressed: () => _edit(url: '')),
        PopupMenuItem(glyph: AppGlyphs.importFile, label: context.tr('Import file'), onPressed: _importing ? null : _import),
      ],
      body: modules.isEmpty ? NullStatus(label: context.tr('No modules'), illustration: NullStatusIllustration.scripts,
        action: TonalButton(size: TonalButtonSize.bar, glyph: AppGlyphs.add, label: context.tr('New module'), onPressed: () => _edit()))
        : AbsorbPointer(absorbing: store.saving, child: PrismReorderableListView(
          padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 16 + context.bottomInset),
          itemCount: modules.length, itemBuilder: (_, index) => row(index),
          proxyDecorator: (_, index, animation) => commonProxyDecorator(row(index), index, animation),
          onReorderItem: (old, next) {
            final reordered = List<Json>.of(modules), item = modules[old];
            reordered.removeAt(old); reordered.insert(next, item);
            store.set([...processingPath, 'mitm', 'modules'], reordered, expectedRevision: revision);
          },
        )));
  });
}

class ConcordModuleScriptsPage extends StatefulWidget {
  const ConcordModuleScriptsPage({super.key, required this.store, this.moduleName});
  final ProfileConfigStore store;
  final String? moduleName;
  @override State<ConcordModuleScriptsPage> createState() => _ConcordModuleScriptsPageState();
}

class _ConcordModuleScriptsPageState extends State<ConcordModuleScriptsPage> {
  final Set<String> _running = {};
  Future<void> _run(String name) async {
    if (_running.contains(name)) return;
    setState(() => _running.add(name));
    try { await _runProcessingScript(context, widget.store, name); }
    catch (failure) { if (mounted) context.box.report(failure); }
    finally { if (mounted) setState(() => _running.remove(name)); }
  }
  @override Widget build(BuildContext context) => ListenableBuilder(listenable: widget.store, builder: (context, _) {
    final scripts = rows(json(json(widget.store.stats['processing'])['lines'])['script']).where((script) =>
      script['task'] == true && script['module'] != null && (widget.moduleName == null || script['module'] == widget.moduleName)).toList();
    return ProcessingConfigScaffold(title: 'Module task scripts', store: widget.store, children: [
      if (scripts.isEmpty) const EmptyState('No results')
      else generateSection(items: [
        for (final script in scripts) ListItem(
          title: Text(script['name'] as String), subtitle: Text('${script['module']} · ${script['kind']}'),
          trailing: _running.contains(script['name']) ? const ActivityIndicator(size: 24) : null,
          onTap: _config(widget.store)['enabled'] == true && context.box.running && !context.box.remote && !_running.contains(script['name'])
            ? () => _run(script['name'] as String) : null,
        ),
      ], footer: context.tr('Task scripts can run while this saved profile is active.')),
    ]);
  });
}

class ConcordModuleEditor extends StatefulWidget {
  const ConcordModuleEditor({super.key, required this.store, this.module, this.text, this.url, this.name});
  final ProfileConfigStore store;
  final Json? module;
  final String? text, url, name;
  @override State<ConcordModuleEditor> createState() => _ConcordModuleEditorState();
}
class _ConcordModuleEditorState extends State<ConcordModuleEditor> {
  late final _name = TextEditingController(text: widget.module?['name'] as String? ?? widget.name ?? '');
  late final _text = TextEditingController(text: widget.text ?? widget.module?['text'] as String? ??
    '#!name=New module\n#!desc=\n\n[URL Rewrite]\n\n[MITM]\nhostname = %APPEND%\n');
  late final _url = TextEditingController(text: widget.url ?? '');
  late final _attachments = TextEditingController(text: configEncoder.convert(widget.module?['attachments'] ?? const {}));
  late final Json _arguments = json(widget.module?['arguments']);
  late String _revision;
  late bool _enabled, _httpOnly;
  bool _busy = false, _dirty = false;
  String? _error, _download;
  Json? _preview;
  List<Json> _argumentDefinitions = [];
  ProfileConfigStore get store => widget.store;
  @override void initState() {
    super.initState();
    _revision = store.revision;
    _enabled = widget.module != null && widget.module?['enabled'] != false;
    _httpOnly = _config(store)['http_only_modules'] == true;
  }
  @override void dispose() { _name.dispose(); _text.dispose(); _url.dispose(); _attachments.dispose(); super.dispose(); }
  void _changed() => setState(() { _dirty = true; _preview = null; });
  Json _attachmentValues() {
    final value = jsonDecode(_attachments.text);
    if (value is! Map || value.values.any((item) => item is! String)) throw const FormatException('Attachments must map names to resource names');
    return json(value);
  }
  bool get _unapplied => _preview != null && (rows(_preview!['rules']).isNotEmpty || (_preview!['ruleSets'] as List? ?? const []).isNotEmpty ||
    json(_preview!['network']).isNotEmpty);
  Future<Json> _read() async {
    final preview = json(await store.box.call('processing.readModule', {'text': _text.text, 'arguments': _arguments, 'attachments': _attachmentValues()}));
    if (mounted) setState(() { _preview = preview; _argumentDefinitions = rows(preview['arguments']); });
    return preview;
  }
  Future<void> _previewModule() async {
    setState(() { _busy = true; _error = null; });
    try { await _read(); } catch (failure) { if (mounted) setState(() => _error = BoxController.describe(failure)); }
    finally { if (mounted) setState(() => _busy = false); }
  }
  Future<void> _fetch() async {
    final urls = processingModuleUrls(_url.text);
    if (urls.length != 1) { setState(() => _error = context.tr('Enter one module URL or install link')); return; }
    final id = 'module-${DateTime.now().microsecondsSinceEpoch}';
    setState(() { _busy = true; _error = null; _download = id; });
    try {
      final result = json(await store.box.call('processing.fetchModule', {'requestId': id, 'url': urls.single}));
      if (mounted) {
        setState(() { _text.text = result['text'] as String; _url.text = urls.single; _dirty = true; });
        await _read();
        if (mounted && _name.text.isEmpty) _name.text = json(_preview?['meta'])['name'] as String? ?? urls.single.split('/').last;
      }
    } catch (failure) { if (mounted) setState(() => _error = BoxController.describe(failure)); }
    finally { if (mounted) setState(() { _busy = false; _download = null; }); }
  }
  Future<void> _save() async {
    setState(() { _busy = true; _error = null; });
    try {
      final preview = _preview ?? await _read(), name = _name.text.trim();
      if (name.isEmpty) throw const FormatException('Module name must not be empty');
      if (_enabled && _unapplied && !_httpOnly) throw const FormatException('Review unapplied DNS and routing settings, then choose HTTP processing only');
      if (_enabled && preview['off'] != null) throw const FormatException('Module requirements are not met');
      final value = _config(store), mitm = json(_config(store)['mitm']), modules = rows(json(_config(store)['mitm'])['modules']);
      final oldName = widget.module?['name'], index = modules.indexWhere((module) => module['name'] == oldName);
      if (modules.indexed.any((entry) => entry.$1 != index && entry.$2['name'] == name)) throw const FormatException('Module name already exists');
      final module = {'name': name, 'text': _text.text, 'enabled': _enabled, 'arguments': _arguments, 'attachments': _attachmentValues()};
      if (index < 0) { modules.add(module); } else { modules[index] = module; }
      mitm['modules'] = modules; value['mitm'] = mitm;
      final urls = json(value['module_urls'])..remove(oldName);
      if (_url.text.trim().isNotEmpty) {
        final input = processingModuleUrls(_url.text);
        if (input.length != 1) throw const FormatException('Enter one module URL or install link');
        urls[name] = input.single;
      }
      value['module_urls'] = urls;
      value['http_only_modules'] = _httpOnly;
      final saved = await store.set(processingPath, value, expectedRevision: _revision, rethrowFailure: true);
      if (mounted && saved) context.safeNestedPop();
    } catch (failure) { if (mounted) setState(() => _error = BoxController.describe(failure)); }
    finally { if (mounted) setState(() => _busy = false); }
  }
  Future<void> _attach() async {
    setState(() { _busy = true; _error = null; });
    try {
      if (_revision != store.revision) throw StateError('Configuration changed; reload it before saving');
      final values = _attachmentValues(), name = await store.importResource('processing');
      if (name != null && mounted) {
        values[name] = name;
        setState(() { _revision = store.revision; _attachments.text = configEncoder.convert(values); _dirty = true; _preview = null; });
      }
    } catch (failure) { if (mounted) setState(() => _error = BoxController.describe(failure)); }
    finally { if (mounted) setState(() => _busy = false); }
  }
  @override Widget build(BuildContext context) => BoxEditGuard(dirty: _dirty, busy: _busy, child: FormPage(
    title: widget.module == null ? 'New module' : 'Module', saving: _busy, error: _error, onSave: _save, children: [
      BoxField(label: context.tr('Name'), child: TextField(key: const ValueKey('module-name'), controller: _name, readOnly: _busy, onChanged: (_) => _changed())),
      if (widget.url != null) ...[
        BoxField(label: context.tr('URL'), child: TextField(key: const ValueKey('module-url'), controller: _url, readOnly: _busy, onChanged: (_) => _changed())),
        ListItem(title: Text(context.tr(widget.module == null ? 'Download and preview' : 'Update and preview')), onTap: _busy ? null : _fetch),
      ],
      if (_download != null) ListItem(title: Text(context.tr('Cancel download')), onTap: () => store.box.call('processing.cancel', {'requestId': _download})),
      BoxField(label: context.tr('Module content'), borderRadius: AppRadius.md, child: TextField(key: const ValueKey('module-content'),
        controller: _text, readOnly: _busy, minLines: 8, maxLines: 20, autocorrect: false, enableSuggestions: false,
        smartDashesType: SmartDashesType.disabled, smartQuotesType: SmartQuotesType.disabled, onChanged: (_) => _changed())),
      if (_argumentDefinitions.isNotEmpty) generateSection(title: context.tr('Arguments'), items: [
        for (final argument in _argumentDefinitions) _argument(argument),
      ]),
      BoxField(label: context.tr('Attachments'), borderRadius: AppRadius.md, child: TextField(controller: _attachments, readOnly: _busy,
        minLines: 2, maxLines: 6, autocorrect: false, enableSuggestions: false, onChanged: (_) => _changed())),
      ListItem(title: Text(context.tr('Import attachment')), onTap: _busy || store.saving ? null : _attach),
      ListItem(key: const ValueKey('module-preview'), title: Text(context.tr('Preview module')), onTap: _busy ? null : _previewModule),
      if (_preview != null) ..._previewWidgets(context, _preview!),
      generateSection(items: [
        ListItem.toggle(key: const ValueKey('module-enabled'), title: Text(context.tr('Enable module')), value: _enabled,
          onChanged: _busy ? null : (value) => setState(() { _enabled = value; _dirty = true; })),
        if (_unapplied) ListItem.toggle(key: const ValueKey('module-http-only'), title: Text(context.tr('Apply only HTTP processing from all modules')), value: _httpOnly,
          onChanged: _busy ? null : (value) => setState(() { _httpOnly = value; _dirty = true; })),
      ]),
    ]));
  Widget _argument(Json argument) {
    final name = argument['name'] as String, kind = json(argument['kind']), title = argument['label'] as String? ?? name;
    final current = _arguments[name]?.toString() ?? argument['default']?.toString() ?? '';
    void changed(String value) => setState(() { _arguments[name] = value; _dirty = true; _preview = null; });
    return switch (kind['type']) {
      'switch' => ListItem.toggle(title: Text(title), value: current == 'true', onChanged: _busy ? null : (value) => changed('$value')),
      'select' => ListItem<String>.options(title: Text(title), options: (kind['options'] as List? ?? const []).whereType<String>().toList(),
        value: current, textBuilder: (value) => value, onChanged: _busy ? null : changed),
      _ => ListItem.input(title: Text(title), dialogTitle: title, value: current,
        onChanged: _busy ? null : (value) { if (value != null) changed(value); }),
    };
  }
}

List<Widget> _previewWidgets(BuildContext context, Json preview) {
  final meta = json(preview['meta']), lines = json(preview['lines']);
  return [
    if (meta['desc'] is String) ListFooter(text: meta['desc'] as String),
    if (preview['off'] != null) SelectableText(configEncoder.convert(preview['off']), style: TextStyle(color: context.colorScheme.error)),
    for (final entry in lines.entries) if (rows(entry.value).isNotEmpty) generateSection(title: entry.key, items: [
      for (final line in rows(entry.value)) ListItem(title: SelectableText(line['text']?.toString() ?? '')),
    ]),
    if (json(preview['issues']).isNotEmpty) generateSection(title: context.tr('Validation errors'), items: [
      ListItem(title: SelectableText(configEncoder.convert(preview['issues']))),
    ]),
    if (rows(preview['resources']).isNotEmpty) generateSection(title: context.tr('Resources'), items: [
      for (final resource in rows(preview['resources'])) ListItem(title: SelectableText(configEncoder.convert(resource))),
    ]),
    if (rows(preview['rules']).isNotEmpty || (preview['ruleSets'] as List? ?? const []).isNotEmpty || json(preview['network']).isNotEmpty)
      generateSection(title: context.tr('Unapplied DNS and routing settings'), items: [
        ListItem(title: SelectableText(configEncoder.convert({'rules': preview['rules'], 'ruleSets': preview['ruleSets'], 'network': preview['network']}))),
      ], footer: context.tr('These settings remain in the module text. Configure DNS and routing in the sing-box profile.')),
    if (rows(preview['ignored']).isNotEmpty) generateSection(title: context.tr('Unapplied module lines'), items: [
      for (final line in rows(preview['ignored'])) ListItem(title: Text(line['text']?.toString() ?? ''), subtitle: Text(line['detail']?.toString() ?? line['reason']?.toString() ?? '')),
    ]),
  ];
}
