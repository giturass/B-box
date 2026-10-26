import 'package:flutter/services.dart';
import 'package:mobile_scanner/mobile_scanner.dart';
import '../widgets.dart';
import 'profile_editor.dart';

class ProfilesPage extends StatefulWidget {
  const ProfilesPage({super.key, this.picker = false});
  final bool picker;
  @override State<ProfilesPage> createState() => _ProfilesPageState();
}
class _ProfilesPageState extends State<ProfilesPage> {
  String _query = '';
  final _updating = <int>{};
  Future<void> _update(Json profile) async {
    final id = (profile['id'] as num).toInt();
    if (_updating.contains(id)) return;
    setState(() => _updating.add(id));
    await context.box.act('profiles.update', {'id': id});
    if (mounted) setState(() => _updating.remove(id));
  }
  Future<void> _export(Json profile) async {
    final box = context.box;
    try {
      final content = await box.call('profiles.read', {'id': profile['id']});
      await box.call('document.save', {'name': profile['name'].toString() + '.json', 'content': content});
    } catch (error) { box.report(error); }
  }
  Future<void> _share(Json profile) async {
    final box = context.box;
    try {
      final content = profile['remote'] == true ? profile['url'] : await box.call('profiles.read', {'id': profile['id']});
      await box.call('share.text', {'content': content});
    } catch (error) { box.report(error); }
  }
  List<CommonPopupMenuItem> _menu(Json profile) => [
    CommonPopupMenuItem(glyph: AppGlyphs.edit, label: context.tr('Edit'), onPressed: () => openForm(context, ProfileForm(initial: profile))),
    CommonPopupMenuItem(glyph: AppGlyphs.profiles, label: context.tr('Open configuration'), onPressed: () => openPage(context, ProfileEditor(profile: profile))),
    if (profile['remote'] == true)
      CommonPopupMenuItem(glyph: AppGlyphs.refresh, label: context.tr('Update'), onPressed: () => _update(profile)),
    CommonPopupMenuItem(glyph: AppGlyphs.upload, label: context.tr('Share'), onPressed: () => _share(profile)),
    CommonPopupMenuItem(glyph: AppGlyphs.copy, label: context.tr('Export'), onPressed: () => _export(profile)),
    if (profile['remote'] == true)
      CommonPopupMenuItem(glyph: AppGlyphs.qrCode, label: context.tr('QR code'), onPressed: () => openForm(context, BoxPage(title: 'QR code', children: [
        Center(child: QrCodeView(data: Uri(scheme: 'sing-box', host: 'import-remote-profile',
          queryParameters: {'name': profile['name'].toString(), 'url': profile['url'].toString()}).toString())),
      ]))),
    CommonPopupMenuItem(glyph: AppGlyphs.delete, label: context.tr('Delete'), onPressed: () async {
      final box = context.box;
      if (await confirm(context, 'Delete this profile?', message: 'This cannot be undone.')) await box.act('profiles.delete', {'id': profile['id']});
    }),
  ];

  @override Widget build(BuildContext context) {
    final box = context.box;
    final profiles = box.profiles.where((p) => p['name'].toString().toLowerCase().contains(_query.toLowerCase())).toList();
    return CommonScaffold(
      title: context.tr('Profiles'),
      searchState: AppBarSearchState(onSearch: (query) => setState(() => _query = query)),
      primaryAction: IconButtonData(glyph: AppGlyphs.add, tooltip: context.tr('Add profile'),
        onPressed: box.remote ? null : () => openForm(context, const AddProfilePage())),
      iconActions: [
        IconButtonData(glyph: AppGlyphs.refresh, tooltip: context.tr('Update all'), isLoading: _updating.isNotEmpty,
          onPressed: box.remote || !box.profiles.any((p) => p['remote'] == true) ? null : () async {
            for (final profile in List<Json>.of(box.profiles).where((p) => p['remote'] == true)) { await _update(profile); }
          }),
        IconButtonData(glyph: AppGlyphs.sort, tooltip: context.tr('Reorder'), onPressed: box.remote || box.profiles.length < 2 ? null : () => openForm(context, const ProfileOrderPage())),
      ],
      body: box.remote ? const EmptyState('Disconnect remote session to edit local profiles') : profiles.isEmpty ? ListView(
        padding: EdgeInsets.only(top: context.contentTopPadding),
        children: [EmptyState(box.profiles.isEmpty ? 'No profiles yet' : 'No results', glyph: AppGlyphs.profiles,
          message: box.profiles.isEmpty ? 'Import a sing-box JSON configuration to get started.' : null,
          action: FilledButton.icon(icon: const GlyphIcon(AppGlyphs.add), label: Text(context.tr('Add profile')), onPressed: () => openForm(context, const AddProfilePage())),
        )],
      ) : LayoutBuilder(builder: (context, constraints) => GridView.builder(
        padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 24 + context.bottomInset),
        gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
          crossAxisCount: constraints.maxWidth < 700 ? 1 : 2,
          mainAxisSpacing: 12, crossAxisSpacing: 12, mainAxisExtent: 96 * MediaQuery.textScalerOf(context).scale(1),
        ),
        itemCount: profiles.length,
        itemBuilder: (context, index) {
          final profile = profiles[index];
          final selected = profile['id'] == box.selectedProfileId;
          return ContextMenuRegion(menuItems: _menu(profile), child: CommonCard(
            radius: AppCorner.xl, isSelected: selected,
            onPressed: () async {
              if (await box.act('profiles.select', {'id': profile['id']}) && widget.picker && context.mounted) context.safeNestedPop();
            },
            child: ListItem(
              title: Column(crossAxisAlignment: CrossAxisAlignment.start, mainAxisSize: MainAxisSize.min, children: [
                Text(profile['name'].toString(), maxLines: 1, overflow: TextOverflow.ellipsis, style: AppTextStyle.headline.of(context)),
                const SizedBox(height: 8),
                Text(context.tr(profile['remote'] == true ? 'Remote' : 'Local'), style: context.listCaptionStyle),
              ]),
              trailing: Row(mainAxisSize: MainAxisSize.min, children: [
                if (_updating.contains(profile['id'])) const SizedBox.square(dimension: 20, child: CircularProgressIndicator(strokeWidth: 2)),
                if (selected) const SelectionBadge(size: 24),
                BoxMenu(items: _menu(profile)),
              ]),
            ),
          ));
        },
      )),
    );
  }
}

class AddProfilePage extends StatefulWidget {
  const AddProfilePage({super.key});
  @override State<AddProfilePage> createState() => _AddProfilePageState();
}
class _AddProfilePageState extends State<AddProfilePage> {
  bool _loading = false;
  Future<void> _import(String source) async {
    final box = context.box;
    setState(() => _loading = true);
    try {
      Json? data;
      switch (source) {
        case 'File':
          final result = await box.call('profiles.importFile');
          if (result != null) data = json(result);
        case 'Clipboard':
          final text = (await Clipboard.getData(Clipboard.kTextPlain))?.text?.trim();
          if (text == null || text.isEmpty) throw StateError(context.tr('Empty clipboard'));
          data = await decodeImport(box, text);
        case 'Scan QR code':
          final text = await openPage<String>(context, const ScanPage());
          if (text != null) data = await decodeImport(box, text);
      }
      if (data != null && mounted) await openForm(context, ProfileForm(initial: data));
    } catch (error) { box.report(error); }
    if (mounted) setState(() => _loading = false);
  }
  @override Widget build(BuildContext context) => BoxPage(
    title: 'Add profile', loading: _loading,
    children: [
      generateSection(items: [
        ListItem.open(leading: const GlyphIcon(AppGlyphs.link), title: Text(context.tr('Import from URL')),
          widget: const ProfileForm(initial: {'url': '', 'remote': true}), forceFull: false),
        for (final item in [('File', AppGlyphs.profiles), ('Clipboard', AppGlyphs.copy), ('Scan QR code', AppGlyphs.qrCode)])
          ListItem(leading: GlyphIcon(item.$2), title: Text(context.tr(item.$1)), onTap: _loading ? null : () => _import(item.$1)),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.add), title: Text(context.tr('New local profile')),
          widget: const ProfileForm(initial: {'content': '{}'}), forceFull: false),
      ], footer: context.tr('This URL must serve a sing-box JSON configuration.')),
    ],
  );
}

Future<Json> decodeImport(BoxController box, String text) async {
  if (text.startsWith('https://') || text.startsWith('http://') || text.startsWith('sing-box://')) {
    return json(await box.call('profiles.decodeIntent', {'uri': text}));
  }
  await box.call('config.check', {'content': text});
  return {'name': 'Imported profile', 'content': text};
}

class ProfileForm extends StatefulWidget {
  const ProfileForm({super.key, required this.initial});
  final Json initial;
  @override State<ProfileForm> createState() => _ProfileFormState();
}
class _ProfileFormState extends State<ProfileForm> {
  final _form = GlobalKey<FormState>();
  late final _name = TextEditingController(text: widget.initial['name'] as String? ?? '');
  late final _url = TextEditingController(text: widget.initial['url'] as String? ?? '');
  late final _interval = TextEditingController(text: (widget.initial['interval'] ?? 60).toString());
  late bool _remote = widget.initial['remote'] == true || _url.text.isNotEmpty;
  late bool _auto = widget.initial['autoUpdate'] != false;
  bool _saving = false;
  @override void dispose() { _name.dispose(); _url.dispose(); _interval.dispose(); super.dispose(); }
  Future<void> _save() async {
    if (_saving || !_form.currentState!.validate()) return;
    final box = context.box;
    setState(() => _saving = true);
    try {
      await box.call('profiles.save', {
        ...widget.initial, 'name': _name.text.trim(), 'url': _remote ? _url.text.trim() : '',
        'autoUpdate': _remote && _auto, 'interval': int.tryParse(_interval.text) ?? 60,
      });
      if (mounted) context.safeNestedPop();
    } catch (error) { box.report(error); }
    if (mounted) setState(() => _saving = false);
  }
  @override Widget build(BuildContext context) => BoxPage(
    title: widget.initial['id'] == null ? 'Add profile' : 'Edit',
    loading: _saving,
    confirmationAction: IconButtonData(glyph: AppGlyphs.check, tooltip: context.tr('Save'), onPressed: _saving ? null : _save),
    children: [Form(key: _form, child: Column(children: [
      TextFormField(controller: _name, decoration: InputDecoration(labelText: context.tr('Name')),
        validator: (text) => text == null || text.trim().isEmpty ? context.tr('Required') : null),
      const SizedBox(height: 16),
      ListItem.toggle(title: Text(context.tr('Remote')), value: _remote, onChanged: (value) => setState(() => _remote = value)),
      if (_remote) ...[
        const SizedBox(height: 16),
        TextFormField(controller: _url, keyboardType: TextInputType.url, autocorrect: false,
          decoration: InputDecoration(labelText: context.tr('sing-box JSON URL')),
          validator: (text) { final uri = Uri.tryParse(text?.trim() ?? ''); return uri != null && ['http', 'https'].contains(uri.scheme) && uri.host.isNotEmpty ? null : context.tr('sing-box JSON URL'); }),
        const SizedBox(height: 16),
        Text(context.tr('This URL must serve a sing-box JSON configuration.'), style: context.listCaptionStyle),
        ListItem.toggle(title: Text(context.tr('Auto update')), value: _auto, onChanged: (value) => setState(() => _auto = value)),
        TextFormField(controller: _interval, keyboardType: TextInputType.number,
          decoration: InputDecoration(labelText: context.tr('Update interval (minutes)')),
          validator: (text) => (int.tryParse(text ?? '') ?? 0) >= 15 ? null : context.tr('At least 15 minutes')),
      ],
      if (widget.initial['id'] != null) ...[
        const SizedBox(height: 16),
        ListItem.open(title: Text(context.tr('Open configuration')), leading: const GlyphIcon(AppGlyphs.profiles),
          widget: ProfileEditor(profile: widget.initial)),
        ListItem(title: Text(context.tr('Last updated')), subtitle: Text(localDate(widget.initial['updatedAt']))),
      ],
      const SizedBox(height: 24),
    ]))],
  );
}

class ProfileOrderPage extends StatefulWidget {
  const ProfileOrderPage({super.key});
  @override State<ProfileOrderPage> createState() => _ProfileOrderPageState();
}
class _ProfileOrderPageState extends State<ProfileOrderPage> {
  List<Json>? _profiles;
  @override Widget build(BuildContext context) {
    _profiles ??= List.of(context.box.profiles);
    return CommonScaffold(title: context.tr('Reorder'),
      confirmationAction: IconButtonData(glyph: AppGlyphs.check, tooltip: context.tr('Save'), onPressed: () async {
        if (await context.box.act('profiles.reorder', {'ids': _profiles!.map((p) => p['id']).toList()}) && context.mounted) context.safeNestedPop();
      }),
      body: ReorderableListView.builder(
        padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 24 + context.bottomInset),
        itemCount: _profiles!.length,
        onReorder: (old, next) => setState(() { if (old < next) next--; _profiles!.insert(next, _profiles!.removeAt(old)); }),
        itemBuilder: (_, index) => ListItem(key: ValueKey(_profiles![index]['id']), title: Text(_profiles![index]['name'].toString()), trailing: const GlyphIcon(AppGlyphs.sort)),
      ),
    );
  }
}

class ScanPage extends StatefulWidget {
  const ScanPage({super.key});
  @override State<ScanPage> createState() => _ScanPageState();
}
class _ScanPageState extends State<ScanPage> {
  final _camera = MobileScannerController(formats: const [BarcodeFormat.qrCode]);
  bool _complete = false;
  @override void dispose() { _camera.dispose(); super.dispose(); }
  @override Widget build(BuildContext context) => CommonScaffold(
    title: context.tr('Scan QR code'),
    iconActions: [IconButtonData(glyph: AppGlyphs.torch, tooltip: 'Flash', onPressed: _camera.toggleTorch)],
    body: Padding(padding: EdgeInsets.only(top: context.appBarInset), child: MobileScanner(
      controller: _camera,
      onDetect: (capture) {
        if (_complete) return;
        for (final code in capture.barcodes) {
          final text = code.rawValue;
          if (text == null || text.isEmpty) continue;
          _complete = true;
          context.safeNestedPop(text);
          break;
        }
      },
      errorBuilder: (context, error) => ErrorState(error, retry: _camera.start),
    )),
  );
}
