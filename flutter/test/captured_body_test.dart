import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';
import 'dart:ui' as ui;

import 'package:b_box_ui/components/captured_body.dart';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:flutter/painting.dart' show ImageStreamListener, ResizeImage, ResizeImagePolicy;
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/errors.dart';

// A complete 32 x 24 RGB PNG; generated from a solid blue pixel buffer.
const _png = 'iVBORw0KGgoAAAANSUhEUgAAACAAAAAYCAIAAAAUMWhjAAAAJElEQVR4nGPQaLpEU8QwasGoBaMWjFowasGoBaMWjFowNCwAAKq6dD0cp/caAAAAAElFTkSuQmCC';

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
  bool dark = false}) async {
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
  openPage(tester.element(find.byType(HomePage)), BoxPage(title: 'Request details', children: [
    CapturedBody(message: message, preview: page, loadPage: handler == null ? null : (offset, source) async =>
      json(await harness.box.call('capture.body', {
        'id': 'body-fixture', 'direction': 'response', 'offset': offset, 'source': source,
      }))),
  ]));
  await tester.pumpAndSettle();
  return harness;
}

Finder get _inline => find.byType(CapturedBody).first;
Finder get _viewer => find.byKey(const ValueKey('captured-body-viewer'));
Finder _in(Finder scope, Finder target) => find.descendant(of: scope, matching: target);
String _text(WidgetTester tester, Finder scope) => tester.widget<SelectableText>(
  _in(scope, find.byKey(const ValueKey('captured-body-text')))).data!;

Future<void> _dismissFeedback(WidgetTester tester) async {
  final banner = find.text('Copied').hitTestable();
  if (banner.evaluate().isNotEmpty) {
    await tester.fling(banner.last, const Offset(0, -120), 1000);
    await tester.pumpAndSettle();
  }
}

Future<void> _tap(WidgetTester tester, Finder target) async {
  await _dismissFeedback(tester);
  await tester.ensureVisible(target.last);
  await tester.pumpAndSettle();
  await tester.tap(target.hitTestable().last);
  await tester.pumpAndSettle();
}

Future<void> _action(WidgetTester tester, Finder scope, String label) async {
  await _dismissFeedback(tester);
  final direct = _in(scope, find.byTooltip(label)).hitTestable();
  if (direct.evaluate().isNotEmpty) {
    await _tap(tester, direct);
  } else {
    // The existing Scaffold folds extra actions into More on narrow pages.
    await _tap(tester, _in(scope, find.byTooltip('More')));
    await _tap(tester, find.text(label).hitTestable());
  }
}

Future<void> _openViewer(WidgetTester tester) async {
  await _action(tester, _inline, 'View body');
  expect(_viewer, findsOneWidget);
}

Finder _hexList(Finder scope) => _in(scope, find.byWidgetPredicate((widget) =>
  widget is ListView && widget.childrenDelegate is SliverChildBuilderDelegate));

List<SelectableText> _hexRows(WidgetTester tester, Finder scope) => tester.widgetList<SelectableText>(
  _in(scope, find.byWidgetPredicate((widget) => widget is SelectableText &&
    widget.key is ValueKey<String> && (widget.key! as ValueKey<String>).value.startsWith('captured-body-hex-line-')))).toList();

Future<void> _decodeImage(WidgetTester tester, Finder imageFinder) async {
  final image = tester.widget<Image>(imageFinder);
  final stream = image.image.resolve(createLocalImageConfiguration(tester.element(imageFinder)));
  var decoded = false;
  final listener = ImageStreamListener((_, _) {
    decoded = true;
  }, onError: (Object error, StackTrace? stack) {
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

  testWidgets('the body viewer copies displayed JSON separately and keeps the inline view unchanged', (tester) async {
    const raw = r'{"id":18446744073709551615,"same":1,"same":2,"value":"a\\b\u4f60"}';
    final harness = await _mount(tester, message: _message(type: 'application/json', saved: raw.length), page: _page(raw));
    final formatted = _text(tester, _inline);
    expect(formatted, contains('\n  "id": 18446744073709551615,'));
    expect(formatted, contains('"same": 1,\n  "same": 2,'));
    await _action(tester, _inline, 'Copy current body page');
    expect(harness.copied.last, raw);
    await _openViewer(tester);
    expect(_text(tester, _viewer), formatted);
    await _action(tester, _viewer, 'Copy displayed body');
    expect(harness.copied.last, formatted);
    await _action(tester, _viewer, 'Show original');
    expect(_text(tester, _viewer), raw);
    await _action(tester, _viewer, 'Copy current body page');
    expect(harness.copied.last, raw);
    await _dismissFeedback(tester);
    Navigator.pop(tester.element(_viewer));
    await tester.pumpAndSettle();
    expect(_text(tester, _inline), formatted);
    expect(tester.takeException(), isNull);
  });

  testWidgets('malformed body data stays visible and can be copied from its error view', (tester) async {
    const original = 'bad===';
    final harness = await _mount(tester, message: _message(saved: 4),
      page: {'text': original, 'encoding': 'base64', 'source': 'decoded', 'offset': 0, 'hasMore': false});
    expect(_in(_inline, find.text('Body data could not be read.')), findsOneWidget);
    expect(_text(tester, _inline), original);
    expect(_hexRows(tester, _inline), isEmpty);
    await _action(tester, _inline, 'Copy current body page');
    expect(harness.copied.last, original);
    await _openViewer(tester);
    await _action(tester, _viewer, 'Copy displayed body');
    expect(harness.copied.last, original);
    expect(_text(tester, _viewer), original);
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
    await _action(tester, _inline, 'Copy current body page');
    expect(find.text('Clipboard denied'), findsOneWidget);
    expect(find.text('Copied'), findsNothing);
    expect(harness.copied, isEmpty);
    expect(_text(tester, _inline), original);
    await tester.fling(find.text('Clipboard denied'), const Offset(0, -120), 1000);
    await tester.pumpAndSettle();
    reject = false;
    await _action(tester, _inline, 'Copy current body page');
    expect(harness.copied.last, original);
    expect(find.text('Copied'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('binary paging and its viewer retain raw source, byte offsets and previous-page history', (tester) async {
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
    expect(_in(_inline, find.byKey(const ValueKey('captured-body-hex-line-0'))), findsOneWidget);
    await _tap(tester, _in(_inline, find.text('Base64')));
    expect(_text(tester, _inline), first['text']);
    await _tap(tester, _in(_inline, find.text('Next page')));
    expect(_text(tester, _inline), last['text']);
    await _openViewer(tester);
    expect(_text(tester, _viewer), last['text']);
    expect(_in(_viewer, find.text('Previous page')), findsOneWidget);
    await _tap(tester, _in(_viewer, find.text('Hex')));
    final hex = tester.widget<SelectableText>(_in(_viewer, find.byKey(const ValueKey('captured-body-hex-line-16')))).data!;
    expect(hex, startsWith('000010  10'));
    await _action(tester, _viewer, 'Copy displayed body');
    expect(harness.copied.last, startsWith('000010  10'));
    expect(harness.copied.last, contains('2f'));
    await _action(tester, _viewer, 'Copy current body page');
    expect(harness.copied.last, last['text']);
    await _tap(tester, _in(_viewer, find.text('Previous page')));
    expect(_in(_viewer, find.byKey(const ValueKey('captured-body-hex-line-0'))), findsOneWidget);
    expect(json(harness.calls.lastWhere((call) => call.method == 'capture.body').arguments), {
      'id': 'body-fixture', 'direction': 'response', 'offset': 0, 'source': 'raw',
    });
    Navigator.pop(tester.element(_viewer));
    await tester.pumpAndSettle();
    expect(_text(tester, _inline), last['text']);
    expect(tester.takeException(), isNull);
  });

  testWidgets('a decoded binary page uses absolute addresses and lazily builds hexadecimal rows', (tester) async {
    const offset = 131072;
    final data = Uint8List.fromList(List.generate(16384, (index) => index % 256));
    await _mount(tester, message: _message(saved: 128), page: _page(base64Encode(data), encoding: 'base64', offset: offset));
    final first = tester.widget<SelectableText>(_in(_inline, find.byKey(const ValueKey('captured-body-hex-line-131072'))));
    expect(first.data, startsWith('020000  00'));
    expect(_hexRows(tester, _inline).length, lessThan(128));
    expect(_in(_inline, find.byKey(const ValueKey('captured-body-text'))), findsNothing);
    final list = tester.widget<ListView>(_hexList(_inline));
    expect(list.controller!.position.maxScrollExtent, greaterThan(10000));
    list.controller!.jumpTo(list.controller!.position.maxScrollExtent);
    await tester.pumpAndSettle();
    final tail = _hexRows(tester, _inline).last;
    expect(tail.data, contains('ff'));
    expect(int.parse((tail.key! as ValueKey<String>).value.split('-').last), greaterThan(offset + 16000));
    expect(tester.takeException(), isNull);
  });

  testWidgets('protobuf and gRPC derived views preserve their original page on copy', (tester) async {
    // Field 1 = uint64 max; field 2 = a printable string.
    final proto = Uint8List.fromList([0x08, ...List.filled(9, 0xff), 0x01, 0x12, 0x02, 0x6f, 0x6b]);
    final original = base64Encode(proto);
    final harness = await _mount(tester, message: _message(type: 'application/x-protobuf', saved: proto.length),
      page: _page(original, encoding: 'base64'));
    expect(_in(_inline, find.text('Protobuf')), findsOneWidget);
    expect(_text(tester, _inline), '1: 18446744073709551615\n2: "ok"');
    await _action(tester, _inline, 'Copy current body page');
    expect(harness.copied.last, original);
    await _openViewer(tester);
    await _action(tester, _viewer, 'Copy displayed body');
    expect(harness.copied.last, '1: 18446744073709551615\n2: "ok"');
    await _tap(tester, _in(_viewer, find.text('Base64')));
    expect(_text(tester, _viewer), original);
    expect(tester.takeException(), isNull);
  });

  testWidgets('gRPC frames display message fields without treating the frame prefix as protobuf', (tester) async {
    final frame = Uint8List.fromList([0, 0, 0, 0, 2, 8, 42]);
    final original = base64Encode(frame);
    final harness = await _mount(tester, message: _message(type: 'application/grpc+proto', saved: frame.length),
      page: _page(original, encoding: 'base64'));
    expect(_in(_inline, find.text('gRPC')), findsOneWidget);
    expect(_text(tester, _inline), '# 1\n1: 42');
    await _action(tester, _inline, 'Copy current body page');
    expect(harness.copied.last, original);
    expect(tester.takeException(), isNull);
  });

  testWidgets('complete captured images open a zoomable viewer with bounded decode dimensions', (tester) async {
    final harness = await _mount(tester, message: _message(type: 'image/png', saved: base64Decode(_png).length),
      page: _page(_png, encoding: 'base64'));
    final inlineImage = _in(_inline, find.byKey(const ValueKey('captured-body-image')));
    await _decodeImage(tester, inlineImage);
    await _capture(tester, harness, '360-light-image-inline');
    await _tap(tester, inlineImage);
    expect(_viewer, findsOneWidget);
    final viewerImage = _in(_viewer, find.byKey(const ValueKey('captured-body-image')));
    await _decodeImage(tester, viewerImage);
    await _capture(tester, harness, '360-light-image-viewer');
    final image = tester.widget<Image>(viewerImage);
    expect(image.image, isA<ResizeImage>());
    final provider = image.image as ResizeImage;
    expect(provider.policy, ResizeImagePolicy.fit);
    expect(provider.width, inInclusiveRange(1, 2048));
    expect(provider.height, inInclusiveRange(1, 2048));
    final interactive = _in(_viewer, find.byType(InteractiveViewer));
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
    await _action(tester, _viewer, 'Reset zoom');
    expect(transform.value.getMaxScaleOnAxis(), closeTo(1, 0.00001));
    expect(transform.value.entry(0, 3), closeTo(0, 0.00001));
    expect(transform.value.entry(1, 3), closeTo(0, 0.00001));
    await _tap(tester, _in(_viewer, find.text('Hex')));
    expect(_in(_viewer, find.byKey(const ValueKey('captured-body-image'))), findsNothing);
    expect(_in(_viewer, find.byKey(const ValueKey('captured-body-hex-line-0'))), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('an undecodable captured image offers a hexadecimal fallback without losing its bytes', (tester) async {
    const original = 'AAECAw==';
    final harness = await _mount(tester, message: _message(type: 'image/png', saved: 4),
      page: _page(original, encoding: 'base64'));
    await _decodeImage(tester, _in(_inline, find.byKey(const ValueKey('captured-body-image'))));
    expect(_in(_inline, find.text('Image preview unavailable')), findsOneWidget);
    await _tap(tester, _in(_inline, find.text('Show hexadecimal')));
    expect(_in(_inline, find.byKey(const ValueKey('captured-body-image'))), findsNothing);
    final hex = tester.widget<SelectableText>(_in(_inline, find.byKey(const ValueKey('captured-body-hex-line-0'))));
    expect(hex.data, contains('00 01'));
    await _action(tester, _inline, 'Copy current body page');
    expect(harness.copied.last, original);
    expect(tester.takeException(), isNull);
  });

  for (final limitation in ['multiple pages', 'incomplete stream', 'truncated capture', 'saved raw bytes']) {
    testWidgets('images with $limitation stay in byte views and never fetch remaining pages automatically', (tester) async {
      final harness = await _mount(tester, message: _message(type: 'image/png', saved: base64Decode(_png).length,
        complete: limitation != 'incomplete stream', truncated: limitation == 'truncated capture'),
        page: _page(_png, encoding: 'base64', more: limitation == 'multiple pages',
          source: limitation == 'saved raw bytes' ? 'raw' : 'decoded'));
      expect(_in(_inline, find.byKey(const ValueKey('captured-body-image'))), findsNothing);
      expect(_in(_inline, find.text('Image')), findsNothing);
      expect(_in(_inline, find.byKey(const ValueKey('captured-body-hex-line-0'))), findsOneWidget);
      await _tap(tester, _in(_inline, find.text('Base64')));
      expect(_text(tester, _inline), _png);
      expect(harness.calls.where((call) => call.method == 'capture.body'), isEmpty);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('long body lines scroll horizontally and return to wrapping while copying the full page', (tester) async {
    final original = 'begin-${'x' * 6000}-end';
    final harness = await _mount(tester, message: _message(type: 'text/plain; charset=utf-8', saved: original.length),
      page: _page(original));
    await _openViewer(tester);
    IconButtonData wrapAction() => tester.widget<Scaffold>(_viewer).iconActions.singleWhere((action) => action.tooltip == 'Wrap lines');
    expect(wrapAction().isSelected, isTrue);
    await _action(tester, _viewer, 'Wrap lines');
    expect(wrapAction().isSelected, isFalse);
    final horizontal = _in(_viewer, find.byWidgetPredicate((widget) =>
      widget is SingleChildScrollView && widget.scrollDirection == Axis.horizontal));
    expect(horizontal, findsOneWidget);
    final scroll = tester.widget<SingleChildScrollView>(horizontal).controller!;
    expect(scroll.position.maxScrollExtent, greaterThan(1000));
    scroll.jumpTo(scroll.position.maxScrollExtent);
    await tester.pumpAndSettle();
    expect(scroll.offset, greaterThan(1000));
    await _action(tester, _viewer, 'Copy displayed body');
    expect(harness.copied.last, original);
    await _action(tester, _viewer, 'Wrap lines');
    expect(wrapAction().isSelected, isTrue);
    expect(horizontal, findsNothing);
    expect(_text(tester, _viewer), original);
    expect(tester.takeException(), isNull);
  });

  testWidgets('a remote to local session change rejects late viewer pages and stale copy actions', (tester) async {
    final pending = Completer<Json>();
    final first = _page('a' * 16384, more: true);
    final harness = await _mount(tester, message: _message(type: 'text/plain', saved: 17000), page: first, handler: (call) async {
      if (call.method == 'capture.body') return pending.future;
      return null;
    });
    await _openViewer(tester);
    final oldScaffold = tester.widget<Scaffold>(_viewer);
    final oldCopy = oldScaffold.iconActions.singleWhere((action) => action.tooltip == 'Copy current body page');
    final oldDisplayedCopy = oldScaffold.menuItems.singleWhere((item) => item.label == 'Copy displayed body');
    await _tap(tester, _in(_viewer, find.text('Next page')));
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
    expect(find.text('Session changed; reopen this activity from the current list'), findsOneWidget);
    expect(harness.copied, isEmpty);
    expect(harness.calls.where((call) => call.method == 'capture.body').length, 1);
    expect(tester.takeException(), isNull);
  });

  for (final layout in [(320.0, 1.8, false), (360.0, 1.4, true)]) {
    testWidgets('body viewing fits ${layout.$1.toInt()} wide with ${layout.$2} system text scaling', (tester) async {
      final data = Uint8List.fromList(List.generate(256, (index) => index));
      final harness = await _mount(tester, size: Size(layout.$1, 568), scale: layout.$2, dark: layout.$3,
        message: _message(type: 'application/octet-stream', saved: data.length),
        page: _page(base64Encode(data), encoding: 'base64', source: 'raw'));
      await _openViewer(tester);
      expect(MediaQuery.textScalerOf(tester.element(_viewer)).scale(1), closeTo(1.4, 0.0001));
      final offsets = _hexRows(tester, _viewer).map((row) => int.parse((row.key! as ValueKey<String>).value.split('-').last)).toList()..sort();
      expect(offsets.length, greaterThan(1));
      expect(offsets[1] - offsets[0], lessThanOrEqualTo(4));
      final label = '${layout.$1.toInt()}-${layout.$2}-${layout.$3 ? 'dark' : 'light'}';
      await _capture(tester, harness, '$label-hex');
      final list = tester.widget<ListView>(_hexList(_viewer));
      list.controller!.jumpTo(list.controller!.position.maxScrollExtent);
      await tester.pumpAndSettle();
      expect(_hexRows(tester, _viewer).last.data, contains('ff'));
      await _capture(tester, harness, '$label-hex-end');
      await _tap(tester, _in(_viewer, find.text('Base64')));
      expect(_text(tester, _viewer), base64Encode(data));
      await _action(tester, _viewer, 'Copy current body page');
      expect(harness.copied.last, base64Encode(data));
      await _dismissFeedback(tester);
      await _capture(tester, harness, '$label-base64');
      expect(tester.takeException(), isNull);
    });
  }
}
