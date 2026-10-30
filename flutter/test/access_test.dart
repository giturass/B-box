import 'dart:async';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/pages/access.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

CommonScaffold scaffold(WidgetTester tester) => tester.widget<CommonScaffold>(
  find.descendant(of: find.byType(AccessPage), matching: find.byType(CommonScaffold)).first,
);

CommonPopupMenuItem action(WidgetTester tester, String label) {
  Iterable<CommonPopupMenuItem> flatten(List<CommonPopupMenuItem> items) sync* {
    for (final item in items) {
      yield item;
      yield* flatten(item.subItems);
    }
  }
  return flatten(scaffold(tester).menuItems).firstWhere((item) => item.label == label);
}

Future<BoxController> openAccess(WidgetTester tester, Future<Object?> Function(MethodCall) handler) async {
  tester.binding.platformDispatcher.localesTestValue = const [Locale('en')];
  addTearDown(tester.binding.platformDispatcher.clearLocalesTestValue);
  const channel = MethodChannel('test.access-loading');
  final events = StreamController<dynamic>.broadcast();
  final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  messenger.setMockMethodCallHandler(channel, handler);
  final box = BoxController(channel: channel, events: events.stream);
  addTearDown(() async {
    await tester.pumpWidget(const SizedBox());
    await events.close();
    messenger.setMockMethodCallHandler(channel, null);
  });
  await tester.pumpWidget(BoxApp(controller: box));
  await tester.pumpAndSettle();
  openPage(tester.element(find.byType(HomePage)), const AccessPage());
  await tester.pumpAndSettle();
  return box;
}

const visibleApp = {'package': 'visible.app', 'name': 'Visible app', 'internet': true};
const systemApp = {'package': 'system.app', 'name': 'System app', 'internet': true, 'system': true};
const offlineApp = {'package': 'offline.app', 'name': 'Offline app', 'internet': false};
Json ready([Json settings = const {}]) => {'version': 'test', 'coreVersion': 'test',
  'settings': {'perAppEnabled': true, ...settings}, 'dashboard': {'status': 'Stopped'}};

void main() {
  testWidgets('filters request cached metadata and preserve selections for hidden apps', (tester) async {
    final lists = <Json>[];
    final icons = <String>[];
    Json? saved;
    await openAccess(tester, (call) async {
      if (call.method == 'ready') return ready({'perAppList': ['offline.app']});
      if (call.method == 'apps.list') {
        final args = json(call.arguments);
        lists.add(args);
        return [visibleApp, if (args['system'] == true) systemApp, if (args['nonInternet'] == true) offlineApp];
      }
      if (call.method == 'apps.icon') icons.add(json(call.arguments)['package'] as String);
      if (call.method == 'settings.patch') { saved = json(json(call.arguments)['value']); return saved; }
      return null;
    });
    expect(lists, [{'refresh': true, 'system': false, 'nonInternet': false}]);
    expect(find.text('Visible app'), findsOneWidget);
    expect(find.text('System app'), findsNothing);
    action(tester, 'Show system apps').onPressed!();
    await tester.pumpAndSettle();
    expect(lists.last, {'refresh': false, 'system': true, 'nonInternet': false});
    expect(find.text('System app'), findsOneWidget);
    action(tester, 'Show apps without Internet permission').onPressed!();
    await tester.pumpAndSettle();
    expect(find.text('Offline app'), findsOneWidget);
    expect(icons.where((package) => package == 'visible.app'), hasLength(1));
    action(tester, 'Show apps without Internet permission').onPressed!();
    await tester.pumpAndSettle();
    await tester.tap(find.text('Visible app'));
    await tester.pumpAndSettle();
    scaffold(tester).confirmationAction!.onPressed!();
    await tester.pumpAndSettle();
    expect(saved!['perAppList'], containsAll(['visible.app', 'offline.app']));
    expect(tester.takeException(), isNull);
  });

  testWidgets('manual managed scan leaves the list visible and a failure preserves selections', (tester) async {
    final scan = Completer<Json>();
    var scans = 0;
    var lists = 0;
    final box = await openAccess(tester, (call) async {
      if (call.method == 'ready') return ready({'perAppManaged': true, 'perAppManagedList': ['visible.app']});
      if (call.method == 'apps.list') { lists++; return [visibleApp]; }
      if (call.method == 'apps.rescan') { scans++; return scan.future; }
      return null;
    });
    expect(scans, 0);
    action(tester, 'Rescan managed apps').onPressed!();
    await tester.pump();
    await tester.pump();
    expect(scans, 1);
    expect(find.text('Visible app'), findsOneWidget);
    expect(action(tester, 'Rescan managed apps').onPressed, isNull);
    scan.completeError(PlatformException(code: 'scan', message: 'Scan failed'));
    await tester.pumpAndSettle();
    expect(box.settings['perAppManagedList'], ['visible.app']);
    expect(find.text('Visible app'), findsOneWidget);
    expect(action(tester, 'Rescan managed apps').onPressed, isNotNull);
    expect(lists, 1);
    expect(tester.takeException(), isNull);
  });

  testWidgets('unchanged query settings do not reload the list or clear icons', (tester) async {
    var lists = 0;
    var icons = 0;
    await openAccess(tester, (call) async {
      if (call.method == 'ready') return ready({'queryMode': 'SHIZUKU'});
      if (call.method == 'apps.list') { lists++; return [visibleApp]; }
      if (call.method == 'apps.icon') icons++;
      return null;
    });
    action(tester, 'Application query').onPressed!();
    await tester.pumpAndSettle();
    final queryPage = find.byWidgetPredicate((widget) => widget is BoxPage && widget.title == 'Application query');
    expect(queryPage, findsOneWidget);
    tester.element(queryPage).safeNestedPop();
    await tester.pumpAndSettle();
    expect(lists, 1);
    expect(icons, 1);
    action(tester, 'Refresh').onPressed!();
    await tester.pumpAndSettle();
    expect(lists, 2);
    expect(icons, 2);
    expect(tester.takeException(), isNull);
  });

  testWidgets('permission requests refresh once after returning from query settings', (tester) async {
    var lists = 0;
    var permissions = 0;
    await openAccess(tester, (call) async {
      if (call.method == 'ready') return ready({'queryMode': 'SHIZUKU'});
      if (call.method == 'apps.list') { lists++; return [visibleApp]; }
      if (call.method == 'apps.permission') permissions++;
      return null;
    });
    action(tester, 'Application query').onPressed!();
    await tester.pumpAndSettle();
    await tester.tap(find.text('Grant Shizuku permission'));
    await tester.pumpAndSettle();
    expect(permissions, 1);
    expect(lists, 1);
    tester.element(find.byWidgetPredicate((widget) => widget is BoxPage && widget.title == 'Application query')).safeNestedPop();
    await tester.pumpAndSettle();
    expect(lists, 2);
    expect(tester.takeException(), isNull);
  });

  testWidgets('a completed managed scan updates the selected-only list without reloading packages', (tester) async {
    const secondApp = {'package': 'second.app', 'name': 'Second app', 'internet': true};
    final scan = Completer<Json>();
    var lists = 0;
    await openAccess(tester, (call) async {
      if (call.method == 'ready') return ready({'perAppManaged': true, 'perAppManagedList': ['visible.app']});
      if (call.method == 'apps.list') { lists++; return [visibleApp, secondApp]; }
      if (call.method == 'apps.rescan') return scan.future;
      return null;
    });
    action(tester, 'Selected apps').onPressed!();
    await tester.pumpAndSettle();
    expect(find.text('Visible app'), findsOneWidget);
    expect(find.text('Second app'), findsNothing);
    action(tester, 'Rescan managed apps').onPressed!();
    await tester.pump();
    await tester.pump();
    scan.complete({'perAppEnabled': true, 'perAppManaged': true, 'perAppManagedList': ['second.app']});
    await tester.pumpAndSettle();
    expect(find.text('Visible app'), findsNothing);
    expect(find.text('Second app'), findsOneWidget);
    expect(lists, 1);
    expect(tester.takeException(), isNull);
  });
}
