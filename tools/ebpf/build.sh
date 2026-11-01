#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
capture_platform="${1:?Expected android/arm64, android/amd64 or linux/amd64}"
case "$capture_platform" in
  android/arm64) capture_arch=arm64; capture_bpf_arch=arm64; capture_abi=arm64-v8a; capture_cc=aarch64-linux-android24-clang ;;
  android/amd64) capture_arch=amd64; capture_bpf_arch=x86; capture_abi=x86_64; capture_cc=x86_64-linux-android24-clang ;;
  linux/amd64) capture_arch=amd64; capture_bpf_arch=x86 ;;
  *) echo "Unsupported capture platform: $capture_platform" >&2; exit 1 ;;
esac
mkdir -p build
clang -target bpfel -mcpu=v2 -D"__TARGET_ARCH_$capture_bpf_arch" -O2 -g -Wall -Werror \
  -c bpf/capture.c -o bpf/capture.o
if [[ "$capture_platform" == linux/* ]]; then
  go build -mod=readonly -trimpath -o capture .
else
  export GOOS=android GOARCH="$capture_arch" CGO_ENABLED=1
  export CC="${ANDROID_NDK_HOME:?Set ANDROID_NDK_HOME}/toolchains/llvm/prebuilt/linux-x86_64/bin/$capture_cc"
  capture_output="../../app/src/main/jniLibs/$capture_abi"
  mkdir -p "$capture_output"
  go build -mod=readonly -trimpath -buildmode=pie \
    -ldflags="-s -w -linkmode=external -extldflags=-Wl,-z,max-page-size=16384" \
    -o "$capture_output/libbbox_capture.so" .
  if [[ "$capture_platform" == android/amd64 ]]; then
    go test -mod=readonly -trimpath -c -buildmode=pie \
      -ldflags="-linkmode=external -extldflags=-Wl,-z,max-page-size=16384" \
      -o build/capture-platform.test ./platformtest
  fi
fi
