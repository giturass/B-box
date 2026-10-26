import 'dart:async';
import 'package:b_box_ui/box.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const channel = MethodChannel('test.box');
  late StreamController<dynamic> events;
  late BoxController box;
  final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;

  setUp(() {
    events = StreamController<dynamic>.broadcast();
    box = BoxController(channel: channel, events: events.stream);
  });
  tearDown(() async { box.dispose(); await events.close(); messenger.setMockMethodCallHandler(channel, null); });

  test('native startup failure is recoverable', () async {
    messenger.setMockMethodCallHandler(channel, (_) async => throw PlatformException(code: 'startup', message: 'configuration error'));
    await box.initialize();
    expect(box.ready, false);
    expect(box.startupError, 'configuration error');
    messenger.setMockMethodCallHandler(channel, (_) async => {'version': 'test', 'coreVersion': 'test', 'dashboard': {'status': 'Stopped'}});
    await box.initialize();
    expect(box.ready, true);
    expect(box.startupError, null);
  });

  test('service and capability state comes from the active core', () async {
    messenger.setMockMethodCallHandler(channel, (_) async => {'version': 'test', 'coreVersion': 'test', 'dashboard': {'status': 'Stopped'}});
    await box.initialize();
    expect(box.running, false);
    events.add({'type': 'dashboard', 'value': {'remote': 'server', 'remoteConnected': true, 'status': 'Stopped', 'modes': ['rule']}});
    await Future<void>.delayed(Duration.zero);
    expect(box.running, true);
    expect(box.dashboard['modes'], ['rule']);
    events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'modes': []}});
    await Future<void>.delayed(Duration.zero);
    expect(box.running, false);
    expect(box.dashboard['modes'], isEmpty);
  });

  test('command errors are visible and never become successful actions', () async {
    messenger.setMockMethodCallHandler(channel, (_) async => throw PlatformException(code: 'core', message: 'automatic group'));
    final error = box.messages.stream.first;
    expect(await box.act('groups.select', {'group': 'auto', 'tag': 'node'}), false);
    expect(await error, 'automatic group');
  });

  test('imports received before the home route mounts stay queued', () async {
    messenger.setMockMethodCallHandler(channel, (call) async => call.method == 'intent.take'
      ? 'sing-box://import-remote-profile?url=https%3A%2F%2Fexample.org%2Fconfig.json'
      : {'version': 'test', 'coreVersion': 'test', 'dashboard': {'status': 'Stopped'}});
    await box.initialize();
    events.add({'type': 'intentAvailable', 'value': true});
    await Future<void>.delayed(Duration.zero);
    await Future<void>.delayed(Duration.zero);
    expect(box.pendingIntents.single, startsWith('sing-box://'));
  });
}
