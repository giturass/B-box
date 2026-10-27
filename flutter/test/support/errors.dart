import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';

/// Keep the offending widget/constraints in CI logs when takeException is used.
void reportFlutterErrors() {
  final previous = FlutterError.onError;
  FlutterError.onError = (details) {
    debugPrint(details.toString());
    previous?.call(details);
  };
  addTearDown(() => FlutterError.onError = previous);
}
