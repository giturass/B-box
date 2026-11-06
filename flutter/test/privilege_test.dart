import 'dart:async';
import 'dart:io';
import 'dart:typed_data';
import 'dart:ui' as ui;
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/pages/privilege.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  late BoxController box;
  late StreamController<dynamic> events;
  late List<MethodCall> calls;
  late Json state;
  late GlobalKey boundary;
  Future<dynamic> Function(MethodCall)? extra;

  setUpAll(() async {
    final root = Platform.environment['FLUTTER_ROOT'];
    if (root != null) {
      final font = File('$root/bin/cache/artifacts/material_fonts/Roboto-Regular.ttf');
      if (font.existsSync()) await (FontLoader('Roboto')..addFont(Future.value(ByteData.sublistView(await font.readAsBytes())))).load();
    }
  });
  setUp(() {
    extra = null;
    calls = [];
    state = {'active': true, 'compatible': true, 'synced': true, 'version': 4, 'expectedVersion': 4, 'revision': 'initial',
      'settings': {'enabled': false, 'packages': ['uninstalled.app'], 'renameInterface': false, 'interfacePrefix': 'wlan'}};
  });

  Future<void> mount(WidgetTester tester, {bool remote = false, String? intent, bool dark = false,
    Size size = const Size(360, 900), double scale = 1}) async {
    tester.binding.platformDispatcher.localesTestValue = const [Locale('en')];
    addTearDown(tester.binding.platformDispatcher.clearLocalesTestValue);
    tester.platformDispatcher.textScaleFactorTestValue = scale;
    addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
    tester.view.physicalSize = size;
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    const channel = MethodChannel('test.privilege');
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    events = StreamController<dynamic>.broadcast();
    messenger.setMockMethodCallHandler(channel, (call) async {
      calls.add(call);
      if (call.method == 'ready') return {'version': 'test', 'coreVersion': 'test',
        'dashboard': {'status': 'Stopped', if (remote) 'remote': 'Server'},
        'preferences': {'theme': dark ? 'dark' : 'light'}, if (intent != null) 'intent': intent};
      if (extra != null) {
        final result = await extra!(call);
        if (result != null) return result;
      }
      if (call.method == 'privilege.read') return state;
      if (call.method == 'privilege.save') {
        state = {...state, 'settings': json(json(call.arguments)['value']), 'revision': 'saved'};
        return state;
      }
      if (call.method == 'privilege.apps') return [
        {'package': 'io.ericlee.bfa', 'name': 'B-box'},
        {'package': 'visible.app', 'name': 'Visible app'},
        {'package': 'system.app', 'name': 'System app', 'system': true},
      ];
      if (call.method == 'privilege.logs') return [{'level': 2, 'timestamp': 1234, 'source': 'HookInstaller', 'message': 'ROM hook unavailable', 'stackTrace': 'test trace'}];
      if (call.method == 'privilege.test') return {'frameworkDetected': ['NetworkCapabilities'], 'nativeDetected': true,
        'frameworkInterfaces': ['tun0'], 'nativeInterfaces': ['tun0'], 'package': 'io.ericlee.bfa'};
      return null;
    });
    box = BoxController(channel: channel, events: events.stream);
    boundary = GlobalKey();
    addTearDown(() async {
      await tester.pumpWidget(const SizedBox());
      await events.close();
      messenger.setMockMethodCallHandler(channel, null);
    });
    await tester.pumpWidget(RepaintBoundary(key: boundary, child: BoxApp(controller: box)));
    await tester.pumpAndSettle();
    if (intent == null) {
      openPage(tester.element(find.byType(HomePage)), const PrivilegePage());
      await tester.pumpAndSettle();
    }
  }

  Scaffold page(WidgetTester tester) => tester.widget<Scaffold>(find.descendant(of: find.byType(PrivilegePage), matching: find.byType(Scaffold)).first);
  List<MethodCall> saves() => calls.where((call) => call.method == 'privilege.save').toList();
  Future<void> tap(WidgetTester tester, String label) async {
    final finder = find.text(label).last;
    if (find.text(label).evaluate().isEmpty) {
      await tester.scrollUntilVisible(find.text(label), 250,
        scrollable: find.descendant(of: find.byType(PrivilegePage), matching: find.byType(Scrollable)).first);
    }
    await tester.ensureVisible(finder);
    await tester.tap(finder);
    await tester.pumpAndSettle();
  }

  testWidgets('inactive and outdated modules keep controls disabled without claiming synchronization', (tester) async {
    state = {...state, 'active': false, 'compatible': false, 'synced': false};
    await mount(tester);
    expect(find.text('Module not active'), findsOneWidget);
    expect(tester.widgetList<Switch>(find.descendant(of: find.byType(PrivilegePage), matching: find.byType(Switch))).every((it) => it.onChanged == null), true);
    state = {...state, 'active': true, 'version': 3};
    page(tester).iconActions.single.onPressed!();
    await tester.pumpAndSettle();
    expect(find.text('Reboot required'), findsOneWidget);
    expect(saves(), isEmpty);
  });

  testWidgets('module settings intent enters Flutter without decoding a profile', (tester) async {
    await mount(tester, intent: 'bbox-settings:privilege');
    expect(find.byType(PrivilegePage), findsOneWidget);
    expect(calls.where((call) => call.method == 'profiles.decodeIntent'), isEmpty);
  });

  testWidgets('save waits for native confirmation and failure preserves the editable draft', (tester) async {
    final response = Completer<Json>();
    extra = (call) async => call.method == 'privilege.save' ? response.future : null;
    await mount(tester);
    await tap(tester, 'Hide VPN');
    expect(saves(), isEmpty);
    page(tester).confirmationAction!.onPressed!();
    await tester.pump();
    await tester.pump();
    expect(saves(), hasLength(1));
    expect(json(saves().single.arguments)['revision'], 'initial');
    expect(page(tester).confirmationAction!.onPressed, isNull);
    expect(tester.widgetList<Switch>(find.descendant(of: find.byType(PrivilegePage), matching: find.byType(Switch))).every((it) => it.onChanged == null), true);
    response.completeError(PlatformException(code: 'restart', message: 'VPN recovery failed'));
    await tester.pumpAndSettle();
    expect(page(tester).confirmationAction!.onPressed, isNotNull);
    expect(json(json(saves().single.arguments)['value'])['enabled'], true);
    extra = null;
    page(tester).confirmationAction!.onPressed!();
    await tester.pumpAndSettle();
    expect(saves(), hasLength(2));
    expect(page(tester).confirmationAction, isNull);
    expect(json(state['settings'])['enabled'], true);
  });

  testWidgets('application picker preserves filtered and uninstalled selections and includes B-box', (tester) async {
    await mount(tester);
    await tap(tester, 'Apps to hide VPN from');
    expect(find.text('B-box'), findsWidgets);
    expect(find.text('System app'), findsNothing);
    await tap(tester, 'Visible app');
    final picker = tester.widget<Scaffold>(find.descendant(of: find.byType(PrivilegeAppsPage), matching: find.byType(Scaffold)));
    picker.confirmationAction!.onPressed!();
    await tester.pumpAndSettle();
    page(tester).confirmationAction!.onPressed!();
    await tester.pumpAndSettle();
    expect(json(state['settings'])['packages'], containsAll(['visible.app', 'uninstalled.app']));
    expect(calls.where((call) => call.method == 'apps.rescan'), isEmpty);
  });

  testWidgets('remote sessions neither query nor apply local module settings', (tester) async {
    await mount(tester, remote: true);
    expect(find.text('Local device only'), findsOneWidget);
    expect(calls.where((call) => call.method.startsWith('privilege.')), isEmpty);
  });

  testWidgets('a late local snapshot cannot populate the page after entering remote control', (tester) async {
    final response = Completer<Json>();
    extra = (call) async => call.method == 'privilege.read' ? response.future : null;
    // Mount manually because the initial progress indicator intentionally remains active.
    await mount(tester, remote: true);
    events.add({'type': 'dashboard', 'value': {'status': 'Stopped'}});
    await tester.pump();
    await tester.pump();
    events.add({'type': 'dashboard', 'value': {'status': 'Started', 'remote': 'Server'}});
    await tester.pump();
    response.complete(state);
    await tester.pumpAndSettle();
    expect(find.text('Local device only'), findsOneWidget);
    expect(find.text('Module active'), findsNothing);
  });

  testWidgets('cancelled diagnostic export does not report success', (tester) async {
    extra = (call) async => call.method == 'privilege.export' ? false : null;
    await mount(tester);
    await tap(tester, 'Export diagnostics (Root)');
    await tap(tester, 'Export');
    expect(calls.where((call) => call.method == 'privilege.export'), hasLength(1));
    expect(find.text('LSPosed diagnostics exported'), findsNothing);
  });

  for (final (size, scale, dark) in [(const Size(320, 900), 1.4, true), (const Size(800, 1000), 1.0, false)]) {
    testWidgets('privilege layout ${size.width} $scale $dark', (tester) async {
      await mount(tester, size: size, scale: scale, dark: dark);
      expect(tester.takeException(), isNull);
      await tester.runAsync(() async {
        final bitmap = await (boundary.currentContext!.findRenderObject()! as RenderRepaintBoundary).toImage();
        final data = await bitmap.toByteData(format: ui.ImageByteFormat.png);
        final file = File('build/screenshots/privilege/${size.width.toInt()}-$scale-${dark ? 'dark' : 'light'}.png');
        await file.parent.create(recursive: true);
        await file.writeAsBytes(data!.buffer.asUint8List());
        bitmap.dispose();
      });
      await tap(tester, 'VPN detection test');
      expect(find.text('NetworkCapabilities'), findsOneWidget);
      expect(find.text('VPN detected'), findsOneWidget);
      expect(tester.takeException(), isNull);
    });
  }
}
