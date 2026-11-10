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
/// Invalid JSON stays in its raw view.
String? formatCapturedJson(String text, {int? maxOutputLength}) {
  final source = text.trim();
  if (!(source.startsWith('{') || source.startsWith('['))) return null;
  final output = StringBuffer();
  var depth = 0;
  var quoted = false, escaped = false;
  var previous = '';
  bool whitespace(String char) => char == ' ' || char == '\t' || char == '\n' || char == '\r';
  bool newLine() {
    if (maxOutputLength != null && output.length + 1 + depth * 2 > maxOutputLength) return false;
    output.write('\n${'  ' * depth}');
    return true;
  }
  for (var index = 0; index < source.length; index++) {
    if (maxOutputLength != null && output.length > maxOutputLength) return null;
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
          output.write(char);
          var next = index + 1;
          while (next < source.length && whitespace(source[next])) { next++; }
          if (next < source.length && source[next] != (char == '{' ? '}' : ']') && !newLine()) return null;
        case '}' || ']':
          depth--;
          if (depth < 0) return null;
          if (previous != '{' && previous != '[' && !newLine()) return null;
          output.write(char);
        case ',':
          output.write(char);
          if (!newLine()) return null;
        case ':':
          output.write(': ');
        default:
          output.write(char);
      }
      previous = char;
    }
  }
  if (quoted || depth != 0 || maxOutputLength != null && output.length > maxOutputLength) return null;
  try {
    jsonDecode(source);
  } on FormatException {
    return null;
  }
  return output.toString();
}
