import 'package:b_box_ui/box.dart';
import 'package:b_box_ui/common/network_activity.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  Json connection(String id, int speed, {bool closed = false}) => {
    'id': id, 'destination': '$id.example:443', 'network': 'tcp',
    'process': {'packages': ['com.$id']}, 'createdAt': id == 'a' ? 1 : 2,
    'upload': speed, 'download': 0, 'uploadTotal': speed * 10, 'downloadTotal': 300 - speed * 10,
    'closedAt': closed ? 100 : null,
  };

  test('search, filters and all sort choices follow connection deltas without changing old rows', () {
    final cache = ActivityList();
    var source = [connection('a', 20), connection('b', 10, closed: true)];
    List<ActivityEntry> select({String query = '', String filter = 'All', String sort = 'Speed'}) =>
      cache.select(source, const [], query: query, filter: filter, sort: sort);
    final original = select();
    expect(original.map((entry) => entry.connection!['id']), ['a', 'b']);
    expect(identical(select(), original), isTrue);
    expect(select(query: 'TCP COM.A').single.connection!['id'], 'a');
    expect(select(filter: 'Active').single.connection!['id'], 'a');
    expect(select(filter: 'Finished').single.connection!['id'], 'b');
    expect(select(sort: 'Created').map((entry) => entry.connection!['id']), ['b', 'a']);
    expect(select(sort: 'Upload').map((entry) => entry.connection!['id']), ['a', 'b']);
    expect(select(sort: 'Download').map((entry) => entry.connection!['id']), ['b', 'a']);
    select(sort: 'Created');
    source = [{...source[0], 'upload': 5}, {...source[1], 'upload': 50}];
    expect(select(sort: 'Created').map((entry) => entry.connection!['id']), ['b', 'a']);
    expect(select(sort: 'Created').first.connection!['upload'], 50);
    expect(select().map((entry) => entry.connection!['id']), ['b', 'a']);
    expect(original.first.connection!['upload'], 20);
    source = [{...source[0], 'destination': 'new.test:443', 'process': {'packages': ['org.changed']}}];
    expect(select(query: 'com.a'), isEmpty);
    expect(select(query: 'new.test org.changed').single.connection!['id'], 'a');
    source = [];
    expect(select(), isEmpty);
  });
}
