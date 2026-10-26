import 'dart:async';
import 'package:flutter/services.dart';
import '../widgets.dart';

class AuthenticationPage extends StatefulWidget {
  const AuthenticationPage({super.key, required this.kind, required this.endpoint, required this.challenge});
  final String kind, endpoint;
  final Json challenge;
  @override State<AuthenticationPage> createState() => _AuthenticationPageState();
}

class _AuthenticationPageState extends State<AuthenticationPage> {
  final _controllers = <String, TextEditingController>{};
  final _values = <String, String>{};
  bool _busy = false, _complete = false;
  BoxController? _box;
  String get _id => widget.challenge['id'].toString();
  Json get _args => {'endpoint': widget.endpoint, 'challenge': _id};
  @override void initState() {
    super.initState();
    if (widget.kind == 'openconnect') {
      for (final field in rows(json(widget.challenge['form'])['fields'])) {
        final key = field['submissionKey'].toString();
        _values[key] = field['value'] as String? ?? '';
        _controllers[key] = TextEditingController(text: _values[key]);
      }
    } else {
      for (final key in ['username', 'password', 'secret']) {
        _controllers[key] = TextEditingController(text: key == 'username' ? widget.challenge[key] as String? ?? '' : '');
      }
    }
  }
  @override void didChangeDependencies() { super.didChangeDependencies(); _box ??= context.box; }
  @override void dispose() {
    for (final controller in _controllers.values) { controller.dispose(); }
    if (!_complete) _box?.act('${widget.kind}.cancel', _args);
    super.dispose();
  }
  Future<void> _submit() async {
    setState(() => _busy = true);
    final values = <String, String>{for (final e in _controllers.entries) e.key: e.value.text, ..._values};
    // Select fields use _values; text/password fields use their controllers.
    for (final field in rows(json(widget.challenge['form'])['fields'])) {
      if (field['kind'] != 'select') values[field['submissionKey'].toString()] = _controllers[field['submissionKey']]!.text;
    }
    final ok = await _box!.act('${widget.kind}.submit', {..._args,
      if (widget.kind == 'openconnect') 'values': values else ...values,
    });
    if (!mounted) return;
    setState(() => _busy = false);
    if (ok) { _complete = true; Navigator.pop(context); }
  }
  @override Widget build(BuildContext context) {
    final challenge = widget.challenge;
    final browser = json(challenge['browser']);
    return BoxPage(title: 'Authentication', loading: _busy, children: [
      for (final key in ['banner', 'message', 'error', 'previousError'])
        if ((challenge[key] ?? '') != '') Padding(padding: const EdgeInsets.all(16), child: SelectableText(challenge[key].toString())),
      if (widget.kind == 'openconnect') ...[
        if (browser.isNotEmpty) ListItem(title: Text(context.tr('Open sign-in page')), onTap: () async {
          final completed = await openPage<bool>(context, AuthBrowser(endpoint: widget.endpoint, challenge: _id));
          if (completed == true && context.mounted) { _complete = true; Navigator.pop(context); }
        }),
        for (final field in rows(json(challenge['form'])['fields']))
          if (field['kind'] == 'select') ListItem<String>.options(
            title: Text((field['label'] ?? field['name']).toString()),
            options: rows(field['options']).map((o) => o['value'].toString()).toList(),
            value: _values[field['submissionKey']] ?? '',
            textBuilder: (value) => rows(field['options']).firstWhere((o) => o['value'] == value, orElse: () => {'label': value})['label'].toString(),
            onChanged: (value) => setState(() => _values[field['submissionKey'].toString()] = value),
          ) else Padding(padding: const EdgeInsets.all(16), child: TextField(
            controller: _controllers[field['submissionKey']], obscureText: field['kind'] == 'password',
            autocorrect: false, enableSuggestions: false,
            decoration: InputDecoration(labelText: (field['label'] ?? field['name']).toString()),
          )),
      ] else if (challenge['kind'] == 'open-url') ListItem(
        title: Text(context.tr('Open sign-in page')), subtitle: SelectableText((challenge['url'] ?? '').toString()),
        onTap: () => _box!.act('url.open', {'url': challenge['url']}),
      ) else ...[
        if (challenge['kind'] == 'credentials') ...[
          _field('username', 'Username'), _field('password', 'Password', secret: true),
        ],
        if (challenge['kind'] == 'secret' || (challenge['secretMessage'] ?? '') != '')
          _field('secret', (challenge['secretMessage'] ?? 'Verification code').toString(), secret: challenge['echo'] != true),
      ],
      if (widget.kind == 'openconnect' ? json(challenge['form']).isNotEmpty : challenge['kind'] != 'open-url')
        Padding(padding: const EdgeInsets.all(16), child: FilledButton(onPressed: _busy ? null : _submit, child: Text(context.tr('Submit')))),
    ]);
  }
  Widget _field(String key, String label, {bool secret = false}) => Padding(padding: const EdgeInsets.all(16), child: TextField(
    controller: _controllers[key], obscureText: secret, autocorrect: false, enableSuggestions: false,
    decoration: InputDecoration(labelText: context.tr(label)),
  ));
}

class AuthBrowser extends StatefulWidget {
  const AuthBrowser({super.key, required this.endpoint, required this.challenge});
  final String endpoint, challenge;
  @override State<AuthBrowser> createState() => _AuthBrowserState();
}
class _AuthBrowserState extends State<AuthBrowser> {
  StreamSubscription<Json>? _subscription;
  @override void didChangeDependencies() {
    super.didChangeDependencies();
    _subscription ??= context.box.browserEvents.stream.listen((event) {
      if (event['id'] == widget.challenge && mounted) Navigator.pop(context, event['success'] == true);
    });
  }
  @override void dispose() { _subscription?.cancel(); super.dispose(); }
  @override Widget build(BuildContext context) => CommonScaffold(title: context.tr('Authentication'), body: Padding(
    padding: EdgeInsets.only(top: context.contentTopPadding, bottom: context.bottomInset),
    child: AndroidView(viewType: 'io.ericlee.bfa/openconnect',
      creationParams: {'endpoint': widget.endpoint, 'challenge': widget.challenge},
      creationParamsCodec: const StandardMessageCodec()),
  ));
}
