import 'package:collection/collection.dart';
import '../box.dart';

/// One HTTP exchange (or recorded TLS stream), or one ordinary connection.
/// Socket traffic is never attributed to each multiplexed HTTP request.
class ActivityEntry {
  const ActivityEntry({this.connection, this.capture});
  final Json? connection;
  final Json? capture;

  String get key => capture != null ? 'request:${capture!['id']}' : 'connection:${connection!['id']}';
  String get connectionId => (capture != null ? capture!['connectionId'] : connection!['id'])?.toString() ?? '';
  bool get active => capture != null
      ? capture!['complete'] != true && capture!['domainOnly'] != true
      : (connection!['closedAt'] as num? ?? 0) <= 0;
  bool get supportsHAR => capture != null && capture!['domainOnly'] != true &&
      (capture!['protocol'] as String? ?? '').startsWith('HTTP/');

  num? rank(String sort) => switch (sort) {
    'Speed' => capture == null ? (connection!['upload'] as num? ?? 0) + (connection!['download'] as num? ?? 0) : null,
    'Upload' => (capture != null ? capture!['requestBytes'] : connection!['uploadTotal']) as num?,
    'Download' => (capture != null ? capture!['responseBytes'] : connection!['downloadTotal']) as num?,
    _ => (capture != null ? capture!['startedAt'] : connection!['createdAt']) as num? ?? 0,
  };
}

/// Joins bounded capture summaries to the current connection snapshot before
/// filtering and pagination. Missing/expired parents never hide saved requests.
class ActivityList {
  static const _connectionFields = ['id', 'displayDestination', 'destination', 'domain', 'outbound',
    'inbound', 'network', 'protocol', 'rule', 'source', 'process', 'chain'];
  static const _captureFields = ['id', 'connectionId', 'url', 'originalUrl', 'method', 'protocol',
    'status', 'packages', 'source', 'destination', 'outbound', 'inbound', 'rule', 'error'];
  final _entries = <String, ActivityEntry>{};
  final _search = <String, (List<Object?>, String)>{};
  List<Json>? _connections, _captures;
  List<ActivityEntry> _result = [];
  Map<String, num?> _ranks = {};
  String? _query, _filter, _sort;

  ActivityEntry _entry(String key, Json? connection, Json? capture) {
    final old = _entries[key];
    if (old != null && identical(old.connection, connection) &&
        const DeepCollectionEquality().equals(old.capture, capture)) return old;
    return _entries[key] = ActivityEntry(connection: connection, capture: capture);
  }

  bool _matches(ActivityEntry entry, List<String> words) {
    if (words.isEmpty) return true;
    final fields = <Object?>[
      for (final field in _connectionFields) entry.connection?[field],
      for (final field in _captureFields) entry.capture?[field],
    ];
    final cached = _search[entry.key];
    final text = cached != null && const DeepCollectionEquality().equals(fields, cached.$1)
        ? cached.$2 : fields.where((value) => value != null).join(' ').toLowerCase();
    _search[entry.key] = (fields, text);
    return words.every(text.contains);
  }

  List<ActivityEntry> select(List<Json> connections, List<Json> captures,
      {String query = '', String filter = 'All', String sort = 'Created'}) {
    query = query.toLowerCase().trim();
    if (identical(connections, _connections) && identical(captures, _captures) &&
        query == _query && filter == _filter && sort == _sort) return _result;
    final words = query.split(RegExp(r'\s+')).where((word) => word.isNotEmpty).toList();
    final parents = {for (final connection in connections) connection['id'].toString(): connection};
    final retained = <String>{};
    final represented = <String>{};
    final matches = <String, ActivityEntry>{};
    bool include(ActivityEntry entry) => !(filter == 'Active' && !entry.active ||
        filter == 'Finished' && entry.active) && _matches(entry, words);

    for (final capture in captures) {
      final key = 'request:${capture['id']}';
      retained.add(key);
      final id = capture['connectionId']?.toString() ?? '';
      final entry = _entry(key, id.isEmpty ? null : parents[id], capture);
      if (!include(entry)) continue;
      matches[key] = entry;
      if (id.isNotEmpty) represented.add(id);
    }
    for (final connection in connections) {
      final id = connection['id'].toString(), key = 'connection:${connection['id']}';
      retained.add(key);
      // Suppress only parents represented in this filtered result. An idle
      // keep-alive socket remains visible in Active after its HTTP work ends.
      if (filter == 'Captured' || represented.contains(id)) continue;
      final entry = _entry(key, connection, null);
      if (include(entry)) matches[key] = entry;
    }
    _entries.removeWhere((key, _) => !retained.contains(key));
    _search.removeWhere((key, _) => !retained.contains(key));
    final ranks = matches.map((key, entry) => MapEntry(key, entry.rank(sort)));
    final sameOrder = sort == _sort && const MapEquality<String, num?>().equals(_ranks, ranks);
    _result = sameOrder ? [for (final old in _result) matches[old.key]!] : (matches.values.toList()
      ..sort((a, b) {
        final ar = ranks[a.key], br = ranks[b.key];
        final order = ar == null ? (br == null ? 0 : 1) : br == null ? -1 : br.compareTo(ar);
        return order != 0 ? order : a.key.compareTo(b.key);
      }));
    _connections = connections;
    _captures = captures;
    _query = query;
    _filter = filter;
    _sort = sort;
    _ranks = ranks;
    return _result;
  }
}
