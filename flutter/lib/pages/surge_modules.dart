import 'dart:async';
import 'dart:convert';
import 'dart:math';

import '../components/code_editor.dart';
import '../components/profile_card.dart';
import '../widgets.dart';
import 'configuration.dart';

const _path = ['experimental', 'modules'];
const _sections = {
  'mitm': 'Hostname', 'url rewrite': 'URL rewrite', 'header rewrite': 'Header rewrite',
  'body rewrite': 'Body rewrite', 'map local': 'Map local', 'script': 'Script', 'host': 'Host',
  'rule': 'Routing rules', 'ignored': 'Ignored lines',
};

extension _ModuleLabels on BuildContext {
  String moduleTr(String value) => Localizations.localeOf(this).languageCode == 'zh' ? const {
    'Source':'来源', 'Enabled':'已开启', 'Routing rules':'分流规则', 'Import from URL':'通过URL导入',
  }[value] ?? tr(value) : value;
}

String moduleCount(BuildContext context, int count) => context.tr(count == 1 ? '1 item' : '{count} items').replaceAll('{count}', '$count');

List<String> surgeModuleUrls(String text) {
  final urls = <String>{};
  for (final token in text.trim().split(RegExp(r'\s+'))) {
    var uri = Uri.tryParse(token);
    if (uri != null && uri.scheme == 'surge' && '${uri.host}${uri.path}'.replaceAll('/', '') == 'install-module') {
      uri = Uri.tryParse(uri.queryParameters['url'] ?? '');
    }
    if (uri == null || !{'http','https'}.contains(uri.scheme) || uri.host.isEmpty) throw const FormatException('Enter an HTTP or HTTPS URL');
    urls.add(uri.toString());
  }
  return urls.toList();
}

class ModuleActions extends ChangeNotifier {
  ModuleActions(this.store) : session = store.box.sessionRevision;
  final ProfileConfigStore store;
  final int session;
  String? working;
  bool _disposed = false;
  List<Json> get modules => store.entries(_path);
  bool get local => !_disposed && !store.box.remote && store.box.sessionRevision == session;
  Json? find(String id) => modules.where((item) => item['id'] == id).firstOrNull;

  void check(String revision) {
    if (!local || store.revision != revision || store.loading) throw StateError('Configuration changed; reload it before saving');
  }

  Future<bool> action(String key, Future<void> Function() body) async {
    if (!local || working != null || store.saving) return false;
    working = key; notifyListeners();
    try { await body(); return true; }
    catch (failure) { if (local) store.box.report(failure); return false; }
    finally { working = null; if (!_disposed) notifyListeners(); }
  }

  Future<void> write(List<Json> next, String revision) async {
    check(revision);
    if (!await store.set(_path, next, remove: next.isEmpty, expectedRevision: revision, rethrowFailure: true)) {
      throw StateError('Configuration changed; reload it before saving');
    }
  }

  Future<void> put(Json next, String revision) => write([
    for (final item in modules) item['id'] == next['id'] ? next : item,
    if (find(next['id'] as String) == null) next,
  ], revision);

  Future<Json> describe(Json item) async => json(jsonDecode(await store.box.call('profiles.moduleDescribe', {'module': jsonEncode(item)}) as String));
  Future<Json> details(String id, String revision) async => json(jsonDecode(await store.box.call('profiles.moduleDetails', {
    'id': store.profile['id'], 'revision': revision, 'moduleId': id,
  }) as String));

  Future<bool> consent(BuildContext context) => confirm(context, 'Run scripts from this module?',
    message: 'Its scripts can read and change traffic for matching hosts, including cookies and tokens, and send requests. Only enable scripts from a source you trust.', action: 'Turn on');

  Future<void> save(BuildContext context, Json candidate, String revision, {String? fallback}) async {
    check(revision);
    final previous = candidate['id'] == null ? null : find(candidate['id'] as String);
    final info = await describe(candidate);
    check(revision);
    final content = candidate['content'] as String;
    if (!RegExp(r'^\s*\[(?:MITM|Rule|Host|Script|URL Rewrite|Header Rewrite|Body Rewrite|Map Local)\]\s*$', multiLine: true, caseSensitive: false).hasMatch(content)) {
      throw const FormatException('No supported Surge module section');
    }
    if (!context.mounted) throw StateError('Module editor closed');
    var enabled = previous == null || previous['enabled'] == true;
    final changed = previous == null || previous['content'] != content ||
      jsonEncode(previous['attachments']) != jsonEncode(candidate['attachments']) || jsonEncode(previous['arguments']) != jsonEncode(candidate['arguments']);
    if (enabled && changed && info['hasScripts'] == true) enabled = await consent(context);
    if (!context.mounted) throw StateError('Module editor closed');
    final name = info['name'] as String? ?? '';
    final random = Random.secure();
    await put({...candidate, 'id': candidate['id'] ?? List.generate(16, (_) => random.nextInt(256).toRadixString(16).padLeft(2, '0')).join(),
      'name': name.isNotEmpty ? name : (fallback ?? candidate['name'] ?? context.tr('Module')), 'enabled': enabled,
      'updated_at': DateTime.now().millisecondsSinceEpoch}, revision);
  }

  Future<void> toggle(BuildContext context, Json item, bool enabled, String revision) async {
    await action(item['id'] as String, () async {
      check(revision);
      if (enabled && (await describe(item))['hasScripts'] == true) {
        if (!context.mounted || !await consent(context)) return;
      }
      if (context.mounted) await put({...item, 'enabled': enabled}, revision);
    });
  }

  Future<void> update(BuildContext context, Json item, String revision) async {
    await action(item['id'] as String, () async {
      final content = await store.box.call('profiles.moduleFetch', {'url': item['url']});
      if (context.mounted) await save(context, {...item, 'content': content}, revision);
    });
  }

  Future<void> edit(BuildContext context, Json? item, String revision) => BaseNavigator.push<void>(context,
    _ModuleEditor(actions: this, item: item, revision: revision));

  @override void dispose() { _disposed = true; super.dispose(); }
}

class SurgeModulesPage extends StatefulWidget {
  const SurgeModulesPage({super.key, required this.store});
  final ProfileConfigStore store;
  @override State<SurgeModulesPage> createState() => _SurgeModulesPageState();
}

class _SurgeModulesPageState extends State<SurgeModulesPage> {
  late final actions = ModuleActions(widget.store);
  @override void dispose() { actions.dispose(); super.dispose(); }

  Future<void> _import(bool remote) async {
    var revision = widget.store.revision;
    await actions.action('import', () async {
      if (!remote) {
        final file = await widget.store.box.call('profiles.moduleFile');
        if (!mounted || file == null) return;
        final data = json(file);
        await actions.save(context, {'content': data['content']}, revision, fallback: data['name'] as String?);
        return;
      }
      final text = await showBoxDialog<String>(context: context, builder: (context) => InputDialog(
        title: context.moduleTr('Import from URL'), value: '', keyboardType: TextInputType.url));
      if (!mounted || text == null) return;
      final urls = surgeModuleUrls(text).toSet();
      final known = actions.modules.map((item) => item['url']).toSet();
      urls.removeAll(known.whereType<String>());
      if (urls.isEmpty) throw const FormatException('Module already exists');
      for (final url in urls) {
        if (!mounted) return;
        actions.check(revision);
        final uri = Uri.parse(url);
        final content = await widget.store.box.call('profiles.moduleFetch', {'url': url});
        if (!mounted) return;
        await actions.save(context, {'url': url, 'content': content}, revision, fallback: uri.pathSegments.lastOrNull ?? uri.host);
        if (!mounted) return;
        revision = widget.store.revision;
      }
    });
  }

  @override Widget build(BuildContext context) => ListenableBuilder(listenable: actions, builder: (context, _) {
    final modules = actions.modules, revision = widget.store.revision;
    final available = actions.local && actions.working == null && !widget.store.saving;
    Widget row(int index) {
      final item = modules[index], id = item['id'] as String, remote = item['url'] != null;
      return SortableItem(key: ValueKey(id), index: index, child: ContextMenuRegion(menuItems: !available ? null : [
        if (remote) PopupMenuItem(glyph: AppGlyphs.sync, label: context.tr('Update'), onPressed: () => actions.update(context, item, revision))
        else PopupMenuItem(glyph: AppGlyphs.edit, label: context.tr('Edit'), onPressed: () => actions.edit(context, item, revision)),
        PopupMenuItem(danger: true, glyph: AppGlyphs.delete, label: context.tr('Delete'), onPressed: () => actions.action(id, () async {
          if (await confirm(context, 'Tip', message: 'Delete this module?')) await actions.write(actions.modules.where((entry) => entry['id'] != id).toList(), revision);
        })),
      ], child: ItemPositionProvider(position: ItemPosition.get(index, modules.length), child: DecorationListItem(
        contentPadding: const EdgeInsets.only(left: 16), title: Text(item['name'] as String? ?? context.tr('Module'), maxLines: 1, overflow: TextOverflow.ellipsis),
        subtitle: Text(context.tr(remote ? 'Remote' : 'Local'), style: context.listCaptionStyle),
        trailing: Padding(padding: const EdgeInsetsDirectional.only(end: 8), child: actions.working == id
          ? const Padding(padding: EdgeInsetsDirectional.only(end: 8), child: ActivityIndicator(size: 24))
          : Switch(value: item['enabled'] == true, onChanged: available ? (value) => actions.toggle(context, item, value, revision) : null)),
        onPressed: () => BaseNavigator.push(context, _ModuleDetail(actions: actions, id: id)),
      ))));
    }
    return Scaffold(title: context.tr('Modules'), canSort: modules.length > 1, isLoading: actions.working == 'import' || widget.store.saving,
      actions: [PopupAnchor(targetBuilder: (open) => IconButton(tooltip: context.tr('Add'), onPressed: available ? open : null, icon: const GlyphIcon(AppGlyphs.addCircle)),
        popupBuilder: (_) => PopupMenu(items: [
          PopupMenuItem(glyph: AppGlyphs.compose, label: context.tr('New module'), onPressed: () => actions.edit(context, null, revision)),
          PopupMenuItem(glyph: AppGlyphs.cloudDownload, label: context.moduleTr('Import from URL'), onPressed: () => _import(true)),
          PopupMenuItem(glyph: AppGlyphs.importFile, label: context.tr('Import from file'), onPressed: () => _import(false)),
        ]))],
      body: NullStatusSwitcher(isEmpty: modules.isEmpty, nullStatus: NullStatus(illustration: NullStatusIllustration.scripts, label: context.tr('No module yet')),
        child: PrismReorderableListView(padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 16 + context.bottomInset),
          itemCount: modules.length, itemBuilder: (_, index) => row(index), proxyDecorator: (_, index, animation) => commonProxyDecorator(row(index), index, animation),
          onReorderItem: (old, next) { if (available) actions.action('sort', () { final items = List.of(modules); items.insert(next, items.removeAt(old)); return actions.write(items, revision); }); })));
  });
}

class _ModuleEditor extends StatefulWidget {
  const _ModuleEditor({required this.actions, required this.item, required this.revision});
  final ModuleActions actions;
  final Json? item;
  final String revision;
  @override State<_ModuleEditor> createState() => _ModuleEditorState();
}

class _ModuleEditorState extends State<_ModuleEditor> {
  final undo = UndoHistoryController();
  final query = TextEditingController();
  late final String initial;
  late final TextEditingController text;
  bool saving = false, searching = false, initialized = false;
  @override void didChangeDependencies() {
    super.didChangeDependencies(); if (initialized) return; initialized = true;
    initial = widget.item?['content'] as String? ?? '#!name=${context.tr('Module')}\n#!desc=\n\n[URL Rewrite]\n\n[MITM]\nhostname = %APPEND%\n';
    text = TextEditingController(text: initial);
  }
  @override void dispose() { text.dispose(); undo.dispose(); query.dispose(); super.dispose(); }
  void _nextMatch() {
    if (query.text.isEmpty) return;
    var offset = text.text.indexOf(query.text, text.selection.isValid ? text.selection.end : 0);
    if (offset < 0) offset = text.text.indexOf(query.text);
    if (offset >= 0) text.selection = TextSelection(baseOffset: offset, extentOffset: offset + query.text.length);
  }
  Future<void> _save() async {
    setState(() => saving = true);
    final ok = await widget.actions.action('editor', () => widget.actions.save(context, {...?widget.item, 'content': text.text}, widget.revision));
    if (!mounted) return;
    if (ok) { Navigator.pop(context); } else { setState(() => saving = false); }
  }
  @override Widget build(BuildContext context) => ListenableBuilder(listenable: Listenable.merge([text, undo]), builder: (context, _) => BoxEditGuard(dirty: text.text != initial, busy: saving,
    child: Scaffold(title: widget.item?['name'] as String? ?? context.tr('New module'), isLoading: saving,
      confirmationAction: IconButtonData(glyph: AppGlyphs.check, tooltip: context.tr('Save'), onPressed: saving || text.text == initial ? null : _save),
      iconActions: [IconButtonData(glyph: AppGlyphs.search, tooltip: context.tr('Search'), onPressed: () => setState(() => searching = !searching))],
      menuItems: [PopupMenuItem(glyph: AppGlyphs.undo, label: context.tr('Undo'), onPressed: !saving && undo.value.canUndo ? undo.undo : null),
        PopupMenuItem(glyph: AppGlyphs.redo, label: context.tr('Redo'), onPressed: !saving && undo.value.canRedo ? undo.redo : null)],
      body: Surface(child: Padding(padding: EdgeInsets.only(top: context.appBarInset, bottom: context.bottomInset), child: Column(children: [
        if (searching) Padding(padding: const EdgeInsets.all(12), child: TextField(controller: query, autofocus: true, onSubmitted: (_) => _nextMatch(),
          decoration: InputDecoration(hintText: context.tr('Search'), suffixIcon: IconButton(icon: const GlyphIcon(AppGlyphs.arrowDown), onPressed: _nextMatch)))),
        Expanded(child: CodeEditor(controller: text, undoController: undo, readOnly: saving)),
      ]))))));
}

class _ModuleDetail extends StatefulWidget {
  const _ModuleDetail({required this.actions, required this.id});
  final ModuleActions actions;
  final String id;
  @override State<_ModuleDetail> createState() => _ModuleDetailState();
}

class _ModuleDetailState extends State<_ModuleDetail> {
  ModuleActions get actions => widget.actions;
  String? loaded;
  Future<Json>? details;

  Future<void> _attachment(Json item, String revision, {String? remove}) async {
    await actions.action(widget.id, () async {
      final attachments = json(item['attachments']);
      if (remove != null) {
        if (!await confirm(context, 'Tip', message: '${context.tr('Delete')} $remove?')) return;
        attachments.remove(remove);
      } else {
        final data = await actions.store.box.call('profiles.moduleFile', {'attachment': true});
        if (data == null || !mounted) return;
        final file = json(data), name = file['name'] as String;
        if (name.isEmpty || name == '.' || name == '..') throw const FormatException('Invalid attachment name');
        if (attachments.containsKey(name) && !await confirm(context, 'Replace attachment?', message: name, action: 'Replace')) return;
        attachments[name] = file['base64'];
      }
      if (mounted) await actions.save(context, {...item, 'attachments': attachments}, revision);
    });
  }

  Widget _argument(Json item, Json arg, String revision) {
    final key = arg['name'] as String, fallback = arg['default'] as String;
    final value = json(item['arguments'])[key] as String? ?? fallback;
    void save(String? next) {
      if (next != null) actions.action(widget.id, () => actions.save(context, {...item, 'arguments': {...json(item['arguments']), key: next}}, revision));
    }
    if (fallback == 'true' || fallback == 'false') return ListItem.toggle(title: Text(key), value: value == 'true', onChanged: (enabled) => save('$enabled'));
    return ListItem.input(title: Text(key), subtitle: value.isEmpty ? null : Text(value), dialogTitle: key, value: value, onChanged: save);
  }

  @override Widget build(BuildContext context) => ListenableBuilder(listenable: Listenable.merge([actions, actions.store, actions.store.box]), builder: (context, _) {
    final item = actions.find(widget.id), revision = actions.store.revision;
    if (!actions.local || item == null) return Scaffold(title: context.tr('Module'), body: NullStatus(label: context.tr('No module yet'), illustration: NullStatusIllustration.scripts));
    if (loaded != revision) { loaded = revision; details = actions.details(widget.id, revision); }
    return FutureBuilder<Json>(future: details, builder: (context, snapshot) {
      if (snapshot.hasError) return Scaffold(title: context.tr('Module'), body: ErrorState(snapshot.error!, retry: actions.store.load));
      final info = snapshot.connectionState == ConnectionState.done ? snapshot.data : null;
      final system = info?['system'] as String? ?? '';
      final about = [if ((info?['description'] as String? ?? '').isNotEmpty) info!['description'],
        if (system.isNotEmpty && !system.toLowerCase().contains('android')) context.tr('Written for {system}; some lines may not apply to this device.').replaceAll('{system}', system)].join('\n\n');
      final remote = item['url'] != null, busy = actions.working != null || actions.store.saving;
      final lines = json(info?['lines']);
      return Scaffold(title: item['name'] as String? ?? context.tr('Module'), body: NullStatusSwitcher(isLoading: info == null, child: ListView(
        padding: EdgeInsets.fromLTRB(16, context.sectionsTopPadding, 16, 16 + context.bottomInset), children: [
          generateSection(items: [ListItem.toggle(title: Text(context.moduleTr('Enabled')), value: item['enabled'] == true,
            onChanged: busy ? null : (enabled) => actions.toggle(context, item, enabled, revision))], footer: about.isEmpty ? null : about),
          generateSection(title: context.moduleTr('Source'), items: [
            ListItem(title: Text(context.moduleTr('Source')), subtitle: Text(item['url'] as String? ?? context.tr('Local'))),
            ListItem(title: Text(context.tr('Last updated')), trailing: ProfileUpdateTime(milliseconds: item['updated_at'] as int?, style: context.listCaptionStyle)),
            ListItem(title: Text(context.tr(remote ? 'Update' : 'Edit')), trailing: remote && actions.working == widget.id ? const ActivityIndicator(size: 24) : null,
              onTap: busy ? null : () => remote ? actions.update(context, item, revision) : actions.edit(context, item, revision)),
          ]),
          if (!remote) generateSection(title: context.tr('Attachments'), footer: context.tr('Refer to attachments by file name, for example script-path=a.js.'), items: [
            for (final name in json(item['attachments']).keys) ContextMenuRegion(menuItems: [PopupMenuItem(danger: true, glyph: AppGlyphs.delete,
              label: context.tr('Delete'), onPressed: () => _attachment(item, revision, remove: name))], child: ListItem(title: Text(name))),
            ListItem(title: Text(context.tr('Add local file')), onTap: busy ? null : () => _attachment(item, revision)),
          ]),
          if (rows(info?['arguments']).isNotEmpty) generateSection(title: context.tr('Arguments'),
            footer: (info?['argumentHelp'] as String? ?? '').isEmpty ? null : info!['argumentHelp'] as String,
            items: [for (final arg in rows(info?['arguments'])) _argument(item, arg, revision)]),
          if (info != null) generateSection(title: context.tr('Rules'), items: [
            for (final entry in _sections.entries) if (rows(lines[entry.key]).isNotEmpty)
              ListItem.open(title: Text(context.moduleTr(entry.value)), subtitle: Text(moduleCount(context, rows(lines[entry.key]).length)),
                widget: _ModuleLines(actions: actions, id: widget.id, revision: revision, title: entry.value, lines: rows(lines[entry.key]))),
          ], footer: context.tr('Module routing rules come before the profile’s own rules.')),
        ])));
    });
  });
}

class _ModuleLines extends StatefulWidget {
  const _ModuleLines({required this.actions, required this.id, required this.revision, required this.title, required this.lines});
  final ModuleActions actions;
  final String id, revision, title;
  final List<Json> lines;
  @override State<_ModuleLines> createState() => _ModuleLinesState();
}

class _ModuleLinesState extends State<_ModuleLines> with WidgetsBindingObserver {
  final invalidation = ValueNotifier(0);
  final requests = <String>{};
  static int sequence = 0;
  bool visible = false;
  Object? identity;
  ProfileConfigStore get store => widget.actions.store;
  bool get current => widget.actions.local && store.revision == widget.revision;
  @override void initState() { super.initState(); store.addListener(_changed); store.box.stateChanges.addListener(_changed); WidgetsBinding.instance.addObserver(this); identity = _identity(); }
  Object _identity() => (store.revision, store.box.sessionRevision, store.box.profilesRevision, store.box.selectedProfileId, store.box.dashboard['status'],
    store.box.dashboard['processingGeneration'], store.box.dashboard['processingApplying'], store.box.serviceSettingsChanging, store.saving, store.loading);
  void _invalidate() {
    invalidation.value++;
    for (final request in requests.toList()) { unawaited(store.cancelScript(request).catchError((Object _) {})); }
  }
  void _changed() {
    final next = _identity(); if (identity != next) { identity = next; _invalidate(); }
    if (mounted) setState(() {});
  }
  void _visibility() {
    final state = WidgetsBinding.instance.lifecycleState;
    final active = PageActivityScope.isActiveOf(context) && TickerMode.of(context) && (state == null || state == AppLifecycleState.resumed);
    if (active != visible) { visible = active; _invalidate(); }
  }
  @override void didChangeDependencies() { super.didChangeDependencies(); _visibility(); }
  @override void didChangeAppLifecycleState(AppLifecycleState state) { _visibility(); if (mounted) setState(() {}); }
  @override void dispose() { store.removeListener(_changed); store.box.stateChanges.removeListener(_changed); WidgetsBinding.instance.removeObserver(this); visible = false; _invalidate(); invalidation.dispose(); super.dispose(); }

  Future<void> _run(Json line, String kind, int index, int epoch) async {
    bool valid() => mounted && visible && current && invalidation.value == epoch;
    if (!valid() || !store.canRunScript(kind, index, moduleRevision: widget.revision)) return;
    final request = 'module-${DateTime.now().microsecondsSinceEpoch}-${++sequence}'; requests.add(request);
    Json? output;
    try { output = await store.runScript(kind, index, requestId: request, moduleRevision: widget.revision); }
    catch (failure) { output = {'error': BoxController.describe(failure)}; }
    finally { requests.remove(request); }
    if (!mounted || !valid() || output == null) return;
    await showBoxDialog<void>(context: context, builder: (_) => ScriptOutputDialog(name: line['text'] as String, output: output!, invalidation: invalidation, isValid: valid));
  }

  @override Widget build(BuildContext context) => Scaffold(title: context.moduleTr(widget.title), body: !current ? EmptyState('Configuration changed; reload it before saving') : PrismListView(
    padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 16 + context.bottomInset), itemCount: widget.lines.length, itemBuilder: (_, position) {
      final line = widget.lines[position], kind = line['kind'] as String? ?? '', index = line['index'] as int? ?? 0, epoch = invalidation.value;
      final running = store.isScriptRunning(kind, index), canRun = visible && widget.actions.find(widget.id)?['enabled'] == true && store.canRunScript(kind, index, moduleRevision: widget.revision);
      return ItemPositionProvider(position: ItemPosition.get(position, widget.lines.length), child: ContextMenuRegion(menuItems: canRun ? [
        PopupMenuItem(glyph: AppGlyphs.playPause(0), label: context.tr('Run now'), onPressed: () => _run(line, kind, index, epoch)),
      ] : null, child: DecorationListItem(title: Text(line['text'] as String), subtitle: line['issue'] == null ? null : Text(line['issue'] as String, style: context.listCaptionStyle),
        trailing: running ? const ActivityIndicator(size: 24) : null)));
    }));
}
