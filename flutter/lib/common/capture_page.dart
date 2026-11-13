import 'package:flutter/foundation.dart' show listEquals;
import '../box.dart';

/// Applies one bounded query window. Unchanged summaries retain their identity;
/// records outside its authoritative ID order are released after each reply.
Future<Json> loadCapturePage(BoxController box, Json arguments, Json? previous) async {
  final before = json(previous?['page']);
  final oldItems = before['items'] as List<Json>? ?? const <Json>[];
  final next = json(await box.call('capture.query', {
    ...arguments, 'known': before['versions'] ?? const <String, String>{},
  }));
  final page = json(next['page']);
  if (page['ids'] is! List || page['versions'] is! Map || page['items'] is! List) {
    throw const FormatException('Incomplete capture page; refresh to retry');
  }
  final byId = {for (final item in oldItems) item['id']: item};
  for (final item in rows(page['items'])) { byId[item['id']] = item; }
  final items = <Json>[];
  for (final id in page['ids'] as List? ?? const []) {
    final item = byId[id];
    if (item == null) throw const FormatException('Incomplete capture page; refresh to retry');
    items.add(item);
  }
  page['items'] = listEquals(oldItems, items) ? oldItems : items;
  return {...next, 'page': page};
}
