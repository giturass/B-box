// Adapted from FlClash common/mitm_module.dart and views/config/mitm_modules.dart.
// GPL-3.0; source commits and adaptation scope are recorded in flclash-manifest.json.
import 'dart:convert';

import '../box.dart';

const processingPath = ['experimental', 'processing'];

Json processingConfig(Json root) => json(json(root['experimental'])['processing']);

List<String> processingModuleUrls(String text) {
  final uri = Uri.tryParse(text.trim());
  if (uri == null) return const [];
  final action = '${uri.host}${uri.path}'.replaceAll('/', '');
  try {
    final urls = switch (uri.scheme) {
      'http' || 'https' when uri.host == 'link.stash.ws' =>
        uri.pathSegments.length < 2 || uri.pathSegments.first != 'install-override'
          ? const <String>[] : ['${uri.queryParameters['scheme'] ?? 'https'}://${uri.pathSegments.skip(1).join('/')}'],
      'http' || 'https' => [text.trim()],
      'surge' when action == 'install-module' => [?uri.queryParameters['url']],
      'loon' when action == 'import' => [?uri.queryParameters['plugin']],
      'shadowrocket' when action == 'install' => [?uri.queryParameters['module']],
      'stash' || 'clash' when action == 'install-override' => [?uri.queryParameters['url']],
      'quantumult-x' when const {'add-resource', 'update-configuration'}.contains(action) =>
        _quantumultXUrls(uri.queryParameters['remote-resource']),
      _ => const <String>[],
    };
    return urls.where((url) {
      final parsed = Uri.tryParse(url);
      return parsed != null && const {'http', 'https'}.contains(parsed.scheme) &&
        parsed.host.isNotEmpty && parsed.userInfo.isEmpty;
    }).toSet().toList();
  } on FormatException {
    return const [];
  }
}

List<String> _quantumultXUrls(String? resource) {
  final value = jsonDecode(resource ?? '');
  final rewrites = value is Map ? value['rewrite_remote'] : null;
  return [if (rewrites is List) for (final entry in rewrites.whereType<String>()) entry.split(',').first.trim()];
}

String processingScriptOutput(Json output) {
  String? resultText(String result) {
    if (result.isEmpty) return null;
    final Object? value;
    try { value = jsonDecode(result); } on FormatException { return result; }
    return switch (value) {
      null || '' => null,
      final String text => text,
      {'title': final Object title} => [title, if ((value as Map)['content'] case final Object content) content].join('\n'),
      final Map map when map.isEmpty => null,
      _ => result,
    };
  }
  return [
    if (output['error'] is String && (output['error'] as String).isNotEmpty) output['error'],
    ?resultText(output['result'] as String? ?? ''),
    ...((output['logs'] as List?) ?? const []).whereType<String>(),
  ].join('\n');
}
