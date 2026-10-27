import 'package:flutter/services.dart';
import '../widgets.dart';
import '../pages/profile_editor.dart';

class IntranetPanel extends StatelessWidget {
  const IntranetPanel({super.key});
  @override Widget build(BuildContext context) {
    final box = context.box, addresses = (context.box.lan['addresses'] as List? ?? []).cast<String>();
    final bindings = rows(box.lan['bindings']);
    return BoxPanel(child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
      PanelTitle('Intranet IP', AppGlyphs.devices, trailing: IconButton(tooltip: context.tr('Copy'),
        icon: const GlyphIcon(AppGlyphs.copy, size: 18), onPressed: addresses.isEmpty ? null : () async {
          await Clipboard.setData(ClipboardData(text: addresses.join('\n')));
          if (context.mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(context.tr('Copied'))));
        })),
      const SizedBox(height: 12),
      InkWell(onTap: addresses.isEmpty ? null : () => openForm(context, const IntranetDetails()),
        child: Text(addresses.isEmpty ? context.tr('No network') : addresses.first, maxLines: 1, overflow: TextOverflow.ellipsis,
          style: AppTextStyle.title1.of(context, emphasized: true).toTabular.copyWith(letterSpacing: 0))),
      const SizedBox(height: 14),
      Material(color: SurfaceLevel.block.colorOf(context.colorScheme), shape: AppShape.all(20), clipBehavior: Clip.antiAlias,
        child: ListItem(leading: const GlyphIcon(AppGlyphs.wifi, size: 32),
          title: Text(context.tr(box.remote ? 'This device' : 'Local proxy listeners')),
          subtitle: Text(box.remote ? context.tr('Device network addresses') : bindings.isEmpty ? context.tr('No local proxy listener') : bindings.map(bindingAddress).join('\n')),
          onTap: box.remote || box.selectedProfile == null ? null : () => openPage(context, ProfileEditor(profile: box.selectedProfile!)))),
    ]));
  }
}

String bindingAddress(Json binding) {
  final address = binding['listen'].toString();
  return '${binding['type']} · ${address.contains(':') ? '[$address]' : address}:${binding['port']}';
}

class IntranetDetails extends StatelessWidget {
  const IntranetDetails({super.key});
  @override Widget build(BuildContext context) => BoxPage(title: 'Intranet IP', children: [
    generateSection(title: context.tr('This device'), items: [
      for (final address in context.box.lan['addresses'] as List? ?? []) ListItem(title: SelectableText(address.toString()),
        trailing: IconButton(icon: const GlyphIcon(AppGlyphs.copy), tooltip: context.tr('Copy'), onPressed: () => Clipboard.setData(ClipboardData(text: address.toString())))),
    ]),
    if (!context.box.remote) generateSection(title: context.tr('Local proxy listeners'), items: [
      for (final binding in rows(context.box.lan['bindings'])) ListItem(title: Text(binding['tag'].toString()), subtitle: Text(bindingAddress(binding))),
    ]),
  ]);
}
