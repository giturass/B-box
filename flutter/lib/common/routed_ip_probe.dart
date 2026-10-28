import 'dart:async';
import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import '../box.dart';
import 'ip_probe.dart';

/// The native session uses the device network while stopped and sing-box while
/// running, sharing cancellation between IP and quality requests.
class RoutedIpProbe implements IpProbeTransport {
  RoutedIpProbe(this.box) : id = '${DateTime.now().microsecondsSinceEpoch}:${_serial++}';
  static int _serial = 0;
  final BoxController box;
  final String id;
  bool _cancelled = false;
  Future<void>? _closing;
  @override bool get cancelled => _cancelled;

  Future<void> open() async {
    if (cancelled) throw const IpProbeCancelled();
    await box.call('dashboard.probe.open', {'id': id});
    if (cancelled) throw const IpProbeCancelled();
  }

  Future<Json> egress() async {
    if (cancelled) throw const IpProbeCancelled();
    final result = json(await box.call('dashboard.egress', {'id': id}));
    if (cancelled) throw const IpProbeCancelled();
    return result;
  }

  @override Future<IpProbeResponse> get(String url, Duration timeout) async {
    if (cancelled) throw const IpProbeCancelled();
    try {
      final result = json(await box.call('dashboard.probe.get', {'id': id, 'url': url, 'timeoutMillis': timeout.inMilliseconds}));
      if (cancelled) throw const IpProbeCancelled();
      return IpProbeResponse(status: (result['status'] as num?)?.toInt() ?? 503, body: result['body'] as String? ?? '');
    } on PlatformException catch (error) {
      if (cancelled) throw const IpProbeCancelled();
      if (error.message?.contains('deadline exceeded') == true) throw TimeoutException('IP source timed out');
      rethrow;
    }
  }

  void cancel() { _cancelled = true; unawaited(close()); }

  @override Future<void> close() => _closing ??= _close();
  Future<void> _close() async {
    try { await box.call('dashboard.probe.close', {'id': id}); }
    catch (error) { debugPrint('Unable to close IP probe: ${BoxController.describe(error)}'); }
  }
}
