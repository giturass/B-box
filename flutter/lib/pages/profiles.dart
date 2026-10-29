import 'package:flutter/services.dart';
import 'package:mobile_scanner/mobile_scanner.dart';
import '../widgets.dart';
import '../components/form.dart';
import '../components/profile_card.dart';
import 'profile_editor.dart';
import 'profile_details.dart';

// libbox remote_profile.go stores the display name in the URL fragment.
String remoteProfileLink(Json profile) => Uri(
  scheme: 'sing-box', host: 'import-remote-profile',
  queryParameters: {'url': profile['url'].toString()},
  fragment: profile['name'].toString(),
).toString();

class ProfilesPage extends StatefulWidget {
  const ProfilesPage({super.key, this.picker = false});
  final bool picker;
  @override State<ProfilesPage> createState() => _ProfilesPageState();
}
class _ProfilesPageState extends State<ProfilesPage> {
  final _updating = <int>{};
  bool _updatingAll = false;
  Future<void> _update(Json profile) async {
    final id = (profile['id'] as num).toInt();
    if (_updating.contains(id)) return;
    setState(() => _updating.add(id));
    try { await context.box.act('profiles.update', {'id': id}); }
    finally { if (mounted) setState(() => _updating.remove(id)); }
  }
  Future<void> _updateAll() async {
    if (_updatingAll || _updating.isNotEmpty) return;
    final box = context.box;
    final profiles = List<Json>.of(box.profiles).where((p) => p['remote'] == true);
    setState(() => _updatingAll = true);
    try {
      // Native profile writes share a mutex; keep updates serialized.
      for (final profile in profiles) {
        if (!mounted || box.remote) break;
        await _update(profile);
      }
    } finally { if (mounted) setState(() => _updatingAll = false); }
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
      final content = profile['remote'] == true ? remoteProfileLink(profile) : await box.call('profiles.read', {'id': profile['id']});
      await box.call('share.text', {'content': content});
    } catch (error) { box.report(error); }
  }
  List<CommonPopupMenuItem> _menu(Json profile) => [
    CommonPopupMenuItem(glyph: AppGlyphs.edit, label: context.tr('Edit'), onPressed: () => openForm(context, ProfileDetailsPage(profile: profile))),
    if (profile['remote'] == true)
      CommonPopupMenuItem(glyph: AppGlyphs.sync, label: context.tr('Update'), onPressed: () => _update(profile)),
    CommonPopupMenuItem(glyph: AppGlyphs.code, label: context.tr('Open configuration'), onPressed: () => openPage(context, ProfileEditor(profile: profile))),
    if (profile['remote'] == true) ...[
      CommonPopupMenuItem(glyph: AppGlyphs.copy, label: context.tr('Copy link'), onPressed: () async {
        await Clipboard.setData(ClipboardData(text: profile['url'].toString()));
        if (mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(context.tr('Copied'))));
      }),
      CommonPopupMenuItem(glyph: AppGlyphs.qrCode, label: context.tr('QR code'), onPressed: () => openForm(context, BoxPage(title: 'QR code', children: [
        Center(child: QrCodeView(data: remoteProfileLink(profile))),
      ]))),
    ],
    CommonPopupMenuItem(glyph: AppGlyphs.upload, label: context.tr('Share'), onPressed: () => _share(profile)),
    CommonPopupMenuItem(glyph: AppGlyphs.export, label: context.tr('Export'), onPressed: () => _export(profile)),
    CommonPopupMenuItem(danger: true, glyph: AppGlyphs.delete, label: context.tr('Delete'), onPressed: () async {
      final box = context.box;
      if (await confirm(context, 'Delete this profile?', message: 'This cannot be undone.')) await box.act('profiles.delete', {'id': profile['id']});
    }),
  ];

  @override Widget build(BuildContext context) {
    final box = context.box;
    final profiles = box.profiles;
    return CommonScaffold(
      title: context.tr('Profiles'),
      actions: box.remote || profiles.isEmpty || widget.picker ? null : [AddProfileMenu(builder: (open) => IconButton(
        tooltip: context.tr('Add profile'), icon: const GlyphIcon(AppGlyphs.addCircle),
        onPressed: open,
      ))],
      iconActions: box.remote || widget.picker || profiles.isEmpty ? const [] : [
        IconButtonData(glyph: AppGlyphs.sync, tooltip: context.tr('Update all'), isLoading: _updatingAll || _updating.isNotEmpty,
          onPressed: _updatingAll || _updating.isNotEmpty || !profiles.any((p) => p['remote'] == true) ? null : _updateAll),
        IconButtonData(glyph: AppGlyphs.sort, tooltip: context.tr('Reorder'), onPressed: profiles.length < 2 ? null : () => openForm(context, const ProfileOrderPage())),
      ],
      body: box.remote ? NullStatus(label: context.tr('Disconnect remote session to edit local profiles'), illustration: NullStatusIllustration.profile) : NullStatusSwitcher(
        isEmpty: profiles.isEmpty,
        nullStatus: NullStatus(
          label: context.tr('No profiles yet'), illustration: NullStatusIllustration.profile,
          description: context.tr('Import a sing-box JSON configuration to get started.'),
          action: AddProfileMenu(builder: (open) => TonalButton(size: TonalButtonSize.bar, glyph: AppGlyphs.addCircle,
            label: context.tr('Add profile'), onPressed: open)),
        ),
        child: _ProfilesGrid(profiles: profiles, selectedId: box.selectedProfileId, picker: widget.picker,
          itemBuilder: (context, profile) => ProfileCard(
            key: ValueKey(profile['id']), profile: profile,
            selected: profile['id'] == box.selectedProfileId,
            updating: _updating.contains(profile['id']),
            menuItems: _menu(profile),
            onPressed: () async {
              if (await box.act('profiles.select', {'id': profile['id']}) && widget.picker && context.mounted) context.safeNestedPop();
            },
          ),
        ),
      ),
    );
  }
}

class _ProfilesGrid extends StatefulWidget {
  const _ProfilesGrid({required this.profiles, required this.selectedId, required this.picker, required this.itemBuilder});
  final List<Json> profiles;
  final int selectedId;
  final bool picker;
  final Widget Function(BuildContext context, Json profile) itemBuilder;
  @override State<_ProfilesGrid> createState() => _ProfilesGridState();
}

class _ProfilesGridState extends State<_ProfilesGrid> {
  late final _scroll = sheetScrollController(context);
  bool _revealed = false;

  @override void dispose() { _scroll.dispose(); super.dispose(); }

  @override Widget build(BuildContext context) {
    final active = PageActivityScope.isActiveOf(context);
    final spacing = (context.isMobileView ? 8.0 : 12.0).mAp(context);
    final padding = EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 16 + context.bottomInset);
    final height = profileCardHeight(context);
    return LayoutBuilder(builder: (context, constraints) {
      final columns = ((constraints.maxWidth - 32 + spacing) / (300.ap(context) + spacing)).floor().clamp(1, 3);
      if (!_revealed && !widget.picker && active && widget.profiles.isNotEmpty) {
        _revealed = true;
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (!mounted || !_scroll.hasClients) return;
          final index = widget.profiles.indexWhere((p) => p['id'] == widget.selectedId);
          if (index < 0) return;
          final position = _scroll.position;
          final top = (index ~/ columns) * (height + spacing);
          final bottom = top + padding.vertical + height - position.viewportDimension;
          if (position.pixels >= bottom && position.pixels <= top) return;
          _scroll.jumpTo(((top + bottom) / 2).clamp(position.minScrollExtent, position.maxScrollExtent));
        });
      }
      return GridView.builder(
        key: PageStorageKey(widget.picker ? 'profile-picker' : 'profiles'),
        controller: _scroll, padding: padding,
        gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(crossAxisCount: columns,
          mainAxisSpacing: spacing, crossAxisSpacing: spacing, mainAxisExtent: height),
        itemCount: widget.profiles.length,
        itemBuilder: (context, index) => widget.itemBuilder(context, widget.profiles[index]),
      );
    });
  }
}

Future<void> _importProfile(BuildContext context, String source) async {
  final box = context.box;
  final emptyClipboard = context.tr('Empty clipboard');
  try {
    Json? data;
    switch (source) {
      case 'File':
        final result = await box.call('profiles.importFile');
        if (result != null) data = json(result);
      case 'Clipboard':
        final text = (await Clipboard.getData(Clipboard.kTextPlain))?.text?.trim();
        if (text == null || text.isEmpty) throw StateError(emptyClipboard);
        data = await decodeImport(box, text);
      case 'Scan QR code':
        data = await openPage<Json>(context, const ScanPage());
    }
    if (data != null && context.mounted) await openForm(context, ProfileForm(initial: data));
  } catch (error) { box.report(error); }
}

class AddProfileMenu extends StatefulWidget {
  const AddProfileMenu({super.key, required this.builder});
  final Widget Function(PopupOpen open) builder;
  @override State<AddProfileMenu> createState() => _AddProfileMenuState();
}

class _AddProfileMenuState extends State<AddProfileMenu> {
  bool _busy = false;
  Future<void> _import(String source) async {
    if (_busy) return;
    setState(() => _busy = true);
    try { await _importProfile(context, source); }
    finally { if (mounted) setState(() => _busy = false); }
  }
  @override Widget build(BuildContext context) => AbsorbPointer(absorbing: _busy, child: CommonPopupBox(
    targetBuilder: widget.builder,
    popupBuilder: (_) => CommonPopupMenu(items: [
      CommonPopupMenuItem(glyph: AppGlyphs.qrCode, label: context.tr('Scan QR code'), onPressed: () => _import('Scan QR code')),
      CommonPopupMenuItem(glyph: AppGlyphs.importFile, label: context.tr('File'), onPressed: () => _import('File')),
      CommonPopupMenuItem(glyph: AppGlyphs.cloudDownload, label: context.tr('Import from URL'),
        onPressed: () => openForm(context, const ProfileForm(initial: {'url': '', 'remote': true}))),
      CommonPopupMenuItem(glyph: AppGlyphs.copy, label: context.tr('Clipboard'), onPressed: () => _import('Clipboard')),
      CommonPopupMenuItem(section: const CommonPopupMenuSection(), glyph: AppGlyphs.customize,
        label: context.tr('New profile'), onPressed: () => openForm(context, const ProfileForm(initial: {'content': '{}'}))),
    ]),
  ));
}

class AddProfilePage extends StatefulWidget {
  const AddProfilePage({super.key});
  @override State<AddProfilePage> createState() => _AddProfilePageState();
}
class _AddProfilePageState extends State<AddProfilePage> {
  bool _loading = false;
  Future<void> _import(String source) async {
    if (_loading) return;
    setState(() => _loading = true);
    try { await _importProfile(context, source); }
    finally { if (mounted) setState(() => _loading = false); }
  }
  @override Widget build(BuildContext context) => BoxPage(
    title: 'Add profile', loading: _loading,
    children: [
      generateSection(items: [
        ListItem.open(leading: const GlyphIcon(AppGlyphs.link), title: Text(context.tr('Import from URL')),
          widget: const ProfileForm(initial: {'url': '', 'remote': true}), forceFull: false),
        for (final item in [('File', AppGlyphs.profiles), ('Clipboard', AppGlyphs.copy), ('Scan QR code', AppGlyphs.qrCode)])
          ListItem(leading: GlyphIcon(item.$2), title: Text(context.tr(item.$1)), onTap: _loading ? null : () => _import(item.$1)),
        ListItem.open(leading: const GlyphIcon(AppGlyphs.add), title: Text(context.tr('New profile')),
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
  late final _name = TextEditingController(text: widget.initial['name'] as String? ?? '');
  late final _url = TextEditingController(text: widget.initial['url'] as String? ?? '');
  late final _interval = TextEditingController(text: (widget.initial['interval'] ?? 60).toString());
  late bool _remote = widget.initial['remote'] == true || _url.text.isNotEmpty;
  late bool _auto = widget.initial['autoUpdate'] != false;
  bool _saving = false, _attempted = false;

  ({String field, String message})? get _issue {
    if (_name.text.trim().isEmpty) return (field: 'name', message: context.tr('Name') + ': ' + context.tr('Required'));
    if (!_remote) return null;
    final uri = Uri.tryParse(_url.text.trim());
    if (uri == null || !['http', 'https'].contains(uri.scheme) || uri.host.isEmpty) {
      return (field: 'url', message: context.tr('sing-box JSON URL'));
    }
    if (_auto && (int.tryParse(_interval.text) ?? 0) < 15) {
      return (field: 'interval', message: context.tr('At least 15 minutes'));
    }
    return null;
  }
  void _changed(String _) { if (_attempted) setState(() {}); }

  @override void dispose() { _name.dispose(); _url.dispose(); _interval.dispose(); super.dispose(); }
  Future<void> _save() async {
    if (_saving) return;
    if (_issue != null) { setState(() => _attempted = true); return; }
    final box = context.box;
    setState(() => _saving = true);
    try {
      final interval = int.tryParse(_interval.text);
      final previousInterval = (widget.initial['interval'] as num?)?.toInt() ?? 60;
      await box.call('profiles.save', {
        ...widget.initial, 'name': _name.text.trim(), 'url': _remote ? _url.text.trim() : '',
        'autoUpdate': _remote && _auto,
        'interval': interval != null && interval >= 15 ? interval : previousInterval >= 15 ? previousInterval : 60,
      });
      if (mounted) context.safeNestedPop();
    } catch (error) { box.report(error); }
    if (mounted) setState(() => _saving = false);
  }
  @override Widget build(BuildContext context) {
    final issue = _attempted ? _issue : null;
    return FormPage(
      title: widget.initial['id'] == null ? 'Add profile' : 'Edit',
      saving: _saving, onSave: _save, error: issue?.message,
      children: [
        generateSection(items: [
          FormRow(title: context.tr('Name'), invalid: issue?.field == 'name',
            trailing: FormRowTextField(controller: _name, hintText: context.tr('Required'),
              textInputAction: TextInputAction.next, onChanged: _changed)),
          FormRow(title: context.tr('Remote'), onPressed: () => setState(() => _remote = !_remote),
            trailing: CommonSwitch(value: _remote, onChanged: (value) => setState(() => _remote = value))),
          if (_remote) FormRow(title: context.tr('URL'), invalid: issue?.field == 'url',
            trailing: FormRowTextField(controller: _url, hintText: context.tr('Required'),
              keyboardType: TextInputType.url, textInputAction: TextInputAction.next,
              autocorrect: false, enableSuggestions: false, onChanged: _changed)),
        ], footer: _remote ? context.tr('This URL must serve a sing-box JSON configuration.') : null),
        if (_remote) generateSection(items: [
          FormRow(title: context.tr('Auto update'), onPressed: () => setState(() => _auto = !_auto),
            trailing: CommonSwitch(value: _auto, onChanged: (value) => setState(() => _auto = value))),
          if (_auto) FormRow(title: context.tr('Update interval'), invalid: issue?.field == 'interval',
            trailing: Row(mainAxisSize: MainAxisSize.min, spacing: 4, children: [
              Flexible(child: FormRowTextField(controller: _interval, hintText: context.tr('Required'),
                keyboardType: TextInputType.number, textInputAction: TextInputAction.done,
                inputFormatters: [FilteringTextInputFormatter.digitsOnly], onChanged: _changed)),
              Text(context.tr('minutes'), style: AppTextStyle.subheadline.of(context)),
            ])),
        ], footer: _auto ? context.tr('At least 15 minutes') : null),
        if (widget.initial['id'] != null) generateSection(items: [
          ListItem.open(title: Text(context.tr('Open configuration')), leading: const GlyphIcon(AppGlyphs.code),
            widget: ProfileEditor(profile: widget.initial)),
          if (_remote) ListItem(title: Text(context.tr('Last updated')), subtitle: Text(localDate(widget.initial['updatedAt']))),
        ]),
      ],
    );
  }
}

class ProfileOrderPage extends StatefulWidget {
  const ProfileOrderPage({super.key});
  @override State<ProfileOrderPage> createState() => _ProfileOrderPageState();
}
class _ProfileOrderPageState extends State<ProfileOrderPage> {
  List<Json>? _profiles;
  bool _saving = false;
  Widget _item(int index) => ReorderableDelayedDragStartListener(
    key: ValueKey(_profiles![index]['id']), index: index,
    child: ItemPositionProvider(position: ItemPosition.get(index, _profiles!.length),
      child: DecorationListItem(contentPadding: const EdgeInsets.only(left: 16),
        title: Text(_profiles![index]['name'].toString()), trailing: SortHandle(index: index))),
  );
  @override Widget build(BuildContext context) {
    _profiles ??= List.of(context.box.profiles);
    return CommonScaffold(title: context.tr('Reorder'),
      confirmationAction: IconButtonData(glyph: AppGlyphs.check, tooltip: context.tr('Save'), isLoading: _saving, onPressed: _saving ? null : () async {
        setState(() => _saving = true);
        final saved = await context.box.act('profiles.reorder', {'ids': _profiles!.map((p) => p['id']).toList()});
        if (!context.mounted) return;
        if (saved) context.safeNestedPop();
        else setState(() => _saving = false);
      }),
      body: AbsorbPointer(absorbing: _saving, child: ReorderableListView.builder(
        buildDefaultDragHandles: false,
        padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 32 + context.bottomInset),
        itemCount: _profiles!.length,
        proxyDecorator: (child, index, animation) => commonProxyDecorator(_item(index), index, animation),
        onReorderItem: (old, next) => setState(() => _profiles!.insert(next, _profiles!.removeAt(old))),
        itemBuilder: (_, index) => _item(index),
      )),
    );
  }
}

class ScanPage extends StatefulWidget {
  const ScanPage({super.key});
  @override State<ScanPage> createState() => _ScanPageState();
}
class _ScanPageState extends State<ScanPage> {
  final _camera = MobileScannerController(formats: const [BarcodeFormat.qrCode]);
  final _session = DateTime.now().microsecondsSinceEpoch.toString();
  BoxController? _box;
  bool _complete = false, _busy = false;
  int _decoded = 0, _total = 0;
  String? _error;
  @override void didChangeDependencies() { super.didChangeDependencies(); _box ??= context.box; }
  @override void dispose() {
    _box?.act('profiles.scan.close', {'session': _session});
    _camera.dispose();
    super.dispose();
  }

  Future<void> _scan(String text) async {
    if (_busy || _complete) return;
    _busy = true;
    try {
      final result = json(await _box!.call('profiles.scan', {'session': _session, 'text': text}));
      if (!mounted) return;
      final profile = result['profile'] is Map ? json(result['profile'])
          : result['text'] is String ? await decodeImport(_box!, result['text'] as String) : null;
      if (!mounted) return;
      if (profile != null) { _complete = true; context.safeNestedPop(profile); }
      else { setState(() { _decoded = (result['decoded'] as num).toInt(); _total = (result['total'] as num).toInt(); _error = null; }); }
    } catch (error) { if (mounted) setState(() => _error = BoxController.describe(error)); }
    finally { _busy = false; }
  }
  @override Widget build(BuildContext context) => CommonScaffold(
    title: context.tr('Scan QR code'),
    iconActions: [IconButtonData(glyph: AppGlyphs.torch, tooltip: 'Flash', onPressed: _camera.toggleTorch)],
    body: Padding(padding: EdgeInsets.only(top: context.appBarInset), child: Column(children: [Expanded(child: MobileScanner(
      controller: _camera,
      onDetect: (capture) {
        if (_complete || _busy) return;
        for (final code in capture.barcodes) {
          final text = code.rawValue;
          if (text == null || text.isEmpty) continue;
          _scan(text);
          break;
        }
      },
      errorBuilder: (context, error) => ErrorState(error, retry: _camera.start),
    )),
      if (_total > 0) Padding(padding: const EdgeInsets.all(16), child: Column(children: [
        LinearProgressIndicator(value: _decoded / _total),
        Text('${context.tr('Scanning configuration')} $_decoded / $_total'),
      ])),
      if (_error != null) Padding(padding: const EdgeInsets.all(16), child: Text(_error!)),
    ])),
  );
}
