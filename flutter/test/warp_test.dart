import 'dart:async';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/pages/tools.dart';
import 'package:b_box_ui/pages/warp.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'support/errors.dart';

const _profiles = [
  {'id': 1, 'name': 'Primary configuration', 'remote': false},
  {'id': 2, 'name': 'Another configuration', 'remote': false},
];

Future<BoxController> _mount(WidgetTester tester, {Json dashboard = const {},
  StreamController<dynamic>? eventController, Size size = const Size(360, 800), double scale = 1,
  Future<dynamic> Function(MethodCall)? handler}) async {
  reportFlutterErrors();
  tester.binding.platformDispatcher.localesTestValue = const [Locale('en')];
  addTearDown(tester.binding.platformDispatcher.clearLocalesTestValue);
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  tester.platformDispatcher.textScaleFactorTestValue = scale;
  addTearDown(tester.view.reset);
  addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
  const channel = MethodChannel('test.warp');
  final events = eventController ?? StreamController<dynamic>.broadcast();
  final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  messenger.setMockMethodCallHandler(channel, (call) async {
    if (call.method == 'ready') return {
      'version': 'test', 'coreVersion': 'test', 'profiles': _profiles,
      'dashboard': {'status': 'Stopped', 'selectedProfileId': 1, ...dashboard},
    };
    return await handler?.call(call);
  });
  final box = BoxController(channel: channel, events: events.stream);
  await tester.pumpWidget(BoxApp(controller: box));
  await tester.pumpAndSettle();
  addTearDown(() async {
    await tester.pumpWidget(const SizedBox());
    await events.close();
    messenger.setMockMethodCallHandler(channel, null);
  });
  await tester.tap(find.text('Tools').last);
  await tester.pumpAndSettle();
  return box;
}

Future<void> _open(WidgetTester tester) async {
  final entry = find.descendant(of: find.byType(ToolsPage), matching: find.text('WARP registration'));
  await Scrollable.ensureVisible(tester.element(entry), alignment: .5);
  await tester.pumpAndSettle();
  await tester.tap(entry);
  await tester.pumpAndSettle();
  expect(find.byType(WarpPage), findsOneWidget);
}

Future<void> _reveal(WidgetTester tester, Finder finder) async {
  await Scrollable.ensureVisible(tester.element(finder), alignment: .5);
  await tester.pump(const Duration(milliseconds: 300));
}

void main() {
  testWidgets('tools places WARP between Terminal and network diagnostics and saves HTTP/2 to the selected revision', (tester) async {
    final calls = <MethodCall>[];
    await _mount(tester, handler: (call) async {
      calls.add(call);
      if (call.method == 'warp.snapshot') return {'registering': false};
      if (call.method == 'nodes.list') return {'profileId': 1, 'profileRevision': 'revision-1'};
      if (call.method == 'warp.register') return {'tag': 'Cloudflare', 'profileId': 1, 'added': 1};
      return null;
    });
    final terminal = find.descendant(of: find.byType(ToolsPage), matching: find.text('Terminal'));
    final warp = find.descendant(of: find.byType(ToolsPage), matching: find.text('WARP registration'));
    final diagnostics = find.descendant(of: find.byType(ToolsPage), matching: find.text('Network diagnostics'));
    expect(tester.getCenter(terminal).dy, lessThan(tester.getCenter(warp).dy));
    expect(tester.getCenter(warp).dy, lessThan(tester.getCenter(diagnostics).dy));
    await _open(tester);
    await tester.enterText(find.byKey(const ValueKey('warp-name')), 'Cloudflare');
    await tester.tap(find.text('HTTP/3'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('HTTP/2').hitTestable());
    await tester.pumpAndSettle();
    final action = find.byKey(const ValueKey('warp-register'));
    await _reveal(tester, action);
    await tester.tap(action);
    await tester.pumpAndSettle();
    final args = json(calls.singleWhere((call) => call.method == 'warp.register').arguments);
    expect(args['requestId'], isA<String>());
    expect(args['requestId'], isNotEmpty);
    expect({...args}..remove('requestId'), {'profileId': 1, 'profileRevision': 'revision-1', 'name': 'Cloudflare', 'transport': 'http2'});
    expect(find.text('WARP saved to configuration'), findsWidgets);
    expect(find.text('Cloudflare'), findsWidgets);
    expect(calls.where((call) => call.method == 'profiles.write'), isEmpty);
    expect(tester.takeException(), isNull);
  });

  testWidgets('registration is single flight, guards back and can cancel its own request', (tester) async {
    final pending = Completer<Json>();
    final calls = <MethodCall>[];
    Json? retained;
    await _mount(tester, handler: (call) async {
      calls.add(call);
      if (call.method == 'warp.snapshot') return {'registering': false, if (retained != null) 'pending': retained};
      if (call.method == 'nodes.list') return {'profileId': 1, 'profileRevision': 'revision-1'};
      if (call.method == 'warp.register') return pending.future;
      if (call.method == 'warp.cancel') {
        retained = {'pendingId': 'registered-before-cancel', 'profileId': 1, 'name': 'WARP', 'transport': 'http3'};
        pending.completeError(PlatformException(code: 'cancelled', message: 'Operation cancelled'));
        return true;
      }
      return null;
    });
    await _open(tester);
    final action = find.byKey(const ValueKey('warp-register'));
    await _reveal(tester, action);
    await tester.tap(action);
    await tester.pump();
    await tester.pump();
    expect(tester.widget<FilledButton>(action).onPressed, isNull);
    final guard = tester.widget<BoxEditGuard>(find.descendant(of: find.byType(WarpPage), matching: find.byType(BoxEditGuard)));
    expect(guard.busy, isTrue);
    final popGuard = tester.widget<PopGuard>(find.descendant(of: find.byType(WarpPage), matching: find.byType(PopGuard)).first);
    expect(await popGuard.onPop!(tester.element(find.byType(WarpPage))), isFalse);
    expect(calls.where((call) => call.method == 'warp.register'), hasLength(1));
    final cancel = find.byKey(const ValueKey('warp-cancel'));
    await _reveal(tester, cancel);
    await tester.tap(cancel);
    await tester.pumpAndSettle();
    final registerArgs = json(calls.singleWhere((call) => call.method == 'warp.register').arguments);
    expect(json(calls.singleWhere((call) => call.method == 'warp.cancel').arguments), {'requestId': registerArgs['requestId']});
    expect(find.byType(WarpPage), findsOneWidget);
    expect(tester.widget<FilledButton>(action).onPressed, isNotNull);
    expect(find.text('Registration succeeded; configuration was not saved'), findsOneWidget,
      reason: 'Cancellation after registration must recover the native pending node.');
    expect(calls.where((call) => call.method == 'warp.snapshot'), hasLength(2));
    expect(find.text('WARP saved to configuration'), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('a changed selection while reading the revision prevents registration of the old target', (tester) async {
    final revision = Completer<Json>();
    final events = StreamController<dynamic>.broadcast();
    final calls = <MethodCall>[];
    await _mount(tester, eventController: events, handler: (call) async {
      calls.add(call);
      if (call.method == 'warp.snapshot') return {'registering': false};
      if (call.method == 'nodes.list') return revision.future;
      return null;
    });
    await _open(tester);
    final action = find.byKey(const ValueKey('warp-register'));
    await _reveal(tester, action);
    await tester.tap(action);
    await tester.pump();
    events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'selectedProfileId': 2}});
    await tester.pump();
    revision.complete({'profileId': 1, 'profileRevision': 'old-target'});
    await tester.pumpAndSettle();
    expect(calls.where((call) => call.method == 'warp.register'), isEmpty);
    expect(find.text('Selected profile changed; reload it before saving'), findsOneWidget);
    expect(find.text('WARP saved to configuration'), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('pending registration survives page navigation and retries only the original configuration with its latest revision', (tester) async {
    const operation = {'pendingId': 'operation-1', 'profileId': 1, 'name': 'WARP', 'transport': 'http3'};
    Json? retained;
    final calls = <MethodCall>[];
    var reads = 0;
    await _mount(tester, handler: (call) async {
      calls.add(call);
      if (call.method == 'warp.snapshot') return {'registering': false, if (retained != null) 'pending': retained};
      if (call.method == 'nodes.list') return {'profileId': 1, 'profileRevision': 'revision-${++reads}'};
      if (call.method == 'warp.register') {
        retained = operation;
        throw PlatformException(code: 'warp_pending', message: 'WARP registered, but the configuration could not be saved. Select the original profile and retry saving.',
          details: {...operation, 'privateKey': 'never-display-this-secret'});
      }
      if (call.method == 'warp.retry') { retained = null; return {'profileId': 1, 'tag': 'WARP', 'added': 1}; }
      return null;
    });
    await _open(tester);
    final action = find.byKey(const ValueKey('warp-register'));
    await _reveal(tester, action);
    await tester.tap(action);
    await tester.pumpAndSettle();
    expect(find.text('Registration succeeded; configuration was not saved'), findsOneWidget);
    expect(find.textContaining('never-display-this-secret'), findsNothing);
    tester.element(find.byType(WarpPage)).safeNestedPop();
    await tester.pumpAndSettle();
    await _open(tester);
    expect(find.text('Registration succeeded; configuration was not saved'), findsOneWidget);
    await _reveal(tester, action);
    await tester.tap(action);
    await tester.pumpAndSettle();
    expect(calls.where((call) => call.method == 'warp.register'), hasLength(1));
    expect(json(calls.singleWhere((call) => call.method == 'warp.retry').arguments),
      {'profileId': 1, 'profileRevision': 'revision-2', 'pendingId': 'operation-1'});
    expect(find.text('Registration succeeded; configuration was not saved'), findsNothing);
    expect(find.text('WARP saved to configuration'), findsWidgets);
    expect(tester.takeException(), isNull);
  });

  testWidgets('pending registration cannot write a different selected profile and discard requires the chosen action', (tester) async {
    final events = StreamController<dynamic>.broadcast();
    final calls = <MethodCall>[];
    await _mount(tester, eventController: events, handler: (call) async {
      calls.add(call);
      if (call.method == 'warp.snapshot') return {'registering': false, 'pending':
        {'pendingId': 'operation-1', 'profileId': 1, 'name': 'WARP', 'transport': 'http3'}};
      return true;
    });
    await _open(tester);
    events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'selectedProfileId': 2}});
    await tester.pumpAndSettle();
    expect(tester.widget<FilledButton>(find.byKey(const ValueKey('warp-register'))).onPressed, isNull);
    expect(find.text('Select the original profile before retrying WARP'), findsOneWidget);
    final discard = find.byKey(const ValueKey('warp-discard'));
    await _reveal(tester, discard);
    await tester.tap(discard);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Cancel').hitTestable());
    await tester.pumpAndSettle();
    expect(calls.where((call) => call.method == 'warp.discard'), isEmpty);
    await tester.tap(discard);
    await tester.pumpAndSettle();
    await tester.tap(find.descendant(of: find.byType(AlertDialog), matching: find.text('Discard')).hitTestable());
    await tester.pumpAndSettle();
    expect(json(calls.singleWhere((call) => call.method == 'warp.discard').arguments), {'pendingId': 'operation-1'});
    expect(find.text('Registration succeeded; configuration was not saved'), findsNothing);
    expect(calls.where((call) => call.method == 'warp.retry'), isEmpty);
    expect(tester.takeException(), isNull);
  });

  for (final dashboard in <Json>[
    {'remote': 'Server', 'remoteConnected': true}, {'status': 'Starting'}, {'status': 'Stopping'}, {'selectedProfileId': -1},
  ]) {
    testWidgets('WARP does not register while unavailable: $dashboard', (tester) async {
      final calls = <MethodCall>[];
      await _mount(tester, dashboard: dashboard, handler: (call) async {
        calls.add(call);
        if (call.method == 'warp.snapshot') return {'registering': false};
        return null;
      });
      await _open(tester);
      if (dashboard.containsKey('remote')) {
        expect(find.text('Disconnect remote session to register WARP'), findsOneWidget);
        expect(calls.where((call) => call.method.startsWith('warp.')), isEmpty);
      } else {
        expect(tester.widget<FilledButton>(find.byKey(const ValueKey('warp-register'))).onPressed, isNull);
      }
      expect(calls.where((call) => call.method == 'nodes.list' || call.method == 'warp.register' || call.method == 'warp.retry'), isEmpty);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('a remote session begun during revision loading blocks the delayed local registration', (tester) async {
    final revision = Completer<Json>();
    final events = StreamController<dynamic>.broadcast();
    final calls = <MethodCall>[];
    await _mount(tester, eventController: events, handler: (call) async {
      calls.add(call);
      if (call.method == 'warp.snapshot') return {'registering': false};
      if (call.method == 'nodes.list') return revision.future;
      return null;
    });
    await _open(tester);
    final action = find.byKey(const ValueKey('warp-register'));
    await _reveal(tester, action);
    await tester.tap(action);
    await tester.pump();
    events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'selectedProfileId': 1, 'remote': 'Server', 'remoteConnected': true}});
    await tester.pump();
    revision.complete({'profileId': 1, 'profileRevision': 'revision-1'});
    await tester.pumpAndSettle();
    expect(find.text('Disconnect remote session to register WARP'), findsOneWidget);
    expect(calls.where((call) => call.method == 'warp.register'), isEmpty);
    expect(find.text('WARP saved to configuration'), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('WARP remains usable at 320 pixels with large text and exposes the Cloudflare terms link', (tester) async {
    final calls = <MethodCall>[];
    await _mount(tester, size: const Size(320, 800), scale: 1.4, handler: (call) async {
      calls.add(call);
      if (call.method == 'warp.snapshot') return {'registering': false};
      return true;
    });
    await _open(tester);
    final field = find.byKey(const ValueKey('warp-name'));
    final action = find.byKey(const ValueKey('warp-register'));
    expect(tester.getRect(field).left, greaterThanOrEqualTo(16));
    expect(tester.getRect(field).right, lessThanOrEqualTo(304));
    await _reveal(tester, action);
    expect(action.hitTestable(), findsOneWidget);
    expect(tester.getSize(action).width, lessThanOrEqualTo(288));
    final terms = find.text('Cloudflare WARP terms of service');
    await _reveal(tester, terms);
    await tester.tap(terms);
    await tester.pumpAndSettle();
    expect(json(calls.singleWhere((call) => call.method == 'url.open').arguments), {'url': 'https://www.cloudflare.com/application/terms/'});
    expect(tester.takeException(), isNull);
  });
}
