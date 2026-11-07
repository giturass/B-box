import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';
import 'dart:ui' as ui;
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/common/config_form.dart';
import 'package:b_box_ui/pages/configuration.dart';
import 'package:b_box_ui/pages/configuration_form.dart';
import 'package:b_box_ui/pages/profile_details.dart';
import 'package:b_box_ui/pages/profile_editor.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'support/errors.dart';

const _profile = {'id': 7, 'name': 'Home', 'remote': false};
const _node = {'type': 'vless', 'tag': 'node-1', 'server': 'old.example', 'server_port': 443,
  'uuid': '00000000-0000-4000-8000-000000000001',
  'tls': {'enabled': true, 'server_name': 'tls.example', 'alpn': ['h2'], 'ech': {'enabled': true}},
  'transport': {'type': 'ws', 'path': '/kept', 'headers': {'X-Custom': 'keep'}},
  'domain_resolver': {'server': 'system', 'timeout': '3s'},
  'multiplex': {'enabled': true, 'protocol': 'h2mux'}};

class _Fixture {
  final channel = const MethodChannel('test.configuration-forms');
  final events = StreamController<dynamic>.broadcast();
  final boundary = GlobalKey();
  late BoxController box;
  late ProfileConfigStore store;
  Json root = {'outbounds': [_node], 'dns': {'servers': [{'type': 'local', 'tag': 'system'}]},
    'experimental': {'cache_file': {'enabled': true}}};
  String revision = 'revision-1';
  bool reject = false;
  final writes = <Json>[];
  final creates = <Json>[];
  Json wire() => {'formatted': jsonEncode(root), 'content': jsonEncode(root), 'revision': revision, 'stats': <String, dynamic>{}};

  Future<void> mount(WidgetTester tester, {Size size = const Size(360, 900), double scale = 1, bool dark = false}) async {
    reportFlutterErrors();
    tester.view.physicalSize = size;
    tester.view.devicePixelRatio = 1;
    tester.platformDispatcher.textScaleFactorTestValue = scale;
    tester.platformDispatcher.localesTestValue = const [Locale('en')];
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
    addTearDown(tester.platformDispatcher.clearLocalesTestValue);
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(channel, (call) async {
      if (call.method == 'ready') return {'version': 'test', 'coreVersion': 'test', 'profiles': [_profile],
        'preferences': {'theme': dark ? 'dark' : 'light'}, 'dashboard': {'status': 'Stopped', 'selectedProfileId': 7}};
      if (call.method == 'profiles.config') return wire();
      if (call.method == 'profiles.stats') return {'outbounds': 1, 'groups': 0, 'rules': 0};
      if (call.method == 'profiles.write') {
        final args = json(call.arguments);
        expect(args['revision'], revision);
        if (reject) throw PlatformException(code: 'config', message: 'Service reload failed');
        root = json(jsonDecode(args['content'] as String));
        writes.add(copyConfig(root));
        revision = 'revision-${writes.length + 1}';
        return wire();
      }
      if (call.method == 'profiles.save') { creates.add(json(call.arguments)); return _profile; }
      if (call.method == 'apps.list') return [{'package': 'app.one', 'name': 'One'}];
      return null;
    });
    box = BoxController(channel: channel, events: events.stream);
    await tester.pumpWidget(RepaintBoundary(key: boundary, child: BoxApp(controller: box)));
    await tester.pumpAndSettle();
    store = ProfileConfigStore(box, _profile);
    await store.load();
    addTearDown(() async {
      await tester.pumpWidget(const SizedBox());
      store.dispose();
      await events.close();
      messenger.setMockMethodCallHandler(channel, null);
    });
  }

  Future<void> open(WidgetTester tester, Widget page, {bool form = false}) async {
    final context = tester.element(find.byType(HomePage, skipOffstage: false));
    if (form) openForm(context, page); else openPage(context, page);
    await tester.pumpAndSettle();
  }

  Future<void> capture(WidgetTester tester, String name) async {
    await tester.runAsync(() async {
      final bitmap = await (boundary.currentContext!.findRenderObject()! as RenderRepaintBoundary).toImage();
      final data = await bitmap.toByteData(format: ui.ImageByteFormat.png);
      final file = File('build/screenshots/configuration-forms/$name.png');
      await file.parent.create(recursive: true);
      await file.writeAsBytes(data!.buffer.asUint8List());
      bitmap.dispose();
    });
  }
}

Future<void> _enter(WidgetTester tester, String key, String value) async {
  final field = find.byKey(ValueKey('config-field-$key'));
  await tester.ensureVisible(field);
  await tester.pumpAndSettle();
  await tester.enterText(field, value);
  await tester.pumpAndSettle();
}

Finder _saveButton() {
  final toolbar = find.byTooltip('Save').hitTestable();
  return toolbar.evaluate().isNotEmpty ? toolbar.last : find.text('Save').hitTestable().last;
}

Future<void> _save(WidgetTester tester) async {
  await tester.tap(_saveButton());
  await tester.pumpAndSettle();
}

void main() {
  setUpAll(() async {
    final root = Platform.environment['FLUTTER_ROOT'];
    if (root != null) {
      final file = File('$root/bin/cache/artifacts/material_fonts/Roboto-Regular.ttf');
      if (file.existsSync()) await (FontLoader('Roboto')..addFont(Future.value(ByteData.sublistView(await file.readAsBytes())))).load();
    }
  });

  testWidgets('profile settings expose forms and keep raw editing inside advanced settings', (tester) async {
    final f = _Fixture();
    await f.mount(tester);
    await f.open(tester, const ProfileDetailsPage(profile: _profile));
    expect(find.text('Open configuration'), findsNothing);
    expect(find.byType(ProfileEditor), findsNothing);
    await tester.tap(find.text('Profile settings').last);
    await tester.pumpAndSettle();
    expect(find.byType(ProfileConfigurationPage), findsOneWidget);
    expect(find.text('Edit full JSON'), findsNothing);
    expect(find.byType(ConfigValueEditor), findsNothing);
    final advanced = find.descendant(of: find.byType(ProfileConfigurationPage), matching: find.text('Advanced settings')).last;
    await tester.ensureVisible(advanced);
    await tester.tap(advanced);
    await tester.pumpAndSettle();
    expect(find.byType(AdvancedConfigPage), findsOneWidget);
    await tester.tap(find.text('Edit full JSON'));
    await tester.pumpAndSettle();
    expect(find.byType(ProfileEditor), findsOneWidget);
    expect(f.writes, isEmpty);
    expect(tester.takeException(), isNull);
  });

  testWidgets('node forms validate ports and preserve nested options and a failed draft', (tester) async {
    final f = _Fixture();
    await f.mount(tester);
    await f.open(tester, ConfigListPage(store: f.store, title: 'Proxy nodes and groups', path: const ['outbounds'], initial: const {}));
    expect(find.textContaining('"server"'), findsNothing);
    await tester.tap(find.text('node-1'));
    await tester.pumpAndSettle();
    expect(find.byType(ConfigEntryForm), findsOneWidget);
    expect(find.byType(ConfigValueEditor), findsNothing);
    await _enter(tester, 'server', 'new.example');
    await _enter(tester, 'server_port', '70000');
    await _save(tester);
    expect(f.writes, isEmpty);
    expect(find.text('Port: 1–65535'), findsOneWidget);
    await f.capture(tester, 'node-validation-error');
    expect(find.text('Port: 1–65535').hitTestable(), findsOneWidget);
    await _enter(tester, 'server_port', '8443');
    f.reject = true;
    await _save(tester);
    expect(find.text('Service reload failed'), findsOneWidget);
    expect(f.store.entries(['outbounds']).single, _node);
    expect(tester.widget<TextField>(find.byKey(const ValueKey('config-field-server'))).controller!.text, 'new.example');
    f.reject = false;
    await _save(tester);
    expect(f.store.entries(['outbounds']).single, {..._node, 'server': 'new.example', 'server_port': 8443});
    expect(f.writes.single['experimental'], {'cache_file': {'enabled': true}});
    expect(find.byType(ConfigEntryForm), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('DNS forms save typed servers without replacing other configuration', (tester) async {
    final f = _Fixture();
    await f.mount(tester);
    await f.open(tester, ConfigEntryForm(kind: ConfigKind.dnsServer, initial: newConfigEntry(ConfigKind.dnsServer, 'https'), isNew: true,
      onSave: (entry) => f.store.set(['dns', 'servers'], [...f.store.entries(['dns', 'servers']), entry], expectedRevision: 'revision-1', rethrowFailure: true)), form: true);
    await _enter(tester, 'tag', 'secure-dns');
    await _enter(tester, 'server', '1.1.1.1');
    await _enter(tester, 'server_port', '443');
    await _enter(tester, 'path', '/dns-query');
    await _save(tester);
    expect(f.store.entries(['dns', 'servers']).last,
      {'type': 'https', 'tag': 'secure-dns', 'server': '1.1.1.1', 'server_port': 443, 'path': '/dns-query'});
    expect(f.writes.single['outbounds'], [_node]);
    expect(tester.takeException(), isNull);
  });

  testWidgets('disabling an optional feature does not require its cleared hidden fields', (tester) async {
    final f = _Fixture();
    await f.mount(tester);
    Json? saved;
    await f.open(tester, ConfigEntryForm(kind: ConfigKind.outbound,
      initial: {..._node, 'tls': {...json(_node['tls']), 'reality': {'enabled': true, 'public_key': 'old-key'}}},
      onSave: (value) async { saved = value; return true; }), form: true);
    await _enter(tester, 'tls.reality.public_key', '');
    final toggle = find.byKey(const ValueKey('config-field-tls.reality.enabled'));
    FocusManager.instance.primaryFocus?.unfocus();
    await tester.pumpAndSettle();
    await Scrollable.ensureVisible(tester.element(toggle), alignment: .5);
    await tester.pumpAndSettle();
    expect(toggle.hitTestable(), findsOneWidget);
    await tester.tap(toggle);
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('config-field-tls.reality.public_key')), findsNothing);
    await _save(tester);
    expect(saved, {..._node, 'tls': {...json(_node['tls']), 'reality': {'enabled': false}}});
    expect(find.byType(ConfigEntryForm), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('routing edits preserve custom matches and selecting block removes the old destination', (tester) async {
    final f = _Fixture();
    await f.mount(tester);
    const rule = {'domain_suffix': 'old.example', 'source_ip_cidr': ['10.0.0.0/8'], 'outbound': 'node-1', 'action': 'route'};
    Json? saved;
    await f.open(tester, ConfigEntryForm(kind: ConfigKind.routeRule, initial: rule, outbounds: const ['node-1'],
      onSave: (value) async { saved = value; return true; }), form: true);
    await _enter(tester, 'domain_suffix', 'one.example\ntwo.example');
    final action = find.byKey(const ValueKey('config-field-action'));
    FocusManager.instance.primaryFocus?.unfocus();
    await tester.pumpAndSettle();
    await tester.ensureVisible(action);
    await tester.pumpAndSettle();
    await tester.tap(action);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Block').last);
    await tester.pumpAndSettle();
    await _save(tester);
    expect(saved, {'domain_suffix': ['one.example', 'two.example'], 'source_ip_cidr': ['10.0.0.0/8'], 'action': 'reject'});
    expect(tester.takeException(), isNull);
  });

  testWidgets('forms cannot overwrite a reloaded revision or save into a remote session', (tester) async {
    final f = _Fixture();
    await f.mount(tester);
    await f.open(tester, ConfigListPage(store: f.store, title: 'Proxy nodes and groups', path: const ['outbounds'], initial: const {}));
    await tester.tap(find.text('node-1'));
    await tester.pumpAndSettle();
    await _enter(tester, 'server', 'draft.example');
    f.revision = 'external-update';
    await f.store.load();
    await _save(tester);
    expect(f.writes, isEmpty);
    expect(find.text('Configuration changed; reload it before saving'), findsOneWidget);
    f.events.add({'type': 'dashboard', 'value': {'status': 'Stopped', 'remote': 'Remote server', 'remoteConnected': true}});
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('config-field-server')), findsNothing);
    await expectLater(f.store.set(['outbounds'], [], rethrowFailure: true), throwsStateError);
    expect(f.writes, isEmpty);
    await f.open(tester, const ProfileEditor(profile: _profile));
    expect(find.byType(TextField), findsNothing);
    await f.open(tester, ConfigEntryForm(kind: ConfigKind.outbound, initial: _node, onSave: (_) async => true), form: true);
    expect(find.byKey(const ValueKey('config-field-server')), findsNothing);
    await tester.tap(find.byTooltip('Close').hitTestable().last);
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
  });

  testWidgets('the new profile menu builds the core-validated VPN template from node fields', (tester) async {
    final expected = json(jsonDecode(File('test/fixtures/guided_profile.json').readAsStringSync()));
    final f = _Fixture();
    await f.mount(tester);
    await tester.tap(find.text('Profiles').last);
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Add profile').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('New profile'));
    await tester.pumpAndSettle();
    expect(find.byType(NewProfilePage), findsOneWidget);
    await tester.enterText(find.byType(TextFormField).last, 'My connection');
    await tester.tap(find.byKey(const ValueKey('new-profile-node')));
    await tester.pumpAndSettle();
    await _enter(tester, 'server', '127.0.0.1');
    await _enter(tester, 'server_port', '1080');
    await _save(tester);
    expect(find.byType(ConfigEntryForm), findsNothing);
    await _save(tester);
    expect(f.creates.single['name'], 'My connection');
    expect(json(jsonDecode(f.creates.single['content'] as String)), expected);
    expect(f.creates.single['url'], '');
    expect(find.byType(ProfileEditor), findsNothing);
    expect(tester.takeException(), isNull);
  });

  for (final (size, scale, dark) in [(const Size(320, 900), 1.4, true), (const Size(800, 1000), 1.0, false)]) {
    testWidgets('DNS and rule forms fit ${size.width} at scale $scale', (tester) async {
      final f = _Fixture();
      await f.mount(tester, size: size, scale: scale, dark: dark);
      await f.open(tester, ConfigEntryForm(kind: ConfigKind.dnsServer,
        initial: const {'type': 'https', 'tag': 'Secure DNS', 'server': '1.1.1.1', 'server_port': 443},
        onSave: (_) async => true), form: true);
      expect(_saveButton(), findsOneWidget);
      expect(Theme.of(tester.element(find.byType(ConfigEntryForm))).brightness, dark ? Brightness.dark : Brightness.light);
      await f.capture(tester, '${size.width.toInt()}-dns-$scale');
      final cancel = find.text('Cancel').hitTestable();
      await tester.tap(cancel.evaluate().isNotEmpty ? cancel.last : find.byTooltip('Close').hitTestable().last);
      await tester.pumpAndSettle();
      await f.open(tester, ConfigEntryForm(kind: ConfigKind.urlRewrite,
        initial: const {'name': 'Example redirect', 'pattern': r'^https://example\.com/(.*)', 'action': '302',
          'replacement': r'https://example.net/$1', 'disabled': true}, onSave: (_) async => true), form: true);
      final replacement = find.byKey(const ValueKey('config-field-replacement'));
      await tester.ensureVisible(replacement);
      await tester.pumpAndSettle();
      await f.capture(tester, '${size.width.toInt()}-rewrite-$scale');
      expect(replacement.hitTestable(), findsOneWidget);
      expect(_saveButton(), findsOneWidget);
      expect(tester.takeException(), isNull);
    });
  }
}
