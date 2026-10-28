import 'dart:async';
import 'dart:convert';
import 'package:b_box_ui/common/ip_probe.dart';

typedef ProbeReply = ({int status, Object? body, Duration delay});

ProbeReply probeOk(Object body, [Duration delay = Duration.zero]) =>
    (status: 200, body: body, delay: delay);

ProbeReply probeStatus(int status) =>
    (status: status, body: null, delay: Duration.zero);

class FakeIpProbe implements IpProbeTransport {
  FakeIpProbe(this.replies);

  final Map<String, ProbeReply> replies;
  final requested = <Uri>[];
  final timeouts = <Duration>[];
  final cancelledHosts = <String>{};
  final _pending = <Completer<IpProbeResponse>, ({String host, Timer timer})>{};
  bool _closed = false;
  @override bool cancelled = false;

  @override Future<IpProbeResponse> get(String url, Duration timeout) {
    if (cancelled || _closed) return Future.error(const IpProbeCancelled());
    final uri = Uri.parse(url);
    requested.add(uri);
    timeouts.add(timeout);
    final reply = replies[uri.host] ?? probeStatus(503);
    final completer = Completer<IpProbeResponse>();
    final timer = Timer(reply.delay, () {
      _pending.remove(completer);
      completer.complete(IpProbeResponse(status: reply.status, body: reply.body == null ? ''
        : reply.body is String ? reply.body as String : jsonEncode(reply.body)));
    });
    _pending[completer] = (host: uri.host, timer: timer);
    return completer.future;
  }

  void cancel() { cancelled = true; unawaited(close()); }

  @override Future<void> close() async {
    if (_closed) return;
    _closed = true;
    for (final entry in _pending.entries) {
      entry.value.timer.cancel();
      cancelledHosts.add(entry.value.host);
      entry.key.completeError(const IpProbeCancelled());
    }
    _pending.clear();
  }
}
