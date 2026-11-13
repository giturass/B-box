import 'package:flutter/foundation.dart' show listEquals;
import '../box.dart';

/// Caches one group's projection. Unrelated preferences, selection changes and
/// latency updates under name/default sorting do not filter or sort it again.
class ProxyNodes {
  List<Json>? _source;
  List<Json> _result = const [];
  List<int> _indices = const [];
  String? _query, _sort;
  bool? _hideTimeouts;

  static bool _timeout(Json node) => (node['testedAt'] as num? ?? 0) > 0 && (node['delay'] as num? ?? 0) <= 0;
  static num _delay(Json node) => (node['delay'] as num? ?? 0) > 0 ? node['delay'] as num : double.infinity;

  List<Json> select(List<Json> source, {String query = '', String sort = 'Default', bool hideTimeouts = false}) {
    query = query.toLowerCase().trim();
    final sameOptions = query == _query && sort == _sort && hideTimeouts == _hideTimeouts;
    if (sameOptions && identical(source, _source)) return _result;
    var sameOrder = sameOptions && source.length == _source?.length;
    if (sameOrder) {
      for (var i = 0; i < source.length; i++) {
        final before = _source![i], after = source[i];
        if (before['tag'] != after['tag'] ||
            hideTimeouts && _timeout(before) != _timeout(after) ||
            sort == 'Latency' && _delay(before) != _delay(after)) {
          sameOrder = false;
          break;
        }
      }
    }
    if (!sameOrder) {
      final words = query.split(RegExp(r'\s+')).where((word) => word.isNotEmpty).toList();
      _indices = [for (var i = 0; i < source.length; i++)
        if ((!hideTimeouts || !_timeout(source[i])) &&
            words.every(source[i]['tag'].toString().toLowerCase().contains)) i];
      if (sort == 'Name' || sort == 'Latency') {
        _indices.sort((a, b) {
          final order = sort == 'Name'
              ? source[a]['tag'].toString().compareTo(source[b]['tag'].toString())
              : _delay(source[a]).compareTo(_delay(source[b]));
          return order != 0 ? order : a.compareTo(b);
        });
      }
    }
    final result = query.isEmpty && !hideTimeouts && sort == 'Default'
        ? source : [for (final index in _indices) source[index]];
    if (!listEquals(result, _result)) _result = result;
    _source = source;
    _query = query;
    _sort = sort;
    _hideTimeouts = hideTimeouts;
    return _result;
  }
}
