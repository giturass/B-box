import 'package:b_box_ui/common/surge_module.dart';
import 'package:flutter_test/flutter_test.dart';

const _module = r'''
#!name=Example
#!desc=Rewrites the example API
#!arguments=host:api.example.test,flag:on

[MITM]
hostname = %APPEND% {{{host}}}, *.example.test:8443
h2 = true

[URL Rewrite]
# a comment
^https?://{{{host}}}/old https://{{{host}}}/new 302

[Header Rewrite]
http-request ^https?://{{{host}}}/ header-add X-Flag {{{flag}}}

[Body Rewrite]
http-response-jq ^https?://{{{host}}}/json$ '.debug = true'

[Map Local]
^https?://{{{host}}}/mock data-type=text data="{}" status-code=200

[Script]
reply = type=http-response,pattern=^https?://{{{host}}}/a{1,3}$,script-path=https://example.test/reply.js,requires-body=true,max-size=-1,timeout=10,argument="a,b"
nightly = type=cron,cronexp="0 2 * * *",script-path=cron.js
http-request ^https?://{{{host}}}/legacy script-path=legacy.js,binary-body-mode=true,requires-body=true
started = type=event,event-name=engine-started,script-path=started.js
notice = type=event,event-name=notification,script-path=notice.js
resolver = type=dns,script-path=dns.js
manual = script-path=manual.js,debug=true
broken = type=generic

[Rule]
DOMAIN,example.test,DIRECT
DOMAIN,example.org,DIRECT
''';

void main() {
  test('a Surge module is reduced to MITM hosts, rewrite lines and scripts', () {
    final module = parseSurgeModule(_module, values: {'flag': 'off'});
    expect(module.name, 'Example');
    expect(module.description, 'Rewrites the example API');
    expect(module.arguments, {'host': 'api.example.test', 'flag': 'on'});
    expect(module.hosts, ['api.example.test', '*.example.test:8443']);
    expect(module.frontHosts, isEmpty);
    expect(module.rewrites, {
      'url_rewrite': [r'^https?://api.example.test/old https://api.example.test/new 302'],
      'header_rewrite': [r'http-request ^https?://api.example.test/ header-add X-Flag off'],
      'body_rewrite': [r"http-response-jq ^https?://api.example.test/json$ '.debug = true'"],
      'map_local': [r'^https?://api.example.test/mock data-type=text data="{}" status-code=200'],
    });
    expect(module.scripts['http'], [
      {'name': 'reply', 'script_path': 'https://example.test/reply.js', 'timeout_ms': 10000, 'argument': 'a,b',
        'pattern': r'^https?://api.example.test/a{1,3}$', 'phase': 'response', 'requires_body': true, 'max_body_bytes': -1},
      {'name': 'legacy.js', 'script_path': 'legacy.js', 'pattern': r'^https?://api.example.test/legacy', 'phase': 'request',
        'requires_body': true, 'binary_body': true},
    ]);
    expect(module.scripts['cron'], [{'name': 'nightly', 'script_path': 'cron.js', 'cron': '0 2 * * *'}]);
    expect(module.scripts['event'], [{'name': 'started', 'script_path': 'started.js', 'event': 'engine-started'}]);
    expect(module.scripts['generic'], [{'name': 'manual', 'script_path': 'manual.js', 'debug': true}]);
    expect(module.skipped, ['[MITM] h2', '[Script] notice (event-name=notification)', '[Script] resolver (type=dns)',
      '[Script] broken (script-path)', '[Rule] × 2']);
    expect(module.isEmpty, isFalse);
    expect(parseSurgeModule('[Rule]\nFINAL,DIRECT').isEmpty, isTrue);
  });

  test('module entries go above the existing ones without repeating or enabling anything', () {
    final module = parseSurgeModule('[MITM]\nhostname = %INSERT% first.test, kept.test\n[Body Rewrite]\nnew line\nold line\n'
      '[Script]\njob = type=cron,cronexp="0 2 * * *",script-path=cron.js');
    final before = {
      'cache_file': {'enabled': true},
      'mitm': {'enabled': false, 'hosts': ['kept.test']},
      'http_rewrite': {'body_rewrite': ['old line', {'pattern': 'p', 'phase': 'response', 'jq': '.'}]},
    };
    final after = applySurgeModule(before, module);
    expect(after['cache_file'], {'enabled': true});
    expect(after['mitm'], {'enabled': false, 'hosts': ['first.test', 'kept.test']});
    expect(after['http_rewrite'], {'body_rewrite': ['new line', 'old line', {'pattern': 'p', 'phase': 'response', 'jq': '.'}]});
    expect(after['scripts'], {'cron': [{'name': 'job', 'script_path': 'cron.js', 'cron': '0 2 * * *'}]});
    expect(before['mitm'], {'enabled': false, 'hosts': ['kept.test']});
    expect(applySurgeModule(after, module), after);
  });
}
