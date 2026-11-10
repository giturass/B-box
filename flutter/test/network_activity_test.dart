import 'package:b_box_ui/box.dart';
import 'package:b_box_ui/common/network_activity.dart';
import 'package:flutter_test/flutter_test.dart';

Json connection(String id, {bool closed = false}) => {
  'id': id, 'displayDestination': '$id.test:443', 'network': 'tcp',
  'createdAt': 1, 'closedAt': closed ? 20 : null, 'upload': 1000, 'download': 2000,
  'uploadTotal': 10000, 'downloadTotal': 20000, 'rule': 'domain_suffix=test', 'outbound': 'selected-node',
  'process': {'packages': ['com.example']},
};

Json capture(String id, {String? connectionId = 'socket', bool complete = true, int time = 10}) => {
  'id': id, if (connectionId != null) 'connectionId': connectionId,
  'startedAt': time, 'complete': complete, 'url': 'https://api.test/$id',
  'method': 'GET', 'protocol': 'HTTP/2.0', 'status': 200,
  'requestBytes': 10, 'responseBytes': 30, 'bytes': 40,
  'outbound': 'selected-node', 'rule': 'domain_suffix=test', 'source': '192.0.2.1:1234',
  'packages': ['com.example'],
};

void main() {
  test('multiplexed requests stay independent and Active retains an idle socket', () {
    final list = ActivityList();
    final connections = [connection('socket'), {...connection('udp', closed: true), 'network': 'udp'}];
    var captures = [capture('one'), capture('two', complete: false, time: 11)];
    List<String> keys([String filter = 'All']) => list.select(connections, captures, filter: filter).map((entry) => entry.key).toList();
    expect(keys(), ['request:two', 'request:one', 'connection:udp']);
    expect(keys('Active'), ['request:two']);
    expect(keys('Captured'), ['request:two', 'request:one']);
    expect(keys('Finished'), ['request:one', 'connection:udp']);
    captures = [captures.first, {...captures.last, 'complete': true}];
    expect(keys('Active'), ['connection:socket'], reason: 'Completed HTTP work does not close a keep-alive socket.');
    expect(list.select(const [], captures).map((entry) => entry.key), ['request:two', 'request:one']);
    expect(list.select(connections, const []).map((entry) => entry.key), ['connection:socket', 'connection:udp']);
  });

  test('legacy IDs, untracked requests and typed row identities never invent a parent', () {
    final list = ActivityList();
    final connections = [connection('socket'), connection('same-id')];
    final captures = [
      capture('same-id'),
      {...capture('legacy', connectionId: null), 'captureId': 'same-id'},
      capture('expired', connectionId: 'gone'),
    ];
    final entries = list.select(connections, captures);
    expect(entries.map((entry) => entry.key).toSet(), {'request:same-id', 'request:legacy', 'request:expired', 'connection:same-id'});
    expect(entries.singleWhere((entry) => entry.key == 'request:same-id').connection, same(connections.first));
    expect(entries.singleWhere((entry) => entry.key == 'request:legacy').connection, isNull);
    expect(entries.singleWhere((entry) => entry.key == 'request:expired').connection, isNull);
  });

  test('search joins HTTP and observed connection fields and survives parent eviction', () {
    final list = ActivityList();
    final connections = [connection('socket')];
    final captures = [{...capture('path'), 'originalUrl': 'https://original.test/before-rewrite'}];
    final joined = list.select(connections, captures, query: 'COM.EXAMPLE\nTCP path selected-node');
    expect(joined.single.key, 'request:path');
    expect(list.select(connections, captures, query: 'original.test').single.key, 'request:path');
    expect(list.select(const [], captures, query: 'path selected-node domain_suffix=test 192.0.2.1').single.key, 'request:path');
    expect(list.select(connections, captures, query: 'no-match'), isEmpty);
    expect(list.select(connections, captures, query: 'socket.test').single.key, 'request:path');
  });

  test('rates and wire traffic are not copied onto each HTTP exchange', () {
    final list = ActivityList();
    final entries = list.select([connection('socket'), connection('other')], [capture('one'), capture('two')]);
    for (final entry in entries.where((entry) => entry.capture != null)) {
      expect(entry.rank('Speed'), isNull);
      expect(entry.rank('Upload'), 10);
      expect(entry.rank('Download'), 30);
      expect(entry.supportsHAR, isTrue);
    }
    final speed = list.select([connection('socket'), connection('other')], [capture('one'), capture('two')], sort: 'Speed');
    expect(speed.first.key, 'connection:other');
    expect(speed.first.rank('Speed'), 3000);
    expect(ActivityEntry(capture: {...capture('domain'), 'domainOnly': true}).supportsHAR, isFalse);
    expect(ActivityEntry(capture: {...capture('tcp'), 'protocol': 'TLS/TCP'}).supportsHAR, isFalse);
    expect(ActivityEntry(capture: {...capture('domain', complete: false), 'domainOnly': true}).active, isFalse);
  });

  test('joining precedes pagination and preserves unchanged rows across counter updates', () {
    final list = ActivityList();
    final parent = {...connection('socket'), 'createdAt': 1000};
    var connections = [parent, connection('other')];
    var captures = [for (var index = 0; index < 75; index++) capture('$index', time: 100 - index)];
    final original = list.select(connections, captures);
    expect(original.length, 76);
    expect(original.take(50).where((entry) => entry.capture == null), isEmpty);
    expect(list.select(connections, captures), same(original));
    final first = original.first;
    connections = [parent, {...connections.last, 'upload': 5}];
    captures = [for (final item in captures) {...item}];
    final updated = list.select(connections, captures);
    expect(updated.map((entry) => entry.key), original.map((entry) => entry.key));
    expect(updated.first, same(first));
    expect(updated.last.connection!['upload'], 5);
    expect(original.last.connection!['upload'], 1000);
    expect(list.select(connections, captures, query: '/74').single.key, 'request:74');
    expect(list.select(connections, captures, filter: 'Captured').length, 75);
  });
}
