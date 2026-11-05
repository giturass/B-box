import 'package:flutter/foundation.dart' show LicenseEntry, LicenseRegistry;
import '../widgets.dart';

class LicensesPage extends StatefulWidget {
  const LicensesPage({super.key});

  @override
  State<LicensesPage> createState() => _LicensesPageState();
}

class _LicensesPageState extends State<LicensesPage> {
  late final _licenses = _load();
  String _query = '';

  Future<Map<String, List<LicenseEntry>>> _load() async {
    final packages = <String, List<LicenseEntry>>{};
    await for (final license in LicenseRegistry.licenses) {
      for (final package in license.packages) {
        (packages[package] ??= []).add(license);
      }
    }
    return packages;
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    title: context.tr('Open-source licenses'),
    searchState: AppBarSearchState(onSearch: (value) => setState(() => _query = value)),
    body: FutureBuilder(
      future: _licenses,
      builder: (context, snapshot) {
        if (snapshot.hasError) return ErrorState(snapshot.error!);
        final licenses = snapshot.data;
        if (licenses == null) return const Center(child: ActivityIndicator.page());
        final packages = licenses.keys
            .where((name) => name.toLowerCase().contains(_query.toLowerCase()))
            .toList()..sort((a, b) => a.toLowerCase().compareTo(b.toLowerCase()));
        return PrismListView(
          padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 24 + context.bottomInset),
          itemCount: packages.length,
          itemBuilder: (context, index) {
            final name = packages[index];
            return ListItem.open(
              title: Text(name),
              widget: BoxPage(title: name, children: [
                for (final license in licenses[name]!)
                  Padding(
                    padding: const EdgeInsets.only(bottom: 24),
                    child: SelectableText(license.paragraphs.map((paragraph) => paragraph.text).join('\n\n')),
                  ),
              ]),
            );
          },
        );
      },
    ),
  );
}
