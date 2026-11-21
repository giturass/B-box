import 'dart:async';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/pages/subscriptions.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

const _uri = 'sing-box://import-remote-profile?url=https%3A%2F%2Fexample.test%2Fnodes';
const _id = 'bf1d8af5-9f2d-4f38-a354-10d94458fdba';
const _input = {'id': _id, 'uri': _uri};
const _second = {'id': 'd19bdc9d-69e9-44c3-af23-d66ff99db9b4', 'uri': _uri};
const _third = {'id': 'a4601c3a-33b7-469c-b23e-a0b6f0b24361', 'uri': _uri};
const _decoded = {'name': 'Pending source', 'url': 'https://example.test/nodes'};

class _IntentFixture {
  _IntentFixture(this.box, this.events, this.nativePending, this.calls);
  final BoxController box;
  final StreamController<dynamic> events;
  final List<Json> nativePending;
  final List<MethodCall> calls;

  void reconnect(List<Json> restored) {
    nativePending.addAll(restored);
    events.add({'type': 'backendReconnected', 'value': null});
  }

  List<MethodCall> callsFor(String method) => calls.where((call) => call.method == method).toList();
}

Future<_IntentFixture> _mountIntent(WidgetTester tester, {
  bool provideRevision = true,
  List<Json> initialInputs = const [_input],
  bool settle = true,
  Future<Json> Function(Json)? readyHandler,
  Future<dynamic> Function(MethodCall)? handler,
}) async {
  tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
  tester.binding.platformDispatcher.localesTestValue = const [Locale('en')];
  tester.view.physicalSize = const Size(360, 800);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.binding.platformDispatcher.clearLocalesTestValue);
  addTearDown(tester.view.reset);
  const channel = MethodChannel('test.intent-lifecycle');
  final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  final events = StreamController<dynamic>.broadcast();
  final calls = <MethodCall>[], nativePending = List<Json>.of(initialInputs);
  final box = BoxController(channel: channel, events: events.stream);
  messenger.setMockMethodCallHandler(channel, (call) async {
    calls.add(call);
    if (call.method == 'ready') {
      final snapshot = <String, dynamic>{
        'version': 'test', 'coreVersion': 'test',
        'intent': nativePending.isEmpty ? null : nativePending.removeAt(0),
        'dashboard': {'status': 'Stopped', 'selectedProfileId': 1},
        'profiles': [{'id': 1, 'name': 'Profile file', 'remote': false, 'default': true}],
        'nodes': {'profileId': 1, if (provideRevision) 'profileRevision': 'file-r', 'nodes': []},
      };
      return readyHandler == null ? snapshot : readyHandler(snapshot);
    }
    if (call.method == 'intent.take') return nativePending.isEmpty ? null : nativePending.removeAt(0);
    if (handler != null) return handler(call);
    if (call.method == 'profiles.decodeIntent') return _decoded;
    return null;
  });
  addTearDown(() async {
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pumpWidget(const SizedBox());
    await events.close();
    messenger.setMockMethodCallHandler(channel, null);
  });
  await tester.pumpWidget(BoxApp(controller: box));
  if (settle) { await tester.pumpAndSettle(); } else { await tester.pump(); }
  return _IntentFixture(box, events, nativePending, calls);
}

void _resume(WidgetTester tester) {
  tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
  tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
}

Future<void> _dismissImport(WidgetTester tester) async {
  final dismiss = SheetDismissScope.of(tester.element(find.byType(SubscriptionForm)))!.value!();
  await tester.pumpAndSettle();
  await dismiss;
}

void main() {
  testWidgets('an import is acknowledged by ID only after its form is dismissed', (tester) async {
    final fixture = await _mountIntent(tester);
    expect(find.byType(SubscriptionForm), findsOneWidget);
    expect(fixture.callsFor('intent.complete'), isEmpty);
    await _dismissImport(tester);
    expect(find.byType(SubscriptionForm), findsNothing);
    expect(fixture.callsFor('subscriptions.import'), isEmpty);
    expect(fixture.callsFor('intent.complete').map((call) => call.arguments), [{'id': _id}]);
    expect(fixture.box.pendingIntents, isEmpty);
    expect(tester.takeException(), isNull);
  });

  testWidgets('saving acknowledges the intent only after the native save finishes', (tester) async {
    final save = Completer<dynamic>();
    final fixture = await _mountIntent(tester, handler: (call) async {
      if (call.method == 'profiles.decodeIntent') return _decoded;
      if (call.method == 'subscriptions.import') return save.future;
      return null;
    });
    await tester.tap(find.byTooltip('Save').last);
    await tester.pump();
    expect(find.byType(SubscriptionForm), findsOneWidget);
    expect(fixture.callsFor('intent.complete'), isEmpty);
    save.complete({'added': 1, 'skipped': 0, 'warnings': []});
    await tester.pumpAndSettle();
    expect(find.byType(SubscriptionForm), findsNothing);
    expect(fixture.callsFor('intent.complete').map((call) => call.arguments), [{'id': _id}]);
    expect(tester.takeException(), isNull);
  });

  for (final recoveryBeforeReply in [true, false]) {
    testWidgets('native recovery restores cold startup cancelled ${recoveryBeforeReply ? 'after' : 'before'} its confirmation', (tester) async {
      final firstReady = Completer<Json>();
      var readyReads = 0;
      final fixture = await _mountIntent(tester, settle: !recoveryBeforeReply, readyHandler: (snapshot) async {
        if (++readyReads == 1) {
          if (recoveryBeforeReply) return firstReady.future;
          throw PlatformException(code: 'cancelled');
        }
        return snapshot;
      });
      expect(fixture.box.ready, isFalse);
      fixture.nativePending.add(_input);
      fixture.events.add({'type': 'intentRecovery', 'value': true});
      if (recoveryBeforeReply) {
        await tester.pump();
        firstReady.completeError(PlatformException(code: 'cancelled'));
      }
      await tester.pumpAndSettle();
      expect(readyReads, 2);
      expect(fixture.box.ready, isTrue);
      expect(fixture.box.startupError, isNull);
      expect(find.byType(SubscriptionForm), findsOneWidget);
      expect(fixture.callsFor('profiles.decodeIntent').length, 1);
      expect(fixture.callsFor('intent.complete'), isEmpty);
      await _dismissImport(tester);
      expect(fixture.callsFor('intent.complete').map((call) => call.arguments), [{'id': _id}]);
      expect(tester.takeException(), isNull);
    });
  }

  for (final code in ['cancelled', 'ui_bridge']) {
    testWidgets('an interrupted $code decode waits for a new wake without retrying forever', (tester) async {
      final fixture = await _mountIntent(tester, handler: (call) async {
        if (call.method == 'profiles.decodeIntent') throw PlatformException(code: code);
        return null;
      });
      expect(find.byType(SubscriptionForm), findsNothing);
      expect(fixture.callsFor('profiles.decodeIntent').length, 1);
      expect(fixture.callsFor('intent.complete'), isEmpty);
      expect(fixture.box.pendingIntents.single.id, _id);
      await tester.pump(const Duration(seconds: 1));
      expect(fixture.callsFor('profiles.decodeIntent').length, 1);
      _resume(tester);
      await tester.pumpAndSettle();
      expect(fixture.callsFor('profiles.decodeIntent').length, 2);
      await tester.pump(const Duration(seconds: 1));
      expect(fixture.callsFor('profiles.decodeIntent').length, 2);
      expect(fixture.callsFor('intent.complete'), isEmpty);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('a quick foreground return retries a decode interrupted after the resume wake', (tester) async {
    final decode = Completer<dynamic>();
    var decodes = 0;
    final fixture = await _mountIntent(tester, handler: (call) async {
      if (call.method == 'profiles.decodeIntent') return ++decodes == 1 ? decode.future : _decoded;
      return null;
    });
    expect(decodes, 1);
    expect(find.byType(SubscriptionForm), findsNothing);
    _resume(tester);
    decode.completeError(PlatformException(code: 'cancelled'));
    await tester.pumpAndSettle();
    expect(decodes, 2);
    expect(find.byType(SubscriptionForm), findsOneWidget);
    expect(fixture.callsFor('intent.complete'), isEmpty);
    await _dismissImport(tester);
    expect(fixture.callsFor('intent.complete').map((call) => call.arguments), [{'id': _id}]);
    expect(tester.takeException(), isNull);
  });

  testWidgets('native foreground recovery retries after Dart resumed still saw a closing bridge', (tester) async {
    var decodes = 0;
    final fixture = await _mountIntent(tester, handler: (call) async {
      if (call.method == 'profiles.decodeIntent') {
        if (++decodes < 3) throw PlatformException(code: 'cancelled');
        return _decoded;
      }
      return null;
    });
    expect(decodes, 1);
    _resume(tester);
    await tester.pumpAndSettle();
    expect(decodes, 2);
    expect(find.byType(SubscriptionForm), findsNothing);
    await tester.pump(const Duration(seconds: 1));
    expect(decodes, 2);
    // Native foreground control emits this after bridge admission and replay.
    fixture.events.add({'type': 'intentRecovery', 'value': true});
    await tester.pumpAndSettle();
    expect(decodes, 3);
    expect(find.byType(SubscriptionForm), findsOneWidget);
    expect(fixture.callsFor('intent.complete'), isEmpty);
    await _dismissImport(tester);
    expect(fixture.callsFor('intent.complete').length, 1);
    expect(tester.takeException(), isNull);
  });

  testWidgets('a cancelled revision lookup stays queued and retries after a quick foreground return', (tester) async {
    final lookup = Completer<dynamic>();
    var lookups = 0;
    final fixture = await _mountIntent(tester, provideRevision: false, handler: (call) async {
      if (call.method == 'profiles.decodeIntent') return _decoded;
      if (call.method == 'nodes.list') return ++lookups == 1 ? lookup.future : {'profileRevision': 'file-r'};
      return null;
    });
    expect(lookups, 1);
    expect(find.byType(SubscriptionForm), findsNothing);
    _resume(tester);
    lookup.completeError(PlatformException(code: 'ui_bridge'));
    await tester.pumpAndSettle();
    expect(lookups, 2);
    expect(find.byType(SubscriptionForm), findsOneWidget);
    expect(fixture.callsFor('intent.complete'), isEmpty);
    await _dismissImport(tester);
    expect(fixture.callsFor('intent.complete').map((call) => call.arguments), [{'id': _id}]);
    expect(tester.takeException(), isNull);
  });

  testWidgets('a restored owned input wakes an interrupted decode after backend reconnection', (tester) async {
    var decodes = 0;
    final fixture = await _mountIntent(tester, handler: (call) async {
      if (call.method == 'profiles.decodeIntent') {
        if (++decodes == 1) throw PlatformException(code: 'ui_bridge');
        return _decoded;
      }
      return null;
    });
    expect(fixture.box.pendingIntents.single.id, _id);
    fixture.reconnect([_input]);
    await tester.pumpAndSettle();
    expect(decodes, 2);
    expect(find.byType(SubscriptionForm), findsOneWidget);
    expect(fixture.box.pendingIntents, isEmpty);
    await _dismissImport(tester);
    expect(fixture.callsFor('intent.complete').map((call) => call.arguments), [{'id': _id}]);
    expect(tester.takeException(), isNull);
  });

  testWidgets('backend replay preserves an open form and never reopens its acknowledged input', (tester) async {
    final fixture = await _mountIntent(tester);
    final form = tester.state(find.byType(SubscriptionForm));
    fixture.reconnect([_input]);
    await tester.pumpAndSettle();
    expect(identical(tester.state(find.byType(SubscriptionForm)), form), isTrue);
    expect(fixture.callsFor('profiles.decodeIntent').length, 1);
    expect(fixture.box.pendingIntents, isEmpty);
    await _dismissImport(tester);
    expect(find.byType(SubscriptionForm), findsNothing);
    expect(fixture.callsFor('intent.complete').length, 1);
    fixture.reconnect([_input]);
    await tester.pumpAndSettle();
    expect(find.byType(SubscriptionForm), findsNothing);
    expect(fixture.callsFor('profiles.decodeIntent').length, 1);
    expect(fixture.callsFor('intent.complete').length, 1);
    expect(tester.takeException(), isNull);
  });

  testWidgets('restored queues drain past an owned replay and preserve separate identical links', (tester) async {
    final fixture = await _mountIntent(tester);
    fixture.reconnect([_input, _second, _third]);
    await tester.pumpAndSettle();
    expect(find.byType(SubscriptionForm), findsOneWidget);
    expect(fixture.callsFor('profiles.decodeIntent').length, 1);
    expect(fixture.box.pendingIntents.map((input) => input.id), [_second['id'], _third['id']]);
    expect(fixture.nativePending, isEmpty);
    await _dismissImport(tester);
    expect(find.byType(SubscriptionForm), findsOneWidget);
    expect(fixture.callsFor('profiles.decodeIntent').length, 2);
    await _dismissImport(tester);
    expect(find.byType(SubscriptionForm), findsOneWidget);
    expect(fixture.callsFor('profiles.decodeIntent').length, 3);
    await _dismissImport(tester);
    expect(find.byType(SubscriptionForm), findsNothing);
    expect(fixture.callsFor('intent.complete').map((call) => json(call.arguments)['id']),
      [_id, _second['id'], _third['id']]);
    expect(tester.takeException(), isNull);
  });

  for (final code in ['ui_bridge', 'box_error']) {
    testWidgets('a failed $code acknowledgement retries without reopening the finished form', (tester) async {
      var acknowledgements = 0;
      final fixture = await _mountIntent(tester, handler: (call) async {
        if (call.method == 'profiles.decodeIntent') return _decoded;
        if (call.method == 'intent.complete' && ++acknowledgements == 1) throw PlatformException(code: code);
        return null;
      });
      await _dismissImport(tester);
      expect(find.byType(SubscriptionForm), findsNothing);
      expect(fixture.box.pendingIntents.single.id, _id);
      expect(fixture.box.pendingIntents.single.consumed, isTrue);
      expect(acknowledgements, 1);
      await tester.pump(const Duration(seconds: 1));
      expect(acknowledgements, 1);
      fixture.reconnect([_input]);
      await tester.pumpAndSettle();
      expect(acknowledgements, 2);
      expect(fixture.box.pendingIntents, isEmpty);
      expect(find.byType(SubscriptionForm), findsNothing);
      expect(fixture.callsFor('profiles.decodeIntent').length, 1);
      expect(fixture.callsFor('intent.complete').map((call) => call.arguments), [{'id': _id}, {'id': _id}]);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('ordinary decode errors finish and acknowledge their input once', (tester) async {
    final fixture = await _mountIntent(tester, handler: (call) async {
      if (call.method == 'profiles.decodeIntent') throw PlatformException(code: 'box_error', message: 'Invalid input');
      return null;
    });
    expect(find.byType(SubscriptionForm), findsNothing);
    expect(fixture.callsFor('intent.complete').map((call) => call.arguments), [{'id': _id}]);
    expect(fixture.box.pendingIntents, isEmpty);
    fixture.reconnect([_input]);
    await tester.pumpAndSettle();
    expect(fixture.callsFor('profiles.decodeIntent').length, 1);
    expect(fixture.callsFor('intent.complete').length, 1);
    expect(tester.takeException(), isNull);
  });
}
