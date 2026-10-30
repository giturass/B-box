import 'dart:async';
import 'package:collection/collection.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'common/ip_quality_cache.dart';

typedef Json = Map<String, dynamic>;

Json json(Object? value) => value is Map
    ? value.map((key, value) => MapEntry(key.toString(), value))
    : <String, dynamic>{};
List<Json> rows(Object? value) => value is List ? value.map(json).toList() : [];

enum BoxAspect { state, appearance, traffic, memory, connections, logs }
enum BoxData { memory, logs }

class BoxController extends ChangeNotifier {
  BoxController({MethodChannel? channel, Stream<dynamic>? events})
      : _channel = channel ?? const MethodChannel('io.ericlee.bfa/box'),
        _events = events ?? const EventChannel('io.ericlee.bfa/events').receiveBroadcastStream();

  final MethodChannel _channel;
  final Stream<dynamic> _events;
  final messages = StreamController<String>.broadcast();
  final intents = StreamController<String>.broadcast();
  final browserEvents = StreamController<Json>.broadcast();
  StreamSubscription<dynamic>? _subscription;
  final _revisions = List<int>.filled(BoxAspect.values.length, 0);
  final _stateChanges = ChangeNotifier();
  final _dataOwners = <BoxData, Set<Object>>{};
  Listenable get stateChanges => _stateChanges;
  Json dashboard = {}, settings = {}, preferences = {}, rates = {};
  Json toolState = {}, stun = {}, quality = {}, inbox = {}, ping = {};
  Json terminal = {};
  Json memory = {}, lan = {};
  List<Json> transfers = [], usbDevices = [];
  List<Json> outbounds = [];
  List<Json> profiles = [], groups = [], connections = [], logs = [];
  String? defaultLogLevel;
  final uploadHistory = <double>[];
  final downloadHistory = <double>[];
  String version = '', coreVersion = '';
  int systemAccentColor = 0xff007aff;
  int sampleRevision = 0;
  int profilesRevision = 0;
  bool _disposed = false;
  bool ready = false;
  bool lanChanging = false;
  bool serviceSettingsChanging = false;
  String? startupError;
  final pendingIntents = <String>[];
  String? applyRequired;
  final appIcons = <String, Future<Uint8List?>>{};
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
    unawaited(act(data == BoxData.memory ? 'dashboard.memory.visible' : 'logs.visible',
      {'visible': _dataOwners[data]?.isNotEmpty == true}));
  }

  Future<Json> profileStats(int id) => _profileStats.putIfAbsent(id, () async => json(await call('profiles.stats', {'id': id})));

  Future<Uint8List?> appIcon(String package) => appIcons.putIfAbsent(package, () async {
    try { return await call('apps.icon', {'package': package}) as Uint8List?; }
    catch (_) { return null; }
  });

  bool get remote => dashboard['remote'] != null;
  bool get running => remote
      ? dashboard['remoteConnected'] == true
      : dashboard['status'] == 'Started';
  bool get transitioning => ['Starting', 'Stopping'].contains(dashboard['status']);
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
    await _subscription?.cancel();
    _subscription = _events.listen(_onEvent, onError: report);
    try {
      final result = json(await call('ready'));
      version = result['version'] as String;
      coreVersion = result['coreVersion'] as String;
      systemAccentColor = (result['systemAccentColor'] as num?)?.toInt() ?? 0xff007aff;
      dashboard = json(result['dashboard']);
      profiles = rows(result['profiles']);
      settings = json(result['settings']);
      preferences = json(result['preferences']);
      if (result['lan'] is Map) lan = json(result['lan']);
      toolState = json(result['tools']);
      final diagnostics = json(result['diagnostics']);
      stun = json(diagnostics['stun']);
      quality = json(diagnostics['quality']);
      final intent = result['intent'] as String?;
      if (intent != null) pendingIntents.add(intent);
      ready = true;
      for (final data in _dataOwners.keys) { _syncData(data); }
    } catch (error) {
      startupError = describe(error);
    }
    notifyListeners();
  }

  Future<dynamic> call(String method, [Json? args]) => _channel.invokeMethod<dynamic>(method, args);

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
    final value = item['value'];
    var aspects = const [BoxAspect.state];
    switch (item['type']) {
      case 'tools': toolState = json(value);
      case 'outbounds': outbounds = rows(value);
      case 'stun': stun = json(value);
      case 'quality': quality = json(value);
      case 'inbox': inbox = json(value);
      case 'ping': ping = json(value);
      case 'transfers': transfers = rows(value);
      case 'usbDevices': usbDevices = rows(value);
      case 'terminal': terminal = json(value);
      case 'memory':
        final nextMemory = json(value);
        if (const DeepCollectionEquality().equals(memory, nextMemory)) return;
        memory = nextMemory;
        aspects = const [BoxAspect.memory];
      case 'lan': lan = json(value);
      case 'browserComplete': browserEvents.add(json(value)); return;
      case 'dashboard':
        final nextDashboard = json(value);
        if (const DeepCollectionEquality().equals(dashboard, nextDashboard)) return;
        const counters = {'memory', 'goroutines', 'uploadTotal', 'downloadTotal', 'connections'};
        Json state(Json data) => Map.fromEntries(data.entries.where((entry) => !counters.contains(entry.key)));
        aspects = const DeepCollectionEquality().equals(state(dashboard), state(nextDashboard))
          ? const [BoxAspect.traffic] : const [BoxAspect.state, BoxAspect.traffic];
        dashboard = nextDashboard;
      case 'profiles': profilesRevision++; _profileStats.clear(); profiles = rows(value);
      case 'groups': groups = rows(value);
      case 'connections':
        connections = rows(value);
        aspects = const [BoxAspect.connections];
      case 'logs':
        final logState = json(value);
        final entries = rows(logState['entries']);
        if (logState['reset'] == false) {
          final firstId = (logState['firstId'] as num?)?.toInt();
          final retained = firstId == null ? <Json>[] : logs.where((entry) => (entry['id'] as num).toInt() >= firstId);
          final lastId = logs.isEmpty ? -1 : (logs.last['id'] as num).toInt();
          logs = [...retained, ...entries.where((entry) => (entry['id'] as num).toInt() > lastId)];
        } else {
          logs = entries;
        }
        defaultLogLevel = logState['defaultLogLevel'] as String?;
        aspects = const [BoxAspect.logs];
      case 'settings': settings = json(value);
      case 'preferences':
        final nextPreferences = json(value);
        if (const DeepCollectionEquality().equals(preferences, nextPreferences)) return;
        preferences = nextPreferences;
        aspects = const [BoxAspect.state, BoxAspect.appearance];
      case 'systemAccent':
        if (systemAccentColor == (value as num).toInt()) return;
        systemAccentColor = value.toInt();
        aspects = const [BoxAspect.appearance];
      case 'rates':
        sampleRevision++;
        rates = json(value);
        uploadHistory.add((rates['upload'] as num? ?? 0).toDouble());
        downloadHistory.add((rates['download'] as num? ?? 0).toDouble());
        if (uploadHistory.length > 30) uploadHistory.removeAt(0);
        if (downloadHistory.length > 30) downloadHistory.removeAt(0);
        aspects = const [BoxAspect.traffic];
      case 'error': messages.add(value.toString()); return;
      case 'applyRequired': applyRequired = value.toString();
      case 'intentAvailable':
        call('intent.take').then((uri) { if (uri is String) { pendingIntents.add(uri); intents.add(uri); } }).catchError(report);
        return;
      case 'openUrl':
        // A backend request remains a visible user action in the Flutter UI.
        messages.add(value.toString());
        return;
      default: return;
    }
    _notify(aspects);
  }

  @override
  void dispose() {
    _disposed = true;
    _subscription?.cancel();
    messages.close();
    intents.close();
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
