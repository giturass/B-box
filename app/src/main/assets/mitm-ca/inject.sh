#!/system/bin/sh
# Namespace handling references FlClash dev 63aa150e23288558074d877f745a6d3b5f6418f6.
# B-box prepares each store in its own namespace before binding the complete copy.
# No platform store is emptied, remounted writable, or modified on disk.

has_certificate() {
  for candidate in "$1/$3."*; do
    [ -f "$candidate" ] || continue
    cmp -s "$candidate" "$2" && return 0
  done
  return 1
}

install_store() (
  target=$1
  cert=$2
  hash=$3
  [ -d "$target" ] || { printf 'pending: %s is not mounted\n' "$target"; exit 2; }
  has_certificate "$target" "$cert" "$hash" && exit 0
  # Different Android namespaces can expose different CA sets. Copy the actual
  # target in that namespace, retaining its files and any subject-hash collision.
  # /data/adb (including this module) can be hidden in an app namespace.
  # The script/certificate arrive over open descriptors; only staging needs a
  # path in that namespace. /dev is writable by the root manager on Android.
  stage=$(mktemp -d /dev/.bbox-ca.XXXXXX) || exit 1
  mounted=0
  # Invoked by the EXIT trap, including failed preparation and signals.
  # shellcheck disable=SC2317,SC2329
  cleanup() {
    if [ "$mounted" = 1 ]; then umount "$stage" || return; fi
    rmdir "$stage"
  }
  trap cleanup EXIT
  trap 'exit 1' HUP INT TERM
  mount -t tmpfs -o mode=0755 tmpfs "$stage" || exit 1
  mounted=1
  count=0
  for original in "$target"/*; do
    [ -f "$original" ] || continue
    cp "$original" "$stage/" || exit 1
    count=$((count + 1))
  done
  [ "$count" -gt 0 ] || { printf 'pending: %s is empty\n' "$target"; exit 2; }
  index=0
  while [ -e "$stage/$hash.$index" ]; do index=$((index + 1)); done
  cp "$cert" "$stage/$hash.$index" || exit 1
  chown -R 0:0 "$stage" || exit 1
  chmod 644 "$stage"/* || exit 1
  # Read the SELinux label, not a filename list; both targets are fixed paths.
  # shellcheck disable=SC2012
  context=$(ls -Zd "$target" | awk '{print $1}')
  case $context in *:*) ;; *) context=u:object_r:system_file:s0 ;; esac
  case $(getenforce) in
    Disabled) ;;
    Enforcing|Permissive) chcon -R "$context" "$stage" || exit 1 ;;
    *) exit 1 ;;
  esac
  mount --bind "$stage" "$target" || exit 1
  if ! has_certificate "$target" "$cert" "$hash"; then
    umount "$target"
    exit 1
  fi
  printf 'mounted: %s\n' "$target"
)

# This entry also runs through /proc/self/fd/8 after nsenter. It must not read
# module-relative files or write diagnostics into the target's /data/adb.
if [ "$1" = --local ]; then
  hash=$2
  sdk=$3
  required=$4
  cert=/proc/self/fd/9
  if [ "$required" != 1 ] && [ ! -d /system/etc/security/cacerts ] && [ ! -d /apex/com.android.conscrypt/cacerts ]; then
    exit 12 # A non-Android filesystem view, not a verified CA namespace.
  fi
  result=0
  for target in /system/etc/security/cacerts /apex/com.android.conscrypt/cacerts; do
    # Conscrypt's CA directory is optional before Android 14, including old
    # releases which have a Conscrypt APEX without its own CA directory.
    if [ "$target" = /apex/com.android.conscrypt/cacerts ] && [ "$sdk" -lt 34 ] && [ ! -d "$target" ]; then continue; fi
    install_store "$target" "$cert" "$hash"
    code=$?
    case $code in
      0) ;;
      2) [ "$result" = 0 ] && result=11 ;;
      *) printf 'failed: %s\n' "$target"; result=10 ;;
    esac
  done
  exit "$result"
fi

MODDIR=${0%/*}
mode=$1
case $mode in --early|--apps) ;; *) printf 'Usage: inject.sh --early|--apps\n' >&2; exit 1 ;; esac
set -- "$MODDIR"/cacert/*.0
if [ "$#" -ne 1 ] || [ ! -f "$1" ]; then
  printf 'failed: expected one public CA\n'
  exit 1
fi
cert=$1
hash=${cert##*/}
hash=${hash%.0}
case $hash in ''|*[!0-9a-f]*) exit 1 ;; esac
[ "${#hash}" -eq 8 ] || exit 1
sdk=$(getprop ro.build.version.sdk)
case $sdk in ''|*[!0-9]*) printf 'pending: Android SDK is unavailable\n'; exit 2 ;; esac

enter_namespace() (
  # Pin the namespace before entering it, and recheck the scanned identity.
  # Open the interpreter, script and public certificate BEFORE switching mounts.
  # A vanished/reused PID must not silently target a different namespace.
  exec 7<"/proc/$1/ns/mnt" || exit 13
  exec 4<"/proc/$1/root" || exit 13
  [ "$(stat -L -c %i /proc/self/fd/7)" = "$2" ] || exit 13
  exec 6<"/proc/$$/exe" 8<"$MODDIR/inject.sh" 9<"$cert" || exit 1
  # BusyBox dispatches by argv[0], so executing a descriptor named "6" directly
  # would select a nonexistent applet. Android's mksh bootstraps the pinned
  # interpreter with argv[0]=sh; BusyBox ash and mksh both support exec -a.
  # setns also resets root/cwd to the namespace root. Preserve the process's
  # actual filesystem root, including chroot-based Android/container views.
  nsenter --mount=/proc/self/fd/7 --root=/proc/self/fd/4 --wd=/proc/self/fd/4 -- /system/bin/sh -c \
    'exec -a sh /proc/self/fd/6 /proc/self/fd/8 --local "$@"' sh "$hash" "$sdk" "$3"
)

seen=' '
ready=0
pending=0
failed=0
skipped=0
visit_namespace() {
  pid=$1
  namespace=$2
  required=$3
  case $seen in *" $namespace "*) return ;; esac
  # Inspect the actual process view on EVERY pass. An inode-only success cache
  # would miss APEX remounts or a CA mount removed in an existing namespace.
  root=/proc/$pid/root
  if [ "$required" = 0 ] && [ -d "$root" ] &&
    [ ! -d "$root/system/etc/security/cacerts" ] && [ ! -d "$root/apex/com.android.conscrypt/cacerts" ]; then
    skipped=$((skipped + 1))
    seen="$seen$namespace "
    return
  fi
  if has_certificate "$root/system/etc/security/cacerts" "$cert" "$hash" &&
    { has_certificate "$root/apex/com.android.conscrypt/cacerts" "$cert" "$hash" ||
      { [ "$sdk" -lt 34 ] && [ ! -d "$root/apex/com.android.conscrypt/cacerts" ]; }; }; then
    if [ "$(stat -L -c %i "/proc/$pid/ns/mnt" 2>/dev/null)" != "$namespace" ]; then
      pending=$((pending + 1))
      return
    fi
    ready=$((ready + 1))
    seen="$seen$namespace "
    return
  fi
  output=$(enter_namespace "$pid" "$namespace" "$required" 2>&1)
  code=$?
  # Do not deduplicate a disappearing process: another PID can still represent
  # this namespace later in the same scan. The next service pass also retries.
  if [ "$code" = 13 ] || [ ! -e "/proc/$pid/ns/mnt" ]; then
    pending=$((pending + 1))
    printf 'pending: namespace %s pid %s disappeared or changed\n' "$namespace" "$pid"
    return
  fi
  seen="$seen$namespace "
  [ -z "$output" ] || printf 'namespace %s pid %s: %s\n' "$namespace" "$pid" "$output"
  case $code in
    0) ready=$((ready + 1)) ;;
    11) pending=$((pending + 1)) ;;
    12) skipped=$((skipped + 1)) ;;
    *) failed=$((failed + 1)); printf 'failed: namespace %s pid %s exit %s\n' "$namespace" "$pid" "$code" ;;
  esac
}

visit_pid() {
  identity=$(stat -L -c %i "/proc/$1/ns/mnt" 2>/dev/null) || {
    pending=$((pending + 1))
    printf 'pending: cannot inspect pid %s\n' "$1"
    return
  }
  visit_namespace "$1" "$identity" 1
}

if [ "$mode" = --apps ]; then
  zygotes=$(pidof zygote zygote64 2>/dev/null)
  if [ -z "$zygotes" ]; then
    pending=$((pending + 1))
    printf 'pending: Zygote is not ready\n'
  fi
  # Patch the spawning parents BEFORE taking the process snapshot. Processes
  # forked afterwards inherit this mount; pre-existing descendants are below.
  for pid in $zygotes; do visit_pid "$pid"; done
fi
visit_pid "$$"
visit_pid 1
if [ "$mode" = --apps ]; then
  # Inspect mount namespaces, not just the main Zygotes' immediate children.
  # This includes app/WebView Zygotes, their existing descendants and native
  # processes. Bulk stat avoids one subprocess for every /proc directory.
  namespaces=$(stat -L -c '%i %n' /proc/[0-9]*/ns/mnt 2>/dev/null)
  while read -r identity path; do
    [ -n "$identity" ] || continue
    pid=${path#/proc/}
    pid=${pid%/ns/mnt}
    visit_namespace "$pid" "$identity" 0
  done <<EOF
$namespaces
EOF
fi
printf 'summary: ready=%s pending=%s failed=%s skipped=%s\n' "$ready" "$pending" "$failed" "$skipped"
[ "$failed" -eq 0 ] || exit 1
[ "$pending" -eq 0 ] || exit 2
