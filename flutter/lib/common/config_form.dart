import 'dart:convert';
import '../box.dart';

// These fields follow option/ at sing-box v1.15.0-alpha.13 and the independent
// B-box processing options in libbox/httpdebug. The native validator remains
// authoritative. A form edits individual fields, never rebuilds an imported item.
enum ConfigKind { dnsServer, routeRule, dnsRule, outbound, inbound, endpoint, ruleSet,
  headlessRule, urlRewrite, headerRewrite, bodyRewrite, mapLocal, mapRemote, httpScript, cronScript, resource, header, user, processingScope }

enum ConfigInput { text, secret, integer, lines, ports, toggle, choice, choices, apps }

class ConfigFieldSpec {
  const ConfigFieldSpec(this.key, this.label, {this.input = ConfigInput.text, this.required = false,
    this.options = const [], this.fallback, this.min = 0, this.max = 0xffffffff, this.multiline = false, this.hint});
  final String key, label;
  final ConfigInput input;
  final bool required, multiline;
  final List<String> options;
  final Object? fallback;
  final int min, max;
  final String? hint;
}

dynamic configValue(Json data, String key) {
  dynamic value = data;
  for (final part in key.split('.')) {
    if (value is! Map) return null;
    value = value[part];
  }
  return value;
}

Json copyConfig(Json value) => json(jsonDecode(jsonEncode(value)));

void setConfigValue(Json data, String key, dynamic value) {
  final parts = key.split('.');
  var parent = data;
  for (final part in parts.take(parts.length - 1)) {
    // Do not replace the other members of a nested option (TLS, resolver, etc.).
    final next = json(parent[part]);
    parent[part] = next;
    parent = next;
  }
  if (value == null) { parent.remove(parts.last); } else { parent[parts.last] = value; }
}

List<String> configStrings(dynamic value) => value == null ? [] : value is List
  ? value.map((item) => item.toString()).toList() : [value.toString()];

List<String> configTypes(ConfigKind kind) => switch (kind) {
  ConfigKind.dnsServer => ['local', 'udp', 'tcp', 'tls', 'https', 'quic', 'h3', 'fakeip'],
  ConfigKind.outbound => ['socks', 'http', 'shadowsocks', 'trojan', 'vless', 'vmess', 'hysteria2', 'tuic', 'direct', 'selector', 'urltest'],
  ConfigKind.inbound => ['mixed', 'socks', 'http', 'tun'],
  ConfigKind.endpoint => ['tailscale'],
  ConfigKind.ruleSet => ['remote', 'local', 'inline'],
  _ => [],
};

String configTypeLabel(String type) => const {
  'local': 'Local', 'udp': 'UDP', 'tcp': 'TCP', 'tls': 'DNS over TLS', 'https': 'DNS over HTTPS',
  'quic': 'DNS over QUIC', 'h3': 'DNS over HTTP/3', 'fakeip': 'Fake IP', 'socks': 'SOCKS', 'http': 'HTTP',
  'shadowsocks': 'Shadowsocks', 'trojan': 'Trojan', 'vless': 'VLESS', 'vmess': 'VMess',
  'hysteria2': 'Hysteria2', 'tuic': 'TUIC', 'direct': 'Direct', 'selector': 'Selector',
  'urltest': 'Automatic selection', 'mixed': 'HTTP and SOCKS', 'tun': 'VPN', 'tailscale': 'Tailscale',
  'remote': 'Remote', 'inline': 'Built-in rules',
}[type] ?? type;

Json newConfigEntry(ConfigKind kind, [String? type]) {
  type ??= configTypes(kind).firstOrNull;
  return switch (kind) {
    ConfigKind.dnsServer => {'type': type, 'tag': 'new-dns',
      if (type == 'fakeip') 'inet4_range': '198.18.0.0/15'},
    ConfigKind.outbound => {'type': type, 'tag': 'new-proxy',
      if (type == 'socks') 'version': '5',
      if (type == 'shadowsocks') 'method': 'aes-128-gcm',
      if (type == 'vmess') 'security': 'auto',
      if (['trojan', 'hysteria2', 'tuic'].contains(type)) 'tls': {'enabled': true},
      if (['selector', 'urltest'].contains(type)) 'outbounds': <String>[]},
    ConfigKind.inbound => {'type': type, 'tag': 'new-inbound',
      if (type == 'tun') ...{'address': ['172.19.0.1/30', 'fdfe:dcba:9876::1/126'], 'auto_route': true}
      else ...{'listen': '127.0.0.1', 'listen_port': 2080}},
    ConfigKind.endpoint => {'type': type, 'tag': 'new-endpoint'},
    ConfigKind.ruleSet => {'type': type, 'tag': 'new-rules',
      if (type == 'inline') 'rules': <Json>[] else 'format': 'binary'},
    ConfigKind.routeRule || ConfigKind.dnsRule => {'action': 'route'},
    ConfigKind.headlessRule => {},
    ConfigKind.resource => {'data_type': 'text', 'data': ''},
    _ => {'disabled': true},
  };
}

List<ConfigFieldSpec> configFields(ConfigKind kind, Json data, {List<String> outbounds = const [],
    List<String> servers = const [], List<String> ruleSets = const []}) {
  const text = ConfigInput.text, secret = ConfigInput.secret, integer = ConfigInput.integer;
  const lines = ConfigInput.lines, toggle = ConfigInput.toggle, choice = ConfigInput.choice, choices = ConfigInput.choices;
  final type = data['type'] as String? ?? '';
  final fields = <ConfigFieldSpec>[];
  if (kind == ConfigKind.resource) fields.add(const ConfigFieldSpec('name', 'Name', required: true));
  if (kind == ConfigKind.header) fields.addAll(const [
    ConfigFieldSpec('name', 'Header name', required: true), ConfigFieldSpec('value', 'Header value'),
  ]);
  if (kind == ConfigKind.user) fields.addAll(const [
    ConfigFieldSpec('username', 'Username', required: true), ConfigFieldSpec('password', 'Password', input: secret),
  ]);
  if (kind == ConfigKind.processingScope) fields.addAll(const [
    ConfigFieldSpec('ports', 'Destination ports', input: ConfigInput.ports, min: 1, max: 65535),
    ConfigFieldSpec('packages', 'Applications', input: ConfigInput.apps),
  ]);
  ConfigFieldSpec select(String key, String label, List<String> values, {bool required = false, Object? fallback}) =>
    ConfigFieldSpec(key, label, input: choice, options: required ? values : ['', ...values], required: required, fallback: fallback);
  if (configTypes(kind).isNotEmpty) {
    fields.add(ConfigFieldSpec('tag', 'Name', required: true, input: kind == ConfigKind.ruleSet ? lines : text));
  }
  if (kind == ConfigKind.dnsServer) {
    if (['udp', 'tcp', 'tls', 'https', 'quic', 'h3'].contains(type)) {
      fields.addAll([
        const ConfigFieldSpec('server', 'Server address', required: true, hint: '1.1.1.1'),
        const ConfigFieldSpec('server_port', 'Port', input: integer, min: 1, max: 65535),
        if (['tls', 'https', 'quic', 'h3'].contains(type)) const ConfigFieldSpec('tls.server_name', 'TLS server name'),
        if (['https', 'h3'].contains(type)) const ConfigFieldSpec('path', 'Request path', hint: '/dns-query'),
        select('detour', 'Connect through', outbounds),
      ]);
    }
    if (type == 'fakeip') fields.addAll(const [
      ConfigFieldSpec('inet4_range', 'IPv4 address range'), ConfigFieldSpec('inet6_range', 'IPv6 address range'),
    ]);
  }
  if ([ConfigKind.routeRule, ConfigKind.dnsRule, ConfigKind.headlessRule].contains(kind)) {
    if (type != 'logical') fields.addAll([
      const ConfigFieldSpec('domain_suffix', 'Domains and subdomains', input: lines, hint: 'example.com'),
      const ConfigFieldSpec('domain', 'Exact domains', input: lines, hint: 'api.example.com'),
      if (kind != ConfigKind.dnsRule) const ConfigFieldSpec('ip_cidr', 'Destination IP ranges', input: lines, hint: '192.168.0.0/16'),
      const ConfigFieldSpec('package_name', 'Applications', input: ConfigInput.apps),
      if (kind != ConfigKind.headlessRule) ConfigFieldSpec('rule_set', 'Rule sets', input: choices, options: ruleSets),
      const ConfigFieldSpec('network', 'Network protocol', input: choices, options: ['tcp', 'udp']),
      if (kind == ConfigKind.routeRule) const ConfigFieldSpec('port', 'Destination ports', input: ConfigInput.ports, min: 1, max: 65535),
    ]);
    if (kind != ConfigKind.headlessRule) {
      final action = data['action'] as String? ?? 'route';
      fields.add(select('action', 'Action', kind == ConfigKind.dnsRule ? ['route', 'reject'] : ['route', 'reject', 'hijack-dns', 'sniff'], required: true, fallback: 'route'));
      if (action == 'route') fields.add(kind == ConfigKind.dnsRule
        ? select('server', 'DNS server', servers, required: true)
        : select('outbound', 'Send through', outbounds, required: true));
    }
    fields.add(const ConfigFieldSpec('invert', 'Invert match', input: toggle));
  }
  if (kind == ConfigKind.outbound) {
    if (['selector', 'urltest'].contains(type)) {
      fields.add(ConfigFieldSpec('outbounds', 'Group members', input: choices, options: outbounds, required: true));
      if (type == 'selector') fields.add(select('default', 'Default node', configStrings(data['outbounds'])));
      if (type == 'urltest') fields.addAll(const [
        ConfigFieldSpec('url', 'Latency test URL'), ConfigFieldSpec('interval', 'Test interval', hint: '3m'),
        ConfigFieldSpec('tolerance', 'Latency tolerance (ms)', input: integer, max: 65535),
      ]);
      fields.add(const ConfigFieldSpec('interrupt_exist_connections', 'Close connections after switching', input: toggle));
    } else if (configTypes(kind).contains(type) && type != 'direct') {
      fields.addAll(const [
        ConfigFieldSpec('server', 'Server address', required: true),
        ConfigFieldSpec('server_port', 'Port', required: true, input: integer, min: 1, max: 65535),
      ]);
      if (type == 'socks') fields.add(select('version', 'SOCKS version', ['4', '4a', '5'], fallback: '5'));
      if (['socks', 'http'].contains(type)) fields.add(const ConfigFieldSpec('username', 'Username'));
      if (['socks', 'http', 'shadowsocks', 'trojan', 'hysteria2', 'tuic'].contains(type)) {
        fields.add(ConfigFieldSpec('password', 'Password', input: secret, required: ['shadowsocks', 'trojan'].contains(type)));
      }
      if (['vmess', 'vless', 'tuic'].contains(type)) fields.add(const ConfigFieldSpec('uuid', 'UUID', required: true));
      if (type == 'shadowsocks') fields.add(select('method', 'Encryption', [
        'aes-128-gcm', 'aes-192-gcm', 'aes-256-gcm', 'chacha20-ietf-poly1305', 'xchacha20-ietf-poly1305', '2022-blake3-aes-128-gcm',
        '2022-blake3-aes-256-gcm', '2022-blake3-chacha20-poly1305', 'none',
        'aes-128-ctr', 'aes-192-ctr', 'aes-256-ctr', 'aes-128-cfb', 'aes-192-cfb', 'aes-256-cfb',
        'rc4-md5', 'chacha20-ietf', 'xchacha20',
      ], required: true));
      if (type == 'vmess') fields.add(select('security', 'Encryption', ['auto', 'none', 'zero', 'aes-128-gcm', 'chacha20-poly1305'], fallback: 'auto'));
      if (type == 'vless') fields.add(select('flow', 'Flow', ['xtls-rprx-vision']));
      if (!['socks', 'shadowsocks'].contains(type)) fields.addAll([
        const ConfigFieldSpec('tls.enabled', 'Use TLS', input: toggle),
        if (configValue(data, 'tls.enabled') == true) ...const [
          ConfigFieldSpec('tls.server_name', 'TLS server name'),
          ConfigFieldSpec('tls.insecure', 'Skip server certificate verification', input: toggle),
        ],
      ]);
      if (['vmess', 'vless', 'trojan'].contains(type)) {
        if (configValue(data, 'tls.enabled') == true) fields.addAll([
          const ConfigFieldSpec('tls.reality.enabled', 'Use Reality', input: toggle),
          if (configValue(data, 'tls.reality.enabled') == true) ...const [
            ConfigFieldSpec('tls.reality.public_key', 'Reality public key', required: true),
            ConfigFieldSpec('tls.reality.short_id', 'Reality short ID'),
          ],
          const ConfigFieldSpec('tls.utls.enabled', 'Use browser TLS fingerprint', input: toggle),
          if (configValue(data, 'tls.utls.enabled') == true)
            select('tls.utls.fingerprint', 'TLS fingerprint', ['chrome', 'firefox', 'edge', 'safari', 'ios', 'android', 'random', 'randomized']),
        ]);
        fields.add(select('transport.type', 'Transport', ['ws', 'http', 'grpc', 'httpupgrade', 'quic']));
        final transport = configValue(data, 'transport.type');
        if (['ws', 'http', 'httpupgrade'].contains(transport)) fields.add(const ConfigFieldSpec('transport.path', 'Request path'));
        if (transport == 'http') fields.add(const ConfigFieldSpec('transport.host', 'Host names', input: lines));
        if (transport == 'ws') fields.add(const ConfigFieldSpec('transport.headers.Host', 'Host names', input: lines));
        if (transport == 'httpupgrade') fields.add(const ConfigFieldSpec('transport.host', 'Host name'));
        if (transport == 'grpc') fields.add(const ConfigFieldSpec('transport.service_name', 'gRPC service name'));
      }
      if (type == 'hysteria2') fields.addAll([
        select('obfs.type', 'Obfuscation', ['salamander', 'gecko']),
        if (configValue(data, 'obfs.type') != null) const ConfigFieldSpec('obfs.password', 'Obfuscation password', input: secret),
        const ConfigFieldSpec('up_mbps', 'Upload bandwidth (Mbps)', input: integer),
        const ConfigFieldSpec('down_mbps', 'Download bandwidth (Mbps)', input: integer),
      ]);
      // A resolver can also be an object. Keep its extra policy options intact.
      if (data['domain_resolver'] is! Map) fields.add(select('domain_resolver', 'Resolve server using', servers));
    }
  }
  if (kind == ConfigKind.inbound) {
    if (type == 'tun') fields.addAll(const [
      ConfigFieldSpec('address', 'Interface addresses', input: lines, required: true),
      ConfigFieldSpec('mtu', 'MTU', input: integer, min: 576, max: 65535),
      ConfigFieldSpec('auto_route', 'Route device traffic', input: toggle),
      ConfigFieldSpec('include_package', 'Only these applications', input: ConfigInput.apps),
      ConfigFieldSpec('exclude_package', 'Bypass these applications', input: ConfigInput.apps),
    ]);
    if (['mixed', 'socks', 'http'].contains(type)) fields.addAll(const [
      ConfigFieldSpec('listen', 'Listen address', hint: '127.0.0.1'),
      ConfigFieldSpec('listen_port', 'Port', input: integer, min: 1, max: 65535, required: true),
    ]);
  }
  if (kind == ConfigKind.endpoint && type == 'tailscale') fields.addAll(const [
    ConfigFieldSpec('hostname', 'Device name'), ConfigFieldSpec('auth_key', 'Auth key', input: secret),
    ConfigFieldSpec('control_url', 'Control server URL'),
    ConfigFieldSpec('accept_routes', 'Accept subnet routes', input: toggle),
    ConfigFieldSpec('exit_node', 'Exit node'),
    ConfigFieldSpec('exit_node_allow_lan_access', 'Allow LAN with exit node', input: toggle),
  ]);
  if (kind == ConfigKind.ruleSet && type != 'inline') fields.addAll([
    if (type == 'remote') const ConfigFieldSpec('url', 'Download URL', required: true),
    if (type == 'local') const ConfigFieldSpec('path', 'Path', required: true),
    select('format', 'Rule format', ['binary', 'source'], required: true, fallback: 'binary'),
    if (type == 'remote') const ConfigFieldSpec('update_interval', 'Update interval', hint: '1d'),
  ]);
  if ([ConfigKind.urlRewrite, ConfigKind.headerRewrite, ConfigKind.bodyRewrite, ConfigKind.mapLocal,
      ConfigKind.mapRemote, ConfigKind.httpScript, ConfigKind.cronScript].contains(kind)) {
    fields.addAll([
      const ConfigFieldSpec('name', 'Name'),
      const ConfigFieldSpec('disabled', 'Enabled', input: toggle, fallback: false),
      if (kind != ConfigKind.cronScript) const ConfigFieldSpec('pattern', 'URL pattern', required: true, hint: r'^https?://example\.com/'),
    ]);
  }
  if (kind == ConfigKind.urlRewrite) fields.addAll([
    select('action', 'Action', ['header', '302', '307', 'reject', 'reject-200', 'reject-dict'], required: true),
    if (['header', '302', '307'].contains(data['action'])) const ConfigFieldSpec('replacement', 'Target URL', required: true),
  ]);
  if ([ConfigKind.headerRewrite, ConfigKind.bodyRewrite, ConfigKind.httpScript].contains(kind)) {
    fields.add(select('phase', 'When to run', ['request', 'response'], required: true));
  }
  if (kind == ConfigKind.headerRewrite) fields.addAll([
    select('operation', 'Header operation', ['add', 'delete', 'replace', 'replace_regex'], required: true),
    const ConfigFieldSpec('header', 'Header name', required: true),
    if (['add', 'replace'].contains(data['operation'])) const ConfigFieldSpec('value', 'Header value'),
    if (data['operation'] == 'replace_regex') ...const [
      ConfigFieldSpec('regex', 'Text pattern', required: true), ConfigFieldSpec('replacement', 'Replacement'),
    ],
  ]);
  if (kind == ConfigKind.bodyRewrite) fields.addAll([
    if (data.containsKey('jq')) const ConfigFieldSpec('jq', 'jq filter', multiline: true)
    else ...const [ConfigFieldSpec('regex', 'Text pattern', required: true), ConfigFieldSpec('replacement', 'Replacement', multiline: true)],
  ]);
  if (kind == ConfigKind.mapRemote) fields.addAll(const [
    ConfigFieldSpec('replacement', 'Target URL', required: true), ConfigFieldSpec('preserve_host', 'Keep original Host header', input: toggle),
  ]);
  if (kind == ConfigKind.mapLocal) fields.add(const ConfigFieldSpec('status', 'Response status', input: integer, min: 200, max: 999, fallback: 200));
  if ([ConfigKind.mapLocal, ConfigKind.resource].contains(kind)) {
    if (data['resource'] != null) fields.add(const ConfigFieldSpec('resource', 'Resource name', required: true));
    else fields.addAll([
      select('data_type', 'Content source', ['text', 'base64', 'file'], fallback: 'text'),
      ConfigFieldSpec('data', data['data_type'] == 'file' ? 'File path or URL' : 'Response content', multiline: true),
    ]);
  }
  if ([ConfigKind.httpScript, ConfigKind.cronScript].contains(kind)) {
    if (kind == ConfigKind.cronScript) fields.addAll(const [
      ConfigFieldSpec('cron', 'Schedule', required: true, hint: '0 2 * * *'), ConfigFieldSpec('timezone', 'Timezone'),
    ]);
    if (data['script_path'] != null) fields.add(const ConfigFieldSpec('script_path', 'Script file or URL', required: true));
    else if (data['resource'] != null) fields.add(const ConfigFieldSpec('resource', 'Resource name', required: true));
    else fields.add(const ConfigFieldSpec('source', 'JavaScript', required: true, multiline: true));
    if (kind == ConfigKind.httpScript) fields.add(const ConfigFieldSpec('requires_body', 'Read message body', input: toggle));
    fields.add(const ConfigFieldSpec('timeout_ms', 'Timeout (ms)', input: integer, min: 1, max: 86400000));
  }
  return fields;
}

String configEntrySummary(Json entry, String Function(String) tr) {
  final parts = <String>[];
  if (entry['disabled'] is bool) parts.add(tr(entry['disabled'] == true ? 'Disabled' : 'Enabled'));
  if (entry['type'] is String) parts.add(tr(configTypeLabel(entry['type'] as String)));
  if (entry['server'] != null) parts.add('${entry['server']}${entry['server_port'] == null ? '' : ':${entry['server_port']}'}');
  for (final field in ['domain', 'domain_suffix', 'ip_cidr', 'package_name', 'rule_set']) {
    if (entry[field] != null) parts.add(configStrings(entry[field]).join(', '));
  }
  if (entry['pattern'] != null) parts.add(entry['pattern'].toString());
  if (entry['cron'] != null) parts.add(entry['cron'].toString());
  if (entry['outbound'] != null) parts.add('→ ${entry['outbound']}');
  if (entry['outbounds'] is List) parts.add('${(entry['outbounds'] as List).length} ${tr('Group members')}');
  if (entry['listen_port'] != null) parts.add('${entry['listen'] ?? '127.0.0.1'}:${entry['listen_port']}');
  if (entry['hostname'] != null) parts.add(entry['hostname'].toString());
  if (entry['url'] != null) parts.add(entry['url'].toString());
  if (entry['path'] != null) parts.add(entry['path'].toString());
  if (parts.isEmpty && entry['action'] != null) parts.add(tr(entry['action'].toString()));
  return parts.isEmpty ? tr('Custom settings') : parts.join(' · ');
}

// A local profile starts with a real node and the Android VPN/DNS plumbing.
// Permissions and starting the service are still handled by the native host.
Json createGuidedProfile(Json outbound) {
  final group = outbound['tag'] == 'proxy' ? 'proxy-group' : 'proxy';
  return {
  'log': {'level': 'info'},
  'dns': {'servers': [{'type': 'local', 'tag': 'device-dns'}], 'final': 'device-dns'},
  'inbounds': [{'type': 'tun', 'tag': 'vpn-in', 'address': ['172.19.0.1/30', 'fdfe:dcba:9876::1/126'], 'auto_route': true}],
  'outbounds': [{'type': 'selector', 'tag': group, 'outbounds': [outbound['tag']]}, copyConfig(outbound)],
  'route': {
    'rules': [{'action': 'sniff'}, {'protocol': ['dns'], 'action': 'hijack-dns'}],
    'final': group, 'default_domain_resolver': 'device-dns', 'auto_detect_interface': true,
  },
  };
}
