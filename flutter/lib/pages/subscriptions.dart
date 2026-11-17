import 'package:flutter/services.dart';
import '../widgets.dart';
import '../components/form.dart';
import '../components/profile_card.dart';

bool profileHasUpdates(Json profile) => profile['remote'] == true || rows(profile['subscriptions']).isNotEmpty;

/// Each source owns its nodes, so updating one source keeps the others.
Future<void> updateProfileSubscriptions(BoxController box, Json profile, {bool Function()? canContinue}) async {
  final profileId = profile['id'], session = box.sessionRevision;
  bool active() => !box.remote && box.sessionRevision == session && (canContinue?.call() ?? true);
  if (!active()) return;
  if (profile['remote'] == true) await box.act('profiles.update', {'id': profileId});
  for (final source in rows(profile['subscriptions'])) {
    if (!active()) break;
    await box.act('subscriptions.update', {'profileId': profileId, 'id': source['id']});
  }
}

/// Pin the file and revision before opening the form or starting a download.
Future<bool> openSubscriptionImport(BuildContext context, [Json initial = const {}]) async {
  final box = context.box;
  if (box.remote) return false;
  final profile = box.selectedProfile;
  if (profile == null) { box.report(StateError(context.tr('Select a profile first'))); return false; }
  final profileId = box.selectedProfileId, snapshot = box.selectedProfileNodes;
  final profileName = profileDisplayName(context, profile);
  final revision = snapshot['profileId'] == profileId ? snapshot['profileRevision'] : null;
  try {
    final profileRevision = revision is String && revision.isNotEmpty ? revision
      : json(await box.call('nodes.list', {'profileId': profileId}))['profileRevision'];
    if (profileRevision is! String || profileRevision.isEmpty) throw StateError(context.tr('Profile revision unavailable'));
    if (!context.mounted) return false;
    return await openForm<bool>(context, SubscriptionForm(
      profileId: profileId, profileRevision: profileRevision, profileName: profileName, initial: initial,
    )) ?? false;
  } catch (error) { box.report(error); return false; }
}

class SubscriptionForm extends StatefulWidget {
  const SubscriptionForm({super.key, required this.profileId, required this.profileName,
    this.profileRevision, this.initial = const {}});
  final int profileId;
  final String profileName;
  final String? profileRevision;
  final Json initial;
  @override State<SubscriptionForm> createState() => _SubscriptionFormState();
}

class _SubscriptionFormState extends State<SubscriptionForm> {
  late final Json _initial;
  late final int _profileId;
  late final String _profileName;
  late final String? _profileRevision;
  late final Object? _sourceId;
  late final _name = TextEditingController(text: _initial['name'] as String? ?? '');
  late final _url = TextEditingController(text: _initial['url'] as String? ?? '');
  late final _interval = TextEditingController(text: (_initial['interval'] ?? 60).toString());
  late bool _auto = _initial['autoUpdate'] != false;
  bool _saving = false, _attempted = false, _saved = false;
  bool get _editing => _sourceId != null;
  bool get _dirty => _name.text != (_initial['name'] as String? ?? '') ||
    _url.text != (_initial['url'] as String? ?? '') ||
    _interval.text != (_initial['interval'] ?? 60).toString() || _auto != (_initial['autoUpdate'] != false);

  @override void initState() {
    super.initState();
    _initial = Map<String, dynamic>.of(widget.initial);
    _profileId = widget.profileId;
    _profileName = widget.profileName;
    _profileRevision = widget.profileRevision;
    _sourceId = _initial['id'];
  }

  ({String field, String message})? get _issue {
    if (_name.text.trim().isEmpty) return (field: 'name', message: context.tr('Name') + ': ' + context.tr('Required'));
    final uri = Uri.tryParse(_url.text.trim());
    if (uri == null || !['http', 'https'].contains(uri.scheme) || uri.host.isEmpty) {
      return (field: 'url', message: context.tr('Node subscription URL'));
    }
    if (_auto && (int.tryParse(_interval.text) ?? 0) < 15) {
      return (field: 'interval', message: context.tr('At least 15 minutes'));
    }
    return null;
  }
  void _changed(String _) { setState(() {}); }
  @override void dispose() { _name.dispose(); _url.dispose(); _interval.dispose(); super.dispose(); }

  Future<void> _save() async {
    if (_saving || _saved) return;
    if (_issue != null) { setState(() => _attempted = true); return; }
    final box = context.box, imported = context.tr('Nodes imported');
    if (box.remote) return;
    setState(() => _saving = true);
    try {
      final interval = int.tryParse(_interval.text);
      final previousInterval = (_initial['interval'] as num?)?.toInt() ?? 60;
      final result = await box.call(_editing ? 'subscriptions.save' : 'subscriptions.import', {
        'profileId': _profileId,
        if (_editing) 'id': _sourceId else 'profileRevision': _profileRevision,
        'name': _name.text.trim(), 'url': _url.text.trim(), 'autoUpdate': _auto,
        'interval': interval != null && interval >= 15 ? interval : previousInterval >= 15 ? previousInterval : 60,
      });
      if (!_editing) box.messages.add('$imported: ${json(result)['added']}');
      if (mounted) {
        setState(() => _saved = true);
        if (ModalRoute.of(context)?.isCurrent != false) context.safeNestedPop(true);
      }
    } catch (error) { box.report(error); }
    if (mounted) setState(() => _saving = false);
  }

  @override Widget build(BuildContext context) {
    final remote = context.box.remote;
    final issue = _attempted ? _issue : null;
    return BoxEditGuard(dirty: !_saved && _dirty, busy: _saving,
      child: remote ? const BoxPage(title: 'Node subscriptions',
        children: [EmptyState('Disconnect remote session to edit local profiles')])
      : FormPage(title: _editing ? 'Edit subscription' : 'Import from URL',
      saving: _saving || _saved, onSave: _save, error: issue?.message,
      children: [
        generateSection(items: [
          ListItem(title: Text(context.tr('Profile')), subtitle: Text(_profileName)),
          FormRow(title: context.tr('Name'), invalid: issue?.field == 'name',
            trailing: FormRowTextField(controller: _name, hintText: context.tr('Required'),
              textInputAction: TextInputAction.next, onChanged: _changed)),
          FormRow(title: context.tr('URL'), invalid: issue?.field == 'url',
            trailing: FormRowTextField(controller: _url, hintText: context.tr('Node subscription URL'),
              keyboardType: TextInputType.url, textInputAction: TextInputAction.next,
              autocorrect: false, enableSuggestions: false, onChanged: _changed)),
        ], footer: context.tr('Supports Clash, sing-box JSON, node links and Base64. Only proxy nodes are imported from the URL; updates keep other profile settings.')),
        generateSection(items: [
          FormRow(title: context.tr('Auto update'), onPressed: () => setState(() => _auto = !_auto),
            trailing: Switch(value: _auto, onChanged: (value) => setState(() => _auto = value))),
          if (_auto) FormRow(title: context.tr('Update interval'), invalid: issue?.field == 'interval',
            trailing: Row(mainAxisSize: MainAxisSize.min, spacing: 4, children: [
              Flexible(child: FormRowTextField(controller: _interval, hintText: context.tr('Required'),
                keyboardType: TextInputType.number, textInputAction: TextInputAction.done,
                inputFormatters: [FilteringTextInputFormatter.digitsOnly], onChanged: _changed)),
              Text(context.tr('minutes'), style: AppTextStyle.subheadline.of(context)),
            ])),
        ], footer: context.tr('Updates replace this subscription\'s nodes and keep other sources and profile settings.')),
        if (_editing) generateSection(items: [
          ListItem(title: Text(context.tr('Last updated')), subtitle: Text(
            (_initial['updatedAt'] as num? ?? 0) <= 0 ? context.tr('Unknown') : localDate(_initial['updatedAt']))),
        ]),
      ],
    ));
  }
}

class SubscriptionsPage extends StatefulWidget {
  const SubscriptionsPage({super.key, required this.profile});
  final Json profile;
  @override State<SubscriptionsPage> createState() => _SubscriptionsPageState();
}

class _SubscriptionsPageState extends State<SubscriptionsPage> {
  List<Json>? _sources;
  Object? _error;
  final _busy = <String>{};
  int _revision = -1, _sessionRevision = -1, _request = 0;
  bool _loading = false, _adding = false;
  late final int _profileId;

  @override void initState() {
    super.initState();
    _profileId = (widget.profile['id'] as num).toInt();
  }

  @override void didChangeDependencies() {
    super.didChangeDependencies();
    final box = context.box;
    if (_revision == box.profilesRevision && _sessionRevision == box.sessionRevision) return;
    _revision = box.profilesRevision;
    _sessionRevision = box.sessionRevision;
    _load();
  }

  Future<void> _load() async {
    final box = context.box, request = ++_request, session = context.box.sessionRevision;
    if (box.remote) { setState(() => _loading = false); return; }
    bool current() => mounted && request == _request && !box.remote && session == box.sessionRevision;
    setState(() { _loading = true; _error = null; });
    try {
      final sources = rows(await box.call('subscriptions.list', {'profileId': _profileId}));
      if (current()) setState(() => _sources = sources);
    } catch (error) {
      if (current()) setState(() => _error = error);
    } finally {
      if (current()) setState(() => _loading = false);
    }
  }

  Future<void> _run(Json source, Future<void> Function(BoxController) action) async {
    final box = context.box, id = source['id'].toString();
    if (box.remote || _busy.isNotEmpty || _adding) return;
    setState(() => _busy.add(id));
    try { await action(box); }
    catch (error) { box.report(error); }
    finally { if (mounted) setState(() => _busy.remove(id)); }
  }

  Future<void> _edit(Json source) => _run(source, (box) async {
    final profile = box.profiles.where((p) => p['id'] == _profileId).firstOrNull ?? widget.profile;
    final saved = await openForm<bool>(context, SubscriptionForm(
      profileId: _profileId, profileName: profileDisplayName(context, profile), initial: source,
    ));
    if (saved == true && mounted) await _load();
  });

  Future<void> _update(Json source) => _run(source, (box) async {
    await box.call('subscriptions.update', {'profileId': _profileId, 'id': source['id']});
    if (mounted) await _load();
  });

  Future<void> _delete(Json source) => _run(source, (box) async {
    if (!await confirm(context, 'Remove this subscription?',
      message: 'Its nodes will stay in this profile as local nodes.')) return;
    if (!mounted || box.remote) return;
    await box.call('subscriptions.delete', {'profileId': _profileId, 'id': source['id']});
    if (mounted) await _load();
  });

  Future<void> _add() async {
    if (_adding || _busy.isNotEmpty || context.box.selectedProfileId != _profileId || context.box.remote) return;
    setState(() => _adding = true);
    try { if (await openSubscriptionImport(context) && mounted) await _load(); }
    finally { if (mounted) setState(() => _adding = false); }
  }

  @override Widget build(BuildContext context) {
    final box = context.box;
    final busy = _adding || _busy.isNotEmpty;
    if (box.remote) return const BoxPage(title: 'Node subscriptions',
      children: [EmptyState('Disconnect remote session to edit local profiles')]);
    if (!box.profiles.any((p) => p['id'] == _profileId)) return const BoxPage(title: 'Node subscriptions',
      children: [EmptyState('Profile no longer exists')]);
    return BoxPage(title: 'Node subscriptions', loading: _loading,
      actions: [
        IconButtonData(glyph: AppGlyphs.addCircle, tooltip: context.tr('Import from URL'),
          onPressed: busy || box.selectedProfileId != _profileId ? null : _add),
        IconButtonData(glyph: AppGlyphs.sync, tooltip: context.tr('Refresh'), onPressed: _loading ? null : _load),
      ], children: [
        if (_error != null) ErrorState(_error!, retry: _load)
        else if (_sources != null && _sources!.isEmpty) const EmptyState('No node subscriptions',
          message: 'Import a URL into the selected profile to keep its subscription source.')
        else if (_sources != null) generateSection(items: [
          for (final source in _sources!) ListItem(
            title: Text(source['name'].toString()),
            subtitle: Text([
              source['url'].toString(),
              '${context.tr('Nodes')}: ${(source['tags'] as List?)?.length ?? 0}',
              '${context.tr('Last updated')}: ${(source['updatedAt'] as num? ?? 0) <= 0 ? context.tr('Unknown') : localDate(source['updatedAt'])}',
              '${context.tr('Auto update')}: ${context.tr(source['autoUpdate'] == true ? 'Turn on' : 'Turn off')}',
            ].join('\n')),
            trailing: BoxMenu(items: [
              PopupMenuItem(glyph: AppGlyphs.edit, label: context.tr('Edit'),
                onPressed: busy ? null : () => _edit(source)),
              PopupMenuItem(glyph: AppGlyphs.sync, label: context.tr('Update'),
                onPressed: busy ? null : () => _update(source)),
              PopupMenuItem(danger: true, glyph: AppGlyphs.delete, label: context.tr('Delete'),
                onPressed: busy ? null : () => _delete(source)),
            ]),
          ),
        ], footer: box.selectedProfileId == _profileId ? null : context.tr('Select this profile to add a subscription.')),
      ],
    );
  }
}
