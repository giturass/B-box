import 'dart:async';
import 'dart:convert';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/pages/backup.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'support/errors.dart';

const _sample = {'format': 'bbox-1', 'profiles': [{'id': 1, 'name': 'Synthetic', 'content': '{}'}]};
const _account = {'uri': 'https://dav.example.test/directory', 'user': 'account', 'password': 'synthetic', 'fileName': 'test.json'};

Future<BoxController> _mount(WidgetTester tester, Future<dynamic> Function(MethodCall) handler, {String strategy = 'Compatible'}) async {
  reportFlutterErrors();
  tester.view.physicalSize = const Size(360, 800);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  const channel = MethodChannel('test.backup');
  final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  messenger.setMockMethodCallHandler(channel, (call) async {
    if (call.method == 'ready') return {'version': 'test', 'coreVersion': 'test', 'dashboard': {'status': 'Stopped'},
      'preferences': {'locale': 'en', 'restoreStrategy': strategy}};
    return handler(call);
  });
  addTearDown(() => messenger.setMockMethodCallHandler(channel, null));
  final box = BoxController(channel: channel, events: const Stream.empty());
  await tester.pumpWidget(BoxApp(controller: box));
  await tester.pumpAndSettle();
  unawaited(openPage(tester.element(find.byType(HomePage)), const BackupPage()));
  await tester.pumpAndSettle();
  return box;
}

Future<void> _tapRow(WidgetTester tester, String key) async {
  await tester.scrollUntilVisible(find.byKey(ValueKey(key)), 160,
    scrollable: find.descendant(of: find.byType(BackupPage), matching: find.byType(Scrollable)).first);
  await tester.pumpAndSettle();
  await tester.tap(find.byKey(ValueKey(key)));
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('canceled local pickers never report success or restore data; exports use compact JSON', (tester) async {
    final calls = <MethodCall>[];
    await _mount(tester, (call) async {
      calls.add(call);
      if (call.method == 'backup.create') return _sample;
      if (call.method == 'document.save') return false;
      return null;
    });
    await _tapRow(tester, 'local-backup');
    expect(find.text('Backup complete'), findsNothing);
    final content = json(calls.singleWhere((call) => call.method == 'document.save').arguments)['content'] as String;
    expect(content, jsonEncode(_sample));
    await _tapRow(tester, 'local-restore');
    await tester.tap(find.text('Restore profiles only'));
    await tester.pumpAndSettle();
    expect(json(calls.singleWhere((call) => call.method == 'backup.read').arguments)['scope'], 'profiles');
    expect(calls.where((call) => call.method == 'backup.restore'), isEmpty);
    expect(find.text('Restore complete'), findsNothing);
    expect(find.byType(BackupPage), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  for (final (scope, strategy) in [('profiles', 'Override'), ('all', 'Compatible')]) {
    testWidgets('restore passes $scope/$strategy and a failed restore remains retryable', (tester) async {
      final calls = <MethodCall>[];
      var reject = true;
      await _mount(tester, (call) async {
        calls.add(call);
        if (call.method == 'backup.read') return _sample;
        if (call.method == 'backup.restore') {
          if (reject) throw PlatformException(code: 'restore', message: 'Synthetic restore failed');
          return 1;
        }
        return null;
      }, strategy: strategy);
      for (var attempt = 0; attempt < 2; attempt++) {
        await _tapRow(tester, 'local-restore');
        await tester.tap(find.text(scope == 'profiles' ? 'Restore profiles only' : 'Restore all data'));
        await tester.pumpAndSettle();
        expect(find.text(strategy == 'Override'
          ? 'Profiles missing from this backup will be removed.'
          : 'Matching profiles will be replaced. Other profiles will be kept.'), findsOneWidget);
        await tester.tap(find.text('Restore').last);
        await tester.pumpAndSettle();
        final args = json(calls.lastWhere((call) => call.method == 'backup.restore').arguments);
        expect(args['scope'], scope);
        expect(args['strategy'], strategy);
        expect(args['backup'], _sample);
        if (attempt == 0) {
          expect(find.text('Restore complete'), findsNothing);
          expect(find.byType(BackupPage), findsOneWidget);
          reject = false;
        } else {
          expect(find.text('Restore complete'), findsOneWidget);
          await tester.tap(find.text('Close'));
          await tester.pumpAndSettle();
        }
      }
      expect(calls.where((call) => call.method == 'backup.restore'), hasLength(2));
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('WebDAV upload waits for the server acknowledgement and does not open a local picker', (tester) async {
    final uploaded = Completer<bool>();
    final calls = <MethodCall>[];
    await _mount(tester, (call) async {
      calls.add(call);
      if (call.method == 'webdav.get') return _account;
      if (call.method == 'webdav.check') return true;
      if (call.method == 'webdav.upload') return uploaded.future;
      return null;
    });
    await tester.tap(find.byKey(const ValueKey('remote-backup')));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 400));
    expect(find.text('Backup complete'), findsNothing);
    expect(calls.where((call) => call.method == 'webdav.upload'), hasLength(1));
    expect(calls.where((call) => call.method == 'document.save'), isEmpty);
    uploaded.complete(true);
    await tester.pumpAndSettle();
    expect(find.text('Backup complete'), findsOneWidget);
    await tester.tap(find.text('Close'));
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
  });

  testWidgets('WebDAV form validates its directory URL and preserves edits after failed save and canceled dismissal', (tester) async {
    final calls = <MethodCall>[];
    var reject = true;
    await _mount(tester, (call) async {
      calls.add(call);
      if (call.method == 'webdav.set') {
        if (reject) throw PlatformException(code: 'disk', message: 'Synthetic account save failed');
        return json(call.arguments)['value'];
      }
      if (call.method == 'webdav.check') return true;
      return null;
    });
    await tester.tap(find.text('Bind'));
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Save'));
    await tester.pumpAndSettle();
    expect(find.text('Enter a WebDAV HTTP or HTTPS directory address'), findsOneWidget);
    expect(calls.where((call) => call.method == 'webdav.set'), isEmpty);
    final fields = find.byType(TextFormField);
    await tester.enterText(fields.at(0), 'https://dav.example.test/directory');
    await tester.enterText(fields.at(1), 'account');
    await tester.enterText(fields.at(2), 'synthetic');
    await tester.tap(find.byTooltip('Save'));
    await tester.pumpAndSettle();
    expect(find.text('Synthetic account save failed'), findsOneWidget);
    final formContext = tester.element(find.byType(WebDAVForm));
    final dismissed = SheetDismissScope.of(formContext)!.value!();
    await tester.pumpAndSettle();
    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();
    await dismissed;
    expect(find.byType(WebDAVForm), findsOneWidget);
    expect(tester.widget<TextFormField>(fields.at(0)).controller!.text, 'https://dav.example.test/directory');
    reject = false;
    await tester.tap(find.byTooltip('Save'));
    await tester.pumpAndSettle();
    expect(find.byType(WebDAVForm), findsNothing);
    expect(json(calls.lastWhere((call) => call.method == 'webdav.set').arguments)['value'], {
      'uri': 'https://dav.example.test/directory', 'user': 'account', 'password': 'synthetic', 'fileName': defaultBackupFileName,
    });
    await tester.scrollUntilVisible(find.text('account'), -160,
      scrollable: find.descendant(of: find.byType(BackupPage), matching: find.byType(Scrollable)).first);
    await tester.pumpAndSettle();
    expect(find.text('account'), findsOneWidget);
    expect(calls.where((call) => call.method == 'webdav.check'), hasLength(1));
    expect(tester.takeException(), isNull);
  });
}
