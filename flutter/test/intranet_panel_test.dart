import 'dart:async';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/components/intranet_panel.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'support/errors.dart';

Json _snapshot({bool enabled = false, bool running = true, int profileId = 1, int port = 2080, List<Json>? bindings}) {
  final listeners = bindings ?? [
    {'type': 'mixed', 'tag': 'proxy', 'port': port, 'listen': enabled ? '0.0.0.0' : '127.0.0.1',
      'lan': enabled, 'endpoints': enabled ? ['192.168.1.2:$port'] : <String>[]},
  ];
  return {
    'addresses': ['192.168.1.2', '2001:db8::2'], 'profileId': profileId, 'revision': 'revision',
    'canToggle': profileId != -1, 'enabled': enabled, 'running': running,
    'bindings': listeners, 'activeBindings': running ? listeners : <Json>[], 'activeProfileId': running ? profileId : -1,
  };
}

class _LAN {
  final channel = const MethodChannel('test.intranet');
  final events = StreamController<dynamic>.broadcast();
  final calls = <MethodCall>[];
  final clipboard = <String>[];
  Json state = _snapshot();
  Json reply = _snapshot(enabled: true);
  bool reject = false, started = true;
  Completer<Json>? pending;
  late BoxController box;

  List<MethodCall> get mutations => calls.where((call) => call.method == 'dashboard.lan.set').toList();
  Finder get panel => find.byType(IntranetPanel);
  Finder get switchWidget => find.descendant(of: panel, matching: find.byType(CommonSwitch));
  Finder get copy => find.descendant(of: panel, matching: find.byTooltip('Copy'));
  CommonSwitch switchValue(WidgetTester tester) => tester.widget<CommonSwitch>(switchWidget);

  Future<void> mount(WidgetTester tester, {String locale = 'en', double scale = 1, double width = 360}) async {
    reportFlutterErrors();
    tester.binding.platformDispatcher.localesTestValue = [Locale(locale)];
    addTearDown(tester.binding.platformDispatcher.clearLocalesTestValue);
    tester.view.physicalSize = Size(width, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(SystemChannels.platform, (call) async {
      if (call.method == 'Clipboard.setData') clipboard.add(json(call.arguments)['text'] as String);
      return null;
    });
    messenger.setMockMethodCallHandler(channel, (call) async {
      calls.add(call);
      switch (call.method) {
        case 'ready': return {
          'version': 'test', 'coreVersion': 'test',
          'dashboard': {'status': started ? 'Started' : 'Stopped', 'selectedProfileId': state['profileId']},
          'profiles': [{'id': 1, 'name': 'Local profile'}], 'lan': state,
          'preferences': {'fontScale': scale, 'dashboardCards': ['Intranet IP', 'Status'],
            'hiddenCards': ['Network speed', 'Outbound mode', 'Current profile', 'Proxy groups', 'Memory']},
        };
        case 'dashboard.egress': throw PlatformException(code: 'unavailable', message: 'No IP probe in this fixture');
        case 'dashboard.lan.refresh': return state;
        case 'dashboard.lan.set':
          if (reject) throw PlatformException(code: 'disk', message: 'Save failed');
          state = pending == null ? reply : await pending!.future;
          return state;
      }
      return null;
    });
    addTearDown(() async {
      await tester.pumpWidget(const SizedBox());
      await tester.pump();
      await events.close();
      messenger.setMockMethodCallHandler(channel, null);
      messenger.setMockMethodCallHandler(SystemChannels.platform, null);
    });
    box = BoxController(channel: channel, events: events.stream);
    await tester.pumpWidget(BoxApp(controller: box));
    await tester.pumpAndSettle();
  }
}

void main() {
  testWidgets('LAN switch changes native listeners once and waits for the saved state', (tester) async {
    final lan = _LAN()..pending = Completer<Json>();
    await lan.mount(tester);
    expect(lan.switchValue(tester).value, false);
    await tester.tap(find.text('LAN proxy'));
    await tester.pump();
    expect(lan.mutations, hasLength(1));
    expect(json(lan.mutations.single.arguments), {'profileId': 1, 'revision': 'revision', 'enabled': true});
    expect(lan.switchValue(tester).onChanged, isNull);
    expect(lan.switchValue(tester).value, false);
    await tester.tap(find.text('LAN proxy'));
    await tester.pump();
    expect(lan.mutations, hasLength(1));
    lan.pending!.complete(lan.reply);
    await tester.pumpAndSettle();
    expect(lan.switchValue(tester).value, true);
    expect(find.text('Port: 2080'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('failed native save keeps sharing disabled and permits retry', (tester) async {
    final lan = _LAN()..reject = true;
    await lan.mount(tester);
    await tester.tap(find.text('LAN proxy'));
    await tester.pumpAndSettle();
    expect(lan.switchValue(tester).value, false);
    expect(lan.switchValue(tester).onChanged, isNotNull);
    expect(find.text('Save failed'), findsOneWidget);
    expect(lan.calls.where((call) => call.method == 'dashboard.lan.refresh'), hasLength(1));
    lan.reject = false;
    await tester.tap(find.text('LAN proxy'));
    await tester.pumpAndSettle();
    expect(lan.mutations, hasLength(2));
    expect(lan.switchValue(tester).value, true);
    expect(tester.takeException(), isNull);
  });

  testWidgets('copy follows the running listeners and falls back to device IP after stop or remote switch', (tester) async {
    final lan = _LAN()..state = _snapshot(enabled: true);
    // A selected profile edit must not advertise its new port before the core applies it.
    lan.state['bindings'] = rows(_snapshot(enabled: true, port: 3128)['bindings']);
    await lan.mount(tester);
    expect(find.text('Port: 2080'), findsOneWidget);
    await tester.tap(lan.copy);
    await tester.pumpAndSettle();
    expect(lan.clipboard.last, '192.168.1.2:2080');
    lan.events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'selectedProfileId': 1}});
    await tester.pumpAndSettle();
    await tester.tap(lan.copy);
    await tester.pumpAndSettle();
    expect(lan.clipboard.last, '192.168.1.2');
    lan.events.add({'type': 'dashboard', 'value': {
      'status': 'Started', 'selectedProfileId': 1, 'remote': 'Remote server', 'remoteConnected': true,
    }});
    await tester.pumpAndSettle();
    expect(lan.switchWidget, findsNothing);
    expect(await lan.box.setLanProxy(false), false);
    expect(lan.mutations, isEmpty);
    await tester.tap(lan.copy);
    await tester.pumpAndSettle();
    expect(lan.clipboard.last, '192.168.1.2');
    expect(tester.takeException(), isNull);
  });

  testWidgets('a profile without proxy listeners opens a validated port form before adding mixed', (tester) async {
    final lan = _LAN()
      ..started = false
      ..state = _snapshot(running: false, bindings: [])
      ..reply = _snapshot(enabled: true, running: false, port: 3128);
    await lan.mount(tester);
    await tester.tap(find.text('LAN proxy'));
    await tester.pumpAndSettle();
    expect(find.byType(NewLANProxy), findsOneWidget);
    expect(lan.mutations, isEmpty);
    await tester.enterText(find.byType(TextFormField), '0');
    await tester.tap(find.byTooltip('Save'));
    await tester.pumpAndSettle();
    expect(find.text('Invalid proxy port'), findsOneWidget);
    expect(lan.mutations, isEmpty);
    await tester.enterText(find.byType(TextFormField), '3128');
    await tester.tap(find.byTooltip('Save'));
    await tester.pumpAndSettle();
    expect(json(lan.mutations.single.arguments), {'profileId': 1, 'revision': 'revision', 'enabled': true, 'port': 3128});
    expect(find.byType(NewLANProxy), findsNothing);
    expect(lan.switchValue(tester).value, true);
    expect(lan.box.lanCopyText, '192.168.1.2');
    expect(tester.takeException(), isNull);
  });

  testWidgets('details retain separate proxy ports and bracketed IPv6 endpoints', (tester) async {
    final lan = _LAN()..state = _snapshot(enabled: true, bindings: [
      {'type': 'http', 'tag': 'HTTP listener', 'port': 3128, 'lan': true, 'endpoints': ['192.168.1.2:3128']},
      {'type': 'socks', 'tag': 'SOCKS listener', 'port': 1080, 'lan': true, 'endpoints': ['[2001:db8::2]:1080']},
    ]);
    await lan.mount(tester);
    expect(find.text('Port: 3128, 1080'), findsOneWidget);
    await tester.tap(find.descendant(of: lan.panel, matching: find.text('192.168.1.2')));
    await tester.pumpAndSettle();
    final details = find.byType(IntranetDetails);
    await tester.scrollUntilVisible(find.text('[2001:db8::2]:1080'), 200,
      scrollable: find.descendant(of: details, matching: find.byType(Scrollable)).first);
    await tester.pumpAndSettle();
    final endpoint = find.ancestor(of: find.text('[2001:db8::2]:1080'), matching: find.byWidgetPredicate((widget) => widget is ListItem)).first;
    await tester.tap(find.descendant(of: endpoint, matching: find.byTooltip('Copy')));
    await tester.pumpAndSettle();
    expect(lan.clipboard.last, '[2001:db8::2]:1080');
    expect(tester.takeException(), isNull);
  });

  for (final locale in ['en', 'zh']) {
    testWidgets('LAN row fits a narrow display with larger text: $locale', (tester) async {
      final lan = _LAN()..state = _snapshot(enabled: true);
      await lan.mount(tester, locale: locale, scale: 1.4, width: 320);
      expect(find.text(locale == 'zh' ? '局域网代理' : 'LAN proxy'), findsOneWidget);
      expect(lan.switchValue(tester).value, true);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('unselected and stale profile snapshots cannot mutate a local profile', (tester) async {
    final lan = _LAN()..state = _snapshot(profileId: -1, bindings: []);
    await lan.mount(tester);
    expect(lan.switchValue(tester).onChanged, isNull);
    expect(await lan.box.setLanProxy(true), false);
    lan.events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'selectedProfileId': 2}});
    lan.events.add({'type': 'lan', 'value': _snapshot(profileId: 1)});
    await tester.pumpAndSettle();
    expect(lan.switchValue(tester).onChanged, isNull);
    expect(await lan.box.setLanProxy(true), false);
    expect(lan.mutations, isEmpty);
    expect(tester.takeException(), isNull);
  });

  testWidgets('late save replies do not replace the newly selected profile state', (tester) async {
    final lan = _LAN()..pending = Completer<Json>();
    await lan.mount(tester);
    final saved = lan.box.setLanProxy(true);
    await tester.pump();
    lan.events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'selectedProfileId': 2}});
    lan.events.add({'type': 'lan', 'value': _snapshot(profileId: 2, port: 3128)});
    await tester.pump();
    lan.pending!.complete(_snapshot(enabled: true, profileId: 1));
    expect(await saved, true);
    await tester.pumpAndSettle();
    expect(lan.box.lan['profileId'], 2);
    expect(lan.box.lan['enabled'], false);
    expect(tester.takeException(), isNull);
  });
}
