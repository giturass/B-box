import 'package:material_ui/material_ui.dart';
import 'box.dart';
import 'l10n.dart';
import 'ui/ui.dart';
import 'ui/icons/icons.dart';

export 'package:material_ui/material_ui.dart';
export 'box.dart';
export 'l10n.dart';
export 'ui/ui.dart';
export 'ui/icons/icons.dart';

class BoxMenu extends StatelessWidget {
  const BoxMenu({super.key, required this.items});
  final List<CommonPopupMenuItem> items;
  @override Widget build(BuildContext context) => CommonPopupBox(
    targetBuilder: (open) => IconButton(tooltip: context.tr('More'), onPressed: open, icon: const GlyphIcon(AppGlyphs.more)),
    popupBuilder: (_) => CommonPopupMenu(items: items),
  );
}

class BoxPage extends StatelessWidget {
  const BoxPage({super.key, required this.title, required this.children, this.actions = const [], this.primaryAction, this.confirmationAction, this.loading = false});
  final String title;
  final List<Widget> children;
  final List<IconButtonData> actions;
  final IconButtonData? primaryAction, confirmationAction;
  final bool loading;
  @override
  Widget build(BuildContext context) => CommonScaffold(
    title: context.tr(title),
    iconActions: actions,
    primaryAction: primaryAction,
    confirmationAction: confirmationAction,
    isLoading: loading,
    body: ListView(
      padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 24 + context.bottomInset),
      children: children,
    ),
  );
}

Future<T?> openPage<T>(BuildContext context, Widget page) => showExtend<T>(context, builder: (_) => page);
Future<T?> openForm<T>(BuildContext context, Widget page) => showFormSheet<T>(context: context, builder: (_) => page);

class EmptyState extends StatelessWidget {
  const EmptyState(this.title, {super.key, this.message, this.action, this.glyph = AppGlyphs.info});
  final String title;
  final String? message;
  final Widget? action;
  final Glyph glyph;
  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.symmetric(vertical: 64, horizontal: 24),
    child: Column(mainAxisSize: MainAxisSize.min, children: [
      GlyphIcon(glyph, size: 56, color: context.colorScheme.onSurfaceVariant),
      const SizedBox(height: 20),
      Text(context.tr(title), style: AppTextStyle.title3.of(context), textAlign: TextAlign.center),
      if (message != null) Padding(
        padding: const EdgeInsets.symmetric(vertical: 12),
        child: Text(context.tr(message!), textAlign: TextAlign.center, style: context.listSubtitleStyle),
      ),
      if (action != null) Padding(padding: const EdgeInsets.only(top: 16), child: action),
    ]),
  );
}

class ErrorState extends StatelessWidget {
  const ErrorState(this.error, {super.key, this.retry});
  final Object error;
  final VoidCallback? retry;
  @override
  Widget build(BuildContext context) => EmptyState(
    'Error',
    message: BoxController.describe(error),
    action: retry == null ? null : FilledButton(onPressed: retry, child: Text(context.tr('Retry'))),
  );
}

Future<bool> confirm(BuildContext context, String title, {String? message, String action = 'Delete'}) async =>
    await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(context.tr(title)),
        content: message == null ? null : Text(context.tr(message)),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context, false), child: Text(context.tr('Cancel'))),
          TextButton(onPressed: () => Navigator.pop(context, true), child: Text(context.tr(action))),
        ],
      ),
    ) ?? false;

class DetailRows extends StatelessWidget {
  const DetailRows(this.values, {super.key});
  final Map<String, dynamic> values;
  @override
  Widget build(BuildContext context) => generateGroup([
    for (final item in values.entries)
      ListItem(title: Text(context.tr(item.key)), subtitle: SelectableText(item.value?.toString() ?? '—')),
  ]);
}

class BoxPanel extends StatelessWidget {
  const BoxPanel({super.key, required this.child, this.onTap, this.padding = const EdgeInsets.symmetric(horizontal: 20, vertical: 18)});
  final Widget child;
  final VoidCallback? onTap;
  final EdgeInsetsGeometry padding;
  @override
  Widget build(BuildContext context) {
    final dark = context.colorScheme.brightness == Brightness.dark;
    BoxShadow shadow(int alpha, double y, double blur, [double spread = 0]) => BoxShadow(
      color: Color.fromARGB(alpha, 0, 0, 0), offset: Offset(0, y),
      blurRadius: blur <= 1 ? 0 : (blur / 2 - .5) / .57735, spreadRadius: spread,
    );
    return Container(
      decoration: ShapeDecoration(
        color: SurfaceLevel.card.colorOf(context.colorScheme),
        shape: AppShape.all(34),
        shadows: dark ? [shadow(0x80, 1, 2), shadow(0x8c, 10, 28, -6)]
            : [shadow(0x0d, .5, 1), shadow(0x0a, 2, 6), shadow(0x17, 12, 32, -6)],
      ),
      child: Material(
        type: MaterialType.transparency,
        shape: AppShape.all(34),
        clipBehavior: Clip.antiAlias,
        child: onTap == null ? Padding(padding: padding, child: child) : InkWell(
          customBorder: AppShape.all(34), onTap: onTap, child: Padding(padding: padding, child: child),
        ),
      ),
    );
  }
}

class PanelTitle extends StatelessWidget {
  const PanelTitle(this.title, this.glyph, {super.key, this.trailing});
  final String title;
  final Glyph glyph;
  final Widget? trailing;
  @override
  Widget build(BuildContext context) => SizedBox(height: 26 * MediaQuery.textScalerOf(context).scale(1), child: Row(children: [
    GlyphIcon(glyph, size: 18, color: context.colorScheme.onSurfaceVariant),
    const SizedBox(width: 8),
    Expanded(child: Text(context.tr(title), maxLines: 1, overflow: TextOverflow.ellipsis, style: AppTextStyle.subheadline.of(context, emphasized: true))),
    if (trailing != null) trailing!,
  ]));
}

String localDate(dynamic value) {
  final ms = (value as num?)?.toInt() ?? 0;
  if (ms <= 0) return '—';
  // libbox connection timestamps are milliseconds; do not infer fabricated ages.
  return DateTime.fromMillisecondsSinceEpoch(ms).toLocal().toString().split('.').first;
}
