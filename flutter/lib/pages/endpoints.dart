import '../widgets.dart';
import 'authentication.dart';
import 'taildrop.dart';
import 'terminal.dart';

String endpointName(String kind) => const {
  'tailscale': 'Tailscale', 'openconnect': 'OpenConnect',
  'openvpn': 'OpenVPN', 'usbip': 'USB/IP',
}[kind] ?? kind;

String peerName(Json peer) {
  final dns = peer['dnsName'] as String? ?? '';
  return dns.isNotEmpty ? dns.replaceFirst(RegExp(r'\.$'), '') : (peer['hostName'] ?? peer['id'] ?? '').toString();
}

List<Json> endpointPeers(Json endpoint) => rows(endpoint['userGroups']).expand((group) => rows(group['peers'])).toList();

class EndpointsPage extends StatelessWidget {
  const EndpointsPage({super.key, required this.kind});
  final String kind;
  @override Widget build(BuildContext context) {
    final endpoints = rows(context.box.toolState[kind]);
    return BoxPage(title: endpointName(kind), children: [
      if (endpoints.isEmpty) const EmptyState('No endpoints'),
      generateGroup([for (final endpoint in endpoints) ListItem.open(
        leading: const GlyphIcon(AppGlyphs.devices),
        title: Text((endpoint['endpointTag'] ?? endpoint['serverTag']).toString()),
        subtitle: Text((endpoint['stateText'] ?? '').toString()),
        widget: EndpointPage(kind: kind, tag: (endpoint['endpointTag'] ?? endpoint['serverTag']).toString()),
      )]),
    ]);
  }
}

class EndpointPage extends StatelessWidget {
  const EndpointPage({super.key, required this.kind, required this.tag});
  final String kind, tag;
  @override Widget build(BuildContext context) {
    final box = context.box;
    final endpoint = rows(box.toolState[kind]).where((item) => (item['endpointTag'] ?? item['serverTag']) == tag).firstOrNull;
    if (endpoint == null) return BoxPage(title: tag, children: const [EmptyState('Endpoint disconnected')]);
    final challenge = json(endpoint[kind == 'openvpn' ? 'challenge' : 'authChallenge']);
    return BoxPage(title: tag, children: [
      if (kind == 'tailscale') ...[
        DetailRows({'Status': endpoint['stateText'], 'Network': endpoint['networkName'],
          'Address': (json(endpoint['selfPeer'])['tailscaleIPs'] as List? ?? []).join('\n')}),
        if ((endpoint['authURL'] as String? ?? '').isNotEmpty)
          ListItem(title: Text(context.tr('Sign in')), trailing: const GlyphIcon(AppGlyphs.openExternal),
            onTap: () => box.act('url.open', {'url': endpoint['authURL']})),
        if (endpoint['canShareFiles'] == true) generateSection(title: 'Taildrop', items: [
          ListItem.open(title: Text(context.tr('Received files')), subtitle: Text('${endpoint['waitingFileCount'] ?? 0}'), widget: InboxPage(endpoint: tag)),
          ListItem.open(title: Text(context.tr('Transfers')), widget: const TransfersPage()),
        ]),
        generateSection(title: context.tr('Exit node'), items: [
          ListItem<String>.options(title: Text(context.tr('Exit node')),
            options: ['', ...endpointPeers(endpoint).where((p) => p['exitNodeOption'] == true).map((p) => p['stableID'].toString())],
            value: json(endpoint['exitNode'])['stableID'] as String? ?? '',
            textBuilder: (id) => id.isEmpty ? context.tr('None') : peerName(endpointPeers(endpoint).firstWhere((p) => p['stableID'] == id, orElse: () => {'id': id})),
            onChanged: (id) => box.act('tailscale.exit', {'endpoint': tag, 'peer': id})),
        ]),
        for (final group in rows(endpoint['userGroups'])) generateSection(title: (group['displayName'] ?? group['loginName']).toString(), items: [
          for (final peer in rows(group['peers'])) ListItem.open(
            leading: GlyphIcon(AppGlyphs.devices, color: peer['online'] == true ? context.colorScheme.primary : context.colorScheme.outline),
            title: Text(peerName(peer)), subtitle: Text((peer['tailscaleIPs'] as List? ?? []).join(' · ')),
            widget: PeerPage(endpoint: tag, id: peer['id'].toString()),
          ),
        ]),
        ListItem(title: Text(context.tr('Sign out')), onTap: () async {
          if (await confirm(context, 'Sign out', action: 'Sign out')) await box.act('tailscale.logout', {'endpoint': tag});
        }),
      ] else if (kind == 'usbip') ...[
        generateSection(title: context.tr('Shared devices'), items: [
          for (final device in rows(endpoint['devices'])) ListItem.open(
            title: Text((device['product'] ?? device['busId']).toString()), subtitle: Text((device['state'] ?? '').toString()),
            widget: BoxPage(title: 'USB/IP', children: [DetailRows(device)])),
        ]),
        if (!box.remote) ...[
          ListItem(title: Text(context.tr('Attach USB device')), trailing: const GlyphIcon(AppGlyphs.add),
            onTap: () => openForm(context, UsbPicker(server: tag))),
          for (final device in box.usbDevices.where((d) => d['server'] == tag)) ListItem(
            title: Text(device['label'].toString()), subtitle: Text('${device['state']}\n${device['error'] ?? ''}'),
            trailing: IconButton(tooltip: context.tr('Detach'), icon: const GlyphIcon(AppGlyphs.close),
              onPressed: () => box.act('usb.detach', {'id': device['id']})),
          ),
        ],
      ] else ...[
        DetailRows({'Status': endpoint['stateText'], if ((endpoint['error'] ?? '') != '') 'Error': endpoint['error'], ...json(endpoint['tunnelInfo'])}),
        if (challenge.isNotEmpty) generateSection(title: context.tr('Authentication'), items: [
          ListItem(title: Text(context.tr('Authenticate')), subtitle: Text((challenge['message'] ?? challenge['banner'] ?? '').toString()),
            onTap: () => openPage(context, AuthenticationPage(kind: kind, endpoint: tag, challenge: challenge))),
        ]),
      ],
    ]);
  }
}

class PeerPage extends StatefulWidget {
  const PeerPage({super.key, required this.endpoint, required this.id});
  final String endpoint, id;
  @override State<PeerPage> createState() => _PeerPageState();
}
class _PeerPageState extends State<PeerPage> {
  BoxController? _box;
  bool _ping = false;
  @override void didChangeDependencies() { super.didChangeDependencies(); _box ??= context.box; }
  @override void dispose() { if (_ping) _box?.act('tailscale.stopPing'); super.dispose(); }
  @override Widget build(BuildContext context) {
    final box = context.box;
    final endpoint = rows(box.toolState['tailscale']).where((e) => e['endpointTag'] == widget.endpoint).firstOrNull;
    final peer = endpoint == null ? null : endpointPeers(endpoint).where((p) => p['id'] == widget.id).firstOrNull;
    if (peer == null) return const BoxPage(title: 'Tailscale', children: [EmptyState('Peer disconnected')]);
    final args = {'endpoint': widget.endpoint, 'peer': widget.id};
    return BoxPage(title: peerName(peer), children: [
      DetailRows({'Address': (peer['tailscaleIPs'] as List? ?? []).join('\n'), 'OS': peer['os'],
        'Status': context.tr(peer['online'] == true ? 'Online' : 'Offline'),
        'Download': bytes(peer['rxBytes'] as num? ?? 0), 'Upload': bytes(peer['txBytes'] as num? ?? 0)}),
      generateSection(items: [
        ListItem(title: const Text('Ping'), onTap: () { setState(() => _ping = true); box.act('tailscale.ping', args); }),
        if ((peer['sshHostKeys'] as List? ?? []).isNotEmpty) ListItem.open(
          title: const Text('SSH'), widget: TerminalPage(endpoint: widget.endpoint, peer: widget.id, name: peerName(peer))),
        if (peer['canReceiveFiles'] == true) ListItem(title: Text(context.tr('Send file')), onTap: () async {
          try {
            final id = await box.call('taildrop.send', args);
            if (id != null && context.mounted) await openPage(context, const TransfersPage());
          } catch (error) { box.report(error); }
        }),
      ]),
      if (_ping) ...[
        if (box.ping['isRunning'] == true) const LinearProgressIndicator(),
        DetailRows({'Latency': '${box.ping['latencyMs'] ?? 0} ms',
          'Path': box.ping['isDirect'] == true ? context.tr('Direct') : box.ping['derpRegionCode'],
          if ((box.ping['error'] ?? '') != '') 'Error': box.ping['error']}),
      ],
    ]);
  }
}

class UsbPicker extends StatefulWidget {
  const UsbPicker({super.key, required this.server});
  final String server;
  @override State<UsbPicker> createState() => _UsbPickerState();
}
class _UsbPickerState extends State<UsbPicker> {
  List<Json>? _devices;
  Object? _error;
  bool _loading = false;
  @override void didChangeDependencies() { super.didChangeDependencies(); if (_devices == null && !_loading) _load(); }
  Future<void> _load() async {
    _loading = true;
    try { final devices = rows(await context.box.call('usb.list')); if (mounted) setState(() { _devices = devices; _error = null; }); }
    catch (error) { if (mounted) setState(() => _error = error); }
    finally { _loading = false; }
  }
  @override Widget build(BuildContext context) => BoxPage(title: 'Attach USB device', loading: _loading, children: [
    if (_error != null) ErrorState(_error!, retry: _load),
    if (_devices?.isEmpty == true) const EmptyState('No USB devices'),
    for (final device in _devices ?? <Json>[]) ListItem(title: Text(device['name'].toString()),
      onTap: () async {
        if (await context.box.act('usb.attach', {'server': widget.server, 'device': device['id']}) && context.mounted) Navigator.pop(context);
      }),
  ]);
}
