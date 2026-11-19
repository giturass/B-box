import '../widgets.dart';

/// Download state and installer permission belong to the Android updater.
class AppUpdateDialog extends StatefulWidget {
  const AppUpdateDialog({super.key, this.release = const {}});
  final Json release;
  @override State<AppUpdateDialog> createState() => _AppUpdateDialogState();
}

class _AppUpdateDialogState extends State<AppUpdateDialog> {
  bool _busy = false;
  String? _error;

  Future<void> _run(Future<void> Function() operation) async {
    if (_busy) return;
    setState(() { _busy = true; _error = null; });
    try { await operation(); }
    catch (error) {
      if (mounted) setState(() => _error = BoxController.describe(error));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override Widget build(BuildContext context) {
    final box = BoxScope.of(context, aspect: BoxAspect.updates);
    final state = box.appUpdate;
    final release = widget.release.isEmpty ? state : widget.release;
    final available = release['available'] == true;
    final nativeStatus = state['status'] as String? ?? 'idle';
    final active = nativeStatus != 'idle' && (nativeStatus != 'cancelled' || widget.release.isEmpty
        || available && release['versionCode'] == state['versionCode']);
    final status = active ? nativeStatus : 'idle';
    final incomplete = release['complete'] == false;
    final version = (active ? state['version'] : release['version']) as String? ?? '';
    final versionCode = ((active ? state['versionCode'] : release['versionCode']) as num?)?.toInt();
    final canInstall = active && state['canInstall'] == true;
    final hasVersionCode = versionCode != null && versionCode > 0;
    final canDownload = available && release['canDownload'] == true && hasVersionCode;
    final retryDownload = ['error', 'cancelled'].contains(status) && !canInstall && hasVersionCode;
    final downloading = status == 'downloading' || status == 'verifying';
    final downloaded = state['downloaded'] as num? ?? 0;
    final total = state['total'] as num? ?? 0;
    final notes = release['notes'] as String? ?? '';
    final error = _error ?? (active ? state['error'] as String? : null);
    final hasCheck = release.containsKey('hasMetadata') || release.containsKey('complete');
    final sameRelease = !active || release['versionCode'] == state['versionCode'];
    final title = switch (status) {
      'downloading' => 'Downloading update',
      'verifying' => 'Verifying update',
      'ready' || 'permissionRequired' || 'installing' => 'Install update',
      'error' => 'Update failed',
      'cancelled' => 'Download cancelled',
      _ => available ? 'Update available' : 'Check for updates',
    };
    return AlertDialog(
      title: context.tr(title),
      child: Column(mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.start, spacing: 12, children: [
        if (version.isNotEmpty && (available || active)) Text('B-box ${version.startsWith('v') ? version : 'v$version'}'),
        if (!active && !available) Text(context.tr(!hasCheck ? 'Check for updates again to download an update.'
            : incomplete ? 'Update check incomplete'
            : release['hasMetadata'] == false ? 'No release metadata available' : 'You are up to date')),
        if (incomplete && sameRelease) Text(context.tr('Some releases could not be checked. Please try again.')),
        if (available && !active && !canDownload) Text(context.tr('In-app update is unavailable for this release.')),
        if (downloading) ...[
          Text(context.tr(status == 'verifying' ? 'Verifying downloaded APK' : 'Downloading update')),
          LinearProgressIndicator(value: status == 'verifying' || total <= 0 ? null : (downloaded / total).clamp(0, 1).toDouble()),
          Text(total > 0 ? '${bytes(downloaded)} / ${bytes(total)}' : bytes(downloaded)),
          Text(context.tr('You can close this dialog while the download continues.')),
        ],
        if (status == 'ready') Text(context.tr('Update is ready to install.')),
        if (status == 'permissionRequired') Text(context.tr('Allow B-box to install apps in Android settings, then return here to install the update.')),
        if (status == 'installing') Text(context.tr('Complete the update in the Android installer.')),
        if (status == 'cancelled') Text(context.tr('Download cancelled')),
        if (error != null && error.isNotEmpty) SelectableText(context.tr(error)),
        if (available && notes.isNotEmpty && sameRelease) SelectableText(notes),
      ]),
      actions: [
        TextButton(onPressed: () => Navigator.pop(context), child: Text(context.tr('Close'))),
        if (downloading || canInstall && status != 'installing' || status == 'error') TextButton(
          onPressed: _busy ? null : () => _run(box.cancelUpdate),
          child: Text(context.tr(downloading ? 'Cancel download' : 'Discard update'))),
        if (canInstall && status != 'installing' && hasVersionCode) TextButton(
          onPressed: _busy ? null : () => _run(() => box.installUpdate(versionCode!)),
          child: Text(context.tr(status == 'ready' && _error == null ? 'Install' : 'Retry install'))),
        if (!downloading && !canInstall && status != 'installing' && (canDownload || retryDownload)) TextButton(
          onPressed: _busy ? null : () => _run(() => box.downloadUpdate(versionCode!)),
          child: Text(context.tr(status == 'idle' && _error == null ? 'Download' : 'Retry download'))),
        if (!active && (!canDownload && available || !available && (release['hasMetadata'] == false || incomplete))) TextButton(
          onPressed: () {
            Navigator.pop(context);
            box.act('url.open', {'url': available ? release['url'] ?? 'https://github.com/giturass/B-box/releases'
                : 'https://github.com/giturass/B-box/releases'});
          }, child: Text(context.tr('Releases'))),
      ],
    );
  }
}
