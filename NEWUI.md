# FlClash Android UI migration

Reference: FlClash **v0.9.2**, commit
`7fd417fb9a9a277c6a9339a8ac87f907df7c25f7` (2026-10-24).

The Flutter module owns application navigation. The existing launcher class
name and package identity remain stable, while the Activity hosts Flutter and
the original sing-box service permission flow. Shared FlClash widgets, shapes,
glyphs, typography, sheets and navigation are vendored with provenance in
`third_party/flclash`. B-box's Room databases and libbox bindings remain the
source of truth. No action may launch an old Compose page as a fallback.

## Capability boundary

- Profiles contain sing-box JSON and are checked by `Libbox.checkConfig` before
  writing. Remote URLs must serve this format. Clash YAML and JavaScript
  overrides are not accepted or silently converted.
- Proxy groups, selectable nodes, delays and available routing modes come from
  libbox. A group that is not selectable must not expose selection. UI controls
  must not invent `rule/global/direct` modes absent from the configuration.
- VPN startup continues through Android permission and foreground service
  handling. An HTTP proxy switch is conditional on libbox availability.
- Connections, logs, traffic and session state reflect the active local or
  remote sing-box session. They are never populated with demonstration data.
- Status-card IP queries use FlClash's eight sources: Cloudflare trace, IP.SB,
  IPinfo, ipwho.is, GeoJS, country.is, IPQuery and ident.me. The race has a
  six-second deadline, an 800 ms stagger, three concurrent requests and a
  4096-byte response limit; the first public-IP answer cancels the losers.
  Organization, ASN, type and quality follow the reference's separate waves:
  ident.me/ip-api.com/IPQuery, IPLocate, ipapi.is, then proxycheck.io. Later
  waves start after one second or when outstanding requests fail; each request
  has an eight-second deadline. Source parsing, explicit-type preference,
  residential inference and Tor/abuser quality rules match the reference.
  Successful quality records persist by IP for three days; failures have a
  five-minute memory cooldown that manual retry bypasses. IP remains visible
  while quality loads or fails. Route changes and backgrounding cancel the
  refresh, and late results are ignored. All requests use the active sing-box
  router and libbox TLS store; remote sessions do not expose this probe.
- Existing sing-box-only capabilities use the same Flutter presentation;
  they do not justify bringing Clash-only controls into the app.
- The optional LAN card retains the reference's sharing switch, port subtitle,
  cookie/circle Wi-Fi badge and copy action. Sharing edits the selected
  profile's native mixed/http/socks listen fields, retaining independent
  ports, authentication, TLS, interface restrictions and routing. It uses
  revision checks, whole-config validation, atomic writes and service reload;
  failed applies restore the previous configuration. A profile without a
  proxy inbound offers an explicit mixed-listener form with a selectable
  port. No global allow-lan or mixed-port fields are invented. Omitted listen
  follows the matching core's TCP default of 127.0.0.1. Copy uses the running
  service's configuration snapshot and expands wildcard binds to physical
  device addresses, respecting IPv4/IPv6 and bind_interface; stopped and
  remote sessions copy the device IP. Details expose all active ports.
- Advanced configuration and resources edit native sing-box DNS, routing,
  rule sets, listeners, outbounds, endpoints and logging. Each save validates
  the whole configuration, checks its content revision and writes atomically.
  Raw edits preserve profile metadata and the subscription URL. The background
  updater shares the profile content mutex with foreground edits.
- Android defaults use the reference's five cards: status, network speed,
  outbound mode, profile and proxy groups. Unsupported routing modes remain
  hidden. Mode labels, glyphs and selection match case-insensitively, while
  actions and button order preserve the core's mode values. Memory and LAN
  cards are optional; connections remain accessible from Tools without a
  dashboard card. Older layouts and
  backups ignore the removed card, and the next layout save removes its keys.
- First launch opens the dashboard directly, without a disclaimer acceptance
  gate. Fresh-start Flutter and Android smoke scenarios cover this behavior.
- Tools use the Android reference section order and open Flutter settings,
  configuration, resources and About pages. About keeps B-box branding and
  checks B-box release metadata against the installed application ID and
  version code, including prereleases, before offering a matching ABI APK.
- Proxy list mode keeps the reference's pinned rounded group headers,
  fixed-extent lazy rows, folding motion and filled/plain/hidden group icons.
  Fold state is stored per selected profile. Locating a node uses its row
  offset and works before the node has entered the viewport.
- Dashboard editing happens on the live cards, with the reference's sway,
  long-press reordering, remove badges and addable rows. Global card keys
  preserve state and prevent duplicate probes during editing. Changes save
  immediately; a failed save retains the draft and Done retries it.
- Tools keeps terminal settings inside Terminal and dashboard editing on the
  dashboard toolbar. The B-box section contains Remote control.
- Backup follows the reference's remote/local sections, WebDAV account form,
  connectivity indicator, filename, scope dialog and Compatible/Override
  strategy. The native `bbox-1` format stores stable profile identities,
  terminal settings/imported fonts and optional WebDAV settings. Profiles-only
  restores do not touch unrelated settings. Restore stages files, uses a Room
  transaction and rolls back files/database/settings after a failed apply.
  Compatible preserves other profiles; Override removes absent profiles after
  a visible confirmation. Reads/transfers are bounded to 64 MiB; canceled
  pickers and unacknowledged uploads do not report success.

## Validation

Local compilation is prohibited by `../AGENTS.md` unless explicitly requested.
Flutter analysis/tests and Android compilation belong in GitHub Actions.
Routed probe tests cover concurrency, fallback, public-IP validation, response
limits, cancellation and service replacement. Flutter tests adapt the pinned
reference's quality fixtures and cover cache lifetime, retry, progressive
status updates and stale results after route/lifecycle changes.
Pixel parity must be checked against the pinned reference at equal viewport,
font scale, locale and theme; source reuse alone is not visual acceptance.
Status-card tests compare pixels against a frozen reference renderer across
twelve mobile scenarios, including both locales/themes, 0.8/1.0/1.4 text scales,
IPv6, pending/failed probes, organization/ASN fallback and quality levels.
Stopped start buttons are compared separately. The oracle retains the reference
header, chips, badge, morph, card surface and typography without importing the
adapted B-box dashboard components. Both images are saved as CI artifacts.
Stopped and remote sessions keep unavailable IP data as placeholders because
the routed sing-box probe requires an active local service.
