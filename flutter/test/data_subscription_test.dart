import 'dart:async';
import 'package:b_box_ui/components/data_subscription.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  for (final data in BoxData.values) {
    testWidgets('$data subscriptions share demand and stop when hidden or backgrounded', (tester) async {
      final channel = MethodChannel('test.subscription.${data.name}');
      final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
      final events = StreamController<dynamic>.broadcast();
      final box = BoxController(channel: channel, events: events.stream);
      final changes = <bool>[];
      final method = data == BoxData.memory ? 'dashboard.memory.visible' : 'logs.visible';
      messenger.setMockMethodCallHandler(channel, (call) async {
        if (call.method == 'ready') return {'version': 'test', 'coreVersion': 'test'};
        expect(call.method, method);
        changes.add(json(call.arguments)['visible'] as bool);
        return null;
      });
      await box.initialize();
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
      Future<void> mount({bool card = true, bool details = false, bool tickers = true}) async {
        await tester.pumpWidget(BoxScope(controller: box, child: Column(children: [
          PageActivityScope(isActive: card, child: TickerMode(enabled: tickers,
            child: BoxDataSubscription(key: const ValueKey('card'), data: data, child: const SizedBox()))),
          if (details) BoxDataSubscription(key: const ValueKey('details'), data: data, child: const SizedBox()),
        ])));
        await tester.pump();
      }
      await mount();
      expect(changes, [true]);
      await mount(details: true);
      await mount(card: false, details: true);
      expect(changes, [true], reason: 'The details page still needs the data.');
      await mount(card: false);
      expect(changes, [true, false]);
      await mount();
      expect(changes, [true, false, true]);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
      await tester.pump();
      expect(changes.last, false);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
      await tester.pump();
      expect(changes.last, true);
      await mount(tickers: false);
      expect(changes.last, false);
      await mount();
      await tester.pumpWidget(const SizedBox());
      await tester.pump();
      expect(changes.last, false);
      expect(tester.takeException(), isNull);
      box.dispose();
      await events.close();
      messenger.setMockMethodCallHandler(channel, null);
    });
  }
}
