import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/common/byte_input.dart';
import 'package:b_box_ui/common/http_message.dart';
import 'package:b_box_ui/pages/breakpoints.dart';
import 'package:b_box_ui/pages/request_replay.dart';
import 'package:b_box_ui/pages/requests.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'support/capture_query.dart';

void main() {
  late BoxController box;
  late StreamController<dynamic> events;
  late List<MethodCall> calls;
  late Json snapshot;
  late Json breakpoints;
  Future<dynamic> Function(MethodCall)? extra;

  const request = <String, dynamic>{'url': 'https://example.test/path', 'method': 'POST', 'protocol': 'HTTP/1.1',
    'bodyComplete': true, 'headers': [{'name': 'X-Request', 'value': 'one'}, {'name': 'Accept', 'value': '*/*'}]};

  setUpAll(() async {
    final mono = await File('assets/fonts/JetBrainsMono-Regular.ttf').readAsBytes();
    await (FontLoader('JetBrainsMono')..addFont(Future.value(ByteData.sublistView(mono)))).load();
  });

  Future<void> mount(WidgetTester tester, {bool remote = false}) async {
    tester.binding.platformDispatcher.localesTestValue = const [Locale('en')];
    addTearDown(tester.binding.platformDispatcher.clearLocalesTestValue);
    tester.view.physicalSize = const Size(360, 900);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    calls = [];
    snapshot = {'state': {'active': false}, 'config': {}, 'page': {'items': [], 'total': 0, 'storedBytes': 0}};
    breakpoints = {'config': {'enabled': false, 'rules': []}, 'paused': []};
    const channel = MethodChannel('test.replay');
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(channel, (call) async {
      calls.add(call);
      if (call.method == 'ready') return {
        'version': 'test', 'coreVersion': 'test', 'preferences': {'theme': 'light'},
        'dashboard': {'status': 'Started', if (remote) ...{'remote': 'Server', 'remoteConnected': true}},
      };
      if (extra != null) {
        final result = await extra!(call);
        if (result != null) return result;
      }
      if (call.method == 'capture.query') return captureQueryResult(snapshot, json(call.arguments), box.connections);
      if (call.method == 'capture.breakpoints') return breakpoints;
      if (call.method == 'capture.breakpoints.set') {
        breakpoints = {...breakpoints, 'config': json(call.arguments)['config']};
        return breakpoints;
      }
      if (call.method == 'capture.breakpoint.resolve') return true;
      return null;
    });
    events = StreamController<dynamic>.broadcast();
    box = BoxController(channel: channel, events: events.stream);
    await tester.pumpWidget(BoxApp(controller: box));
    await tester.pumpAndSettle();
    addTearDown(() async {
      extra = null;
      await tester.pumpWidget(const SizedBox());
      await events.close();
      messenger.setMockMethodCallHandler(channel, null);
    });
  }

  Future<void> open(WidgetTester tester, Widget page) async {
    // An earlier page leaves the home page offstage.
    openPage(tester.element(find.byType(HomePage, skipOffstage: false)), page);
    await tester.pumpAndSettle();
  }

  Future<void> enter(WidgetTester tester, String key, String text) async {
    final field = find.byKey(ValueKey(key));
    await tester.ensureVisible(field);
    await tester.enterText(field, text);
    await tester.pumpAndSettle();
  }

  Future<void> choose(WidgetTester tester, String key, String option) async {
    final row = find.byKey(ValueKey(key));
    await Scrollable.ensureVisible(tester.element(row), alignment: .5);
    await tester.pumpAndSettle();
    await tester.tap(row.hitTestable());
    await tester.pumpAndSettle();
    await tester.tap(find.text(option).last.hitTestable());
    await tester.pumpAndSettle();
  }

  Future<void> press(WidgetTester tester, String tooltip) async {
    await tester.tap(find.byTooltip(tooltip).last);
    await tester.pumpAndSettle();
  }

  Future<void> flip(WidgetTester tester, String key) async {
    await tester.tap(find.descendant(of: find.byKey(ValueKey(key)), matching: find.byType(Switch)));
    await tester.pumpAndSettle();
  }

  Json arguments(String method) => json(calls.lastWhere((call) => call.method == method).arguments);

  Json replayArguments() {
    final args = arguments('capture.replay');
    expect(args['requestId'], isNotEmpty);
    return {...args}..remove('requestId');
  }

  Json paused(String phase, {bool bodyEditable = true, String body = 'hello'}) => {
    'id': 'p1', 'exchangeId': 'x1', 'phase': phase, 'pausedAt': 1700000000000, 'method': 'POST',
    'url': 'https://api.test/login', if (phase == 'response') 'status': 200,
    'headers': [{'name': 'X-Trace', 'value': 'one'}], 'bodyEditable': bodyEditable, 'bodyBytes': body.length,
  };

  /// Lists one held message and answers its detail read with [body].
  void hold(String phase, {bool bodyEditable = true, String body = 'hello'}) {
    final message = paused(phase, bodyEditable: bodyEditable, body: body);
    breakpoints = {'config': {'enabled': true, 'rules': [{'enabled': true, 'request': true, 'response': true}]}, 'paused': [message]};
    extra = (call) async => call.method == 'capture.breakpoint.detail'
        ? {...message, if (bodyEditable) 'body': base64.encode(utf8.encode(body))} : null;
  }

  test('byte input reads and writes text, hexadecimal and Base64', () {
    expect(parseByteInput('Text', 'é'), [0xc3, 0xa9]);
    expect(parseByteInput('Hex', 'c3 A9\n00'), [0xc3, 0xa9, 0]);
    expect(parseByteInput('Base64', 'w6k='), [0xc3, 0xa9]);
    expect(() => parseByteInput('Hex', 'c3a'), throwsFormatException);
    expect(() => parseByteInput('Base64', '!'), throwsFormatException);
    expect(formatByteInput('Text', [0xc3, 0xa9]), 'é');
    expect(formatByteInput('Text', [0xff]), isNull);
    expect(parseByteInput('Text', formatByteInput('Text', [0xef, 0xbb, 0xbf, 0x68])!), [0xef, 0xbb, 0xbf, 0x68]);
    expect(formatByteInput('Hex', [0xc3, 0xa9, 0]), 'c3 a9 00');
    expect(formatByteInput('Base64', [0xc3, 0xa9]), 'w6k=');
  });

  test('header lines keep repeated names and colons in values', () {
    final headers = [{'name': 'Set-Cookie', 'value': 'a=1'}, {'name': 'Set-Cookie', 'value': 'b=2'}, {'name': 'Referer', 'value': 'https://example.test/'}];
    final text = headerLines(headers);
    expect(text, 'Set-Cookie: a=1\nSet-Cookie: b=2\nReferer: https://example.test/');
    expect(parseHeaderLines('$text\n\n'), headers);
    expect(parseHeaderLines('X-Empty:'), [{'name': 'X-Empty', 'value': ''}]);
    expect(() => parseHeaderLines('no separator'), throwsFormatException);
    expect(() => parseHeaderLines(': value'), throwsFormatException);
  });

  testWidgets('a detail replays its request with only the edited fields and opens the new record', (tester) async {
    await mount(tester);
    extra = (call) async {
      if (call.method == 'capture.detail') {
        final id = json(call.arguments)['id'];
        return {'id': id, 'complete': true, 'request': {...request, if (id == 'replayed') 'url': 'https://example.test/other'},
          'response': {'status': 200, 'bodyComplete': true, 'headers': []}};
      }
      if (call.method == 'capture.replay') return {'id': 'replayed', 'status': 200};
      return null;
    };
    await open(tester, const RequestDetailPage(id: 'one'));
    final page = find.byType(RequestDetailPage).last;
    await tester.tap(find.descendant(of: find.descendant(of: page, matching: find.byType(ScaffoldBar)).first, matching: find.byTooltip('More')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Replay request').last);
    await tester.pumpAndSettle();
    expect(find.byType(RequestReplayPage), findsOneWidget);
    expect(tester.widget<TextField>(find.byKey(const ValueKey('replay-headers'))).controller!.text, 'X-Request: one\nAccept: */*');
    expect(find.byKey(const ValueKey('replay-body')), findsNothing);
    await enter(tester, 'replay-url', 'https://example.test/other');
    await press(tester, 'Send');
    expect(replayArguments(), {'id': 'one', 'options': {'url': 'https://example.test/other'}});
    expect(find.byType(RequestReplayPage), findsNothing);
    expect(find.text('Request replayed'), findsOneWidget);
    expect(tester.widget<RequestDetailPage>(find.byType(RequestDetailPage).last).id, 'replayed');
    expect(tester.takeException(), isNull);
  });

  testWidgets('a replay sends replacement headers and body and keeps the draft when it is rejected', (tester) async {
    await mount(tester);
    var reject = true;
    extra = (call) async {
      if (call.method != 'capture.replay') return null;
      if (reject) throw PlatformException(code: 'replay', message: 'Request body is incomplete');
      return {'id': 'failed-send', 'error': 'connection refused'};
    };
    await open(tester, RequestReplayPage(id: 'one', request: {...request, 'headersTruncated': true}));
    expect(find.text('The captured headers are truncated. Complete them before sending.'), findsOneWidget);
    await enter(tester, 'replay-method', 'put');
    await enter(tester, 'replay-headers', 'broken');
    await press(tester, 'Send');
    expect(find.text('Enter each header as Name: value'), findsOneWidget);
    expect(calls.where((call) => call.method == 'capture.replay'), isEmpty);
    await enter(tester, 'replay-headers', 'X-Request: two');
    await choose(tester, 'replay-body-source', 'Hex');
    await enter(tester, 'replay-body', '00 ff');
    await press(tester, 'Send');
    expect(find.text('Request body is incomplete'), findsOneWidget);
    expect(tester.widget<TextField>(find.byKey(const ValueKey('replay-body'))).controller!.text, '00 ff');
    final sent = {'id': 'one', 'options': {'method': 'put', 'headers': [{'name': 'X-Request', 'value': 'two'}], 'body': base64.encode([0, 0xff])}};
    expect(replayArguments(), sent);
    reject = false;
    await press(tester, 'Send');
    expect(replayArguments(), sent);
    // A send that failed on the wire is still a record; its error is reported.
    expect(find.byType(RequestReplayPage), findsNothing);
    expect(find.text('connection refused'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('a typed replay body keeps its bytes when its format changes', (tester) async {
    await mount(tester);
    await open(tester, RequestReplayPage(id: 'one', request: request));
    String body() => tester.widget<TextField>(find.byKey(const ValueKey('replay-body'))).controller!.text;
    await choose(tester, 'replay-body-source', 'Text');
    await enter(tester, 'replay-body', 'hi');
    await choose(tester, 'replay-body-source', 'Hex');
    expect(body(), '68 69');
    await enter(tester, 'replay-body', '68 ff');
    await choose(tester, 'replay-body-source', 'Text');
    expect(find.text('This body is not UTF-8 text'), findsOneWidget);
    expect(body(), '68 ff');
    await choose(tester, 'replay-body-source', 'Base64');
    expect(find.text('This body is not UTF-8 text'), findsNothing);
    expect(body(), 'aP8=');
    // The captured body has no typed form, so passing through it leaves what was typed as it is.
    await choose(tester, 'replay-body-source', 'Captured body');
    expect(find.byKey(const ValueKey('replay-body')), findsNothing);
    await choose(tester, 'replay-body-source', 'Hex');
    expect(body(), 'aP8=');
    expect(calls.where((call) => call.method == 'capture.replay'), isEmpty);
    expect(tester.takeException(), isNull);
  });

  testWidgets('an unchanged replay sends no options and a running one is cancelled by request ID', (tester) async {
    await mount(tester);
    final pending = Completer<dynamic>();
    String? requestId;
    extra = (call) async {
      if (call.method == 'capture.replay') {
        requestId = json(call.arguments)['requestId'] as String;
        return pending.future;
      }
      if (call.method == 'capture.replay.cancel') {
        expect(json(call.arguments), {'requestId': requestId});
        pending.completeError(PlatformException(code: 'cancelled', message: 'Operation cancelled'));
        return true;
      }
      return null;
    };
    await open(tester, const RequestReplayPage(id: 'slow', request: request));
    await tester.tap(find.byTooltip('Send').last);
    await tester.pump(const Duration(milliseconds: 400));
    expect(replayArguments(), {'id': 'slow', 'options': <String, dynamic>{}});
    expect(find.byTooltip('Cancel replay').hitTestable(), findsOneWidget);
    await tester.tap(find.byTooltip('Cancel replay').last);
    await tester.pumpAndSettle();
    expect(find.text('Replay cancelled'), findsOneWidget);
    expect(find.byType(RequestReplayPage), findsOneWidget);
    expect(calls.where((call) => call.method == 'capture.replay.cancel').length, 1);
    expect(tester.takeException(), isNull);
  });

  testWidgets('remote sessions cannot replay or use breakpoints', (tester) async {
    await mount(tester, remote: true);
    await open(tester, const RequestReplayPage(id: 'one', request: request));
    expect(find.text('HTTP debugging is only available for the local service'), findsOneWidget);
    expect(find.byKey(const ValueKey('replay-url')), findsNothing);
    await open(tester, const BreakpointsPage());
    expect(find.byKey(const ValueKey('breakpoints-enabled')), findsNothing);
    await open(tester, const PausedMessagePage(id: 'p1'));
    expect(find.text('HTTP debugging is only available for the local service'), findsWidgets);
    expect(calls.where((call) => call.method.startsWith('capture.')), isEmpty);
    expect(tester.takeException(), isNull);
  });

  testWidgets('the request list shows held messages and opens breakpoints from its menu', (tester) async {
    await mount(tester);
    await open(tester, const RequestsPage());
    expect(find.byKey(const ValueKey('capture-breakpoints')), findsNothing);
    await tester.tap(find.byTooltip('More').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Breakpoints').last);
    await tester.pumpAndSettle();
    expect(find.byType(BreakpointsPage), findsOneWidget);
    expect(find.text('No breakpoint rules'), findsOneWidget);
    expect(find.text('No paused messages'), findsOneWidget);
    snapshot['state'] = {'active': true, 'breakpointRules': 2, 'pausedMessages': 1};
    Navigator.pop(tester.element(find.byType(BreakpointsPage)));
    await tester.pumpAndSettle();
    expect(find.text('2 active rules · 1 paused'), findsOneWidget);
    await tester.tap(find.byKey(const ValueKey('capture-breakpoints')));
    await tester.pumpAndSettle();
    expect(find.byType(BreakpointsPage), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('breakpoint rules are added, edited, switched off and deleted', (tester) async {
    await mount(tester);
    await open(tester, const BreakpointsPage());
    await press(tester, 'Add rule');
    await enter(tester, 'breakpoint-url', ' api.test/login ');
    await enter(tester, 'breakpoint-method', 'post');
    await flip(tester, 'breakpoint-request');
    await press(tester, 'Save');
    expect(find.text('Pause requests, responses or both'), findsOneWidget);
    expect(calls.where((call) => call.method == 'capture.breakpoints.set'), isEmpty);
    await flip(tester, 'breakpoint-response');
    await press(tester, 'Save');
    // The first rule switches breakpoints on.
    expect(arguments('capture.breakpoints.set'), {'config': {'enabled': true, 'rules': [
      {'enabled': true, 'url': 'api.test/login', 'method': 'POST', 'request': false, 'response': true}]}});
    expect(find.byType(BreakpointRulePage), findsNothing);
    expect(find.text('api.test/login'), findsOneWidget);
    expect(find.text('POST · Response'), findsOneWidget);
    expect(find.text('Matching messages wait here for a decision'), findsOneWidget);

    await tester.tap(find.text('Pause matching messages'));
    await tester.pumpAndSettle();
    expect(json(arguments('capture.breakpoints.set')['config'])['enabled'], isFalse);
    expect(rows(json(arguments('capture.breakpoints.set')['config'])['rules']), hasLength(1));

    await tester.tap(find.byKey(const ValueKey('breakpoint-rule-0')));
    await tester.pumpAndSettle();
    await enter(tester, 'breakpoint-url', '');
    await enter(tester, 'breakpoint-method', '');
    await flip(tester, 'breakpoint-rule-enabled');
    await press(tester, 'Save');
    // Editing leaves the switch as the user set it, and empty filters are omitted.
    expect(arguments('capture.breakpoints.set'), {'config': {'enabled': false, 'rules': [
      {'enabled': false, 'request': false, 'response': true}]}});
    expect(find.text('Any URL'), findsOneWidget);
    expect(find.text('Disabled · Any method · Response'), findsOneWidget);

    await tester.tap(find.byKey(const ValueKey('breakpoint-rule-0')));
    await tester.pumpAndSettle();
    // The last row starts below the edge of the sheet.
    final delete = find.byKey(const ValueKey('breakpoint-delete'));
    await tester.dragUntilVisible(delete, find.byKey(const ValueKey('breakpoint-rule-enabled')), const Offset(0, -100));
    await tester.pumpAndSettle();
    await tester.tap(delete);
    await tester.pumpAndSettle();
    expect(arguments('capture.breakpoints.set'), {'config': {'enabled': false, 'rules': []}});
    expect(find.text('No breakpoint rules'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('a held request continues with only the edited fields', (tester) async {
    await mount(tester);
    hold('request');
    await open(tester, const BreakpointsPage());
    expect(find.text('POST https://api.test/login'), findsOneWidget);
    await tester.tap(find.byKey(const ValueKey('paused:p1')));
    await tester.pumpAndSettle();
    expect(arguments('capture.breakpoint.detail'), {'id': 'p1'});
    expect(find.byKey(const ValueKey('paused-status')), findsNothing);
    expect(tester.widget<TextField>(find.byKey(const ValueKey('paused-body'))).controller!.text, 'hello');
    await enter(tester, 'paused-headers', 'X-Trace one');
    await press(tester, 'Continue');
    expect(find.text('Enter each header as Name: value'), findsOneWidget);
    expect(calls.where((call) => call.method == 'capture.breakpoint.resolve'), isEmpty);
    await enter(tester, 'paused-headers', 'X-Trace: one\nX-Added: two');
    await enter(tester, 'paused-url', 'https://api.test/logout');
    await enter(tester, 'paused-body', 'world');
    breakpoints = {...breakpoints, 'paused': []};
    await press(tester, 'Continue');
    expect(arguments('capture.breakpoint.resolve'), {'id': 'p1', 'decision': {'action': 'continue', 'url': 'https://api.test/logout',
      'headers': [{'name': 'X-Trace', 'value': 'one'}, {'name': 'X-Added', 'value': 'two'}], 'body': base64.encode(utf8.encode('world'))}});
    expect(find.byType(PausedMessagePage), findsNothing);
    expect(find.text('Message resumed'), findsOneWidget);
    expect(find.text('No paused messages'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('an untouched held message continues unchanged and its body changes format in place', (tester) async {
    await mount(tester);
    hold('request', body: 'hi');
    await open(tester, const PausedMessagePage(id: 'p1'));
    await choose(tester, 'paused-body-format', 'Hex');
    expect(tester.widget<TextField>(find.byKey(const ValueKey('paused-body'))).controller!.text, '68 69');
    await enter(tester, 'paused-body', '68 ff');
    await choose(tester, 'paused-body-format', 'Text');
    expect(find.text('This body is not UTF-8 text'), findsOneWidget);
    expect(tester.widget<TextField>(find.byKey(const ValueKey('paused-body'))).controller!.text, '68 ff');
    await enter(tester, 'paused-body', '68 69');
    await press(tester, 'Continue');
    expect(arguments('capture.breakpoint.resolve'), {'id': 'p1', 'decision': {'action': 'continue'}});
    expect(tester.takeException(), isNull);
  });

  testWidgets('a held body that starts with a byte order mark continues unchanged', (tester) async {
    await mount(tester);
    hold('response', body: '\u{feff}hi');
    await open(tester, const PausedMessagePage(id: 'p1'));
    await press(tester, 'Continue');
    expect(arguments('capture.breakpoint.resolve'), {'id': 'p1', 'decision': {'action': 'continue'}});
  });

  testWidgets('a held response validates its status and a streamed body is not editable', (tester) async {
    await mount(tester);
    hold('response', bodyEditable: false);
    await open(tester, const PausedMessagePage(id: 'p1'));
    expect(find.byKey(const ValueKey('paused-method')), findsNothing);
    expect(find.byKey(const ValueKey('paused-url')), findsNothing);
    expect(find.byKey(const ValueKey('paused-body')), findsNothing);
    expect(find.text('This body is streamed unchanged and cannot be edited'), findsOneWidget);
    expect(find.text('POST https://api.test/login'), findsOneWidget);
    await enter(tester, 'paused-status', '999');
    await press(tester, 'Continue');
    expect(find.text('Status must be between 200 and 599'), findsOneWidget);
    expect(calls.where((call) => call.method == 'capture.breakpoint.resolve'), isEmpty);
    await enter(tester, 'paused-status', '404');
    await press(tester, 'Continue');
    expect(arguments('capture.breakpoint.resolve'), {'id': 'p1', 'decision': {'action': 'continue', 'status': 404}});
    expect(find.byType(PausedMessagePage), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('aborting a held message asks first and a rejected decision keeps the page', (tester) async {
    await mount(tester);
    hold('request');
    final detail = extra!;
    var reject = true;
    extra = (call) async {
      if (call.method == 'capture.breakpoint.resolve' && reject) throw PlatformException(code: 'breakpoint', message: 'Paused message not found');
      return detail(call);
    };
    await open(tester, const PausedMessagePage(id: 'p1'));
    await press(tester, 'Abort');
    expect(find.text('Abort this exchange'), findsOneWidget);
    await tester.tap(find.text('Cancel').hitTestable());
    await tester.pumpAndSettle();
    expect(calls.where((call) => call.method == 'capture.breakpoint.resolve'), isEmpty);
    await press(tester, 'Abort');
    await tester.tap(find.text('Abort').hitTestable());
    await tester.pumpAndSettle();
    expect(arguments('capture.breakpoint.resolve'), {'id': 'p1', 'decision': {'action': 'abort'}});
    expect(find.text('Paused message not found'), findsOneWidget);
    expect(find.byType(PausedMessagePage), findsOneWidget);
    reject = false;
    await press(tester, 'Abort');
    await tester.tap(find.text('Abort').hitTestable());
    await tester.pumpAndSettle();
    expect(find.byType(PausedMessagePage), findsNothing);
    expect(find.text('Exchange aborted'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('a held message that is already gone offers a retry', (tester) async {
    await mount(tester);
    var missing = true;
    hold('request');
    final detail = extra!;
    extra = (call) async {
      if (call.method == 'capture.breakpoint.detail' && missing) throw PlatformException(code: 'breakpoint', message: 'Paused message not found');
      return detail(call);
    };
    await open(tester, const PausedMessagePage(id: 'p1'));
    expect(find.textContaining('Paused message not found'), findsOneWidget);
    expect(find.byTooltip('Continue'), findsNothing);
    missing = false;
    await tester.tap(find.text('Retry'));
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('paused-url')), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}
