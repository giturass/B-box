import '../widgets.dart';

String reportTitle(String kind) => {'crash': 'Crash reports', 'oom': 'Memory reports', 'power': 'Power reports'}[kind]!;

class ReportsPage extends StatefulWidget {
  const ReportsPage({super.key, required this.kind});
  final String kind;
  @override State<ReportsPage> createState() => _ReportsPageState();
}
class _ReportsPageState extends State<ReportsPage> {
  Future<List<Json>>? _reports;
  Future<List<Json>> _load() async => rows(await context.box.call('reports.list', {'kind': widget.kind}));
  @override void didChangeDependencies() { super.didChangeDependencies(); _reports ??= _load(); }
  void _refresh() => setState(() => _reports = _load());
  @override Widget build(BuildContext context) => CommonScaffold(title: context.tr(reportTitle(widget.kind)), iconActions: [
    IconButtonData(glyph: AppGlyphs.refresh, onPressed: _refresh, tooltip: context.tr('Refresh')),
    IconButtonData(glyph: AppGlyphs.delete, tooltip: context.tr('Clear'), onPressed: () async {
      final box = context.box;
      if (await confirm(context, 'Delete all reports?')) {
        await box.act('reports.clear', {'kind': widget.kind});
        if (mounted) _refresh();
      }
    }),
  ], body: FutureBuilder<List<Json>>(future: _reports, builder: (context, snapshot) => ListView(
    padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 24 + context.bottomInset),
    children: [
      if (snapshot.hasError) ErrorState(snapshot.error!, retry: _refresh),
      if (!snapshot.hasData && !snapshot.hasError) const Center(child: CircularProgressIndicator()),
      if (snapshot.data?.isEmpty == true) const EmptyState('No reports'),
      for (final report in snapshot.data ?? <Json>[]) ListItem(
        title: Text(localDate(report['date'])), subtitle: Text(report['id'].toString()),
        leading: GlyphIcon(report['read'] == true ? AppGlyphs.profiles : AppGlyphs.info),
        onTap: () async {
          await openPage(context, ReportDetail(kind: widget.kind, id: report['id'].toString()));
          if (mounted) _refresh();
        },
      ),
    ],
  )));
}

class ReportDetail extends StatefulWidget {
  const ReportDetail({super.key, required this.kind, required this.id});
  final String kind, id;
  @override State<ReportDetail> createState() => _ReportDetailState();
}
class _ReportDetailState extends State<ReportDetail> {
  Future<List<Json>>? _files;
  bool _config = false, _logs = true, _encrypt = true, _busy = false;
  Json get _args => {'kind': widget.kind, 'id': widget.id};
  @override void didChangeDependencies() { super.didChangeDependencies(); _files ??= context.box.call('reports.files', _args).then(rows); }
  @override Widget build(BuildContext context) => BoxPage(title: reportTitle(widget.kind), loading: _busy, actions: [
    IconButtonData(glyph: AppGlyphs.delete, tooltip: context.tr('Delete'), onPressed: () async {
      final box = context.box;
      if (await confirm(context, 'Delete', message: widget.id) && await box.act('reports.delete', _args) && context.mounted) Navigator.pop(context);
    }),
  ], children: [
    FutureBuilder<List<Json>>(future: _files, builder: (context, snapshot) => snapshot.hasError ? ErrorState(snapshot.error!) : generateGroup([
      for (final file in snapshot.data ?? <Json>[]) ListItem(title: Text(file['label'].toString()), subtitle: Text(bytes(file['size'] as num? ?? 0)),
        onTap: () => file['kind'] == 'PROFILE'
          ? context.box.act('reports.save', {..._args, 'name': file['name']})
          : openPage(context, ReportText(args: {..._args, 'name': file['name']}, title: file['label'].toString())),
        trailing: IconButton(tooltip: context.tr('Export'), icon: const GlyphIcon(AppGlyphs.download),
          onPressed: () => context.box.act('reports.save', {..._args, 'name': file['name']}))),
    ])),
    generateSection(title: context.tr('Export'), items: [
      ListItem.toggle(title: Text(context.tr('Include configuration')), value: _config, onChanged: (v) => setState(() => _config = v)),
      ListItem.toggle(title: Text(context.tr('Include logs')), value: _logs, onChanged: (v) => setState(() => _logs = v)),
      ListItem.toggle(title: Text(context.tr('Encrypt for sing-box maintainers')), value: _encrypt, onChanged: (v) => setState(() => _encrypt = v)),
      ListItem(title: Text(context.tr('Export archive')), onTap: _busy ? null : () async {
        setState(() => _busy = true);
        await context.box.act('reports.export', {..._args, 'config': _config, 'logs': _logs, 'encrypt': _encrypt});
        if (mounted) setState(() => _busy = false);
      }),
    ]),
  ]);
}

class ReportText extends StatefulWidget {
  const ReportText({super.key, required this.args, required this.title});
  final Json args;
  final String title;
  @override State<ReportText> createState() => _ReportTextState();
}
class _ReportTextState extends State<ReportText> {
  Future<dynamic>? _content;
  @override void didChangeDependencies() { super.didChangeDependencies(); _content ??= context.box.call('reports.read', widget.args); }
  @override Widget build(BuildContext context) => BoxPage(title: widget.title, children: [
    FutureBuilder<dynamic>(future: _content, builder: (context, snapshot) => snapshot.hasError ? ErrorState(snapshot.error!)
      : snapshot.hasData ? SelectableText(snapshot.data.toString(), style: const TextStyle(fontFamily: 'JetBrainsMono', fontSize: 12))
      : const Center(child: CircularProgressIndicator())),
  ]);
}
