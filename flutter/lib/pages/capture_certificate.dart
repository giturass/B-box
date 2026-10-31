import '../widgets.dart';

class CaptureCertificatePage extends StatefulWidget {
  const CaptureCertificatePage({super.key, required this.initial, required this.active});
  final Json initial;
  final bool active;
  @override State<CaptureCertificatePage> createState() => _CaptureCertificatePageState();
}

class _CaptureCertificatePageState extends State<CaptureCertificatePage> {
  late Json _ca = widget.initial;
  late bool _active = widget.active;
  bool _busy = false;
  String? _error;

  Future<void> _act(String action) async {
    if (_busy) return;
    setState(() { _busy = true; _error = null; });
    try {
      await context.box.call('capture.ca.$action');
      if (!mounted) return;
      final snapshot = json(await context.box.call('capture.ca.snapshot'));
      if (mounted) setState(() { _ca = json(snapshot['ca']); _active = json(snapshot['state'])['active'] == true; });
    } catch (error) {
      if (mounted) setState(() => _error = BoxController.describe(error));
    } finally { if (mounted) setState(() => _busy = false); }
  }

  @override Widget build(BuildContext context) {
    if (context.box.remote) return const BoxPage(title: 'CA certificate', children: [EmptyState('HTTP debugging is only available for the local service')]);
    final exists = _ca['exists'] == true, configured = (_ca['filename'] as String? ?? '').isNotEmpty;
    return BoxPage(title: 'CA certificate', loading: _busy, children: [
      if (_error != null) Text(_error!, style: TextStyle(color: context.colorScheme.error)),
      if (exists) DetailRows({
        'SHA-256': _ca['fingerprint'], 'Expires': localDate(_ca['expiresAt']),
        'CertBridge import': context.tr(configured ? 'Imported' : 'Not imported'),
        'System trust in this process': context.tr((_ca['checkedAt'] as num? ?? 0) == 0 ? 'Not checked' : _ca['trusted'] == true ? 'Trusted' : 'Not trusted'),
        if ((_ca['checkedAt'] as num? ?? 0) > 0) 'Last checked': localDate(_ca['checkedAt']),
      }),
      Padding(padding: const EdgeInsets.all(12), child: Text(context.tr('CertBridge imports only the public CA. Reboot after importing or removing it, then check system trust.'))),
      if (_ca['pendingReboot'] == true) Padding(padding: const EdgeInsets.all(12), child: Text(context.tr('Certificate change pending; reboot and verify again.'))),
      if (_active) Padding(padding: const EdgeInsets.all(12), child: Text(context.tr('Stop capture before changing its certificate'))),
      generateSection(items: [
        if (!exists) ListItem(title: Text(context.tr('Generate CA')), onTap: _busy || _active ? null : () => _act('generate')),
        if (exists) ...[
          ListItem(title: Text(context.tr('Export public CA')), onTap: _busy ? null : () => _act('export')),
          ListItem(title: Text(context.tr('Import with CertBridge')), subtitle: Text(context.tr('Root permission required')), onTap: _busy ? null : () => _act('install')),
          ListItem(title: Text(context.tr('Check system trust')), onTap: _busy ? null : () => _act('status')),
          if (configured) ListItem(title: Text(context.tr('Remove from CertBridge')), onTap: _busy || _active ? null : () async {
            if (await confirm(context, 'Remove the debugging CA from CertBridge?') && mounted) await _act('remove');
          }),
          ListItem(title: Text(context.tr('Rotate CA')), onTap: _busy || _active ? null : () async {
            if (await confirm(context, 'Replace the debugging CA?', message: 'The old CertBridge import is removed first. Install the new CA and reboot.', action: 'Replace') && mounted) await _act('rotate');
          }),
        ],
        ListItem(title: Text(context.tr('CertBridge documentation')), onTap: () => context.box.act('url.open', {'url': 'https://github.com/Eikeitsu/CertBridge'})),
      ]),
      if ((_ca['backendOutput'] as String? ?? '').isNotEmpty) Padding(padding: const EdgeInsets.all(12),
        child: SelectableText(_ca['backendOutput'].toString(), style: const TextStyle(fontFamily: 'JetBrainsMono', fontSize: 12))),
    ]);
  }
}
