import '../widgets.dart';
import 'licenses.dart';

// Hero and link badges adapted from FlClash v0.9.3 views/about.dart.
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
      final incomplete = result['complete'] == false;
      final available = result['available'] == true;
      if (!mounted) return;
      setState(() => _checking = false);
      await showBoxDialog<void>(context: context, builder: (context) => AlertDialog(
        title: context.tr(result['available'] == true ? 'Update available' : 'Check for updates'),
        child: ConstrainedBox(constraints: const BoxConstraints(maxWidth: 420), child: SingleChildScrollView(child: Column(
          mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.start, spacing: 12, children: [
            Text(available ? 'B-box ${result['version']}' : context.tr(incomplete ? 'Update check incomplete' : result['hasMetadata'] == false ? 'No release metadata available' : 'You are up to date')),
            if (incomplete) Text(context.tr('Some releases could not be checked. Please try again.')),
            if (result['available'] == true && result['notes'] is String && (result['notes'] as String).isNotEmpty) SelectableText(result['notes'] as String),
          ],
        ))),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context), child: Text(context.tr('Close'))),
          if (available || result['hasMetadata'] == false || incomplete) TextButton(onPressed: () {
            Navigator.pop(context);
            box.act('url.open', {'url': available ? result['downloadUrl'] ?? result['url'] ?? 'https://github.com/giturass/B-box/releases' : 'https://github.com/giturass/B-box/releases'});
          }, child: Text(context.tr(!available || result['downloadUrl'] == null ? 'Releases' : 'Download'))),
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
    return Scaffold(title: context.tr('About'), isLoading: _checking,
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
          _link(AppGlyphs.paintbrush, 'FlClash interface', 'FlClash v0.9.3 · prism_ui 0.1.2 · GPL-3.0', 'https://github.com/chen08209/FlClash/tree/be84736605527034689c95bf27828ff27d912c5e'),
          ListItem(leading: const _LinkBadge(glyph: AppGlyphs.info), title: Text(context.tr('Open-source licenses')),
            onTap: () => openPage(context, const LicensesPage())),
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
