import 'dart:async';
import 'dart:convert';
import 'package:flutter/services.dart';
import 'package:xterm/xterm.dart';
import '../widgets.dart';

class TerminalPage extends StatefulWidget {
  const TerminalPage({super.key, required this.endpoint, required this.peer, required this.name});
  final String endpoint, peer, name;
  @override State<TerminalPage> createState() => _TerminalPageState();
}
class _TerminalPageState extends State<TerminalPage> {
  final _terminal = Terminal(maxLines: 10000);
  final _username = TextEditingController();
  final _output = StreamController<List<int>>();
  final _id = DateTime.now().microsecondsSinceEpoch.toString();
  StreamSubscription<Json>? _events;
  StreamSubscription<String>? _decoded;
  BoxController? _box;
  bool _started = false, _connected = false, _closed = false;
  Future<void> _inputQueue = Future.value();
  @override void didChangeDependencies() {
    super.didChangeDependencies();
    if (_box != null) return;
    _box = context.box;
    _decoded = _output.stream.transform(const Utf8Decoder(allowMalformed: true)).listen(_terminal.write);
    _events = _box!.sshEvents.stream.where((event) => event['id'] == _id).listen((event) {
      switch (event['event']) {
        case 'output': _output.add((event['value'] as List).cast<int>());
        case 'banner': _terminal.write(event['value'].toString());
        case 'ready': setState(() => _connected = true);
        case 'exit':
          final value = json(event['value']);
          _terminal.write('\r\n${context.tr('Disconnected')} (${value['code']}) ${value['error'] ?? ''}\r\n');
          setState(() { _connected = false; _closed = true; });
        case 'error':
          _terminal.write('\r\n${event['value']}\r\n');
          setState(() { _connected = false; _closed = true; });
      }
    });
    _terminal.onOutput = (text) {
      // Preserve the byte order even when native writes suspend on IO.
      if (_connected) _inputQueue = _inputQueue.then((_) async { await _box!.act('ssh.input', {'id': _id, 'text': text}); });
    };
    _terminal.onResize = (columns, rows, width, height) {
      if (_connected) _box!.act('ssh.resize', {'id': _id, 'columns': columns, 'rows': rows});
    };
  }
  Future<void> _start() async {
    setState(() => _started = true);
    final ok = await _box!.act('ssh.start', {'id': _id, 'endpoint': widget.endpoint, 'peer': widget.peer,
      'username': _username.text.trim(), 'columns': _terminal.viewWidth, 'rows': _terminal.viewHeight});
    if (!ok && mounted) setState(() { _started = false; _connected = false; });
  }
  @override void dispose() {
    _terminal.onOutput = null;
    _terminal.onResize = null;
    _events?.cancel(); _output.close(); _decoded?.cancel();
    _box?.act('ssh.close', {'id': _id});
    _username.dispose();
    super.dispose();
  }
  @override Widget build(BuildContext context) => CommonScaffold(title: 'SSH · ${widget.name}', body: Padding(
    padding: EdgeInsets.only(top: context.contentTopPadding, bottom: context.bottomInset),
    child: !_started ? ListView(padding: const EdgeInsets.all(20), children: [
      TextField(controller: _username, autofocus: true, autocorrect: false,
        decoration: InputDecoration(labelText: context.tr('Username')), onSubmitted: (_) => _start()),
      const SizedBox(height: 20), FilledButton(onPressed: _start, child: Text(context.tr('Connect'))),
    ]) : Column(children: [
      if (!_connected && !_closed) const LinearProgressIndicator(),
      Expanded(child: TerminalView(_terminal, autofocus: true)),
      SingleChildScrollView(scrollDirection: Axis.horizontal, child: Row(children: [
        for (final item in const [('Esc', '\x1b'), ('Tab', '\t'), ('Ctrl+C', '\x03'), ('↑', '\x1b[A'), ('↓', '\x1b[B'), ('←', '\x1b[D'), ('→', '\x1b[C')])
          TextButton(onPressed: !_connected ? null : () => _terminal.onOutput?.call(item.$2), child: Text(item.$1)),
        TextButton(onPressed: !_connected ? null : () async {
          final data = await Clipboard.getData(Clipboard.kTextPlain);
          if (_connected && mounted && data?.text != null) _terminal.paste(data!.text!);
        }, child: Text(context.tr('Paste'))),
      ])),
    ]),
  ));
}
