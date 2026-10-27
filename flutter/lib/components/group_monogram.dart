import '../widgets.dart';

// FlClash v0.9.2 widgets/icon.dart: a real group name supplies the fallback.
class GroupMonogram extends StatelessWidget {
  const GroupMonogram(this.name, {super.key});
  final String name;
  static final _letter = RegExp(r'[\p{L}\p{N}]', unicode: true);

  @override Widget build(BuildContext context) {
    final theme = IconTheme.of(context), size = IconTheme.of(context).size ?? 24;
    final mark = _letter.firstMatch(name)?.group(0)?.toUpperCase() ?? name.characters.firstOrNull ?? '';
    return SizedBox.square(dimension: size, child: Center(child: mark.isEmpty
      ? GlyphIcon(AppGlyphs.proxies, size: size * .7, color: theme.color)
      : EmojiText(mark, maxLines: 1, style: AppTextStyle.custom(context,
          size: size * .62, leading: size * .62, weight: FontWeight.w600).copyWith(color: theme.color))));
  }
}
