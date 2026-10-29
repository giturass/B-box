import 'dart:async';
import 'package:b_box_ui/main.dart';
import 'package:b_box_ui/pages/logs.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

const _levels = ['Trace', 'Debug', 'Info', 'Warn', 'Error', 'Fatal', 'Panic'];
final _records = <Json>[
  for (var index = 0; index < _levels.length; index++)
    {'id': index + 1, 'level': _levels[index], 'message': 'core ${_levels[index]} record'},
];

void main() {
  late StreamController<dynamic> events;
  late BoxController box;
  String? exported;

  Future<void> mount(WidgetTester tester, {bool remote = false}) async {
    tester.binding.platformDispatcher.localesTestValue = const [Locale('en')];
    addTearDown(tester.binding.platformDispatcher.clearLocalesTestValue);
    tester.view.physicalSize = const Size(360, 1200);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    const channel = MethodChannel('test.logs');
    events = StreamController<dynamic>.broadcast();
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(channel, (call) async {
      if (call.method == 'ready') return {
        'version': 'test', 'coreVersion': 'test',
        'dashboard': {'status': 'Stopped',
          if (remote) ...{'remote': 'Server', 'remoteConnected': true}},
      };
      if (call.method == 'document.save') exported = json(call.arguments)['content'] as String;
      if (call.method == 'logs.clear') {
        events.add({'type': 'logs', 'value': {'defaultLogLevel': box.defaultLogLevel, 'entries': []}});
      }
      return null;
    });
    box = BoxController(channel: channel, events: events.stream);
    exported = null;
    await tester.pumpWidget(BoxApp(controller: box));
    await tester.pumpAndSettle();
    addTearDown(() async {
      await tester.pumpWidget(const SizedBox());
      await events.close();
      messenger.setMockMethodCallHandler(channel, null);
    });
    openPage(tester.element(find.byType(HomePage)), const LogsPage());
    await tester.pumpAndSettle();
  }

  CommonScaffold scaffold(WidgetTester tester) => tester.widget<CommonScaffold>(
    find.descendant(of: find.byType(LogsPage), matching: find.byType(CommonScaffold)).first,
  );

  Future<void> emit(WidgetTester tester, String? level, [List<Json>? records]) async {
    events.add({'type': 'logs', 'value': {'defaultLogLevel': level, 'entries': records ?? _records}});
    await tester.pumpAndSettle();
  }

  Future<void> choose(WidgetTester tester, String label) async {
    await tester.tap(find.byTooltip('More').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text(label).last);
    await tester.pumpAndSettle();
  }

  Future<void> expectExport(WidgetTester tester, List<String> levels) async {
    scaffold(tester).iconActions.single.onPressed!();
    await tester.pumpAndSettle();
    expect(exported, isNotNull);
    expect(exported!.split('\n').where((line) => line.isNotEmpty),
      levels.map((level) => '— [$level] core $level record'));
  }

  for (final remote in [false, true]) {
    testWidgets('default follows the core threshold including severe logs, remote=$remote', (tester) async {
      await mount(tester, remote: remote);
      for (var index = 0; index < _levels.length; index++) {
        final level = _levels[index];
        await emit(tester, level);
        final selected = scaffold(tester).menuItems.where((item) => item.checked == true);
        expect(selected.map((item) => item.label), ['Follow configuration ($level)']);
        await expectExport(tester, _levels.sublist(index));
        expect(box.logs, hasLength(_levels.length), reason: 'Filtering must retain the original records.');
        if (level == 'Warn') {
          expect(find.text('core Error record'), findsOneWidget);
          expect(find.text('core Info record'), findsNothing);
        }
      }
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('manual filters survive core updates and can return to following configuration', (tester) async {
    await mount(tester);
    await emit(tester, 'Info');
    await choose(tester, 'All');
    await emit(tester, 'Error');
    await expectExport(tester, _levels);
    await choose(tester, 'Debug');
    await emit(tester, 'Warn');
    await expectExport(tester, ['Debug']);
    await choose(tester, 'Follow configuration (Warn)');
    await expectExport(tester, ['Warn', 'Error', 'Fatal', 'Panic']);
    await emit(tester, 'Info');
    await expectExport(tester, ['Info', 'Warn', 'Error', 'Fatal', 'Panic']);
    expect(tester.takeException(), isNull);
  });

  testWidgets('unknown defaults, pause, search, export and clear keep records available', (tester) async {
    await mount(tester);
    await emit(tester, null);
    await expectExport(tester, _levels);
    await emit(tester, 'Warn');
    await choose(tester, 'Pause');
    await emit(tester, 'Warn', [..._records, {'id': 8, 'level': 'Error', 'message': 'later record'}]);
    scaffold(tester).onKeywordsUpdate!(['core']);
    await tester.pumpAndSettle();
    await expectExport(tester, ['Warn', 'Error', 'Fatal', 'Panic']);
    scaffold(tester).onKeywordsUpdate!([]);
    await tester.pumpAndSettle();
    expect(find.text('later record'), findsNothing);
    await choose(tester, 'Resume');
    expect(find.text('later record'), findsOneWidget);
    await choose(tester, 'Clear');
    expect(box.logs, isEmpty);
    expect(box.defaultLogLevel, 'Warn');
    expect(find.text('No logs'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}
