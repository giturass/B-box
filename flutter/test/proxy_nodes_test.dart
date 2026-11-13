import 'package:b_box_ui/box.dart';
import 'package:b_box_ui/common/proxy_nodes.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('node filters retain unchanged results and update data without stale sorting', () {
    final cache = ProxyNodes();
    final a = <String, dynamic>{'tag': 'Node A', 'delay': 30, 'testedAt': 1};
    final b = <String, dynamic>{'tag': 'Node B', 'delay': 10, 'testedAt': 1};
    final original = <Json>[b, a];
    final first = cache.select(original, sort: 'Name');
    expect(first, [a, b]);
    expect(identical(cache.select(original, sort: 'Name'), first), isTrue);
    final updated = {...b, 'delay': 50};
    final source = <Json>[updated, a];
    final next = cache.select(source, sort: 'Name');
    expect(next, [a, updated]);
    expect(identical(next.first, a), isTrue);
    expect(first.last['delay'], 10);
    expect(cache.select(source, sort: 'Latency'), [a, updated]);
    expect(cache.select(source), source);
    expect(cache.select(source, query: 'NODE a'), [a]);
    expect(cache.select(source, query: 'missing'), isEmpty);
  });

  test('timeouts, renames, reordered equal delays and removals invalidate projections', () {
    final cache = ProxyNodes();
    final a = <String, dynamic>{'tag': 'A', 'delay': 20, 'testedAt': 1};
    final b = <String, dynamic>{'tag': 'B', 'delay': 20, 'testedAt': 1};
    List<Json> select(List<Json> source) => cache.select(source, hideTimeouts: true, sort: 'Latency');
    expect(select([a, b]), [a, b]);
    expect(select([b, a]), [b, a]);
    final timeout = {...b, 'delay': 0};
    expect(select([timeout, a]), [a]);
    final untested = {...timeout, 'testedAt': 0};
    expect(select([untested, a]), [a, untested]);
    expect(cache.select([a, b], query: 'A'), [a]);
    expect(cache.select([{...a, 'tag': 'C'}, b], query: 'A'), isEmpty);
    expect(select([]), isEmpty);
  });
}
