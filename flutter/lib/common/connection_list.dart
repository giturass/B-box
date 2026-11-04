import 'package:collection/collection.dart';
import '../box.dart';

/// Reuses searchable metadata and ordering when only unrelated counters change.
class ConnectionList {
  static const _fields = ['destination', 'domain', 'outbound', 'network', 'rule', 'source', 'process'];
  final _search = <String, (Json, String)>{};
  List<Json>? _source;
  List<Json> _result = [];
  Map<String, num> _ranks = {};
  String? _query, _filter, _sort;

  List<Json> select(List<Json> source, {required String query, required String filter, required String sort}) {
    query = query.toLowerCase().trim();
    if (identical(source, _source) && query == _query && filter == _filter && sort == _sort) return _result;
    final words = query.split(' ').where((word) => word.isNotEmpty).toList();
    final key = {'Created': 'createdAt', 'Upload': 'uploadTotal', 'Download': 'downloadTotal'}[sort];
    num rank(Json value) => key == null
      ? (value['upload'] as num? ?? 0) + (value['download'] as num? ?? 0) : value[key] as num? ?? 0;
    final matches = <String, Json>{};
    final ids = <String>{};
    for (final connection in source) {
      final id = connection['id'] as String;
      ids.add(id);
      final closed = (connection['closedAt'] as num? ?? 0) > 0;
      if (filter == 'Active' && closed || filter == 'Closed' && !closed) continue;
      if (words.isNotEmpty) {
        final cached = _search[id];
        final unchanged = cached != null && _fields.every((field) =>
          const DeepCollectionEquality().equals(cached.$1[field], connection[field]));
        final text = unchanged ? cached!.$2 : _fields.map((field) => connection[field]).join(' ').toLowerCase();
        _search[id] = (connection, text);
        if (!words.every(text.contains)) continue;
      }
      matches[id] = connection;
    }
    _search.removeWhere((id, _) => !ids.contains(id));
    final ranks = matches.map((id, connection) => MapEntry(id, rank(connection)));
    final sameOrder = sort == _sort && const MapEquality<String, num>().equals(_ranks, ranks);
    _result = sameOrder ? [for (final old in _result) matches[old['id']]!] : (matches.values.toList()
      ..sort((a, b) {
        final order = ranks[b['id']]!.compareTo(ranks[a['id']]!);
        return order != 0 ? order : (a['id'] as String).compareTo(b['id'] as String);
      }));
    _source = source;
    _query = query;
    _filter = filter;
    _sort = sort;
    _ranks = ranks;
    return _result;
  }
}
