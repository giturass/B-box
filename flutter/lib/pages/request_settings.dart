import 'package:flutter/services.dart' show TextInputAction;

import '../widgets.dart';
import '../components/form.dart';
import 'configuration.dart';

class RequestSettingsPage extends StatefulWidget {
  const RequestSettingsPage({super.key, required this.initial, this.source = const {}});
  final Json initial;
  final Json source;
  @override State<RequestSettingsPage> createState() => _RequestSettingsPageState();
}

class _RequestSettingsPageState extends State<RequestSettingsPage> {
  late final _ports = TextEditingController(text: (widget.initial['ports'] as List? ?? [80, 443, 8080, 8443]).join(', '));
  late final _initialBodyLimit = (widget.initial['bodyLimit'] as num? ?? 65536).toInt();
  late final _bodyLimit = TextEditingController(text: (_initialBodyLimit > 0 ? _initialBodyLimit : 65536).toString());
  late bool _completeBodies = widget.initial['bodyLimit'] == -1;
  late List<String> _packages = (widget.initial['packages'] as List? ?? []).cast<String>().toList();
  late bool _extract = widget.initial['extractHosts'] == true;
  bool _busy = false, _dirty = false;
  String? _error;

  List<String> _lines(String value) => value.trim().split(RegExp(r'[\s,;]+')).where((item) => item.isNotEmpty).toSet().toList();

  Future<void> _save() async {
    if (_busy || context.box.remote) return;
    setState(() { _busy = true; _error = null; });
    try {
      final ports = _lines(_ports.text).map(int.tryParse).toList();
      if (ports.isEmpty || ports.any((port) => port == null || port < 1 || port > 65535)) throw const FormatException('Ports must be between 1 and 65535');
      final bodyLimit = _completeBodies ? -1 : int.tryParse(_bodyLimit.text.trim());
      if (bodyLimit == null || !_completeBodies && bodyLimit < 1) throw const FormatException('Body limit must be a positive number of bytes');
      await context.box.call('capture.configure', {'revision': widget.source['revision'], 'config': {
        'version': 3, 'packages': _packages, 'ports': ports,
        'bodyLimit': bodyLimit,
        'extractHosts': _extract,
      }});
      if (mounted) { setState(() { _dirty = false; _busy = false; }); Navigator.pop(context); }
    } catch (error) {
      if (mounted) setState(() { _error = error is FormatException ? context.tr(error.message) : BoxController.describe(error); _busy = false; });
    }
  }

  @override void dispose() { _ports.dispose(); _bodyLimit.dispose(); super.dispose(); }

  Widget _field(TextEditingController controller, String label, {String? footer, String? hint, bool multiline = true}) => generateSection(
    title: context.tr(label), footer: footer == null ? null : context.tr(footer),
    items: [DecorationListItem(title: TextField(
      key: ValueKey(label), controller: controller, minLines: multiline ? 2 : 1, maxLines: multiline ? 4 : 2,
      keyboardType: multiline ? TextInputType.multiline : TextInputType.text,
      textInputAction: multiline ? TextInputAction.newline : TextInputAction.done,
      autocorrect: false, enableSuggestions: false,
      style: AppTextStyle.body.of(context),
      decoration: InputDecoration.collapsed(border: const NoInputBorder(), hintText: hint),
      onChanged: (_) => setState(() => _dirty = true),
    ))],
  );

  @override Widget build(BuildContext context) {
    if (context.box.remote) return const BoxPage(title: 'Capture settings', children: [EmptyState('HTTP debugging is only available for the local service')]);
    return BoxEditGuard(dirty: _dirty, busy: _busy, child: FormPage(title: 'Capture settings', saving: _busy, error: _error, onSave: _save, children: [
      ListFooter(text: context.tr('Saved on this device. Capture stops when the local service restarts.')),
      generateSection(items: [ListItem.open(leading: const GlyphIcon(AppGlyphs.lock), title: const Text('MITM'),
        subtitle: Text(context.tr('HTTPS and TLS/TCP decryption use the selected profile.')), widget: const MitmPage())]),
      generateSection(items: [ListItem.toggle(title: Text(context.tr('Extract TLS/QUIC domains')), value: _extract,
        subtitle: Text(context.tr('Collect visible TLS/QUIC hostnames through the local service. These records contain names only.')),
        onChanged: (value) => setState(() { _extract = value; _dirty = true; })),
      ]),
      generateSection(title: context.tr('Filters and storage'), items: [
        ListItem(leading: const GlyphIcon(AppGlyphs.appsList), title: Text(context.tr('Capture applications')),
          subtitle: Text(_packages.isEmpty ? context.tr('All applications') : _packages.join(', ')), onTap: () async {
            final selected = await openPage<List<String>>(context, CaptureAppsPage(selected: _packages));
            if (selected != null && mounted) setState(() { _packages = selected; _dirty = true; });
          }),
        ListItem.toggle(title: Text(context.tr('Save complete bodies')), value: _completeBodies,
          onChanged: (value) => setState(() { _completeBodies = value; _dirty = true; })),
      ]),
      if (!_completeBodies) _field(_bodyLimit, 'Body limit per message (bytes)', multiline: false),
      _field(_ports, 'Capture ports', hint: '80, 443, 8080, 8443', multiline: false,
        footer: 'Records stay until you clear them. Body limits affect saved content only. Port filters use the connection destination.'),
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
  @override Widget build(BuildContext context) => Scaffold(title: context.tr('Capture applications'),
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
            Padding(padding: const EdgeInsets.all(12), child: Text(context.tr('An empty selection captures all applications routed through the local service. Unknown owners do not match a selected application. Applications sharing a UID share the same filter.'))),
            if (snapshot.hasError) ErrorState(snapshot.error!, retry: () => setState(() => _apps = context.box.call('apps.list', {'system': true, 'refresh': true}).then(rows))),
            if (!snapshot.hasData && !snapshot.hasError) const Center(child: ActivityIndicator()),
          ]);
          final app = visible[index - 1], package = app['package'].toString();
          return ListItem.checkbox(title: Text(app['name']?.toString() ?? package), subtitle: Text(package), value: _selected.contains(package),
            onChanged: (_) => setState(() { if (!_selected.add(package)) _selected.remove(package); }));
        });
    }));
}
