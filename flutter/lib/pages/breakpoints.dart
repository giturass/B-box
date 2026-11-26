import 'dart:convert';

import 'package:flutter/foundation.dart' show listEquals;
import 'package:flutter/services.dart' show TextInputAction;

import '../widgets.dart';
import '../common/byte_input.dart';
import '../common/http_message.dart';
import '../components/capture_poll.dart';
import '../components/form.dart';
import '../components/record.dart';

const _monospace = TextStyle(fontFamily: 'JetBrainsMono', fontSize: 13, height: 1.6);
const _localOnly = 'HTTP debugging is only available for the local service';

/// A larger body is forwarded unchanged; the core accepts edits up to 1 MiB.
const _editableBodyLimit = 256 * 1024;

String _phaseLabel(Json message) => message['phase'] == 'response' ? 'Response' : 'Request';

/// Breakpoint rules and the HTTP messages they currently hold.
class BreakpointsPage extends StatefulWidget {
  const BreakpointsPage({super.key});
  @override State<BreakpointsPage> createState() => _BreakpointsPageState();
}

class _BreakpointsPageState extends State<BreakpointsPage> {
  int _revision = 0;
  bool _busy = false;

  Future<void> _apply(bool enabled, List<Json> rules) async {
    final box = context.box;
    if (_busy || box.remote) return;
    setState(() => _busy = true);
    try { await box.act('capture.breakpoints.set', {'config': {'enabled': enabled, 'rules': rules}}); }
    finally { if (mounted) setState(() { _busy = false; _revision++; }); }
  }

  Future<void> _editRule(bool enabled, List<Json> rules, int? index) async {
    final box = context.box, session = box.sessionRevision;
    final result = await openForm<Json>(context, BreakpointRulePage(rule: index == null ? null : rules[index]));
    // The rules that were read belong to the session they were edited in.
    if (result == null || !mounted || box.remote || box.sessionRevision != session) return;
    final next = [...rules];
    if (result['delete'] == true) { if (index != null) next.removeAt(index); }
    else if (index == null) { next.add(result); }
    else { next[index] = result; }
    // A first rule is added to be used.
    await _apply(enabled || index == null && rules.isEmpty, next);
  }

  Future<void> _openPaused(String id) async {
    await openPage(context, PausedMessagePage(id: id));
    if (mounted) setState(() => _revision++);
  }

  String _ruleSummary(Json rule) => [
    if (rule['enabled'] != true) context.tr('Disabled'),
    (rule['method'] as String? ?? '').isEmpty ? context.tr('Any method') : rule['method'],
    if (rule['request'] == true) context.tr('Request'),
    if (rule['response'] == true) context.tr('Response'),
  ].join(' · ');

  @override Widget build(BuildContext context) {
    final box = context.watchBox(BoxAspect.connections);
    if (box.remote) return const BoxPage(title: 'Breakpoints', children: [EmptyState(_localOnly)]);
    return CapturePoll(method: 'capture.breakpoints', arguments: const <String, dynamic>{}, revision: _revision,
      builder: (context, value, error, refresh) {
        final config = json(value?['config']), rules = rows(config['rules']), paused = rows(value?['paused']);
        final enabled = config['enabled'] == true, ready = value != null && !_busy;
        return BoxPage(title: 'Breakpoints', loading: _busy, actions: [
          IconButtonData(glyph: AppGlyphs.add, tooltip: context.tr('Add rule'),
            onPressed: ready ? () => _editRule(enabled, rules, null) : null),
        ], children: [
          if (error != null) ErrorState(error, retry: refresh),
          if (value == null && error == null) const Center(child: ActivityIndicator()),
          if (value != null) ...[
            generateSection(items: [
              ListItem.toggle(key: const ValueKey('breakpoints-enabled'), title: Text(context.tr('Pause matching messages')),
                subtitle: Text(context.tr(enabled ? 'Matching messages wait here for a decision' : 'Messages are forwarded without pausing')),
                value: enabled, onChanged: ready ? (value) => _apply(value, rules) : null),
            ], footer: context.tr('Only HTTP messages that are being recorded can pause, so start capturing first. '
                'Stopping the capture or turning this off forwards held messages unchanged. '
                'A held message is also dropped when its client gives up waiting.')),
            generateSection(title: context.tr('Paused messages'), items: [
              if (paused.isEmpty) ListItem(title: Text(context.tr('No paused messages'), style: context.listSubtitleStyle)),
              for (final message in paused) ListItem(key: ValueKey('paused:${message['id']}'),
                title: Text('${message['method']} ${message['url']}', maxLines: 2, overflow: TextOverflow.ellipsis),
                subtitle: Text([context.tr(_phaseLabel(message)), if (message['status'] != null) '${message['status']}',
                  localDate(message['pausedAt'])].join(' · ')),
                onTap: () => _openPaused(message['id'].toString())),
            ]),
            generateSection(title: context.tr('Breakpoint rules'), items: [
              if (rules.isEmpty) ListItem(title: Text(context.tr('No breakpoint rules'), style: context.listSubtitleStyle)),
              for (final (index, rule) in rules.indexed) ListItem(key: ValueKey('breakpoint-rule-$index'),
                title: Text((rule['url'] as String? ?? '').isEmpty ? context.tr('Any URL') : rule['url'] as String,
                  maxLines: 2, overflow: TextOverflow.ellipsis),
                subtitle: Text(_ruleSummary(rule)),
                onTap: ready ? () => _editRule(enabled, rules, index) : null),
            ], footer: context.tr('Rules are kept in memory and are cleared when the app process restarts.')),
          ],
        ]);
      });
  }
}

/// Edits one rule. Pops with the rule, or with `{'delete': true}`.
class BreakpointRulePage extends StatefulWidget {
  const BreakpointRulePage({super.key, this.rule});
  final Json? rule;
  @override State<BreakpointRulePage> createState() => _BreakpointRulePageState();
}

class _BreakpointRulePageState extends State<BreakpointRulePage> {
  static final _methodToken = RegExp(r"^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$");
  late final _url = TextEditingController(text: widget.rule?['url'] as String? ?? '');
  late final _method = TextEditingController(text: widget.rule?['method'] as String? ?? '');
  late bool _enabled = widget.rule?['enabled'] != false;
  late bool _request = widget.rule == null || widget.rule!['request'] == true;
  late bool _response = widget.rule?['response'] == true;
  bool _dirty = false;
  String? _issue;

  @override void dispose() { _url.dispose(); _method.dispose(); super.dispose(); }
  void _changed(VoidCallback update) => setState(() { update(); _dirty = true; _issue = null; });

  void _save() {
    final url = _url.text.trim(), method = _method.text.trim().toUpperCase();
    final issue = !_request && !_response ? 'Pause requests, responses or both'
      : method.isNotEmpty && !_methodToken.hasMatch(method) ? 'Enter a valid HTTP method'
      : url.length > 2048 ? 'The URL filter is too long' : null;
    if (issue != null) { setState(() => _issue = issue); return; }
    setState(() => _dirty = false);
    context.safeNestedPop<Json>({'enabled': _enabled, if (url.isNotEmpty) 'url': url, if (method.isNotEmpty) 'method': method,
      'request': _request, 'response': _response});
  }

  Widget _toggle(String key, String title, bool value, ValueChanged<bool> update) => FormRow(key: ValueKey(key),
    title: context.tr(title), onPressed: () => _changed(() => update(!value)),
    trailing: Switch(value: value, onChanged: (value) => _changed(() => update(value))));

  @override Widget build(BuildContext context) => BoxEditGuard(dirty: _dirty, child: FormPage(title: 'Breakpoint rule',
    onSave: _save, error: _issue == null ? null : context.tr(_issue!), children: [
      generateSection(footer: context.tr('A rule matches when the request URL contains the text, ignoring case. Leave a field empty to match everything.'), items: [
        FormRow(title: context.tr('URL contains'), trailing: FormRowTextField(key: const ValueKey('breakpoint-url'), controller: _url,
          hintText: context.tr('Any URL'), keyboardType: TextInputType.url, textInputAction: TextInputAction.next,
          autocorrect: false, enableSuggestions: false, onChanged: (_) => _changed(() {}))),
        FormRow(title: context.tr('Method'), invalid: _issue == 'Enter a valid HTTP method',
          trailing: FormRowTextField(key: const ValueKey('breakpoint-method'), controller: _method,
            hintText: context.tr('Any method'), textInputAction: TextInputAction.done,
            autocorrect: false, enableSuggestions: false, onChanged: (_) => _changed(() {}))),
      ]),
      generateSection(items: [
        _toggle('breakpoint-request', 'Pause requests', _request, (value) => _request = value),
        _toggle('breakpoint-response', 'Pause responses', _response, (value) => _response = value),
        _toggle('breakpoint-rule-enabled', 'Enabled', _enabled, (value) => _enabled = value),
      ]),
      if (widget.rule != null) generateSection(items: [FormRow(key: const ValueKey('breakpoint-delete'), title: context.tr('Delete'),
        titleStyle: TextStyle(color: context.colorScheme.error),
        onPressed: () { setState(() => _dirty = false); context.safeNestedPop<Json>({'delete': true}); })]),
    ]));
}

/// Shows one held message; continuing sends only what was changed.
class PausedMessagePage extends StatefulWidget {
  const PausedMessagePage({super.key, required this.id});
  final String id;
  @override State<PausedMessagePage> createState() => _PausedMessagePageState();
}

class _PausedMessagePageState extends State<PausedMessagePage> {
  final _method = TextEditingController(), _url = TextEditingController(), _status = TextEditingController();
  final _headers = TextEditingController(), _body = TextEditingController();
  Json? _message;
  Object? _loadError;
  List<int> _pausedBody = const [];
  String _pausedHeaders = '', _format = 'Text';
  bool _requested = false, _busy = false, _dirty = false;
  String? _error;
  int? _session;

  bool get _response => _message?['phase'] == 'response';
  bool get _bodyEditable => _message?['bodyEditable'] == true;
  bool get _bodyShown => _bodyEditable && _pausedBody.length <= _editableBodyLimit;

  @override void didChangeDependencies() {
    super.didChangeDependencies();
    _session ??= context.box.sessionRevision;
    // Held messages belong to the local service; a remote session is never asked for one.
    if (!_requested && !context.box.remote) { _requested = true; _load(context.box); }
  }

  @override void dispose() {
    for (final controller in [_method, _url, _status, _headers, _body]) { controller.dispose(); }
    super.dispose();
  }

  Future<void> _load(BoxController box) async {
    try {
      final message = json(await box.call('capture.breakpoint.detail', {'id': widget.id}));
      if (mounted && !box.remote && _session == box.sessionRevision) setState(() => _show(message));
    } catch (error) { if (mounted && !box.remote && _session == box.sessionRevision) setState(() => _loadError = error); }
  }

  void _show(Json message) {
    _message = message;
    _method.text = message['method']?.toString() ?? '';
    _url.text = message['url']?.toString() ?? '';
    _status.text = '${message['status'] ?? ''}';
    _headers.text = _pausedHeaders = headerLines(rows(message['headers']));
    if (!_bodyEditable) return;
    _pausedBody = base64.decode(message['body'] as String? ?? '');
    final text = formatByteInput('Text', _pausedBody);
    _format = text == null ? 'Base64' : 'Text';
    _body.text = text ?? base64.encode(_pausedBody);
  }

  void _setFormat(String format) {
    if (format == _format) return;
    try {
      final text = formatByteInput(format, parseByteInput(_format, _body.text));
      setState(() {
        if (text == null) { _error = context.tr('This body is not UTF-8 text'); return; }
        _format = format;
        _body.text = text;
        _error = null;
      });
    } on FormatException catch (failure) { setState(() => _error = context.tr(failure.message)); }
  }

  /// Only what differs from the held message is sent; the core keeps the rest.
  Json _decision() {
    final message = _message!, method = _method.text.trim(), url = _url.text.trim(), status = _status.text.trim();
    final decision = <String, dynamic>{'action': 'continue'};
    if (_response) {
      if (status != '${message['status'] ?? ''}') {
        final code = int.tryParse(status);
        if (code == null || code < 200 || code > 599) throw const FormatException('Status must be between 200 and 599');
        decision['status'] = code;
      }
    } else {
      if (method != message['method']) decision['method'] = method;
      if (url != message['url']) decision['url'] = url;
    }
    if (_headers.text != _pausedHeaders) decision['headers'] = parseHeaderLines(_headers.text);
    if (_bodyShown) {
      final body = parseByteInput(_format, _body.text);
      if (!listEquals(body, _pausedBody)) decision['body'] = base64.encode(body);
    }
    return decision;
  }

  Future<void> _resolve({required bool abort}) async {
    final box = context.box;
    if (_busy || _message == null || box.remote || _session != box.sessionRevision) return;
    if (abort && !await confirm(context, 'Abort this exchange',
      message: 'The client connection is closed without a response.', action: 'Abort')) return;
    // The decision belongs to the session that holds the message, which may have changed while the dialog was open.
    if (!mounted || _busy || box.remote || _session != box.sessionRevision) return;
    setState(() { _busy = true; _error = null; });
    try {
      await box.call('capture.breakpoint.resolve', {'id': widget.id, 'decision': abort ? {'action': 'abort'} : _decision()});
      if (!mounted || box.remote || _session != box.sessionRevision) return;
      setState(() => _dirty = false);
      Navigator.pop(context);
      box.messages.add(context.tr(abort ? 'Exchange aborted' : 'Message resumed'));
    } catch (failure) {
      if (mounted) setState(() => _error = context.tr(failure is FormatException ? failure.message : BoxController.describe(failure)));
    } finally { if (mounted) setState(() => _busy = false); }
  }

  // A body of up to 256 KiB is edited here, so typing rebuilds the page only when what it shows changes.
  void _edited() {
    if (!_dirty || _error != null) setState(() { _dirty = true; _error = null; });
  }

  Widget _field(String label, String key, TextEditingController controller,
      {bool multiline = false, TextInputType keyboardType = TextInputType.url}) => Padding(
    padding: const EdgeInsets.only(bottom: 12),
    child: BoxField(label: context.tr(label), borderRadius: AppRadius.md,
      child: TextField(key: ValueKey(key), controller: controller, readOnly: _busy,
        minLines: multiline ? 4 : 1, maxLines: multiline ? 16 : 4,
        keyboardType: multiline ? TextInputType.multiline : keyboardType,
        autocorrect: false, enableSuggestions: false,
        smartDashesType: SmartDashesType.disabled, smartQuotesType: SmartQuotesType.disabled,
        style: _monospace, onChanged: (_) => _edited())));

  @override Widget build(BuildContext context) {
    final box = context.watchBox(BoxAspect.connections), message = _message;
    const title = 'Paused message';
    if (box.remote) return const BoxPage(title: title, children: [EmptyState(_localOnly)]);
    if (_session != box.sessionRevision) return const BoxPage(title: title, children: [EmptyState('Session changed; reopen this activity from the current list')]);
    if (message == null) return BoxPage(title: title, children: [
      if (_loadError != null) ErrorState(_loadError!, retry: () { setState(() => _loadError = null); _load(box); })
      else const Center(child: ActivityIndicator()),
    ]);
    return BoxEditGuard(dirty: _dirty, busy: _busy, child: BoxPage(title: title, loading: _busy,
      actions: [IconButtonData(glyph: AppGlyphs.block, tooltip: context.tr('Abort'), onPressed: _busy ? null : () => _resolve(abort: true))],
      confirmationAction: IconButtonData(glyph: AppGlyphs.playPause(0), tooltip: context.tr('Continue'), isLoading: _busy,
        onPressed: _busy ? null : () => _resolve(abort: false)), children: [
        if (_error != null) Padding(padding: const EdgeInsets.all(12),
          child: Text(_error!, style: TextStyle(color: context.colorScheme.error))),
        generateSection(items: [
          DetailRow.text(title: context.tr('Paused at'), value: '${context.tr(_phaseLabel(message))} · ${localDate(message['pausedAt'])}'),
          if (_response) DetailRow.text(title: context.tr('Request'), value: '${message['method']} ${message['url']}', stacked: true),
        ]),
        if (_response) _field('Status', 'paused-status', _status, keyboardType: TextInputType.number)
        else ...[_field('Method', 'paused-method', _method), _field('URL', 'paused-url', _url)],
        _field('Headers', 'paused-headers', _headers, multiline: true),
        if (_bodyShown) ...[
          AbsorbPointer(absorbing: _busy, child: generateSection(items: [
            ListItem<String>.options(key: const ValueKey('paused-body-format'), title: Text(context.tr('Content format')),
              options: byteInputFormats, value: _format, textBuilder: context.tr, onChanged: _setFormat),
          ])),
          _field('Body', 'paused-body', _body, multiline: true),
        ],
        ListFooter(text: context.tr(_bodyShown ? 'The body is shown decoded. Content-Length, Transfer-Encoding and Content-Encoding follow the forwarded body and cannot be edited.'
          : _bodyEditable ? 'This body is too large to edit here and is forwarded unchanged'
          : 'This body is streamed unchanged and cannot be edited')),
      ]));
  }
}
