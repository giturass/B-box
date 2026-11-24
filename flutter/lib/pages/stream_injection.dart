import 'dart:async';
import 'dart:convert';

import '../widgets.dart';
import '../components/record.dart';

/// Sends bytes through an intercepted stream whose lifetime is checked by the core.
class StreamInjectionPage extends StatefulWidget {
  const StreamInjectionPage({super.key, required this.streamId, required this.websocket,
    this.direction = 'client', this.opcode = 1});
  final String streamId;
  final bool websocket;
  final String direction;
  final int opcode;
  @override State<StreamInjectionPage> createState() => _StreamInjectionPageState();
}

class _StreamInjectionPageState extends State<StreamInjectionPage> {
  static int _requestSequence = 0;
  final _content = TextEditingController();
  late String _direction = widget.direction;
  late int _opcode = widget.opcode == 2 ? 2 : 1;
  String _format = 'Text';
  final _cancellations = <String, Future<bool>>{};
  bool _busy = false, _dirty = false, _canceling = false;
  String? _error;
  String? _requestId;
  BoxController? _box;
  int? _session;

  @override void didChangeDependencies() {
    super.didChangeDependencies();
    _box = context.box;
    _session ??= context.box.sessionRevision;
  }

  @override void dispose() {
    if (_requestId != null) _box?.call('capture.inject.cancel', {'requestId': _requestId}).catchError((_) => false);
    _content.dispose();
    super.dispose();
  }

  List<int> _bytes() {
    if (_format == 'Text') return utf8.encode(_content.text);
    final value = _content.text.replaceAll(RegExp(r'\s+'), '');
    if (_format == 'Base64') {
      try { return base64.decode(value); }
      on FormatException { throw const FormatException('Invalid Base64 message'); }
    }
    if (value.length.isOdd || !RegExp(r'^[0-9a-fA-F]*$').hasMatch(value)) {
      throw const FormatException('Enter hexadecimal byte pairs');
    }
    return [for (var offset = 0; offset < value.length; offset += 2) int.parse(value.substring(offset, offset + 2), radix: 16)];
  }

  Future<void> _send() async {
    final box = context.box;
    if (_busy || box.remote || _session != box.sessionRevision || !box.running || box.transitioning) return;
    final requestId = 'stream-${DateTime.now().microsecondsSinceEpoch}-${++_requestSequence}';
    setState(() { _busy = true; _error = null; _requestId = requestId; _canceling = false; });
    try {
      final data = _bytes();
      if (widget.websocket && _opcode == 1) {
        try { utf8.decode(data); }
        on FormatException { throw const FormatException('WebSocket text messages must contain valid UTF-8'); }
      }
      await box.call('capture.inject', {'requestId': requestId, 'streamId': widget.streamId, 'direction': _direction,
        'opcode': widget.websocket ? _opcode : 0, 'data': base64.encode(data)});
      if (!mounted || box.remote || _session != box.sessionRevision) return;
      setState(() { _dirty = false; _busy = false; });
      Navigator.pop(context);
      box.messages.add(context.tr('Message injected'));
    } catch (failure) {
      final cancellation = _cancellations[requestId];
      final cancelled = cancellation != null && await cancellation;
      if (mounted) setState(() => _error = cancelled ? context.tr('Injection cancelled')
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
    try { completion.complete(await context.box.call('capture.inject.cancel', {'requestId': requestId}) == true); }
    catch (failure) {
      completion.complete(false);
      if (mounted && _requestId == requestId) setState(() => _error = BoxController.describe(failure));
    } finally { if (mounted && _requestId == requestId) setState(() => _canceling = false); }
  }

  void _changed(VoidCallback update) => setState(() { update(); _dirty = true; _error = null; });

  @override Widget build(BuildContext context) {
    final box = context.watchBox(BoxAspect.connections);
    if (box.remote) return const BoxPage(title: 'Inject message', children: [EmptyState('HTTP debugging is only available for the local service')]);
    if (_session != box.sessionRevision) return const BoxPage(title: 'Inject message', children: [EmptyState('Session changed; reopen this activity from the current list')]);
    return BoxEditGuard(dirty: _dirty, busy: _busy, child: BoxPage(title: 'Inject message', loading: _busy,
      actions: [if (_busy) IconButtonData(glyph: AppGlyphs.close, tooltip: context.tr('Cancel injection'),
        isLoading: _canceling, onPressed: _canceling ? null : _cancel)],
      confirmationAction: IconButtonData(glyph: AppGlyphs.send, tooltip: context.tr('Send'), isLoading: _busy,
        onPressed: _busy || !box.running || box.transitioning ? null : _send), children: [
        if (_error != null) Padding(padding: const EdgeInsets.all(12),
          child: Text(_error!, style: TextStyle(color: context.colorScheme.error))),
        AbsorbPointer(absorbing: _busy, child: generateSection(items: [
          DetailRow.text(title: context.tr('Stream ID'), value: widget.streamId, stacked: true),
          ListItem<String>.options(key: const ValueKey('inject-direction'), title: Text(context.tr('Direction')),
            options: const ['client', 'server'], value: _direction,
            textBuilder: (value) => context.tr(value == 'server' ? 'Server to client' : 'Client to server'),
            onChanged: (value) { if (!_busy) _changed(() => _direction = value); }),
          if (widget.websocket) ListItem<int>.options(key: const ValueKey('inject-opcode'), title: Text(context.tr('Message type')),
            options: const [1, 2], value: _opcode,
            textBuilder: (value) => context.tr(value == 2 ? 'Binary message' : 'Text message'),
            onChanged: (value) { if (!_busy) _changed(() => _opcode = value); }),
          ListItem<String>.options(key: const ValueKey('inject-format'), title: Text(context.tr('Content format')),
            options: const ['Text', 'Hex', 'Base64'], value: _format,
            textBuilder: context.tr, onChanged: (value) { if (!_busy) _changed(() => _format = value); }),
        ], footer: context.tr('Client to server sends to the upstream. Server to client sends to the captured application.'))),
        if (widget.websocket) ListFooter(text: context.tr('Cancelling a WebSocket write in progress closes its stream to keep message framing valid.')),
        BoxField(label: context.tr('Message content'), borderRadius: AppRadius.md,
          child: TextField(key: const ValueKey('inject-content'), controller: _content, readOnly: _busy,
            minLines: 6, maxLines: 16, keyboardType: TextInputType.multiline, autocorrect: false, enableSuggestions: false,
            smartDashesType: SmartDashesType.disabled, smartQuotesType: SmartQuotesType.disabled,
            style: const TextStyle(fontFamily: 'JetBrainsMono', fontSize: 13, height: 1.6),
            onChanged: (_) => _changed(() {}))),
      ]));
  }
}
