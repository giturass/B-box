# FlClash Android UI migration

Reference: FlClash **v0.9.2**, commit
`7fd417fb9a9a277c6a9339a8ac87f907df7c25f7` (2026-10-24).

The Flutter module owns application navigation. The existing launcher class
name and package identity remain stable, while the Activity hosts Flutter and
the original sing-box service permission flow. Shared FlClash widgets, shapes,
glyphs, typography, sheets and navigation are vendored with provenance in
`third_party/flclash`. B-box's Room databases and libbox bindings remain the
source of truth. Native state holders and the OpenConnect WebView controller
live in `backend/`; the old Compose pages, navigation, editors and dependencies
have been removed. `compose/` contains only the two stable Android component
names, both extending the Flutter host.

The retired GitHub/F-Droid/Play updater, APK download/install helpers,
installer Binder methods, permissions, preferences and UI resources are gone.
About uses `BoxUpdates`; subscription refresh uses `UpdateProfileWork`.
`UpdateWorker` is only a no-op compatibility class for old WorkManager records,
and startup cancels their `AutoUpdate` work.

## Capability boundary

- Profiles contain sing-box JSON and are checked by `Libbox.checkConfig` before
  writing. Remote URLs must serve this format. Clash YAML and JavaScript
  overrides are not accepted or silently converted.
- Proxy groups, selectable nodes, delays and available routing modes come from
  libbox. A group that is not selectable must not expose selection. UI controls
  must not invent `rule/global/direct` modes absent from the configuration.
- VPN startup continues through Android permission and foreground service
  handling. An HTTP proxy switch is conditional on libbox availability and
  uses the LAN proxy row's emphasized label style with a leading network glyph.
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
  while quality loads or fails. Returning to the foreground keeps a successful
  IP for the unchanged route; failed IP checks retry with FlClash's five-second
  throttle. Manual refresh bypasses it and retries failed quality explicitly.
  Inactive windows remain visible, and requests already sent may finish in the
  background; new refreshes wait for the foreground. Quality expiry refreshes only
  quality, without another IP lookup. Service, mode, group-selection and network
  changes cancel stale requests, ignore late results and coalesce for 300 ms
  before a new probe, bypassing the previous route's cooldown. Background UI
  disconnects retain the local group snapshot until it can be compared on return;
  ordering, delay updates and stopped-profile selection do not invalidate it.
  Stopped local sessions query the device
  network even without a selected profile; running sessions use the active
  sing-box router. Starting/stopping cancels the previous probe and waits for a
  stable service state before refreshing the new exit. Both paths use the
  libbox TLS store; failed routed requests never fall back to the device network,
  and remote sessions do not expose this probe.
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
- Tools open Flutter settings and About pages.
  Settings appear in this order: Appearance, General, Access control,
  Backup and restore. Advanced configuration opens from profile
  details. The app follows the system
  language, including live changes; older settings and backups discard their
  retired language choice while preserving the other preferences.
  About keeps B-box branding and
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
  dashboard toolbar. Remote control appears directly after Terminal in the
  first section.
- Backup follows the reference's remote/local sections, WebDAV account form,
  connectivity indicator, filename, scope dialog and Compatible/Override
  strategy. The native `bbox-1` format stores stable profile identities,
  terminal settings/imported fonts and optional WebDAV settings. Profiles-only
  restores do not touch unrelated settings. Restore stages files, uses a Room
  transaction and rolls back files/database/settings after a failed apply.
  Compatible preserves other profiles; Override removes absent profiles after
  a visible confirmation. Reads/transfers are bounded to 64 MiB; canceled
  pickers and unacknowledged uploads do not report success.
- Configuration restart waits for Android's Started callback; startup errors
  restore the previous files/settings and restart the previous service.
  Android 12 precise-location requests include both fine and coarse location.
- Managed application mode scans before activation, refreshes when its page
  opens, and persists the package-query strategy. Native logs keep every
  received level for Flutter's filters. Remote profile cards do not use local
  configuration names.
- Binary `.bpf` import preserves subscription URL, update interval, auto-update
  and timestamp. Animated QRS imports accumulate bounded frames per camera
  session, check CRC and support the original compressed file envelope.

## Validation

Local compilation is prohibited by `../AGENTS.md` unless explicitly requested.
Flutter analysis/tests and Android compilation belong in GitHub Actions.
Native JVM regressions cover startup acknowledgement/failure/timeout, log
levels/bounds and QRS frame handling. Android smoke runs on APIs 24, 31 and 35,
compiles both flavors, and exercises actual libbox binary imports, query-mode
persistence and occupied-port rollback for configuration/full-backup restores.
Probe tests cover concurrency, source fallback, public-IP validation, response
limits, direct/routed cancellation and service replacement. Flutter tests adapt
the pinned reference's quality fixtures and cover cache lifetime, retry, progressive
status updates, stopped queries/retries without a profile, start/stop transitions,
cached results on resume, in-flight completion across visibility changes,
throttled/manual retries, independent quality expiry and stale route results.
Pixel parity must be checked against the pinned reference at equal viewport,
font scale, locale and theme; source reuse alone is not visual acceptance.
Status-card tests compare pixels against a frozen reference renderer across
sixteen mobile scenarios, including both locales/themes, 0.8/1.0/1.4 text scales,
IPv6, pending/failed probes, organization/ASN fallback and quality levels.
Stopped start buttons are compared separately. The oracle retains the reference
header, chips, badge, morph, card surface and typography without importing the
adapted B-box dashboard components. Both images are saved as CI artifacts.
Captures use the dashboard's natural card height and identical Roboto/Noto Sans
SC fonts; a height assertion prevents the test frame from stretching the card.
Stopped cards also cover successful, pending and failed device-network queries,
including a fresh installation without a profile. Remote sessions retain IP
placeholders because the remote core does not expose an egress probe.
