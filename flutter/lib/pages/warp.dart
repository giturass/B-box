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
  final _name = TextEditingController(text: 'WARP');
  String _transport = 'http3';
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
      'transport': data['transport'] == 'http2' ? 'http2' : 'http3',
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
      setState(() => _error = 'Node name must not be empty');
      return;
    }
    final transport = _transport, session = box.sessionRevision;
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
        _error = BoxController.describe(error);
        if (error is PlatformException && error.code == 'warp_pending') {
          _pending = _pendingMetadata(error.details) ?? pending;
        }
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

  @override Widget build(BuildContext context) {
    final box = context.box;
    if (box.remote) return BoxEditGuard(dirty: false, busy: _busy, child: const BoxPage(
      title: 'WARP registration', children: [EmptyState('Disconnect remote session to register WARP')],
    ));
    final pending = _pending, saved = _saved;
    final target = pending?['profileId'] as int? ?? box.selectedProfileId;
    final canSave = !_busy && !_loading && _snapshotLoaded && !_registeringElsewhere && !box.transitioning &&
      box.selectedProfile != null && target == box.selectedProfileId;
    return BoxEditGuard(dirty: false, busy: _busy, child: BoxPage(
      title: 'WARP registration', loading: _busy || _loading || _registeringElsewhere,
      actions: [IconButtonData(glyph: AppGlyphs.refresh, tooltip: context.tr('Refresh'), onPressed: _busy || _loading ? null : _refresh)],
      children: [
        ListFooter(text: context.tr('Register a WARP MASQUE node and add it to the selected configuration.')),
        if (_error != null) Padding(padding: const EdgeInsets.all(16),
          child: Text(context.tr(_error!), style: context.listSubtitleStyle.copyWith(color: context.colorScheme.error))),
        if (pending != null) generateSection(title: context.tr('Registration succeeded; configuration was not saved'),
          footer: context.tr('Retry saves the registered node to its original configuration.'), items: [
            ListItem(title: Text(context.tr('Profile')), subtitle: Text(_profileName(box, target))),
            ListItem(title: Text(context.tr('Node name')), subtitle: Text(pending['name'] as String)),
            ListItem(title: Text(context.tr('MASQUE transport')), trailing: Text(pending['transport'] == 'http2' ? 'HTTP/2' : 'HTTP/3')),
          ]),
        if (saved != null) generateSection(title: context.tr('WARP saved to configuration'), items: [
          ListItem(title: Text(_profileName(box, saved['profileId'] as int)), subtitle: Text(saved['tag'] as String)),
        ]),
        if (pending == null) generateSection(items: [
          ListItem(title: Text(context.tr('Current profile')), subtitle: Text(box.selectedProfile == null
            ? context.tr('No profile') : profileDisplayName(context, box.selectedProfile!))),
          Padding(padding: const EdgeInsets.all(16), child: BoxField(label: context.tr('Node name'),
            child: TextField(key: const ValueKey('warp-name'), controller: _name, readOnly: _busy || _registeringElsewhere,
              autocorrect: false, onChanged: (_) { if (_error != null) setState(() => _error = null); }))),
          AbsorbPointer(absorbing: _busy || _registeringElsewhere, child: ListItem<String>.options(
            title: Text(context.tr('MASQUE transport')), options: const ['http3', 'http2'],
            value: _transport, textBuilder: (value) => value == 'http2' ? 'HTTP/2' : 'HTTP/3',
            onChanged: (value) => setState(() => _transport = value))),
        ]),
        if (_registeringElsewhere) ListFooter(text: context.tr('WARP registration is already running')),
        if (box.transitioning) ListFooter(text: context.tr('Wait for the service to finish starting or stopping')),
        if (box.selectedProfile == null) ListFooter(text: context.tr('Select a profile before registering WARP')),
        if (pending != null && target != box.selectedProfileId) ListFooter(text: context.tr('Select the original profile before retrying WARP')),
        Padding(padding: const EdgeInsets.symmetric(vertical: 16), child: FilledButton(
          key: const ValueKey('warp-register'), onPressed: canSave ? _registerOrRetry : null,
          child: Text(context.tr(_busy ? _discarding ? 'Discarding WARP node' : _canceling ? 'Canceling WARP registration' : _retrying ? 'Saving WARP configuration' : 'Registering WARP and saving configuration'
            : pending == null ? 'Register and save' : 'Retry saving')),
        )),
        if (_busy && _requestId != null && !_retrying) TextButton(key: const ValueKey('warp-cancel'),
          onPressed: _canceling ? null : _cancel, child: Text(context.tr('Cancel registration'))),
        if (pending != null) TextButton(key: const ValueKey('warp-discard'), onPressed: _busy || _loading || _registeringElsewhere ? null : _discard,
          child: Text(context.tr('Discard'))),
        if (pending == null) ...[
          ListFooter(text: context.tr('Registering creates a device account with Cloudflare and saves the node to this configuration.')),
          TextButton(onPressed: _busy ? null : () => box.act('url.open', {'url': 'https://www.cloudflare.com/application/terms/'}),
            child: Text(context.tr('Cloudflare WARP terms of service'), style: context.listSubtitleStyle.copyWith(color: context.colorScheme.primary))),
        ],
      ],
    ));
  }
}
