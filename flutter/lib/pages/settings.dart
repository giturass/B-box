import '../widgets.dart';

export 'appearance.dart';
export 'access.dart';
export 'about.dart';
export 'backup.dart';

class NativeToggle extends StatelessWidget {
  const NativeToggle(this.title, this.setting, {super.key, this.restart = true});
  final String title, setting;
  final bool restart;
  @override Widget build(BuildContext context) => ListItem.toggle(
    title: Text(context.tr(title)), value: context.box.settings[setting] == true,
    onChanged: (value) => context.box.setSetting(setting, value, restart: restart),
  );
}

class GeneralSettingsPage extends StatelessWidget {
  const GeneralSettingsPage({super.key});
  @override Widget build(BuildContext context) => BoxPage(title: 'General', children: [
    generateSection(items: [
      ListItem.open(leading: const GlyphIcon(AppGlyphs.vpn), title: Text(context.tr('Service')), widget: const ServiceSettingsPage()),
      ListItem.open(leading: const GlyphIcon(AppGlyphs.cpu), title: Text(context.tr('Core')), widget: const CoreSettingsPage()),
    ]),
  ]);
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
