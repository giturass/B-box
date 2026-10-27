import 'package:flutter/services.dart';
import '../widgets.dart';
import 'terminal_settings.dart';

class TerminalPage extends StatefulWidget {
  const TerminalPage({super.key, this.endpoint, this.peer, this.name});
  final String? endpoint, peer, name;
  @override State<TerminalPage> createState() => _TerminalPageState();
}
class _TerminalPageState extends State<TerminalPage> {
  final _username = TextEditingController(text: 'root');
  final _terminalType = TextEditingController(text: 'xterm-256color');
  BoxController? _box;
  bool _loading = true, _busy = false, _remember = false;
  late bool _prompt = widget.peer != null;
  String? _error;
  int _settingsRevision = 0;
  bool _covered = false;
  @override void didChangeDependencies() {
    super.didChangeDependencies();
    if (_box != null) return;
    _box = context.box;
    _load();
  }
  Future<void> _load() async {
    try {
      final snapshot = json(await _box!.call('ssh.snapshot'));
      _box!.terminal = snapshot;
      if (_prompt) {
        final options = json(await _box!.call('ssh.options', {'endpoint': widget.endpoint, 'peer': widget.peer}));
        if (!mounted) return;
        _username.text = options['username'] as String? ?? 'root';
        _terminalType.text = options['terminalType'] as String? ?? 'xterm-256color';
        _remember = options['remember'] == true;
      }
      if (!mounted) return;
      setState(() { _loading = false; _error = null; });
      if (_prompt && _remember) await _start();
    } catch (error) { if (mounted) setState(() { _loading = false; _error = BoxController.describe(error); }); }
  }
  Future<void> _start() async {
    if (_busy || _username.text.trim().isEmpty) return;
    setState(() => _busy = true);
    try {
      final snapshot = json(await _box!.call('ssh.start', {'endpoint': widget.endpoint, 'peer': widget.peer,
        'username': _username.text.trim(), 'terminalType': _terminalType.text.trim(), 'remember': _remember}));
      if (!mounted) return;
      _box!.terminal = snapshot;
      setState(() { _prompt = false; _error = null; });
    } catch (error) { if (mounted) setState(() => _error = BoxController.describe(error)); }
    if (mounted) setState(() => _busy = false);
  }
  Future<void> _settings() async {
    await _pauseSurface(() => openPage(context, const TerminalSettingsPage()));
    if (mounted) setState(() => _settingsRevision++);
  }
  Future<void> _newSession() async {
    await _pauseSurface(() async {
      final peer = await openForm<Json>(context, const TerminalPeerPicker());
      if (!mounted || peer == null) return;
      await openPage(context, TerminalPage(endpoint: peer['endpoint'] as String, peer: peer['id'] as String, name: peer['name'] as String));
    });
  }
  Future<void> _sessions() async {
    await _pauseSurface(() => openForm(context, const TerminalSessionsPage()));
  }
  Future<void> _pauseSurface(Future<dynamic> Function() open) async {
    setState(() => _covered = true);
    await WidgetsBinding.instance.endOfFrame;
    try { if (mounted) await open(); }
    finally { if (mounted) setState(() => _covered = false); }
  }
  @override void dispose() { _username.dispose(); _terminalType.dispose(); super.dispose(); }
  @override Widget build(BuildContext context) {
    final box = context.box, snapshot = context.box.terminal;
    final sessions = rows(snapshot['sessions']);
    Json? active;
    for (final session in sessions) { if (session['id'] == snapshot['activeId']) active = session; }
    final progress = json(active?['progress']);
    return CommonScaffold(title: _prompt ? 'SSH · ${widget.name ?? ''}' : active?['name'] as String? ?? context.tr('Terminal'),
      isLoading: _loading || _busy,
      iconActions: [
        IconButtonData(glyph: AppGlyphs.add, tooltip: context.tr('New session'), onPressed: _newSession),
        IconButtonData(glyph: AppGlyphs.layers, tooltip: context.tr('Sessions'), onPressed: _sessions),
        IconButtonData(glyph: AppGlyphs.settings, tooltip: context.tr('Ghostty settings'), onPressed: _settings),
      ],
      body: Padding(padding: EdgeInsets.only(top: context.appBarInset, bottom: MediaQuery.paddingOf(context).bottom), child:
        _loading ? const SizedBox() : _prompt ? ListView(padding: const EdgeInsets.all(20), children: [
          TextField(controller: _username, autocorrect: false, enableSuggestions: false,
            decoration: InputDecoration(labelText: context.tr('Username'))),
          const SizedBox(height: 16),
          TextField(controller: _terminalType, autocorrect: false, enableSuggestions: false,
            decoration: InputDecoration(labelText: context.tr('Terminal type')), onSubmitted: (_) => _start()),
          const SizedBox(height: 16),
          ListItem.toggle(title: Text(context.tr('Quick connect')), subtitle: Text(context.tr('Remember options and connect directly next time')),
            value: _remember, onChanged: (value) => setState(() => _remember = value)),
          if (_error != null) Padding(padding: const EdgeInsets.symmetric(vertical: 16), child: Text(_error!, style: TextStyle(color: context.colorScheme.error))),
          const SizedBox(height: 20), FilledButton(onPressed: _busy ? null : _start, child: Text(context.tr('Connect'))),
        ]) : active == null ? EmptyState('No terminal sessions',
          message: _error, action: FilledButton(onPressed: _newSession, child: Text(context.tr('New session')))) : Column(children: [
          if (active['phase'] == 'CONNECTING' || progress.isNotEmpty) LinearProgressIndicator(
            value: active['phase'] == 'CONNECTING' || progress['percent'] == null ? null : (progress['percent'] as num).toDouble() / 100,
            color: progress['state'] == 'ERROR' ? context.colorScheme.error : null),
          if (active['banner'] != null) Padding(padding: const EdgeInsets.all(12), child: SelectableText(active['banner'].toString())),
          Expanded(child: _covered ? const SizedBox() : GhosttySurface(sessionId: active['id'] as String, settingsRevision: _settingsRevision)),
          if (active['phase'] == 'FINISHED') TextButton(onPressed: () => box.act('ssh.close', {'id': active!['id']}), child: Text(context.tr('Close session'))),
        ])),
    );
  }
}

class GhosttySurface extends StatefulWidget {
  const GhosttySurface({super.key, required this.sessionId, required this.settingsRevision, this.preview = false});
  final String sessionId;
  final int settingsRevision;
  final bool preview;
  @override State<GhosttySurface> createState() => _GhosttySurfaceState();
}
class _GhosttySurfaceState extends State<GhosttySurface> {
  MethodChannel? _channel;
  Json _modifiers = {};
  bool? _dark;
  Json get _parameters => {'id': widget.sessionId, 'dark': _dark == true, 'preview': widget.preview};
  @override void didChangeDependencies() {
    super.didChangeDependencies();
    final dark = Theme.of(context).brightness == Brightness.dark;
    if (_dark == dark) return;
    _dark = dark;
    _update();
  }
  @override void didUpdateWidget(GhosttySurface oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.sessionId != widget.sessionId || oldWidget.settingsRevision != widget.settingsRevision) _update();
  }
  Future<void> _update() async {
    try { await _channel?.invokeMethod('update', _parameters); }
    catch (error) { if (mounted) context.box.report(error); }
  }
  void _created(int id) {
    _channel = MethodChannel('io.ericlee.bfa/terminal/$id')..setMethodCallHandler((call) async {
      if (!mounted) return null;
      switch (call.method) {
        case 'modifiers': setState(() => _modifiers = json(call.arguments)); return null;
        case 'unsafePaste': return confirm(context, 'Paste multiple lines?', message: 'Pasted text may execute commands in this terminal.', action: 'Paste');
        case 'clipboardRead': return confirm(context, 'Allow terminal to read clipboard?', action: 'Allow');
        case 'link': return showDialog<String>(context: context, builder: (context) => AlertDialog(
          title: Text(context.tr('Link')), content: SelectableText(call.arguments.toString()), actions: [
            TextButton(onPressed: () => Navigator.pop(context), child: Text(context.tr('Cancel'))),
            TextButton(onPressed: () => Navigator.pop(context, 'copy'), child: Text(context.tr('Copy'))),
            TextButton(onPressed: () => Navigator.pop(context, 'open'), child: Text(context.tr('Open'))),
          ]));
      }
      return null;
    });
    _update();
    setState(() {});
  }
  Future<void> _key(String key) async {
    try { await _channel?.invokeMethod('key', {'key': key}); }
    catch (error) { if (mounted) context.box.report(error); }
  }
  @override void dispose() { _channel?.setMethodCallHandler(null); super.dispose(); }
  @override Widget build(BuildContext context) => Column(children: [
    Expanded(child: AndroidView(viewType: 'io.ericlee.bfa/ghostty', creationParams: _parameters,
      creationParamsCodec: const StandardMessageCodec(), onPlatformViewCreated: _created)),
    if (!widget.preview) SingleChildScrollView(scrollDirection: Axis.horizontal, child: Row(children: [
      for (final key in const ['Esc', 'Tab', 'Ctrl', 'Alt', '↑', '↓', '←', '→', 'Home', 'End', 'PgUp', 'PgDn', 'Paste', 'Keyboard'])
        TextButton(onPressed: _channel == null ? null : () => _key(key),
          style: _modifiers[key.toLowerCase()] == 'ARMED' || _modifiers[key.toLowerCase()] == 'LOCKED'
            ? TextButton.styleFrom(backgroundColor: context.colorScheme.primaryContainer) : null,
          child: Text(context.tr(key) + (_modifiers[key.toLowerCase()] == 'LOCKED' ? ' •' : ''))),
    ])),
  ]);
}

class TerminalSessionsPage extends StatelessWidget {
  const TerminalSessionsPage({super.key});
  @override Widget build(BuildContext context) {
    final box = context.box, sessions = rows(context.box.terminal['sessions']);
    return BoxPage(title: 'Sessions', actions: [
      if (sessions.isNotEmpty) IconButtonData(glyph: AppGlyphs.copy, tooltip: context.tr('Duplicate session'), onPressed: () async {
        if (await box.act('ssh.duplicate') && context.mounted) context.safeNestedPop();
      }),
    ], children: [
      if (sessions.isEmpty) const EmptyState('No terminal sessions'),
      generateGroup([for (final session in sessions) ListItem(
        leading: session['id'] == box.terminal['activeId'] ? const SelectionCheck() : const GlyphIcon(AppGlyphs.code),
        title: Text(session['name'].toString()), subtitle: Text('${session['username']}@${session['host']} · ${context.tr(session['phase'] == 'RUNNING' ? 'Running' : session['phase'] == 'CONNECTING' ? 'Connecting' : 'Disconnected')}'),
        trailing: IconButton(tooltip: context.tr('Close session'), icon: const GlyphIcon(AppGlyphs.close), onPressed: () async {
          if (await confirm(context, 'Close this SSH session?', action: 'Close')) await box.act('ssh.close', {'id': session['id']});
        }),
        onTap: () async { if (await box.act('ssh.select', {'id': session['id']}) && context.mounted) context.safeNestedPop(); },
      )]),
    ]);
  }
}

class TerminalPeerPicker extends StatelessWidget {
  const TerminalPeerPicker({super.key});
  @override Widget build(BuildContext context) {
    final peers = <Json>[
      for (final endpoint in rows(context.box.toolState['tailscale']))
        for (final group in rows(endpoint['userGroups']))
          for (final peer in rows(group['peers']))
            if ((peer['sshHostKeys'] as List? ?? []).isNotEmpty) {...peer, 'endpoint': endpoint['endpointTag']},
    ];
    return BoxPage(title: 'New session', children: [
      if (peers.isEmpty) const EmptyState('No SSH peers', message: 'Start a Tailscale endpoint with SSH-enabled peers.'),
      generateGroup([for (final peer in peers) ListItem(title: Text(peer['hostName'].toString()),
        subtitle: Text((peer['tailscaleIPs'] as List? ?? []).join(' · ')),
        onTap: () => context.safeNestedPop({'id': peer['id'], 'endpoint': peer['endpoint'], 'name': peer['hostName']}))]),
    ]);
  }
}
