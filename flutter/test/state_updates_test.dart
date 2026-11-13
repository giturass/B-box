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
      ('tools', {'tailscale': []}, BoxAspect.tools),
      ('outbounds', [], BoxAspect.diagnostics),
      ('quality', {'phase': 1}, BoxAspect.diagnostics),
      ('stun', {'phase': 1}, BoxAspect.diagnostics),
      ('ping', {'latencyMs': 10}, BoxAspect.diagnostics),
      ('transfers', [{'id': 1, 'finished': false}], BoxAspect.transfers),
      ('inbox', {'receiving': []}, BoxAspect.transfers),
      ('usbDevices', [], BoxAspect.tools),
      ('terminal', {'sessions': []}, BoxAspect.terminal),
    ]) {
      final before = Map.of(builds);
      events.add({'type': type, 'value': value});
      // The broadcast arrives asynchronously and then schedules the model's frame.
      await tester.pumpAndSettle();
      for (final candidate in BoxAspect.values) {
        expect(builds[candidate], before[candidate]! + (candidate == aspect ? 1 : 0), reason: '$type -> $candidate');
      }
      expect(routeUpdates, 0);
    }
    events.add({'type': 'dashboard', 'value': {'status': 'Started', 'mode': 'rule'}});
    await tester.pumpAndSettle();
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
    final app = tester.widget<PrismApp>(find.byType(PrismApp));
    for (final (type, value) in <(String, Object)>[
      ('rates', {'upload': 1024, 'download': 2048}),
      ('memory', {'total': 100}),
      ('logs', {'entries': [], 'defaultLogLevel': 'Info'}),
      ('connections', []),
      ('tools', {'tailscale': []}),
      ('transfers', [{'id': 1, 'finished': false}]),
      ('quality', {'phase': 1}),
      ('terminal', {'sessions': []}),
      ('dashboard', {...box.dashboard, 'uploadTotal': '2 MB', 'memory': '20 MB'}),
      ('settings', {'allowBypass': true}),
    ]) {
      events.add({'type': type, 'value': value});
      await tester.pumpAndSettle();
      expect(identical(app, tester.widget<PrismApp>(find.byType(PrismApp))), isTrue, reason: type);
    }
    events.add({'type': 'preferences', 'value': {'theme': 'dark'}});
    await tester.pumpAndSettle();
    expect(tester.widget<PrismApp>(find.byType(PrismApp)).themeMode, ThemeMode.dark);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
  });

  test('connection deltas retain unchanged objects and reset between subscriptions', () async {
    await box.initialize();
    Future<void> emit(Json value) async {
      events.add({'type': 'connections', 'value': value});
      await Future<void>.delayed(Duration.zero);
    }
    await emit({'reset': true, 'entries': [{'id': 'a', 'upload': 1}, {'id': 'b', 'upload': 2}]});
    final old = box.connections;
    await emit({'reset': false, 'entries': [{'id': 'a', 'upload': 5}, {'id': 'c', 'upload': 3}], 'removed': []});
    expect(box.connections.map((entry) => entry['id']), ['a', 'b', 'c']);
    expect(identical(old[1], box.connections[1]), isTrue);
    expect(old[0]['upload'], 1);
    await emit({'reset': false, 'entries': [], 'removed': ['a']});
    expect(box.connections.map((entry) => entry['id']), ['b', 'c']);
    await emit({'reset': true, 'entries': [{'id': 'remote', 'upload': 10}]});
    expect(box.connections.single['id'], 'remote');
    box.dispose();
  });

  testWidgets('group latency updates preserve nodes and notify only group consumers', (tester) async {
    await box.initialize();
    Json group(int delay, {String selected = 'A'}) => {'tag': 'group', 'selected': selected, 'selectable': true,
      'items': [{'tag': 'A', 'delay': delay, 'testedAt': 1}, {'tag': 'B', 'delay': 20, 'testedAt': 1}]};
    events.add({'type': 'groups', 'value': [group(10)]});
    await tester.pump();
    var stateBuilds = 0, groupBuilds = 0, routeUpdates = 0;
    box.stateChanges.addListener(() => routeUpdates++);
    await tester.pumpWidget(BoxScope(controller: box, child: Directionality(textDirection: TextDirection.ltr,
      child: Column(children: [
        Builder(builder: (context) { context.box; stateBuilds++; return const SizedBox(); }),
        Builder(builder: (context) { BoxScope.of(context, aspect: BoxAspect.groups); groupBuilds++; return const SizedBox(); }),
      ]))));
    final oldGroup = box.groups.single;
    final oldNodes = oldGroup['items'] as List<Json>;
    events.add({'type': 'groups', 'value': [group(30)]});
    await tester.pumpAndSettle();
    expect(stateBuilds, 1);
    expect(groupBuilds, 2);
    expect(routeUpdates, 0);
    final nextNodes = box.groups.single['items'] as List<Json>;
    expect(nextNodes.first['delay'], 30);
    expect(identical(nextNodes.last, oldNodes.last), isTrue);
    expect(oldNodes.first['delay'], 10);
    final updated = box.groups;
    events.add({'type': 'groups', 'value': [group(30)]});
    await tester.pumpAndSettle();
    expect(identical(box.groups, updated), isTrue);
    expect(groupBuilds, 2);
    events.add({'type': 'groups', 'value': [group(30, selected: 'B')]});
    await tester.pumpAndSettle();
    expect(routeUpdates, 1);
    expect(stateBuilds, 1);
    expect(identical(box.groups.single['items'], nextNodes), isTrue);
    await tester.pumpWidget(const SizedBox());
    box.dispose();
  });

  testWidgets('memory pressure clears icons and theme changes retain only the current pair', (tester) async {
    messenger.setMockMethodCallHandler(channel, (call) async => switch (call.method) {
      'ready' => {'version': 'test', 'coreVersion': 'test', 'dashboard': {'status': 'Stopped'}},
      'apps.icon' => Uint8List(4),
      _ => null,
    });
    await tester.pumpWidget(BoxApp(controller: box));
    await tester.pumpAndSettle();
    final original = tester.widget<PrismApp>(find.byType(PrismApp)).theme!;
    await box.appIcon('app');
    expect(box.appIcons.retainedBytes, 4);
    tester.binding.handleMemoryPressure();
    expect(box.appIcons.retainedBytes, 0);
    for (final color in [0xffff2d55, 0xff34c759, 0xff007aff]) {
      events.add({'type': 'preferences', 'value': {'color': color}});
      await tester.pumpAndSettle();
    }
    final restored = tester.widget<PrismApp>(find.byType(PrismApp)).theme!;
    expect(restored.colorScheme, original.colorScheme);
    expect(identical(restored, original), isFalse);
    events.add({'type': 'preferences', 'value': {'color': 0xff007aff, 'floatingNavigation': false}});
    await tester.pumpAndSettle();
    expect(identical(tester.widget<PrismApp>(find.byType(PrismApp)).theme, restored), isTrue);
    await tester.pumpWidget(const SizedBox());
  });

  test('startup preserves tool and network events delivered before the minimal ready response', () async {
    final ready = Completer<Json>();
    messenger.setMockMethodCallHandler(channel, (call) async => call.method == 'ready' ? ready.future : null);
    final initialized = box.initialize();
    await Future<void>.delayed(Duration.zero);
    events.add({'type': 'tools', 'value': {'tailscale': [{'endpointTag': 'tail', 'authURL': 'https://login.test'}]}});
    events.add({'type': 'quality', 'value': {'phase': 3}});
    events.add({'type': 'dashboard', 'value': {'status': 'Started'}});
    events.add({'type': 'lan', 'value': {'addresses': ['192.168.1.10']}});
    await Future<void>.delayed(Duration.zero);
    ready.complete({'version': 'test', 'coreVersion': 'test', 'dashboard': {'status': 'Stopped'}});
    await initialized;
    expect(box.ready, isTrue);
    expect(box.running, isTrue);
    expect(rows(box.toolState['tailscale']).single['authURL'], 'https://login.test');
    expect(box.quality['phase'], 3);
    expect(box.lanAddresses, ['192.168.1.10']);
    box.dispose();
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
