import 'dart:io';
import 'dart:math';
import '../widgets.dart';
import '../theme_settings.dart';
import '../components/theme_preview.dart';
import '../components/palette.dart';

/// FlClash's Android appearance controls, driven by B-box preferences.
class AppearancePage extends StatelessWidget {
  const AppearancePage({super.key});

  @override
  Widget build(BuildContext context) {
    final box = context.box, prefs = context.box.preferences;
    final floating = prefs['floatingNavigation'] != false;
    final glass = (prefs['liquidGlass'] ?? 'none') != 'none';
    Widget preview({Brightness? brightness, bool? pureBlack, bool? floatingBar, bool? liquidGlass, TabAnimation? animation}) => MiniScreenThumb(
      screen: MiniScreen(
        colorScheme: appColorScheme(box, brightness ?? Theme.of(context).brightness, pureBlack: pureBlack),
        floatingBar: floatingBar ?? floating, liquidGlass: liquidGlass ?? glass,
        selected: animation == null ? 0 : 1, previous: animation == null ? null : 0,
        progress: animation == null ? 1 : .55, tabAnimation: animation ?? tabAnimationOf(prefs),
      ),
    );
    return CommonScaffold(title: context.tr('Appearance'), body: ListView(
      padding: EdgeInsets.only(top: context.contentTopPadding, bottom: 32 + context.bottomInset),
      children: [
        const ThemeLivePreview(), const SizedBox(height: 16),
        PreviewChoiceGroup<String>(
          info: Info(label: context.tr('Theme'), glyph: AppGlyphs.sun), value: prefs['theme'] as String? ?? 'system',
          choices: [
            PreviewChoice(value: 'system', label: context.tr('System'), pictogram: MiniScreenThumb(screen: MiniSplitScreen(
              light: MiniScreen(colorScheme: appColorScheme(box, Brightness.light), floatingBar: floating, liquidGlass: glass),
              dark: MiniScreen(colorScheme: appColorScheme(box, Brightness.dark), floatingBar: floating, liquidGlass: glass),
            ))),
            PreviewChoice(value: 'light', label: context.tr('Light'), pictogram: preview(brightness: Brightness.light)),
            PreviewChoice(value: 'dark', label: context.tr('Dark'), pictogram: preview(brightness: Brightness.dark)),
          ], onChanged: (v) => box.setPreference('theme', v),
        ), const SizedBox(height: 16),
        PreviewChoiceGroup<bool>(
          info: Info(label: context.tr('Pure black mode'), glyph: AppGlyphs.pureBlack), value: prefs['pureBlack'] == true,
          choices: [
            PreviewChoice(value: false, label: context.tr('Standard'), pictogram: preview(brightness: Brightness.dark, pureBlack: false)),
            PreviewChoice(value: true, label: context.tr('Pure black'), pictogram: preview(brightness: Brightness.dark, pureBlack: true)),
          ], onChanged: (v) => box.setPreference('pureBlack', v),
        ), const SizedBox(height: 16),
        const _AccentColors(), const SizedBox(height: 16),
        PreviewChoiceGroup<bool>(
          info: Info(label: context.tr('Navigation bar style'), glyph: AppGlyphs.layers), value: floating,
          choices: [
            PreviewChoice(value: true, label: context.tr('Floating'), pictogram: preview(floatingBar: true)),
            PreviewChoice(value: false, label: context.tr('Docked'), pictogram: preview(floatingBar: false)),
          ], onChanged: (v) => box.setPreference('floatingNavigation', v),
        ), const SizedBox(height: 16),
        PreviewChoiceGroup<TabAnimation>(
          info: Info(label: context.tr('Tab animation'), glyph: AppGlyphs.motion), value: tabAnimationOf(prefs),
          choices: [for (final mode in TabAnimation.values) PreviewChoice(value: mode,
            label: context.tr(mode == TabAnimation.slide ? 'Slide' : 'Fade'), pictogram: preview(animation: mode))],
          onChanged: (v) => box.setPreference('tabAnimation', v.name),
        ), const SizedBox(height: 16),
        PreviewChoiceGroup<String>(
          info: Info(label: context.tr('Liquid glass'), glyph: AppGlyphs.liquidGlass), value: prefs['liquidGlass'] as String? ?? 'none',
          choices: [for (final mode in ['none', 'standard', 'ultra']) PreviewChoice(value: mode,
            label: context.tr({'none': 'None', 'standard': 'Standard', 'ultra': 'Ultra'}[mode]!),
            pictogram: preview(floatingBar: true, liquidGlass: mode != 'none'))],
          onChanged: (v) => box.setPreference('liquidGlass', v),
        ), const SizedBox(height: 16),
        InfoHeader(info: Info(label: context.tr('Font family'), glyph: AppGlyphs.font)),
        Padding(padding: const EdgeInsets.fromLTRB(16, 8, 16, 0), child: generateGroup([
          ListItem.open(leading: Text('Aa', style: AppTextStyle.headline.of(context)),
            title: Text(prefs['fontFamily'] as String? ?? context.tr('Default')), widget: const FontFamilyPage()),
        ])), const SizedBox(height: 16),
        const _TextScaleControl(),
      ],
    ));
  }
}

class _AccentColors extends StatelessWidget {
  const _AccentColors();
  Future<void> _edit(BuildContext context, int? old) async {
    final box = context.box;
    final value = await openForm<int>(context, CustomColorPage(initial: Color(old ?? (box.preferences['color'] as num?)?.toInt() ?? box.systemAccentColor)));
    if (value == null) return;
    final colors = ((box.preferences['accentColors'] as List?)?.cast<int>() ?? defaultAccentColors).toList();
    final index = old == null ? -1 : colors.indexOf(old);
    if (index >= 0) colors.removeAt(index);
    if (!colors.contains(value)) colors.insert(index < 0 ? colors.length : index, value);
    await box.setPreferences({'accentColors': colors, 'color': value, 'systemColor': false});
  }
  @override Widget build(BuildContext context) {
    final box = context.box, prefs = context.box.preferences;
    final colors = (prefs['accentColors'] as List?)?.cast<int>() ?? defaultAccentColors;
    final selected = prefs['systemColor'] == true ? null : (prefs['color'] as num?)?.toInt() ?? 0xff007aff;
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      InfoHeader(info: Info(label: context.tr('Accent color'), glyph: AppGlyphs.palette), actions: [
        IconButtonData(glyph: AppGlyphs.reset, tooltip: context.tr('Reset'), onPressed: () async {
          if (await confirm(context, 'Reset accent colors?', action: 'Reset')) {
            await box.setPreferences({'accentColors': defaultAccentColors, 'color': 0xff007aff, 'systemColor': false, 'colorStyle': 'vivid'});
          }
        }),
      ]),
      Padding(padding: const EdgeInsets.fromLTRB(16, 8, 16, 0), child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, spacing: 8, children: [
        _ColorSwatches(colors: [null, ...colors], selected: selected,
          onSelect: (v) => box.setPreferences({'systemColor': v == null, if (v != null) 'color': v}),
          onEdit: (v) => _edit(context, v), onAdd: colors.length >= 64 ? null : () => _edit(context, null),
          onDelete: (v) async {
            if (await confirm(context, 'Delete this color?')) {
              final remaining = colors.where((c) => c != v).toList();
              await box.setPreferences({'accentColors': remaining, if (selected == v) 'systemColor': true});
            }
          }),
        generateGroup([ListItem<String>.options(title: Text(context.tr('Color style')), options: ColorStyle.values.map((v) => v.name).toList(),
          value: prefs['colorStyle'] as String? ?? 'vivid', textBuilder: (v) => context.tr(v[0].toUpperCase() + v.substring(1)),
          onChanged: (v) => box.setPreference('colorStyle', v))]),
      ])),
    ]);
  }
}

class CustomColorPage extends StatefulWidget {
  const CustomColorPage({super.key, required this.initial});
  final Color initial;
  @override State<CustomColorPage> createState() => _CustomColorPageState();
}
class _CustomColorPageState extends State<CustomColorPage> {
  late final _color = ValueNotifier(widget.initial);
  @override void dispose() { _color.dispose(); super.dispose(); }
  @override Widget build(BuildContext context) => BoxPage(title: 'Custom color',
    confirmationAction: IconButtonData(glyph: AppGlyphs.check, tooltip: context.tr('Save'), onPressed: () => context.safeNestedPop(_color.value.toARGB32())),
    children: [
      ValueListenableBuilder(valueListenable: _color, builder: (context, color, _) => Container(
        padding: const EdgeInsets.all(12), decoration: ShapeDecoration(color: context.raisedSurfaceLevel.colorOf(context.colorScheme), shape: AppShape.xl),
        child: Row(spacing: 12, children: [
          for (final brightness in Brightness.values) SizedBox(width: 48, height: 72, child: ClipRSuperellipse(borderRadius: AppRadius.sm,
            child: MiniScreen(colorScheme: appColorScheme(context.box, brightness, color: color), floatingBar: true))),
          Expanded(child: HexColorField(color: color, onChanged: (v) => _color.value = v)),
        ]),
      )),
      const SizedBox(height: 16), Palette(controller: _color),
    ]);
}

class _TextScaleControl extends StatefulWidget {
  const _TextScaleControl();
  @override State<_TextScaleControl> createState() => _TextScaleControlState();
}
class _TextScaleControlState extends State<_TextScaleControl> {
  double? _draft;
  @override Widget build(BuildContext context) {
    final box = context.box, prefs = context.box.preferences;
    final enabled = customFontScale(prefs);
    final scale = _draft ?? appTextScale(prefs, View.of(context).platformDispatcher.textScaleFactor);
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      InfoHeader(info: Info(label: context.tr('Font scale'), glyph: AppGlyphs.textSize), actions: [
        if (enabled && scale != 1) IconButtonData(glyph: AppGlyphs.reset, tooltip: context.tr('Reset'), onPressed: () => box.setPreference('fontScale', 1)),
      ]),
      Padding(padding: const EdgeInsets.fromLTRB(16, 8, 16, 0), child: Column(spacing: 8, children: [
        SegmentedControl<bool>(segments: [(false, context.tr('Follow system')), (true, context.tr('Custom'))], value: enabled,
          onChanged: (v) { setState(() => _draft = null); box.setPreference('customFontScale', v); }),
        Container(padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
          decoration: ShapeDecoration(color: context.raisedSurfaceLevel.colorOf(context.colorScheme), shape: AppShape.lg),
          child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
            Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
              Expanded(child: MediaQuery(data: MediaQuery.of(context).copyWith(textScaler: TextScaler.linear(scale)), child: Column(
                crossAxisAlignment: CrossAxisAlignment.start, spacing: 4, children: [Text('Aa', style: AppTextStyle.title3.of(context)),
                  Text(context.tr('Text size preview'), style: AppTextStyle.subheadline.of(context))],
              ))), Text('${(scale * 100).round()}%', style: AppTextStyle.subheadline.of(context).toTabular),
            ]),
            const SizedBox(height: 8),
            MediaQuery.withNoTextScaling(child: Row(spacing: 12, children: [
              Text('A', style: AppTextStyle.footnote.of(context)),
              Expanded(child: Slider(min: .8, max: 1.4, divisions: 12, value: scale,
                onChanged: enabled ? (v) => setState(() => _draft = v) : null,
                onChangeEnd: (v) async { await box.setPreference('fontScale', v); if (mounted) setState(() => _draft = null); })),
              Text('A', style: AppTextStyle.title2.of(context)),
            ])),
          ]),
        ),
      ])),
    ]);
  }
}

class FontFamilyPage extends StatefulWidget {
  const FontFamilyPage({super.key});
  @override State<FontFamilyPage> createState() => _FontFamilyPageState();
}
class _FontFamilyPageState extends State<FontFamilyPage> {
  List<String>? _fonts;
  String _query = '';
  @override void initState() { super.initState(); _load(); }
  Future<void> _load() async {
    final fonts = <String>{'JetBrainsMono'};
    if (Platform.isAndroid) {
      try {
        final xml = await File('/system/etc/fonts.xml').readAsString();
        fonts.addAll(RegExp(r'<family\b[^>]*\bname="([^"]+)"').allMatches(xml).map((m) => m.group(1)!));
      } on FileSystemException catch (error) { if (mounted) context.box.report(error); }
    }
    if (mounted) setState(() => _fonts = fonts.toList()..sort((a, b) => a.toLowerCase().compareTo(b.toLowerCase())));
  }
  @override Widget build(BuildContext context) {
    final box = context.box, selected = context.box.preferences['fontFamily'];
    final fonts = (_fonts ?? <String>[]).where((v) => v.toLowerCase().contains(_query.toLowerCase())).toList();
    return CommonScaffold(title: context.tr('Font family'), isLoading: _fonts == null,
      searchState: AppBarSearchState(onSearch: (v) => setState(() => _query = v)),
      body: ListView.builder(padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 24 + context.bottomInset),
        itemCount: fonts.length + 1, itemBuilder: (context, i) {
          final family = i == 0 ? null : fonts[i - 1];
          return DecorationListItem(title: Text(family ?? context.tr('Default'), style: TextStyle(fontFamily: family)),
            isSelected: selected == family, trailing: selected == family ? const SelectionCheck() : null,
            onPressed: () => box.setPreference('fontFamily', family));
        }),
    );
  }
}

class _ColorSwatches extends StatelessWidget {
  const _ColorSwatches({
    required this.colors,
    required this.selected,
    required this.onSelect,
    required this.onEdit,
    required this.onDelete,
    required this.onAdd,
  });

  static const double _minGap = 6;

  // Lines the dots, not their selection rings, up with the rows' text.
  static const double _inset = 16 - (_ColorSwatch.size - _ColorSwatch.dot) / 2;

  final List<int?> colors;
  final int? selected;
  final ValueChanged<int?> onSelect;
  final ValueChanged<int> onEdit;
  final ValueChanged<int> onDelete;
  final VoidCallback? onAdd;

  @override
  Widget build(BuildContext context) {
    final colorScheme = context.colorScheme;
    final level = context.raisedSurfaceLevel;
    final systemColor = Color(context.box.systemAccentColor);
    return Container(
      padding: const EdgeInsets.all(_inset),
      decoration: ShapeDecoration(
        color: level.colorOf(colorScheme),
        shape: AppShape.xl,
      ),
      child: SurfaceScope(
        level: level,
        child: LayoutBuilder(
          builder: (_, constraints) {
            const cell = _ColorSwatch.size;
            final width = constraints.maxWidth;
            final columns = max(
              1,
              ((width + _minGap) / (cell + _minGap)).floor(),
            );
            final gap = columns > 1
                ? ((width - columns * cell) / (columns - 1)).floorToDouble()
                : 0.0;
            return Wrap(
              spacing: gap,
              runSpacing: gap,
              children: [
                for (final color in colors)
                  _ColorSwatch(
                    color: color == null ? null : Color(color),
                    systemColor: systemColor,
                    isSelected: color == selected,
                    label: color == null
                        ? context.tr('Follow system')
                        : '#${(color & 0xffffff).toRadixString(16).padLeft(6, '0').toUpperCase()}',
                    onPressed: () => onSelect(color),
                    menuItems: color == null
                        ? null
                        : [
                            CommonPopupMenuItem(
                              glyph: AppGlyphs.edit,
                              label: context.tr('Edit'),
                              onPressed: () => onEdit(color),
                            ),
                            CommonPopupMenuItem(
                              danger: true,
                              glyph: AppGlyphs.delete,
                              label: context.tr('Delete'),
                              onPressed: () => onDelete(color),
                            ),
                          ],
                  ),
                if (onAdd != null) _AddColorButton(onPressed: onAdd!),
              ],
            );
          },
        ),
      ),
    );
  }
}

class _ColorSwatch extends StatefulWidget {
  const _ColorSwatch({
    required this.color,
    required this.systemColor,
    required this.isSelected,
    required this.label,
    required this.onPressed,
    this.menuItems,
  });

  static const double size = 44;
  static const double dot = 32;
  static const double _ring = 2.5;
  static const _duration = Duration(milliseconds: 200);

  static const double _badge = 20;

  /// Null follows the system's accent color, [systemColor].
  final Color? color;
  final Color systemColor;
  final bool isSelected;
  final String label;
  final VoidCallback onPressed;
  final List<CommonPopupMenuItem>? menuItems;

  @override
  State<_ColorSwatch> createState() => _ColorSwatchState();
}

class _ColorSwatchState extends State<_ColorSwatch> {
  bool _focused = false;

  @override
  Widget build(BuildContext context) {
    final color = widget.color;
    final fill = color ?? widget.systemColor;
    final selected = widget.isSelected;
    final check = ThemeData.estimateBrightnessForColor(fill) == Brightness.dark
        ? Colors.white
        : Colors.black;
    final swatch = Semantics(
      button: true,
      selected: selected,
      label: widget.label,
      child: SizedBox.square(
        dimension: _ColorSwatch.size,
        child: Material(
          type: MaterialType.transparency,
          shape: AppShape.circle,
          clipBehavior: Clip.antiAlias,
          child: InkWell(
            customBorder: AppShape.circle,
            onTap: widget.onPressed,
            onFocusChange: (value) => setState(() => _focused = value),
            child: Stack(
              alignment: Alignment.center,
              children: [
                AnimatedOpacity(
                  opacity: selected || _focused ? 1 : 0,
                  duration: _ColorSwatch._duration,
                  child: DecoratedBox(
                    decoration: ShapeDecoration(
                      shape: AppShape.circle.copyWith(
                        side: BorderSide(
                          color: selected ? fill : context.colorScheme.primary,
                          width: _ColorSwatch._ring,
                        ),
                      ),
                    ),
                    child: const SizedBox.expand(),
                  ),
                ),
                SizedBox.square(
                  dimension: _ColorSwatch.dot,
                  child: DecoratedBox(
                    decoration: ShapeDecoration(
                      color: fill,
                      shape: AppShape.circle,
                    ),
                  ),
                ),
                if (color == null)
                  const Positioned(
                    right: 0,
                    bottom: 0,
                    child: _EyedropperBadge(size: _ColorSwatch._badge),
                  ),
                AnimatedScale(
                  scale: selected ? 1 : 0,
                  duration: _ColorSwatch._duration,
                  curve: Curves.easeOutBack,
                  child: GlyphIcon(
                    AppGlyphs.check,
                    size: 18,
                    fill: 1,
                    color: check,
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
    final menuItems = widget.menuItems;
    if (menuItems == null) {
      return swatch;
    }
    return ContextMenuRegion(menuItems: menuItems, child: swatch);
  }
}

class _EyedropperBadge extends StatelessWidget {
  const _EyedropperBadge({required this.size});

  static const double _gap = 2;

  final double size;

  @override
  Widget build(BuildContext context) {
    final colorScheme = context.colorScheme;
    return Container(
      width: size,
      height: size,
      padding: const EdgeInsets.all(_gap),
      decoration: ShapeDecoration(
        color: context.surfaceLevel.colorOf(colorScheme),
        shape: AppShape.circle,
      ),
      child: DecoratedBox(
        decoration: ShapeDecoration(
          color: context.raisedSurfaceLevel.colorOf(colorScheme),
          shape: AppShape.circle,
        ),
        child: Center(
          child: GlyphIcon(
            AppGlyphs.eyedropper,
            size: size - _gap * 2 - 4,
            color: colorScheme.onSurface,
          ),
        ),
      ),
    );
  }
}

class _AddColorButton extends StatelessWidget {
  const _AddColorButton({required this.onPressed});

  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) {
    final colorScheme = context.colorScheme;
    return Tooltip(
      message: context.tr('Add'),
      child: Semantics(
        button: true,
        child: SizedBox.square(
          dimension: _ColorSwatch.size,
          child: Material(
            type: MaterialType.transparency,
            shape: AppShape.circle,
            clipBehavior: Clip.antiAlias,
            child: InkWell(
              customBorder: AppShape.circle,
              onTap: onPressed,
              child: Center(
                child: Container(
                  width: _ColorSwatch.dot,
                  height: _ColorSwatch.dot,
                  decoration: ShapeDecoration(
                    color: context.raisedSurfaceLevel.colorOf(colorScheme),
                    shape: AppShape.circle,
                  ),
                  child: GlyphIcon(
                    AppGlyphs.add,
                    size: 20,
                    color: colorScheme.onSurfaceVariant,
                  ),
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}


