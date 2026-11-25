import 'dart:convert';

/// The ways bytes can be typed into a message or body editor.
const byteInputFormats = ['Text', 'Hex', 'Base64'];

/// Reads typed bytes. A [FormatException] carries an untranslated message.
List<int> parseByteInput(String format, String text) {
  if (format == 'Text') return utf8.encode(text);
  final value = text.replaceAll(RegExp(r'\s+'), '');
  if (format == 'Base64') {
    try { return base64.decode(value); }
    on FormatException { throw const FormatException('Invalid Base64 message'); }
  }
  if (value.length.isOdd || !RegExp(r'^[0-9a-fA-F]*$').hasMatch(value)) {
    throw const FormatException('Enter hexadecimal byte pairs');
  }
  return [for (var offset = 0; offset < value.length; offset += 2) int.parse(value.substring(offset, offset + 2), radix: 16)];
}

/// Writes [bytes] the way [parseByteInput] reads them; null when they are not UTF-8 text.
String? formatByteInput(String format, List<int> bytes) {
  if (format == 'Base64') return base64.encode(bytes);
  if (format == 'Hex') return bytes.map((byte) => byte.toRadixString(16).padLeft(2, '0')).join(' ');
  try {
    // The decoder drops a leading byte order mark; it is kept so that unedited text still encodes to [bytes].
    final marked = bytes.length >= 3 && bytes[0] == 0xef && bytes[1] == 0xbb && bytes[2] == 0xbf;
    return (marked ? '\u{feff}' : '') + utf8.decode(bytes);
  } on FormatException { return null; }
}
