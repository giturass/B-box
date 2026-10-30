import 'dart:async';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const channel = MethodChannel('test.state_updates');
  final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  late StreamController<dynamic> events;
  late BoxController box;

  setUp(() {
    events = StreamController<dynamic>.broadcast();
    box = BoxController(channel: channel, events: events.stream);
    messenger.setMockMethodCallHandler(channel, (call) async => call.method == 'ready'
      ? {'version': 'test', 'coreVersion': 'test', 'dashboard': {'status': 'Stopped'}} : null);
  });

  tearDown(() async {
    await events.close();
    messenger.setMockMethodCallHandler(channel, null);
  });

  testWidgets('telemetry updates only its subscribers and preserves state notifications', (tester) async {
    await box.initialize();
    final builds = {for (final aspect in BoxAspect.values) aspect: 0};
    var routeUpdates = 0;
    box.stateChanges.addListener(() => routeUpdates++);
    await tester.pumpWidget(BoxScope(controller: box, child: Directionality(
      textDirection: TextDirection.ltr,
      child: Column(children: [for (final aspect in BoxAspect.values) Builder(builder: (context) {
        final state = aspect == BoxAspect.state ? context.box : BoxScope.of(context, aspect: aspect);
        builds[aspect] = builds[aspect]! + 1;
        return Text('${aspect.name}:${state.ready}');
      })]),
    )));
    for (final (type, value, aspect) in <(String, Object, BoxAspect)>[
      ('rates', {'upload': 42, 'download': 84}, BoxAspect.traffic),
      ('dashboard', {'status': 'Stopped', 'uploadTotal': '1 MB', 'connections': 20}, BoxAspect.traffic),
      ('memory', {'total': 100}, BoxAspect.memory),
      ('logs', {'entries': [], 'defaultLogLevel': 'Warn'}, BoxAspect.logs),
      ('connections', [], BoxAspect.connections),
    ]) {
      final before = Map.of(builds);
      events.add({'type': type, 'value': value});
      await tester.pump();
      for (final candidate in BoxAspect.values) {
        expect(builds[candidate], before[candidate]! + (candidate == aspect ? 1 : 0), reason: '$type -> $candidate');
      }
      expect(routeUpdates, 0);
    }
    events.add({'type': 'dashboard', 'value': {'status': 'Started', 'mode': 'rule'}});
    await tester.pump();
    expect(routeUpdates, 1);
    expect(builds[BoxAspect.state], 2);
    final before = Map.of(builds);
    box.notifyListeners();
    await tester.pump();
    for (final aspect in BoxAspect.values) { expect(builds[aspect], before[aspect]! + 1); }
    await tester.pumpWidget(const SizedBox());
    box.dispose();
  });

  testWidgets('the application root survives telemetry and responds to appearance changes', (tester) async {
    await tester.pumpWidget(BoxApp(controller: box));
    await tester.pumpAndSettle();
    final app = tester.widget<MaterialApp>(find.byType(MaterialApp));
    for (final (type, value) in <(String, Object)>[
      ('rates', {'upload': 1024, 'download': 2048}),
      ('memory', {'total': 100}),
      ('logs', {'entries': [], 'defaultLogLevel': 'Info'}),
      ('connections', []),
      ('dashboard', {...box.dashboard, 'uploadTotal': '2 MB', 'memory': '20 MB'}),
      ('settings', {'allowBypass': true}),
    ]) {
      events.add({'type': type, 'value': value});
      await tester.pumpAndSettle();
      expect(identical(app, tester.widget<MaterialApp>(find.byType(MaterialApp))), isTrue, reason: type);
    }
    events.add({'type': 'preferences', 'value': {'theme': 'dark'}});
    await tester.pumpAndSettle();
    expect(tester.widget<MaterialApp>(find.byType(MaterialApp)).themeMode, ThemeMode.dark);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
  });

  test('log deltas retain existing records, evict old entries and keep pause snapshots intact', () async {
    await box.initialize();
    Json record(int id) => {'id': id, 'level': 'Debug', 'message': 'record $id'};
    Future<void> emit(Json value) async {
      events.add({'type': 'logs', 'value': value});
      await Future<void>.delayed(Duration.zero);
    }
    await emit({'reset': true, 'firstId': 1, 'defaultLogLevel': 'Info', 'entries': [record(1), record(2)]});
    final paused = List<Json>.of(box.logs);
    await emit({'reset': false, 'firstId': 2, 'defaultLogLevel': 'Warn', 'entries': [record(3)]});
    expect(box.logs.map((entry) => entry['id']), [2, 3]);
    expect(identical(box.logs.first, paused.last), isTrue);
    expect(paused.map((entry) => entry['id']), [1, 2]);
    expect(box.defaultLogLevel, 'Warn');
    await emit({'reset': false, 'firstId': 2, 'defaultLogLevel': 'Error', 'entries': []});
    expect(box.logs.map((entry) => entry['id']), [2, 3]);
    await emit({'reset': true, 'firstId': null, 'defaultLogLevel': null, 'entries': []});
    expect(box.logs, isEmpty);
    await emit({'reset': true, 'firstId': 1, 'defaultLogLevel': 'Trace', 'entries': [record(1)]});
    expect(box.logs.single['id'], 1);
    expect(box.defaultLogLevel, 'Trace');
    box.dispose();
  });
}
