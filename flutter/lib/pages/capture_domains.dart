import 'package:flutter/services.dart';
import '../widgets.dart';

class CaptureDomainsPage extends StatefulWidget {
  const CaptureDomainsPage({super.key, this.query = '', this.select = false});
  final String query;
  final bool select;
  @override State<CaptureDomainsPage> createState() => _CaptureDomainsPageState();
}

class _CaptureDomainsPageState extends State<CaptureDomainsPage> {
  Future<List<String>>? _domains;
  final _selected = <String>{};
  bool _busy = false;

  Future<List<String>> _load() => context.box.call('capture.domains', {'query': widget.query})
    .then((value) => (value as List? ?? []).cast<String>());

  @override void didChangeDependencies() {
    super.didChangeDependencies();
    if (!context.box.remote) _domains ??= _load();
  }

  Future<void> _copy(String text) async {
    await Clipboard.setData(ClipboardData(text: text));
    if (mounted) context.box.messages.add(context.tr('Copied'));
  }

  @override Widget build(BuildContext context) {
    if (context.box.remote) return const BoxPage(title: 'Extract domains', children: [EmptyState('HTTP debugging is only available for the local service')]);
    return FutureBuilder<List<String>>(future: _domains, builder: (context, snapshot) {
      final domains = snapshot.data ?? [];
      return CommonScaffold(title: context.tr('Extract domains'), isLoading: _busy,
        confirmationAction: widget.select ? IconButtonData(glyph: AppGlyphs.check, tooltip: context.tr('Add domains'),
          onPressed: _selected.isEmpty ? null : () => Navigator.pop(context, _selected.toList()..sort())) : null,
        iconActions: [
          IconButtonData(glyph: AppGlyphs.copy, tooltip: context.tr('Copy domains'), onPressed: domains.isEmpty ? null : () => _copy(domains.join('\n'))),
          if (!widget.select) IconButtonData(glyph: AppGlyphs.arrowDown, tooltip: context.tr('Export domains'), onPressed: domains.isEmpty || _busy ? null : () async {
            setState(() => _busy = true);
            await context.box.act('capture.export', {'format': 'domains', 'query': widget.query});
            if (mounted) setState(() => _busy = false);
          }),
          IconButtonData(glyph: AppGlyphs.refresh, tooltip: context.tr('Refresh'), onPressed: () => setState(() => _domains = _load())),
        ],
        body: ListView.builder(padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 24 + context.bottomInset),
          itemCount: domains.length + 1, itemBuilder: (context, index) {
            if (index == 0) return Column(children: [
              ListFooter(text: context.tr('Domains are extracted from retained requests and visible TLS/QUIC names, using the current search filter. Encrypted hostnames cannot be recovered.')),
              if (snapshot.hasError) ErrorState(snapshot.error!, retry: () => setState(() => _domains = _load())),
              if (!snapshot.hasData && !snapshot.hasError) const Center(child: CircularProgressIndicator()),
              if (snapshot.hasData && domains.isEmpty) const EmptyState('No captured domains'),
            ]);
            final host = domains[index - 1];
            return widget.select ? ListItem.checkbox(title: Text(host), value: _selected.contains(host),
              onChanged: (_) => setState(() { if (!_selected.add(host)) _selected.remove(host); }))
              : ListItem(title: Text(host), onTap: () => _copy(host));
          },
        ),
      );
    });
  }
}
