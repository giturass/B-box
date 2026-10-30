import 'package:collection/collection.dart';
import 'package:flutter/services.dart';
import '../widgets.dart';

class AccessPage extends StatefulWidget {
  const AccessPage({super.key});
  @override State<AccessPage> createState() => _AccessPageState();
}

class _AccessPageState extends State<AccessPage> {
  Future<List<Json>>? _apps;
  final _scroll = ScrollController();
  Json? _draft, _saved;
  List<Json>? _displaySource, _displayApps;
  Set<String>? _displaySelection;
  Set<String> _pinned = {};
  String _query = '', _sort = 'Default';
  bool _system = false, _nonInternet = false, _selectedOnly = false, _saving = false, _scanning = false;
  bool get _enabled => _draft?['perAppEnabled'] == true;
  bool get _managed => _draft?['perAppManaged'] == true;
  Set<String> get _selected => ((_managed ? context.box.settings['perAppManagedList'] : _draft?['perAppList']) as List? ?? []).cast<String>().toSet();
  bool get _hasChanges => !const DeepCollectionEquality().equals(_draft, _saved);

  @override void didChangeDependencies() {
    super.didChangeDependencies();
    if (_draft == null) {
      final settings = context.box.settings;
      _draft = {'perAppEnabled': settings['perAppEnabled'] == true, 'perAppManaged': settings['perAppManaged'] == true,
        'perAppMode': (settings['perAppMode'] as num?)?.toInt() == 2 ? 2 : 1,
        'perAppList': List<String>.of((settings['perAppList'] as List? ?? []).cast<String>())};
      _saved = Map.of(_draft!);
      _pinned = _selected;
    }
    _apps ??= _load(refresh: true);
  }

  Future<List<Json>> _load({bool refresh = false}) async => rows(await context.box.call('apps.list', {
    'refresh': refresh, 'system': _system, 'nonInternet': _nonInternet,
  }));

  void _reload() {
    context.box.appIcons.clear();
    setState(() => _apps = _load(refresh: true));
  }

  void _filter(VoidCallback change, {bool sourceChanged = false}) {
    setState(() {
      change();
      _displayApps = null;
      if (sourceChanged) _apps = _load();
    });
  }

  List<Json> _display(List<Json> source, Set<String> selected) {
    if (identical(source, _displaySource) && _displayApps != null &&
        (!_selectedOnly || const SetEquality<String>().equals(selected, _displaySelection))) return _displayApps!;
    final words = _query.toLowerCase().trim().split(RegExp(r'\s+'));
    final apps = source.where((app) =>
      (_system || app['system'] != true) && (_nonInternet || app['internet'] != false) &&
      (!_selectedOnly || selected.contains(app['package'])) &&
      words.every('${app['name']} ${app['package']}'.toLowerCase().contains),
    ).toList();
    apps.sort((a, b) {
      final pinned = (_pinned.contains(b['package']) ? 1 : 0) - (_pinned.contains(a['package']) ? 1 : 0);
      if (pinned != 0) return pinned;
      if (_sort == 'Time') {
        final time = (b['updatedAt'] as num? ?? 0).compareTo(a['updatedAt'] as num? ?? 0);
        if (time != 0) return time;
      }
      final aName = a['name'].toString(), bName = b['name'].toString();
      final name = _sort == 'Name' ? aName.compareTo(bName) : aName.toLowerCase().compareTo(bName.toLowerCase());
      return name != 0 ? name : a['package'].toString().compareTo(b['package'].toString());
    });
    _displaySource = source;
    _displaySelection = selected;
    return _displayApps = apps;
  }

  Future<void> _rescan() async {
    if (_scanning || _saving || _hasChanges) return;
    final box = context.box;
    setState(() => _scanning = true);
    try {
      final settings = json(await box.call('apps.rescan'));
      if (mounted) setState(() => box.settings = settings);
    } catch (error) {
      box.report(error);
    } finally {
      if (mounted) setState(() => _scanning = false);
    }
  }
  @override void dispose() { _scroll.dispose(); super.dispose(); }

  void _set(String key, dynamic value) { if (!_saving) setState(() => _draft![key] = value); }
  void _toggle(String package) {
    if (!_enabled || _managed || _saving) return;
    final next = _selected;
    next.contains(package) ? next.remove(package) : next.add(package);
    _set('perAppList', next.toList()..sort());
  }

  Future<bool> _save() async {
    if (_saving) return false;
    setState(() => _saving = true);
    final values = Map<String, dynamic>.of(_draft!);
    final saved = await context.box.setSettings(values);
    if (mounted) setState(() { _saving = false; if (saved) _saved = values; });
    return saved;
  }

  Future<bool> _back(BuildContext context) async {
    if (_saving) return false;
    final decision = await showDialog<String>(context: context, builder: (context) => AlertDialog(
      title: Text(context.tr('Unsaved changes')),
      actions: [
        TextButton(onPressed: () => Navigator.pop(context), child: Text(context.tr('Cancel'))),
        TextButton(onPressed: () => Navigator.pop(context, 'discard'), child: Text(context.tr('Discard'))),
        TextButton(onPressed: () => Navigator.pop(context, 'save'), child: Text(context.tr('Save'))),
      ],
    ));
    return decision == 'discard' || decision == 'save' && await _save();
  }

  Future<void> _import() async {
    if (_managed) return;
    final text = (await Clipboard.getData(Clipboard.kTextPlain))?.text;
    if (text != null && mounted) _set('perAppList', text.split(RegExp(r'\s+')).where((value) => value.isNotEmpty).toSet().toList()..sort());
  }

  Future<void> _querySettings({bool retry = false}) async {
    final box = context.box;
    final previousMode = box.settings['queryMode'];
    var permissionRequested = false;
    Future<void>? pendingMode;
    await openForm(context, BoxPage(title: 'Application query', children: [
      ListItem.options(title: Text(context.tr('Application query')), options: const ['SHIZUKU', 'ROOT'],
        value: box.settings['queryMode'] as String? ?? 'SHIZUKU', textBuilder: (value) => value == 'ROOT' ? 'Root' : 'Shizuku',
        onChanged: (value) async { await (pendingMode = box.setSetting('queryMode', value, restart: false)); }),
      ListItem(title: Text(context.tr('Grant Shizuku permission')), onTap: () {
        permissionRequested = true;
        return box.act('apps.permission');
      }),
    ]));
    await pendingMode;
    if (mounted && (retry || permissionRequested || box.settings['queryMode'] != previousMode)) _reload();
  }

  CommonPopupMenuItem _option(String label, String value, VoidCallback change, {bool checked = false, CommonPopupMenuSection? section}) =>
    CommonPopupMenuItem(label: context.tr(label), checked: checked, section: section, onPressed: change);

  @override Widget build(BuildContext context) => FutureBuilder<List<Json>>(
    future: _apps,
    builder: (context, snapshot) {
      final selected = _selected;
      final apps = _display(snapshot.data ?? const <Json>[], selected);
      final allSelected = apps.isNotEmpty && apps.every((app) => selected.contains(app['package']));
      final selectAll = _enabled && !_managed && apps.isNotEmpty ? IconButtonData(
        glyph: allSelected ? AppGlyphs.deselect : AppGlyphs.selectAll,
        tooltip: context.tr(allSelected ? 'Deselect all' : 'Select all'),
        onPressed: _saving ? null : () {
          final next = Set<String>.of(selected), visible = apps.map((app) => app['package'] as String);
          allSelected ? next.removeAll(visible) : next.addAll(visible);
          _set('perAppList', next.toList()..sort());
        },
      ) : null;
      final mode = _managed ? 1 : _draft!['perAppMode'];
      return CommonPopScope(
        onPop: _hasChanges || _saving ? _back : null,
        child: CommonScaffold(
          title: context.tr('Access control'), isLoading: _saving || _scanning,
          searchInBar: true,
          searchState: AppBarSearchState(onSearch: (query) => _filter(() { _query = query; _pinned = _selected; })),
          searchActions: [if (selectAll != null) selectAll],
          iconActions: [if (selectAll != null) selectAll],
          confirmationAction: _hasChanges ? IconButtonData(glyph: AppGlyphs.check, tooltip: context.tr('Save'), onPressed: _saving ? null : _save) : null,
          menuItems: [
            CommonPopupMenuItem(glyph: AppGlyphs.toggleOn, label: context.tr('Turn on'), checked: _enabled,
              section: CommonPopupMenuSection(title: context.tr('Access control')), onPressed: () => _set('perAppEnabled', true)),
            CommonPopupMenuItem(glyph: AppGlyphs.toggle, label: context.tr('Turn off'), checked: !_enabled, onPressed: () => _set('perAppEnabled', false)),
            CommonPopupMenuItem(glyph: AppGlyphs.sliders, label: context.tr('Settings'), subtitle: context.tr(mode == 2 ? 'Proxy selected apps' : 'Bypass selected apps'),
              section: const CommonPopupMenuSection(), subItems: [
                for (final value in [2, 1]) _option(value == 2 ? 'Proxy selected apps' : 'Bypass selected apps', '$value', () => _set('perAppMode', value),
                  checked: mode == value, section: value == 2 ? CommonPopupMenuSection(title: context.tr('Mode')) : null),
                for (final sort in ['Default', 'Name', 'Time']) _option(sort, sort, () => _filter(() => _sort = sort), checked: sort == _sort,
                  section: sort == 'Default' ? CommonPopupMenuSection(title: context.tr('Sort')) : null),
                _option('Show system apps', '', () => _filter(() => _system = !_system, sourceChanged: true), checked: _system,
                  section: CommonPopupMenuSection(title: context.tr('Source'))),
                _option('Show apps without Internet permission', '', () => _filter(() => _nonInternet = !_nonInternet, sourceChanged: true), checked: _nonInternet),
                _option('Selected apps', '', () => _filter(() => _selectedOnly = !_selectedOnly), checked: _selectedOnly),
                _option('Managed app list', '', () => _set('perAppManaged', !_managed), checked: _managed),
              ]),
            CommonPopupMenuItem(glyph: AppGlyphs.moreCircle, label: context.tr('Action'), subItems: [
              CommonPopupMenuItem(glyph: AppGlyphs.copy, label: context.tr('Clipboard export'), onPressed: () => Clipboard.setData(ClipboardData(text: selected.join('\n')))),
              if (!_managed) CommonPopupMenuItem(glyph: AppGlyphs.paste, label: context.tr('Clipboard import'), onPressed: _import),
              CommonPopupMenuItem(glyph: AppGlyphs.refresh, label: context.tr('Refresh'), onPressed: _reload),
              if (_managed) CommonPopupMenuItem(glyph: AppGlyphs.appsList, label: context.tr('Rescan managed apps'),
                onPressed: _scanning || _saving || _hasChanges ? null : _rescan),
              CommonPopupMenuItem(glyph: AppGlyphs.lockOpen, label: context.tr('Application query'), onPressed: _querySettings),
            ]),
          ],
          body: FloatingFootBar(
            bar: Padding(padding: EdgeInsetsDirectional.fromSTEB(16, 6, _enabled ? 16 : 8, 6), child: Row(spacing: 12, children: [
              GlyphIcon(_enabled ? AppGlyphs.appsList : AppGlyphs.info, size: 20, color: context.colorScheme.onSurfaceVariant),
              Expanded(child: Text(context.tr(!_enabled ? 'Access control is disabled' : _managed ? 'Disable managed app list to edit selection'
                : mode == 2 ? 'Only selected apps use the VPN' : 'Selected apps bypass the VPN'), maxLines: 2, overflow: TextOverflow.ellipsis, style: AppTextStyle.footnote.of(context))),
              if (_enabled) TonalChip(label: '${context.tr('Selected')} ${selected.length}', color: context.colorScheme.primary.opacity15, foregroundColor: context.colorScheme.primary)
              else TonalButton(size: TonalButtonSize.compact, label: context.tr('Turn on'), onPressed: () => _set('perAppEnabled', true)),
            ])),
            body: snapshot.hasError ? NullStatus(
              label: context.tr('Application query failed'), description: BoxController.describe(snapshot.error!), illustration: NullStatusIllustration.permission,
              action: TonalButton(size: TonalButtonSize.bar, glyph: AppGlyphs.lockOpen, label: context.tr('Application query'), onPressed: () => _querySettings(retry: true)),
            ) : !snapshot.hasData ? const Center(child: CommonCircleLoading.page())
              : NullStatusSwitcher(isEmpty: apps.isEmpty, isSearching: _query.trim().isNotEmpty,
                  nullStatus: NullStatus(label: context.tr('No results'), illustration: NullStatusIllustration.apps),
                  child: DisabledMask(status: !_enabled, child: CommonScrollBar(controller: _scroll, child: ListView.builder(
                    controller: _scroll, itemCount: apps.length,
                    itemExtent: 72 * MediaQuery.textScalerOf(context).scale(1).clamp(1.0, 1.4),
                    padding: EdgeInsets.only(top: context.contentTopPadding, bottom: context.bottomInset),
                    itemBuilder: (context, index) {
                      final app = apps[index], package = app['package'] as String;
                      return ListItem.checkbox(
                        key: ValueKey(package), leading: PackageIcon(package: package),
                        title: Text(app['name'].toString(), maxLines: 1, overflow: TextOverflow.ellipsis),
                        subtitle: Text(package, maxLines: 1, overflow: TextOverflow.ellipsis),
                        value: selected.contains(package), onChanged: _enabled && !_managed && !_saving ? (_) => _toggle(package) : null,
                      );
                    },
                  ))),
                ),
          ),
        ),
      );
    },
  );
}

class PackageIcon extends StatelessWidget {
  const PackageIcon({super.key, required this.package});
  final String package;
  @override Widget build(BuildContext context) => SizedBox.square(dimension: 48, child: FutureBuilder<Uint8List?>(
    future: context.box.appIcon(package),
    builder: (context, snapshot) => snapshot.data == null ? GlyphIcon(AppGlyphs.appsList, size: 32, color: context.colorScheme.onSurfaceVariant)
      : Image.memory(snapshot.data!, width: 48, height: 48, gaplessPlayback: true),
  ));
}
