import 'dart:async';
import 'dart:io';

/// A root package's assets are bundled without the `packages/prism_ui/` prefix
/// the components load them by, so link it in as an app's bundle has it.
Future<void> testExecutable(FutureOr<void> Function() testMain) async {
  try {
    Link(
      'build/unit_test_assets/packages/prism_ui',
    ).createSync('..', recursive: true);
  } on FileSystemException {
    // Another test file linked it first.
  }
  await testMain();
}
