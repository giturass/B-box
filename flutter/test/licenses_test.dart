import 'dart:async';
import 'package:b_box_ui/pages/licenses.dart';
import 'package:b_box_ui/widgets.dart';
import 'package:flutter/foundation.dart' show LicenseEntry, LicenseEntryWithLineBreaks, LicenseRegistry;
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('licenses load, group shared notices, search and open complete selectable text', (tester) async {
    LicenseRegistry.reset();
    addTearDown(LicenseRegistry.reset);
    final entries = StreamController<LicenseEntry>();
    LicenseRegistry.addLicense(() => entries.stream);
    await tester.pumpWidget(PrismApp(
      localizationsDelegates: const [BoxUiDelegate(), GlobalWidgetsLocalizations.delegate],
      builder: (context, child) => ViewScope(size: MediaQuery.sizeOf(context), isMobile: true, child: child!),
      home: const LicensesPage(),
    ));
    await tester.pump();
    expect(find.byType(ActivityIndicator), findsOneWidget);
    entries.add(LicenseEntryWithLineBreaks(['prism_ui', 'shared dependency'], 'Shared copyright notice.\n\nPermission to redistribute.'));
    entries.add(LicenseEntryWithLineBreaks(['prism_ui'], 'Additional library terms.'));
    await entries.close();
    await tester.pumpAndSettle();
    expect(find.text('prism_ui'), findsOneWidget);
    expect(find.text('shared dependency'), findsOneWidget);

    final scaffold = tester.widget<Scaffold>(find.byType(Scaffold));
    scaffold.searchState!.onSearch('PRISM');
    await tester.pumpAndSettle();
    expect(find.text('shared dependency'), findsNothing);
    await tester.tap(find.text('prism_ui'));
    await tester.pumpAndSettle();
    expect(find.byWidgetPredicate((widget) => widget is SelectableText &&
      widget.data == 'Shared copyright notice.\n\nPermission to redistribute.'), findsOneWidget);
    expect(find.byWidgetPredicate((widget) => widget is SelectableText &&
      widget.data == 'Additional library terms.'), findsOneWidget);
    tester.element(find.byType(BoxPage)).safeNestedPop();
    await tester.pumpAndSettle();
    expect(find.byType(BoxPage), findsNothing);
    expect(find.text('prism_ui'), findsOneWidget);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
  });
}
