import 'dart:async';
import 'dart:typed_data';
import 'package:b_box_ui/common/app_icon_cache.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('icons share pending loads and evict the least recently used bytes', () async {
    final calls = <String>[];
    final removed = <Uint8List>[];
    final pending = Completer<Uint8List?>();
    final cache = AppIconCache(maxBytes: 8, maxEntries: 5, onEvict: removed.add, load: (package) {
      calls.add(package);
      return package == 'pending' ? pending.future : Future.value(Uint8List(4));
    });
    final first = cache.get('pending');
    expect(identical(first, cache.get('pending')), isTrue);
    pending.complete(Uint8List(4));
    final a = await first;
    final b = await cache.get('b');
    expect(identical(await cache.get('pending'), a), isTrue);
    await cache.get('c');
    expect(cache.retainedBytes, 8);
    expect(removed, [b]);
    expect(calls, ['pending', 'b', 'c']);
    await cache.get('b');
    expect(calls, ['pending', 'b', 'c', 'b']);
    expect(cache.retainedBytes, lessThanOrEqualTo(cache.maxBytes));
  });

  test('misses and oversized icons cannot grow the cache without a bound', () async {
    final cache = AppIconCache(maxBytes: 4, maxEntries: 3,
      load: (package) async => package == 'large' ? Uint8List(5) : null);
    for (var i = 0; i < 20; i++) { await cache.get('missing-$i'); }
    expect(cache.length, 3);
    expect(cache.retainedBytes, 0);
    expect(await cache.get('large'), hasLength(5));
    expect(cache.retainedBytes, 0);
    expect(cache.length, lessThanOrEqualTo(3));
  });

  test('clearing during a load cannot repopulate or overwrite a fresh entry', () async {
    final old = Completer<Uint8List?>(), fresh = Completer<Uint8List?>();
    var loads = 0;
    final cache = AppIconCache(load: (_) => loads++ == 0 ? old.future : fresh.future);
    final first = cache.get('app');
    cache.clear();
    final second = cache.get('app');
    final newBytes = Uint8List(2);
    fresh.complete(newBytes);
    await second;
    old.complete(Uint8List(20));
    expect(await first, hasLength(20)); // Existing rows may finish their own load.
    expect(identical(await cache.get('app'), newBytes), isTrue);
    expect(cache.retainedBytes, 2);
    cache.clear();
    expect(cache.length, 0);
    expect(cache.retainedBytes, 0);
  });
}
