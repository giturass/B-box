import 'dart:convert';
import 'package:flutter/services.dart';
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

class NodeSharePage extends StatefulWidget {
  const NodeSharePage({super.key, required this.tags, required this.profileId,
    required this.profileRevision, required this.session, required this.showQr});

  final List<String> tags;
  final int profileId, session;
  final String profileRevision;
  final bool showQr;

  @override State<NodeSharePage> createState() => _NodeSharePageState();
}

class _NodeSharePageState extends State<NodeSharePage> {
  Future<Json>? _request;
  bool? _standard;
  String? _qrContent;
  bool _qrFits = false;

  @override void didChangeDependencies() {
    super.didChangeDependencies();
    _request ??= _load();
  }

  Future<Json> _load() async {
    final result = await context.box.call('nodes.share', {
      'tags': widget.tags,
      'profileId': widget.profileId,
      'profileRevision': widget.profileRevision,
    }) as String;
    return json(jsonDecode(result));
  }

  bool _current(BoxController box) => !box.remote && box.sessionRevision == widget.session &&
      box.selectedProfileId == widget.profileId &&
      box.selectedProfileNodes['profileId'] == widget.profileId &&
      box.selectedProfileNodes['profileRevision'] == widget.profileRevision;

  Future<void> _copy(String content) async {
    final box = context.box;
    if (!_current(box)) return;
    try {
      await Clipboard.setData(ClipboardData(text: content));
      if (mounted) BannerHost.maybeOf(context)?.show(context.tr('Copied'));
    } catch (error) { box.report(error); }
  }

  Widget _qr(String content) {
    if (_qrContent != content) {
      _qrContent = content;
      _qrFits = QrCodeView.fits(content);
    }
    return Padding(
      padding: const EdgeInsets.only(top: 16),
      child: Center(child: _qrFits
        ? QrCodeView(data: content)
        : Text(context.tr('Too long for a QR code. Copy or share the text.'), textAlign: TextAlign.center)),
    );
  }

  @override Widget build(BuildContext context) {
    final box = context.box;
    const title = 'Share nodes';
    if (!_current(box)) {
      return const BoxPage(title: title, children: [
        EmptyState('Configuration changed. Reopen node sharing.'),
      ]);
    }
    return FutureBuilder<Json>(future: _request, builder: (context, snapshot) {
      final result = snapshot.data;
      if (result == null) {
        return BoxPage(title: title, loading: !snapshot.hasError, children: [
          if (snapshot.hasError) ErrorState(snapshot.error!, retry: () => setState(() => _request = _load())),
        ]);
      }
      final entries = rows(result['nodes']);
      final links = entries.map((node) => node['link']).whereType<String>().toList();
      final standard = _standard ?? (links.length == entries.length);
      final content = standard ? links.join('\n') : result['json'] as String;
      return BoxPage(
        title: title,
        actions: [
          IconButtonData(glyph: AppGlyphs.copy, tooltip: context.tr(standard
              ? (links.length == 1 ? 'Copy link' : 'Copy links') : 'Copy JSON'), onPressed: () => _copy(content)),
          IconButtonData(glyph: AppGlyphs.upload, tooltip: context.tr('Share'), onPressed: () async {
            if (_current(box)) await box.act('share.text', {'content': content});
          }),
        ],
        children: [
          if (widget.showQr) ...[
            Text(widget.tags.single, textAlign: TextAlign.center, style: context.listSubtitleStyle),
            const SizedBox(height: 12),
          ],
          if (links.isNotEmpty)
            SegmentedControl<bool>(
              segments: [(true, context.tr('Standard links')), (false, context.tr('sing-box JSON'))],
              value: standard,
              onChanged: (value) => setState(() => _standard = value),
            )
          else
            Text(context.tr('These nodes are available as sing-box JSON.'), style: context.listSubtitleStyle),
          if (standard && links.length < entries.length)
            Padding(padding: const EdgeInsets.only(top: 12), child: Text(
              context.tr('Standard links available for {available} of {total} nodes.')
                .replaceFirst('{available}', '${links.length}').replaceFirst('{total}', '${entries.length}'),
              style: context.listSubtitleStyle,
            )),
          if (widget.showQr) _qr(content),
          const SizedBox(height: 16),
          SelectableText(content),
        ],
      );
    });
  }
}

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
