import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';
import 'dart:ui' as ui;

import 'package:b_box_ui/components/capture_poll.dart';
import 'package:b_box_ui/components/captured_body.dart';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:flutter/painting.dart' show FileImage, ImageStreamListener, ResizeImage, ResizeImagePolicy;
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/errors.dart';

// A complete 32 x 24 RGB PNG; generated from a solid blue pixel buffer.
const _png = 'iVBORw0KGgoAAAANSUhEUgAAACAAAAAYCAIAAAAUMWhjAAAAJElEQVR4nGPQaLpEU8QwasGoBaMWjFowasGoBaMWjFowNCwAAKq6dD0cp/caAAAAAElFTkSuQmCC';

Uint8List _largePng() {
  const width = 128, height = 192;
  final header = ByteData(13)..setUint32(0, width)..setUint32(4, height)..setUint8(8, 8)..setUint8(9, 2);
  final pixels = Uint8List((width * 3 + 1) * height);
  for (var row = 0; row < height; row++) {
    for (var index = 1; index <= width * 3; index++) pixels[row * (width * 3 + 1) + index] = (row + index) % 256;
  }
  List<int> chunk(String name, List<int> data) {
    final type = ascii.encode(name);
    var crc = 0xffffffff;
    for (final byte in [...type, ...data]) {
      crc ^= byte;
      for (var bit = 0; bit < 8; bit++) crc = (crc >>> 1) ^ ((crc & 1) == 1 ? 0xedb88320 : 0);
    }
    return [...(ByteData(4)..setUint32(0, data.length)).buffer.asUint8List(), ...type, ...data,
      ...(ByteData(4)..setUint32(0, crc ^ 0xffffffff)).buffer.asUint8List()];
  }
  return Uint8List.fromList([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
    ...chunk('IHDR', header.buffer.asUint8List()), ...chunk('IDAT', ZLibEncoder(level: 0).convert(pixels)),
    ...chunk('IEND', const [])]);
}

class _BodyHarness {
  final events = StreamController<dynamic>.broadcast();
  final calls = <MethodCall>[];
  final copied = <String>[];
  final boundaryKey = GlobalKey();
  late final BoxController box;
}

Json _message({String type = 'application/octet-stream', int saved = 0, bool complete = true, bool truncated = false}) => {
  'headers': [{'name': 'Content-Type', 'value': type}],
  'capturedBytes': saved, 'bodyBytes': saved, 'bodyComplete': complete, 'bodyTruncated': truncated,
};

Json _page(String text, {String encoding = 'text', String source = 'decoded', int offset = 0,
  int? nextOffset, bool more = false, bool truncated = false}) => {
  'text': text, 'encoding': encoding, 'source': source, 'offset': offset,
  'nextOffset': nextOffset ?? offset + (encoding == 'base64' ? base64Decode(text).length : utf8.encode(text).length),
  'hasMore': more, 'truncated': truncated,
};

Future<_BodyHarness> _mount(WidgetTester tester, {required Json message, required Json page,
  Future<dynamic> Function(MethodCall)? handler, Size size = const Size(360, 800), double scale = 1,
  bool dark = false, bool nativeReader = false, ValueNotifier<Json>? live,
  List<Widget> afterBody = const []}) async {
  reportFlutterErrors();
  tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
  tester.binding.platformDispatcher.localesTestValue = const [Locale('en')];
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  tester.platformDispatcher.textScaleFactorTestValue = scale;
  addTearDown(tester.binding.platformDispatcher.clearLocalesTestValue);
  addTearDown(tester.view.reset);
  addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
  final harness = _BodyHarness();
  const channel = MethodChannel('test.captured-body');
  final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  messenger.setMockMethodCallHandler(SystemChannels.platform, (call) async {
    if (call.method == 'Clipboard.setData') harness.copied.add(json(call.arguments)['text'] as String);
    return null;
  });
  messenger.setMockMethodCallHandler(channel, (call) async {
    harness.calls.add(call);
    if (call.method == 'ready') return {
      'version': 'test', 'coreVersion': 'test', 'preferences': {'theme': dark ? 'dark' : 'light'},
      'dashboard': {'status': 'Started'},
    };
    return await handler?.call(call);
  });
  harness.box = BoxController(channel: channel, events: harness.events.stream);
  await tester.pumpWidget(RepaintBoundary(key: harness.boundaryKey, child: BoxApp(controller: harness.box)));
  await tester.pumpAndSettle();
  addTearDown(() async {
    await tester.pumpWidget(const SizedBox());
    await harness.events.close();
    messenger.setMockMethodCallHandler(channel, null);
    messenger.setMockMethodCallHandler(SystemChannels.platform, null);
  });
  Widget body(Json message, Json page) => CapturedBody(message: message, preview: page,
    captureId: nativeReader ? 'body-fixture' : null, direction: 'response',
    loadPage: nativeReader || handler == null ? null : (offset, source) async =>
      json(await harness.box.call('capture.body', {
        'id': 'body-fixture', 'direction': 'response', 'offset': offset, 'source': source,
      })));
  openPage(tester.element(find.byType(HomePage)), BoxPage(title: 'Request details', children: [
    if (live != null) ValueListenableBuilder<Json>(valueListenable: live, builder: (context, value, child) =>
      body(json(value['message']), json(value['page']))),
    if (live == null && nativeReader) CapturePoll(method: 'capture.detail', arguments: const {'id': 'body-fixture'},
      untilComplete: true, builder: (context, detail, error, refresh) {
        final response = json(detail?['response']);
        final preview = json(detail?['responsePreview']);
        return body(response.isEmpty ? message : response, preview.isEmpty ? page : preview);
      }),
    if (live == null && !nativeReader) body(message, page),
    ...afterBody,
  ]));
  await tester.pumpAndSettle();
  return harness;
}

Finder get _body => find.byKey(const ValueKey('captured-body'));
Finder _in(Finder scope, Finder target) => find.descendant(of: scope, matching: target);
String _text(WidgetTester tester, Finder scope) => tester.widget<SelectableText>(
  _in(scope, find.byKey(const ValueKey('captured-body-text')))).data!;
IconButton _button(WidgetTester tester, String tooltip) => tester.widget<IconButton>(
  _in(_body, find.byWidgetPredicate((widget) => widget is IconButton && widget.tooltip == tooltip)));
BoxMenu _menu(WidgetTester tester) => tester.widget<BoxMenu>(_in(_body, find.byType(BoxMenu)));
void _expectCopyOnlyMenu(WidgetTester tester) =>
  expect(_menu(tester).items.map((item) => item.label), ['Copy displayed body']);

Future<void> _dismissFeedback(WidgetTester tester) async {
  final banner = find.text('Copied').hitTestable();
  if (banner.evaluate().isNotEmpty) {
    await tester.fling(banner.last, const Offset(0, -120), 1000);
    await tester.pumpAndSettle();
  }
}

Future<void> _tap(WidgetTester tester, Finder target) async {
  await _dismissFeedback(tester);
  // The detail viewport extends beneath its floating page bar. Center the
  // control in that viewport so the page bar cannot cover the tap position.
  await Scrollable.ensureVisible(tester.element(target.last), alignment: .5);
  await tester.pumpAndSettle();
  expect(target.hitTestable(), findsWidgets, reason: 'The body control must be reachable after scrolling.');
  await tester.tap(target.hitTestable().last);
  await tester.pumpAndSettle();
}

Future<void> _action(WidgetTester tester, Finder scope, String label) async {
  await _dismissFeedback(tester);
  final direct = _in(scope, find.byTooltip(label));
  if (direct.evaluate().isNotEmpty) {
    await _tap(tester, direct);
  } else {
    // Copying the displayed representation lives in the body menu.
    await _tap(tester, _in(scope, find.byTooltip('More')));
    await _tap(tester, find.text(label).hitTestable());
  }
}

Finder _hexList(Finder scope) => _in(scope, find.byWidgetPredicate((widget) =>
  widget is ListView && widget.childrenDelegate is SliverChildBuilderDelegate));

List<SelectableText> _hexRows(WidgetTester tester, Finder scope) => tester.widgetList<SelectableText>(
  _in(scope, find.byWidgetPredicate((widget) => widget is SelectableText &&
    widget.key is ValueKey<String> && (widget.key! as ValueKey<String>).value.startsWith('captured-body-hex-line-')))).toList();

Future<void> _decodeImage(WidgetTester tester, Finder imageFinder, {bool expectSuccess = true}) async {
  final image = tester.widget<Image>(imageFinder);
  final stream = image.image.resolve(createLocalImageConfiguration(tester.element(imageFinder)));
  var decoded = false;
  Object? decodeError;
  final listener = ImageStreamListener((info, _) {
    info.dispose();
    decoded = true;
  }, onError: (Object error, StackTrace? stack) {
    decodeError = error;
    decoded = true;
  });
  stream.addListener(listener);
  try {
    // The widget started loading in fakeAsync. Let the engine finish in real
    // time, then pump its continuation instead of waiting across both zones.
    for (var attempt = 0; !decoded && attempt < 100; attempt++) {
      await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 20)));
      await tester.pump();
    }
    expect(decoded, isTrue, reason: 'The captured image did not finish decoding.');
    if (expectSuccess) expect(decodeError, isNull, reason: 'The PNG fixture must decode successfully.');
  } finally {
    stream.removeListener(listener);
  }
  await tester.pumpAndSettle();
}

Future<void> _capture(WidgetTester tester, _BodyHarness harness, String label) async {
  await tester.runAsync(() async {
    final boundary = harness.boundaryKey.currentContext!.findRenderObject()! as RenderRepaintBoundary;
    final bitmap = await boundary.toImage();
    try {
      final png = await bitmap.toByteData(format: ui.ImageByteFormat.png);
      final file = File('build/screenshots/body/$label.png');
      await file.parent.create(recursive: true);
      await file.writeAsBytes(png!.buffer.asUint8List());
    } finally { bitmap.dispose(); }
  });
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUpAll(() async {
    final mono = await File('assets/fonts/JetBrainsMono-Regular.ttf').readAsBytes();
    await (FontLoader('JetBrainsMono')..addFont(Future.value(ByteData.sublistView(mono)))).load();
    final root = Platform.environment['FLUTTER_ROOT'];
    if (root != null) {
      final font = File('$root/bin/cache/artifacts/material_fonts/Roboto-Regular.ttf');
      if (font.existsSync()) {
        await (FontLoader('Roboto')..addFont(Future.value(ByteData.sublistView(await font.readAsBytes())))).load();
      }
    }
  });

  testWidgets('request details directly format JSON and copy displayed text separately from the original page', (tester) async {
    const raw = r'{"id":18446744073709551615,"same":1,"same":2,"value":"a\\b\u4f60"}';
    final harness = await _mount(tester, message: _message(type: 'application/json', saved: raw.length), page: _page(raw));
    final formatted = _text(tester, _body);
    expect(formatted, contains('\n  "id": 18446744073709551615,'));
    expect(formatted, contains('"same": 1,\n  "same": 2,'));
    expect(find.byType(CapturedBody), findsOneWidget);
    expect(_in(_body, find.byType(Scaffold)), findsNothing);
    expect(find.byTooltip('View body'), findsNothing);
    _expectCopyOnlyMenu(tester);
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, raw);
    await _action(tester, _body, 'Copy displayed body');
    expect(harness.copied.last, formatted);
    await _action(tester, _body, 'Show original');
    expect(_text(tester, _body), raw);
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, raw);
    await _action(tester, _body, 'Format JSON');
    expect(_text(tester, _body), formatted);
    expect(tester.takeException(), isNull);
  });

  testWidgets('a declared JSON scalar opens its complete native view automatically and keeps the original page', (tester) async {
    const original = ' \r\ntrue \t';
    final message = _message(type: 'application/json', saved: original.length);
    final preview = _page(original);
    final harness = await _mount(tester, message: message, page: preview, nativeReader: true, handler: (call) async {
      if (call.method == 'capture.detail') return {'complete': true, 'response': message, 'responsePreview': preview};
      if (call.method == 'capture.body.view') return {'format': 'json', 'message': message, 'page': _page('true', source: 'view')};
      return null;
    });
    expect(_text(tester, _body), 'true');
    expect(_in(_body, find.text('JSON')), findsOneWidget);
    expect(harness.calls.where((call) => call.method == 'capture.body.view')
      .map((call) => json(call.arguments)['format']), ['json']);
    _expectCopyOnlyMenu(tester);
    await _action(tester, _body, 'Copy displayed body');
    expect(harness.copied.last, 'true');
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, original);
    await _action(tester, _body, 'Show original');
    expect(_text(tester, _body), original);
    await _action(tester, _body, 'Format JSON');
    expect(_text(tester, _body), 'true');
    expect(harness.calls.where((call) => call.method == 'capture.body.view'), hasLength(1));
    expect(harness.calls.where((call) => call.method == 'capture.body'), isEmpty);
    expect(tester.takeException(), isNull);
  });

  testWidgets('valid single-page JSON exceeding the formatting budget automatically pages its native view', (tester) async {
    const depth = 200;
    final original = '${'[' * depth}0${']' * depth}';
    final formatted = [
      for (var level = 0; level < depth; level++) '${'  ' * level}[',
      '${'  ' * depth}0',
      for (var level = depth - 1; level >= 0; level--) '${'  ' * level}]',
    ].join('\n');
    expect(original.length, lessThan(16384));
    expect(formatted.length, greaterThan(65536));
    final first = _page(formatted.substring(0, 16384), source: 'view', more: true);
    final second = _page(formatted.substring(16384, 32768), source: 'view', offset: 16384, more: true);
    final message = _message(type: 'application/json', saved: original.length);
    final preview = _page(original);
    final harness = await _mount(tester, message: message, page: preview, nativeReader: true, handler: (call) async {
      if (call.method == 'capture.detail') return {'complete': true, 'response': message, 'responsePreview': preview};
      if (call.method == 'capture.body.view') return {'format': 'json', 'message': message, 'page': first};
      if (call.method == 'capture.body.view.page') return second;
      return null;
    });
    expect(_text(tester, _body), first['text']);
    expect(harness.calls.where((call) => call.method == 'capture.body.view')
      .map((call) => json(call.arguments)['format']), ['json']);
    _expectCopyOnlyMenu(tester);
    await _action(tester, _body, 'Copy displayed body');
    expect(harness.copied.last, first['text']);
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, original);
    await _action(tester, _body, 'Show original');
    expect(_text(tester, _body), original);
    expect(_in(_body, find.text('Next page')), findsNothing);
    await _action(tester, _body, 'Format JSON');
    expect(_text(tester, _body), first['text']);
    await _tap(tester, _in(_body, find.text('Next page')));
    expect(_text(tester, _body), second['text']);
    expect(_in(_body, find.text('Derived body page: 2')), findsOneWidget);
    expect(harness.calls.where((call) => call.method == 'capture.body.view.page')
      .map((call) => json(call.arguments)['offset']), [16384]);
    await _action(tester, _body, 'Copy displayed body');
    expect(harness.copied.last, second['text']);
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, original);
    expect(harness.calls.where((call) => call.method == 'capture.body.view'), hasLength(1));
    expect(harness.calls.where((call) => call.method == 'capture.body'), isEmpty);
    expect(tester.takeException(), isNull);
  });

  for (final (mime, original, paged, shown) in [('application/json', ' \r\ntrue \t', false, 'true'),
    ('', '{"id":', true, '{\n  "id": 1\n}')]) {
    testWidgets('failed automatic JSON parsing of a ${paged ? 'paged candidate' : 'scalar'} keeps its page until the same format is retried', (tester) async {
      var fail = true;
      final message = _message(type: mime, saved: original.length + (paged ? 2 : 0));
      final preview = _page(original, more: paged);
      final harness = await _mount(tester, message: message, page: preview, nativeReader: true, handler: (call) async {
        if (call.method == 'capture.detail') return {'complete': true, 'response': message, 'responsePreview': preview};
        if (call.method == 'capture.body.view') {
          if (fail) throw PlatformException(code: 'capture', message: 'Saved JSON could not be parsed');
          return {'format': 'json', 'message': message, 'page': _page(shown, source: 'view')};
        }
        return null;
      });
      expect(_text(tester, _body), original);
      expect(_in(_body, find.text('Saved JSON could not be parsed')), findsOneWidget);
      expect(_in(_body, find.byTooltip('Show original')), findsNothing);
      expect(_in(_body, find.byTooltip('Format JSON')), findsNothing);
      _expectCopyOnlyMenu(tester);
      await _action(tester, _body, 'Copy displayed body');
      expect(harness.copied.last, original);
      await tester.pump(const Duration(seconds: 4));
      await tester.pumpAndSettle();
      expect(harness.calls.where((call) => call.method == 'capture.body.view'), hasLength(1));
      fail = false;
      await _tap(tester, _in(_body, find.text('Retry body view')));
      expect(_text(tester, _body), shown);
      expect(_in(_body, find.text('Saved JSON could not be parsed')), findsNothing);
      expect(harness.calls.where((call) => call.method == 'capture.body.view')
        .map((call) => json(call.arguments)['format']), ['json', 'json']);
      await _action(tester, _body, 'Copy current body page');
      expect(harness.copied.last, original);
      await _action(tester, _body, 'Show original');
      expect(_text(tester, _body), original);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('malformed body data stays visible and can be copied from its error view', (tester) async {
    const original = 'bad===';
    final harness = await _mount(tester, message: _message(saved: 4),
      page: {'text': original, 'encoding': 'base64', 'source': 'decoded', 'offset': 0, 'hasMore': false});
    expect(_in(_body, find.text('Body data could not be read.')), findsOneWidget);
    expect(_text(tester, _body), original);
    expect(_hexRows(tester, _body), isEmpty);
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, original);
    await _action(tester, _body, 'Copy displayed body');
    expect(harness.copied.last, original);
    expect(_text(tester, _body), original);
    expect(tester.takeException(), isNull);
  });

  testWidgets('clipboard failure keeps the body visible and permits a successful retry', (tester) async {
    const original = 'saved body';
    final harness = await _mount(tester, message: _message(type: 'text/plain', saved: original.length), page: _page(original));
    var reject = true;
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger.setMockMethodCallHandler(SystemChannels.platform, (call) async {
      if (call.method == 'Clipboard.setData') {
        if (reject) throw PlatformException(code: 'clipboard', message: 'Clipboard denied');
        harness.copied.add(json(call.arguments)['text'] as String);
      }
      return null;
    });
    await _action(tester, _body, 'Copy current body page');
    expect(find.text('Clipboard denied'), findsOneWidget);
    expect(find.text('Copied'), findsNothing);
    expect(harness.copied, isEmpty);
    expect(_text(tester, _body), original);
    await tester.fling(find.text('Clipboard denied'), const Offset(0, -120), 1000);
    await tester.pumpAndSettle();
    reject = false;
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, original);
    expect(find.text('Copied'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('an empty completed JSON body never opens a native view and only offers body copying', (tester) async {
    final message = _message(type: 'application/json');
    final preview = _page('');
    final harness = await _mount(tester, message: message, page: preview, nativeReader: true, handler: (call) async {
      if (call.method == 'capture.detail') return {'complete': true, 'response': message, 'responsePreview': preview};
      return null;
    });
    expect(_in(_body, find.text('No body captured')), findsOneWidget);
    expect(harness.calls.where((call) => call.method == 'capture.body.view'), isEmpty);
    _expectCopyOnlyMenu(tester);
    expect(_button(tester, 'Copy current body page').onPressed, isNull);
    expect(tester.takeException(), isNull);
  });

  for (final (mime, original, paged) in [('text/plain', 'saved plain body', false),
    ('application/json', 'this is plain text', false), ('', 'true', false),
    ('application/json', '<html>Upstream failed', true), ('', '[INFO] saved plain body', true)]) {
    testWidgets('${mime.isEmpty ? 'untyped text' : mime} ${paged ? 'prefix' : 'body'} stays plain when no automatic format applies', (tester) async {
      final message = _message(type: mime, saved: original.length + (paged ? 16384 : 0));
      final preview = _page(original, more: paged);
      final harness = await _mount(tester, message: message, page: preview, nativeReader: true, handler: (call) async {
        if (call.method == 'capture.detail') return {'complete': true, 'response': message, 'responsePreview': preview};
        return null;
      });
      expect(_text(tester, _body), original);
      expect(_in(_body, find.byTooltip('Show original')), findsNothing);
      expect(_in(_body, find.byTooltip('Format JSON')), findsNothing);
      expect(harness.calls.where((call) => call.method == 'capture.body.view'), isEmpty);
      _expectCopyOnlyMenu(tester);
      await _action(tester, _body, 'Copy displayed body');
      expect(harness.copied.last, original);
      await _action(tester, _body, 'Copy current body page');
      expect(harness.copied.last, original);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('invalid original JSON control characters stay readable without formatting or opening a native view', (tester) async {
    final original = '{"text":"${'a' * 500}\u0001"}';
    final displayed = original.replaceAll('\u0001', '\ufffd');
    final message = _message(type: 'application/json', saved: utf8.encode(original).length);
    final preview = _page(original);
    final harness = await _mount(tester, message: message, page: preview, nativeReader: true, handler: (call) async {
      if (call.method == 'capture.detail') return {'complete': true, 'response': message, 'responsePreview': preview};
      return null;
    });
    expect(_text(tester, _body), displayed);
    expect(_in(_body, find.text('Text')), findsOneWidget);
    expect(_in(_body, find.text('JSON')), findsNothing);
    expect(_in(_body, find.byTooltip('Show original')), findsNothing);
    expect(_in(_body, find.byTooltip('Format JSON')), findsNothing);
    expect(harness.calls.where((call) => call.method == 'capture.body.view'), isEmpty);
    _expectCopyOnlyMenu(tester);
    await _action(tester, _body, 'Copy displayed body');
    expect(harness.copied.last, displayed);
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, original);
    await _tap(tester, _in(_body, find.text('Base64')));
    expect(_text(tester, _body), base64Encode(utf8.encode(original)));
    expect(harness.calls.where((call) => call.method == 'capture.body.view'), isEmpty);
    expect(tester.takeException(), isNull);
  });

  testWidgets('complete unknown binary data stays hexadecimal and never guesses a parser', (tester) async {
    final original = base64Encode([0xff, 0xfb, 0xfd]);
    final message = _message(saved: 3);
    final preview = _page(original, encoding: 'base64');
    final harness = await _mount(tester, message: message, page: preview, nativeReader: true, handler: (call) async {
      if (call.method == 'capture.detail') return {'complete': true, 'response': message, 'responsePreview': preview};
      return null;
    });
    expect(_hexRows(tester, _body).first.data, contains('ff fb'));
    expect(_in(_body, find.byKey(const ValueKey('captured-body-text'))), findsNothing);
    _expectCopyOnlyMenu(tester);
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, original);
    await _tap(tester, _in(_body, find.text('Base64')));
    expect(_text(tester, _body), original);
    expect(harness.calls.where((call) => call.method == 'capture.body.view'), isEmpty);
    expect(tester.takeException(), isNull);
  });

  testWidgets('binary paging retains raw source, byte offsets and previous-page history in request details', (tester) async {
    final firstBytes = Uint8List.fromList(List.generate(16, (index) => index));
    final lastBytes = Uint8List.fromList(List.generate(32, (index) => index + 16));
    final first = _page(base64Encode(firstBytes), encoding: 'base64', source: 'raw', nextOffset: 16, more: true);
    final last = _page(base64Encode(lastBytes), encoding: 'base64', source: 'raw', offset: 16);
    final harness = await _mount(tester, message: _message(saved: 48), page: first, handler: (call) async {
      if (call.method != 'capture.body') return null;
      final args = json(call.arguments);
      expect(args['source'], 'raw');
      expect(args['direction'], 'response');
      return args['offset'] == 0 ? first : last;
    });
    expect(_in(_body, find.byKey(const ValueKey('captured-body-hex-line-0'))), findsOneWidget);
    await _tap(tester, _in(_body, find.text('Base64')));
    expect(_text(tester, _body), first['text']);
    await _tap(tester, _in(_body, find.text('Next page')));
    expect(_text(tester, _body), last['text']);
    expect(_in(_body, find.text('Previous page')), findsOneWidget);
    await _tap(tester, _in(_body, find.text('Hex')));
    final hex = tester.widget<SelectableText>(_in(_body, find.byKey(const ValueKey('captured-body-hex-line-16')))).data!;
    expect(hex, startsWith('000010  10'));
    await _action(tester, _body, 'Copy displayed body');
    expect(harness.copied.last, startsWith('000010  10'));
    expect(harness.copied.last, contains('2f'));
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, last['text']);
    await _tap(tester, _in(_body, find.text('Previous page')));
    expect(_in(_body, find.byKey(const ValueKey('captured-body-hex-line-0'))), findsOneWidget);
    expect(json(harness.calls.lastWhere((call) => call.method == 'capture.body').arguments), {
      'id': 'body-fixture', 'direction': 'response', 'offset': 0, 'source': 'raw',
    });
    await _tap(tester, _in(_body, find.text('Base64')));
    expect(_text(tester, _body), first['text']);
    await _tap(tester, _in(_body, find.text('Next page')));
    expect(_text(tester, _body), last['text']);
    expect(_in(_body, find.text('Body page: 2')), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('a decoded binary page uses absolute addresses and lazily builds hexadecimal rows', (tester) async {
    const offset = 131072;
    final data = Uint8List.fromList(List.generate(16384, (index) => index % 256));
    await _mount(tester, message: _message(saved: 128), page: _page(base64Encode(data), encoding: 'base64', offset: offset));
    final first = tester.widget<SelectableText>(_in(_body, find.byKey(const ValueKey('captured-body-hex-line-131072'))));
    expect(first.data, startsWith('020000  00'));
    expect(_hexRows(tester, _body).length, lessThan(128));
    expect(_in(_body, find.byKey(const ValueKey('captured-body-text'))), findsNothing);
    final list = tester.widget<ListView>(_hexList(_body));
    expect(list.controller!.position.maxScrollExtent, greaterThan(10000));
    list.controller!.jumpTo(list.controller!.position.maxScrollExtent);
    await tester.pumpAndSettle();
    final tail = _hexRows(tester, _body).last;
    expect(tail.data, contains('ff'));
    expect(int.parse((tail.key! as ValueKey<String>).value.split('-').last), greaterThan(offset + 16000));
    expect(tester.takeException(), isNull);
  });

  testWidgets('protobuf derived text preserves its original page on copy', (tester) async {
    // Field 1 = uint64 max; field 2 = a printable string.
    final proto = Uint8List.fromList([0x08, ...List.filled(9, 0xff), 0x01, 0x12, 0x02, 0x6f, 0x6b]);
    final original = base64Encode(proto);
    final harness = await _mount(tester, message: _message(type: 'application/x-protobuf', saved: proto.length),
      page: _page(original, encoding: 'base64'));
    expect(_in(_body, find.text('Protobuf')), findsOneWidget);
    expect(_text(tester, _body), '1: 18446744073709551615\n2: "ok"');
    _expectCopyOnlyMenu(tester);
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, original);
    await _action(tester, _body, 'Copy displayed body');
    expect(harness.copied.last, '1: 18446744073709551615\n2: "ok"');
    await _tap(tester, _in(_body, find.text('Base64')));
    expect(_text(tester, _body), original);
    expect(tester.takeException(), isNull);
  });

  testWidgets('a complete untyped Protobuf body automatically opens its detected native view', (tester) async {
    final bytes = Uint8List.fromList([8, 1, 18, 2, 0x6f, 0x6b]);
    final original = base64Encode(bytes);
    const shown = '1: 1\n2: "ok"';
    final message = _message(type: '', saved: bytes.length);
    final preview = {..._page(original, encoding: 'base64'), 'charset': 'utf-8'};
    final harness = await _mount(tester, message: message, page: preview, nativeReader: true, handler: (call) async {
      if (call.method == 'capture.detail') return {'complete': true, 'response': message, 'responsePreview': preview};
      if (call.method == 'capture.body.view') return {'format': 'protobuf', 'message': message, 'page': _page(shown, source: 'view')};
      return null;
    });
    expect(_text(tester, _body), shown);
    expect(_in(_body, find.text('Protobuf')), findsOneWidget);
    expect(harness.calls.where((call) => call.method == 'capture.body.view')
      .map((call) => json(call.arguments)['format']), ['protobuf']);
    _expectCopyOnlyMenu(tester);
    await _action(tester, _body, 'Copy displayed body');
    expect(harness.copied.last, shown);
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, original);
    await _tap(tester, _in(_body, find.text('Base64')));
    expect(_text(tester, _body), original);
    await _tap(tester, _in(_body, find.text('Protobuf')));
    expect(_text(tester, _body), shown);
    expect(harness.calls.where((call) => call.method == 'capture.body.view'), hasLength(1));
    expect(tester.takeException(), isNull);
  });

  testWidgets('gRPC frames display message fields without treating the frame prefix as protobuf', (tester) async {
    final frame = Uint8List.fromList([0, 0, 0, 0, 2, 8, 42]);
    final original = base64Encode(frame);
    final harness = await _mount(tester, message: _message(type: 'application/grpc+proto', saved: frame.length),
      page: _page(original, encoding: 'base64'));
    expect(_in(_body, find.text('gRPC')), findsOneWidget);
    expect(_text(tester, _body), '# 1\n1: 42');
    _expectCopyOnlyMenu(tester);
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, original);
    expect(tester.takeException(), isNull);
  });

  testWidgets('gRPC JSON payloads automatically keep their frame view without a Protobuf schema note', (tester) async {
    const payload = '{"id":42}';
    final bytes = Uint8List.fromList([0, ...(ByteData(4)..setUint32(0, payload.length)).buffer.asUint8List(), ...utf8.encode(payload)]);
    final original = base64Encode(bytes);
    const shown = '# 1\n{"id":42}';
    final message = _message(type: 'application/grpc+json', saved: bytes.length);
    final preview = _page(original, encoding: 'base64');
    final harness = await _mount(tester, message: message, page: preview, nativeReader: true, handler: (call) async {
      if (call.method == 'capture.detail') return {'complete': true, 'response': message, 'responsePreview': preview};
      if (call.method == 'capture.body.view') return {'format': 'grpc', 'message': message, 'page': _page(shown, source: 'view')};
      return null;
    });
    expect(_text(tester, _body), shown);
    expect(_in(_body, find.text('gRPC')), findsOneWidget);
    expect(_in(_body, find.text('JSON')), findsNothing);
    expect(_in(_body, find.text('Field numbers are shown without a Protobuf schema.')), findsNothing);
    expect(_in(_body, find.byTooltip('Show original')), findsNothing);
    expect(harness.calls.where((call) => call.method == 'capture.body.view')
      .map((call) => json(call.arguments)['format']), ['grpc']);
    _expectCopyOnlyMenu(tester);
    await _action(tester, _body, 'Copy displayed body');
    expect(harness.copied.last, shown);
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, original);
    await _tap(tester, _in(_body, find.text('Base64')));
    expect(_text(tester, _body), original);
    await _tap(tester, _in(_body, find.text('gRPC')));
    expect(_text(tester, _body), shown);
    expect(harness.calls.where((call) => call.method == 'capture.body.view'), hasLength(1));
    expect(tester.takeException(), isNull);
  });

  testWidgets('complete captured images zoom directly in request details with bounded decode dimensions', (tester) async {
    final harness = await _mount(tester, message: _message(type: 'image/png', saved: base64Decode(_png).length),
      page: _page(_png, encoding: 'base64'));
    final imageFinder = _in(_body, find.byKey(const ValueKey('captured-body-image')));
    await _decodeImage(tester, imageFinder);
    expect(find.byType(CapturedBody), findsOneWidget);
    expect(find.byTooltip('View body'), findsNothing);
    _expectCopyOnlyMenu(tester);
    await _capture(tester, harness, '360-light-image');
    final image = tester.widget<Image>(imageFinder);
    expect(image.image, isA<ResizeImage>());
    final provider = image.image as ResizeImage;
    expect(provider.policy, ResizeImagePolicy.fit);
    expect(provider.width, inInclusiveRange(1, 2048));
    expect(provider.height, inInclusiveRange(1, 2048));
    final interactive = _in(_body, find.byType(InteractiveViewer));
    await tester.ensureVisible(interactive);
    await tester.pumpAndSettle();
    final transform = tester.widget<InteractiveViewer>(interactive).transformationController!;
    final center = tester.getCenter(interactive);
    final left = await tester.createGesture(pointer: 1);
    final right = await tester.createGesture(pointer: 2);
    await left.down(center - const Offset(24, 0));
    await right.down(center + const Offset(24, 0));
    await tester.pump();
    await left.moveTo(center - const Offset(64, 0));
    await right.moveTo(center + const Offset(64, 0));
    await tester.pump();
    await left.up();
    await right.up();
    await tester.pumpAndSettle();
    expect(transform.value.getMaxScaleOnAxis(), greaterThan(1.05));
    await _capture(tester, harness, '360-light-image-zoomed');
    await _action(tester, _body, 'Reset zoom');
    expect(transform.value.getMaxScaleOnAxis(), closeTo(1, 0.00001));
    expect(transform.value.entry(0, 3), closeTo(0, 0.00001));
    expect(transform.value.entry(1, 3), closeTo(0, 0.00001));
    await _tap(tester, _in(_body, find.text('Hex')));
    expect(_in(_body, find.byKey(const ValueKey('captured-body-image'))), findsNothing);
    expect(_in(_body, find.byKey(const ValueKey('captured-body-hex-line-0'))), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('an undecodable captured image offers a hexadecimal fallback without losing its bytes', (tester) async {
    const original = 'AAECAw==';
    final harness = await _mount(tester, message: _message(type: 'image/png', saved: 4),
      page: _page(original, encoding: 'base64'));
    await _decodeImage(tester, _in(_body, find.byKey(const ValueKey('captured-body-image'))), expectSuccess: false);
    expect(_in(_body, find.text('Image preview unavailable')), findsOneWidget);
    await _tap(tester, _in(_body, find.text('Show hexadecimal')));
    expect(_in(_body, find.byKey(const ValueKey('captured-body-image'))), findsNothing);
    final hex = tester.widget<SelectableText>(_in(_body, find.byKey(const ValueKey('captured-body-hex-line-0'))));
    expect(hex.data, contains('00 01'));
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, original);
    expect(tester.takeException(), isNull);
  });

  testWidgets('a plain body mislabeled as image PNG remains text on later pages and keeps body copying', (tester) async {
    final first = _page('first-', more: true);
    final last = _page('second', offset: first['nextOffset'] as int);
    final message = _message(type: 'image/png', saved: 12);
    final harness = await _mount(tester, message: message, page: first, nativeReader: true, handler: (call) async {
      if (call.method == 'capture.detail') return {'complete': true, 'response': message, 'responsePreview': first};
      if (call.method == 'capture.body') return {...last, 'message': message};
      return null;
    });
    expect(_text(tester, _body), first['text']);
    await _tap(tester, _in(_body, find.text('Next page')));
    expect(_text(tester, _body), last['text']);
    expect(_in(_body, find.text('Text')), findsOneWidget);
    expect(_in(_body, find.text('Image')), findsNothing);
    expect(_in(_body, find.byKey(const ValueKey('captured-body-image'))), findsNothing);
    expect(_in(_body, find.byTooltip('Reset zoom')), findsNothing);
    _expectCopyOnlyMenu(tester);
    await _action(tester, _body, 'Copy displayed body');
    expect(harness.copied.last, last['text']);
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, last['text']);
    expect(harness.calls.where((call) => call.method == 'capture.body.view'), isEmpty);
    expect(harness.calls.where((call) => call.method == 'capture.body')
      .map((call) => json(call.arguments)['offset']), [first['nextOffset']]);
    expect(tester.takeException(), isNull);
  });

  testWidgets('an incomplete detected PNG keeps a readable later byte fragment in hexadecimal views', (tester) async {
    final first = _page(base64Encode(base64Decode(_png).sublist(0, 16)), encoding: 'base64', more: true);
    const fragment = 'image-bytes-are-not-text';
    final later = _page(fragment, offset: 16, more: true);
    final message = _message(type: 'image/png', saved: 80, complete: false);
    final harness = await _mount(tester, message: message, page: first, nativeReader: true, handler: (call) async {
      if (call.method == 'capture.detail') return {'complete': false, 'response': message, 'responsePreview': first};
      if (call.method == 'capture.body') return {...later, 'message': message};
      return null;
    });
    expect(_hexRows(tester, _body).first.data, contains('89 50'));
    await _tap(tester, _in(_body, find.text('Next page')));
    expect(_in(_body, find.byKey(const ValueKey('captured-body-text'))), findsNothing);
    expect(_hexRows(tester, _body).first.data, startsWith('000010'));
    expect(_hexRows(tester, _body).first.data, contains('69 6d'));
    expect(_in(_body, find.text('Text')), findsNothing);
    expect(_in(_body, find.byTooltip('Wrap lines')), findsNothing);
    expect(_in(_body, find.byKey(const ValueKey('captured-body-image'))), findsNothing);
    _expectCopyOnlyMenu(tester);
    await _action(tester, _body, 'Copy displayed body');
    expect(harness.copied.last, startsWith('000010'));
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, fragment);
    await _tap(tester, _in(_body, find.text('Base64')));
    expect(_text(tester, _body), base64Encode(utf8.encode(fragment)));
    expect(harness.calls.where((call) => call.method == 'capture.body.view'), isEmpty);
    expect(tester.takeException(), isNull);
  });

  for (final limitation in ['multiple pages', 'incomplete stream', 'truncated capture', 'saved raw bytes']) {
    testWidgets('images with $limitation stay in byte views and never fetch remaining pages automatically', (tester) async {
      final harness = await _mount(tester, message: _message(type: 'image/png', saved: base64Decode(_png).length,
        complete: limitation != 'incomplete stream', truncated: limitation == 'truncated capture'),
        page: _page(_png, encoding: 'base64', more: limitation == 'multiple pages',
          source: limitation == 'saved raw bytes' ? 'raw' : 'decoded'));
      expect(_in(_body, find.byKey(const ValueKey('captured-body-image'))), findsNothing);
      expect(_in(_body, find.text('Image')), findsNothing);
      expect(_in(_body, find.byKey(const ValueKey('captured-body-hex-line-0'))), findsOneWidget);
      await _tap(tester, _in(_body, find.text('Base64')));
      expect(_text(tester, _body), _png);
      expect(harness.calls.where((call) => call.method == 'capture.body'), isEmpty);
      expect(tester.takeException(), isNull);
    });
  }

  for (final limitation in ['raw fallback', 'incomplete stream', 'capture truncation', 'page truncation', 'decode error']) {
    testWidgets('native images with $limitation keep their saved bytes without opening a complete view', (tester) async {
      final message = _message(type: 'image/png', saved: base64Decode(_png).length,
        complete: limitation != 'incomplete stream', truncated: limitation == 'capture truncation');
      final preview = {..._page(_png, encoding: 'base64',
        source: limitation == 'raw fallback' ? 'raw' : 'decoded', truncated: limitation == 'page truncation'),
        if (limitation == 'decode error') 'error': 'Saved body decoding failed'};
      final harness = await _mount(tester, message: message, page: preview, nativeReader: true, handler: (call) async {
        if (call.method == 'capture.detail') return {'complete': message['bodyComplete'], 'response': message, 'responsePreview': preview};
        return null;
      });
      expect(_in(_body, find.byKey(const ValueKey('captured-body-image'))), findsNothing);
      expect(_hexRows(tester, _body), isNotEmpty);
      _expectCopyOnlyMenu(tester);
      await _action(tester, _body, 'Copy current body page');
      expect(harness.copied.last, _png);
      expect(harness.calls.where((call) => call.method == 'capture.body.view'), isEmpty);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('long body lines scroll horizontally and return to wrapping while copying the full page', (tester) async {
    final original = 'begin-${'x' * 6000}-end';
    final harness = await _mount(tester, message: _message(type: 'text/plain; charset=utf-8', saved: original.length),
      page: _page(original));
    IconButton wrapAction() => _button(tester, 'Wrap lines');
    expect(wrapAction().isSelected, isTrue);
    await _action(tester, _body, 'Wrap lines');
    expect(wrapAction().isSelected, isFalse);
    final horizontal = _in(_body, find.byWidgetPredicate((widget) =>
      widget is SingleChildScrollView && widget.scrollDirection == Axis.horizontal));
    expect(horizontal, findsOneWidget);
    final scroll = tester.widget<SingleChildScrollView>(horizontal).controller!;
    expect(scroll.position.maxScrollExtent, greaterThan(1000));
    scroll.jumpTo(scroll.position.maxScrollExtent);
    await tester.pumpAndSettle();
    expect(scroll.offset, greaterThan(1000));
    await _action(tester, _body, 'Copy displayed body');
    expect(harness.copied.last, original);
    await _action(tester, _body, 'Wrap lines');
    expect(wrapAction().isSelected, isTrue);
    expect(horizontal, findsNothing);
    expect(_text(tester, _body), original);
    expect(tester.takeException(), isNull);
  });

  testWidgets('a remote to local session change rejects late body pages and stale copy actions', (tester) async {
    final pending = Completer<Json>();
    final first = _page('a' * 16384, more: true);
    final harness = await _mount(tester, message: _message(type: 'text/plain', saved: 17000), page: first, handler: (call) async {
      if (call.method == 'capture.body') return pending.future;
      return null;
    });
    final oldCopy = _button(tester, 'Copy current body page');
    final oldDisplayedCopy = _menu(tester).items.singleWhere((item) => item.label == 'Copy displayed body');
    await _tap(tester, _in(_body, find.text('Next page')));
    expect(harness.calls.where((call) => call.method == 'capture.body').length, 1);
    harness.events.add({'type': 'dashboard', 'value': {'status': 'Started', 'remote': 'Server', 'remoteId': 1, 'remoteConnected': true}});
    await tester.pumpAndSettle();
    harness.events.add({'type': 'dashboard', 'value': {'status': 'Started'}});
    await tester.pumpAndSettle();
    pending.complete(_page('late private body from the previous session', offset: 16384));
    await tester.pumpAndSettle();
    oldCopy.onPressed!();
    oldDisplayedCopy.onPressed!();
    await tester.pumpAndSettle();
    expect(find.text('late private body from the previous session'), findsNothing);
    expect(_body, findsNothing);
    expect(harness.copied, isEmpty);
    expect(harness.calls.where((call) => call.method == 'capture.body').length, 1);
    expect(tester.takeException(), isNull);
  });

  testWidgets('live details refresh their current last page and apply body reply metadata without resetting history', (tester) async {
    final first = _page('first-', nextOffset: 6, more: true);
    var message = _message(type: 'text/plain', saved: 10, complete: false);
    var tail = _page('tail', offset: 6);
    final live = ValueNotifier<Json>({'message': message, 'page': first});
    addTearDown(live.dispose);
    final harness = await _mount(tester, message: message, page: first, nativeReader: true, live: live,
      handler: (call) async {
        if (call.method == 'capture.body') return {...(json(call.arguments)['offset'] == 0 ? first : tail), 'message': message};
        return null;
      });
    await _tap(tester, _in(_body, find.text('Next page')));
    expect(_text(tester, _body), 'tail');
    expect(_in(_body, find.text('Body page: 2')), findsOneWidget);
    message = _message(type: 'text/plain', saved: 14, complete: false);
    tail = _page('tail+new', offset: 6);
    live.value = {'message': message, 'page': first};
    await tester.pumpAndSettle();
    expect(_text(tester, _body), 'tail+new');
    expect(_in(_body, find.textContaining('14 B / 14 B')), findsOneWidget);
    expect(_in(_body, find.text('Body stream is incomplete')), findsOneWidget);
    final finalDetail = _message(type: 'text/plain', saved: 14);
    message = {...finalDetail, 'bodyBytes': 20, 'bodyTruncated': true};
    // The detail asks for a completion reread; the page contains still newer
    // truncation/counter metadata and must win over the detail snapshot.
    live.value = {'message': finalDetail, 'page': first};
    await tester.pumpAndSettle();
    expect(_in(_body, find.text('Body stream is incomplete')), findsNothing);
    expect(_in(_body, find.text('Captured body was truncated.')), findsOneWidget);
    expect(_in(_body, find.textContaining('14 B / 20 B')), findsOneWidget);
    expect(_in(_body, find.text('Body page: 2')), findsOneWidget);
    final reads = harness.calls.where((call) => call.method == 'capture.body').toList();
    expect(reads.map((call) => json(call.arguments)['offset']), [6, 6, 6]);
    expect(reads.map((call) => json(call.arguments)['readerId']).toSet(), hasLength(1));
    await tester.pump(const Duration(seconds: 6));
    expect(harness.calls.where((call) => call.method == 'capture.body'), hasLength(3));
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, 'tail+new');
    await _tap(tester, _in(_body, find.text('Previous page')));
    expect(_text(tester, _body), 'first-');
    expect(_in(_body, find.text('Body page: 1')), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('active request details update the current tail with one reader and stop polling at completion', (tester) async {
    final first = _page('head', nextOffset: 4, more: true);
    var tail = _page('tail', offset: 4);
    var message = _message(type: 'text/plain', saved: 8, complete: false);
    final harness = await _mount(tester, message: message, page: first, nativeReader: true, handler: (call) async {
      if (call.method == 'capture.detail') return {'id': 'body-fixture', 'complete': message['bodyComplete'], 'response': message, 'responsePreview': first};
      if (call.method == 'capture.body') return {...(json(call.arguments)['offset'] == 0 ? first : tail), 'message': message};
      return null;
    });
    expect(harness.calls.where((call) => call.method == 'capture.detail'), hasLength(1));
    await _tap(tester, _in(_body, find.text('Next page')));
    final reader = json(harness.calls.lastWhere((call) => call.method == 'capture.body').arguments)['readerId'];
    expect(_text(tester, _body), 'tail');
    expect(harness.calls.where((call) => call.method == 'capture.body.close' &&
      json(call.arguments)['readerId'] == reader), isEmpty);
    message = _message(type: 'text/plain', saved: 12, complete: false);
    tail = _page('tail-new', offset: 4);
    await tester.pump(const Duration(seconds: 2));
    await tester.pumpAndSettle();
    expect(_text(tester, _body), 'tail-new');
    expect(_in(_body, find.textContaining('12 B / 12 B')), findsOneWidget);
    expect(json(harness.calls.lastWhere((call) => call.method == 'capture.body').arguments)['readerId'], reader);
    message = _message(type: 'text/plain', saved: 12);
    await tester.pump(const Duration(seconds: 2));
    await tester.pumpAndSettle();
    expect(_in(_body, find.text('Body stream is incomplete')), findsNothing);
    expect(_in(_body, find.text('Body page: 2')), findsOneWidget);
    final completedPolls = harness.calls.where((call) => call.method == 'capture.detail').length;
    final completedReads = harness.calls.where((call) => call.method == 'capture.body').length;
    await tester.pump(const Duration(seconds: 6));
    await tester.pumpAndSettle();
    expect(harness.calls.where((call) => call.method == 'capture.detail'), hasLength(completedPolls));
    expect(harness.calls.where((call) => call.method == 'capture.body'), hasLength(completedReads));
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, 'tail-new');
    await _dismissFeedback(tester);
    Navigator.pop(tester.element(_body));
    await tester.pumpAndSettle();
    expect(harness.calls.where((call) => call.method == 'capture.body.close' &&
      json(call.arguments)['readerId'] == reader), hasLength(1));
    expect(find.byType(CapturedBody), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('a healthy decoded first page replaces a raw fallback reader and rejects its pending next-page reply', (tester) async {
    final pending = Completer<Json>();
    final rawFirst = _page(base64Encode(List.generate(16, (index) => index)), encoding: 'base64',
      source: 'raw', nextOffset: 16, more: true);
    final initial = _message(type: 'application/json', saved: 48);
    final message = _message(type: 'application/json', saved: 48);
    final decodedFirst = _page('{"value":', more: true);
    final decodedLast = _page('"ready"}', offset: decodedFirst['nextOffset'] as int);
    const shown = '{\n  "value": "ready"\n}';
    final live = ValueNotifier<Json>({'message': initial, 'page': rawFirst});
    addTearDown(live.dispose);
    final harness = await _mount(tester, message: initial, page: rawFirst, nativeReader: true, live: live, handler: (call) async {
      if (call.method == 'capture.body') {
        if (json(call.arguments)['source'] == 'raw') return pending.future;
        return {...decodedLast, 'message': message};
      }
      if (call.method == 'capture.body.view') return {'format': 'json', 'message': message, 'page': _page(shown, source: 'view')};
      return null;
    });
    expect(_hexRows(tester, _body), isNotEmpty);
    expect(harness.calls.where((call) => call.method == 'capture.body.view'), isEmpty);
    await _tap(tester, _in(_body, find.text('Next page')));
    final rawReader = json(harness.calls.lastWhere((call) => call.method == 'capture.body').arguments)['readerId'];
    live.value = {'message': message, 'page': decodedFirst};
    await tester.pumpAndSettle();
    expect(_text(tester, _body), shown);
    expect(_in(_body, find.text('Showing saved bytes without decoding.')), findsNothing);
    expect(harness.calls.where((call) => call.method == 'capture.body.close' &&
      json(call.arguments)['readerId'] == rawReader), hasLength(1));
    final nativeView = harness.calls.singleWhere((call) => call.method == 'capture.body.view');
    expect(json(nativeView.arguments)['format'], 'json');
    expect(json(nativeView.arguments)['readerId'], isNot(rawReader));
    pending.complete({..._page(base64Encode(List.filled(16, 0xff)), encoding: 'base64', source: 'raw',
      offset: 16, more: true), 'message': initial});
    await tester.pumpAndSettle();
    expect(_text(tester, _body), shown);
    _expectCopyOnlyMenu(tester);
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, decodedFirst['text']);
    await _tap(tester, _in(_body, find.text('Base64')));
    expect(_text(tester, _body), base64Encode(utf8.encode(decodedFirst['text'] as String)));
    expect(_in(_body, find.text('Body page: 1')), findsOneWidget);
    await _tap(tester, _in(_body, find.text('Next page')));
    expect(_text(tester, _body), base64Encode(utf8.encode(decodedLast['text'] as String)));
    expect(_in(_body, find.text('Body page: 2')), findsOneWidget);
    final reads = harness.calls.where((call) => call.method == 'capture.body').toList();
    expect(reads.map((call) => json(call.arguments)['source']), ['raw', 'decoded']);
    expect(reads.map((call) => json(call.arguments)['offset']), [16, decodedFirst['nextOffset']]);
    expect(json(reads.last.arguments)['readerId'], json(nativeView.arguments)['readerId']);
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, decodedLast['text']);
    expect(harness.calls.where((call) => call.method == 'capture.body.view'), hasLength(1));
    expect(tester.takeException(), isNull);
  });

  for (final background in [false, true]) {
    testWidgets('healthy decoded evidence received in the ${background ? 'background' : 'foreground'} interprets a kept raw page and rebinds at its first page', (tester) async {
      final rawFirst = _page(base64Encode(List.generate(16, (index) => index)), encoding: 'base64',
        source: 'raw', nextOffset: 16, more: true);
      final rawSecond = _page(base64Encode(List.generate(16, (index) => index + 16)), encoding: 'base64',
        source: 'raw', offset: 16, nextOffset: 32, more: true);
      final initial = _message(type: '', saved: 48, complete: false);
      final message = _message(type: '', saved: 48);
      final decodedFirst = _page('{"items":[', more: true);
      final decodedLast = _page('1,2,3]}', offset: decodedFirst['nextOffset'] as int);
      final derivedFirst = _page('{\n  "items": [\n', source: 'view', more: true);
      final derivedLast = _page('    1,\n    2,\n    3\n  ]\n}', source: 'view', offset: derivedFirst['nextOffset'] as int);
      final live = ValueNotifier<Json>({'message': initial, 'page': rawFirst});
      addTearDown(live.dispose);
      final harness = await _mount(tester, message: initial, page: rawFirst, nativeReader: true, live: live, handler: (call) async {
        if (call.method == 'capture.body') {
          final arguments = json(call.arguments);
          if (arguments['source'] == 'decoded') return {...decodedLast, 'message': message};
          return {...(arguments['offset'] == 0 ? rawFirst : rawSecond), 'message': live.value['message']};
        }
        if (call.method == 'capture.body.view') return {'format': 'json', 'message': message, 'page': derivedFirst};
        if (call.method == 'capture.body.view.page') return derivedLast;
        return null;
      });
      await _tap(tester, _in(_body, find.text('Next page')));
      expect(_in(_body, find.byKey(const ValueKey('captured-body-hex-line-16'))), findsOneWidget);
      expect(_in(_body, find.text('Body page: 2')), findsOneWidget);
      expect(harness.calls.where((call) => call.method == 'capture.body.view'), isEmpty);
      final rawReader = json(harness.calls.lastWhere((call) => call.method == 'capture.body').arguments)['readerId'];
      if (background) {
        tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
        tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
        tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
        await tester.pumpAndSettle();
        expect(harness.calls.where((call) => call.method == 'capture.body.close' &&
          json(call.arguments)['readerId'] == rawReader), hasLength(1));
      }
      live.value = {'message': message, 'page': decodedFirst};
      await tester.pumpAndSettle();
      if (background) {
        expect(harness.calls.where((call) => call.method == 'capture.body.view'), isEmpty);
        tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
        tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
        tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
        await tester.pumpAndSettle();
      }
      expect(_text(tester, _body), derivedFirst['text']);
      final nativeView = harness.calls.singleWhere((call) => call.method == 'capture.body.view');
      expect(json(nativeView.arguments)['format'], 'json');
      if (background) expect(json(nativeView.arguments)['readerId'], isNot(rawReader));
      _expectCopyOnlyMenu(tester);
      await _action(tester, _body, 'Copy current body page');
      expect(harness.copied.last, rawSecond['text']);
      await _tap(tester, _in(_body, find.text('Next page')));
      expect(_text(tester, _body), derivedLast['text']);
      expect(_in(_body, find.text('Derived body page: 2')), findsOneWidget);
      await _action(tester, _body, 'Copy displayed body');
      expect(harness.copied.last, derivedLast['text']);
      await _tap(tester, _in(_body, find.text('Base64')));
      expect(_text(tester, _body), rawSecond['text']);
      expect(_in(_body, find.text('Body page: 2')), findsOneWidget);
      expect(_in(_body, find.text('Showing saved bytes without decoding.')), findsOneWidget);
      await _tap(tester, _in(_body, find.text('Previous page')));
      expect(_text(tester, _body), base64Encode(utf8.encode(decodedFirst['text'] as String)));
      expect(_in(_body, find.text('Body page: 1')), findsOneWidget);
      expect(_in(_body, find.text('Showing saved bytes without decoding.')), findsNothing);
      expect(harness.calls.where((call) => call.method == 'capture.body.close' &&
        json(call.arguments)['readerId'] == json(nativeView.arguments)['readerId']), hasLength(1));
      await _tap(tester, _in(_body, find.text('Next page')));
      expect(_text(tester, _body), base64Encode(utf8.encode(decodedLast['text'] as String)));
      expect(_in(_body, find.text('Body page: 2')), findsOneWidget);
      final reads = harness.calls.where((call) => call.method == 'capture.body').toList();
      expect(reads.map((call) => json(call.arguments)['source']), ['raw', 'raw', 'decoded']);
      expect(reads.map((call) => json(call.arguments)['offset']), [16, 0, decodedFirst['nextOffset']]);
      expect(json(reads.last.arguments)['readerId'], isNot(json(nativeView.arguments)['readerId']));
      await _action(tester, _body, 'Copy current body page');
      expect(harness.copied.last, decodedLast['text']);
      expect(harness.calls.where((call) => call.method == 'capture.body.view'), hasLength(1));
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('a healthy first-page hint automatically interprets a kept decoded error page without replacing its bytes', (tester) async {
    const error = 'Saved body decoding failed';
    final first = {..._page(base64Encode([8, 1]), encoding: 'base64', more: true), 'error': error};
    final second = {..._page(base64Encode([8, 2]), encoding: 'base64', offset: 2, more: true), 'error': error};
    final healthyFirst = _page(base64Encode([8, 1]), encoding: 'base64', more: true);
    final initial = _message(type: 'application/protobuf', saved: 6);
    final message = _message(type: 'application/protobuf', saved: 6);
    const shown = '1: 1\n1: 2\n1: 3';
    final live = ValueNotifier<Json>({'message': initial, 'page': first});
    addTearDown(live.dispose);
    final harness = await _mount(tester, message: initial, page: first, nativeReader: true, live: live, handler: (call) async {
      if (call.method == 'capture.body') return {...second, 'message': initial};
      if (call.method == 'capture.body.view') return {'format': 'protobuf', 'message': message, 'page': _page(shown, source: 'view')};
      return null;
    });
    await _tap(tester, _in(_body, find.text('Next page')));
    expect(_in(_body, find.text(error)), findsOneWidget);
    expect(harness.calls.where((call) => call.method == 'capture.body.view'), isEmpty);
    live.value = {'message': message, 'page': healthyFirst};
    await tester.pumpAndSettle();
    expect(_text(tester, _body), shown);
    expect(_in(_body, find.text(error)), findsNothing);
    expect(harness.calls.where((call) => call.method == 'capture.body.view')
      .map((call) => json(call.arguments)['format']), ['protobuf']);
    _expectCopyOnlyMenu(tester);
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, second['text']);
    await _tap(tester, _in(_body, find.text('Hex')));
    expect(_in(_body, find.byKey(const ValueKey('captured-body-hex-line-2'))), findsOneWidget);
    expect(_in(_body, find.text('Body page: 2')), findsOneWidget);
    expect(_in(_body, find.text(error)), findsOneWidget);
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, second['text']);
    expect(harness.calls.where((call) => call.method == 'capture.body'), hasLength(1));
    expect(tester.takeException(), isNull);
  });

  testWidgets('an in-flight write can clear truncation and enable the complete native body view', (tester) async {
    var message = {..._message(type: 'application/protobuf', saved: 2, complete: false, truncated: true), 'bodyBytes': 4};
    var preview = _page(base64Encode([8, 1]), encoding: 'base64', truncated: true);
    final harness = await _mount(tester, message: message, page: preview, nativeReader: true, handler: (call) async {
      if (call.method == 'capture.detail') return {'complete': message['bodyComplete'], 'response': message, 'responsePreview': preview};
      if (call.method == 'capture.body.view') return {
        'format': 'protobuf', 'message': message, 'page': _page('1: 1\n1: 2', source: 'view'),
      };
      return null;
    });
    expect(_in(_body, find.text('Captured body was truncated.')), findsOneWidget);
    expect(harness.calls.where((call) => call.method == 'capture.body.view'), isEmpty);
    message = _message(type: 'application/protobuf', saved: 4);
    preview = _page(base64Encode([8, 1, 8, 2]), encoding: 'base64');
    await tester.pump(const Duration(seconds: 2));
    await tester.pumpAndSettle();
    expect(_in(_body, find.text('Captured body was truncated.')), findsNothing);
    expect(_in(_body, find.text('Body stream is incomplete')), findsNothing);
    expect(_in(_body, find.textContaining('4 B / 4 B')), findsOneWidget);
    expect(_text(tester, _body), '1: 1\n1: 2');
    expect(harness.calls.where((call) => call.method == 'capture.body.view'), hasLength(1));
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, preview['text']);
    expect(tester.takeException(), isNull);
  });

  testWidgets('a late detail preview cannot replace a newer body page or restore transient truncation', (tester) async {
    final pending = Completer<Json>();
    final oldMessage = {..._message(type: 'text/plain', saved: 8, complete: false, truncated: true), 'bodyBytes': 12};
    final oldHead = _page('old-', nextOffset: 4, more: true, truncated: true);
    final message = _message(type: 'text/plain', saved: 12);
    final newHead = _page('NEW-', nextOffset: 4, more: true);
    final tail = _page('new-tail', offset: 4);
    final harness = await _mount(tester, message: oldMessage, page: oldHead, nativeReader: true, handler: (call) async {
      if (call.method == 'capture.detail') return pending.future;
      if (call.method == 'capture.body') return {
        ...(json(call.arguments)['offset'] == 0 ? newHead : tail), 'message': message,
      };
      return null;
    });
    await _tap(tester, _in(_body, find.text('Next page')));
    await _tap(tester, _in(_body, find.text('Previous page')));
    expect(_text(tester, _body), 'NEW-');
    pending.complete({'complete': oldMessage['bodyComplete'], 'response': oldMessage, 'responsePreview': oldHead});
    await tester.pumpAndSettle();
    expect(_text(tester, _body), 'NEW-');
    expect(_in(_body, find.textContaining('12 B / 12 B')), findsOneWidget);
    expect(_in(_body, find.text('Captured body was truncated.')), findsNothing);
    expect(_in(_body, find.text('Body stream is incomplete')), findsNothing);
    expect(_in(_body, find.text('Body page: 1')), findsOneWidget);
    expect(harness.calls.where((call) => call.method == 'capture.body'), hasLength(2));
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, 'NEW-');
    expect(tester.takeException(), isNull);
  });

  testWidgets('completion during a pending next-page read refreshes the late tail without adding a page', (tester) async {
    final pending = Completer<Json>();
    final first = _page('head', nextOffset: 4, more: true);
    final initial = _message(type: 'text/plain', saved: 8, complete: false);
    final finalMessage = _message(type: 'text/plain', saved: 12);
    final live = ValueNotifier<Json>({'message': initial, 'page': first});
    addTearDown(live.dispose);
    var reads = 0;
    final harness = await _mount(tester, message: initial, page: first, nativeReader: true, live: live, handler: (call) async {
      if (call.method != 'capture.body') return null;
      if (++reads == 1) return pending.future;
      return {..._page('tail-new', offset: 4), 'message': finalMessage};
    });
    await _tap(tester, _in(_body, find.text('Next page')));
    live.value = {'message': finalMessage, 'page': first};
    await tester.pumpAndSettle();
    pending.complete({..._page('tail', offset: 4), 'message': initial});
    await tester.pumpAndSettle();
    expect(_text(tester, _body), 'tail-new');
    expect(_in(_body, find.text('Body page: 2')), findsOneWidget);
    expect(_in(_body, find.text('Body stream is incomplete')), findsNothing);
    expect(_in(_body, find.textContaining('12 B / 12 B')), findsOneWidget);
    final bodyReads = harness.calls.where((call) => call.method == 'capture.body').toList();
    expect(bodyReads.map((call) => json(call.arguments)['offset']), [4, 4]);
    expect(bodyReads.map((call) => json(call.arguments)['readerId']).toSet(), hasLength(1));
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, 'tail-new');
    expect(tester.takeException(), isNull);
  });

  testWidgets('native charset display text is selectable while body copying and byte views keep the original page', (tester) async {
    final original = base64Encode([0xc4, 0xe3, 0xba, 0xc3]);
    final harness = await _mount(tester, message: _message(type: 'text/plain; charset=gbk', saved: 4, complete: false),
      page: {..._page(original, encoding: 'base64', offset: 16384), 'charset': 'gbk', 'displayText': '你好😀'});
    expect(_text(tester, _body), '你好😀');
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, original);
    await _action(tester, _body, 'Copy displayed body');
    expect(harness.copied.last, '你好😀');
    await _tap(tester, _in(_body, find.text('Hex')));
    expect(_hexRows(tester, _body).first.data, contains('c4 e3'));
    expect(_hexRows(tester, _body).first.data, startsWith('004000'));
    await _tap(tester, _in(_body, find.text('Base64')));
    expect(_text(tester, _body), original);
    expect(tester.takeException(), isNull);
  });

  for (final mime in ['image/png', '', 'application/json']) {
    testWidgets('complete images larger than the body page with ${mime.isEmpty ? 'no MIME' : mime} display from a bounded file view and retain their format on byte pages', (tester) async {
      final fullImage = _largePng();
      expect(fullImage.length, greaterThan(65536));
      late Directory directory;
      late File file;
      await tester.runAsync(() async {
        final fixtureRoot = Directory('${Platform.environment['HOME'] ?? Directory.systemTemp.path}/tmp');
        await fixtureRoot.create(recursive: true);
        directory = await fixtureRoot.createTemp('captured-body-view-');
        file = await File('${directory.path}/complete.png').writeAsBytes(fullImage);
      });
      addTearDown(() async { if (await directory.exists()) await directory.delete(recursive: true); });
      final first = _page(base64Encode(fullImage.sublist(0, 16384)), encoding: 'base64', nextOffset: 16384, more: true);
      final second = _page(base64Encode(fullImage.sublist(16384, 32768)), encoding: 'base64',
        offset: 16384, nextOffset: 32768, more: true);
      final message = _message(type: mime, saved: fullImage.length);
      final harness = await _mount(tester, message: message, page: first, nativeReader: true, handler: (call) async {
        if (call.method == 'capture.detail') return {'complete': message['bodyComplete'], 'response': message, 'responsePreview': first};
        if (call.method == 'capture.body') return {...second, 'message': message};
        if (call.method == 'capture.body.view') {
          expect(json(call.arguments)['format'], 'image');
          return {'format': 'image', 'message': message, 'path': file.path};
        }
        return null;
      });
      final imageFinder = _in(_body, find.byKey(const ValueKey('captured-body-image')));
      await _decodeImage(tester, imageFinder);
      final provider = tester.widget<Image>(imageFinder).image as ResizeImage;
      expect(provider.imageProvider, isA<FileImage>());
      expect((provider.imageProvider as FileImage).file.path, file.path);
      expect(provider.width, inInclusiveRange(1, 2048));
      expect(provider.height, inInclusiveRange(1, 2048));
      expect(harness.calls.where((call) => call.method == 'capture.body.view'), hasLength(1));
      expect(harness.calls.where((call) => call.method == 'capture.body'), isEmpty);
      _expectCopyOnlyMenu(tester);
      if (mime == 'image/png') await _capture(tester, harness, '360-light-large-image');
      await _action(tester, _body, 'Copy current body page');
      expect(harness.copied.last, first['text']);
      await _tap(tester, _in(_body, find.text('Hex')));
      expect(_hexRows(tester, _body).first.data, contains('89 50 4e 47'));
      await _tap(tester, _in(_body, find.text('Next page')));
      expect(_in(_body, find.byKey(const ValueKey('captured-body-hex-line-16384'))), findsOneWidget);
      expect(_in(_body, find.text('Body page: 2')), findsOneWidget);
      await _tap(tester, _in(_body, find.text('Image')));
      await _decodeImage(tester, _in(_body, find.byKey(const ValueKey('captured-body-image'))));
      expect(harness.calls.where((call) => call.method == 'capture.body.view'), hasLength(1));
      expect(harness.calls.where((call) => call.method == 'capture.body'), hasLength(1));
      await _action(tester, _body, 'Copy current body page');
      expect(harness.copied.last, second['text']);
      final reader = json(harness.calls.lastWhere((call) => call.method == 'capture.body.view').arguments)['readerId'];
      Navigator.pop(tester.element(_body));
      await tester.pumpAndSettle();
      expect(harness.calls.where((call) => call.method == 'capture.body.close' && json(call.arguments)['readerId'] == reader), hasLength(1));
      expect(tester.takeException(), isNull);
    });
  }

  for (final (format, mime) in [('json', 'application/json'), ('json', ''),
    ('protobuf', 'application/protobuf'), ('grpc', 'application/grpc+proto')]) {
    testWidgets('complete large $format ${mime.isEmpty ? 'without MIME' : 'with its MIME'} views page derived UTF-8 separately from the original body and history', (tester) async {
      final proto = Uint8List.fromList([for (var index = 0; index < 10000; index++) ...[8, 1]]);
      final body = format == 'json' ? Uint8List.fromList(utf8.encode('{"id":18446744073709551615,"payload":"${'a' * 20000}"}')) :
        format == 'protobuf' ? proto : Uint8List.fromList([0, ...(ByteData(4)..setUint32(0, proto.length)).buffer.asUint8List(), ...proto]);
      final binary = format != 'json';
      String wire(List<int> bytes) => binary ? base64Encode(bytes) : utf8.decode(bytes);
      final first = _page(wire(body.sublist(0, 16384)), encoding: binary ? 'base64' : 'text', nextOffset: 16384, more: true);
      final last = _page(wire(body.sublist(16384)), encoding: binary ? 'base64' : 'text', offset: 16384);
      final message = _message(type: mime, saved: body.length);
      final shownFirst = '名字😀\n${'a' * 16365}';
      final next = utf8.encode(shownFirst).length;
      final shownLast = format == 'json' ? '  "id": 18446744073709551615\n}' : '1: 18446744073709551615\n2: "名字😀"';
      final derivedFirst = {..._page(shownFirst, source: 'view', nextOffset: next, more: true),
        'message': _message(saved: next + utf8.encode(shownLast).length)};
      final derivedLast = {..._page(shownLast, source: 'view', offset: next),
        'message': _message(saved: next + utf8.encode(shownLast).length)};
      final harness = await _mount(tester, message: message, page: first, nativeReader: true, handler: (call) async {
        if (call.method == 'capture.detail') return {'complete': message['bodyComplete'], 'response': message, 'responsePreview': first};
        if (call.method == 'capture.body') return {...(json(call.arguments)['offset'] == 0 ? first : last), 'message': message};
        if (call.method == 'capture.body.view') return {'format': format, 'message': message, 'page': derivedFirst};
        if (call.method == 'capture.body.view.page') {
          expect(json(call.arguments)['offset'], next);
          return derivedLast;
        }
        return null;
      });
      expect(harness.calls.where((call) => call.method == 'capture.body.view'), hasLength(1));
      expect(_text(tester, _body), shownFirst);
      _expectCopyOnlyMenu(tester);
      await _tap(tester, _in(_body, find.text('Base64')));
      expect(_text(tester, _body), base64Encode(body.sublist(0, 16384)));
      await _tap(tester, _in(_body, find.text('Next page')));
      expect(_text(tester, _body), base64Encode(body.sublist(16384)));
      await _tap(tester, _in(_body, find.text(switch (format) {
        'json' => 'JSON', 'protobuf' => 'Protobuf', _ => 'gRPC',
      })));
      expect(_text(tester, _body), shownFirst);
      expect(_in(_body, find.text('Derived body page: 1')), findsOneWidget);
      await _tap(tester, _in(_body, find.text('Next page')));
      expect(_text(tester, _body), shownLast);
      expect(_in(_body, find.text('Derived body page: 2')), findsOneWidget);
      expect(_in(_body, find.textContaining('${bytes(body.length)} / ${bytes(body.length)}')), findsOneWidget);
      await _action(tester, _body, 'Copy displayed body');
      expect(harness.copied.last, shownLast);
      await _action(tester, _body, 'Copy current body page');
      expect(harness.copied.last, last['text']);
      if (format == 'json') {
        await _action(tester, _body, 'Show original');
        expect(_text(tester, _body), last['text']);
        expect(_in(_body, find.text('Body page: 2')), findsOneWidget);
        await _action(tester, _body, 'Format JSON');
        expect(_text(tester, _body), shownLast);
      }
      await _tap(tester, _in(_body, find.text('Hex')));
      expect(_in(_body, find.byKey(const ValueKey('captured-body-hex-line-16384'))), findsOneWidget);
      expect(_in(_body, find.text('Body page: 2')), findsOneWidget);
      await _tap(tester, _in(_body, find.text('Base64')));
      expect(_text(tester, _body), base64Encode(body.sublist(16384)));
      if (mime.isEmpty) {
        await _tap(tester, _in(_body, find.text('JSON')));
        final reader = json(harness.calls.lastWhere((call) => call.method == 'capture.body.view').arguments)['readerId'];
        tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
        tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
        tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
        await tester.pumpAndSettle();
        expect(harness.calls.where((call) => call.method == 'capture.body.close' &&
          json(call.arguments)['readerId'] == reader), hasLength(1));
        tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
        tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
        tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
        await tester.pumpAndSettle();
        expect(_text(tester, _body), shownLast);
        expect(_in(_body, find.text('Derived body page: 2')), findsOneWidget);
        expect(json(harness.calls.lastWhere((call) => call.method == 'capture.body.view').arguments)['readerId'], isNot(reader));
        _expectCopyOnlyMenu(tester);
        await _action(tester, _body, 'Copy current body page');
        expect(harness.copied.last, last['text']);
        await _tap(tester, _in(_body, find.text('Base64')));
        expect(_text(tester, _body), base64Encode(body.sublist(16384)));
        expect(_in(_body, find.text('Body page: 2')), findsOneWidget);
      }
      expect(harness.calls.where((call) => call.method == 'capture.body.view'), hasLength(mime.isEmpty ? 2 : 1));
      expect(harness.calls.where((call) => call.method == 'capture.body.view.page'), hasLength(mime.isEmpty ? 2 : 1));
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('scrolling request details preserves original and derived page histories and the chosen display', (tester) async {
    final first = _page(base64Encode([8, 1]), encoding: 'base64', nextOffset: 2, more: true);
    final last = _page(base64Encode([8, 2]), encoding: 'base64', offset: 2);
    final message = _message(type: 'application/protobuf', saved: 4);
    final derivedFirst = _page('1: 1\n', source: 'view', more: true);
    final derivedLast = _page('1: 2', source: 'view', offset: derivedFirst['nextOffset'] as int);
    final harness = await _mount(tester, message: message, page: first, nativeReader: true,
      afterBody: const [SizedBox(height: 1200), Text('After request body')], handler: (call) async {
        if (call.method == 'capture.detail') return {'complete': true, 'response': message, 'responsePreview': first};
        if (call.method == 'capture.body') return {...last, 'message': message};
        if (call.method == 'capture.body.view') return {'format': 'protobuf', 'message': message, 'page': derivedFirst};
        if (call.method == 'capture.body.view.page') return derivedLast;
        return null;
      });
    await _tap(tester, _in(_body, find.text('Next page')));
    expect(_text(tester, _body), '1: 2');
    await _tap(tester, _in(_body, find.text('Base64')));
    await _tap(tester, _in(_body, find.text('Next page')));
    expect(_text(tester, _body), last['text']);
    await _tap(tester, _in(_body, find.text('Protobuf')));
    await _action(tester, _body, 'Wrap lines');
    expect(_button(tester, 'Wrap lines').isSelected, isFalse);
    final state = tester.state(find.byType(CapturedBody));
    final detailsScroll = Scrollable.of(tester.element(_body)).position;
    detailsScroll.jumpTo(detailsScroll.maxScrollExtent);
    await tester.pumpAndSettle();
    expect(find.text('After request body').hitTestable(), findsOneWidget);
    detailsScroll.jumpTo(0);
    await tester.pumpAndSettle();
    expect(tester.state(find.byType(CapturedBody)), same(state));
    expect(_text(tester, _body), '1: 2');
    expect(_in(_body, find.text('Derived body page: 2')), findsOneWidget);
    expect(_button(tester, 'Wrap lines').isSelected, isFalse);
    await _tap(tester, _in(_body, find.text('Base64')));
    expect(_text(tester, _body), last['text']);
    expect(_in(_body, find.text('Body page: 2')), findsOneWidget);
    expect(harness.calls.where((call) => call.method == 'capture.body.view'), hasLength(1));
    expect(harness.calls.where((call) => call.method == 'capture.body.view.page'), hasLength(1));
    expect(harness.calls.where((call) => call.method == 'capture.body'), hasLength(1));
    expect(harness.calls.where((call) => call.method == 'capture.body.close'), isEmpty);
    expect(tester.takeException(), isNull);
  });

  testWidgets('an untyped paged binary body stays in byte views and never chooses a complete parser', (tester) async {
    final original = base64Encode([for (var index = 0; index < 8192; index++) ...[8, 1]]);
    final first = _page(original, encoding: 'base64', more: true);
    final message = _message(saved: 20000);
    final harness = await _mount(tester, message: message, page: first, nativeReader: true, handler: (call) async {
      if (call.method == 'capture.detail') return {'complete': message['bodyComplete'], 'response': message, 'responsePreview': first};
      return null;
    });
    expect(harness.calls.where((call) => call.method == 'capture.body.view'), isEmpty);
    expect(_hexRows(tester, _body), isNotEmpty);
    _expectCopyOnlyMenu(tester);
    await _action(tester, _body, 'Copy displayed body');
    expect(harness.copied.last, startsWith('000000  08 01'));
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, original);
    await _tap(tester, _in(_body, find.text('Base64')));
    expect(_text(tester, _body), original);
    expect(harness.calls.where((call) => call.method == 'capture.body.view'), isEmpty);
    expect(tester.takeException(), isNull);
  });

  testWidgets('valid JSON automatically overrides a misleading image MIME and copies displayed text separately', (tester) async {
    const original = '{"id":18446744073709551615}';
    const shown = '{\n  "id": 18446744073709551615\n}';
    final first = _page(original);
    final message = _message(type: 'image/png', saved: original.length);
    final harness = await _mount(tester, message: message, page: first, nativeReader: true, handler: (call) async {
      if (call.method == 'capture.detail') return {'complete': message['bodyComplete'], 'response': message, 'responsePreview': first};
      return null;
    });
    expect(_in(_body, find.byKey(const ValueKey('captured-body-image'))), findsNothing);
    expect(_text(tester, _body), shown);
    expect(_in(_body, find.byTooltip('Reset zoom')), findsNothing);
    expect(harness.calls.where((call) => call.method == 'capture.body.view'), isEmpty);
    _expectCopyOnlyMenu(tester);
    await _action(tester, _body, 'Copy displayed body');
    expect(harness.copied.last, shown);
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, original);
    expect(tester.takeException(), isNull);
  });

  testWidgets('a native derived body cancels in the background and restores its derived page and history on resume', (tester) async {
    final first = _page(base64Encode(List.filled(16384, 8)), encoding: 'base64', more: true);
    final message = _message(type: 'application/protobuf', saved: 20000);
    final shownFirst = _page('1: 1\n' * 3276, source: 'view', more: true);
    final next = shownFirst['nextOffset'] as int;
    final shownLast = _page('1: 2', source: 'view', offset: next);
    final harness = await _mount(tester, message: message, page: first, nativeReader: true, handler: (call) async {
      if (call.method == 'capture.detail') return {'complete': message['bodyComplete'], 'response': message, 'responsePreview': first};
      if (call.method == 'capture.body.view') return {'format': 'protobuf', 'message': message, 'page': shownFirst};
      if (call.method == 'capture.body.view.page') return shownLast;
      return null;
    });
    await _tap(tester, _in(_body, find.text('Next page')));
    expect(_text(tester, _body), '1: 2');
    final reader = json(harness.calls.lastWhere((call) => call.method == 'capture.body.view').arguments)['readerId'];
    expect(harness.calls.where((call) => call.method == 'capture.body.view.page').map((call) => json(call.arguments)).toList(), [
      {'readerId': reader, 'offset': next},
    ]);
    final session = harness.box.sessionRevision;
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    await tester.pumpAndSettle();
    expect(harness.calls.where((call) => call.method == 'capture.body.close' && json(call.arguments)['readerId'] == reader), hasLength(1));
    await tester.pump(const Duration(seconds: 4));
    expect(harness.calls.where((call) => call.method == 'capture.body.view'), hasLength(1));
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pumpAndSettle();
    final bodies = _body.evaluate().toList();
    final bodyContext = bodies.isEmpty ? null : bodies.first;
    final resumeState = jsonEncode({
      'lifecycle': tester.binding.lifecycleState.toString(),
      'session': harness.box.sessionRevision,
      'remote': harness.box.remote,
      'bodyCount': bodies.length,
      'pageActive': bodyContext == null ? null : PageActivityScope.isActiveOf(bodyContext),
      'tickerEnabled': bodyContext == null ? null : TickerMode.of(bodyContext),
      'routeCurrent': bodyContext == null ? null : ModalRoute.of(bodyContext)?.isCurrent,
      'calls': [for (final call in harness.calls.where((call) => call.method.startsWith('capture.')))
        {'method': call.method, 'arguments': call.arguments}],
      'bodyLabels': tester.widgetList<Text>(_in(_body, find.byType(Text)))
        .map((text) => text.data).whereType<String>().take(12).toList(),
    });
    expect(tester.binding.lifecycleState, AppLifecycleState.resumed, reason: resumeState);
    expect(harness.box.sessionRevision, session, reason: resumeState);
    expect(harness.box.remote, isFalse, reason: resumeState);
    expect(_body, findsOneWidget, reason: resumeState);
    expect(harness.calls.where((call) => call.method == 'capture.body.view'), hasLength(2), reason: resumeState);
    expect(harness.calls.where((call) => call.method == 'capture.body.view.page'), hasLength(2), reason: resumeState);
    final resumedReader = json(harness.calls.lastWhere((call) => call.method == 'capture.body.view').arguments)['readerId'];
    expect(harness.calls.where((call) => call.method == 'capture.body.view.page').map((call) => json(call.arguments)).toList(), [
      {'readerId': reader, 'offset': next},
      {'readerId': resumedReader, 'offset': next},
    ], reason: resumeState);
    expect(_in(_body, find.byKey(const ValueKey('captured-body-text'))), findsOneWidget, reason: resumeState);
    expect(_text(tester, _body), '1: 2', reason: resumeState);
    expect(_in(_body, find.text('Derived body page: 2')), findsOneWidget, reason: resumeState);
    expect(resumedReader, isNot(reader), reason: resumeState);
    expect(tester.takeException(), isNull);
  });

  testWidgets('leaving request details closes its in-flight native body reader and drops its late reply', (tester) async {
    final pending = Completer<Json>();
    final first = _page('a' * 16384, more: true);
    final harness = await _mount(tester, message: _message(type: 'text/plain', saved: 20000, complete: false),
      page: first, nativeReader: true, handler: (call) async => call.method == 'capture.body' ? pending.future : null);
    await _tap(tester, _in(_body, find.text('Next page')));
    final reader = json(harness.calls.lastWhere((call) => call.method == 'capture.body').arguments)['readerId'];
    Navigator.pop(tester.element(_body));
    await tester.pumpAndSettle();
    expect(find.byType(CapturedBody), findsNothing);
    expect(harness.calls.where((call) => call.method == 'capture.body.close' && json(call.arguments)['readerId'] == reader), hasLength(1));
    pending.complete(_page('late private body', offset: 16384));
    await tester.pumpAndSettle();
    expect(find.text('late private body'), findsNothing);
    expect(harness.calls.where((call) => call.method == 'capture.body'), hasLength(1));
    expect(tester.takeException(), isNull);
  });

  testWidgets('a canceled native view cannot restore data after a session change and can be retried after read failure', (tester) async {
    final pending = Completer<Json>();
    var fail = true;
    final first = _page(base64Encode(List.filled(16384, 8)), encoding: 'base64', more: true);
    final message = _message(type: 'application/protobuf', saved: 20000);
    final harness = await _mount(tester, message: message, page: first, nativeReader: true, handler: (call) async {
      if (call.method == 'capture.detail') return {'complete': message['bodyComplete'], 'response': message, 'responsePreview': first};
      if (call.method == 'capture.body.view') {
        if (fail) throw PlatformException(code: 'capture', message: 'Saved body could not be opened');
        return pending.future;
      }
      return null;
    });
    expect(_in(_body, find.text('Saved body could not be opened')), findsOneWidget);
    expect(_hexRows(tester, _body), isNotEmpty);
    fail = false;
    await _tap(tester, _in(_body, find.text('Retry body view')));
    final reader = json(harness.calls.lastWhere((call) => call.method == 'capture.body.view').arguments)['readerId'];
    final oldCopy = _button(tester, 'Copy current body page');
    harness.events.add({'type': 'dashboard', 'value': {'status': 'Started', 'remote': 'Server', 'remoteId': 1, 'remoteConnected': true}});
    await tester.pumpAndSettle();
    harness.events.add({'type': 'dashboard', 'value': {'status': 'Started'}});
    await tester.pumpAndSettle();
    pending.complete({'format': 'protobuf', 'message': message, 'page': _page('late private fields', source: 'view')});
    await tester.pumpAndSettle();
    oldCopy.onPressed!();
    await tester.pumpAndSettle();
    expect(find.text('late private fields'), findsNothing);
    expect(_body, findsNothing);
    expect(harness.copied, isEmpty);
    expect(harness.calls.where((call) => call.method == 'capture.body.close' && json(call.arguments)['readerId'] == reader), hasLength(1));
    expect(harness.calls.where((call) => call.method == 'capture.body.view'), hasLength(2));
    expect(tester.takeException(), isNull);
  });

  for (final coalesced in [false, true]) {
    testWidgets('a ${coalesced ? 'coalesced remote/local' : 'remote'} session change closes a native reader before its view replies', (tester) async {
      final pending = Completer<Json>();
      final first = _page(base64Encode(List.filled(16384, 8)), encoding: 'base64', more: true);
      final message = _message(type: 'application/protobuf', saved: 20000);
      final harness = await _mount(tester, message: message, page: first, nativeReader: true, handler: (call) async {
        if (call.method == 'capture.detail') return {'complete': message['bodyComplete'], 'response': message, 'responsePreview': first};
        if (call.method == 'capture.body.view') return pending.future;
        return null;
      });
      final reader = json(harness.calls.lastWhere((call) => call.method == 'capture.body.view').arguments)['readerId'];
      harness.events.add({'type': 'dashboard', 'value': {'status': 'Started', 'remote': 'Server', 'remoteId': 1, 'remoteConnected': true}});
      if (coalesced) harness.events.add({'type': 'dashboard', 'value': {'status': 'Started'}});
      await tester.pumpAndSettle();
      expect(harness.calls.where((call) => call.method == 'capture.body.close' && json(call.arguments)['readerId'] == reader), hasLength(1));
      pending.complete({'format': 'protobuf', 'message': message, 'page': _page('late private fields', source: 'view')});
      await tester.pumpAndSettle();
      expect(find.text('late private fields'), findsNothing);
      expect(_body, findsNothing);
      expect(harness.calls.where((call) => call.method == 'capture.body.view'), hasLength(1));
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('covering request details cancels a pending native view and returning uses a new reader', (tester) async {
    final pending = Completer<Json>();
    final first = _page(base64Encode(List.filled(16384, 8)), encoding: 'base64', more: true);
    final message = _message(type: 'application/protobuf', saved: 20000);
    var views = 0;
    final harness = await _mount(tester, message: message, page: first, nativeReader: true, handler: (call) async {
      if (call.method == 'capture.detail') return {'complete': message['bodyComplete'], 'response': message, 'responsePreview': first};
      if (call.method == 'capture.body.view') {
        if (++views == 1) return pending.future;
        return {'format': 'protobuf', 'message': message, 'page': _page('1: 2', source: 'view')};
      }
      return null;
    });
    final reader = json(harness.calls.lastWhere((call) => call.method == 'capture.body.view').arguments)['readerId'];
    openPage(tester.element(_body), const BoxPage(title: 'Other page', children: [Text('Other content')]));
    await tester.pumpAndSettle();
    expect(harness.calls.where((call) => call.method == 'capture.body.close' && json(call.arguments)['readerId'] == reader), hasLength(1));
    pending.complete({'format': 'protobuf', 'message': message, 'page': _page('late private fields', source: 'view')});
    await tester.pumpAndSettle();
    expect(find.text('late private fields', skipOffstage: false), findsNothing);
    Navigator.pop(tester.element(find.text('Other content')));
    await tester.pumpAndSettle();
    expect(_text(tester, _body), '1: 2');
    expect(harness.calls.where((call) => call.method == 'capture.body.view'), hasLength(2));
    expect(json(harness.calls.lastWhere((call) => call.method == 'capture.body.view').arguments)['readerId'], isNot(reader));
    await _action(tester, _body, 'Copy current body page');
    expect(harness.copied.last, first['text']);
    expect(tester.takeException(), isNull);
  });

  for (final layout in [(320.0, 1.8, false), (360.0, 1.4, true)]) {
    testWidgets('the body in request details fits ${layout.$1.toInt()} wide with ${layout.$2} system text scaling', (tester) async {
      final data = Uint8List.fromList(List.generate(256, (index) => index));
      final harness = await _mount(tester, size: Size(layout.$1, 568), scale: layout.$2, dark: layout.$3,
        message: _message(type: 'application/octet-stream', saved: data.length),
        page: _page(base64Encode(data), encoding: 'base64', source: 'raw'));
      expect(MediaQuery.textScalerOf(tester.element(_body)).scale(1), closeTo(1.4, 0.0001));
      final offsets = _hexRows(tester, _body).map((row) => int.parse((row.key! as ValueKey<String>).value.split('-').last)).toList()..sort();
      expect(offsets.length, greaterThan(1));
      expect(offsets[1] - offsets[0], lessThanOrEqualTo(4));
      final label = '${layout.$1.toInt()}-${layout.$2}-${layout.$3 ? 'dark' : 'light'}';
      await _capture(tester, harness, '$label-hex');
      final list = tester.widget<ListView>(_hexList(_body));
      list.controller!.jumpTo(list.controller!.position.maxScrollExtent);
      await tester.pumpAndSettle();
      expect(_hexRows(tester, _body).last.data, contains('ff'));
      await _capture(tester, harness, '$label-hex-end');
      await _tap(tester, _in(_body, find.text('Base64')));
      expect(_text(tester, _body), base64Encode(data));
      await _action(tester, _body, 'Copy current body page');
      expect(harness.copied.last, base64Encode(data));
      await _dismissFeedback(tester);
      await _capture(tester, harness, '$label-base64');
      expect(tester.takeException(), isNull);
    });
  }
}
