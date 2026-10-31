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
  String? _notice;

  Future<void> _act(String action) async {
    if (_busy) return;
    setState(() { _busy = true; _error = null; _notice = null; });
    try {
      final result = await context.box.call('capture.ca.$action');
      if (mounted && result == true) {
        if (action == 'module') setState(() => _notice = 'Module ZIP saved. Install it in Magisk or KernelSU, then reboot.');
        if (action == 'export') setState(() => _notice = 'System CA saved.');
      }
    } catch (error) {
      if (mounted) setState(() => _error = BoxController.describe(error));
    }
    if (!mounted) return;
    // Preserve the action error while refreshing the last saved CA/trust state.
    try {
      final snapshot = json(await context.box.call('capture.ca.snapshot'));
      if (mounted) setState(() { _ca = json(snapshot['ca']); _active = json(snapshot['state'])['active'] == true; });
    } catch (error) {
      if (mounted) setState(() => _error ??= BoxController.describe(error));
    } finally { if (mounted) setState(() => _busy = false); }
  }

  @override Widget build(BuildContext context) {
    if (context.box.remote) return const BoxPage(title: 'CA certificate', children: [EmptyState('HTTP debugging is only available for the local service')]);
    final exists = _ca['exists'] == true;
    return BoxPage(title: 'CA certificate', loading: _busy, children: [
      if (_error != null) Text(_error!, style: TextStyle(color: context.colorScheme.error)),
      if (_notice != null) Padding(padding: const EdgeInsets.all(12), child: Text(context.tr(_notice!))),
      if (exists) DetailRows({
        'SHA-256': _ca['fingerprint'], 'Expires': localDate(_ca['expiresAt']),
        'System CA filename': _ca['systemFilename'],
        'System trust in this process': context.tr((_ca['checkedAt'] as num? ?? 0) == 0 ? 'Not checked' : _ca['trusted'] == true ? 'Trusted' : 'Not trusted'),
        if ((_ca['checkedAt'] as num? ?? 0) > 0) 'Last checked': localDate(_ca['checkedAt']),
        if ((_ca['moduleExportedAt'] as num? ?? 0) > 0) 'Last module export': localDate(_ca['moduleExportedAt']),
      }),
      Padding(padding: const EdgeInsets.all(12), child: Text(context.tr('The module contains this installation\'s public CA. Install it in Magisk or KernelSU, reboot, then check system trust.'))),
      if (_active) Padding(padding: const EdgeInsets.all(12), child: Text(context.tr('Stop capture before changing its certificate'))),
      generateSection(items: [
        if (!exists) ListItem(title: Text(context.tr('Generate CA')), onTap: _busy || _active ? null : () => _act('generate')),
        if (exists) ...[
          ListItem(title: Text(context.tr('Download module ZIP')), subtitle: const Text('Magisk / KernelSU'), onTap: _busy ? null : () => _act('module')),
          ListItem(title: Text(context.tr('Export system CA (.0)')), onTap: _busy ? null : () => _act('export')),
          ListItem(title: Text(context.tr('Check system trust')), onTap: _busy ? null : () => _act('status')),
          ListItem(title: Text(context.tr('Rotate CA')), onTap: _busy || _active ? null : () async {
            if (await confirm(context, 'Replace the debugging CA?', message: 'Download and install the new module, then reboot. Remove old CA copies installed through other modules separately.', action: 'Replace') && mounted) await _act('rotate');
          }),
        ],
        ListItem(title: Text(context.tr('ProxyPin project')), onTap: () => context.box.act('url.open', {'url': 'https://github.com/wanghongenpin/proxypin'})),
      ]),
      ListFooter(text: context.tr('To remove system trust, uninstall B-box HTTP Debug CA in Magisk or KernelSU and reboot.')),
    ]);
  }
}
