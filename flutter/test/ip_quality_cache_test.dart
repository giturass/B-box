import 'dart:async';
import 'package:b_box_ui/common/ip_probe.dart';
import 'package:b_box_ui/common/ip_quality.dart';
import 'package:b_box_ui/common/ip_quality_cache.dart';
import 'package:flutter_test/flutter_test.dart';
import 'support/ip_probe.dart';

const _ip = '203.0.113.9';
const _quality = IpQuality(ip: _ip, source: IpQualitySource.identMe,
  type: IpType.business, organization: 'Example Business', asn: 64500);

class _Cache {
  DateTime now = DateTime.utc(2026, 10, 28);
  final disk = <String, Map<String, Object?>>{};
  int reads = 0, writes = 0;
  late final value = create();
  IpQualityCache create() => IpQualityCache(
    now: () => now,
    read: (ip) async { reads++; return disk[ip]; },
    write: (ip, record) async { writes++; disk[ip] = record; },
  );
}

FakeIpProbe _probe({bool fail = false, Duration delay = Duration.zero}) {
  final probe = FakeIpProbe(fail ? {} : {
    'ident.me': probeOk({'ip': _ip, 'type': 'business', 'aso': 'Example Business', 'asn': 64500}, delay),
  });
  addTearDown(probe.close);
  return probe;
}

void main() {
  test('successful results are shared in memory and persisted by IP', () async {
    final cache = _Cache();
    final first = _probe();
    final record = await cache.value.lookup(first, _ip);
    expect(record.quality, _quality);
    expect(cache.writes, 1);
    expect(first.cancelled, false, reason: 'Closing losing requests is not user cancellation.');

    final second = _probe();
    expect((await cache.value.lookup(second, _ip)).quality, _quality);
    expect(second.requested, isEmpty);
    expect(cache.writes, 1);

    final restarted = cache.create();
    final third = _probe();
    final loaded = await restarted.lookup(third, _ip);
    expect(loaded.quality, _quality);
    expect(loaded.checkedAt.millisecondsSinceEpoch, cache.now.millisecondsSinceEpoch);
    expect(third.requested, isEmpty);
  });

  test('memory and persisted records expire at exactly three days', () async {
    final cache = _Cache();
    await cache.value.lookup(_probe(), _ip);
    cache.now = cache.now.add(IpQualityCache.maxAge - const Duration(milliseconds: 1));
    final fresh = _probe();
    await cache.value.lookup(fresh, _ip);
    expect(fresh.requested, isEmpty);

    cache.now = cache.now.add(const Duration(milliseconds: 1));
    final expired = _probe();
    final record = await cache.value.lookup(expired, _ip);
    expect(expired.requested, hasLength(3));
    expect(cache.writes, 2);
    expect(record.checkedAt.millisecondsSinceEpoch, cache.now.millisecondsSinceEpoch);
  });

  for (final invalid in ['future timestamp', 'different IP', 'corrupt record']) {
    test('ignores a persisted $invalid', () async {
      final cache = _Cache();
      cache.disk[_ip] = switch (invalid) {
        'future timestamp' => IpQualityRecord(_quality, cache.now.add(const Duration(seconds: 1))).toJson(),
        'different IP' => IpQualityRecord(
          const IpQuality(ip: '198.51.100.1', source: IpQualitySource.identMe, type: IpType.hosting),
          cache.now).toJson(),
        _ => {'quality': {}, 'checkedAt': 1},
      };
      final probe = _probe();
      expect((await cache.value.lookup(probe, _ip)).quality, _quality);
      expect(probe.requested, hasLength(3));
      expect(cache.writes, 1);
    });
  }

  test('failures have a five-minute memory cooldown and are never persisted', () async {
    final cache = _Cache();
    await expectLater(cache.value.lookup(_probe(fail: true), _ip),
      throwsA(isA<IpQualityLookupException>()));
    expect(cache.writes, 0);
    expect(cache.disk, isEmpty);
    final reads = cache.reads;
    cache.now = cache.now.add(const Duration(minutes: 4, seconds: 59));
    final blocked = _probe();
    await expectLater(cache.value.lookup(blocked, _ip), throwsA(isA<IpQualityLookupException>()));
    expect(blocked.requested, isEmpty);
    expect(cache.reads, reads);

    cache.now = cache.now.add(const Duration(seconds: 1));
    final retried = _probe();
    expect((await cache.value.lookup(retried, _ip)).quality, _quality);
    expect(retried.requested, hasLength(3));
    expect(cache.writes, 1);
  });

  test('manual retry bypasses the failure cooldown', () async {
    final cache = _Cache();
    await expectLater(cache.value.lookup(_probe(fail: true), _ip),
      throwsA(isA<IpQualityLookupException>()));
    final retried = _probe();
    expect((await cache.value.lookup(retried, _ip, refresh: true)).quality, _quality);
    expect(retried.requested, hasLength(3));
    expect(cache.writes, 1);
  });

  test('a refresh skips both successful memory and persisted records', () async {
    final cache = _Cache();
    await cache.value.lookup(_probe(), _ip);
    final retried = _probe();
    await cache.value.lookup(retried, _ip, refresh: true);
    expect(retried.requested, hasLength(3));
    expect(cache.writes, 2);
  });

  test('cache IO failures do not discard a successful lookup', () async {
    final errors = <Object>[];
    final cache = IpQualityCache(
      read: (_) async => throw StateError('read failed'),
      write: (_, _) async => throw StateError('write failed'),
      onCacheError: errors.add,
    );
    expect((await cache.lookup(_probe(), _ip)).quality, _quality);
    expect(errors, hasLength(2));
    final reused = _probe();
    expect((await cache.lookup(reused, _ip)).quality, _quality);
    expect(reused.requested, isEmpty);
  });

  test('cancellation while reading the cache does not return stale data', () async {
    final read = Completer<Object?>();
    var writes = 0;
    final errors = <Object>[];
    final cache = IpQualityCache(read: (_) => read.future,
      write: (_, _) async { writes++; }, onCacheError: errors.add);
    final probe = _probe();
    final result = cache.lookup(probe, _ip);
    final assertion = expectLater(result, throwsA(isA<IpProbeCancelled>()));
    probe.cancel();
    read.complete(IpQualityRecord(_quality, DateTime.now()).toJson());
    await assertion;
    expect(probe.requested, isEmpty);
    expect(writes, 0);
    expect(errors, isEmpty);
  });

  test('cancelled lookups do not create a failure cooldown', () async {
    final cache = _Cache();
    final probe = _probe(delay: const Duration(seconds: 2));
    final result = cache.value.lookup(probe, _ip);
    final assertion = expectLater(result, throwsA(isA<IpProbeCancelled>()));
    await pumpEventQueue();
    probe.cancel();
    await assertion;
    expect(cache.writes, 0);
    final retry = _probe();
    expect((await cache.value.lookup(retry, _ip)).quality, _quality);
    expect(retry.requested, hasLength(3));
  });

  test('simultaneous lookups for one IP share the pending result', () async {
    final cache = _Cache();
    final first = _probe(delay: const Duration(milliseconds: 60));
    final second = _probe();
    final results = [cache.value.lookup(first, _ip), cache.value.lookup(second, _ip)];
    await pumpEventQueue();
    expect(first.requested, hasLength(3));
    expect(second.requested, isEmpty);
    final records = await Future.wait(results);
    expect(records.map((record) => record.quality), everyElement(_quality));
    expect(cache.writes, 1);
  });

  test('a cancelled consumer of a shared lookup does not accept its result', () async {
    final cache = _Cache();
    final first = _probe(delay: const Duration(milliseconds: 60));
    final second = _probe();
    final result = cache.value.lookup(first, _ip);
    final shared = cache.value.lookup(second, _ip);
    final assertion = expectLater(shared, throwsA(isA<IpProbeCancelled>()));
    await pumpEventQueue();
    second.cancel();
    expect((await result).quality, _quality);
    await assertion;
    expect(cache.writes, 1);
  });
}
