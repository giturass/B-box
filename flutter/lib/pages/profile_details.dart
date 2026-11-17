import 'package:flutter/services.dart';
import '../widgets.dart';
import '../components/profile_panel.dart';
import '../components/profile_card.dart';
import 'profiles.dart';
import 'profile_editor.dart';
import 'configuration.dart';
import 'subscriptions.dart';

class ProfileDetailsPage extends StatefulWidget {
  const ProfileDetailsPage({super.key, this.profile});
  final Json? profile;
  @override State<ProfileDetailsPage> createState() => _ProfileDetailsPageState();
}

class _ProfileDetailsPageState extends State<ProfileDetailsPage> {
  bool _updating = false;
  Future<void> _update(Json profile) async {
    if (_updating) return;
    setState(() => _updating = true);
    try { await updateProfileSubscriptions(context.box, profile, canContinue: () => mounted); }
    finally { if (mounted) setState(() => _updating = false); }
  }
  @override Widget build(BuildContext context) {
    final box = context.watchBox(BoxAspect.groups);
    final p = box.remote ? null : widget.profile == null ? box.selectedProfile : box.profiles.where((p) => p['id'] == widget.profile!['id']).firstOrNull;
    if (!box.remote && widget.profile != null && p == null) return const BoxPage(title: 'Edit', children: [EmptyState('Profile no longer exists')]);
    if (p == null) return BoxPage(title: 'Edit', children: [DetailRows({
      'Name': '—', 'Remote control': box.dashboard['remote'] ?? '—',
      'Proxy groups': box.groups.length,
    })]);
    return BoxPage(title: 'Edit', actions: [
      IconButtonData(glyph: AppGlyphs.edit, tooltip: context.tr('Edit'), onPressed: () => openForm(context, ProfileForm(initial: p))),
      if (profileHasUpdates(p)) IconButtonData(glyph: AppGlyphs.sync, tooltip: context.tr('Update'),
        isLoading: _updating, onPressed: _updating ? null : () => _update(p)),
    ], children: [
      Padding(padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 12), child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, spacing: 14, children: [
        EmojiText(profileDisplayName(context, p), style: AppTextStyle.title2.of(context, emphasized: true)),
        ProfileStats(profile: p),
      ])),
      generateSection(items: [
        if (p['remote'] == true) ListItem(title: Text(context.tr('URL')), subtitle: SelectableText(p['url'].toString()),
          trailing: IconButton(icon: const GlyphIcon(AppGlyphs.copy), tooltip: context.tr('Copy'), onPressed: () => Clipboard.setData(ClipboardData(text: p['url'].toString())))),
        ListItem(title: Text(context.tr('Last updated')), subtitle: Text(
          (p['updatedAt'] as num? ?? 0) <= 0 ? context.tr('Unknown') : localDate(p['updatedAt']))),
        if (p['remote'] == true) ...[
          ListItem(title: Text(context.tr('Auto update')), trailing: Text(context.tr(p['autoUpdate'] == true ? 'Turn on' : 'Turn off'))),
          ListItem(title: Text(context.tr('Update interval (minutes)')), trailing: Text(p['interval'].toString())),
        ],
      ]),
      generateSection(items: [
        ListItem.open(leading: const GlyphIcon(AppGlyphs.cloudDownload), title: Text(context.tr('Node subscriptions')),
          widget: SubscriptionsPage(profile: p)),
      ]),
      generateSection(items: [
        for (final section in ProfileConfigSection.values)
          ListItem.open(leading: GlyphIcon(section.glyph), title: Text(context.tr(section.title)),
            widget: ProfileConfigurationPage(profile: p, section: section)),
      ]),
      generateSection(items: [
        ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Open configuration')), widget: ProfileEditor(profile: p)),
      ]),
    ]);
  }
}
