import '../widgets.dart';
import 'endpoints.dart';

class InboxPage extends StatefulWidget {
  const InboxPage({super.key, required this.endpoint});
  final String endpoint;
  @override State<InboxPage> createState() => _InboxPageState();
}
class _InboxPageState extends State<InboxPage> {
  BoxController? _box;
  @override void didChangeDependencies() {
    super.didChangeDependencies();
    if (_box != null) return;
    _box = context.box;
    _box!.act('taildrop.subscribe', {'endpoint': widget.endpoint});
  }
  @override void dispose() { _box?.act('taildrop.unsubscribe'); super.dispose(); }
  @override Widget build(BuildContext context) {
    final box = context.box, state = context.box.inbox;
    return BoxPage(title: 'Received files', children: [
      if (rows(state['files']).isEmpty && rows(state['receiving']).isEmpty) const EmptyState('No received files'),
      for (final file in rows(state['receiving'])) ListItem(title: Text(file['name'].toString()),
        subtitle: Text('${bytes(file['receivedBytes'] as num? ?? 0)} / ${bytes(file['size'] as num? ?? 0)}'),
        trailing: IconButton(tooltip: context.tr('Cancel'), icon: const GlyphIcon(AppGlyphs.close),
          onPressed: () => box.act('taildrop.cancelReceive', {'endpoint': widget.endpoint, 'name': file['name'], 'sender': file['senderID']}))),
      for (final file in rows(state['files'])) ListItem(title: Text(file['name'].toString()),
        subtitle: Text('${bytes(file['size'] as num? ?? 0)} · ${file['senderName']}'),
        onTap: () => box.act('taildrop.save', {'endpoint': widget.endpoint, 'name': file['name']}),
        trailing: IconButton(tooltip: context.tr('Delete'), icon: const GlyphIcon(AppGlyphs.delete), onPressed: () async {
          if (await confirm(context, 'Delete', message: file['name'].toString())) await box.act('taildrop.delete', {'endpoint': widget.endpoint, 'name': file['name']});
        })),
    ]);
  }
}

class TransfersPage extends StatelessWidget {
  const TransfersPage({super.key});
  @override Widget build(BuildContext context) => BoxPage(title: 'Transfers', children: [
    if (context.box.transfers.isEmpty) const EmptyState('No transfers'),
    for (final transfer in context.box.transfers) generateSection(title: transfer['peerName'].toString(), items: [
      for (final file in rows(transfer['files'])) ListItem(title: Text(file['name'].toString()), subtitle: Column(
        crossAxisAlignment: CrossAxisAlignment.start, children: [
          Text('${bytes(file['sentBytes'] as num? ?? 0)} / ${bytes(file['size'] as num? ?? 0)}'),
          LinearProgressIndicator(value: file['completed'] == true ? 1 : (file['size'] as num? ?? 0) > 0
            ? ((file['sentBytes'] as num? ?? 0) / (file['size'] as num)).clamp(0, 1).toDouble() : null),
        ])),
      if (transfer['error'] != null) ListItem(title: Text(context.tr('Error')), subtitle: SelectableText(transfer['error'].toString())),
      ListItem(title: Text(context.tr(transfer['finished'] == true ? 'Done' : 'Cancel')),
        onTap: () => context.box.act(transfer['finished'] == true ? 'taildrop.dismissSend' : 'taildrop.cancelSend', {'id': transfer['id']})),
    ]),
  ]);
}

/// Android ACTION_SEND content stays owned by the native host until sent or discarded.
class SharePage extends StatefulWidget {
  const SharePage({super.key, required this.token});
  final String token;
  @override State<SharePage> createState() => _SharePageState();
}
class _SharePageState extends State<SharePage> {
  BoxController? _box;
  bool _sending = false, _sent = false;
  @override void didChangeDependencies() { super.didChangeDependencies(); _box ??= context.box; }
  @override void dispose() { if (!_sent) _box?.act('share.discard', {'token': widget.token}); super.dispose(); }
  @override Widget build(BuildContext context) {
    final box = context.box;
    final endpoints = rows(box.toolState['tailscale']).where((e) => e['canShareFiles'] == true).toList();
    return BoxPage(title: 'Send with Taildrop', loading: _sending, children: [
      if (!box.running) EmptyState('Service not started', action: FilledButton(
        onPressed: () => box.act('service.start'), child: Text(context.tr('Start')))),
      if (box.running && endpoints.isEmpty) const EmptyState('No recipients'),
      for (final endpoint in endpoints) generateSection(title: endpoint['endpointTag'].toString(), items: [
        for (final peer in endpointPeers(endpoint).where((p) => p['canReceiveFiles'] == true && p['stableID'] != json(endpoint['selfPeer'])['stableID']))
          ListItem(title: Text(peerName(peer)), subtitle: Text(context.tr(peer['online'] == true ? 'Online' : 'Offline')),
            onTap: _sending || peer['online'] != true ? null : () async {
              setState(() => _sending = true);
              final ok = await box.act('share.send', {'token': widget.token, 'endpoint': endpoint['endpointTag'], 'peer': peer['id']});
              if (!context.mounted) return;
              setState(() { _sending = false; _sent = ok; });
              if (ok) { Navigator.pop(context); await openPage(context, const TransfersPage()); }
            }),
      ]),
    ]);
  }
}
