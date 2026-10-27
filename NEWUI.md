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
- Existing sing-box-only capabilities use the same Flutter presentation;
  they do not justify bringing Clash-only controls into the app.
- Advanced configuration and resources edit native sing-box DNS, routing,
  rule sets, listeners, outbounds, endpoints and logging. Each save validates
  the whole configuration, checks its content revision and writes atomically.
  Raw edits preserve profile metadata and the subscription URL. The background
  updater shares the profile content mutex with foreground edits.
- Android defaults use the reference's five cards: status, network speed,
  outbound mode, profile and proxy groups. Unsupported routing modes remain
  hidden. Memory, LAN and connection cards are optional.
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
Pixel parity must be checked against the pinned reference at equal viewport,
font scale, locale and theme; source reuse alone is not visual acceptance.
