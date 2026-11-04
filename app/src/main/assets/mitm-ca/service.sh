#!/system/bin/sh
MODDIR=${0%/*}
# KernelSU/APatch and Magisk run this stage asynchronously. Do not wait for
# sys.boot_completed before the first pass: early Zygote children need the CA.
if ! command -v flock >/dev/null 2>&1; then
  printf 'failed: root manager BusyBox does not provide flock\n' >> "$MODDIR/mount.log"
  exit 1
fi
exec 5>"$MODDIR/.service.lock"
flock -n 5 || exit 0
previous=
attempt=0
while [ -f "$MODDIR/module.prop" ] && [ ! -e "$MODDIR/disable" ] && [ ! -e "$MODDIR/remove" ]; do
  output=$(sh "$MODDIR/inject.sh" --apps 2>&1)
  result=$?
  boot=$(getprop sys.boot_completed)
  status="boot_completed=${boot:-0} exit=$result
$output"
  printf '%s\n' "$status" > "$MODDIR/.mount.status.tmp"
  mv -f "$MODDIR/.mount.status.tmp" "$MODDIR/mount.status"
  if [ "$status" != "$previous" ]; then
    # Keep diagnostics bounded even when a namespace remains inaccessible.
    { cat "$MODDIR/mount.log" 2>/dev/null; date; printf '%s\n' "$status"; } |
      tail -c 65536 > "$MODDIR/.mount.log.tmp"
    mv -f "$MODDIR/.mount.log.tmp" "$MODDIR/mount.log"
    previous=$status
  fi
  attempt=$((attempt + 1))
  if [ "$attempt" -lt 60 ] && { [ "$boot" != 1 ] || [ "$result" != 0 ] || [ "$attempt" -lt 3 ]; }; then
    sleep 2
  else
    # Keep reconciling late app namespaces, Zygote restarts and APEX remounts.
    # A missing store is retried even after the bounded fast boot phase.
    sleep 30
  fi
done
