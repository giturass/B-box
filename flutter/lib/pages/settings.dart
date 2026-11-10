import '../widgets.dart';
import 'appearance.dart';
import 'access.dart';
import 'about.dart';
import 'backup.dart';
import 'diagnostics.dart';

export 'appearance.dart';
export 'access.dart';
export 'about.dart';
export 'backup.dart';

class NativeToggle extends StatelessWidget {
  const NativeToggle(this.title, this.setting, {super.key, this.restart = true, this.autoRestart = false});
  final String title, setting;
  final bool restart, autoRestart;
  @override Widget build(BuildContext context) {
    final box = context.box;
    return ListItem.toggle(
      title: Text(context.tr(title)), value: box.settings[setting] == true,
      onChanged: box.serviceSettingsChanging || autoRestart && box.transitioning && !box.remote
        ? null : (value) => box.setSetting(setting, value, restart: restart, autoRestart: autoRestart),
    );
  }
}

class SettingsPage extends StatelessWidget {
  const SettingsPage({super.key});
  @override Widget build(BuildContext context) => BoxPage(title: 'Settings', children: [
    generateSection(items: [
      ListItem.open(leading: const GlyphIcon(AppGlyphs.paintbrush), title: Text(context.tr('Appearance')), widget: const AppearancePage()),
      ListItem.open(leading: const GlyphIcon(AppGlyphs.vpn), title: Text(context.tr('Service')), widget: const ServiceSettingsPage()),
      ListItem.open(leading: const GlyphIcon(AppGlyphs.cpu), title: Text(context.tr('Core')), widget: const CoreSettingsPage()),
      ListItem.open(leading: const GlyphIcon(AppGlyphs.appsList), title: Text(context.tr('Access control')), widget: const AccessPage()),
      ListItem.open(leading: const GlyphIcon(AppGlyphs.speed), title: Text(context.tr('Network diagnostics')), widget: const NetworkDiagnosticsPage()),
      ListItem.open(leading: const GlyphIcon(AppGlyphs.cloudSync), title: Text(context.tr('Backup and restore')), widget: const BackupPage()),
      ListItem.open(leading: const GlyphIcon(AppGlyphs.info), title: Text(context.tr('About')), widget: const AboutPage()),
    ]),
  ]);
}

class ServiceSettingsPage extends StatelessWidget {
  const ServiceSettingsPage({super.key});
  @override Widget build(BuildContext context) => BoxPage(title: 'Service', loading: context.box.serviceSettingsChanging, children: [
    generateSection(items: [
      const NativeToggle('Allow VPN bypass', 'allowBypass', autoRestart: true),
      const NativeToggle('Auto redirect', 'autoRedirect', autoRestart: true),
      const NativeToggle('Dynamic notification', 'dynamicNotification', autoRestart: true),
      ListItem(title: Text(context.tr('Battery optimization')),
        subtitle: Text(context.tr(context.box.settings['batteryExempt'] == true ? 'Unrestricted' : 'Allow background operation')),
        onTap: () => context.box.act('battery.open')),
    ]),
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
  @override Widget build(BuildContext context) => Scaffold(
    title: context.tr('Remote control'),
    primaryAction: IconButtonData(glyph: AppGlyphs.add, tooltip: context.tr('Add server'), onPressed: _edit),
    body: FutureBuilder<List<Json>>(future: _servers, builder: (context, snapshot) => ListView(
      padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 24 + context.bottomInset),
      children: [
        if (context.box.remote) ListItem(title: Text(context.box.dashboard['remote'].toString()), trailing: TextButton(
          onPressed: () => context.box.act('remote.disconnect'), child: Text(context.tr('Disconnect')))),
        if (snapshot.hasError) ErrorState(snapshot.error!),
        if (snapshot.hasData && snapshot.data!.isEmpty) const EmptyState('No remote servers'),
        for (final server in snapshot.data ?? <Json>[]) Padding(padding: const EdgeInsets.only(bottom: 12), child: Card(
          onPressed: () => context.box.act('remote.connect', {'id': server['id']}),
          child: ListItem(title: Text(server['name'].toString()), subtitle: Text(server['url'].toString()), trailing: BoxMenu(items: [
            PopupMenuItem(glyph: AppGlyphs.edit, label: context.tr('Edit'), onPressed: () => _edit(server)),
            PopupMenuItem(glyph: AppGlyphs.delete, label: context.tr('Delete'), danger: true, onPressed: () async {
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
      BoxField(label: context.tr('Name'), child: TextField(controller: _name)),
      const SizedBox(height: 16),
      BoxField(label: context.tr('Address'), child: TextField(controller: _url, keyboardType: TextInputType.url)),
      const SizedBox(height: 16),
      BoxField(label: context.tr('Secret'), child: TextField(controller: _secret, obscureText: true)),
    ],
  );
}
