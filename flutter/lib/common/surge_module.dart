import 'dart:convert';
import '../box.dart';

/// The parts of a Surge module that a B-box profile can hold. Rewrite lines are
/// kept as written; the core parses them when the profile is checked.
class SurgeModule {
  String name = '', description = '';
  /// Declared parameter names with their default values, in declaration order.
  final arguments = <String, String>{};
  final hosts = <String>[], frontHosts = <String>[];
  final rewrites = <String, List<String>>{'url_rewrite': [], 'header_rewrite': [], 'body_rewrite': [], 'map_local': []};
  final scripts = <String, List<Json>>{'http': [], 'cron': [], 'event': [], 'generic': []};
  /// Sections, keys and scripts this profile format cannot express.
  final skipped = <String>[];

  int get rewriteCount => rewrites.values.fold(0, (count, lines) => count + lines.length);
  int get scriptCount => scripts.values.fold(0, (count, items) => count + items.length);
  int get hostCount => hosts.length + frontHosts.length;
  bool get isEmpty => hostCount + rewriteCount + scriptCount == 0;
}

const _sections = {'url rewrite': 'url_rewrite', 'header rewrite': 'header_rewrite', 'body rewrite': 'body_rewrite', 'map local': 'map_local'};
const _scriptTypes = {'http-request', 'http-response', 'cron', 'event', 'rule', 'dns', 'generic'};
const _events = {'network-changed', 'engine-started', 'profile-reloaded'};
final _parameter = RegExp(r'^[a-z][a-z0-9-]*\s*=');

/// Reads `.sgmodule` text. [values] replaces the defaults of declared parameters.
SurgeModule parseSurgeModule(String text, {Map<String, String> values = const {}}) {
  final module = SurgeModule(), lines = const LineSplitter().convert(text);
  // Metadata applies to the whole file, wherever it is written.
  for (final raw in lines) {
    final line = raw.trim(), split = line.indexOf('=');
    if (!line.startsWith('#!') || split < 0) continue;
    final value = line.substring(split + 1).trim();
    switch (line.substring(2, split).trim()) {
      case 'name': module.name = value;
      case 'desc': module.description = value;
      case 'arguments':
        for (final item in value.split(',')) {
          final colon = item.indexOf(':'), name = (colon < 0 ? item : item.substring(0, colon)).trim();
          if (name.isNotEmpty) module.arguments[name] = colon < 0 ? '' : item.substring(colon + 1).trim();
        }
    }
  }
  final unsupported = <String, int>{};
  String? section;
  for (final raw in lines) {
    var line = raw.trim();
    if (line.isEmpty || line.startsWith('#') || line.startsWith(';') || line.startsWith('//')) continue;
    module.arguments.forEach((name, fallback) => line = line.replaceAll('{{{$name}}}', values[name] ?? fallback));
    if (line.startsWith('[') && line.endsWith(']')) {
      section = line.substring(1, line.length - 1).trim();
      continue;
    }
    final key = section?.toLowerCase();
    if (_sections[key] case final list?) {
      module.rewrites[list]!.add(line);
    } else if (key == 'mitm') {
      _mitm(module, line);
    } else if (key == 'script') {
      _script(module, line);
    } else {
      final label = section == null ? 'No section' : '[$section]';
      unsupported[label] = (unsupported[label] ?? 0) + 1;
    }
  }
  unsupported.forEach((label, count) => module.skipped.add('$label × $count'));
  return module;
}

void _mitm(SurgeModule module, String line) {
  final split = line.indexOf('='), key = split < 0 ? line : line.substring(0, split).trim();
  if (split < 0 || key != 'hostname') {
    module.skipped.add('[MITM] $key');
    return;
  }
  var value = line.substring(split + 1).trim();
  final front = value.startsWith('%INSERT%');
  if (front || value.startsWith('%APPEND%')) value = value.substring(8);
  for (final item in value.split(',')) {
    final host = item.trim();
    if (host.isNotEmpty) (front ? module.frontHosts : module.hosts).add(host);
  }
}

String _unquote(String value) =>
  value.length >= 2 && value.startsWith('"') && value.endsWith('"') ? value.substring(1, value.length - 1) : value;

/// Splits `key=value` parameters on commas outside quotes. A piece that names
/// no parameter continues the previous value, as in a pattern like `a{1,3}`.
Map<String, String> _parameters(String text) {
  final pieces = <String>[], current = StringBuffer();
  var quoted = false;
  for (final unit in text.split('')) {
    if (unit == '"') quoted = !quoted;
    if (unit == ',' && !quoted) {
      pieces.add(current.toString());
      current.clear();
    } else {
      current.write(unit);
    }
  }
  pieces.add(current.toString());
  final result = <String, String>{};
  String? last;
  for (final piece in pieces) {
    final item = piece.trim();
    if (_parameter.hasMatch(item)) {
      final split = item.indexOf('=');
      last = item.substring(0, split).trim();
      result[last] = _unquote(item.substring(split + 1).trim());
    } else if (last != null) {
      result[last] = '${result[last]},$piece';
    }
  }
  return result;
}

void _script(SurgeModule module, String line) {
  var name = '';
  Map<String, String> options;
  final legacy = RegExp(r'^(\S+)\s+(?!=)(.*)$').firstMatch(line);
  if (legacy != null && _scriptTypes.contains(legacy[1])) {
    // <type> <value> <parameters>
    final type = legacy[1]!, rest = legacy[2]!;
    final end = rest.startsWith('"') ? rest.indexOf('"', 1) + 1 : rest.indexOf(RegExp(r'\s'));
    final value = _unquote(end <= 0 ? rest : rest.substring(0, end));
    options = {..._parameters(end <= 0 ? '' : rest.substring(end).trim()), 'type': type};
    switch (type) {
      case 'http-request' || 'http-response': options['pattern'] = value;
      case 'cron': options['cronexp'] = value;
      case 'event': options['event-name'] = value;
      default: name = value;
    }
  } else {
    final split = line.indexOf('=');
    if (split < 0) {
      module.skipped.add('[Script] $line');
      return;
    }
    name = line.substring(0, split).trim();
    options = _parameters(line.substring(split + 1));
  }
  final type = options['type'] ?? 'generic', path = options['script-path'] ?? '';
  if (name.isEmpty) name = path.split('/').last;
  void skip(String reason) => module.skipped.add('[Script] $name ($reason)');
  if (path.isEmpty) return skip('script-path');
  bool flag(String key) => options[key] == 'true' || options[key] == '1';
  final entry = <String, dynamic>{'name': name, 'script_path': path};
  if (options['timeout'] case final text?) {
    final seconds = num.tryParse(text);
    if (seconds == null || seconds <= 0) return skip('timeout=$text');
    entry['timeout_ms'] = (seconds * 1000).round();
  }
  if (options['script-update-interval'] case final text?) {
    final seconds = int.tryParse(text);
    if (seconds == null || seconds <= 0) return skip('script-update-interval=$text');
    entry['script_update_interval'] = seconds;
  }
  if (options['argument'] case final text? when text.isNotEmpty) entry['argument'] = text;
  if (flag('debug')) entry['debug'] = true;
  switch (type) {
    case 'http-request' || 'http-response':
      final pattern = options['pattern'] ?? '';
      if (pattern.isEmpty) return skip('pattern');
      entry['pattern'] = pattern;
      entry['phase'] = type == 'http-request' ? 'request' : 'response';
      if (flag('requires-body')) entry['requires_body'] = true;
      if (flag('binary-body-mode')) entry['binary_body'] = true;
      if (flag('full-header-mode')) entry['full_headers'] = true;
      if (options['max-size'] case final text?) {
        final size = int.tryParse(text);
        if (size == null || size == 0 || size < -1) return skip('max-size=$text');
        entry['max_body_bytes'] = size;
      }
      module.scripts['http']!.add(entry);
    case 'cron':
      final expression = options['cronexp'] ?? '';
      if (expression.isEmpty) return skip('cronexp');
      entry['cron'] = expression;
      module.scripts['cron']!.add(entry);
    case 'event':
      final event = options['event-name'] ?? '';
      if (!_events.contains(event)) return skip('event-name=$event');
      entry['event'] = event;
      module.scripts['event']!.add(entry);
    case 'generic':
      module.scripts['generic']!.add(entry);
    default:
      skip('type=$type');
  }
}

/// Returns the profile's `experimental` object with the module merged in. As
/// in Surge, new lines go above the existing ones; entries already present are
/// not repeated. No switch is turned on.
Json applySurgeModule(Object? experimental, SurgeModule module) {
  final next = json(experimental);
  List<Object?> merged(Object? existing, List<Object?> added, {bool front = true}) {
    final current = existing is List ? existing : const <Object?>[];
    final seen = {for (final item in current) jsonEncode(item)};
    final fresh = [for (final item in added) if (seen.add(jsonEncode(item))) item];
    return front ? [...fresh, ...current] : [...current, ...fresh];
  }
  if (module.hostCount > 0) {
    final mitm = json(next['mitm']);
    mitm['hosts'] = merged(merged(mitm['hosts'], module.hosts, front: false), module.frontHosts);
    next['mitm'] = mitm;
  }
  if (module.rewriteCount > 0) {
    final rewrite = json(next['http_rewrite']);
    module.rewrites.forEach((key, lines) { if (lines.isNotEmpty) rewrite[key] = merged(rewrite[key], lines); });
    next['http_rewrite'] = rewrite;
  }
  if (module.scriptCount > 0) {
    final scripts = json(next['scripts']);
    module.scripts.forEach((key, items) { if (items.isNotEmpty) scripts[key] = merged(scripts[key], items); });
    next['scripts'] = scripts;
  }
  return next;
}
