import '../widgets.dart';
import 'connections.dart';
import 'logs.dart';
import 'settings.dart';
import 'diagnostics.dart';
import 'endpoints.dart';
import 'reports.dart';
import 'terminal.dart';
import 'terminal_settings.dart';
import 'configuration.dart';

class ToolsPage extends StatelessWidget {
  const ToolsPage({super.key});
  @override Widget build(BuildContext context) {
    final box = context.box;
    return BoxPage(title: 'Tools', children: [
      generateSection(items: [
        ListItem.open(leading: const GlyphIcon(AppGlyphs.connections), title: Text(context.tr('Connections')), widget: const ConnectionsPage()),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.logs), title: Text(context.tr('Logs')), widget: const LogsPage()),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Terminal')), widget: const TerminalPage()),
      ]),
      generateSection(title: context.tr('Settings'), items: [
        ListItem<String>.options(leading: const GlyphIcon(AppGlyphs.language), title: Text(context.tr('Language')),
          options: const ['system', 'zh', 'en'], value: box.preferences['locale'] as String? ?? 'system',
          textBuilder: (value) => value == 'system' ? context.tr('Follow system') : value == 'zh' ? '简体中文' : 'English',
          onChanged: (value) => box.setPreference('locale', value)),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.paintbrush), title: Text(context.tr('Appearance')), widget: const AppearancePage()),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.cloudSync), title: Text(context.tr('Backup and restore')), widget: const BackupPage()),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.appsList), title: Text(context.tr('Access control')), widget: const AccessPage()),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.wrench), title: Text(context.tr('Advanced configuration')), widget: const ProfileConfigurationPage()),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.customize), title: Text(context.tr('Resources')), widget: const ProfileConfigurationPage(resources: true)),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.settings), title: Text(context.tr('General')), widget: const GeneralSettingsPage()),
      ], footer: context.tr('Manage DNS, routing and rule sets for the selected profile.')),
      generateSection(title: 'B-box', items: [
        ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Ghostty settings')), widget: const TerminalSettingsPage()),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.proxies), title: Text(context.tr('Remote control')), widget: const RemotePage()),
        ListItem(leading: const GlyphIcon(AppGlyphs.dashboard), title: Text(context.tr('Dashboard cards')), onTap: box.editDashboard),
      ]),
      generateSection(title: context.tr('Network diagnostics'), items: [
        ListItem.open(leading: const GlyphIcon(AppGlyphs.speed), title: Text(context.tr('Network quality')), widget: const DiagnosticsPage(quality: true)),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.proxies), title: Text(context.tr('STUN test')), widget: const DiagnosticsPage(quality: false)),
      ]),
      generateSection(title: context.tr('Reports'), items: [
        for (final kind in ['crash', 'oom', 'power'])
          ListItem.open(leading: const GlyphIcon(AppGlyphs.profiles), title: Text(context.tr(reportTitle(kind))), widget: ReportsPage(kind: kind)),
      ]),
      if (box.running) generateSection(title: context.tr('Endpoints'), items: [
        for (final kind in ['tailscale', 'openvpn', 'openconnect', 'usbip'])
          if (rows(box.toolState[kind]).isNotEmpty)
            ListItem.open(leading: const GlyphIcon(AppGlyphs.devices), title: Text(endpointName(kind)), widget: EndpointsPage(kind: kind)),
      ]),
      generateSection(title: context.tr('Other'), items: [
        ListItem.open(leading: const GlyphIcon(AppGlyphs.info), title: Text(context.tr('About')), widget: const AboutPage()),
      ]),
    ]);
  }
}
