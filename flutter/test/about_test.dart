import 'dart:async';
import 'dart:io';
import 'dart:ui' as ui;
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/pages/about.dart';
import 'package:b_box_ui/components/app_update_dialog.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:flutter/foundation.dart' show TargetPlatform, debugDefaultTargetPlatformOverride;
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'support/errors.dart';

const _release = <String, dynamic>{
  'available': true, 'complete': true, 'hasMetadata': true, 'canDownload': true,
  'version': '1.0.2', 'versionCode': 10102, 'notes': 'Release notes',
  'url': 'https://github.com/giturass/B-box/releases/tag/v1.0.2',
  'downloadUrl': 'https://github.com/giturass/B-box/releases/download/v1.0.2/B-box-1.0.2-arm64-v8a.apk',
  'assetName': 'B-box-1.0.2-arm64-v8a.apk', 'size': 4096,
};

Json _snapshot(String status, {num downloaded = 0, num total = 4096, bool? canInstall, String? error}) => {
  'status': status, 'version': '1.0.2', 'versionCode': 10102,
  'downloaded': downloaded, 'total': total,
  'canInstall': canInstall ?? ['ready', 'permissionRequired', 'installing'].contains(status),
  if (error != null) 'error': error,
};

class _AboutFixture {
  _AboutFixture(this.box, this.events, this.nativeState);
  final BoxController box;
  final StreamController<dynamic> events;
  final calls = <MethodCall>[];
  Json nativeState;
  void emit(Json state) {
    nativeState = state;
    events.add({'type': 'updates', 'value': state});
  }
}

Future<_AboutFixture> _mount(WidgetTester tester, {Json initialUpdate = const {'status': 'idle'},
  Size size = const Size(360, 800), double scale = 1, Locale locale = const Locale('en'),
  GlobalKey? boundaryKey,
  Future<dynamic> Function(MethodCall)? handler}) async {
  reportFlutterErrors();
  tester.binding.platformDispatcher.localesTestValue = [locale];
  addTearDown(tester.binding.platformDispatcher.clearLocalesTestValue);
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  tester.platformDispatcher.textScaleFactorTestValue = scale;
  addTearDown(tester.view.reset);
  addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
  if (boundaryKey != null) {
    // Linux's registered CJK fallback makes CI images readable. B-box still
    // selects its Android phone layout from this test's narrow viewport.
    debugDefaultTargetPlatformOverride = TargetPlatform.linux;
    addTearDown(() => debugDefaultTargetPlatformOverride = null);
  }
  const channel = MethodChannel('test.about');
  final events = StreamController<dynamic>.broadcast();
  final fixture = _AboutFixture(BoxController(channel: channel, events: events.stream), events, initialUpdate);
  final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  messenger.setMockMethodCallHandler(channel, (call) async {
    fixture.calls.add(call);
    if (call.method == 'ready') return {'version': '1.0.1', 'coreVersion': '1.13.0',
      'dashboard': {'status': 'Stopped'}, 'updates': fixture.nativeState};
    final override = await handler?.call(call);
    if (override != null) return override;
    switch (call.method) {
      case 'updates.check': return _release;
      case 'updates.download': return fixture.nativeState = _snapshot('downloading');
      case 'updates.cancel': return fixture.nativeState = _snapshot('cancelled', total: 0);
      case 'updates.install': return fixture.nativeState = _snapshot('installing', downloaded: 4096);
      case 'updates.snapshot': return fixture.nativeState;
      default: return null;
    }
  });
  final app = BoxApp(controller: fixture.box);
  await tester.pumpWidget(boundaryKey == null ? app : RepaintBoundary(key: boundaryKey, child: app));
  await tester.pumpAndSettle();
  addTearDown(() async {
    await tester.pumpWidget(const SizedBox());
    await events.close();
    messenger.setMockMethodCallHandler(channel, null);
  });
  openPage(tester.element(find.byType(HomePage)), const AboutPage());
  await tester.pumpAndSettle();
  return fixture;
}

Future<void> _openUpdate(WidgetTester tester) async {
  final button = find.descendant(of: find.byType(AboutPage), matching: find.byType(TonalButton));
  await Scrollable.ensureVisible(tester.element(button), alignment: .5);
  await tester.pumpAndSettle();
  expect(button.hitTestable(), findsOneWidget);
  await tester.tap(button);
  await tester.pumpAndSettle();
  expect(find.byType(AppUpdateDialog), findsOneWidget);
}

Future<void> _action(WidgetTester tester, String label, {bool settle = true}) async {
  await tester.tap(find.widgetWithText(TextButton, label));
  if (settle) { await tester.pumpAndSettle(); }
  else { await tester.pump(); }
}

Future<void> _capture(WidgetTester tester, GlobalKey key, String label) async {
  await tester.runAsync(() async {
    final boundary = key.currentContext!.findRenderObject()! as RenderRepaintBoundary;
    final bitmap = await boundary.toImage();
    try {
      final data = await bitmap.toByteData(format: ui.ImageByteFormat.png);
      final file = File('build/screenshots/about-updates/$label.png');
      await file.parent.create(recursive: true);
      await file.writeAsBytes(data!.buffer.asUint8List());
    } finally { bitmap.dispose(); }
  });
}

Future<void> _narrowStates(WidgetTester tester, Locale locale, {bool screenshots = false}) async {
  final boundaryKey = screenshots ? GlobalKey() : null;
  final fixture = await _mount(tester, size: const Size(320, 568), scale: 1.4, locale: locale, boundaryKey: boundaryKey);
  await _openUpdate(tester);
  final dialogContext = tester.element(find.byType(AppUpdateDialog));
  expect(Theme.of(dialogContext).platform, screenshots ? TargetPlatform.linux : TargetPlatform.android);
  await _action(tester, dialogContext.tr('Download'));
  for (final (status, labels) in [
    ('downloading', ['Close', 'Cancel download']),
    ('permissionRequired', ['Close', 'Discard update', 'Retry install']),
    ('error', ['Close', 'Discard update', 'Retry download']),
  ]) {
    fixture.emit(_snapshot(status, downloaded: 2048,
      error: status == 'error' ? 'APK signing identity does not match this installation' : null));
    await tester.pumpAndSettle();
    for (final label in labels) {
      final action = find.widgetWithText(TextButton, dialogContext.tr(label));
      expect(action.hitTestable(), findsOneWidget);
      final rect = tester.getRect(action);
      expect(rect.left, greaterThanOrEqualTo(0));
      expect(rect.right, lessThanOrEqualTo(320));
      expect(rect.bottom, lessThanOrEqualTo(568));
    }
    if (boundaryKey != null) {
      await _capture(tester, boundaryKey, '${locale.languageCode}-${status == 'error' ? 'signature-error' : status}');
    }
  }
  expect(find.text(locale.languageCode == 'zh' ? '安装包签名与当前应用不一致'
      : 'APK signing identity does not match this installation'), findsOneWidget);
  expect(tester.takeException(), isNull);
}

void main() {
  setUpAll(() async {
    final cjk = await File('test/support/fonts/NotoSansSC-updates.ttf').readAsBytes();
    await (FontLoader('Noto Sans SC')..addFont(Future.value(ByteData.sublistView(cjk)))).load();
    final root = Platform.environment['FLUTTER_ROOT'];
    if (root == null) return;
    final font = File('$root/bin/cache/artifacts/material_fonts/Roboto-Regular.ttf');
    if (font.existsSync()) {
      await (FontLoader('Roboto')..addFont(Future.value(ByteData.sublistView(await font.readAsBytes())))).load();
    }
  });

  testWidgets('About distinguishes application version from the current sing-box core', (tester) async {
    await _mount(tester);
    expect(find.text('v1.0.1'), findsOneWidget);
    final core = find.text('sing-box 1.13.0');
    await tester.ensureVisible(core);
    expect(core, findsOneWidget);
    expect(find.text('v1.13.0'), findsNothing);
    expect(tester.takeException(), isNull);
  });

  for (final available in [true, false]) {
    for (final complete in [true, false]) {
      testWidgets('release checks retain available=$available complete=$complete semantics', (tester) async {
        final reply = Completer<Json>();
        final fixture = await _mount(tester, handler: (call) async => call.method == 'updates.check' ? reply.future : null);
        final check = find.widgetWithText(TonalButton, 'Check for updates');
        await tester.ensureVisible(check);
        await tester.tap(check);
        await tester.pump();
        expect(fixture.calls.where((call) => call.method == 'updates.check'), hasLength(1));
        expect(tester.widget<TonalButton>(check).onPressed, isNull);
        reply.complete({..._release, 'available': available, 'complete': complete});
        await tester.pumpAndSettle();
        expect(find.text(available ? 'Update available' : complete ? 'You are up to date' : 'Update check incomplete'), findsOneWidget);
        expect(tester.widget<TonalButton>(check).onPressed, isNotNull);
        if (!complete) {
          expect(find.text('Some releases could not be checked. Please try again.'), findsOneWidget);
          expect(find.text('You are up to date'), findsNothing);
        }
        if (available) {
          expect(find.text('Release notes'), findsOneWidget);
          await _action(tester, 'Download');
          expect(fixture.calls.lastWhere((call) => call.method == 'updates.download').arguments, {'versionCode': 10102});
          expect(fixture.calls.where((call) => call.method == 'url.open'), isEmpty);
        } else {
          expect(find.widgetWithText(TextButton, 'Download'), findsNothing);
          await _action(tester, complete ? 'Close' : 'Releases');
          if (!complete) {
            expect(fixture.calls.lastWhere((call) => call.method == 'url.open').arguments,
              {'url': 'https://github.com/giturass/B-box/releases'});
          }
        }
        expect(tester.takeException(), isNull);
      });
    }
  }

  testWidgets('missing metadata never reports that the app is current', (tester) async {
    final fixture = await _mount(tester, handler: (call) async => call.method == 'updates.check'
      ? {'available': false, 'complete': true, 'hasMetadata': false, 'canDownload': false} : null);
    await _openUpdate(tester);
    expect(find.text('No release metadata available'), findsOneWidget);
    expect(find.text('You are up to date'), findsNothing);
    expect(find.widgetWithText(TextButton, 'Download'), findsNothing);
    await _action(tester, 'Releases');
    expect(fixture.calls.lastWhere((call) => call.method == 'url.open').arguments,
      {'url': 'https://github.com/giturass/B-box/releases'});
  });

  testWidgets('releases without a compatible verified artifact offer the release page', (tester) async {
    final fixture = await _mount(tester, handler: (call) async => call.method == 'updates.check'
      ? {..._release, 'canDownload': false} : null);
    await _openUpdate(tester);
    expect(find.text('In-app update is unavailable for this release.'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Download'), findsNothing);
    await _action(tester, 'Releases');
    expect(fixture.calls.lastWhere((call) => call.method == 'url.open').arguments, {'url': _release['url']});
    expect(fixture.calls.where((call) => call.method == 'updates.download'), isEmpty);
  });

  testWidgets('download progress survives dialog closure and supports cancel and retry', (tester) async {
    final fixture = await _mount(tester);
    await _openUpdate(tester);
    await _action(tester, 'Download');
    fixture.emit(_snapshot('downloading', downloaded: 2048));
    await tester.pumpAndSettle();
    expect(find.text('2.0 KB / 4.0 KB'), findsOneWidget);
    expect(tester.widget<LinearProgressIndicator>(find.byType(LinearProgressIndicator)).value, .5);
    await _action(tester, 'Close');
    expect(find.widgetWithText(TonalButton, 'View update'), findsOneWidget);
    fixture.emit(_snapshot('downloading', downloaded: 3072));
    await tester.pumpAndSettle();
    await _openUpdate(tester);
    expect(find.text('3.0 KB / 4.0 KB'), findsOneWidget);
    expect(fixture.calls.where((call) => call.method == 'updates.check'), hasLength(1));
    await _action(tester, 'Cancel download');
    expect(fixture.calls.lastWhere((call) => call.method == 'updates.cancel').arguments, isNull);
    expect(find.text('Download cancelled'), findsNWidgets(2));
    await _action(tester, 'Retry download');
    expect(fixture.calls.where((call) => call.method == 'updates.download'), hasLength(2));
    fixture.emit(_snapshot('verifying', downloaded: 4096));
    // The asynchronous broadcast schedules the model's frame after delivery.
    // Keep the indeterminate indicator active without waiting for it to settle.
    await tester.pump();
    await tester.pump();
    expect(find.text('Verifying downloaded APK'), findsOneWidget);
    expect(tester.widget<LinearProgressIndicator>(find.byType(LinearProgressIndicator)).value, isNull);
    expect(find.widgetWithText(TextButton, 'Install'), findsNothing);
    fixture.emit(_snapshot('ready', downloaded: 4096));
    await tester.pumpAndSettle();
    expect(find.widgetWithText(TextButton, 'Install'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('a cancelled download does not replace a newly checked release', (tester) async {
    final fixture = await _mount(tester, initialUpdate: _snapshot('cancelled'), handler: (call) async =>
      call.method == 'updates.check' ? {..._release, 'version': '1.0.3', 'versionCode': 10103} : null);
    await _openUpdate(tester);
    expect(find.text('B-box v1.0.3'), findsOneWidget);
    expect(find.text('B-box v1.0.2'), findsNothing);
    expect(find.text('Download cancelled'), findsNothing);
    await _action(tester, 'Download');
    expect(fixture.calls.lastWhere((call) => call.method == 'updates.download').arguments, {'versionCode': 10103});
  });

  testWidgets('the download action waits for native acknowledgement', (tester) async {
    final reply = Completer<Json>();
    final fixture = await _mount(tester, handler: (call) async => call.method == 'updates.download' ? reply.future : null);
    await _openUpdate(tester);
    await _action(tester, 'Download', settle: false);
    expect(tester.widget<TextButton>(find.widgetWithText(TextButton, 'Download')).onPressed, isNull);
    expect(fixture.calls.where((call) => call.method == 'updates.download'), hasLength(1));
    reply.complete(_snapshot('downloading'));
    await tester.pumpAndSettle();
    expect(tester.widget<TextButton>(find.widgetWithText(TextButton, 'Cancel download')).onPressed, isNotNull);
    expect(tester.takeException(), isNull);
  });

  testWidgets('permission return refreshes state and requires an explicit installation retry', (tester) async {
    var installs = 0;
    final fixture = await _mount(tester, initialUpdate: _snapshot('ready', downloaded: 4096), handler: (call) async {
      if (call.method == 'updates.install') {
        installs++;
        return _snapshot(installs == 1 ? 'permissionRequired' : 'installing', downloaded: 4096);
      }
      if (call.method == 'updates.snapshot') return _snapshot('ready', downloaded: 4096);
      return null;
    });
    await _openUpdate(tester);
    expect(fixture.calls.where((call) => call.method == 'updates.check'), isEmpty);
    await _action(tester, 'Install');
    expect(find.text('Allow B-box to install apps in Android settings, then return here to install the update.'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Retry install'), findsOneWidget);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pumpAndSettle();
    expect(fixture.calls.where((call) => call.method == 'updates.snapshot'), hasLength(1));
    expect(installs, 1);
    await _action(tester, 'Install');
    expect(installs, 2);
    expect(fixture.calls.lastWhere((call) => call.method == 'updates.install').arguments, {'versionCode': 10102});
    expect(find.text('Complete the update in the Android installer.'), findsOneWidget);
    expect(fixture.box.version, '1.0.1');
    expect(find.text('You are up to date'), findsNothing);
    fixture.emit(_snapshot('ready', downloaded: 4096));
    await tester.pumpAndSettle();
    expect(find.widgetWithText(TextButton, 'Install'), findsOneWidget);
    expect(installs, 2);
    expect(tester.takeException(), isNull);
  });

  testWidgets('download errors retry downloading and installer errors reuse the verified file', (tester) async {
    final fixture = await _mount(tester);
    await _openUpdate(tester);
    await _action(tester, 'Download');
    fixture.emit(_snapshot('error', error: 'Checksum mismatch'));
    await tester.pumpAndSettle();
    expect(find.text('Checksum mismatch'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Install'), findsNothing);
    await _action(tester, 'Retry download');
    fixture.emit(_snapshot('error', downloaded: 4096, canInstall: true, error: 'Android installer unavailable'));
    await tester.pumpAndSettle();
    expect(find.text('Android installer unavailable'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Retry download'), findsNothing);
    await _action(tester, 'Retry install');
    expect(fixture.calls.where((call) => call.method == 'updates.download'), hasLength(2));
    expect(fixture.calls.where((call) => call.method == 'updates.install'), hasLength(1));
    expect(tester.takeException(), isNull);
  });

  testWidgets('failed bridge requests preserve the candidate and allow a download retry', (tester) async {
    var attempts = 0;
    await _mount(tester, handler: (call) async {
      if (call.method == 'updates.download' && ++attempts == 1) {
        throw PlatformException(code: 'download', message: 'Network unavailable');
      }
      return null;
    });
    await _openUpdate(tester);
    await _action(tester, 'Download');
    expect(find.text('Network unavailable'), findsOneWidget);
    await _action(tester, 'Retry download');
    expect(attempts, 2);
    expect(find.text('Network unavailable'), findsNothing);
    expect(tester.takeException(), isNull);
  });

  for (final locale in [const Locale('en'), const Locale('zh', 'CN')]) {
    testWidgets('update dialog keeps progress and controls reachable on a narrow phone in $locale', (tester) async {
      debugDefaultTargetPlatformOverride = TargetPlatform.android;
      addTearDown(() => debugDefaultTargetPlatformOverride = null);
      await _narrowStates(tester, locale);
    });
    testWidgets('update dialog screenshots use readable CI fonts in $locale', (tester) async {
      await _narrowStates(tester, locale, screenshots: true);
    });
  }
}
