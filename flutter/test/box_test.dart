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

  test('service setting changes wait for the native restart and ignore repeated edits', () async {
    box.dashboard = {'status': 'Started'};
    box.settings = {'allowBypass': false};
    box.applyRequired = 'Restart';
    final reply = Completer<Json>();
    final edits = <Json>[];
    messenger.setMockMethodCallHandler(channel, (call) async {
      expect(call.method, 'settings.set');
      edits.add(json(call.arguments));
      return reply.future;
    });
    final saving = box.setSetting('allowBypass', true, autoRestart: true);
    await Future<void>.delayed(Duration.zero);
    expect(box.serviceSettingsChanging, true);
    expect(box.settings['allowBypass'], false);
    expect(box.applyRequired, 'Restart');
    await box.setSetting('autoRedirect', true, autoRestart: true);
    expect(edits, [{'key': 'allowBypass', 'value': true, 'restart': true}]);
    reply.complete({'allowBypass': true});
    await saving;
    expect(box.serviceSettingsChanging, false);
    expect(box.settings['allowBypass'], true);
    expect(box.applyRequired, null);
  });

  for (final dashboard in <Json>[
    {'status': 'Stopped'},
    {'status': 'Started', 'remote': 'Server', 'remoteConnected': true},
    {'status': 'Stopped', 'remote': 'Server', 'remoteConnected': false},
  ]) {
    test('service settings only persist while stopped or remote: $dashboard', () async {
      box.dashboard = dashboard;
      final edits = <Json>[];
      messenger.setMockMethodCallHandler(channel, (call) async {
        expect(call.method, 'settings.set');
        edits.add(json(call.arguments));
        return {'allowBypass': true};
      });
      await box.setSetting('allowBypass', true, autoRestart: true);
      expect(edits, [{'key': 'allowBypass', 'value': true}]);
      expect(box.settings['allowBypass'], true);
      expect(box.applyRequired, null);
    });
  }

  test('service settings cannot change during a local start or stop', () async {
    final calls = <String>[];
    messenger.setMockMethodCallHandler(channel, (call) async { calls.add(call.method); return {}; });
    for (final status in ['Starting', 'Stopping']) {
      box.dashboard = {'status': status};
      await box.setSetting('allowBypass', true, autoRestart: true);
    }
    expect(calls, isEmpty);
    expect(box.serviceSettingsChanging, false);
  });

  test('failed automatic restart reports the error and keeps the recovered native settings', () async {
    final reply = Completer<void>();
    messenger.setMockMethodCallHandler(channel, (call) async {
      if (call.method == 'ready') return {'version': 'test', 'coreVersion': 'test',
        'dashboard': {'status': 'Started'}, 'settings': {'allowBypass': false}};
      expect(call.method, 'settings.set');
      events.add({'type': 'settings', 'value': {'allowBypass': true}});
      await reply.future;
      events.add({'type': 'settings', 'value': {'allowBypass': false}});
      throw PlatformException(code: 'box_error', message: 'Startup failed');
    });
    await box.initialize();
    box.applyRequired = 'Restart';
    final error = box.messages.stream.first;
    final saving = box.setSetting('allowBypass', true, autoRestart: true);
    await Future<void>.delayed(Duration.zero);
    expect(box.settings['allowBypass'], true);
    expect(box.serviceSettingsChanging, true);
    reply.complete();
    await saving;
    await Future<void>.delayed(Duration.zero);
    expect(await error, 'Startup failed');
    expect(box.settings['allowBypass'], false);
    expect(box.serviceSettingsChanging, false);
    expect(box.applyRequired, 'Restart');
  });

  test('core and access settings retain their existing apply behavior', () async {
    box.dashboard = {'status': 'Started'};
    messenger.setMockMethodCallHandler(channel, (call) async {
      expect(json(call.arguments).containsKey('restart'), false);
      return {'oomKillerEnabled': true};
    });
    await box.setSetting('oomKillerEnabled', true);
    expect(box.applyRequired, 'Restart');
    box.clearApplyRequired();
    await box.setSetting('disableDeprecatedWarnings', true, restart: false);
    expect(box.applyRequired, null);
    await box.setSettings({'perAppEnabled': true});
    expect(box.applyRequired, 'Restart');
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
