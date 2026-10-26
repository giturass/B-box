import '../widgets.dart';
import 'connections.dart';
import 'logs.dart';
import 'dashboard.dart';
import 'settings.dart';
import 'diagnostics.dart';
import 'endpoints.dart';

class ToolsPage extends StatelessWidget {
  const ToolsPage({super.key});
  @override Widget build(BuildContext context) {
    final box = context.box;
    return BoxPage(title: 'Tools', children: [
      generateSection(title: context.tr('View'), items: [
        ListItem.open(leading: const GlyphIcon(AppGlyphs.connections), title: Text(context.tr('Connections')), widget: const ConnectionsPage()),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.logs), title: Text(context.tr('Logs')), widget: const LogsPage()),
      ]),
      generateSection(title: context.tr('Settings'), items: [
        ListItem.open(leading: const GlyphIcon(AppGlyphs.palette), title: Text(context.tr('Appearance')), widget: const AppearancePage()),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.appsList), title: Text(context.tr('Access control')), widget: const AccessPage()),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.vpn), title: Text(context.tr('Service')), widget: const ServiceSettingsPage()),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.memory), title: Text(context.tr('Core')), widget: const CoreSettingsPage()),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.proxies), title: Text(context.tr('Remote control')), widget: const RemotePage()),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.profiles), title: Text(context.tr('Backup and restore')), widget: const BackupPage()),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.dashboard), title: Text(context.tr('Dashboard cards')), widget: const DashboardEditor()),
      ]),
      generateSection(title: context.tr('Network diagnostics'), items: [
        ListItem.open(leading: const GlyphIcon(AppGlyphs.speed), title: Text(context.tr('Network quality')), widget: const DiagnosticsPage(quality: true)),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.proxies), title: Text(context.tr('STUN test')), widget: const DiagnosticsPage(quality: false)),
      ]),
      if (box.running) generateSection(title: context.tr('Endpoints'), items: [
        for (final kind in ['tailscale', 'openvpn', 'openconnect', 'usbip'])
          if (rows(box.toolState[kind]).isNotEmpty)
            ListItem.open(leading: const GlyphIcon(AppGlyphs.devices), title: Text(endpointName(kind)), widget: EndpointsPage(kind: kind)),
      ]),
      generateSection(title: context.tr('About'), items: [
        ListItem.open(leading: const GlyphIcon(AppGlyphs.info), title: Text('B-box'), subtitle: Text(box.version), widget: const AboutPage()),
      ]),
    ]);
  }
}
