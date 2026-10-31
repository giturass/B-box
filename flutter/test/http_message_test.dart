import 'package:b_box_ui/common/http_message.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('JSON formatting preserves exact numbers, duplicate keys and escapes', () {
    const source = r'{"id":123456789012345678901234567890,"n":1.2300e+04,"same":1,"same":2,"text":"a\\b\"{},:\u4f60","items":[{},[],true,null]}';
    expect(formatCapturedJson(source), r'''{
  "id": 123456789012345678901234567890,
  "n": 1.2300e+04,
  "same": 1,
  "same": 2,
  "text": "a\\b\"{},:\u4f60",
  "items": [
    {},
    [],
    true,
    null
  ]
}''');
    expect(formatCapturedJson('  { "empty" : [ ] }\r\n'), '{\n  "empty": []\n}');
  });

  test('partial, invalid and excessively nested or expanded JSON stays raw', () {
    for (final source in ['{"unfinished":', '[1,]', '{"line":"\n"}', '<html>body</html>', 'plain text']) {
      expect(formatCapturedJson(source), isNull, reason: source);
    }
    expect(formatCapturedJson('${'[' * 65}0${']' * 65}'), isNull);
    expect(formatCapturedJson('["${'a' * 65536}"]'), isNull);
    expect(formatCapturedJson('${'[' * 64}${List.filled(3000, '0').join(',')}${']' * 64}'), isNull);
  });

  test('preview paging does not split a surrogate pair', () {
    final source = '${'a' * 16383}😀tail';
    expect(capturedTextPrefix(source, 16384), 'a' * 16383);
    expect(capturedTextPrefix(source, 16385), '${'a' * 16383}😀');
    expect(capturedTextPrefix(source, 32768), source);
    expect(capturedTextPrefix(source, 0), '');
  });

  test('request cookies keep repeated names, empty values and embedded equals', () {
    final cookies = capturedCookies([
      {'name': 'cOoKiE', 'value': 'token=abc==; empty=; quoted="a=b"; token=second; invalid'},
      {'name': 'Set-Cookie', 'value': 'ignored=response'},
    ], response: false);
    expect(cookies.map((cookie) => cookie.name), ['token', 'empty', 'quoted', 'token']);
    expect(cookies.map((cookie) => cookie.value), ['abc==', '', '"a=b"', 'second']);
    expect(cookies.first.raw, 'token=abc==');
    expect(cookies.every((cookie) => cookie.attributes.isEmpty), isTrue);
  });

  test('response cookies preserve separate headers and Expires commas', () {
    const first = 'session=abc==; Expires=Wed, 09 Jun 2027 10:18:14 GMT; Path=/; Secure; HttpOnly; SameSite=None';
    const second = 'session=other; Path=/api';
    final cookies = capturedCookies([
      {'name': 'Set-Cookie', 'value': first},
      {'name': 'set-cookie', 'value': second},
      {'name': 'Cookie', 'value': 'ignored=request'},
    ], response: true);
    expect(cookies.map((cookie) => cookie.name), ['session', 'session']);
    expect(cookies.map((cookie) => cookie.raw), [first, second]);
    expect(cookies.first.attributes, ['Expires=Wed, 09 Jun 2027 10:18:14 GMT', 'Path=/', 'Secure', 'HttpOnly', 'SameSite=None']);
    expect(cookies.last.value, 'other');
  });

  test('content type lookup uses case insensitive header names', () {
    expect(capturedHeader([{'name': 'CONTENT-type', 'value': 'application/problem+json; charset=utf-8'}], 'content-type'),
      'application/problem+json; charset=utf-8');
    expect(capturedHeader([], 'Content-Type'), '');
  });
}
