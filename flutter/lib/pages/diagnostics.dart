import '../widgets.dart';

class DiagnosticsPage extends StatefulWidget {
  const DiagnosticsPage({super.key, required this.quality});
  final bool quality;
  @override State<DiagnosticsPage> createState() => _DiagnosticsPageState();
}
class _DiagnosticsPageState extends State<DiagnosticsPage> {
  TextEditingController? _address;
  BoxController? _box;
  String _outbound = '';
  bool _serial = false, _http3 = false, _asking = false;
  int _seconds = 30;
  @override void didChangeDependencies() {
    super.didChangeDependencies();
    _box ??= context.box;
    final state = widget.quality ? _box!.quality : _box!.stun;
    _address ??= TextEditingController(text: state[widget.quality ? 'configURL' : 'server'] as String? ?? '');
  }
  @override void dispose() {
    _address?.dispose();
    _box?.act(widget.quality ? 'quality.stop' : 'stun.stop');
    super.dispose();
  }
  Future<void> _start() async {
    final box = context.box;
    await box.act(widget.quality ? 'quality.start' : 'stun.start', {
      if (widget.quality) 'url': _address!.text.trim() else 'server': _address!.text.trim(),
      'outbound': _outbound, 'serial': _serial, 'http3': _http3, 'seconds': _seconds,
    });
  }
  Future<void> _metered() async {
    if (_asking) return;
    _asking = true;
    final result = await confirm(context, 'Network quality',
      message: 'This test transfers data on a metered network. Continue?', action: 'Start');
    await _box!.act(result ? 'quality.confirm' : 'quality.dismiss');
    _asking = false;
  }
  @override Widget build(BuildContext context) {
    final box = context.box;
    final state = widget.quality ? box.quality : box.stun;
    final running = state['isRunning'] == true;
    if (widget.quality && state['showMeteredWarning'] == true && !_asking) {
      WidgetsBinding.instance.addPostFrameCallback((_) { if (mounted) _metered(); });
    }
    final outbounds = <String>{'', ...box.outbounds.map((o) => o['tag'].toString())}.toList();
    final phase = state['phase'] as num? ?? -1;
    return BoxPage(
      title: widget.quality ? 'Network quality' : 'STUN test',
      children: [
        generateSection(items: [
          Padding(padding: const EdgeInsets.all(16), child: TextField(
            controller: _address, readOnly: running,
            decoration: InputDecoration(labelText: context.tr(widget.quality ? 'URL' : 'Address')),
          )),
          if (box.running) ListItem.options(title: Text(context.tr('Outbound')), options: outbounds,
            value: outbounds.contains(_outbound) ? _outbound : '',
            textBuilder: (value) => value.isEmpty ? context.tr('Default') : value,
            onChanged: (value) { if (!running) setState(() => _outbound = value); }),
          if (widget.quality) ...[
            ListItem.toggle(title: Text(context.tr('Serial test')), value: _serial, onChanged: running ? null : (value) => setState(() => _serial = value)),
            ListItem.toggle(title: const Text('HTTP/3'), value: _http3, onChanged: running ? null : (value) => setState(() => _http3 = value)),
            ListItem<int>.options(title: Text(context.tr('Duration')), options: const [30, 60], value: _seconds,
              textBuilder: (value) => value.toString() + ' s', onChanged: (value) { if (!running) setState(() => _seconds = value); }),
          ],
        ]),
        Padding(padding: const EdgeInsets.symmetric(vertical: 20), child: FilledButton(
          onPressed: running ? () => box.act(widget.quality ? 'quality.stop' : 'stun.stop') : _start,
          child: Text(context.tr(running ? 'Stop' : 'Start')),
        )),
        if (running) const LinearProgressIndicator(),
        if (phase >= 0) Padding(padding: const EdgeInsets.only(top: 16), child: DetailRows(widget.quality ? {
          'Latency': (state['idleLatencyMs'] ?? 0).toString() + ' ms',
          'Download': ((state['downloadCapacity'] as num? ?? 0) / 1000000).toStringAsFixed(2) + ' Mbps',
          'Upload': ((state['uploadCapacity'] as num? ?? 0) / 1000000).toStringAsFixed(2) + ' Mbps',
          'Download RPM': state['downloadRPM'], 'Upload RPM': state['uploadRPM'],
        } : {
          'Address': state['externalAddr'],
          'Latency': (state['latencyMs'] ?? 0).toString() + ' ms',
          if (state['natTypeSupported'] == true) ...{
            'NAT mapping': state['mapping'], 'NAT filtering': state['filtering'],
          },
        })),
      ],
    );
  }
}
