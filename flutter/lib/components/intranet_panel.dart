// Adapted from FlClash v0.9.2 intranet_ip.dart and card.dart (GPL-3.0).
import 'package:flutter/services.dart';
import 'package:material_new_shapes/material_new_shapes.dart';
import '../widgets.dart';
import '../pages/configuration.dart';
import 'shape_badge.dart';

Future<void> _copy(BuildContext context, String text) async {
  await Clipboard.setData(ClipboardData(text: text));
  if (context.mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(context.tr('Copied'))));
}

Future<void> _change(BuildContext context, bool enabled) async {
  final box = context.box;
  if (!box.canSetLanProxy) return;
  if (enabled && rows(box.lan['bindings']).isEmpty) {
    await openForm(context, NewLANProxy(profileId: box.selectedProfileId, revision: box.lan['revision'] as String));
  } else {
    await box.setLanProxy(enabled);
  }
}

String _ports(BuildContext context, List<Json> bindings) {
  final values = bindings.where((binding) => binding['lan'] == true).map((binding) => binding['port']).toSet();
  return '${context.tr('Port')}: ${values.map((port) => port == 0 ? context.tr('Automatic port') : port).join(', ')}';
}

String _subtitle(BuildContext context) {
  final box = context.box;
  if (box.selectedProfileId == -1) return context.tr('No profile');
  if (box.lan['error'] != null) return context.tr('Unavailable');
  final active = box.running && box.lan['running'] == true;
  final bindings = rows(box.lan[active ? 'activeBindings' : 'bindings']);
  if (bindings.isEmpty) return context.tr('Add proxy listener');
  if (box.lan['enabled'] != true) return context.tr('Disabled');
  final ports = _ports(context, bindings);
  if (!active) return '$ports · ${context.tr('Not running')}';
  if (box.lanProxyEndpoints.isEmpty) return '$ports · ${context.tr('Unavailable')}';
  return ports;
}

class IntranetPanel extends StatelessWidget {
  const IntranetPanel({super.key});
  @override Widget build(BuildContext context) {
    final box = context.box, addresses = context.box.lanAddresses;
    final copy = box.lanCopyText;
    final figure = AppTextStyle.title1.of(context, emphasized: true).toTabular.copyWith(letterSpacing: 0);
    return BoxPanel(child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
      PanelTitle('Intranet IP', AppGlyphs.devices, trailing: Transform.translate(
        offset: const Offset(8, 0), child: SizedBox(width: 32, child: OverflowBox(
        fit: OverflowBoxFit.deferToChild, minHeight: 32, maxHeight: 32,
        child: IconButton(tooltip: context.tr('Copy'), padding: EdgeInsets.zero, iconSize: 18,
        style: IconButton.styleFrom(minimumSize: const Size.square(32), fixedSize: const Size.square(32),
          tapTargetSize: MaterialTapTargetSize.shrinkWrap, foregroundColor: context.colorScheme.onSurfaceVariant),
        icon: const GlyphIcon(AppGlyphs.copy, size: 18), onPressed: copy == null ? null : () {
          final text = box.lanCopyText;
          if (text != null) _copy(context, text);
        }))))),
      const SizedBox(height: 12),
      InkWell(onTap: () => openForm(context, const IntranetDetails()), child: SizedBox(
        height: AppTextStyle.title1.leading * MediaQuery.textScalerOf(context).scale(1),
        child: Align(alignment: AlignmentDirectional.centerStart, child: box.lan['addresses'] is! List
          ? SkeletonText(width: 120, style: figure)
          : addresses.isEmpty ? Text(context.tr('No network'), maxLines: 1, overflow: TextOverflow.ellipsis,
              style: figure.copyWith(color: context.colorScheme.onSurfaceVariant))
          : FittedBox(fit: BoxFit.scaleDown, alignment: AlignmentDirectional.centerStart,
              child: Text(addresses.first, maxLines: 1, style: figure))))),
      const SizedBox(height: 14),
      if (box.remote) Material(color: SurfaceLevel.block.colorOf(context.colorScheme), shape: AppShape.all(20),
        child: ListItem(leading: const GlyphIcon(AppGlyphs.wifi, size: 32),
          title: Text(context.tr('This device')), subtitle: Text(context.tr('Device network addresses'))))
      else LANProxyRow(value: box.lan['enabled'] == true, subtitle: _subtitle(context),
        onChanged: box.canSetLanProxy ? (value) => _change(context, value) : null),
    ]));
  }
}

/// FlClash's mobile switch row: an enabled cookie badge, disabled circle,
/// compact switch track, and an icon that yields space to larger text.
class LANProxyRow extends StatelessWidget {
  const LANProxyRow({super.key, required this.value, required this.subtitle, required this.onChanged});
  final bool value;
  final String subtitle;
  final ValueChanged<bool>? onChanged;
  @override Widget build(BuildContext context) {
    final title = context.tr('LAN proxy'), scheme = context.colorScheme;
    final style = AppTextStyle.subheadline.of(context, emphasized: true);
    return LayoutBuilder(builder: (context, constraints) {
      final measurement = TextPainter(text: TextSpan(text: title, style: style),
        textDirection: Directionality.of(context), textScaler: MediaQuery.textScalerOf(context))..layout();
      final showIcon = measurement.width <= constraints.maxWidth - 22 - 38 - 24 - CommonSwitch.trackWidth;
      measurement.dispose();
      return Material(color: SurfaceLevel.block.colorOf(scheme), shape: AppShape.all(20), clipBehavior: Clip.antiAlias,
        child: InkWell(onTap: onChanged == null ? null : () => onChanged!(!value), child: Padding(
          padding: EdgeInsets.fromLTRB(showIcon ? 10 : 14, 10, 12, 10), child: Row(spacing: 12, children: [
            if (showIcon) AnimatedSwitcher(duration: context.motionDuration(commonDuration), child: ShapeBadge(
              key: ValueKey(value), shape: value ? MaterialShapes.cookie6Sided : MaterialShapes.circle,
              color: value ? scheme.primary : SurfaceLevel.card.colorOf(scheme), size: 38,
              child: GlyphIcon(AppGlyphs.wifi, size: 19, color: value ? scheme.onPrimary : scheme.onSurfaceVariant))),
            Expanded(child: Column(mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.start, children: [
              Text(title, maxLines: 1, overflow: TextOverflow.ellipsis, style: style),
              Text(subtitle, maxLines: 1, overflow: TextOverflow.ellipsis, style: context.listCaptionStyle),
            ])),
            SizedBox(width: CommonSwitch.trackWidth, height: CommonSwitch.trackHeight, child: OverflowBox(
              maxWidth: double.infinity, maxHeight: double.infinity, child: CommonSwitch(
                value: value, onChanged: onChanged, semanticLabel: title))),
          ]))));
    });
  }
}

String _protocol(BuildContext context, Json binding) => switch (binding['type']) {
  'mixed' => context.tr(binding['tls'] == true ? 'HTTP / SOCKS over TLS' : 'HTTP / SOCKS'),
  'http' => binding['tls'] == true ? 'HTTPS' : 'HTTP',
  _ => 'SOCKS',
};

class IntranetDetails extends StatelessWidget {
  const IntranetDetails({super.key});
  @override Widget build(BuildContext context) {
    final box = context.box, active = !context.box.remote && context.box.running && context.box.lan['running'] == true;
    final bindings = rows(box.lan[active ? 'activeBindings' : 'bindings']);
    return BoxPage(title: 'Intranet IP', actions: [
      IconButtonData(glyph: AppGlyphs.refresh, tooltip: context.tr('Reload'), onPressed: box.lanChanging ? null : box.refreshLan),
    ], children: [
      generateSection(title: context.tr('This device'), items: [
        for (final address in box.lanAddresses) ListItem(title: SelectableText(address),
          trailing: IconButton(icon: const GlyphIcon(AppGlyphs.copy), tooltip: context.tr('Copy'), onPressed: () => _copy(context, address))),
        if (box.lanAddresses.isEmpty) ListItem(title: Text(context.tr('No network'))),
      ]),
      if (!box.remote) ...[
        LANProxyRow(value: box.lan['enabled'] == true, subtitle: _subtitle(context),
          onChanged: box.canSetLanProxy ? (value) => _change(context, value) : null),
        if (box.lan['error'] != null) ErrorState(box.lan['error']!, retry: box.refreshLan),
        generateSection(title: context.tr('Local proxy listeners'), items: [
          if (!active) ListItem(title: Text(context.tr('Not running'))),
          for (final binding in bindings) ...[
            ListItem(title: Text((binding['tag'] as String?)?.isNotEmpty == true ? binding['tag'] as String : _protocol(context, binding)),
              subtitle: Text('${_protocol(context, binding)} · ${binding['port'] == 0 ? context.tr('Automatic port') : '${context.tr('Port')}: ${binding['port']}'}'
                '${binding['authentication'] == true ? ' · ${context.tr('Authentication required')}' : ''}')),
            if (active) for (final endpoint in (binding['endpoints'] as List? ?? []).whereType<String>())
              ListItem(title: SelectableText(endpoint), trailing: IconButton(icon: const GlyphIcon(AppGlyphs.copy),
                tooltip: context.tr('Copy'), onPressed: () => _copy(context, endpoint))),
          ],
          if (bindings.isEmpty) ListItem(title: Text(context.tr('No local proxy listener'))),
        ]),
        if (box.selectedProfile != null) generateSection(items: [
          ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Open configuration')),
            widget: ProfileConfigurationPage(profile: box.selectedProfile)),
        ]),
      ],
    ]);
  }
}

class NewLANProxy extends StatefulWidget {
  const NewLANProxy({super.key, required this.profileId, required this.revision});
  final int profileId;
  final String revision;
  @override State<NewLANProxy> createState() => _NewLANProxyState();
}

class _NewLANProxyState extends State<NewLANProxy> {
  final _form = GlobalKey<FormState>();
  final _port = TextEditingController(text: '2080');
  @override void dispose() { _port.dispose(); super.dispose(); }
  Future<void> _save() async {
    if (!_form.currentState!.validate()) return;
    final saved = await context.box.setLanProxy(true, port: int.parse(_port.text),
      profileId: widget.profileId, revision: widget.revision);
    if (saved && mounted) context.safeNestedPop();
  }
  @override Widget build(BuildContext context) => BoxPage(title: 'Add proxy listener', loading: context.box.lanChanging,
    confirmationAction: IconButtonData(glyph: AppGlyphs.check, tooltip: context.tr('Save'),
      onPressed: context.box.canSetLanProxy ? _save : null),
    children: [
      Form(key: _form, child: TextFormField(controller: _port, keyboardType: TextInputType.number,
        decoration: InputDecoration(labelText: context.tr('Port')), validator: (value) {
          final port = int.tryParse(value ?? '');
          return port != null && port >= 1 && port <= 65535 ? null : context.tr('Invalid proxy port');
        })),
      const SizedBox(height: 16),
      Text(context.tr('Adds an HTTP and SOCKS proxy to the selected profile.'), style: context.listCaptionStyle),
    ]);
}
