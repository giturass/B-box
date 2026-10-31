import 'dart:convert';
import 'package:flutter/services.dart';
import '../widgets.dart';
import '../components/form.dart';
import '../components/code_editor.dart';

const captureRuleKinds = <String, String>{
  'headerRewrite': 'Header Rewrite', 'urlRewrite': 'URL Rewrite',
  'bodyRewrite': 'Body Rewrite', 'mapLocal': 'Map Local', 'scripts': 'JavaScript',
};

class CaptureRuleListPage extends StatefulWidget {
  const CaptureRuleListPage({super.key, required this.kind, required this.initial});
  final String kind;
  final List<Json> initial;
  @override State<CaptureRuleListPage> createState() => _CaptureRuleListPageState();
}

class _CaptureRuleListPageState extends State<CaptureRuleListPage> {
  late final _rules = widget.initial.map((rule) => Json.of(rule)).toList();
  bool _dirty = false;

  Future<void> _edit([int? index]) async {
    final result = await openPage<Json>(context, CaptureRuleEditorPage(kind: widget.kind, initial: index == null ? null : _rules[index]));
    if (result == null || !mounted) return;
    setState(() {
      if (result['_delete'] == true) { if (index != null) _rules.removeAt(index); }
      else if (index == null) { _rules.add(result); }
      else { _rules[index] = result; }
      _dirty = true;
    });
  }

  @override Widget build(BuildContext context) {
    if (context.box.remote) return const BoxPage(title: 'HTTP rewrites', children: [EmptyState('HTTP debugging is only available for the local service')]);
    return BoxEditGuard(dirty: _dirty, child: CommonScaffold(title: context.tr(captureRuleKinds[widget.kind]!),
      confirmationAction: IconButtonData(glyph: AppGlyphs.check, tooltip: context.tr('Save'), onPressed: () {
        setState(() => _dirty = false);
        Navigator.pop(context, _rules);
      }),
      iconActions: [IconButtonData(glyph: AppGlyphs.add, tooltip: context.tr('Add rule'), onPressed: () => _edit())],
      body: ReorderableListView.builder(
        buildDefaultDragHandles: false,
        padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 24 + context.bottomInset),
        header: ListFooter(text: context.tr('Rules run in list order. Drag to reorder; tap to edit. Save capture settings to apply your changes.')),
        footer: _rules.isEmpty ? const EmptyState('No rewrite rules') : null,
        itemCount: _rules.length,
        onReorderItem: (old, next) => setState(() { _rules.insert(next, _rules.removeAt(old)); _dirty = true; }),
        itemBuilder: (_, index) {
          final rule = _rules[index];
          final name = (rule['name'] as String? ?? '').trim();
          return ReorderableDelayedDragStartListener(key: ObjectKey(rule), index: index,
            child: ItemPositionProvider(position: ItemPosition.get(index, _rules.length), child: DecorationListItem(
              title: Text(name.isEmpty ? rule['pattern'].toString() : name, maxLines: 2, overflow: TextOverflow.ellipsis),
              subtitle: Text('${context.tr(rule['disabled'] == true ? 'Disabled' : 'Enabled')} · ${rule['pattern']}', maxLines: 2, overflow: TextOverflow.ellipsis),
              trailing: SortHandle(index: index), onPressed: () => _edit(index),
            )));
        },
      ),
    ));
  }
}

class CaptureRuleEditorPage extends StatefulWidget {
  const CaptureRuleEditorPage({super.key, required this.kind, this.initial});
  final String kind;
  final Json? initial;
  @override State<CaptureRuleEditorPage> createState() => _CaptureRuleEditorPageState();
}

class _CaptureRuleEditorPageState extends State<CaptureRuleEditorPage> {
  late final _value = <String, dynamic>{..._defaults(), ...?widget.initial};
  final _fields = <String, TextEditingController>{};
  final _undo = UndoHistoryController();
  bool _dirty = false, _busy = false;
  String? _error;

  Json _defaults() => switch (widget.kind) {
    'headerRewrite' => {'type': 'http-request', 'action': 'header-add', 'header': 'X-Debug', 'value': 'B-box'},
    'urlRewrite' => {'type': 'header', 'replacement': r'https://example.com/new/$1'},
    'bodyRewrite' => {'type': 'http-response', 'regex': 'old', 'replacement': 'new'},
    'mapLocal' => {'dataType': 'text', 'data': '{}', 'status': 200, 'headers': [{'name': 'Content-Type', 'value': 'application/json'}]},
    _ => {'type': 'http-request', 'source': r'$done({headers: {...$request.headers, "X-Debug": "B-box"}});', 'timeoutMs': 1000, 'maxSize': 1048576},
  };

  TextEditingController _controller(String key) => _fields.putIfAbsent(key, () {
    final controller = TextEditingController(text: key == 'headers'
      ? const JsonEncoder.withIndent('  ').convert(_value[key] ?? []) : _value[key]?.toString() ?? '');
    if (key == 'source') controller.addListener(_sourceChanged);
    return controller;
  });

  Widget _field(String key, String label, {String? hint, int lines = 1, int limit = 131072}) => generateSection(title: context.tr(label), items: [
    DecorationListItem(title: TextField(key: ValueKey('rule-$key'), controller: _controller(key),
      minLines: lines, maxLines: lines == 1 ? 3 : lines + 4, autocorrect: false, enableSuggestions: false,
      keyboardType: lines > 1 ? TextInputType.multiline : TextInputType.text,
      inputFormatters: [LengthLimitingTextInputFormatter(limit)],
      style: AppTextStyle.body.of(context), decoration: InputDecoration.collapsed(hintText: hint, border: const NoInputBorder()),
      onChanged: (_) => setState(() => _dirty = true),
    )),
  ]);

  Widget _option(String key, String label, List<String> options) => ListItem.options(
    title: Text(context.tr(label)), options: options, value: _value[key]?.toString() ?? options.first,
    textBuilder: context.tr, onChanged: (value) => setState(() { _value[key] = value; _dirty = true; }),
  );

  Widget _toggle(String key, String label) => ListItem.toggle(title: Text(context.tr(label)), value: _value[key] == true,
    onChanged: (value) => setState(() { _value[key] = value; _dirty = true; }));

  Future<void> _import() async {
    setState(() { _busy = true; _error = null; });
    try {
      final value = await context.box.call('capture.import', {'kind': widget.kind == 'scripts' ? 'script' : 'mapLocal'});
      if (value != null && mounted) {
        final imported = json(value);
        setState(() {
          for (final key in widget.kind == 'scripts' ? ['source'] : ['dataType', 'data', 'headers']) {
            _value[key] = imported[key];
            if (_fields.containsKey(key)) _fields[key]!.text = key == 'headers' ? const JsonEncoder.withIndent('  ').convert(imported[key]) : imported[key].toString();
          }
          _dirty = true;
        });
      }
    } catch (error) { if (mounted) setState(() => _error = BoxController.describe(error)); }
    finally { if (mounted) setState(() => _busy = false); }
  }

  Future<void> _save() async {
    if (_busy || context.box.remote) return;
    setState(() { _busy = true; _error = null; });
    try {
      final value = Json.of(_value);
      for (final field in _fields.entries) {
        value[field.key] = switch (field.key) {
          'headers' => jsonDecode(field.value.text),
          'status' || 'timeoutMs' || 'maxSize' => int.parse(field.value.text),
          _ => field.value.text,
        };
      }
      // Validate through the same engine used by full JSON profile saves.
      await context.box.call('capture.validate', {'config': {widget.kind: [value]}});
      if (mounted) { setState(() { _dirty = false; _busy = false; }); Navigator.pop(context, value); }
    } catch (error) { if (mounted) setState(() { _error = BoxController.describe(error); _busy = false; }); }
  }

  @override void dispose() { for (final field in _fields.values) { field.dispose(); } _undo.dispose(); super.dispose(); }

  @override Widget build(BuildContext context) {
    if (context.box.remote) return const BoxPage(title: 'HTTP rewrites', children: [EmptyState('HTTP debugging is only available for the local service')]);
    final kind = widget.kind;
    return BoxEditGuard(dirty: _dirty, busy: _busy, child: FormPage(title: captureRuleKinds[kind]!, saving: _busy, error: _error, onSave: _save,
      actions: [
        if (kind == 'scripts' || kind == 'mapLocal') IconButtonData(glyph: AppGlyphs.importFile, tooltip: context.tr('Import file'), onPressed: _busy ? null : _import),
        if (widget.initial != null) IconButtonData(glyph: AppGlyphs.delete, tooltip: context.tr('Delete rule'), onPressed: _busy ? null : () async {
          if (await confirm(context, 'Delete rule?') && context.mounted) { setState(() => _dirty = false); Navigator.pop(context, <String, dynamic>{'_delete': true}); }
        }),
      ], children: [
        generateSection(items: [_toggle('disabled', 'Disable rule')]),
        _field('name', 'Name', limit: 128),
        _field('pattern', 'URL pattern', hint: r'^https?://example\.com/', limit: 8192),
        if (kind != 'mapLocal') generateSection(items: [
          if (kind != 'urlRewrite') _option('type', 'Direction', const ['http-request', 'http-response'])
          else _option('type', 'Action', const ['header', '301', '302', '307', '308', 'reject']),
          if (kind == 'headerRewrite') _option('action', 'Action', const ['header-add', 'header-del', 'header-replace', 'header-replace-regex']),
        ]),
        if (kind == 'headerRewrite') ...[
          _field('header', 'Header name', hint: 'User-Agent', limit: 256),
          if (_value['action'] != 'header-del') _field('value', _value['action'] == 'header-replace-regex' ? 'Regular expression' : 'Value', limit: 8192),
          if (_value['action'] == 'header-replace-regex') _field('replacement', 'Replacement', limit: 8192),
        ],
        if (kind == 'urlRewrite' && _value['type'] != 'reject') _field('replacement', 'Replacement', hint: r'https://example.com/new/$1', limit: 8192),
        if (kind == 'bodyRewrite') ...[
          _field('regex', 'Regular expression', limit: 8192), _field('replacement', 'Replacement', lines: 3),
        ],
        if (kind == 'mapLocal') ...[
          generateSection(items: [_option('dataType', 'Local response type', const ['text', 'base64', 'tiny-gif'])]),
          _field('status', 'Status', limit: 3),
          if (_value['dataType'] != 'tiny-gif') _field('data', 'Local response body', lines: 4, limit: 1398104),
          _field('headers', 'Local response headers (JSON)', lines: 4),
          ListFooter(text: context.tr('Import a local file to embed its contents in this profile. Map Local does not contact the origin server.')),
        ],
        if (kind == 'scripts') ...[
          generateSection(items: [
            _toggle('requiresBody', 'Read and rewrite body'), _toggle('binaryBodyMode', 'Binary body mode'), _toggle('fullHeaderMode', 'Preserve duplicate headers'),
          ]),
          _field('timeoutMs', 'Script timeout (ms)', limit: 4), _field('maxSize', 'Script body limit (bytes)', limit: 7),
          _field('argument', 'Script argument', lines: 2, limit: 8192),
          Padding(padding: const EdgeInsets.symmetric(vertical: 12), child: Text(context.tr('JavaScript source'))),
          SizedBox(height: 320, child: CodeEditor(controller: _controller('source'), undoController: _undo)),
          ListFooter(text: context.tr(r'Use $request, $response, $argument and $done(). Scripts run locally with a timeout; network, filesystem and timer APIs are unavailable.')),
        ],
        ListFooter(text: context.tr(r'Patterns use Go regular expressions. Use $1 or ${name} in replacements. HTTPS requires MITM for the original domain.')),
      ],
    ));
  }

  void _sourceChanged() { if (!_dirty) setState(() => _dirty = true); }
}
