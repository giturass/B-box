import 'dart:convert';
import '../widgets.dart';

export 'appearance.dart';

class NativeToggle extends StatelessWidget {
  const NativeToggle(this.title, this.setting, {super.key, this.restart = true});
  final String title, setting;
  final bool restart;
  @override Widget build(BuildContext context) => ListItem.toggle(
    title: Text(context.tr(title)), value: context.box.settings[setting] == true,
    onChanged: (value) => context.box.setSetting(setting, value, restart: restart),
  );
}

class ServiceSettingsPage extends StatelessWidget {
  const ServiceSettingsPage({super.key});
  @override Widget build(BuildContext context) => BoxPage(title: 'Service', children: [
    generateSection(items: [
      const NativeToggle('Allow VPN bypass', 'allowBypass'),
      const NativeToggle('Auto redirect', 'autoRedirect'),
      const NativeToggle('Dynamic notification', 'dynamicNotification'),
      ListItem(title: Text(context.tr('Battery optimization')),
        subtitle: Text(context.tr(context.box.settings['batteryExempt'] == true ? 'Unrestricted' : 'Allow background operation')),
        onTap: () => context.box.act('battery.open')),
    ]),
    if (context.box.running && !context.box.remote) Padding(padding: const EdgeInsets.all(16), child: FilledButton(
      onPressed: () async { if (await context.box.act('service.reload', {'restart': true})) context.box.clearApplyRequired(); },
      child: Text(context.tr('Apply service changes')),
    )),
  ]);
}

class CoreSettingsPage extends StatelessWidget {
  const CoreSettingsPage({super.key});
  @override Widget build(BuildContext context) => BoxPage(title: 'Core', children: [
    generateSection(items: [
      ListItem(title: Text(context.tr('Core version')), subtitle: Text(context.box.coreVersion)),
      const NativeToggle('Disable deprecation warnings', 'disableDeprecatedWarnings', restart: false),
      const NativeToggle('Enable memory protection', 'oomKillerEnabled'),
      const NativeToggle('Disable core memory killer', 'oomKillerDisabled'),
      ListItem.input(title: Text(context.tr('Memory limit')), dialogTitle: context.tr('Memory limit'),
        value: (context.box.settings['oomMemoryLimitMB'] ?? 50).toString(), suffixText: 'MiB',
        keyboardType: TextInputType.number, validator: (value) {
          final number = int.tryParse(value ?? '') ?? 0;
          return number >= 10 && number <= 4096 ? null : '10–4096 MiB';
        },
        onChanged: (value) { if (value != null) context.box.setSetting('oomMemoryLimitMB', int.parse(value)); }),
      const NativeToggle('Power reports', 'powerReportEnabled'),
    ]),
  ]);
}

class AccessPage extends StatefulWidget {
  const AccessPage({super.key});
  @override State<AccessPage> createState() => _AccessPageState();
}
class _AccessPageState extends State<AccessPage> {
  Future<List<Json>>? _apps;
  String _query = '';
  bool _system = false, _selectedOnly = false;
  @override void didChangeDependencies() { super.didChangeDependencies(); _apps ??= _load(); }
  Future<List<Json>> _load() async => rows(await context.box.call('apps.list'));
  void _reload() => setState(() => _apps = _load());
  @override Widget build(BuildContext context) {
    final box = context.box;
    final selected = (box.settings[box.settings['perAppManaged'] == true ? 'perAppManagedList' : 'perAppList'] as List? ?? []).cast<String>().toSet();
    return CommonScaffold(
      title: context.tr('Access control'),
      searchState: AppBarSearchState(onSearch: (query) => setState(() => _query = query)),
      iconActions: [IconButtonData(glyph: AppGlyphs.refresh, tooltip: context.tr('Retry'), onPressed: _reload)],
      body: FutureBuilder<List<Json>>(
        future: _apps,
        builder: (context, snapshot) {
          final apps = (snapshot.data ?? <Json>[]).where((app) =>
            (_system || app['system'] != true) && (!_selectedOnly || selected.contains(app['package'])) &&
            (app['name'].toString() + ' ' + app['package'].toString()).toLowerCase().contains(_query.toLowerCase()),
          ).toList();
          return ListView.builder(
            padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 24 + context.bottomInset),
            itemCount: apps.length + 1,
            itemBuilder: (context, index) {
              if (index == 0) return Column(children: [
                generateSection(items: [
                  const NativeToggle('Per-app proxy', 'perAppEnabled'),
                  ListItem<int>.options(title: Text(context.tr('Mode')), options: const [1, 2],
                    value: (box.settings['perAppMode'] as num?)?.toInt() == 2 ? 2 : 1,
                    textBuilder: (value) => context.tr(value == 1 ? 'Bypass selected apps' : 'Proxy selected apps'),
                    onChanged: (value) => box.setSetting('perAppMode', value)),
                  const NativeToggle('Managed app list', 'perAppManaged'),
                  ListItem.options(title: Text(context.tr('Application query')), options: const ['SHIZUKU', 'ROOT'],
                    value: box.settings['queryMode'] as String? ?? 'SHIZUKU', textBuilder: (value) => value == 'ROOT' ? 'Root' : 'Shizuku',
                    onChanged: (value) async { await box.setSetting('queryMode', value, restart: false); _reload(); }),
                  if (box.settings['queryMode'] == 'SHIZUKU') ListItem(title: Text(context.tr('Grant Shizuku permission')),
                    onTap: () => box.act('apps.permission')),
                  ListItem.toggle(title: Text(context.tr('Show system apps')), value: _system, onChanged: (value) => setState(() => _system = value)),
                  ListItem.toggle(title: Text(context.tr('Selected apps')), value: _selectedOnly, onChanged: (value) => setState(() => _selectedOnly = value)),
                ]),
                if (box.settings['perAppManaged'] == true) Padding(padding: const EdgeInsets.all(16), child: Text(context.tr('Disable managed app list to edit selection'))),
                if (snapshot.hasError) ErrorState(snapshot.error!, retry: _reload),
                if (!snapshot.hasData && !snapshot.hasError) const Padding(padding: EdgeInsets.all(24), child: Center(child: CircularProgressIndicator())),
                if (snapshot.hasData && apps.isEmpty) const EmptyState('No results'),
                if (apps.isNotEmpty) const ListSectionGap(),
              ]);
              final app = apps[index - 1];
              return ItemPositionProvider(
                position: ItemPosition.get(index - 1, apps.length),
                child: ListItem.checkbox(
                  title: Text(app['name'].toString()), subtitle: Text(app['package'].toString()),
                  value: selected.contains(app['package']),
                  onChanged: box.settings['perAppManaged'] == true ? null : (value) {
                    final next = Set<String>.of(selected);
                    value == true ? next.add(app['package'] as String) : next.remove(app['package']);
                    box.setSetting('perAppList', next.toList());
                  },
                ),
              );
            },
          );
        },
      ),
    );
  }
}

class RemotePage extends StatefulWidget {
  const RemotePage({super.key});
  @override State<RemotePage> createState() => _RemotePageState();
}
class _RemotePageState extends State<RemotePage> {
  Future<List<Json>>? _servers;
  @override void didChangeDependencies() { super.didChangeDependencies(); _servers ??= _load(); }
  Future<List<Json>> _load() async => rows(await context.box.call('remote.list'));
  Future<void> _edit([Json? server]) async {
    await openForm(context, RemoteForm(initial: server ?? {}));
    if (mounted) setState(() => _servers = _load());
  }
  @override Widget build(BuildContext context) => CommonScaffold(
    title: context.tr('Remote control'),
    primaryAction: IconButtonData(glyph: AppGlyphs.add, tooltip: context.tr('Add server'), onPressed: _edit),
    body: FutureBuilder<List<Json>>(future: _servers, builder: (context, snapshot) => ListView(
      padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 24 + context.bottomInset),
      children: [
        if (context.box.remote) ListItem(title: Text(context.box.dashboard['remote'].toString()), trailing: TextButton(
          onPressed: () => context.box.act('remote.disconnect'), child: Text(context.tr('Disconnect')))),
        if (snapshot.hasError) ErrorState(snapshot.error!),
        if (snapshot.hasData && snapshot.data!.isEmpty) const EmptyState('No remote servers'),
        for (final server in snapshot.data ?? <Json>[]) Padding(padding: const EdgeInsets.only(bottom: 12), child: CommonCard(
          onPressed: () => context.box.act('remote.connect', {'id': server['id']}),
          child: ListItem(title: Text(server['name'].toString()), subtitle: Text(server['url'].toString()), trailing: BoxMenu(items: [
            CommonPopupMenuItem(glyph: AppGlyphs.edit, label: context.tr('Edit'), onPressed: () => _edit(server)),
            CommonPopupMenuItem(glyph: AppGlyphs.delete, label: context.tr('Delete'), danger: true, onPressed: () async {
              if (await confirm(context, 'Delete', message: server['name'].toString()) && context.mounted) {
                await context.box.act('remote.delete', {'id': server['id']});
                if (mounted) setState(() => _servers = _load());
              }
            }),
          ])),
        )),
      ],
    )),
  );
}

class RemoteForm extends StatefulWidget {
  const RemoteForm({super.key, required this.initial});
  final Json initial;
  @override State<RemoteForm> createState() => _RemoteFormState();
}
class _RemoteFormState extends State<RemoteForm> {
  late final _name = TextEditingController(text: widget.initial['name'] as String? ?? '');
  late final _url = TextEditingController(text: widget.initial['url'] as String? ?? '');
  late final _secret = TextEditingController(text: widget.initial['secret'] as String? ?? '');
  bool _saving = false;
  @override void dispose() { _name.dispose(); _url.dispose(); _secret.dispose(); super.dispose(); }
  @override Widget build(BuildContext context) => BoxPage(title: 'Remote control', loading: _saving,
    confirmationAction: IconButtonData(glyph: AppGlyphs.check, tooltip: context.tr('Save'), onPressed: _saving ? null : () async {
      setState(() => _saving = true);
      final saved = await context.box.act('remote.save', {...widget.initial, 'name': _name.text, 'url': _url.text, 'secret': _secret.text});
      if (mounted) {
        setState(() => _saving = false);
        if (saved) context.safeNestedPop();
      }
    }),
    children: [
      TextField(controller: _name, decoration: InputDecoration(labelText: context.tr('Name'))),
      const SizedBox(height: 16),
      TextField(controller: _url, keyboardType: TextInputType.url, decoration: InputDecoration(labelText: context.tr('Address'))),
      const SizedBox(height: 16),
      TextField(controller: _secret, obscureText: true, decoration: InputDecoration(labelText: context.tr('Secret'))),
    ],
  );
}

class BackupPage extends StatefulWidget {
  const BackupPage({super.key});
  @override State<BackupPage> createState() => _BackupPageState();
}
class _BackupPageState extends State<BackupPage> {
  bool _busy = false;
  Future<void> _backup(bool restore) async {
    final box = context.box;
    setState(() => _busy = true);
    try {
      if (restore) {
        final data = await box.call('backup.read');
        if (data != null && mounted && await confirm(context, 'Restore backup', message: 'Import profiles and remote servers, and restore settings from this backup?', action: 'Import')) {
          await box.call('backup.restore', {'backup': data});
        }
      } else {
        final data = await box.call('backup.create');
        await box.call('document.save', {'name': 'B-box-backup.json', 'content': const JsonEncoder.withIndent('  ').convert(data)});
      }
    } catch (error) { box.report(error); }
    if (mounted) setState(() => _busy = false);
  }
  @override Widget build(BuildContext context) => BoxPage(title: 'Backup and restore', loading: _busy, children: [
    generateSection(items: [
      ListItem(leading: const GlyphIcon(AppGlyphs.upload), title: Text(context.tr('Create backup')), onTap: _busy ? null : () => _backup(false)),
      ListItem(leading: const GlyphIcon(AppGlyphs.profiles), title: Text(context.tr('Restore backup')), onTap: _busy || context.box.remote ? null : () => _backup(true)),
    ]),
  ]);
}

class AboutPage extends StatelessWidget {
  const AboutPage({super.key});
  @override Widget build(BuildContext context) => BoxPage(title: 'About', children: [
    const SizedBox(height: 24),
    Center(child: Text('B-box', style: AppTextStyle.largeTitle.of(context, emphasized: true))),
    const SizedBox(height: 24),
    DetailRows({'Version': context.box.version, 'Core version': context.box.coreVersion, 'FlClash interface': 'v0.9.2'}),
    generateSection(items: [
      ListItem(title: Text(context.tr('Source code')), subtitle: const Text('giturass/B-box'),
        onTap: () => context.box.act('url.open', {'url': 'https://github.com/giturass/B-box'})),
      ListItem(title: const Text('FlClash'), subtitle: const Text('chen08209/FlClash · GPL-3.0'),
        onTap: () => context.box.act('url.open', {'url': 'https://github.com/chen08209/FlClash'})),
      ListItem(title: Text(context.tr('Open-source licenses')), onTap: () => showLicensePage(context: context, applicationName: 'B-box', applicationVersion: context.box.version)),
    ]),
  ]);
}
