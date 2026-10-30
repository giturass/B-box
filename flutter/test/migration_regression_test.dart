import 'dart:async';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:b_box_ui/pages/access.dart';
import 'package:b_box_ui/pages/profiles.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('binary subscription metadata survives the import form', (tester) async {
    const channel = MethodChannel('test.binary-import');
    final events = StreamController<dynamic>.broadcast();
    Json? saved;
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(channel, (call) async {
      if (call.method == 'ready') return {'version': 'test', 'coreVersion': 'test', 'dashboard': {'status': 'Stopped'}};
      if (call.method == 'profiles.save') saved = json(call.arguments);
      return null;
    });
    addTearDown(() => messenger.setMockMethodCallHandler(channel, null));
    await tester.pumpWidget(BoxApp(controller: BoxController(channel: channel, events: events.stream)));
    await tester.pumpAndSettle();
    const imported = {'name': 'Imported subscription', 'content': '{}', 'remote': true,
      'url': 'https://example.com/config.json', 'autoUpdate': false, 'interval': 120, 'updatedAt': 1700000000000};
    openForm(tester.element(find.byType(HomePage)), const ProfileForm(initial: imported));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Save').last);
    await tester.pumpAndSettle();
    expect(saved, imported);
    expect(find.byType(ProfileForm), findsNothing);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
    await events.close();
  });

  testWidgets('remote dashboard cannot display the selected local profile name', (tester) async {
    const channel = MethodChannel('test.remote-name');
    final events = StreamController<dynamic>.broadcast();
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(channel, (call) async => call.method == 'ready' ? {
      'version': 'test', 'coreVersion': 'test',
      'profiles': [{'id': 1, 'name': 'Private local profile'}],
      'dashboard': {'status': 'Started', 'selectedProfileId': 1, 'remote': 'Server', 'remoteConnected': true, 'profileName': 'Private local profile'},
    } : null);
    addTearDown(() => messenger.setMockMethodCallHandler(channel, null));
    await tester.pumpWidget(BoxApp(controller: BoxController(channel: channel, events: events.stream)));
    await tester.pumpAndSettle();
    expect(find.text('Remote configuration'), findsOneWidget);
    expect(find.text('Private local profile'), findsNothing);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
    await events.close();
  });

  testWidgets('managed application page uses saved selections without scanning APKs', (tester) async {
    const channel = MethodChannel('test.managed-scan');
    final events = StreamController<dynamic>.broadcast();
    final calls = <String>[];
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    const settings = {'perAppEnabled': true, 'perAppManaged': true, 'perAppManagedList': ['cn.example.app']};
    messenger.setMockMethodCallHandler(channel, (call) async {
      calls.add(call.method);
      if (call.method == 'ready') return {'version': 'test', 'coreVersion': 'test', 'settings': settings, 'dashboard': {'status': 'Stopped'}};
      if (call.method == 'apps.rescan') throw StateError('Opening the page must not scan APKs');
      if (call.method == 'apps.list') return [{'package': 'cn.example.app', 'name': 'Managed app', 'internet': true}];
      return null;
    });
    addTearDown(() => messenger.setMockMethodCallHandler(channel, null));
    final box = BoxController(channel: channel, events: events.stream);
    await tester.pumpWidget(BoxApp(controller: box));
    await tester.pumpAndSettle();
    openPage(tester.element(find.byType(HomePage)), const AccessPage());
    await tester.pumpAndSettle();
    expect(calls.where((method) => method == 'apps.rescan'), isEmpty);
    expect(calls.where((method) => method == 'apps.list'), hasLength(1));
    expect(box.settings['perAppManagedList'], ['cn.example.app']);
    expect(find.text('Managed app'), findsOneWidget);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
    await events.close();
  });
}
