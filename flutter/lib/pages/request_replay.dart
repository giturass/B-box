import 'dart:async';
import 'dart:convert';

import '../widgets.dart';
import '../common/byte_input.dart';
import '../common/http_message.dart';

const _monospace = TextStyle(fontFamily: 'JetBrainsMono', fontSize: 13, height: 1.6);
const _capturedBody = 'Captured body';

/// Resends one captured request through the running service. Pops with the ID of the new record.
class RequestReplayPage extends StatefulWidget {
  const RequestReplayPage({super.key, required this.id, required this.request});
  final String id;
  /// The captured request message of [id].
  final Json request;
  @override State<RequestReplayPage> createState() => _RequestReplayPageState();
}

class _RequestReplayPageState extends State<RequestReplayPage> {
  static int _requestSequence = 0;
  late final _capturedMethod = widget.request['method']?.toString() ?? '';
  late final _capturedUrl = widget.request['url']?.toString() ?? '';
  late final _capturedHeaders = headerLines(rows(widget.request['headers']));
  late final _method = TextEditingController(text: _capturedMethod);
  late final _url = TextEditingController(text: _capturedUrl);
  late final _headers = TextEditingController(text: _capturedHeaders);
  final _body = TextEditingController();
  final _cancellations = <String, Future<bool>>{};
  String _source = _capturedBody;
  bool _busy = false, _dirty = false, _canceling = false;
  String? _error;
  String? _requestId;
  BoxController? _box;
  int? _session;

  bool get _headersTruncated => widget.request['headersTruncated'] == true;

  @override void didChangeDependencies() {
    super.didChangeDependencies();
    _box = context.box;
    _session ??= context.box.sessionRevision;
  }

  @override void dispose() {
    if (_requestId != null) _box?.call('capture.replay.cancel', {'requestId': _requestId}).catchError((_) => false);
    for (final controller in [_method, _url, _headers, _body]) { controller.dispose(); }
    super.dispose();
  }

  /// Only what differs from the capture is sent; the core resends the rest as recorded.
  Json _options() {
    final method = _method.text.trim(), url = _url.text.trim();
    return {
      if (method != _capturedMethod) 'method': method,
      if (url != _capturedUrl) 'url': url,
      if (_headers.text != _capturedHeaders || _headersTruncated) 'headers': parseHeaderLines(_headers.text),
      if (_source != _capturedBody) 'body': base64.encode(parseByteInput(_source, _body.text)),
    };
  }

  Future<void> _send() async {
    final box = context.box;
    if (_busy || box.remote || _session != box.sessionRevision || !box.running || box.transitioning) return;
    final requestId = 'replay-${DateTime.now().microsecondsSinceEpoch}-${++_requestSequence}';
    setState(() { _busy = true; _error = null; _requestId = requestId; _canceling = false; });
    try {
      final result = json(await box.call('capture.replay', {'requestId': requestId, 'id': widget.id, 'options': _options()}));
      if (!mounted || box.remote || _session != box.sessionRevision) return;
      final failure = result['error'] as String? ?? '';
      setState(() { _dirty = false; _busy = false; });
      Navigator.pop(context, result['id'] as String?);
      box.messages.add(failure.isEmpty ? context.tr('Request replayed') : failure);
    } catch (failure) {
      final cancellation = _cancellations[requestId];
      final cancelled = cancellation != null && await cancellation;
      if (mounted) setState(() => _error = cancelled ? context.tr('Replay cancelled')
        : failure is FormatException ? context.tr(failure.message) : BoxController.describe(failure));
    } finally {
      _cancellations.remove(requestId);
      if (mounted && _requestId == requestId) setState(() { _busy = false; _requestId = null; });
    }
  }

  Future<void> _cancel() async {
    final requestId = _requestId;
    if (requestId == null || _canceling) return;
    final completion = Completer<bool>();
    _cancellations[requestId] = completion.future;
    setState(() => _canceling = true);
    try { completion.complete(await context.box.call('capture.replay.cancel', {'requestId': requestId}) == true); }
    catch (failure) {
      completion.complete(false);
      if (mounted && _requestId == requestId) setState(() => _error = BoxController.describe(failure));
    } finally { if (mounted && _requestId == requestId) setState(() => _canceling = false); }
  }

  void _changed(VoidCallback update) => setState(() { update(); _dirty = true; _error = null; });

  Widget _field(String label, String key, TextEditingController controller, {bool multiline = false}) => Padding(
    padding: const EdgeInsets.only(bottom: 12),
    child: BoxField(label: context.tr(label), borderRadius: AppRadius.md,
      child: TextField(key: ValueKey(key), controller: controller, readOnly: _busy,
        minLines: multiline ? 4 : 1, maxLines: multiline ? 16 : 4,
        keyboardType: multiline ? TextInputType.multiline : TextInputType.url,
        autocorrect: false, enableSuggestions: false,
        smartDashesType: SmartDashesType.disabled, smartQuotesType: SmartQuotesType.disabled,
        style: _monospace, onChanged: (_) => _changed(() {}))));

  @override Widget build(BuildContext context) {
    final box = context.watchBox(BoxAspect.connections);
    if (box.remote) return const BoxPage(title: 'Replay request', children: [EmptyState('HTTP debugging is only available for the local service')]);
    if (_session != box.sessionRevision) return const BoxPage(title: 'Replay request', children: [EmptyState('Session changed; reopen this activity from the current list')]);
    return BoxEditGuard(dirty: _dirty, busy: _busy, child: BoxPage(title: 'Replay request', loading: _busy,
      actions: [if (_busy) IconButtonData(glyph: AppGlyphs.close, tooltip: context.tr('Cancel replay'),
        isLoading: _canceling, onPressed: _canceling ? null : _cancel)],
      confirmationAction: IconButtonData(glyph: AppGlyphs.send, tooltip: context.tr('Send'), isLoading: _busy,
        onPressed: _busy || !box.running || box.transitioning ? null : _send), children: [
        if (_error != null) Padding(padding: const EdgeInsets.all(12),
          child: Text(_error!, style: TextStyle(color: context.colorScheme.error))),
        if (!box.running) Padding(padding: const EdgeInsets.all(12),
          child: Text(context.tr('Start the local service before replaying a request'))),
        _field('Method', 'replay-method', _method),
        _field('URL', 'replay-url', _url),
        _field('Headers', 'replay-headers', _headers, multiline: true),
        if (_headersTruncated) ListFooter(text: context.tr('The captured headers are truncated. Complete them before sending.')),
        AbsorbPointer(absorbing: _busy, child: generateSection(items: [
          ListItem<String>.options(key: const ValueKey('replay-body-source'), title: Text(context.tr('Body')),
            options: const [_capturedBody, ...byteInputFormats], value: _source,
            textBuilder: context.tr, onChanged: (value) { if (!_busy) _changed(() => _source = value); }),
        ], footer: context.tr('A replacement body is sent as entered: Content-Length is recalculated and Content-Encoding is removed.'))),
        if (_source != _capturedBody) _field('Replacement body', 'replay-body', _body, multiline: true),
        ListFooter(text: context.tr('The request is sent through the routing of the running service and saved as a new record. '
            'Rewrites, scripts and breakpoints are not applied, and redirects are not followed.')),
      ]));
  }
}
