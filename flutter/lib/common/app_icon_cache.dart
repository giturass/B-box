import 'dart:async';
import 'dart:collection';
import 'dart:typed_data';

/// Retains recently used encoded icons, including a bounded set of misses.
/// Eviction never cancels a future already used by a visible application row.
class AppIconCache {
  AppIconCache({required this.load, this.onEvict,
    this.maxBytes = 4 * 1024 * 1024, this.maxEntries = 256})
      : assert(maxBytes >= 0), assert(maxEntries >= 0);

  final Future<Uint8List?> Function(String) load;
  final void Function(Uint8List)? onEvict;
  final int maxBytes, maxEntries;
  final _entries = LinkedHashMap<String, _IconEntry>();
  int _bytes = 0;
  int get retainedBytes => _bytes;
  int get length => _entries.length;

  Future<Uint8List?> get(String package) {
    final cached = _entries.remove(package);
    if (cached != null) {
      _entries[package] = cached;
      return cached.result.future;
    }
    final entry = _IconEntry();
    _entries[package] = entry;
    _trim();
    unawaited(_load(package, entry));
    return entry.result.future;
  }

  Future<void> _load(String package, _IconEntry entry) async {
    Uint8List? bytes;
    try { bytes = await load(package); } catch (_) { /* Keep the fallback icon. */ }
    // A refresh, eviction or memory-pressure signal may have removed this load.
    if (identical(_entries[package], entry)) {
      entry.bytes = bytes;
      _bytes += bytes?.lengthInBytes ?? 0;
      _trim();
    }
    entry.result.complete(bytes);
  }

  void _trim() {
    while (_entries.length > maxEntries || _bytes > maxBytes) {
      _remove(_entries.keys.first);
    }
  }

  void _remove(String package) {
    final bytes = _entries.remove(package)?.bytes;
    if (bytes != null) {
      _bytes -= bytes.lengthInBytes;
      onEvict?.call(bytes);
    }
  }

  void clear() {
    for (final package in _entries.keys.toList()) { _remove(package); }
  }
}

class _IconEntry {
  final result = Completer<Uint8List?>();
  Uint8List? bytes;
}
