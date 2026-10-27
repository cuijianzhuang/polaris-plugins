#!/usr/bin/env bash
# Cross-compiles cf-ip-probe and packs one archive per platform plus
# sha256sums.txt:  scripts/build.sh <version> <out-dir>
# Archive names carry no version (cf-ip-probe-linux-amd64.tar.gz) so the
# installer can build the download URL from the release tag alone.
set -euo pipefail
version=${1:-dev}
out=$(mkdir -p "${2:-dist}" && cd "${2:-dist}" && pwd)
cd "$(dirname "$0")/.."

# os/arch[/variant]: armv7 = GOARM=7, armv5 = GOARM=5 (older ARM routers),
# mips* = soft float (most routers have no FPU).
targets=(
  linux/amd64 linux/386 linux/arm64 linux/arm/7 linux/arm/5
  linux/mips/softfloat linux/mipsle/softfloat linux/mips64 linux/mips64le
  linux/riscv64 linux/loong64
  freebsd/amd64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
)

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
for t in "${targets[@]}"; do
  IFS=/ read -r os arch variant <<<"$t"
  name=$arch
  env=(CGO_ENABLED=0 GOOS="$os" GOARCH="$arch")
  case "$arch/$variant" in
    arm/7) name=armv7; env+=(GOARM=7) ;;
    arm/5) name=armv5; env+=(GOARM=5) ;;
    mips/softfloat|mipsle/softfloat) env+=(GOMIPS=softfloat) ;;
    mips64/|mips64le/) env+=(GOMIPS64=softfloat) ;;
  esac
  dir="$work/cf-ip-probe-$os-$name"
  mkdir -p "$dir"
  bin=cf-ip-probe
  [ "$os" = windows ] && bin=cf-ip-probe.exe
  env "${env[@]}" go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$dir/$bin" .
  cp README.md "$dir/"
  if [ "$os" = windows ]; then
    (cd "$dir" && zip -q -9 "$out/cf-ip-probe-$os-$name.zip" "$bin" README.md)
  else
    tar -C "$dir" -czf "$out/cf-ip-probe-$os-$name.tar.gz" "$bin" README.md
  fi
  echo "built $os-$name"
done
cp scripts/install.sh "$out/install.sh"
(cd "$out" && sha256sum cf-ip-probe-* install.sh > sha256sums.txt)
