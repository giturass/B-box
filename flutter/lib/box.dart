import 'dart:async';
import 'package:collection/collection.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/painting.dart' show MemoryImage, PaintingBinding;
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'common/ip_quality_cache.dart';
import 'common/app_icon_cache.dart';

typedef Json = Map<String, dynamic>;

Json json(Object? value) => value is Map
    ? value.map((key, value) => MapEntry(key.toString(), value))
    : <String, dynamic>{};
List<Json> rows(Object? value) => value is List ? value.map(json).toList() : [];

enum BoxAspect { state, appearance, groups, traffic, memory, connections, logs, tools, diagnostics, transfers, terminal, updates }
enum BoxData { memory, logs, connections, tools, outbounds, lan }

/// One Android delivery retains its identity until its acknowledgement is durable.
class BoxIntent {
  BoxIntent(this.id, this.uri);
  final String id, uri;
  bool consumed = false;

  static BoxIntent? fromWire(Object? value) {
    final input = json(value), id = input['id'], uri = input['uri'];
    return id is String && id.isNotEmpty && uri is String && uri.isNotEmpty
        ? BoxIntent(id, uri) : null;
  }
}

class BoxController extends ChangeNotifier with WidgetsBindingObserver {
  BoxController({MethodChannel? channel, Stream<dynamic>? events})
      : _channel = channel ?? const MethodChannel('io.ericlee.bfa/box'),
        _events = events ?? const EventChannel('io.ericlee.bfa/events').receiveBroadcastStream();

  final MethodChannel _channel;
  final Stream<dynamic> _events;
  final messages = StreamController<String>.broadcast();
  final intents = StreamController<String>.broadcast();
  final intentRecovery = StreamController<void>.broadcast();
  final browserEvents = StreamController<Json>.broadcast();
  StreamSubscription<dynamic>? _subscription;
  final _revisions = List<int>.filled(BoxAspect.values.length, 0);
  final _eventRevisions = <String, int>{};
  final _stateChanges = ChangeNotifier();
  final _dataOwners = <BoxData, Set<Object>>{};
  Listenable get stateChanges => _stateChanges;
  Json dashboard = {}, settings = {}, preferences = {}, rates = {};
  Json toolState = {}, stun = {}, quality = {}, inbox = {}, ping = {};
  Json terminal = {};
  Json appUpdate = {'status': 'idle'};
  Json memory = {}, lan = {};
  /// The selected profile's nodes, stopped groups and revision, without credentials.
  Json _selectedProfileNodes = {};
  List<Json> _profileGroups = [];
  Json get selectedProfileNodes => _selectedProfileNodes;
  set selectedProfileNodes(Json value) {
    _selectedProfileNodes = value;
    _profileGroups = _normalizeGroups(value['groups'], _profileGroups);
  }
  List<Json> transfers = [], usbDevices = [];
  List<Json> outbounds = [];
  List<Json> profiles = [], groups = [], connections = [], logs = [];
  final _connectionsById = <String, Json>{};
  String? defaultLogLevel;
  final uploadHistory = <double>[];
  final downloadHistory = <double>[];
  String version = '', coreVersion = '';
  int systemAccentColor = 0xff007aff;
  int sampleRevision = 0;
  int profilesRevision = 0;
  int sessionRevision = 0;
  bool _disposed = false;
  bool _observingLifecycle = false;
  int _backendRefresh = 0;
  int _nativeRecovery = 0;
  bool _startupInterrupted = false;
  bool _takingIntents = false, _takeIntentsAgain = false;
  bool ready = false;
  bool lanChanging = false;
  bool serviceSettingsChanging = false;
  String? startupError;
  final pendingIntents = <BoxIntent>[];
  final _ownedIntents = <String, BoxIntent>{};
  final _completedIntentIds = <String>{};
  String? applyRequired;
  late final appIcons = AppIconCache(
    load: (package) async => await call('apps.icon', {'package': package}) as Uint8List?,
    onEvict: (bytes) => PaintingBinding.instance.imageCache.evict(MemoryImage(bytes), includeLive: false),
  );
  final _profileStats = <int, Future<Json>>{};
  late final ipQualities = IpQualityCache(
    read: (ip) => call('dashboard.qualityCache.get', {'ip': ip}),
    write: (ip, record) async { await call('dashboard.qualityCache.put', {'ip': ip, 'value': record}); },
    onCacheError: (error) => debugPrint('IP quality cache: ${describe(error)}'),
  );

  void _notify(Iterable<BoxAspect> aspects) {
    if (_disposed) return;
    for (final aspect in aspects) { _revisions[aspect.index]++; }
    if (aspects.contains(BoxAspect.state)) _stateChanges.notifyListeners();
    super.notifyListeners();
  }

  // Preserve explicit notifications for callers that mutate several fields.
  @override void notifyListeners() => _notify(BoxAspect.values);

  void setDataVisible(BoxData data, Object owner, bool visible) {
    if (_disposed) return;
    final owners = _dataOwners.putIfAbsent(data, () => <Object>{});
    final wasVisible = owners.isNotEmpty;
    if (visible) { owners.add(owner); } else { owners.remove(owner); }
    if (ready && wasVisible != owners.isNotEmpty) _syncData(data);
  }

  void _syncData(BoxData data) {
    final method = switch (data) {
      BoxData.memory => 'dashboard.memory.visible',
      BoxData.lan => 'dashboard.lan.visible',
      _ => '${data.name}.visible',
    };
    unawaited(act(method,
      {'visible': _dataOwners[data]?.isNotEmpty == true}));
  }

  Future<Json> profileStats(int id) => _profileStats.putIfAbsent(id, () async => json(await call('profiles.stats', {'id': id})));

  Future<Uint8List?> appIcon(String package) => appIcons.get(package);

  List<Json> _normalizeGroups(Object? value, List<Json> source) {
    final previous = {for (final group in source) group['tag']: group};
    final next = <Json>[];
    for (final group in rows(value)) {
      final old = previous[group['tag']];
      final oldItems = old?['items'] as List<Json>? ?? const <Json>[];
      final oldNodes = {for (final node in oldItems) node['tag']: node};
      final items = <Json>[];
      for (final node in rows(group['items'])) {
        final before = oldNodes[node['tag']];
        items.add(before != null && const MapEquality<String, dynamic>().equals(before, node) ? before : node);
      }
      group['items'] = listEquals(oldItems, items) ? oldItems : items;
      next.add(old != null && const MapEquality<String, dynamic>().equals(old, group) ? old : group);
    }
    return listEquals(source, next) ? source : next;
  }

  bool _setGroups(Object? value) {
    final next = _normalizeGroups(value, groups);
    if (identical(groups, next)) return false;
    final selections = {for (final group in groups) group['tag']: group['selected']};
    groups = next;
    // Route observers also run without a Flutter frame, but latency and group
    // order changes must not rebuild unrelated pages or invalidate the egress IP.
    if (!const MapEquality().equals(selections, {for (final group in groups) group['tag']: group['selected']})) {
      _stateChanges.notifyListeners();
    }
    return true;
  }

  bool get remote => dashboard['remote'] != null;
  Object? get connectionTarget => _targetOf(dashboard);
  static Object? _targetOf(Json value) => value['remoteId'] ?? value['remote'];

  /// Replaces the dashboard and returns whether it belongs to another session.
  bool _setDashboard(Json value) {
    final sessionChanged = connectionTarget != _targetOf(value);
    if (sessionChanged) {
      sessionRevision++;
      _connectionsById.clear();
      connections = [];
    }
    dashboard = value;
    return sessionChanged;
  }

  bool get running => remote
      ? dashboard['remoteConnected'] == true
      : dashboard['status'] == 'Started';
  bool get transitioning => ['Starting', 'Stopping'].contains(dashboard['status']);
  bool get canSelectProxy => !transitioning && (running || !remote && dashboard['status'] == 'Stopped');
  /// Remote sessions always use their own live groups, including while disconnected.
  List<Json> get proxyGroups => running || remote ? groups :
      selectedProfileNodes['profileId'] == selectedProfileId ? _profileGroups : const [];
  int get selectedProfileId => (dashboard['selectedProfileId'] as num?)?.toInt() ?? -1;
  Json? get selectedProfile {
    for (final profile in profiles) {
      if (profile['id'] == selectedProfileId) return profile;
    }
    return null;
  }

  List<String> get lanAddresses => (lan['addresses'] as List? ?? []).whereType<String>().toList();
  bool get canSetLanProxy => !remote && !transitioning && !lanChanging &&
      lan['canToggle'] == true && lan['profileId'] == selectedProfileId;
  List<String> get lanProxyEndpoints => remote || !running || lan['running'] != true ? [] :
      rows(lan['activeBindings']).expand((binding) =>
          (binding['endpoints'] as List? ?? []).whereType<String>()).toSet().toList();

  String? get lanCopyText {
    final addresses = lanAddresses, endpoints = lanProxyEndpoints;
    for (final address in addresses) {
      for (final endpoint in endpoints) {
        if (endpoint.startsWith('$address:') || endpoint.startsWith('[$address]:')) return endpoint;
      }
    }
    return endpoints.isNotEmpty ? endpoints.first : addresses.isNotEmpty ? addresses.first : null;
  }

  Future<void> refreshLan() async {
    try {
      final value = await call('dashboard.lan.refresh');
      if (!_disposed && value is Map) { lan = json(value); _notify([BoxAspect.state]); }
    } catch (error) { report(error); }
  }

  Future<bool> setLanProxy(bool enabled, {int? port, int? profileId, String? revision}) async {
    if (!canSetLanProxy) return false;
    final target = profileId ?? selectedProfileId;
    if (target != selectedProfileId) {
      report(StateError('Selected profile changed; reload it before saving'));
      return false;
    }
    lanChanging = true;
    _notify([BoxAspect.state]);
    try {
      final value = json(await call('dashboard.lan.set', {
        'profileId': target, 'revision': revision ?? lan['revision'], 'enabled': enabled,
        if (port != null) 'port': port,
      }));
      if (!_disposed && !remote && selectedProfileId == target) lan = value;
      return true;
    } catch (error) {
      report(error);
      if (!_disposed) await refreshLan();
      return false;
    } finally {
      lanChanging = false;
      if (!_disposed) _notify([BoxAspect.state]);
    }
  }

  Future<void> initialize() async {
    startupError = null;
    notifyListeners();
    if (!_observingLifecycle) {
      WidgetsBinding.instance.addObserver(this);
      _observingLifecycle = true;
    }
    await _subscription?.cancel();
    _subscription = _events.listen(_onEvent, onError: report);
    await _refreshBackend();
  }

  Future<void> _refreshBackend() async {
    final refresh = ++_backendRefresh;
    final recovery = _nativeRecovery;
    var retryAfterRecovery = false;
    _startupInterrupted = false;
    startupError = null;
    try {
      final before = Map<String, int>.of(_eventRevisions);
      final result = json(await call('ready'));
      if (_disposed || refresh != _backendRefresh) return;
      bool unchanged(String type) => _eventRevisions[type] == before[type];
      version = result['version'] as String;
      coreVersion = result['coreVersion'] as String;
      systemAccentColor = (result['systemAccentColor'] as num?)?.toInt() ?? 0xff007aff;
      if (unchanged('dashboard')) _setDashboard(json(result['dashboard']));
      if (unchanged('profiles')) profiles = rows(result['profiles']);
      if (unchanged('nodes')) selectedProfileNodes = json(result['nodes']);
      if (unchanged('settings')) settings = json(result['settings']);
      if (unchanged('preferences')) preferences = json(result['preferences']);
      if (unchanged('lan') && result['lan'] is Map) lan = json(result['lan']);
      if (unchanged('tools') && result['tools'] is Map) toolState = json(result['tools']);
      if (unchanged('updates') && result['updates'] is Map) appUpdate = json(result['updates']);
      final diagnostics = json(result['diagnostics']);
      if (unchanged('stun') && diagnostics['stun'] is Map) stun = json(diagnostics['stun']);
      if (unchanged('quality') && diagnostics['quality'] is Map) quality = json(diagnostics['quality']);
      _acceptIntent(result['intent']);
      ready = true;
      for (final data in _dataOwners.keys) { _syncData(data); }
      // ready takes one native input. Continue through restored queues even
      // when that first input already belongs to the currently open form.
      if (result['intent'] != null || _takingIntents) unawaited(_takeIntent());
      intentRecovery.add(null);
    } catch (error) {
      if (_disposed || refresh != _backendRefresh) return;
      startupError = describe(error);
      _startupInterrupted = error is PlatformException &&
          (error.code == 'cancelled' || error.code == 'ui_bridge');
      if (ready && !_startupInterrupted) report(error);
      // Recovery can arrive before a cancelled ready response. Retry that
      // newer confirmation once, including a cold UI that has no Home yet.
      retryAfterRecovery = _startupInterrupted && recovery != _nativeRecovery;
    }
    notifyListeners();
    if (retryAfterRecovery && !_disposed && refresh == _backendRefresh) unawaited(_refreshBackend());
  }

  Future<dynamic> call(String method, [Json? args]) => _channel.invokeMethod<dynamic>(method, args);

  /// A late method response must not replace newer download/installer events.
  Future<void> _requestAppUpdate(String method, [Json? args]) async {
    final before = _eventRevisions['updates'];
    final value = await call(method, args);
    if (_disposed || value is! Map || _eventRevisions['updates'] != before) return;
    final aspects = _setAppUpdate(json(value));
    if (aspects.isNotEmpty) _notify(aspects);
  }

  Future<void> downloadUpdate(int versionCode) =>
      _requestAppUpdate('updates.download', {'versionCode': versionCode});
  Future<void> cancelUpdate() => _requestAppUpdate('updates.cancel');
  Future<void> installUpdate(int versionCode) =>
      _requestAppUpdate('updates.install', {'versionCode': versionCode});

  Future<void> refreshUpdates() async {
    try { await _requestAppUpdate('updates.snapshot'); }
    catch (error) { report(error); }
  }

  @override void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state != AppLifecycleState.resumed || !ready) return;
    unawaited(_takeIntent());
    if (appUpdate['status'] != 'idle') {
      // Android permission and installer screens only report their confirmed
      // state. Returning to the app never automatically installs an APK.
      unawaited(refreshUpdates());
    }
  }

  void clearApplyRequired() { applyRequired = null; _notify([BoxAspect.state]); }

  Future<bool> act(String method, [Json? args]) async {
    try {
      await call(method, args);
      return true;
    } catch (error) {
      report(error);
      return false;
    }
  }

  Future<bool> setPreference(String key, dynamic value) => setPreferences({key: value});

  Future<bool> setPreferences(Json values) async {
    try {
      preferences = json(await call('preferences.set', {'value': values}));
      _notify([BoxAspect.state, BoxAspect.appearance]);
      return true;
    } catch (error) {
      report(error);
      return false;
    }
  }

  Future<void> setSetting(String key, dynamic value, {bool restart = true, bool autoRestart = false}) async {
    if (serviceSettingsChanging || autoRestart && transitioning && !remote) return;
    final restartService = autoRestart && running && !remote;
    if (autoRestart) {
      serviceSettingsChanging = true;
      _notify([BoxAspect.state]);
    }
    try {
      final saved = json(await call('settings.set', {'key': key, 'value': value,
        if (restartService) 'restart': true}));
      if (!_disposed) {
        settings = saved;
        if (restartService) applyRequired = null;
        else if (restart && !autoRestart && running && !remote) applyRequired = 'Restart';
        _notify([BoxAspect.state]);
      }
    } catch (error) {
      report(error);
    } finally {
      if (autoRestart) {
        serviceSettingsChanging = false;
        if (!_disposed) _notify([BoxAspect.state]);
      }
    }
  }

  Future<bool> setSettings(Json values, {bool restart = true}) async {
    try {
      settings = json(await call('settings.patch', {'value': values}));
      if (restart && running && !remote) applyRequired = 'Restart';
      _notify([BoxAspect.state]);
      return true;
    } catch (error) { report(error); return false; }
  }

  void report(Object error) { if (!_disposed) messages.add(describe(error)); }
  static String describe(Object error) => error is PlatformException
      ? error.message ?? error.code : error.toString();

  void _onEvent(dynamic event) {
    if (_disposed) return;
    final item = json(event);
    final type = item['type'] as String?;
    if (type == null) return;
    _eventRevisions[type] = (_eventRevisions[type] ?? 0) + 1;
    final aspects = _applyEvent(type, item['value']);
    if (aspects.isNotEmpty) _notify(aspects);
  }

  /// Applies one backend event and returns the aspects whose consumers rebuild.
  List<BoxAspect> _applyEvent(String type, Object? value) {
    switch (type) {
      case 'backendReconnected':
        sessionRevision++;
        profilesRevision++;
        _profileStats.clear();
        _connectionsById.clear();
        connections = [];
        unawaited(_refreshBackend());
        return const [BoxAspect.state, BoxAspect.connections];
      case 'dashboard':
        return _updateDashboard(json(value));
      case 'profiles':
        profilesRevision++;
        _profileStats.clear();
        profiles = rows(value);
        return const [BoxAspect.state];
      case 'nodes':
        selectedProfileNodes = json(value);
        return const [BoxAspect.state, BoxAspect.groups];
      case 'settings':
        settings = json(value);
        return const [BoxAspect.state];
      case 'lan':
        lan = json(value);
        return const [BoxAspect.state];
      case 'applyRequired':
        applyRequired = value.toString();
        return const [BoxAspect.state];
      case 'preferences':
        final next = json(value);
        if (const DeepCollectionEquality().equals(preferences, next)) return const [];
        preferences = next;
        return const [BoxAspect.state, BoxAspect.appearance];
      case 'systemAccent':
        final color = (value as num).toInt();
        if (systemAccentColor == color) return const [];
        systemAccentColor = color;
        return const [BoxAspect.appearance];
      case 'groups':
        return _setGroups(value) ? const [BoxAspect.groups] : const [];
      case 'rates':
        _addRateSample(json(value));
        return const [BoxAspect.traffic];
      case 'memory':
        final next = json(value);
        if (const DeepCollectionEquality().equals(memory, next)) return const [];
        memory = next;
        return const [BoxAspect.memory];
      case 'connections':
        _mergeConnections(value);
        return const [BoxAspect.connections];
      case 'logs':
        _mergeLogs(json(value));
        return const [BoxAspect.logs];
      case 'tools':
        toolState = json(value);
        return const [BoxAspect.tools];
      case 'usbDevices':
        usbDevices = rows(value);
        return const [BoxAspect.tools];
      case 'terminal':
        terminal = json(value);
        return const [BoxAspect.terminal];
      case 'updates':
        return _setAppUpdate(json(value));
      case 'outbounds':
        outbounds = rows(value);
        return const [BoxAspect.diagnostics];
      case 'stun':
        stun = json(value);
        return const [BoxAspect.diagnostics];
      case 'quality':
        quality = json(value);
        return const [BoxAspect.diagnostics];
      case 'ping':
        ping = json(value);
        return const [BoxAspect.diagnostics];
      case 'transfers':
        transfers = rows(value);
        return const [BoxAspect.transfers];
      case 'inbox':
        inbox = json(value);
        return const [BoxAspect.transfers];
      // The remaining events are passed on to their listeners and rebuild nothing.
      case 'error':
        messages.add(value.toString());
      case 'openUrl':
        // A backend request remains a visible user action in the Flutter UI.
        messages.add(value.toString());
      case 'browserComplete':
        browserEvents.add(json(value));
      case 'intentAvailable':
        unawaited(_takeIntent());
      case 'intentRecovery':
        _nativeRecovery++;
        if (_startupInterrupted) {
          unawaited(_refreshBackend());
          return const [];
        }
        unawaited(_takeIntent());
        intentRecovery.add(null);
    }
    return const [];
  }

  List<BoxAspect> _updateDashboard(Json next) {
    const equality = DeepCollectionEquality();
    if (equality.equals(dashboard, next)) return const [];
    // A change to these counters alone only concerns the traffic consumers.
    const counters = {'memory', 'goroutines', 'uploadTotal', 'downloadTotal', 'connections'};
    Json state(Json data) => Map.fromEntries(data.entries.where((entry) => !counters.contains(entry.key)));
    final stateChanged = !equality.equals(state(dashboard), state(next));
    if (_setDashboard(next)) return const [BoxAspect.state, BoxAspect.traffic, BoxAspect.connections];
    return stateChanged ? const [BoxAspect.state, BoxAspect.traffic] : const [BoxAspect.traffic];
  }

  List<BoxAspect> _setAppUpdate(Json next) {
    if (const DeepCollectionEquality().equals(appUpdate, next)) return const [];
    appUpdate = next;
    return const [BoxAspect.updates];
  }

  void _addRateSample(Json sample) {
    sampleRevision++;
    rates = sample;
    uploadHistory.add((sample['upload'] as num? ?? 0).toDouble());
    downloadHistory.add((sample['download'] as num? ?? 0).toDouble());
    if (uploadHistory.length > 30) uploadHistory.removeAt(0);
    if (downloadHistory.length > 30) downloadHistory.removeAt(0);
  }

  // A list replaces every connection. A map holds the entries that changed and the IDs
  // that were removed since the previous event, unless it starts over with a reset.
  void _mergeConnections(Object? value) {
    final update = json(value);
    if (value is List || update['reset'] != false) _connectionsById.clear();
    for (final id in update['removed'] as List? ?? const []) {
      _connectionsById.remove(id);
    }
    for (final entry in rows(value is List ? value : update['entries'])) {
      _connectionsById[entry['id'] as String] = entry;
    }
    connections = _connectionsById.values.toList();
  }

  void _mergeLogs(Json update) {
    final entries = rows(update['entries']);
    if (update['reset'] == false) {
      int id(Json entry) => (entry['id'] as num).toInt();
      // firstId is the oldest record that the backend still holds. Without one it holds none.
      final firstId = (update['firstId'] as num?)?.toInt();
      final lastId = logs.isEmpty ? -1 : id(logs.last);
      logs = [
        if (firstId != null) ...logs.where((entry) => id(entry) >= firstId),
        ...entries.where((entry) => id(entry) > lastId),
      ];
    } else {
      logs = entries;
    }
    defaultLogLevel = update['defaultLogLevel'] as String?;
  }

  bool _acceptIntent(Object? value) {
    final input = BoxIntent.fromWire(value);
    if (input == null) return false;
    // URI equality is irrelevant: two identical links can be separate inputs.
    if (_completedIntentIds.contains(input.id) || _ownedIntents.containsKey(input.id)) return true;
    _ownedIntents[input.id] = input;
    pendingIntents.add(input);
    intents.add(input.uri);
    return true;
  }

  void retryIntent(BoxIntent input) {
    if (_disposed || !_ownedIntents.containsKey(input.id) || pendingIntents.any((pending) => pending.id == input.id)) return;
    pendingIntents.insert(0, input);
  }

  Future<void> completeIntent(BoxIntent input) async {
    await call('intent.complete', {'id': input.id});
    if (_disposed) return;
    _completedIntentIds.add(input.id);
    _ownedIntents.remove(input.id);
    pendingIntents.removeWhere((pending) => pending.id == input.id);
  }

  Future<void> _takeIntent() async {
    if (_disposed) return;
    _takeIntentsAgain = true;
    if (_takingIntents) return;
    _takingIntents = true;
    try {
      do {
        _takeIntentsAgain = false;
        final refresh = _backendRefresh;
        try {
          while (!_disposed) {
            final input = await call('intent.take');
            if (_disposed || refresh != _backendRefresh) break;
            if (!_acceptIntent(input)) break;
          }
        } catch (error) {
          if (!_disposed && refresh == _backendRefresh) report(error);
        }
        // Events arriving during a read request another pass. A failed read
        // alone waits for foreground/reconnection instead of retrying forever.
      } while (_takeIntentsAgain && !_disposed);
    } finally { _takingIntents = false; }
  }

  @override
  void dispose() {
    _disposed = true;
    if (_observingLifecycle) WidgetsBinding.instance.removeObserver(this);
    _subscription?.cancel();
    appIcons.clear();
    messages.close();
    intents.close();
    intentRecovery.close();
    browserEvents.close();
    _stateChanges.dispose();
    super.dispose();
  }
}

class BoxScope extends StatelessWidget {
  const BoxScope({super.key, required this.controller, required this.child});
  final BoxController controller;
  final Widget child;
  @override Widget build(BuildContext context) => ListenableBuilder(
    listenable: controller,
    child: child,
    builder: (_, child) => _BoxState(controller: controller,
      revisions: List<int>.of(controller._revisions), child: child!),
  );
  static BoxController of(BuildContext context, {BoxAspect aspect = BoxAspect.state}) =>
    InheritedModel.inheritFrom<_BoxState>(context, aspect: aspect)!.controller;
}

class _BoxState extends InheritedModel<BoxAspect> {
  const _BoxState({required this.controller, required this.revisions, required super.child});
  final BoxController controller;
  final List<int> revisions;
  @override bool updateShouldNotify(_BoxState oldWidget) =>
    controller != oldWidget.controller || !listEquals(revisions, oldWidget.revisions);
  @override bool updateShouldNotifyDependent(_BoxState oldWidget, Set<BoxAspect> dependencies) =>
    controller != oldWidget.controller || dependencies.any((aspect) => revisions[aspect.index] != oldWidget.revisions[aspect.index]);
}

extension BoxContext on BuildContext {
  BoxController get box => BoxScope.of(this);
  BoxController watchBox(BoxAspect aspect) {
    BoxScope.of(this);
    return BoxScope.of(this, aspect: aspect);
  }
}

String bytes(num value) {
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  var size = value.toDouble();
  var unit = 0;
  while (size.abs() >= 1024 && unit < units.length - 1) { size /= 1024; unit++; }
  return '${size.toStringAsFixed(unit == 0 ? 0 : 1)} ${units[unit]}';
}
