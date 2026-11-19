import 'dart:async';
import 'package:flutter/services.dart';
import '../widgets.dart';
import '../components/profile_card.dart';

class WarpPage extends StatefulWidget {
  const WarpPage({super.key});
  @override State<WarpPage> createState() => _WarpPageState();
}

class _WarpPageState extends State<WarpPage> {
  static int _requestSequence = 0;
  static const _emptyNameError = 'Node name must not be empty';
  final _name = TextEditingController(text: 'WARP');
  String _protocol = 'masque', _transport = 'http3';
  BoxController? _box;
  String? _requestId;
  String? _error;
  Json? _pending, _saved;
  int? _session;
  int _snapshotEpoch = 0;
  bool _busy = false, _loading = false, _snapshotLoaded = false;
  bool _registeringElsewhere = false, _retrying = false;
  bool _canceling = false, _discarding = false;

  @override void didChangeDependencies() {
    super.didChangeDependencies();
    final box = context.box;
    _box ??= box;
    if (_session == box.sessionRevision) return;
    _session = box.sessionRevision;
    _snapshotEpoch++;
    _loading = false;
    _snapshotLoaded = false;
    _registeringElsewhere = false;
    _pending = null;
    _saved = null;
    _error = null;
    if (!box.remote) WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) unawaited(_refresh());
    });
  }

  @override void dispose() {
    final requestId = _requestId;
    if (requestId != null) unawaited(_box!.call('warp.cancel', {'requestId': requestId}).then<void>((_) {}, onError: (Object _) {}));
    _name.dispose();
    super.dispose();
  }

  bool _sameSession(BoxController box, int session) =>
    mounted && !box.remote && box.sessionRevision == session;

  // Only operation metadata reaches the page; registered credentials stay native.
  Json? _pendingMetadata(Object? value) {
    final data = json(value);
    if (data['pendingId'] is! String || (data['pendingId'] as String).isEmpty || data['profileId'] is! num) return null;
    return {
      'pendingId': data['pendingId'], 'profileId': (data['profileId'] as num).toInt(),
      'name': data['name'] is String ? data['name'] : 'WARP',
      'transport': switch (data['transport']) {
        'wireguard' => 'wireguard',
        'http2' => 'http2',
        _ => 'http3',
      },
    };
  }

  Future<void> _refresh() async {
    final box = context.box;
    if (_busy || _loading || box.remote) return;
    final session = box.sessionRevision, epoch = ++_snapshotEpoch;
    setState(() { _loading = true; _error = null; });
    try {
      final snapshot = json(await box.call('warp.snapshot'));
      if (!_sameSession(box, session) || epoch != _snapshotEpoch) return;
      setState(() {
        _pending = _pendingMetadata(snapshot['pending']);
        _registeringElsewhere = snapshot['registering'] == true;
        _snapshotLoaded = true;
      });
    } catch (error) {
      if (_sameSession(box, session) && epoch == _snapshotEpoch) setState(() => _error = BoxController.describe(error));
    } finally {
      if (mounted && epoch == _snapshotEpoch) setState(() => _loading = false);
    }
  }

  Future<String> _revision(BoxController box, int profileId) async {
    final nodes = json(await box.call('nodes.list', {'profileId': profileId}));
    final revision = nodes['profileRevision'];
    if (nodes['profileId'] != profileId || revision is! String || revision.isEmpty) {
      throw PlatformException(code: 'warp_revision', message: 'Configuration revision unavailable; reload before registering WARP');
    }
    return revision;
  }

  Future<void> _registerOrRetry() async {
    final box = context.box;
    if (_busy || _loading || !_snapshotLoaded || _registeringElsewhere || box.remote || box.transitioning) return;
    final pending = _pending;
    final profileId = pending?['profileId'] as int? ?? box.selectedProfileId;
    if (profileId < 0 || profileId != box.selectedProfileId || box.selectedProfile == null) return;
    final name = _name.text.trim();
    if (pending == null && name.isEmpty) {
      setState(() => _error = _emptyNameError);
      return;
    }
    final transport = _protocol == 'wireguard' ? 'wireguard' : _transport, session = box.sessionRevision;
    setState(() { _busy = true; _retrying = pending != null; _canceling = false; _error = null; _saved = null; });
    try {
      final revision = await _revision(box, profileId);
      if (!_sameSession(box, session)) return;
      if (box.selectedProfileId != profileId) {
        throw PlatformException(code: 'warp_profile', message: 'Selected profile changed; reload it before saving');
      }
      if (box.transitioning) {
        throw PlatformException(code: 'warp_service', message: 'Wait for the service to finish starting or stopping');
      }
      if (pending == null) setState(() => _requestId = '${DateTime.now().microsecondsSinceEpoch}-${++_requestSequence}');
      final result = json(await box.call(pending == null ? 'warp.register' : 'warp.retry', {
        'profileId': profileId, 'profileRevision': revision,
        if (pending != null) 'pendingId': pending['pendingId'] else ...{
          'requestId': _requestId,
          'name': name, 'transport': transport,
        },
      }));
      if (!_sameSession(box, session)) return;
      if (result['profileId'] != profileId || result['tag'] is! String) {
        throw PlatformException(code: 'warp_result', message: 'WARP save result unavailable; refresh registration status');
      }
      setState(() {
        _pending = null;
        _saved = {'profileId': profileId, 'tag': result['tag']};
      });
      box.messages.add(context.tr('WARP saved to configuration'));
    } catch (error) {
      if (!_sameSession(box, session)) return;
      setState(() {
        final pendingResult = error is PlatformException && error.code == 'warp_pending'
          ? _pendingMetadata(error.details) ?? pending : null;
        if (pendingResult != null) _pending = pendingResult;
        _error = pendingResult == null ? BoxController.describe(error) : null;
      });
    } finally {
      if (mounted) {
        final refreshSnapshot = _canceling || !_sameSession(box, session);
        setState(() { _busy = false; _requestId = null; _canceling = false; });
        if (refreshSnapshot) unawaited(_refresh());
      }
    }
  }

  Future<void> _cancel() async {
    final box = context.box, requestId = _requestId;
    if (requestId == null || !_busy || _retrying || _canceling || box.remote) return;
    final session = box.sessionRevision;
    setState(() => _canceling = true);
    try { await box.call('warp.cancel', {'requestId': requestId}); }
    catch (error) {
      if (_sameSession(box, session) && _requestId == requestId) setState(() { _error = BoxController.describe(error); _canceling = false; });
    }
  }

  Future<void> _discard() async {
    final box = context.box, pending = _pending;
    if (_busy || _loading || _registeringElsewhere || box.remote || pending == null) return;
    final session = box.sessionRevision;
    setState(() { _busy = true; _discarding = true; });
    try {
      if (!await confirm(context, 'Discard this unsaved WARP node?', action: 'Discard') || !_sameSession(box, session)) return;
      await box.call('warp.discard', {'pendingId': pending['pendingId']});
      if (_sameSession(box, session)) setState(() { _pending = null; _error = null; });
    } catch (error) {
      if (_sameSession(box, session)) setState(() => _error = BoxController.describe(error));
    } finally { if (mounted) setState(() { _busy = false; _discarding = false; }); }
  }

  String _profileName(BoxController box, int id) {
    final profile = box.profiles.where((profile) => profile['id'] == id).firstOrNull;
    return profile == null ? context.tr('Profile no longer exists') : profileDisplayName(context, profile);
  }

  String _protocolLabel(Object? value) => value == 'wireguard' ? 'WireGuard' : 'MASQUE';
  String _transportLabel(Object? value) => value == 'http2' ? 'HTTP/2' : 'HTTP/3';

  Widget _registrationForm(Json? profile) {
    final readOnly = _busy || _registeringElsewhere;
    final protocolTitle = Text(context.tr('WARP protocol'));
    final transportTitle = Text(context.tr('MASQUE transport'));
    return generateSection(
      title: context.tr('Node settings'),
      footer: _protocol == 'wireguard' ? context.tr('WireGuard is saved as an endpoint in the current configuration.') : null,
      items: [
        ListItem(
          title: Text(context.tr('Current profile')),
          subtitle: Text(profile == null ? context.tr('No profile') : profileDisplayName(context, profile)),
        ),
        DecorationListItem(
          minVerticalPadding: 12,
          title: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            spacing: 4,
            children: [
              Text(context.tr('Node name'), style: context.listSubtitleStyle),
              TextField(
                key: const ValueKey('warp-name'),
                controller: _name,
                readOnly: readOnly,
                autocorrect: false,
                decoration: InputDecoration(
                  isCollapsed: true,
                  border: const NoInputBorder(),
                  hintText: context.tr('Required'),
                  errorText: _error == _emptyNameError ? context.tr(_emptyNameError) : null,
                ),
                onChanged: (_) {
                  if (_error == _emptyNameError) setState(() => _error = null);
                },
              ),
            ],
          ),
        ),
        if (readOnly)
          ListItem(key: const ValueKey('warp-protocol'), title: protocolTitle, trailing: Text(_protocolLabel(_protocol)))
        else
          ListItem<String>.options(
            key: const ValueKey('warp-protocol'),
            title: protocolTitle,
            options: const ['masque', 'wireguard'],
            value: _protocol,
            textBuilder: _protocolLabel,
            onChanged: (value) {
              if (!_busy && !_registeringElsewhere) setState(() => _protocol = value);
            },
          ),
        if (_protocol == 'masque')
          if (readOnly)
            ListItem(title: transportTitle, trailing: Text(_transportLabel(_transport)))
          else
            ListItem<String>.options(
              title: transportTitle,
              options: const ['http3', 'http2'],
              value: _transport,
              textBuilder: _transportLabel,
              onChanged: (value) {
                if (!_busy && !_registeringElsewhere) setState(() => _transport = value);
              },
            ),
      ],
    );
  }

  @override Widget build(BuildContext context) {
    final box = context.box;
    if (box.remote) return BoxEditGuard(dirty: false, busy: _busy, child: const BoxPage(
      title: 'WARP registration',
      children: [EmptyState('Disconnect remote session to register WARP', glyph: AppGlyphs.cloudDownload)],
    ));

    final pending = _pending, saved = _saved, profile = box.selectedProfile;
    final target = pending?['profileId'] as int? ?? box.selectedProfileId;
    final targetAvailable = profile != null && target == box.selectedProfileId;
    final locked = _busy || _loading || _registeringElsewhere;
    final canSave = !locked && _snapshotLoaded && !box.transitioning && targetAvailable;
    final operation = switch ((_busy, _discarding, _canceling, _retrying)) {
      (false, _, _, _) => null,
      (_, true, _, _) => 'Discarding WARP node',
      (_, _, true, _) => 'Canceling WARP registration',
      (_, _, _, true) => 'Saving WARP configuration',
      _ => 'Registering WARP and saving configuration',
    };
    final notice = _registeringElsewhere ? 'WARP registration is already running'
      : box.transitioning ? 'Wait for the service to finish starting or stopping'
      : pending == null && profile == null ? 'Select a profile before registering WARP' : null;
    final error = _error == _emptyNameError ? null : _error;

    return BoxEditGuard(dirty: false, busy: _busy, child: BoxPage(
      title: 'WARP registration',
      loading: locked,
      actions: [IconButtonData(glyph: AppGlyphs.refresh, tooltip: context.tr('Refresh'), onPressed: _busy || _loading ? null : _refresh)],
      children: [
        if (saved != null) generateSection(items: [
          Semantics(liveRegion: true, child: ListItem(
            leading: GlyphIcon(AppGlyphs.checkCircle, color: context.colorScheme.success),
            title: Text(context.tr('WARP saved to configuration')),
            subtitle: Column(mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.start, children: [
              Text(_profileName(box, saved['profileId'] as int)),
              Text(saved['tag'] as String),
            ]),
          )),
        ]),
        if (pending != null)
          generateSection(
            footer: context.tr(targetAvailable ? 'Retry saves the registered node to its original configuration.'
              : 'Select the original profile before retrying WARP'),
            items: [
              Semantics(liveRegion: true, child: ListItem(
                leading: GlyphIcon(AppGlyphs.warning, color: context.colorScheme.warning),
                title: Text(context.tr('WARP node awaiting save')),
              )),
              ListItem(title: Text(context.tr('Profile')), subtitle: Text(_profileName(box, target))),
              ListItem(title: Text(context.tr('Node name')), subtitle: Text(pending['name'] as String)),
              ListItem(title: Text(context.tr('WARP protocol')), trailing: Text(_protocolLabel(pending['transport']))),
              if (pending['transport'] != 'wireguard')
                ListItem(title: Text(context.tr('MASQUE transport')), trailing: Text(_transportLabel(pending['transport']))),
            ],
          )
        else
          _registrationForm(profile),
        if (error != null || notice != null || operation != null) generateSection(items: [
          if (error != null) Semantics(liveRegion: true, child: ListItem(
            leading: GlyphIcon(AppGlyphs.error, color: context.colorScheme.error),
            title: Text(context.tr(error)),
          )),
          if (notice != null) ListItem(leading: const GlyphIcon(AppGlyphs.info), title: Text(context.tr(notice))),
          if (operation != null) Semantics(liveRegion: true, child: ListItem(
            leading: const ActivityIndicator(size: 18),
            title: Text(context.tr(operation)),
          )),
        ]),
        Padding(
          padding: const EdgeInsets.only(top: 16),
          child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, spacing: 4, children: [
            FilledButton.icon(
              key: const ValueKey('warp-register'),
              onPressed: canSave ? _registerOrRetry : null,
              icon: GlyphIcon(pending == null ? AppGlyphs.cloudDownload : AppGlyphs.check),
              label: Text(context.tr(pending == null ? 'Register and save' : 'Retry saving'), textAlign: TextAlign.center),
            ),
            if (pending == null) Padding(
              padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 8),
              child: Text(
                context.tr('Registration creates a device account with Cloudflare and writes it to the current configuration file.'),
                key: const ValueKey('warp-registration-description'),
                style: context.listSubtitleStyle,
                textAlign: TextAlign.center,
              ),
            ),
            if (_busy && _requestId != null && !_retrying) TextButton(
              key: const ValueKey('warp-cancel'),
              onPressed: _canceling ? null : _cancel,
              child: Text(context.tr('Cancel registration')),
            ),
            if (pending != null) TextButton(
              key: const ValueKey('warp-discard'),
              onPressed: locked ? null : _discard,
              child: Text(context.tr('Discard')),
            ),
          ]),
        ),
        generateSection(items: [
          ListItem(
            leading: const GlyphIcon(AppGlyphs.link),
            title: Text(context.tr('Cloudflare WARP terms of service')),
            trailing: const GlyphIcon(AppGlyphs.openExternal),
            onTap: _busy ? null : () => box.act('url.open', {'url': 'https://www.cloudflare.com/application/terms/'}),
          ),
        ]),
      ],
    ));
  }
}
