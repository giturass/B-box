#!/system/bin/sh
# Namespace handling references FlClash dev 63aa150e23288558074d877f745a6d3b5f6418f6.
# B-box prepares each store in its own namespace before binding the complete copy.
# No platform store is emptied, remounted writable, or modified on disk.

MODDIR=${0%/*}

log_failure() {
  printf '%s\n' "$*" >> "$MODDIR/mount.log"
}

has_certificate() {
  lookup_name=${2##*/}
  for candidate in "$1/${lookup_name%.0}."*; do
    [ -f "$candidate" ] || continue
    cmp -s "$candidate" "$2" && return 0
  done
  return 1
}

install_store() (
  target=$1
  [ -d "$target" ] || exit 0
  set -- "$MODDIR"/cacert/*.0
  [ "$#" -eq 1 ] && [ -f "$1" ] || exit 1
  cert=$1
  has_certificate "$target" "$cert" && exit 0
  # Different Android namespaces can expose different CA sets. Copy the actual
  # target in that namespace, retaining its files and any subject-hash collision.
  stage=$(mktemp -d "$MODDIR/.store.XXXXXX") || exit 1
  mounted=0
  cleanup() {
    if [ "$mounted" = 1 ]; then umount "$stage" || return; fi
    rmdir "$stage"
  }
  trap cleanup EXIT
  mount -t tmpfs -o mode=0755 tmpfs "$stage" || exit 1
  mounted=1
  count=0
  for original in "$target"/*; do
    [ -f "$original" ] || continue
    cp "$original" "$stage/" || exit 1
    count=$((count + 1))
  done
  [ "$count" -gt 0 ] || exit 1
  hash=${cert##*/}
  hash=${hash%.0}
  index=0
  while [ -e "$stage/$hash.$index" ]; do index=$((index + 1)); done
  cp "$cert" "$stage/$hash.$index" || exit 1
  chown -R 0:0 "$stage" || exit 1
  chmod 644 "$stage"/* || exit 1
  context=$(ls -Zd "$target" | awk '{print $1}')
  case $context in *:*) ;; *) context=u:object_r:system_file:s0 ;; esac
  case $(getenforce) in
    Disabled) ;;
    Enforcing|Permissive) chcon -R "$context" "$stage" || exit 1 ;;
    *) exit 1 ;;
  esac
  mount --bind "$stage" "$target" || exit 1
)

if [ "$1" = --local ]; then
  install_store /system/etc/security/cacerts || log_failure "Could not mount system CA store in namespace $$"
  install_store /apex/com.android.conscrypt/cacerts || log_failure "Could not mount Conscrypt CA store in namespace $$"
  exit 0
fi

# Manager scripts use its BusyBox applets, including nsenter on older Android.
# Do not assume a bind in init is visible in the private Conscrypt APEX mounts.
sh "$0" --local
zygotes=" $(pidof zygote zygote64 || true) "
pids="1 $zygotes"
if [ "$1" = --apps ]; then
  for status in /proc/[0-9]*/status; do
    while read -r key value; do
      if [ "$key" = PPid: ]; then
        case $zygotes in
          *" $value "*) pid=${status%/status}; pids="$pids ${pid#/proc/}" ;;
        esac
        break
      fi
    done < "$status" 2>/dev/null
  done
fi
for pid in $pids; do
  [ -e "/proc/$pid/ns/mnt" ] || continue
  nsenter --mount="/proc/$pid/ns/mnt" -- sh "$0" --local || log_failure "Could not enter mount namespace $pid"
done
