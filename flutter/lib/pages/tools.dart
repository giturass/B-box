import '../widgets.dart';
import '../components/data_subscription.dart';
import 'requests.dart';
import 'logs.dart';
import 'settings.dart' show RemotePage;
import 'endpoints.dart';
import 'terminal.dart';
import 'diagnostics.dart';

class ToolsPage extends StatelessWidget {
  const ToolsPage({super.key});
  @override Widget build(BuildContext context) {
    final box = context.watchBox(BoxAspect.tools);
    return BoxDataSubscription(data: BoxData.tools, child: BoxPage(title: 'Tools', children: [
      generateSection(items: [
        ListItem.open(leading: const GlyphIcon(AppGlyphs.connections), title: Text(context.tr('Connections and requests')),
          widget: const RequestsPage()),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.logs), title: Text(context.tr('Logs')), widget: const LogsPage()),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Terminal')), widget: const TerminalPage()),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.speed), title: Text(context.tr('Network diagnostics')), widget: const NetworkDiagnosticsPage()),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.proxies), title: Text(context.tr('Remote control')), widget: const RemotePage()),
      ]),
      if (box.running) generateSection(title: context.tr('Endpoints'), items: [
        for (final kind in ['tailscale', 'openvpn', 'openconnect', 'usbip'])
          if (rows(box.toolState[kind]).isNotEmpty)
            ListItem.open(leading: const GlyphIcon(AppGlyphs.devices), title: Text(endpointName(kind)), widget: EndpointsPage(kind: kind)),
      ]),
    ]));
  }
}
