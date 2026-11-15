import 'dart:convert';
import 'package:flutter/services.dart';
import '../widgets.dart';
import '../components/form.dart';
import 'configuration.dart';

// Shared nodes are kept outside the profiles. A profile takes them through an empty member of a
// group or by naming one, so they stay available whichever profile is selected.

String nodeSummary(Json node) {
  final server = node['server']?.toString() ?? '';
  final port = node['server_port'] ?? node['port'];
  return [node['type'].toString(), if (server.isNotEmpty) port is num && port > 0 ? '$server:$port' : server].join(' · ');
}

String pickedCount(BuildContext context, int count) => context.tr('{count} selected').replaceFirst('{count}', '$count');

/// Deletes nodes where each is defined, after asking. True when they were removed.
Future<bool> removeNodes(BuildContext context, List<String> tags) async {
  final box = context.box;
  if (tags.isEmpty || box.remote) return false;
  final confirmed = await confirm(context, tags.length == 1 ? 'Delete this node?' : 'Delete the selected nodes?',
    message: tags.length == 1 ? 'This cannot be undone.' : pickedCount(context, tags.length));
  return confirmed && await box.act('nodes.remove', {'tags': tags});
}

/// Offers the node links libbox read from the clipboard, and stores them when accepted.
Future<void> offerClipboardNodes(BuildContext context, String parsed) async {
  final box = context.box;
  final nodes = rows(json(jsonDecode(parsed))['nodes']);
  if (nodes.isEmpty) return;
  final names = nodes.take(3).map((node) => node['tag'].toString()).join(', ');
  final imported = context.tr('Nodes imported'), profileName = context.tr('Default');
  final accepted = await confirm(context, 'Import nodes from the clipboard?',
    message: nodes.length > 3 ? '$names… (${nodes.length})' : names, action: 'Import');
  if (!accepted) return;
  try {
    final result = json(await box.call('nodes.add', {'nodes': parsed, 'profileName': profileName}));
    box.messages.add('$imported: ${result['added']}');
  } catch (error) { box.report(error); }
}

/// Shows what an import read from links, a subscription text or a file before it is stored.
class NodeImportPage extends StatefulWidget {
  const NodeImportPage({super.key, required this.parsed});
  /// The result of libbox as text, which keeps the key order of every node.
  final String parsed;
  @override State<NodeImportPage> createState() => _NodeImportPageState();
}

class _NodeImportPageState extends State<NodeImportPage> {
  static const _shown = 30;
  late final Json _result = json(jsonDecode(widget.parsed));
  bool _saving = false;

  Future<void> _save() async {
    if (_saving) return;
    final box = context.box;
    final imported = context.tr('Nodes imported'), profileName = context.tr('Default');
    setState(() => _saving = true);
    try {
      final result = json(await box.call('nodes.add', {'nodes': widget.parsed, 'profileName': profileName}));
      box.messages.add('$imported: ${result['added']}');
      if (mounted) context.safeNestedPop();
    } catch (error) { box.report(error); }
    if (mounted) setState(() => _saving = false);
  }

  @override Widget build(BuildContext context) {
    final nodes = rows(_result['nodes']), skipped = rows(_result['skipped']);
    return FormPage(title: 'Import nodes', saving: _saving, onSave: _save, children: [
      generateSection(title: '${context.tr('Nodes')} (${nodes.length})', items: [
        for (final node in nodes.take(_shown)) ListItem(title: Text(node['tag'].toString()), subtitle: Text(nodeSummary(node))),
      ], footer: [
        if (nodes.length > _shown) '+${nodes.length - _shown}',
        if (_result['hint'] == 'providers') context.tr('Nodes served by proxy providers are not included.'),
        context.tr('Imported nodes are shared by every profile.'),
      ].join('\n')),
      if (skipped.isNotEmpty) generateSection(title: '${context.tr('Skipped')} (${skipped.length})', items: [
        for (final entry in skipped.take(_shown)) ListItem(title: Text(entry['name'].toString()), subtitle: Text(entry['reason'].toString())),
      ]),
    ]);
  }
}

/// Adds a subscription that serves nodes in any form libbox reads, or edits a stored one.
class NodeSubscriptionForm extends StatefulWidget {
  const NodeSubscriptionForm({super.key, this.initial = const {}});
  /// The subscription as the node state lists it; empty for a new one.
  final Json initial;
  @override State<NodeSubscriptionForm> createState() => _NodeSubscriptionFormState();
}

class _NodeSubscriptionFormState extends State<NodeSubscriptionForm> {
  late final _name = TextEditingController(text: widget.initial['name'] as String? ?? '');
  late final _url = TextEditingController(text: widget.initial['url'] as String? ?? '');
  late final _interval = TextEditingController(text: (widget.initial['interval'] ?? 60).toString());
  late bool _auto = widget.initial['autoUpdate'] != false;
  bool _saving = false, _attempted = false;

  ({String field, String message})? get _issue {
    final uri = Uri.tryParse(_url.text.trim());
    if (uri == null || !['http', 'https'].contains(uri.scheme) || uri.host.isEmpty) {
      return (field: 'url', message: context.tr('Enter an HTTP or HTTPS subscription URL'));
    }
    if (_auto && (int.tryParse(_interval.text) ?? 0) < 15) {
      return (field: 'interval', message: context.tr('At least 15 minutes'));
    }
    return null;
  }
  void _changed(String _) { if (_attempted) setState(() {}); }

  @override void dispose() { _name.dispose(); _url.dispose(); _interval.dispose(); super.dispose(); }

  Future<void> _save() async {
    if (_saving) return;
    if (_issue != null) { setState(() => _attempted = true); return; }
    final box = context.box;
    final id = widget.initial['id'];
    final imported = context.tr('Nodes imported'), profileName = context.tr('Default');
    final interval = int.tryParse(_interval.text);
    final Json args = {'name': _name.text.trim(), 'url': _url.text.trim(), 'autoUpdate': _auto,
      'interval': interval != null && interval >= 15 ? interval : 60};
    setState(() => _saving = true);
    try {
      if (id == null) {
        final result = json(await box.call('nodes.subscribe', {...args, 'profileName': profileName}));
        box.messages.add('$imported: ${result['added']}');
      } else {
        await box.call('nodes.saveSource', {...args, 'id': id});
      }
      if (mounted) context.safeNestedPop();
    } catch (error) { box.report(error); }
    if (mounted) setState(() => _saving = false);
  }

  @override Widget build(BuildContext context) {
    final issue = _attempted ? _issue : null;
    return FormPage(
      title: widget.initial['id'] == null ? 'Node subscription' : 'Edit',
      saving: _saving, onSave: _save, error: issue?.message,
      children: [
        generateSection(items: [
          FormRow(title: context.tr('Name'), trailing: FormRowTextField(controller: _name,
            hintText: context.tr('Optional'), textInputAction: TextInputAction.next, onChanged: _changed)),
          FormRow(title: context.tr('URL'), invalid: issue?.field == 'url',
            trailing: FormRowTextField(controller: _url, hintText: context.tr('Required'),
              keyboardType: TextInputType.url, textInputAction: TextInputAction.next,
              autocorrect: false, enableSuggestions: false, onChanged: _changed)),
        ], footer: context.tr('Share links, Base64, Clash, SIP008 and sing-box subscriptions are read for their nodes only.')),
        generateSection(items: [
          FormRow(title: context.tr('Auto update'), onPressed: () => setState(() => _auto = !_auto),
            trailing: Switch(value: _auto, onChanged: (value) => setState(() => _auto = value))),
          if (_auto) FormRow(title: context.tr('Update interval'), invalid: issue?.field == 'interval',
            trailing: Row(mainAxisSize: MainAxisSize.min, spacing: 4, children: [
              Flexible(child: FormRowTextField(controller: _interval, hintText: context.tr('Required'),
                keyboardType: TextInputType.number, textInputAction: TextInputAction.done,
                inputFormatters: [FilteringTextInputFormatter.digitsOnly], onChanged: _changed)),
              Text(context.tr('minutes'), style: AppTextStyle.subheadline.of(context)),
            ])),
        ], footer: _auto ? context.tr('At least 15 minutes') : null),
      ],
    );
  }
}

/// Edits one node as the JSON sing-box reads, where the node is defined.
class NodeEditorPage extends StatefulWidget {
  const NodeEditorPage({super.key, required this.tag});
  /// Empty for a shared node that does not exist yet.
  final String tag;
  @override State<NodeEditorPage> createState() => _NodeEditorPageState();
}

class _NodeEditorPageState extends State<NodeEditorPage> {
  static const _template = '{"type": "vless", "tag": "", "server": "", "server_port": 443, "uuid": ""}';
  Future<Json>? _node;
  @override void didChangeDependencies() { super.didChangeDependencies(); _node ??= _load(); }
  Future<Json> _load() async => widget.tag.isEmpty ? {'content': _template}
      : json(await context.box.call('nodes.get', {'tag': widget.tag}));

  @override Widget build(BuildContext context) {
    final title = widget.tag.isEmpty ? 'New node' : widget.tag;
    return FutureBuilder<Json>(future: _node, builder: (context, snapshot) {
      final node = snapshot.data;
      if (node == null) {
        return BoxPage(title: title, loading: !snapshot.hasError, children: [
          if (snapshot.hasError) ErrorState(snapshot.error!, retry: () => setState(() { _node = _load(); })),
        ]);
      }
      final box = context.box;
      return ConfigValueEditor(title: title, initial: jsonDecode(node['content'] as String), onSave: (value) async {
        await box.call('nodes.set', {'tag': widget.tag, 'content': jsonEncode(value), 'revision': node['revision']});
        return true;
      });
    });
  }
}

/// The shared nodes and the subscriptions that serve them.
class NodesPage extends StatefulWidget {
  const NodesPage({super.key});
  @override State<NodesPage> createState() => _NodesPageState();
}

class _NodesPageState extends State<NodesPage> {
  final _updating = <String>{};
  Set<String>? _picked;
  String _query = '';

  Future<void> _update(Json source) async {
    final id = source['id'].toString();
    if (_updating.contains(id)) return;
    final box = context.box;
    final imported = context.tr('Nodes imported');
    setState(() => _updating.add(id));
    try {
      final result = json(await box.call('nodes.updateSource', {'id': id}));
      box.messages.add('$imported: ${result['added']}');
    } catch (error) { box.report(error); }
    if (mounted) setState(() => _updating.remove(id));
  }

  Future<void> _updateAll(List<Json> sources) async {
    // Node writes share a lock; keep the updates one after another.
    for (final source in sources) {
      if (!mounted || context.box.remote) break;
      await _update(source);
    }
  }

  Future<void> _removeSource(Json source) async {
    final box = context.box;
    if (!await confirm(context, 'Delete this subscription?', message: 'Its nodes are deleted with it.')) return;
    await box.act('nodes.removeSource', {'id': source['id']});
  }

  Future<void> _remove(List<String> tags) async {
    if (await removeNodes(context, tags) && mounted) setState(() => _picked = null);
  }

  void _pick(String tag) => setState(() {
    final picked = _picked ??= {};
    if (!picked.remove(tag)) picked.add(tag);
  });

  Future<void> _paste() async {
    final box = context.box;
    final empty = context.tr('Empty clipboard');
    try {
      final text = (await Clipboard.getData(Clipboard.kTextPlain))?.text?.trim();
      if (text == null || text.isEmpty) throw StateError(empty);
      final parsed = await box.call('nodes.parse', {'content': text});
      if (parsed is String && mounted) await openForm(context, NodeImportPage(parsed: parsed));
    } catch (error) { box.report(error); }
  }

  Widget _source(Json source) {
    final id = source['id'].toString();
    final updated = (source['updatedAt'] as num? ?? 0) <= 0 ? context.tr('Unknown') : localDate(source['updatedAt']);
    return Padding(padding: const EdgeInsets.only(bottom: 8), child: Card(
      onPressed: context.box.remote ? null : () => openForm(context, NodeSubscriptionForm(initial: source)),
      child: ListItem(
        leading: _updating.contains(id) ? const ActivityIndicator(size: 20) : const GlyphIcon(AppGlyphs.cloudSync),
        title: Text(source['name'].toString()),
        subtitle: Text('${context.tr('Nodes')}: ${source['count']} · ${context.tr('Updated')} $updated'),
        trailing: context.box.remote ? null : BoxMenu(items: [
          PopupMenuItem(glyph: AppGlyphs.sync, label: context.tr('Update'), onPressed: () => _update(source)),
          PopupMenuItem(glyph: AppGlyphs.edit, label: context.tr('Edit'),
            onPressed: () => openForm(context, NodeSubscriptionForm(initial: source))),
          PopupMenuItem(danger: true, glyph: AppGlyphs.delete, label: context.tr('Delete'), onPressed: () => _removeSource(source)),
        ]),
      ),
    ));
  }

  Widget _node(Json node, String? source) {
    final tag = node['tag'].toString();
    final picked = _picked;
    final items = context.box.remote ? const <PopupMenuItem>[] : [
      PopupMenuItem(glyph: AppGlyphs.edit, label: context.tr('Edit'), onPressed: () => openForm(context, NodeEditorPage(tag: tag))),
      PopupMenuItem(glyph: AppGlyphs.checkDouble, label: context.tr('Select multiple'), onPressed: () => _pick(tag)),
      PopupMenuItem(danger: true, glyph: AppGlyphs.delete, label: context.tr('Delete'), onPressed: () => _remove([tag])),
    ];
    return Padding(padding: const EdgeInsets.only(bottom: 8), child: ContextMenuRegion(
      menuItems: picked == null ? items : null,
      child: Card(
        isSelected: picked != null && picked.contains(tag),
        onPressed: picked != null ? () => _pick(tag) : items.isEmpty ? null : () => openForm(context, NodeEditorPage(tag: tag)),
        child: ListItem(
          title: Text(tag), subtitle: Text([nodeSummary(node), ?source].join(' · ')),
          trailing: picked != null ? (picked.contains(tag) ? const SelectionBadge() : null)
              : items.isEmpty ? null : BoxMenu(items: items),
        ),
      ),
    ));
  }

  @override Widget build(BuildContext context) {
    final box = context.watchBox(BoxAspect.state);
    final sources = rows(box.nodes['sources']), all = rows(box.nodes['nodes']);
    final names = {for (final source in sources) source['id'].toString(): source['name'].toString()};
    final query = _query.trim().toLowerCase();
    final nodes = [for (final node in all)
      if (query.isEmpty || '${node['tag']} ${node['server']}'.toLowerCase().contains(query)) node];
    final picked = _picked;
    picked?.retainAll(all.map((node) => node['tag'].toString()).toSet());
    final shown = nodes.map((node) => node['tag'].toString()).toList();
    final allPicked = picked != null && shown.isNotEmpty && picked.containsAll(shown);
    final head = sources.isEmpty ? 0 : sources.length + 1;
    final count = head + (nodes.isEmpty ? 0 : nodes.length + 1);
    final offers = box.preferences['clipboardImport'] != false;
    return PopGuard(
      onPop: picked == null ? null : (_) { setState(() => _picked = null); return false; },
      child: Scaffold(
        title: picked == null ? context.tr('Shared nodes') : pickedCount(context, picked.length),
        backAction: picked == null ? null : () => setState(() => _picked = null),
        searchState: AppBarSearchState(onSearch: (query) => setState(() => _query = query)),
        selectionActions: picked == null ? const [] : [
          IconButtonData(glyph: allPicked ? AppGlyphs.deselect : AppGlyphs.selectAll,
            tooltip: context.tr(allPicked ? 'Deselect all' : 'Select all'),
            onPressed: () => setState(() { if (allPicked) { picked.clear(); } else { picked.addAll(shown); } })),
          IconButtonData(glyph: AppGlyphs.delete, tooltip: context.tr('Delete'),
            onPressed: picked.isEmpty ? null : () => _remove(picked.toList())),
        ],
        actions: picked != null || box.remote ? null : [
          PopupAnchor(
            targetBuilder: (open) => AppBarActionButton(data: IconButtonData(
              glyph: AppGlyphs.addCircle, tooltip: context.tr('Add nodes'), onPressed: open)),
            popupBuilder: (_) => PopupMenu(items: [
              PopupMenuItem(glyph: AppGlyphs.paste, label: context.tr('Clipboard'), onPressed: _paste),
              PopupMenuItem(glyph: AppGlyphs.cloudSync, label: context.tr('Node subscription'),
                onPressed: () => openForm(context, const NodeSubscriptionForm())),
              PopupMenuItem(glyph: AppGlyphs.code, label: context.tr('New node'),
                onPressed: () => openForm(context, const NodeEditorPage(tag: ''))),
            ]),
          ),
        ],
        menuItems: picked != null ? const [] : [
          if (!box.remote) PopupMenuItem(glyph: AppGlyphs.sync, label: context.tr('Update all'),
            onPressed: sources.isEmpty || _updating.isNotEmpty ? null : () => _updateAll(sources)),
          PopupMenuItem(glyph: AppGlyphs.paste, label: context.tr('Offer copied node links'), checked: offers,
            onPressed: () => box.setPreference('clipboardImport', !offers)),
        ],
        body: count == 0 ? (all.isEmpty && sources.isEmpty
            ? NullStatus(label: context.tr('No shared nodes'), illustration: NullStatusIllustration.proxies,
                description: context.tr('Import share links, a subscription or a file. Every profile can use these nodes.'))
            : NullStatus(label: context.tr('No results'), illustration: NullStatusIllustration.proxies))
          : ListView.builder(
              padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 24 + context.bottomInset),
              itemCount: count,
              itemBuilder: (context, index) {
                if (index < head) {
                  return index == 0 ? ListHeader(title: context.tr('Subscriptions')) : _source(sources[index - 1]);
                }
                final offset = index - head;
                if (offset == 0) return ListHeader(title: '${context.tr('Nodes')} (${nodes.length})');
                final node = nodes[offset - 1];
                return _node(node, names[node['source']]);
              },
            ),
      ),
    );
  }
}
