import 'package:b_box_ui/box.dart';
import 'package:b_box_ui/common/capture_page.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  test('query deltas reuse retained summaries and release records outside the window', () async {
    const channel = MethodChannel('test.capture-page');
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    final box = BoxController(channel: channel, events: const Stream.empty());
    final arguments = <Json>[];
    Json page = {'ids': ['a', 'b'], 'versions': {'a': 'a1', 'b': 'b1'}, 'items': [
      {'id': 'a', 'complete': true}, {'id': 'b', 'complete': false},
    ]};
    messenger.setMockMethodCallHandler(channel, (call) async {
      expect(call.method, 'capture.query');
      arguments.add(json(call.arguments));
      return {'state': {'active': true}, 'page': page};
    });
    addTearDown(() { box.dispose(); messenger.setMockMethodCallHandler(channel, null); });
    var result = await loadCapturePage(box, {'limit': 2}, null);
    final oldItems = json(result['page'])['items'] as List<Json>;
    expect(arguments.last['known'], isEmpty);
    page = {'ids': ['b', 'c'], 'versions': {'b': 'b1', 'c': 'c1'}, 'items': [{'id': 'c', 'complete': true}]};
    result = await loadCapturePage(box, {'limit': 2}, result);
    final items = json(result['page'])['items'] as List<Json>;
    expect(arguments.last['known'], {'a': 'a1', 'b': 'b1'});
    expect(items.map((item) => item['id']), ['b', 'c']);
    expect(items.first, same(oldItems.last));
    expect(oldItems.first['id'], 'a');
    page = {...page, 'items': <Json>[]};
    result = await loadCapturePage(box, {'limit': 2}, result);
    expect(arguments.last['known'], {'b': 'b1', 'c': 'c1'});
    expect(json(result['page'])['items'], same(items));
    page = {'ids': ['b'], 'versions': {'b': 'b2'}, 'items': [{'id': 'b', 'complete': true}]};
    result = await loadCapturePage(box, {'limit': 2}, result);
    expect((json(result['page'])['items'] as List<Json>).single['complete'], isTrue);
    expect(items.first['complete'], isFalse);
    page = {'ids': [], 'versions': {}, 'items': []};
    result = await loadCapturePage(box, {'limit': 2}, result);
    expect(json(result['page'])['items'], isEmpty);
    expect(json(result['page'])['versions'], isEmpty);
  });

  test('a malformed incremental page is rejected without inventing empty history', () async {
    const channel = MethodChannel('test.capture-page-invalid');
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    final box = BoxController(channel: channel, events: const Stream.empty());
    messenger.setMockMethodCallHandler(channel, (_) async => {'page': {'ids': ['missing'], 'versions': {'missing': 'v1'}, 'items': []}});
    addTearDown(() { box.dispose(); messenger.setMockMethodCallHandler(channel, null); });
    await expectLater(loadCapturePage(box, {'limit': 50}, null), throwsFormatException);
  });
}
