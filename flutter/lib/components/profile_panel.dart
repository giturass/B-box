import '../widgets.dart';
import '../pages/profiles.dart';
import '../pages/profile_details.dart';

class ProfilePanel extends StatefulWidget {
  const ProfilePanel({super.key});
  @override State<ProfilePanel> createState() => _ProfilePanelState();
}
class _ProfilePanelState extends State<ProfilePanel> {
  bool _updating = false;
  @override Widget build(BuildContext context) {
    final box = context.box;
    final profile = box.remote ? null : box.selectedProfile;
    final name = box.remote ? context.tr('Remote configuration') : profile?['name'];
    final label = AppTextStyle.caption1.of(context).copyWith(color: context.colorScheme.onSurfaceVariant);
    final updated = (profile?['updatedAt'] as num?)?.toInt() ?? 0;
    return BoxPanel(onTap: () => box.remote ? openForm(context, const ProfileDetailsPage()) : profile == null
      ? openForm(context, const AddProfilePage()) : openForm(context, ProfileDetailsPage(profile: profile)),
      child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
        PanelTitle('Profile', AppGlyphs.profiles, trailing: Row(mainAxisSize: MainAxisSize.min, children: [
          if (profile?['remote'] == true) SizedBox.square(dimension: 26, child: _updating ? const Center(child: CommonCircleLoading(size: 18)) : IconButton(
            padding: EdgeInsets.zero, tooltip: context.tr('Update'), icon: const GlyphIcon(AppGlyphs.sync, size: 18), onPressed: () async {
              setState(() => _updating = true);
              await box.act('profiles.update', {'id': profile!['id']});
              if (mounted) setState(() => _updating = false);
            })),
          const GlyphIcon(AppGlyphs.chevronForward, size: 18),
        ])),
        const SizedBox(height: 12),
        Row(children: [
          Flexible(child: EmojiText(name?.toString() ?? context.tr('Add profile'), maxLines: 1, overflow: TextOverflow.ellipsis, style: AppTextStyle.headline.of(context, emphasized: true))),
          if (!box.remote && box.profiles.length > 1) SizedBox.square(dimension: 32, child: IconButton(padding: EdgeInsets.zero,
            tooltip: context.tr('Switch profile'), icon: const GlyphIcon(AppGlyphs.swap, size: 18),
            onPressed: box.transitioning ? null : () => openForm(context, const ProfilesPage(picker: true)))),
        ]),
        if (profile != null || box.remote) ...[
          Row(spacing: 12, children: [
            Expanded(child: Text(updated > 0 ? '${context.tr('Last updated')} ${localDate(updated)}' : '', maxLines: 1, overflow: TextOverflow.ellipsis, style: label)),
            Text(context.tr(box.remote || profile?['remote'] == true ? 'Remote' : 'Local'), style: label),
          ]),
          const SizedBox(height: 14),
          ProfileStats(profile: profile),
        ],
      ]),
    );
  }
}

class ProfileStats extends StatelessWidget {
  const ProfileStats({super.key, this.profile});
  final Json? profile;
  @override Widget build(BuildContext context) => FutureBuilder<Json>(
    future: profile == null ? null : context.box.profileStats((profile!['id'] as num).toInt()),
    builder: (context, snapshot) {
      final figure = AppTextStyle.title3.of(context, emphasized: true).toTabular.copyWith(letterSpacing: 0);
      final label = AppTextStyle.caption1.of(context).copyWith(color: context.colorScheme.onSurfaceVariant);
      return LayoutBuilder(builder: (context, constraints) => FittedBox(fit: BoxFit.scaleDown, alignment: AlignmentDirectional.centerStart,
        child: ConstrainedBox(constraints: BoxConstraints(minWidth: constraints.maxWidth), child: Row(mainAxisAlignment: MainAxisAlignment.spaceBetween, spacing: 16, children: [
          for (final (key, name) in [('groups', 'Proxy groups'), ('outbounds', 'Outbounds'), ('rules', 'Routing rules')])
            Column(mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.start, children: [
              if (snapshot.connectionState == ConnectionState.waiting) SkeletonText(width: 34, style: figure)
              else Text(snapshot.data?[key]?.toString() ?? '—', maxLines: 1, style: figure),
              Text(context.tr(name), maxLines: 1, style: label),
            ]),
        ])),
      ));
    },
  );
}
