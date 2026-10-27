import 'dart:io';
import 'dart:typed_data';
import 'package:flutter/services.dart';
import '../widgets.dart';
import 'terminal.dart';

class TerminalSettingsPage extends StatefulWidget {
  const TerminalSettingsPage({super.key});
  @override State<TerminalSettingsPage> createState() => _TerminalSettingsPageState();
}
class _TerminalSettingsPageState extends State<TerminalSettingsPage> {
  Json? _settings;
  String? _error;
  bool _saving = false;
  @override void initState() { super.initState(); WidgetsBinding.instance.addPostFrameCallback((_) => _load()); }
  Future<void> _load() async {
    try {
      final settings = json(await context.box.call('terminal.settings'));
      if (mounted) setState(() { _settings = settings; _error = null; });
    } catch (error) { if (mounted) setState(() => _error = BoxController.describe(error)); }
  }
  Future<void> _set(String key, dynamic value) async {
    if (_saving) return;
    setState(() => _saving = true);
    try {
      final settings = json(await context.box.call('terminal.set', {'key': key, 'value': value}));
      if (mounted) setState(() => _settings = settings);
    } catch (error) { if (mounted) context.box.report(error); }
    if (mounted) setState(() => _saving = false);
  }
  Future<void> _open(Widget page) async { await openPage(context, page); if (mounted) await _load(); }
  @override Widget build(BuildContext context) {
    final settings = _settings;
    return BoxPage(title: 'Ghostty settings', loading: _saving || settings == null && _error == null, children: [
      if (_error != null) ErrorState(_error!, retry: _load),
      if (settings != null) ...[
        generateSection(items: [ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Terminal preview')),
          widget: const TerminalPreviewPage())]),
        for (final dark in [false, true]) generateSection(title: context.tr(dark ? 'Dark configuration' : 'Light configuration'), items: [
          ListItem(title: Text(context.tr('Theme')), subtitle: Text(settings[dark ? 'darkTheme' : 'lightTheme'] == ''
              ? context.tr('Custom configuration') : settings[dark ? 'darkTheme' : 'lightTheme'] as String? ?? ''),
            trailing: const GlyphIcon(AppGlyphs.chevronForward), onTap: () => _open(TerminalThemePage(dark: dark))),
          ListItem.toggle(title: Text(context.tr('Custom configuration')), value: settings[dark ? 'darkTheme' : 'lightTheme'] == '',
            onChanged: _saving ? null : (enabled) => _set(dark ? 'darkTheme' : 'lightTheme', enabled ? '' : settings[dark ? 'defaultDarkTheme' : 'defaultLightTheme'])),
          if (settings[dark ? 'darkTheme' : 'lightTheme'] == '') ListItem(title: Text(context.tr('Edit Ghostty configuration')),
            trailing: const GlyphIcon(AppGlyphs.chevronForward),
            onTap: () => _open(TerminalConfigEditor(dark: dark, initial: settings[dark ? 'darkConfig' : 'lightConfig'] as String? ?? ''))),
        ]),
        generateSection(title: context.tr('Font'), items: [
          ListItem.toggle(title: Text(context.tr('Font follows theme')), value: settings['fontFollowTheme'] != false,
            onChanged: _saving ? null : (value) => _set('fontFollowTheme', value)),
          if (settings['fontFollowTheme'] == false) ListItem(title: Text(context.tr('Font family')),
            subtitle: Text((settings['fontPath'] as String? ?? '').isNotEmpty ? File(settings['fontPath'] as String).uri.pathSegments.last
              : (settings['fontFamily'] as String? ?? '').isNotEmpty ? settings['fontFamily'] as String : 'monospace'),
            trailing: const GlyphIcon(AppGlyphs.chevronForward), onTap: () => _open(const TerminalFontPage())),
          ListItem(title: Text(context.tr('Font size')), trailing: Row(mainAxisSize: MainAxisSize.min, children: [
            IconButton(tooltip: context.tr('Decrease'), icon: const GlyphIcon(AppGlyphs.remove), onPressed: _saving || (settings['fontSize'] as num? ?? 14) <= 8
              ? null : () => _set('fontSize', (settings['fontSize'] as num? ?? 14).toInt() - 1)),
            Text('${settings['fontSize'] ?? 14}', style: AppTextStyle.body.of(context).toTabular),
            IconButton(tooltip: context.tr('Increase'), icon: const GlyphIcon(AppGlyphs.add), onPressed: _saving || (settings['fontSize'] as num? ?? 14) >= 48
              ? null : () => _set('fontSize', (settings['fontSize'] as num? ?? 14).toInt() + 1)),
          ])),
        ]),
      ],
    ]);
  }
}

class TerminalThemePage extends StatefulWidget {
  const TerminalThemePage({super.key, required this.dark});
  final bool dark;
  @override State<TerminalThemePage> createState() => _TerminalThemePageState();
}
class _TerminalThemePageState extends State<TerminalThemePage> {
  List<Json>? _themes;
  String? _selected, _error;
  String _query = '';
  @override void initState() { super.initState(); WidgetsBinding.instance.addPostFrameCallback((_) => _load()); }
  Future<void> _load() async {
    try {
      final box = context.box;
      final settings = json(await box.call('terminal.settings'));
      final themes = rows(await box.call('terminal.themes', {'dark': widget.dark}));
      if (mounted) setState(() { _themes = themes; _selected = settings[widget.dark ? 'darkTheme' : 'lightTheme'] as String?; _error = null; });
    } catch (error) { if (mounted) setState(() => _error = BoxController.describe(error)); }
  }
  @override Widget build(BuildContext context) {
    final themes = (_themes ?? []).where((theme) => theme['name'].toString().toLowerCase().contains(_query.toLowerCase())).toList();
    return CommonScaffold(title: context.tr(widget.dark ? 'Dark theme' : 'Light theme'), isLoading: _themes == null && _error == null,
      searchState: AppBarSearchState(onSearch: (query) => setState(() => _query = query)),
      body: _error != null ? ErrorState(_error!, retry: _load) : ListView.builder(
        padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 24 + context.bottomInset), itemCount: themes.length,
        itemBuilder: (context, index) {
          final theme = themes[index];
          final background = Color((theme['background'] as num?)?.toInt() ?? (widget.dark ? 0xff000000 : 0xffffffff));
          final foreground = Color((theme['foreground'] as num?)?.toInt() ?? (widget.dark ? 0xffffffff : 0xff000000));
          return Padding(padding: const EdgeInsets.only(bottom: 8), child: DecorationListItem(
            isSelected: _selected == theme['name'], title: Text(theme['name'].toString()),
            trailing: _selected == theme['name'] ? const SelectionCheck() : null,
            subtitle: Container(margin: const EdgeInsets.only(top: 8), padding: const EdgeInsets.all(12),
              decoration: ShapeDecoration(color: background, shape: AppShape.md),
              child: Column(crossAxisAlignment: CrossAxisAlignment.start, spacing: 8, children: [
                Text('user@b-box ~\n❯ echo Ghostty', style: TextStyle(fontFamily: 'JetBrainsMono', color: foreground, fontSize: 12)),
                Row(children: [for (final color in (theme['palette'] as List? ?? []).take(8)) Expanded(child: Container(height: 8,
                  color: color == null ? foreground : Color((color as num).toInt())))]),
              ])),
            onPressed: () async {
              if (await context.box.act('terminal.set', {'key': widget.dark ? 'darkTheme' : 'lightTheme', 'value': theme['name']}) && context.mounted) context.safeNestedPop();
            },
          ));
        }),
    );
  }
}

class TerminalFontPage extends StatefulWidget {
  const TerminalFontPage({super.key});
  @override State<TerminalFontPage> createState() => _TerminalFontPageState();
}
class _TerminalFontPageState extends State<TerminalFontPage> {
  Json? _fonts, _settings;
  String? _error;
  final _loaded = <String>{};
  @override void initState() { super.initState(); WidgetsBinding.instance.addPostFrameCallback((_) => _load()); }
  Future<void> _load() async {
    try {
      final box = context.box, fonts = json(await context.box.call('terminal.fonts'));
      final settings = json(await box.call('terminal.settings'));
      for (final font in rows(fonts['imported'])) {
        final path = font['path'] as String;
        if (_loaded.add(path)) {
          final data = await File(path).readAsBytes();
          await (FontLoader('ghostty:$path')..addFont(Future.value(ByteData.sublistView(data)))).load();
        }
      }
      if (mounted) setState(() { _fonts = fonts; _settings = settings; _error = null; });
    } catch (error) { if (mounted) setState(() => _error = BoxController.describe(error)); }
  }
  Future<void> _select(String key, String value) async {
    if (await context.box.act('terminal.set', {'key': key, 'value': value}) && mounted) await _load();
  }
  @override Widget build(BuildContext context) => BoxPage(title: 'Font', loading: _fonts == null && _error == null,
    actions: [IconButtonData(glyph: AppGlyphs.add, tooltip: context.tr('Import font'), onPressed: () async {
      await context.box.act('terminal.importFont'); if (mounted) await _load();
    })], children: [
      if (_error != null) ErrorState(_error!, retry: _load),
      generateSection(title: context.tr('System fonts'), items: [
        for (final font in (_fonts?['system'] as List? ?? [])) DecorationListItem(
          title: Text(font.toString(), style: TextStyle(fontFamily: font.toString())),
          isSelected: _settings?['fontFamily'] == font && _settings?['fontPath'] == '',
          onPressed: () => _select('fontFamily', font.toString())),
      ]),
      generateSection(title: context.tr('Imported fonts'), items: [
        for (final font in rows(_fonts?['imported'])) DecorationListItem(
          title: Text(font['name'].toString(), style: TextStyle(fontFamily: 'ghostty:${font['path']}')),
          isSelected: _settings?['fontPath'] == font['path'], onPressed: () => _select('fontPath', font['path'] as String),
          trailing: IconButton(tooltip: context.tr('Delete'), icon: const GlyphIcon(AppGlyphs.delete), onPressed: () async {
            final box = context.box;
            if (await confirm(context, 'Delete this font?')) { await box.act('terminal.deleteFont', {'path': font['path']}); if (mounted) await _load(); }
          })),
      ]),
    ]);
}

class TerminalConfigEditor extends StatefulWidget {
  const TerminalConfigEditor({super.key, required this.dark, required this.initial});
  final bool dark;
  final String initial;
  @override State<TerminalConfigEditor> createState() => _TerminalConfigEditorState();
}
class _TerminalConfigEditorState extends State<TerminalConfigEditor> {
  late final _text = TextEditingController(text: widget.initial)..addListener(_changed);
  final _undo = UndoHistoryController();
  bool _saving = false, _allowPop = false;
  String? _saved;
  bool get _dirty => _text.text != (_saved ?? widget.initial);
  void _changed() { if (mounted) setState(() {}); }
  Future<void> _back() async {
    if (_saving || _dirty && !await confirm(context, 'Discard changes?', action: 'Discard')) return;
    if (mounted) { setState(() => _allowPop = true); WidgetsBinding.instance.addPostFrameCallback((_) { if (mounted) context.safeNestedPop(); }); }
  }
  Future<void> _save() async {
    setState(() => _saving = true);
    if (await context.box.act('terminal.set', {'key': widget.dark ? 'darkConfig' : 'lightConfig', 'value': _text.text}) && mounted) {
      setState(() => _saved = _text.text);
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(context.tr('Done'))));
    }
    if (mounted) setState(() => _saving = false);
  }
  @override void dispose() { _text.dispose(); _undo.dispose(); super.dispose(); }
  @override Widget build(BuildContext context) => PopScope(canPop: _allowPop || !_dirty,
    onPopInvokedWithResult: (didPop, _) { if (!didPop) _back(); },
    child: CommonScaffold(title: context.tr(widget.dark ? 'Dark configuration' : 'Light configuration'), backAction: _back, isLoading: _saving,
      confirmationAction: IconButtonData(glyph: AppGlyphs.check, tooltip: context.tr('Save'), onPressed: _saving ? null : _save),
      iconActions: [IconButtonData(glyph: AppGlyphs.undo, tooltip: context.tr('Undo'), onPressed: _undo.undo),
        IconButtonData(glyph: AppGlyphs.redo, tooltip: context.tr('Redo'), onPressed: _undo.redo)],
      body: Padding(padding: EdgeInsets.only(top: context.appBarInset, bottom: MediaQuery.paddingOf(context).bottom), child: Column(children: [
        Padding(padding: const EdgeInsets.all(16), child: Text(context.tr('Supported Ghostty options: foreground, background, cursor-color, selection-background, selection-foreground, palette, font-family.'), style: context.listCaptionStyle)),
        Expanded(child: TextField(controller: _text, undoController: _undo, expands: true, maxLines: null, minLines: null, readOnly: _saving,
          autocorrect: false, enableSuggestions: false, smartDashesType: SmartDashesType.disabled, smartQuotesType: SmartQuotesType.disabled,
          textAlignVertical: TextAlignVertical.top, keyboardType: TextInputType.multiline,
          style: const TextStyle(fontFamily: 'JetBrainsMono', fontSize: 13, height: 1.6),
          decoration: const InputDecoration(border: InputBorder.none, contentPadding: EdgeInsets.all(16)))),
      ])),
    ));
}

class TerminalPreviewPage extends StatelessWidget {
  const TerminalPreviewPage({super.key});
  @override Widget build(BuildContext context) => CommonScaffold(title: context.tr('Terminal preview'),
    body: Padding(padding: EdgeInsets.only(top: context.appBarInset, bottom: MediaQuery.paddingOf(context).bottom),
      child: const GhosttySurface(sessionId: '', settingsRevision: 0, preview: true)));
}
