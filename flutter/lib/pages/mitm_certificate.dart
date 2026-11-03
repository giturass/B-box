import '../widgets.dart';
import '../components/record.dart';

class MitmCertificatePage extends StatefulWidget {
  const MitmCertificatePage({super.key});
  @override State<MitmCertificatePage> createState() => _MitmCertificatePageState();
}

class _MitmCertificatePageState extends State<MitmCertificatePage> with WidgetsBindingObserver {
  Json? _state;
  Object? _error;
  bool _busy = false;
  bool? _remote;
  int _epoch = 0;

  @override void initState() { super.initState(); WidgetsBinding.instance.addObserver(this); }
  @override void dispose() { WidgetsBinding.instance.removeObserver(this); super.dispose(); }
  @override void didChangeDependencies() {
    super.didChangeDependencies();
    final remote = context.box.remote;
    if (_remote != remote) {
      _remote = remote;
      _epoch++;
      _state = null;
      _error = null;
      _busy = false;
      if (!remote) _refresh();
    }
  }
  @override void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed && mounted && !_busy) _refresh();
  }

  Future<void> _refresh() async {
    if (_busy || context.box.remote) return;
    final epoch = _epoch;
    setState(() { _busy = true; _error = null; });
    try {
      final state = json(await context.box.call('capture.ca.snapshot'));
      if (mounted && epoch == _epoch && !context.box.remote) setState(() => _state = state);
    } catch (error) { if (mounted && epoch == _epoch) setState(() => _error = error); }
    finally { if (mounted && epoch == _epoch) setState(() => _busy = false); }
  }

  Future<void> _run(String method) async {
    if (_busy || context.box.remote || _state == null) return;
    final epoch = _epoch;
    final revision = _state!['revision'];
    if (method == 'capture.ca.generate' && _state!['exists'] == true) {
      if (!await confirm(context, 'Regenerate the MITM CA? Replace the old certificate in Android and target applications afterwards.') || !mounted) return;
    }
    if (epoch != _epoch || context.box.remote || _busy) return;
    setState(() { _busy = true; _error = null; });
    try {
      final result = await context.box.call(method, {'revision': revision});
      if (!mounted || epoch != _epoch || context.box.remote) return;
      if (method == 'capture.ca.generate') {
        setState(() => _state = json(result));
      } else if (result == true) {
        context.box.messages.add(context.tr(switch (method) {
          'capture.ca.install' => 'Certificate settings opened. Refresh trust after installation.',
          'capture.ca.module' => 'CA module exported. Install it with your root manager, reboot, then refresh trust.',
          _ => 'Public CA certificate exported',
        }));
      }
    } catch (error) { if (mounted && epoch == _epoch) setState(() => _error = error); }
    finally { if (mounted && epoch == _epoch) setState(() => _busy = false); }
  }

  @override Widget build(BuildContext context) {
    if (context.box.remote) return const BoxPage(title: 'MITM certificate', children: [EmptyState('HTTP debugging is only available for the local service')]);
    final exists = _state?['exists'] == true;
    final usable = exists && _state?['valid'] == true && _state?['keyAvailable'] == true;
    final trust = _state?['systemTrusted'] == true ? 'Present in system CA store'
      : _state?['userTrusted'] == true ? 'Present in user CA store' : 'CA not installed';
    return BoxPage(title: 'MITM certificate', loading: _busy, actions: [
      IconButtonData(glyph: AppGlyphs.refresh, tooltip: context.tr('Refresh trust'), onPressed: _busy ? null : _refresh),
    ], children: [
      ListFooter(text: context.tr('HTTPS decryption requires the target app to trust this CA. Apps with certificate pinning or their own trust store may reject it; remove those domains from the MITM domain list.')),
      if (_error != null) ErrorState(_error!, retry: _refresh),
      if (_state != null) generateSection(title: context.tr('Certificate'), items: [
        ListItem(title: Text(context.tr(exists ? trust : 'No MITM certificate')),
          subtitle: Text(context.tr('Trust status describes this app process. It does not prove that every target app trusts the CA.'))),
        if (exists) ...[
          DetailRow.text(title: context.tr('Subject'), value: _state!['subject']?.toString() ?? '—', stacked: true),
          DetailRow.text(title: 'SHA-256', value: _state!['fingerprint']?.toString() ?? '—', stacked: true),
          DetailRow.text(title: context.tr('Expires'), value: localDate(_state!['expiresAt'])),
          if (!usable) ListItem(title: Text(context.tr('Certificate expired or signing key unavailable. Regenerate the CA.'))),
        ],
        ListItem(key: const ValueKey('mitm-ca-generate'), title: Text(context.tr(exists ? 'Regenerate CA' : 'Generate CA')),
          subtitle: _state!['captureActive'] == true || _state!['mitmActive'] == true ? Text(context.tr('Stop capture and the local service before changing the MITM certificate')) : null,
          onTap: _busy || _state!['captureActive'] == true || _state!['mitmActive'] == true ? null : () => _run('capture.ca.generate')),
      ]),
      if (exists) generateSection(title: context.tr('Install and export'), footer: context.tr('On Android 11 or later, export the certificate first, then choose CA certificate in Android security settings. For system trust, install the exported module with Magisk, KernelSU or APatch and reboot.'), items: [
        ListItem(key: const ValueKey('mitm-ca-export'), title: Text(context.tr('Export public CA certificate')),
          onTap: _busy || !usable ? null : () => _run('capture.ca.export')),
        ListItem(key: const ValueKey('mitm-ca-install'), title: Text(context.tr('Open certificate settings')),
          onTap: _busy || !usable ? null : () => _run('capture.ca.install')),
        ListItem(key: const ValueKey('mitm-ca-module'), title: Text(context.tr('Export system CA module')),
          onTap: _busy || !usable ? null : () => _run('capture.ca.module')),
      ]),
      ListFooter(text: context.tr('The private signing key stays on this device. Exports contain only the public CA. Regeneration requires replacing installed certificates; remove system modules in the root manager and reboot.')),
    ]);
  }
}
