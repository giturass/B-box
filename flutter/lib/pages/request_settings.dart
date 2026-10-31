import 'package:flutter/services.dart';
import '../widgets.dart';
import '../components/form.dart';

class RequestSettingsPage extends StatefulWidget {
  const RequestSettingsPage({super.key, required this.initial});
  final Json initial;
  @override State<RequestSettingsPage> createState() => _RequestSettingsPageState();
}

class _RequestSettingsPageState extends State<RequestSettingsPage> {
  late final _decrypt = TextEditingController(text: (widget.initial['decryptHosts'] as List? ?? []).join('\n'));
  late final _include = TextEditingController(text: (widget.initial['includeHosts'] as List? ?? []).join('\n'));
  late final _exclude = TextEditingController(text: (widget.initial['excludeHosts'] as List? ?? []).join('\n'));
  late final _ports = TextEditingController(text: (widget.initial['ports'] as List? ?? [80, 443, 8080, 8443]).join(', '));
  late bool _https = widget.initial['https'] == true, _quic = widget.initial['blockQUIC'] == true;
  late int _bodyKiB = ((widget.initial['bodyLimit'] as num? ?? 65536) / 1024).round();
  late List<String> _packages = (widget.initial['packages'] as List? ?? []).cast<String>().toList();
  bool _busy = false, _dirty = false;
  String? _error;

  List<String> _lines(String value) => value.trim().split(RegExp(r'[\s,;]+')).where((item) => item.isNotEmpty).toSet().toList();

  Future<void> _save() async {
    if (_busy || context.box.remote) return;
    setState(() { _busy = true; _error = null; });
    try {
      final ports = _lines(_ports.text).map(int.parse).toList();
      if (ports.isEmpty || ports.any((port) => port < 1 || port > 65535)) throw const FormatException('Ports must be between 1 and 65535');
      await context.box.call('capture.configure', {'config': {
        'https': _https, 'decryptHosts': _lines(_decrypt.text), 'includeHosts': _lines(_include.text),
        'excludeHosts': _lines(_exclude.text), 'packages': _packages, 'ports': ports,
        'bodyLimit': _bodyKiB * 1024, 'blockQUIC': _quic,
      }});
      if (mounted) { setState(() { _dirty = false; _busy = false; }); Navigator.pop(context); }
    } catch (error) {
      if (mounted) setState(() { _error = error is FormatException ? context.tr('Ports must be between 1 and 65535') : BoxController.describe(error); _busy = false; });
    }
  }

  @override void dispose() { for (final c in [_decrypt, _include, _exclude, _ports]) { c.dispose(); } super.dispose(); }

  Widget _field(TextEditingController controller, String label, {String? footer, String? hint, bool multiline = true}) => generateSection(
    title: context.tr(label), footer: footer == null ? null : context.tr(footer),
    items: [DecorationListItem(title: TextField(
      key: ValueKey(label), controller: controller, minLines: multiline ? 2 : 1, maxLines: multiline ? 4 : 2,
      keyboardType: multiline ? TextInputType.multiline : TextInputType.text,
      textInputAction: multiline ? TextInputAction.newline : TextInputAction.done,
      inputFormatters: [LengthLimitingTextInputFormatter(65536)], autocorrect: false, enableSuggestions: false,
      style: AppTextStyle.body.of(context),
      decoration: InputDecoration.collapsed(border: const NoInputBorder(), hintText: hint),
      onChanged: (_) => setState(() => _dirty = true),
    ))],
  );

  @override Widget build(BuildContext context) {
    if (context.box.remote) return const BoxPage(title: 'Capture settings', children: [EmptyState('HTTP debugging is only available for the local service')]);
    return BoxEditGuard(dirty: _dirty, busy: _busy, child: FormPage(title: 'Capture settings', saving: _busy, error: _error, onSave: _save, children: [
      generateSection(title: 'HTTPS', items: [
        ListItem.toggle(title: Text(context.tr('Decrypt HTTPS')), value: _https, onChanged: (value) => setState(() { _https = value; _dirty = true; })),
      ]),
      if (_https) ...[
        _field(_decrypt, 'HTTPS domains', hint: '*.example.com',
          footer: 'Leave empty to decrypt all domains. Use exact names or *.example.com; bypass rules take priority.'),
        ListFooter(text: context.tr('Install the debugging CA on the target device. Apps with certificate pinning or their own trust store may require bypass.')),
        generateSection(items: [
          ListItem.toggle(title: Text(context.tr('Block selected QUIC connections')), value: _quic,
            subtitle: Text(context.tr('Try HTTP/2 or HTTP/1.1 fallback for matching HTTPS domains. Unknown QUIC domains are passed through.')),
            onChanged: (value) => setState(() { _quic = value; _dirty = true; })),
        ]),
      ],
      _field(_include, 'Capture domains', hint: '*.example.com', footer: 'Leave empty to capture all domains. Applies to both HTTP and HTTPS.'),
      _field(_exclude, 'Bypass domains', hint: 'private.example.com', footer: 'Matching domains pass through without capture or HTTPS decryption.'),
      generateSection(title: context.tr('Filters and storage'), items: [
        ListItem(leading: const GlyphIcon(AppGlyphs.appsList), title: Text(context.tr('Capture applications')),
          subtitle: Text(_packages.isEmpty ? context.tr('All applications') : _packages.join(', ')), onTap: () async {
            final selected = await openPage<List<String>>(context, CaptureAppsPage(selected: _packages));
            if (selected != null && mounted) setState(() { _packages = selected; _dirty = true; });
          }),
        ListItem.options(title: Text(context.tr('Body limit per message')), options: const [64, 128, 256, 1024], value: _bodyKiB,
          textBuilder: (value) => '$value KiB', onChanged: (value) => setState(() { _bodyKiB = value; _dirty = true; })),
      ]),
      _field(_ports, 'Capture ports', hint: '80, 443, 8080, 8443', multiline: false,
        footer: 'Keeps up to 300 requests and 32 MiB. Large bodies keep a prefix; forwarding continues in full. Capture stops on service restart.'),
    ]));
  }
}

class CaptureAppsPage extends StatefulWidget {
  const CaptureAppsPage({super.key, required this.selected});
  final List<String> selected;
  @override State<CaptureAppsPage> createState() => _CaptureAppsPageState();
}

class _CaptureAppsPageState extends State<CaptureAppsPage> {
  late final _selected = widget.selected.toSet();
  Future<List<Json>>? _apps;
  String _query = '';
  @override void didChangeDependencies() {
    super.didChangeDependencies();
    _apps ??= context.box.call('apps.list', {'system': true, 'refresh': true}).then(rows);
  }
  @override Widget build(BuildContext context) => CommonScaffold(title: context.tr('Capture applications'),
    searchState: AppBarSearchState(onSearch: (query) => setState(() => _query = query.toLowerCase())),
    confirmationAction: IconButtonData(glyph: AppGlyphs.check, tooltip: context.tr('Done'), onPressed: () => Navigator.pop(context, _selected.toList()..sort())),
    iconActions: [IconButtonData(glyph: AppGlyphs.clearAll, tooltip: context.tr('All applications'), onPressed: () => setState(_selected.clear))],
    body: FutureBuilder<List<Json>>(future: _apps, builder: (context, snapshot) {
      final apps = [...?snapshot.data];
      final installed = apps.map((app) => app['package']).toSet();
      for (final package in _selected.difference(installed.cast<String>())) { apps.add({'name': package, 'package': package}); }
      final visible = apps.where((app) => '${app['name']} ${app['package']}'.toLowerCase().contains(_query)).toList();
      return ListView.builder(padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 24 + context.bottomInset),
        itemCount: visible.length + 1, itemBuilder: (context, index) {
          if (index == 0) return Column(children: [
            Padding(padding: const EdgeInsets.all(12), child: Text(context.tr('An empty selection captures all applications. Unknown owners do not match a selection.'))),
            if (snapshot.hasError) ErrorState(snapshot.error!, retry: () => setState(() => _apps = context.box.call('apps.list', {'system': true, 'refresh': true}).then(rows))),
            if (!snapshot.hasData && !snapshot.hasError) const Center(child: CircularProgressIndicator()),
          ]);
          final app = visible[index - 1], package = app['package'].toString();
          return ListItem.checkbox(title: Text(app['name']?.toString() ?? package), subtitle: Text(package), value: _selected.contains(package),
            onChanged: (_) => setState(() { if (!_selected.add(package)) _selected.remove(package); }));
        });
    }));
}
