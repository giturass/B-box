import 'dart:convert';
import 'package:b_box_ui/box.dart';
import 'package:b_box_ui/common/network_activity.dart';

/// Simulates the page/delta bridge for widget tests. Core query tests exercise
/// the real store; this fixture uses the original full-list join as its oracle.
Json captureQueryResult(Json snapshot, Json arguments, List<Json> connections) {
  final source = json(snapshot['page']);
  if (source.containsKey('ids')) return snapshot;
  final captures = rows(source['items']).where((item) => arguments['connectionId'] == null ||
    item['connectionId'] == arguments['connectionId']).toList();
  final matched = ActivityList().select(connections, captures,
    query: arguments['query'] as String? ?? '', filter: arguments['filter'] as String? ?? 'All',
    sort: arguments['sort'] as String? ?? 'Created').where((entry) => entry.capture != null).toList();
  final limit = arguments['limit'] as int? ?? 50;
  final selected = matched.take(limit).map((entry) => entry.capture!).toList();
  final versions = {for (final item in selected) item['id'].toString(): jsonEncode(item)};
  final known = json(arguments['known']);
  final parents = json(arguments['parents']);
  return {...snapshot, 'page': {
    ...source, 'total': matched.length, 'capturedTotal': rows(source['items']).length,
    'ids': [for (final item in selected) item['id']], 'versions': versions,
    'items': [for (final item in selected) if (known[item['id']] != versions[item['id']]) item],
    'represented': {for (final entry in matched) if (parents.containsKey(entry.connectionId)) entry.connectionId}.toList(),
    'hasMore': matched.length > limit, 'harCount': matched.where((entry) => entry.supportsHAR).length,
  }};
}
