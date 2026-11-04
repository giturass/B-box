import 'dart:async';
import 'package:b_box_ui/components/capture_poll.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('completed details retain their snapshot and refresh only on demand or resume', (tester) async {
    const channel = MethodChannel('test.completed_capture');
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    final events = StreamController<dynamic>.broadcast();
    final box = BoxController(channel: channel, events: events.stream);
    var reads = 0, complete = false;
    VoidCallback? refresh;
    messenger.setMockMethodCallHandler(channel, (call) async {
      if (call.method == 'ready') return {'version': 'test', 'coreVersion': 'test'};
      if (call.method == 'capture.detail') {
        reads++;
        return {'id': 'request', 'complete': complete, 'durationMs': reads};
      }
      return null;
    });
    await box.initialize();
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pumpWidget(BoxScope(controller: box, child: Directionality(textDirection: TextDirection.ltr,
      child: CapturePoll(method: 'capture.detail', arguments: const {'id': 'request'}, untilComplete: true,
        builder: (context, value, error, reload) {
          refresh = reload;
          return Text('${value?['durationMs']}');
        }),
    )));
    await tester.pumpAndSettle();
    expect(reads, 1);
    complete = true;
    await tester.pump(const Duration(seconds: 2));
    await tester.pumpAndSettle();
    expect(reads, 2);
    await tester.pump(const Duration(seconds: 20));
    expect(reads, 2);
    expect(find.text('2'), findsOneWidget);
    refresh!();
    await tester.pumpAndSettle();
    expect(reads, 3);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    await tester.pump(const Duration(seconds: 20));
    expect(reads, 3);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pumpAndSettle();
    expect(reads, 4);
    await tester.pump(const Duration(seconds: 20));
    expect(reads, 4);
    await tester.pumpWidget(const SizedBox());
    box.dispose();
    await events.close();
    messenger.setMockMethodCallHandler(channel, null);
  });
}
