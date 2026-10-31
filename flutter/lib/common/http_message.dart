import 'dart:convert';

String capturedHeader(Iterable<Map<String, dynamic>> headers, String name) {
  for (final header in headers) {
    if (header['name']?.toString().toLowerCase() == name.toLowerCase()) {
      return header['value']?.toString() ?? '';
    }
  }
  return '';
}

class CapturedCookie {
  const CapturedCookie({required this.name, required this.value, required this.raw, this.attributes = const []});
  final String name, value, raw;
  final List<String> attributes;
}

List<CapturedCookie> capturedCookies(Iterable<Map<String, dynamic>> headers, {required bool response}) {
  final cookies = <CapturedCookie>[];
  for (final header in headers) {
    if (header['name']?.toString().toLowerCase() != (response ? 'set-cookie' : 'cookie')) continue;
    final raw = header['value']?.toString() ?? '';
    // Set-Cookie headers are separate records. A comma can belong to Expires;
    // neither it nor an equals sign inside a value separates cookies.
    for (final entry in response ? [raw] : raw.split(';')) {
      final parts = entry.split(';');
      final pair = parts.first.trim();
      final separator = pair.indexOf('=');
      if (separator <= 0) continue;
      final name = pair.substring(0, separator).trim();
      if (name.isEmpty) continue;
      cookies.add(CapturedCookie(
        name: name,
        value: pair.substring(separator + 1).trim(),
        raw: response ? raw : pair,
        attributes: parts.skip(1).map((part) => part.trim()).where((part) => part.isNotEmpty).toList(),
      ));
    }
  }
  return cookies;
}

/// Adds whitespace without rewriting numbers, duplicate keys or string escapes.
/// Invalid, deeply nested or excessively expanded JSON stays in its raw view.
String? formatCapturedJson(String text) {
  final source = text.trim();
  if (source.length > 65536 || !(source.startsWith('{') || source.startsWith('['))) return null;
  final output = StringBuffer();
  var depth = 0;
  var quoted = false, escaped = false;
  var previous = '';
  bool whitespace(String char) => char == ' ' || char == '\t' || char == '\n' || char == '\r';
  void newLine() => output.write('\n${'  ' * depth}');
  for (var index = 0; index < source.length; index++) {
    final char = source[index];
    if (quoted) {
      output.write(char);
      if (escaped) {
        escaped = false;
      } else if (char == '\\') {
        escaped = true;
      } else if (char == '"') {
        quoted = false;
        previous = char;
      }
    } else {
      if (whitespace(char)) continue;
      switch (char) {
        case '"':
          quoted = true;
          output.write(char);
        case '{' || '[':
          depth++;
          if (depth > 64) return null;
          output.write(char);
          var next = index + 1;
          while (next < source.length && whitespace(source[next])) { next++; }
          if (next < source.length && source[next] != (char == '{' ? '}' : ']')) newLine();
        case '}' || ']':
          depth--;
          if (depth < 0) return null;
          if (previous != '{' && previous != '[') newLine();
          output.write(char);
        case ',':
          output.write(char);
          newLine();
        case ':':
          output.write(': ');
        default:
          output.write(char);
      }
      previous = char;
    }
    if (output.length > 262144) return null;
  }
  if (quoted || depth != 0) return null;
  try {
    jsonDecode(source);
  } on FormatException {
    return null;
  }
  return output.toString();
}

String capturedTextPrefix(String text, int limit) {
  if (text.length <= limit) return text;
  var end = limit;
  if (end > 0 && text.codeUnitAt(end - 1) >= 0xd800 && text.codeUnitAt(end - 1) <= 0xdbff &&
      text.codeUnitAt(end) >= 0xdc00 && text.codeUnitAt(end) <= 0xdfff) end--;
  return text.substring(0, end);
}
