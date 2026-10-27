import '../widgets.dart';

// Hero and link badges adapted from FlClash v0.9.2 views/about.dart.
class AboutPage extends StatefulWidget {
  const AboutPage({super.key});
  @override State<AboutPage> createState() => _AboutPageState();
}

class _AboutPageState extends State<AboutPage> {
  bool _checking = false;

  Future<void> _check() async {
    if (_checking) return;
    final box = context.box;
    setState(() => _checking = true);
    try {
      final result = json(await box.call('updates.check'));
      if (!mounted) return;
      setState(() => _checking = false);
      await showDialog<void>(context: context, builder: (context) => AlertDialog(
        title: Text(context.tr(result['available'] == true ? 'Update available' : 'Check for updates')),
        content: ConstrainedBox(constraints: const BoxConstraints(maxWidth: 420), child: SingleChildScrollView(child: Column(
          mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.start, spacing: 12, children: [
            Text(result['available'] == true ? 'B-box ${result['version']}' : context.tr(result['hasMetadata'] == false ? 'No release metadata available' : 'You are up to date')),
            if (result['available'] == true && result['notes'] is String && (result['notes'] as String).isNotEmpty) SelectableText(result['notes'] as String),
          ],
        ))),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context), child: Text(context.tr('Close'))),
          if (result['available'] == true || result['hasMetadata'] == false) TextButton(onPressed: () {
            Navigator.pop(context);
            box.act('url.open', {'url': result['downloadUrl'] ?? result['url'] ?? 'https://github.com/giturass/B-box/releases'});
          }, child: Text(context.tr(result['downloadUrl'] == null ? 'Releases' : 'Download'))),
        ],
      ));
    } catch (error) { box.report(error); }
    finally { if (mounted) setState(() => _checking = false); }
  }

  Widget _link(Glyph glyph, String title, String label, String url) => ListItem(
    leading: _LinkBadge(glyph: glyph), title: Text(context.tr(title)),
    subtitle: Text(label, maxLines: 1, overflow: TextOverflow.ellipsis),
    trailing: const GlyphIcon(AppGlyphs.openExternal),
    onTap: () => context.box.act('url.open', {'url': url}),
  );

  @override Widget build(BuildContext context) {
    final box = context.box, colors = context.colorScheme;
    return CommonScaffold(title: context.tr('About'), isLoading: _checking,
      body: ListView(padding: EdgeInsets.fromLTRB(16, context.contentTopPadding, 16, 32 + context.bottomInset), children: [
        Padding(padding: const EdgeInsets.fromLTRB(24, 16, 24, 0), child: Column(children: [
          DecoratedBox(decoration: ShapeDecoration(color: context.raisedSurfaceLevel.colorOf(colors), shape: AppShape.all(AppCorner.fit(96))),
            child: Padding(padding: const EdgeInsets.all(14), child: Image.asset('assets/images/icon.png', width: 68, height: 68))),
          const SizedBox(height: 20),
          Text('B-box', style: AppTextStyle.title2.of(context, emphasized: true)),
          const SizedBox(height: 10),
          Wrap(spacing: 8, runSpacing: 8, alignment: WrapAlignment.center, children: [
            _Pill(label: 'v${box.version}', color: colors.primary, foreground: colors.onPrimary),
            _Pill(label: 'GPL-3.0', color: context.raisedSurfaceLevel.colorOf(colors), foreground: colors.onSurfaceVariant),
          ]),
          const SizedBox(height: 16),
          ConstrainedBox(constraints: const BoxConstraints(maxWidth: 420), child: Text(context.tr('A sing-box client with the FlClash Android interface.'),
            textAlign: TextAlign.center, style: AppTextStyle.subheadline.of(context).copyWith(color: colors.onSurfaceVariant, height: 1.5))),
          const SizedBox(height: 20),
          TonalButton(size: TonalButtonSize.bar, glyph: AppGlyphs.sync, label: context.tr('Check for updates'), onPressed: _checking ? null : _check),
        ])),
        const SizedBox(height: 8),
        generateSection(title: context.tr('More'), items: [
          _link(AppGlyphs.code, 'Source code', 'github.com/giturass/B-box', 'https://github.com/giturass/B-box'),
          _link(AppGlyphs.cpu, 'Core', 'sing-box ${box.coreVersion}', 'https://github.com/SagerNet/sing-box'),
          _link(AppGlyphs.paintbrush, 'FlClash interface', 'chen08209/FlClash · v0.9.2 · GPL-3.0', 'https://github.com/chen08209/FlClash/tree/7fd417fb9a9a277c6a9339a8ac87f907df7c25f7'),
          ListItem(leading: const _LinkBadge(glyph: AppGlyphs.info), title: Text(context.tr('Open-source licenses')),
            onTap: () => showLicensePage(context: context, applicationName: 'B-box', applicationVersion: box.version)),
        ]),
      ]),
    );
  }
}

class _Pill extends StatelessWidget {
  const _Pill({required this.label, required this.color, required this.foreground});
  final String label;
  final Color color, foreground;
  @override Widget build(BuildContext context) => DecoratedBox(decoration: ShapeDecoration(color: color, shape: AppShape.full),
    child: Padding(padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4), child: Text(label,
      style: AppTextStyle.caption1.of(context, emphasized: true).copyWith(color: foreground))));
}

class _LinkBadge extends StatelessWidget {
  const _LinkBadge({required this.glyph});
  final Glyph glyph;
  @override Widget build(BuildContext context) => DecoratedBox(
    decoration: ShapeDecoration(color: context.raisedSurfaceLevel.colorOf(context.colorScheme), shape: AppShape.md),
    child: SizedBox.square(dimension: 40, child: Center(child: GlyphIcon(glyph, size: 20, color: context.colorScheme.onSurfaceVariant))));
}
