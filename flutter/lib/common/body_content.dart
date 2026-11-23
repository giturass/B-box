// Adapted from FlClash (GPL-3.0), fixed reference
// 5e579c4987e50e5c68e6023cc31ff4db55d480bb:
// lib/common/mitm_body.dart and lib/common/mitm_protobuf.dart.
// B-box operates on one native body page and keeps its original bytes.
import 'dart:convert';
import 'dart:math' as math;
import 'dart:typed_data';

enum CapturedBodyKind { text, image, binary }

enum CapturedBodyFormat { plain, protobuf, grpc }

class CapturedBodyContent {
  const CapturedBodyContent({
    required this.kind,
    required this.bytes,
    this.text = '',
    this.preview = '',
    this.format = CapturedBodyFormat.plain,
    this.mime = '',
    this.charset = '',
    this.complete = false,
    this.error,
  });

  final CapturedBodyKind kind;
  final Uint8List bytes;
  final String text, preview, mime, charset;
  final CapturedBodyFormat format;
  final bool complete;
  final String? error;

  bool get isCut => preview.length < text.length;
}

const _previewChars = 3000;
const _previewLines = 30;
const _maxOutputChars = 65536;
const _maxProtoDepth = 16;
const _maxProtoNodes = 4096;
const _maxHexBytes = 256;
const _maxGuessedFieldNumber = 1 << 16;

/// Classifies only [page], keeping its original bytes for copy and byte views.
/// The native reader may supply charset-decoded UTF-8 display text separately;
/// complete image/Protobuf/gRPC files are opened by the viewer, not accumulated.
CapturedBodyContent readCapturedBody(Map<String, dynamic> message, Map<String, dynamic> page) {
  final latest = page['message'];
  if (latest is Map) message = {...message, ...Map<String, dynamic>.from(latest)};
  final contentType = _header(message, 'content-type');
  final mime = contentType.split(';').first.trim().toLowerCase();
  final source = page['text']?.toString() ?? '';
  final encoding = page['encoding']?.toString() ?? 'text';
  Uint8List bytes;
  if (encoding == 'base64') {
    final decoded = _strictBase64(source);
    if (decoded == null) {
      return CapturedBodyContent(kind: CapturedBodyKind.binary, bytes: Uint8List(0),
        mime: mime, error: 'Invalid Base64 body page');
    }
    bytes = decoded;
  } else if (encoding == 'text') {
    bytes = Uint8List.fromList(utf8.encode(source));
  } else {
    return CapturedBodyContent(kind: CapturedBodyKind.binary, bytes: Uint8List(0),
      mime: mime, error: 'Unsupported body page encoding');
  }
  final offset = (page['offset'] as num? ?? 0).toInt();
  final complete = offset == 0 && page['source'] != 'raw' &&
      message['bodyComplete'] == true && message['bodyTruncated'] != true &&
      page['truncated'] != true && page['hasMore'] != true &&
      (page['error']?.toString() ?? '').isEmpty;
  final declaredCharset = _declaredCharset(bytes, contentType, mime, firstPage: offset == 0);
  final charset = page['charset'] is String ? (page['charset'] as String).trim().toLowerCase() : declaredCharset;
  CapturedBodyContent binary() => CapturedBodyContent(kind: CapturedBodyKind.binary,
    bytes: bytes, mime: mime, charset: charset, complete: complete);
  CapturedBodyContent text(String value, {CapturedBodyFormat format = CapturedBodyFormat.plain}) =>
      CapturedBodyContent(kind: CapturedBodyKind.text, bytes: bytes, text: value,
        preview: _preview(value), format: format, mime: mime, charset: charset, complete: complete);
  if (page['source'] == 'raw') return binary();
  if (bytes.isEmpty) return text('');
  if (mime.startsWith('image/') && !mime.contains('svg')) {
    return complete ? CapturedBodyContent(kind: CapturedBodyKind.image,
      bytes: bytes, mime: mime, complete: true) : binary();
  }
  if (mime.startsWith('application/grpc')) {
    if (!complete) return binary();
    final parsed = _grpcText(bytes, mime, _header(message, 'grpc-encoding'));
    return parsed == null ? binary() : text(parsed, format: CapturedBodyFormat.grpc);
  } else if (mime.contains('proto')) {
    if (!complete) return binary();
    final parsed = _protobufText(bytes);
    return parsed == null ? binary() : text(parsed, format: CapturedBodyFormat.protobuf);
  }
  // The stateful native reader decodes across byte-page boundaries. Its
  // display field does not replace Text/Encoding or their original bytes.
  String? readable;
  if (page['displayText'] is String) {
    final display = page['displayText'] as String;
    if (!_validSurrogates(display)) return binary();
    readable = _readableText(display);
  } else {
    // Legacy single-page callers have no previous byte-order/decoder state.
    if (_isUtf16Charset(charset) && !complete) return binary();
    // Only an incomplete final character may be replaced. Invalid bytes inside
    // a truncated capture still identify binary data.
    final allowTail = page['hasMore'] != true &&
        (message['bodyTruncated'] == true || page['truncated'] == true);
    final decoded = _decodeText(bytes, charset, allowTail: allowTail);
    readable = decoded == null ? null : _readableText(decoded);
  }
  if (readable != null) return text(readable);
  // Native pages name the default UTF-8 decoder even without a declaration.
  // That default must not suppress the existing untyped binary Proto guess.
  if (complete && declaredCharset.isEmpty && (charset.isEmpty || charset == 'utf-8' || charset == 'utf8') &&
      (mime.isEmpty || mime == 'application/octet-stream')) {
    final parsed = _protobufText(bytes, guessed: true);
    if (parsed != null) return text(parsed, format: CapturedBodyFormat.protobuf);
  }
  return binary();
}

String _header(Map<String, dynamic> message, String name) {
  final headers = message['headers'];
  if (headers is! Iterable) return '';
  for (final header in headers) {
    if (header is Map && header['name']?.toString().toLowerCase() == name) {
      return header['value']?.toString() ?? '';
    }
  }
  return '';
}

final _base64Shape = RegExp(r'^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$');

Uint8List? _strictBase64(String text) {
  if (!_base64Shape.hasMatch(text)) return null;
  try {
    final bytes = base64.decode(text);
    // Require the native page's canonical padding and zero unused pad bits.
    return base64.encode(bytes) == text ? bytes : null;
  } on FormatException {
    return null;
  }
}

final _charsetInType = RegExp(r'''(?:^|;)\s*charset\s*=\s*(?:"([^"]*)"|'([^']*)'|([^;\s]+))''', caseSensitive: false);
final _charsetInMarkup = RegExp(r'''<(?:meta[^>]+charset|\?xml[^>]+encoding)\s*=\s*["']?([\w.:-]+)''', caseSensitive: false);

String _declaredCharset(Uint8List bytes, String contentType, String mime, {required bool firstPage}) {
  final header = _charsetInType.firstMatch(contentType);
  if (header != null) return (header.group(1) ?? header.group(2) ?? header.group(3) ?? '').trim().toLowerCase();
  if (firstPage) {
    if (bytes.length >= 2 && bytes[0] == 0xff && bytes[1] == 0xfe) return 'utf-16le';
    if (bytes.length >= 2 && bytes[0] == 0xfe && bytes[1] == 0xff) return 'utf-16be';
    if (bytes.length >= 3 && bytes[0] == 0xef && bytes[1] == 0xbb && bytes[2] == 0xbf) return 'utf-8';
    if (mime.isEmpty || mime.contains('html') || mime.contains('xml')) {
      final head = Uint8List.sublistView(bytes, 0, math.min(bytes.length, 1024));
      final markup = _charsetInMarkup.firstMatch(latin1.decode(head));
      if (markup != null) return markup.group(1)!.toLowerCase();
    }
  }
  return '';
}

String? _decodeText(Uint8List bytes, String charset, {required bool allowTail}) {
  switch (charset) {
    case '' || 'utf-8' || 'utf8':
      return _utf8Text(bytes, allowTail: allowTail);
    case 'ascii' || 'us-ascii':
      return bytes.any((byte) => byte > 0x7f) ? null : ascii.decode(bytes);
    case 'latin1' || 'latin-1' || 'iso-8859-1' || 'iso8859-1':
      return latin1.decode(bytes);
    case 'utf-16' || 'utf16' || 'utf-16le' || 'utf16le' || 'utf-16be' || 'utf16be':
      return _utf16Text(bytes, charset, allowTail: allowTail);
    default:
      // A declared unsupported charset cannot be guessed as UTF-8 or Proto.
      return null;
  }
}

bool _isUtf16Charset(String charset) => const {
  'utf-16', 'utf16', 'utf-16le', 'utf16le', 'utf-16be', 'utf16be',
}.contains(charset);

bool _validSurrogates(String text) {
  for (var index = 0; index < text.length; index++) {
    final unit = text.codeUnitAt(index);
    if (unit >= 0xd800 && unit <= 0xdbff) {
      if (++index >= text.length) return false;
      final low = text.codeUnitAt(index);
      if (low < 0xdc00 || low > 0xdfff) return false;
    } else if (unit >= 0xdc00 && unit <= 0xdfff) {
      return false;
    }
  }
  return true;
}

String? _utf8Text(Uint8List bytes, {required bool allowTail}) {
  try {
    return utf8.decode(bytes);
  } on FormatException {
    if (!allowTail) return null;
  }
  for (var start = math.max(0, bytes.length - 3); start < bytes.length; start++) {
    final lead = bytes[start];
    final needed = lead >= 0xc2 && lead <= 0xdf ? 2 :
      lead >= 0xe0 && lead <= 0xef ? 3 : lead >= 0xf0 && lead <= 0xf4 ? 4 : 0;
    if (needed == 0 || bytes.length - start >= needed) continue;
    var validTail = true;
    for (var index = start + 1; index < bytes.length; index++) {
      final byte = bytes[index];
      if (byte < 0x80 || byte > 0xbf || index == start + 1 &&
          (lead == 0xe0 && byte < 0xa0 || lead == 0xed && byte > 0x9f ||
           lead == 0xf0 && byte < 0x90 || lead == 0xf4 && byte > 0x8f)) {
        validTail = false;
        break;
      }
    }
    if (!validTail) continue;
    try {
      return '${utf8.decode(Uint8List.sublistView(bytes, 0, start))}\ufffd';
    } on FormatException {
      return null;
    }
  }
  return null;
}

String? _utf16Text(Uint8List bytes, String charset, {required bool allowTail}) {
  final littleBom = bytes.length >= 2 && bytes[0] == 0xff && bytes[1] == 0xfe;
  final bigBom = bytes.length >= 2 && bytes[0] == 0xfe && bytes[1] == 0xff;
  final explicitLittle = charset.endsWith('le'), explicitBig = charset.endsWith('be');
  if (explicitLittle && bigBom || explicitBig && littleBom) return null;
  if (!explicitLittle && !explicitBig && !littleBom && !bigBom) return null;
  final little = explicitLittle || !explicitBig && littleBom;
  final units = <int>[];
  var offset = littleBom || bigBom ? 2 : 0;
  int readUnit(int at) => little ? bytes[at] | bytes[at + 1] << 8 : bytes[at] << 8 | bytes[at + 1];
  while (offset + 1 < bytes.length) {
    final unit = readUnit(offset);
    offset += 2;
    if (unit >= 0xd800 && unit <= 0xdbff) {
      if (offset + 1 >= bytes.length) {
        if (!allowTail) return null;
        units.add(0xfffd);
        offset = bytes.length;
        break;
      }
      final low = readUnit(offset);
      if (low < 0xdc00 || low > 0xdfff) return null;
      units.addAll([unit, low]);
      offset += 2;
    } else if (unit >= 0xdc00 && unit <= 0xdfff) {
      return null;
    } else {
      units.add(unit);
    }
  }
  if (offset != bytes.length) {
    if (!allowTail) return null;
    units.add(0xfffd);
  }
  return String.fromCharCodes(units);
}

bool _isControl(int unit) => unit < 0x20 && unit != 0x09 && unit != 0x0a && unit != 0x0d;

String? _readableText(String text) {
  final units = text.codeUnits;
  var controls = 0;
  for (final unit in units) {
    if (unit == 0) return null;
    if (_isControl(unit)) controls++;
  }
  if (controls == 0) return text;
  if (controls * 100 > units.length) return null;
  return String.fromCharCodes(units.map((unit) => _isControl(unit) ? 0xfffd : unit));
}

String _preview(String text) {
  var end = math.min(text.length, _previewChars), lines = 0;
  for (var index = 0; index < end; index++) {
    if (text.codeUnitAt(index) == 0x0a && ++lines == _previewLines) {
      end = index;
      break;
    }
  }
  if (end > 0 && end < text.length && text.codeUnitAt(end - 1) >= 0xd800 &&
      text.codeUnitAt(end - 1) <= 0xdbff && text.codeUnitAt(end) >= 0xdc00 && text.codeUnitAt(end) <= 0xdfff) end--;
  return text.substring(0, end);
}

/// [offset] is local to this page; displayed addresses include [baseOffset].
String capturedHexLine(Uint8List bytes, int offset, int perLine, {int baseOffset = 0}) {
  RangeError.checkValueInInterval(offset, 0, bytes.length, 'offset');
  _checkHexArguments(bytes.length, perLine, baseOffset);
  final digits = capturedHexOffsetDigits(bytes.length, baseOffset: baseOffset);
  final end = math.min(offset + perLine, bytes.length), hex = StringBuffer(), ascii = StringBuffer();
  for (var index = offset; index < offset + perLine; index++) {
    if (index >= end) {
      hex.write('   ');
    } else {
      final byte = bytes[index];
      hex.write('${byte.toRadixString(16).padLeft(2, '0')} ');
      ascii.writeCharCode(byte >= 0x20 && byte < 0x7f ? byte : 0x2e);
    }
  }
  final address = (BigInt.from(baseOffset) + BigInt.from(offset)).toRadixString(16).padLeft(digits, '0');
  return '$address  $hex $ascii';
}

int capturedHexOffsetDigits(int length, {int baseOffset = 0}) {
  if (length < 0) throw RangeError.value(length, 'length');
  if (baseOffset < 0) throw RangeError.value(baseOffset, 'baseOffset');
  return math.max(6, (BigInt.from(baseOffset) + BigInt.from(length)).toRadixString(16).length);
}

int capturedHexLineLength(int length, int perLine, {int baseOffset = 0}) {
  _checkHexArguments(length, perLine, baseOffset);
  return capturedHexOffsetDigits(length, baseOffset: baseOffset) + 2 + perLine * 3 + 1 + perLine;
}

void _checkHexArguments(int length, int perLine, int baseOffset) {
  if (length < 0) throw RangeError.value(length, 'length');
  if (baseOffset < 0) throw RangeError.value(baseOffset, 'baseOffset');
  RangeError.checkValueInInterval(perLine, 1, 16, 'perLine');
}

class _ProtoBudget {
  var nodes = 0;
  bool get exhausted => nodes > _maxProtoNodes;
  bool take() => ++nodes <= _maxProtoNodes;
}

class _ProtoField {
  const _ProtoField(this.number, {this.value, this.fixedDigits = 0, this.bytes, this.text, this.nested});
  final int number, fixedDigits;
  final BigInt? value;
  final Uint8List? bytes;
  final String? text;
  final List<_ProtoField>? nested;
}

/// Unsigned wire values use BigInt so uint64 does not wrap or lose precision.
(BigInt, int)? _varint(Uint8List bytes, int offset, int end) {
  var value = BigInt.zero;
  for (var index = 0; index < 10 && offset + index < end; index++) {
    final byte = bytes[offset + index];
    if (index == 9 && byte > 1) return null;
    value |= BigInt.from(byte & 0x7f) << (index * 7);
    if (byte & 0x80 == 0) return (value, offset + index + 1);
  }
  return null;
}

List<_ProtoField>? _parseProto(Uint8List bytes, int start, int end, int depth, _ProtoBudget budget) {
  final fields = <_ProtoField>[];
  var offset = start;
  while (offset < end) {
    if (!budget.take()) return null;
    final key = _varint(bytes, offset, end);
    if (key == null || key.$1 > BigInt.from(0xffffffff)) return null;
    final tag = key.$1.toInt(), number = key.$1.toInt() >>> 3;
    if (number == 0 || number > 0x1fffffff) return null;
    offset = key.$2;
    switch (tag & 7) {
      case 0:
        final value = _varint(bytes, offset, end);
        if (value == null) return null;
        fields.add(_ProtoField(number, value: value.$1));
        offset = value.$2;
      case 1 || 5:
        final length = (tag & 7) == 1 ? 8 : 4;
        if (offset + length > end) return null;
        var value = BigInt.zero;
        for (var index = 0; index < length; index++) {
          value |= BigInt.from(bytes[offset + index]) << (index * 8);
        }
        fields.add(_ProtoField(number, value: value, fixedDigits: length * 2));
        offset += length;
      case 2:
        final length = _varint(bytes, offset, end);
        if (length == null || length.$1 > BigInt.from(end - length.$2)) return null;
        final bodyStart = length.$2, bodyEnd = length.$2 + length.$1.toInt();
        final body = Uint8List.sublistView(bytes, bodyStart, bodyEnd);
        String? printable;
        if (!body.any(_isControl)) {
          try { printable = utf8.decode(body); } on FormatException { /* Keep bytes. */ }
        }
        List<_ProtoField>? nested;
        if (printable == null && depth < _maxProtoDepth) {
          nested = _parseProto(bytes, bodyStart, bodyEnd, depth + 1, budget);
          if (budget.exhausted) return null;
        }
        fields.add(_ProtoField(number, bytes: body, text: printable,
          nested: nested == null || nested.isEmpty ? null : nested));
        offset = bodyEnd;
      default:
        return null;
    }
  }
  return fields;
}

class _BoundedText {
  final buffer = StringBuffer();
  bool append(String value) {
    if (buffer.length + value.length > _maxOutputChars) return false;
    buffer.write(value);
    return true;
  }
}

String _shortHex(Uint8List bytes) {
  final shown = Uint8List.sublistView(bytes, 0, math.min(bytes.length, _maxHexBytes));
  final hex = shown.map((byte) => byte.toRadixString(16).padLeft(2, '0')).join();
  return bytes.length > _maxHexBytes ? '<$hex… ${bytes.length} bytes>' : '<$hex>';
}

bool _writeProto(_BoundedText out, List<_ProtoField> fields, String indent) {
  for (final field in fields) {
    if (field.nested != null) {
      if (!out.append('$indent${field.number} {\n') ||
          !_writeProto(out, field.nested!, '$indent  ') || !out.append('$indent}\n')) return false;
    } else {
      final value = field.value != null ? field.fixedDigits == 0 ? field.value.toString() :
        '0x${field.value!.toRadixString(16).padLeft(field.fixedDigits, '0')}' :
        field.text != null ? jsonEncode(field.text) : _shortHex(field.bytes!);
      if (!out.append('$indent${field.number}: $value\n')) return false;
    }
  }
  return true;
}

String? _protobufText(Uint8List bytes, {bool guessed = false, _ProtoBudget? budget}) {
  final fields = _parseProto(bytes, 0, bytes.length, 0, budget ?? _ProtoBudget());
  if (fields == null || fields.isEmpty || guessed &&
      fields.any((field) => field.number > _maxGuessedFieldNumber)) return null;
  final out = _BoundedText();
  return _writeProto(out, fields, '') ? out.buffer.toString().trimRight() : null;
}

/// gRPC-Web text may have independently padded Base64 chunks. This is transport
/// decoding for the derived view; [CapturedBodyContent.bytes] stays unchanged.
Uint8List? _grpcWebText(Uint8List bytes) {
  if (bytes.any((byte) => byte > 0x7f)) return null;
  final clean = ascii.decode(bytes).replaceAll(RegExp(r'[\t\r\n ]'), '');
  final decoded = BytesBuilder(copy: false);
  var start = 0;
  while (start < clean.length) {
    var end = clean.indexOf('=', start);
    if (end < 0) {
      end = clean.length;
    } else {
      end++;
      if (end < clean.length && clean[end] == '=') end++;
    }
    final chunk = _strictBase64(clean.substring(start, end));
    if (chunk == null) return null;
    decoded.add(chunk);
    start = end;
  }
  return decoded.takeBytes();
}

String? _grpcText(Uint8List bytes, String mime, String grpcEncoding) {
  final web = mime.startsWith('application/grpc-web');
  final wire = mime.startsWith('application/grpc-web-text') ? _grpcWebText(bytes) : bytes;
  if (wire == null) return null;
  final out = _BoundedText(), budget = _ProtoBudget();
  var offset = 0, index = 0;
  while (offset < wire.length) {
    if (offset + 5 > wire.length || !budget.take()) return null;
    final flag = wire[offset];
    if (flag != 0 && flag != 1 && !(web && flag == 0x80)) return null;
    final length = wire[offset + 1] * 0x1000000 + wire[offset + 2] * 0x10000 +
        wire[offset + 3] * 0x100 + wire[offset + 4];
    final start = offset + 5;
    if (length > wire.length - start) return null;
    final end = start + length, frame = Uint8List.sublistView(wire, start, end);
    String? text;
    var note = '';
    if (flag == 0x80) {
      note = ' [trailers]';
      text = _readableText(latin1.decode(frame))?.trimRight();
    } else if (flag == 1) {
      // A frame's compressed flag is independent of HTTP Content-Encoding.
      // Retain the compressed bytes instead of assuming they are gzip.
      final coding = grpcEncoding.trim();
      final knownToken = RegExp(r'^[A-Za-z0-9._-]{1,128}$').hasMatch(coding);
      note = knownToken ? ' [compressed: $coding]' : ' [compressed]';
    } else {
      text = _protobufText(frame, budget: budget);
      if (budget.exhausted) return null;
      if (text == null && mime.endsWith('+json')) {
        final decoded = _utf8Text(frame, allowTail: false);
        text = decoded == null ? null : _readableText(decoded);
      }
    }
    if (!out.append('# ${++index}$note\n${text ?? _shortHex(frame)}\n')) return null;
    offset = end;
  }
  return index == 0 ? null : out.buffer.toString().trimRight();
}
