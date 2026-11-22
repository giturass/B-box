import 'dart:convert';
import 'dart:typed_data';

import 'package:b_box_ui/common/body_content.dart';
import 'package:flutter_test/flutter_test.dart';

CapturedBodyContent _read(List<int> bytes, {
  String contentType = '', String grpcEncoding = '', String source = 'decoded',
  bool bodyComplete = true, bool bodyTruncated = false, bool pageTruncated = false,
  bool hasMore = false, int offset = 0, String? error,
}) => readCapturedBody({
  'headers': [
    {'name': 'CONTENT-TYPE', 'value': contentType},
    if (grpcEncoding.isNotEmpty) {'name': 'grpc-encoding', 'value': grpcEncoding},
  ],
  'bodyComplete': bodyComplete, 'bodyTruncated': bodyTruncated,
}, {
  'text': base64.encode(bytes), 'encoding': 'base64', 'source': source,
  'offset': offset, 'hasMore': hasMore, 'truncated': pageTruncated,
  if (error != null) 'error': error,
});

List<int> _wireVarint(BigInt value) {
  final bytes = <int>[];
  do {
    final byte = (value & BigInt.from(0x7f)).toInt();
    value >>= 7;
    bytes.add(byte | (value == BigInt.zero ? 0 : 0x80));
  } while (value != BigInt.zero);
  return bytes;
}

List<int> _field(int number, List<int> payload) => [
  ..._wireVarint(BigInt.from((number << 3) | 2)),
  ..._wireVarint(BigInt.from(payload.length)), ...payload,
];

List<int> _frame(int flag, List<int> payload) => [
  flag, ...(ByteData(4)..setUint32(0, payload.length)).buffer.asUint8List(), ...payload,
];

void main() {
  test('text pages preserve JSON numbers, duplicate keys, escapes and exact bytes', () {
    const source = r'{"id":123456789012345678901234567890,"same":1,"same":2,"n":1.2300e+04,"s":"\u4f60"}';
    final content = readCapturedBody({
      'headers': [{'name': 'content-type', 'value': 'Application/Problem+JSON; CHARSET="UTF-8"'}],
      'bodyComplete': true,
    }, {'text': source, 'encoding': 'text', 'offset': 0});
    expect(content.kind, CapturedBodyKind.text);
    expect(content.format, CapturedBodyFormat.plain);
    expect(content.mime, 'application/problem+json');
    expect(content.charset, 'utf-8');
    expect(content.text, source);
    expect(content.bytes, utf8.encode(source));
    expect(content.complete, isTrue);
  });

  test('invalid or noncanonical Base64 produces a visible decode error', () {
    for (final source in ['%%%', 'YQ', 'YQ=', 'YQ==\n', 'YR==', '____']) {
      final content = readCapturedBody({}, {'text': source, 'encoding': 'base64'});
      expect(content.kind, CapturedBodyKind.binary, reason: source);
      expect(content.bytes, isEmpty, reason: source);
      expect(content.error, 'Invalid Base64 body page', reason: source);
    }
    expect(_read([0xff]).bytes, [0xff]);
    expect(readCapturedBody({}, {'text': 'abc', 'encoding': 'unknown'}).error,
      'Unsupported body page encoding');
  });

  test('raw pages stay binary regardless of MIME or readable content', () {
    for (final type in ['', 'text/plain', 'image/png', 'application/protobuf']) {
      final content = _read(utf8.encode('abc'), contentType: type, source: 'raw');
      expect(content.kind, CapturedBodyKind.binary, reason: type);
      expect(content.bytes, utf8.encode('abc'));
      expect(content.complete, isFalse);
    }
  });

  test('raster images require the complete first decoded page; SVG stays text', () {
    const png = [0x89, 0x50, 0x4e, 0x47];
    expect(_read(png, contentType: 'image/png').kind, CapturedBodyKind.image);
    for (final content in [
      _read(png, contentType: 'image/png', bodyComplete: false),
      _read(png, contentType: 'image/png', bodyTruncated: true),
      _read(png, contentType: 'image/png', pageTruncated: true),
      _read(png, contentType: 'image/png', hasMore: true),
      _read(png, contentType: 'image/png', offset: 16384),
      _read(png, contentType: 'image/png', error: 'decoding failed'),
    ]) {
      expect(content.kind, CapturedBodyKind.binary);
      expect(content.bytes, png);
    }
    final svg = _read(utf8.encode('<svg/>'), contentType: 'image/svg+xml');
    expect(svg.kind, CapturedBodyKind.text);
    expect(svg.text, '<svg/>');
  });

  test('sparse controls are replaced only in display and dense controls stay binary', () {
    final bytes = [...utf8.encode('a' * 200), 0x1b, ...utf8.encode('b' * 200)];
    final content = _read(bytes, contentType: 'text/plain');
    expect(content.kind, CapturedBodyKind.text);
    expect(content.text, '${'a' * 200}\ufffd${'b' * 200}');
    expect(content.bytes, bytes);
    expect(_read([...utf8.encode('a' * 500), 0], contentType: 'text/plain').kind,
      CapturedBodyKind.binary);
    expect(_read([0x08, 0x01, 0x12, 0x03, 0x61], contentType: 'text/plain').kind,
      CapturedBodyKind.binary);
  });

  test('only an incomplete final UTF-8 character is tolerated in a truncated page', () {
    final cut = [...utf8.encode('名字').take(4)];
    final content = _read(cut, contentType: 'text/plain', bodyTruncated: true);
    expect(content.text, '名\ufffd');
    expect(content.bytes, cut);
    expect(_read(cut, contentType: 'text/plain').kind, CapturedBodyKind.binary);
    expect(_read(cut, contentType: 'text/plain', bodyTruncated: true, hasMore: true).kind,
      CapturedBodyKind.binary);
    for (final invalid in [[0xff, 0xc3], [0x61, 0xe0, 0x80], [0x61, 0xed, 0xa0], [0x61, 0x80]]) {
      expect(_read(invalid, contentType: 'text/plain', bodyTruncated: true).kind,
        CapturedBodyKind.binary, reason: '$invalid');
    }
  });

  test('supported charsets decode without replacing original page bytes', () {
    const little = [0xff, 0xfe, 0x41, 0x00, 0x3d, 0xd8, 0x00, 0xde];
    const big = [0xfe, 0xff, 0x00, 0x41, 0xd8, 0x3d, 0xde, 0x00];
    for (final data in [little, big]) {
      final content = _read(data, contentType: 'text/plain');
      expect(content.text, 'A😀');
      expect(content.bytes, data);
    }
    expect(_read(little.skip(2).toList(), contentType: 'text/plain; charset=UTF-16LE').text, 'A😀');
    expect(_read(big.skip(2).toList(), contentType: 'text/plain; charset=UTF-16BE').text, 'A😀');
    expect(_read([0x63, 0x61, 0x66, 0xe9], contentType: 'text/plain; charset=iso-8859-1').text, 'café');
    expect(_read([0xef, 0xbb, 0xbf, 0x61], contentType: 'text/plain').text, 'a');
    final markup = latin1.encode('<?xml version="1.0" encoding="iso-8859-1"?><x>é</x>');
    expect(_read(markup, contentType: 'application/xml').text, latin1.decode(markup));
    expect(_read([0x41, 0], contentType: 'text/plain; charset=utf-16').kind, CapturedBodyKind.binary);
    expect(_read(big, contentType: 'text/plain; charset=utf-16le').kind, CapturedBodyKind.binary);
    expect(_read([0x00, 0xdc], contentType: 'text/plain; charset=utf-16le', bodyTruncated: true).kind,
      CapturedBodyKind.binary);
    expect(_read([0x41, 0, 0xe9], contentType: 'text/plain; charset=utf-16le', bodyTruncated: true).kind,
      CapturedBodyKind.binary);
  });

  test('UTF-16 requires a complete first page while Latin1 can be read page by page', () {
    const data = [0xff, 0xfe, 0x41, 0x00];
    for (final content in [
      _read(data, contentType: 'text/plain; charset=utf-16le', bodyComplete: false),
      _read(data, contentType: 'text/plain; charset=utf-16le', bodyTruncated: true),
      _read(data, contentType: 'text/plain; charset=utf-16le', pageTruncated: true),
      _read(data, contentType: 'text/plain; charset=utf-16le', hasMore: true),
      _read(data, contentType: 'text/plain; charset=utf-16le', offset: 16384),
      _read(data, contentType: 'text/plain; charset=utf-16le', error: 'decode failed'),
    ]) {
      expect(content.kind, CapturedBodyKind.binary);
      expect(content.bytes, data);
    }
    final latinPage = _read([0x63, 0x61, 0x66, 0xe9], contentType: 'text/plain; charset=latin1',
      offset: 16384, hasMore: true);
    expect(latinPage.kind, CapturedBodyKind.text);
    expect(latinPage.text, 'café');
  });

  test('unsupported declared charsets keep bytes and do not guess text or Protobuf', () {
    const gbk = [0xc4, 0xe3, 0xba, 0xc3];
    final content = _read(gbk, contentType: 'text/plain; charset=GBK');
    expect(content.kind, CapturedBodyKind.binary);
    expect(content.charset, 'gbk');
    expect(content.bytes, gbk);
    expect(_read(utf8.encode('ascii'), contentType: 'text/plain; charset=big5').kind,
      CapturedBodyKind.binary);
    expect(_read(latin1.encode('<meta charset="big5">'), contentType: 'text/html').charset, 'big5');
    expect(_read([0xc3, 0xa9], contentType: 'text/plain; charset=us-ascii').kind,
      CapturedBodyKind.binary);
  });

  test('previews stop at 30 lines or 3000 characters without splitting a surrogate pair', () {
    final lines = _read(utf8.encode(List.generate(100, (index) => 'line $index').join('\n')));
    expect(lines.preview.split('\n'), hasLength(30));
    expect(lines.isCut, isTrue);
    final chars = _read(utf8.encode('${'a' * 2999}😀tail'));
    expect(chars.preview, 'a' * 2999);
    expect(chars.isCut, isTrue);
    expect(_read(utf8.encode('short')).isCut, isFalse);
  });

  group('schema-free Protobuf', () {
    const message = [0x08, 0x96, 0x01, 0x12, 0x02, 0x68, 0x69, 0x1a, 0x02, 0x08, 0x02];
    const shown = '1: 150\n2: "hi"\n3 {\n  1: 2\n}';

    test('preserves field order, repeats and unsigned 64-bit integers', () {
      final content = _read(message, contentType: 'application/x-protobuf');
      expect(content.format, CapturedBodyFormat.protobuf);
      expect(content.text, shown);
      expect(content.bytes, message);
      final maxUint = _wireVarint(BigInt.parse('18446744073709551615'));
      final large = _read([0x08, ...maxUint, 0x08, 0x01, 0x09, ...List.filled(8, 0xff),
        0x15, ...List.filled(4, 0xff)], contentType: 'application/protobuf');
      expect(large.text, '1: 18446744073709551615\n1: 1\n1: 0xffffffffffffffff\n2: 0xffffffff');
    });

    test('guesses only a complete untyped or octet-stream binary body', () {
      for (final type in ['', 'application/octet-stream']) {
        expect(_read(message, contentType: type).format, CapturedBodyFormat.protobuf);
      }
      expect(_read(message, contentType: 'font/woff2').kind, CapturedBodyKind.binary);
      for (final content in [
        _read(message, contentType: 'application/protobuf', bodyComplete: false),
        _read(message, contentType: 'application/protobuf', bodyTruncated: true),
        _read(message, contentType: 'application/protobuf', hasMore: true),
        _read(message, contentType: 'application/protobuf', offset: 16384),
      ]) {
        expect(content.kind, CapturedBodyKind.binary);
        expect(content.format, CapturedBodyFormat.plain);
      }
      final implausible = [..._wireVarint(BigInt.from(65537 << 3)), 1];
      expect(_read(implausible, contentType: 'application/octet-stream').kind, CapturedBodyKind.binary);
      expect(_read(implausible, contentType: 'application/protobuf').format, CapturedBodyFormat.protobuf);
    });

    test('invalid lengths, tags and uint64 overflow fall back to exact binary bytes', () {
      for (final data in [[0], [0x12, 0xff, 0xff, 0x7f], [0x08, ...List.filled(9, 0xff), 2],
        [0x0b], [0x08, 0x01, 0x12, 0x02, 0x61]]) {
        final content = _read(data, contentType: 'application/protobuf');
        expect(content.kind, CapturedBodyKind.binary, reason: '$data');
        expect(content.bytes, data);
      }
    });

    test('depth, field and formatted output budgets prevent expansive derived views', () {
      var deep = <int>[0x08, 1];
      for (var index = 0; index < 24; index++) { deep = _field(1, deep); }
      final limited = _read(deep, contentType: 'application/protobuf');
      expect(limited.format, CapturedBodyFormat.protobuf);
      expect(limited.text.length, lessThan(2000));
      final many = [for (var index = 0; index < 4097; index++) ...[0x08, 1]];
      expect(_read(many, contentType: 'application/protobuf').kind, CapturedBodyKind.binary);
      var expanded = [for (var index = 0; index < 2000; index++) ...[0x08, 1]];
      for (var index = 0; index < 16; index++) { expanded = _field(1, expanded); }
      expect(_read(expanded, contentType: 'application/protobuf').kind, CapturedBodyKind.binary);
    });
  });

  group('gRPC', () {
    test('reads complete message frames and gRPC-Web trailers', () {
      final bytes = [..._frame(0, [0x08, 1]), ..._frame(0, [0x08, 2]),
        ..._frame(0x80, utf8.encode('grpc-status: 0\r\n'))];
      final content = _read(bytes, contentType: 'application/grpc-web+proto');
      expect(content.format, CapturedBodyFormat.grpc);
      expect(content.text, '# 1\n1: 1\n# 2\n1: 2\n# 3 [trailers]\ngrpc-status: 0');
      expect(content.bytes, bytes);
      expect(_read(bytes, contentType: 'application/grpc+proto').kind, CapturedBodyKind.binary);
    });

    test('compressed frame coding is marked while compressed bytes stay intact', () {
      final bytes = _frame(1, [0xff, 0x00, 0xab]);
      final content = _read(bytes, contentType: 'application/grpc', grpcEncoding: 'deflate');
      expect(content.format, CapturedBodyFormat.grpc);
      expect(content.text, '# 1 [compressed: deflate]\n<ff00ab>');
      expect(content.bytes, bytes);
      expect(_read(bytes, contentType: 'application/grpc', grpcEncoding: 'x' * 1000).text,
        '# 1 [compressed]\n<ff00ab>');
    });

    test('gRPC-Web text decodes independent padding only for the derived view', () {
      final wire = [..._frame(0, [0x08, 1]), ..._frame(0x80, utf8.encode('grpc-status: 0'))];
      final transport = ascii.encode('${base64.encode(wire.take(2).toList())}\r\n${base64.encode(wire.skip(2).toList())}');
      final content = _read(transport, contentType: 'application/grpc-web-text+proto');
      expect(content.format, CapturedBodyFormat.grpc);
      expect(content.text, '# 1\n1: 1\n# 2 [trailers]\ngrpc-status: 0');
      expect(content.bytes, transport);
      expect(_read(transport, contentType: 'application/grpc-web-text+proto', hasMore: true).kind,
        CapturedBodyKind.binary);
    });

    test('partial frames, unsupported flags and malformed transport stay binary', () {
      for (final data in [[0, 0, 0, 0, 2, 8], _frame(2, [0x08, 1]),
        [..._frame(0, [0x08, 1]), 0]]) {
        final content = _read(data, contentType: 'application/grpc');
        expect(content.kind, CapturedBodyKind.binary);
        expect(content.bytes, data);
      }
      expect(_read(ascii.encode('bad==='), contentType: 'application/grpc-web-text+proto').kind,
        CapturedBodyKind.binary);
      expect(_read(_frame(0, [0x08, 1]), contentType: 'application/grpc', bodyTruncated: true).kind,
        CapturedBodyKind.binary);
    });
  });

  test('hex columns keep absolute page addresses, spacing and unsigned large offsets', () {
    final bytes = Uint8List.fromList([0x41, 0, 0xff, 0x7e, 0x20]);
    expect(capturedHexLine(bytes, 0, 4), '000000  41 00 ff 7e  A..~');
    expect(capturedHexLine(bytes, 4, 4), '000004  20           ' ' ');
    expect(capturedHexLine(bytes, 0, 4, baseOffset: 16384), '004000  41 00 ff 7e  A..~');
    expect(capturedHexOffsetDigits(bytes.length, baseOffset: 0xffffff), 7);
    expect(capturedHexLineLength(bytes.length, 4), 6 + 2 + 12 + 1 + 4);
    expect(capturedHexLine(bytes, 4, 4, baseOffset: 0x7fffffffffffffff),
      startsWith('8000000000000003  '));
    expect(() => capturedHexLine(bytes, -1, 4), throwsRangeError);
    expect(() => capturedHexLine(bytes, 6, 4), throwsRangeError);
    expect(() => capturedHexLine(bytes, 0, 17), throwsRangeError);
    expect(() => capturedHexLineLength(5, 0), throwsRangeError);
    expect(() => capturedHexOffsetDigits(-1), throwsRangeError);
  });
}
