import 'dart:convert';
import '../widgets.dart';
import '../components/form.dart';
import '../components/profile_card.dart';
import 'configuration.dart';

// Node operations keep the selected profile and its revision from when the action opens.
Json _nodeTarget(BoxController box) {
  final id = box.selectedProfileId, snapshot = box.selectedProfileNodes;
  return {'profileId': id,
    if (snapshot['profileId'] == id && snapshot['profileRevision'] is String)
      'profileRevision': snapshot['profileRevision'],
  };
}

Future<Json> _resolveTarget(BoxController box, Json target) async {
  if (target['profileRevision'] is String) return target;
  final snapshot = json(await box.call('nodes.list', {'profileId': target['profileId']}));
  return {'profileId': target['profileId'], 'profileRevision': snapshot['profileRevision']};
}

String nodeSummary(Json node) {
  final server = node['server']?.toString() ?? '';
  final port = node['server_port'] ?? node['port'];
  return [node['type'].toString(), if (server.isNotEmpty) port is num && port > 0 ? '$server:$port' : server].join(' · ');
}

String pickedCount(BuildContext context, int count) => context.tr('{count} selected').replaceFirst('{count}', '$count');

/// Deletes nodes from the selected profile after confirmation.
Future<bool> removeNodes(BuildContext context, List<String> tags) async {
  final box = context.box;
  if (tags.isEmpty || box.remote) return false;
  final target = _nodeTarget(box);
  final confirmed = await confirm(context, tags.length == 1 ? 'Delete this node?' : 'Delete the selected nodes?',
    message: tags.length == 1 ? 'This cannot be undone.' : pickedCount(context, tags.length));
  if (!confirmed) return false;
  try {
    return await box.act('nodes.remove', {'tags': tags, ...await _resolveTarget(box, target)});
  } catch (error) { box.report(error); return false; }
}

/// Offers the node links libbox read from the clipboard, and stores them when accepted.
Future<void> offerClipboardNodes(BuildContext context, String parsed) async {
  final box = context.box;
  final nodes = rows(json(jsonDecode(parsed))['nodes']);
  if (nodes.isEmpty) return;
  final names = nodes.take(3).map((node) => node['tag'].toString()).join(', ');
  final imported = context.tr('Nodes imported'), target = _nodeTarget(box);
  final accepted = await confirm(context, 'Import nodes from the clipboard?',
    message: nodes.length > 3 ? '$names… (${nodes.length})' : names, action: 'Import');
  if (!accepted) return;
  try {
    final result = json(await box.call('nodes.add', {'nodes': parsed, ...await _resolveTarget(box, target)}));
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
  Json? _target;
  String? _profileName;
  @override void didChangeDependencies() {
    super.didChangeDependencies();
    _target ??= _nodeTarget(context.box);
    final profile = context.box.selectedProfile;
    if (profile != null) _profileName ??= profileDisplayName(context, profile);
  }

  Future<void> _save() async {
    if (_saving) return;
    final box = context.box;
    final imported = context.tr('Nodes imported');
    setState(() => _saving = true);
    try {
      final result = json(await box.call('nodes.add', {'nodes': widget.parsed, ...await _resolveTarget(box, _target!)}));
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
        if (_profileName != null) '${context.tr('Profile')}: $_profileName',
        context.tr('Imported nodes are appended to this profile.'),
      ].join('\n')),
      if (skipped.isNotEmpty) generateSection(title: '${context.tr('Skipped')} (${skipped.length})', items: [
        for (final entry in skipped.take(_shown)) ListItem(title: Text(entry['name'].toString()), subtitle: Text(entry['reason'].toString())),
      ]),
    ]);
  }
}

/// Edits one node as the JSON sing-box reads, where the node is defined.
class NodeEditorPage extends StatefulWidget {
  const NodeEditorPage({super.key, required this.tag});
  /// Empty for a new node in the selected profile.
  final String tag;
  @override State<NodeEditorPage> createState() => _NodeEditorPageState();
}

class _NodeEditorPageState extends State<NodeEditorPage> {
  static const _template = '{"type": "vless", "tag": "", "server": "", "server_port": 443, "uuid": ""}';
  Future<Json>? _node;
  Json? _target;
  @override void didChangeDependencies() {
    super.didChangeDependencies();
    _target ??= _nodeTarget(context.box);
    _node ??= _load();
  }
  Future<Json> _load() async {
    final box = context.box;
    if (widget.tag.isEmpty) return {'content': _template, 'revision': '', ...await _resolveTarget(box, _target!)};
    return json(await box.call('nodes.get', {'tag': widget.tag, 'profileId': _target!['profileId']}));
  }

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
      return ConfigValueEditor(title: title, initial: decodeConfigJson(node['content'] as String), onSave: (value) async {
        await box.call('nodes.set', {'tag': widget.tag, 'content': jsonEncode(value), 'revision': node['revision'],
          'profileId': node['profileId'], 'profileRevision': node['profileRevision']});
        return true;
      });
    });
  }
}
