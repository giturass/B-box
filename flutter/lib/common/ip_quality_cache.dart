import 'ip_probe.dart';
import 'ip_quality.dart';

// FlClash providers/ip_quality.dart: successful records live for three days;
// failures stay in memory for five minutes and manual retry bypasses them.
class IpQualityCache {
  IpQualityCache({required this.read, required this.write, DateTime Function()? now, this.onCacheError})
    : now = now ?? DateTime.now;
  static const maxAge = Duration(days: 3);
  static const failureCooldown = Duration(minutes: 5);
  final Future<Object?> Function(String ip) read;
  final Future<void> Function(String ip, Map<String, Object?> record) write;
  final DateTime Function() now;
  final void Function(Object)? onCacheError;
  final _records = <String, IpQualityRecord>{};
  final _failures = <String, ({DateTime checkedAt, IpQualityLookupException error})>{};
  final _pending = <String, ({IpProbeTransport transport, Future<IpQualityRecord> result})>{};

  bool _fresh(IpQualityRecord record, String ip) {
    final age = now().difference(record.checkedAt);
    return record.quality.ip == ip && !age.isNegative && age < maxAge;
  }

  Future<IpQualityRecord> lookup(IpProbeTransport transport, String ip, {bool refresh = false}) async {
    if (transport.cancelled) throw const IpProbeCancelled();
    _records.removeWhere((key, record) => !_fresh(record, key));
    _failures.removeWhere((_, failure) {
      final age = now().difference(failure.checkedAt);
      return age.isNegative || age >= failureCooldown;
    });
    if (refresh) { _records.remove(ip); _failures.remove(ip); }
    if (!refresh) {
      final memory = _records[ip];
      if (memory != null) return memory;
      final failure = _failures[ip];
      if (failure != null) throw failure.error;
      Object? raw;
      try { raw = await read(ip); }
      catch (error) { onCacheError?.call(error); }
      if (transport.cancelled) throw const IpProbeCancelled();
      try {
        if (raw is Map) {
          final record = IpQualityRecord.fromJson(Map<String, dynamic>.from(raw));
          if (_fresh(record, ip)) { _records[ip] = record; return record; }
        }
      } catch (error) { onCacheError?.call(error); }
    }
    if (transport.cancelled) throw const IpProbeCancelled();
    final pending = _pending[ip];
    if (pending != null && !pending.transport.cancelled) {
      final record = await pending.result;
      if (transport.cancelled) throw const IpProbeCancelled();
      return record;
    }
    final result = _lookup(transport, ip);
    _pending[ip] = (transport: transport, result: result);
    try { return await result; }
    finally { if (identical(_pending[ip]?.result, result)) _pending.remove(ip); }
  }

  Future<IpQualityRecord> _lookup(IpProbeTransport transport, String ip) async {
    try {
      final quality = await lookupIpQuality(transport, ip);
      if (transport.cancelled) throw const IpProbeCancelled();
      final record = IpQualityRecord(quality, now());
      _records[ip] = record;
      _failures.remove(ip);
      try { await write(ip, record.toJson()); }
      catch (error) { onCacheError?.call(error); }
      return record;
    } on IpQualityLookupException catch (error) {
      if (!transport.cancelled) _failures[ip] = (checkedAt: now(), error: error);
      rethrow;
    } finally { await transport.close(); }
  }
}
