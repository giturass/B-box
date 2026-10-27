import 'dart:async';
import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

typedef Json = Map<String, dynamic>;

Json json(Object? value) => value is Map
    ? value.map((key, value) => MapEntry(key.toString(), value))
    : <String, dynamic>{};
List<Json> rows(Object? value) => value is List ? value.map(json).toList() : [];

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
  Json dashboard = {}, settings = {}, preferences = {}, rates = {};
  Json toolState = {}, stun = {}, quality = {}, inbox = {}, ping = {};
  Json terminal = {};
  Json memory = {}, lan = {};
  List<Json> transfers = [], usbDevices = [];
  List<Json> outbounds = [];
  List<Json> profiles = [], groups = [], connections = [], logs = [];
  final uploadHistory = <double>[];
  final downloadHistory = <double>[];
  String version = '', coreVersion = '';
  int systemAccentColor = 0xff007aff;
  int sampleRevision = 0;
  int profilesRevision = 0;
  int dashboardEditRevision = 0;
  bool _disposed = false;
  bool ready = false;
  String? startupError;
  final pendingIntents = <String>[];
  String? applyRequired;
  final appIcons = <String, Future<Uint8List?>>{};
  final _profileStats = <int, Future<Json>>{};

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

  void editDashboard() { dashboardEditRevision++; notifyListeners(); }

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
      toolState = json(result['tools']);
      final diagnostics = json(result['diagnostics']);
      stun = json(diagnostics['stun']);
      quality = json(diagnostics['quality']);
      final intent = result['intent'] as String?;
      if (intent != null) pendingIntents.add(intent);
      ready = true;
    } catch (error) {
      startupError = describe(error);
    }
    notifyListeners();
  }

  Future<dynamic> call(String method, [Json? args]) => _channel.invokeMethod<dynamic>(method, args);

  void clearApplyRequired() { applyRequired = null; notifyListeners(); }

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
      notifyListeners();
      return true;
    } catch (error) {
      report(error);
      return false;
    }
  }

  Future<void> setSetting(String key, dynamic value, {bool restart = true}) async {
    try {
      settings = json(await call('settings.set', {'key': key, 'value': value}));
      if (restart && running && !remote) applyRequired = 'Restart';
      notifyListeners();
    } catch (error) {
      report(error);
    }
  }

  Future<bool> setSettings(Json values, {bool restart = true}) async {
    try {
      settings = json(await call('settings.patch', {'value': values}));
      if (restart && running && !remote) applyRequired = 'Restart';
      notifyListeners();
      return true;
    } catch (error) { report(error); return false; }
  }

  void report(Object error) { if (!_disposed) messages.add(describe(error)); }
  static String describe(Object error) => error is PlatformException
      ? error.message ?? error.code : error.toString();

  void _onEvent(dynamic event) {
    final item = json(event);
    final value = item['value'];
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
      case 'memory': memory = json(value);
      case 'lan': lan = json(value);
      case 'browserComplete': browserEvents.add(json(value)); return;
      case 'dashboard': dashboard = json(value);
      case 'profiles': profilesRevision++; _profileStats.clear(); profiles = rows(value);
      case 'groups': groups = rows(value);
      case 'connections': connections = rows(value);
      case 'logs': logs = rows(value);
      case 'settings': settings = json(value);
      case 'preferences': preferences = json(value);
      case 'systemAccent': systemAccentColor = (value as num).toInt();
      case 'rates':
        sampleRevision++;
        rates = json(value);
        uploadHistory.add((rates['upload'] as num? ?? 0).toDouble());
        downloadHistory.add((rates['download'] as num? ?? 0).toDouble());
        if (uploadHistory.length > 30) uploadHistory.removeAt(0);
        if (downloadHistory.length > 30) downloadHistory.removeAt(0);
      case 'error': messages.add(value.toString());
      case 'applyRequired': applyRequired = value.toString();
      case 'intentAvailable':
        call('intent.take').then((uri) { if (uri is String) { pendingIntents.add(uri); intents.add(uri); } }).catchError(report);
      case 'openUrl':
        // A backend request remains a visible user action in the Flutter UI.
        messages.add(value.toString());
    }
    notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    _subscription?.cancel();
    messages.close();
    intents.close();
    browserEvents.close();
    super.dispose();
  }
}

class BoxScope extends InheritedNotifier<BoxController> {
  const BoxScope({super.key, required BoxController controller, required super.child}) : super(notifier: controller);
  static BoxController of(BuildContext context) => context.dependOnInheritedWidgetOfExactType<BoxScope>()!.notifier!;
}

extension BoxContext on BuildContext {
  BoxController get box => BoxScope.of(this);
}

String bytes(num value) {
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  var size = value.toDouble();
  var unit = 0;
  while (size.abs() >= 1024 && unit < units.length - 1) { size /= 1024; unit++; }
  return '${size.toStringAsFixed(unit == 0 ? 0 : 1)} ${units[unit]}';
}
