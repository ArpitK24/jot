#!/usr/bin/env bash
# Builds release archives, MCP bundles (.mcpb) and a filled-in server.json.
# Usage: packaging/release.sh <version without v>   (outputs to dist/)
set -euo pipefail
v="$1"
repo="https://github.com/ArpitK24/jot/releases/download/v$v"
rm -rf build dist && mkdir -p build dist
pkgs="[]"
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  os="${target%/*}"; arch="${target#*/}"
  name="jot_${v}_${os}_${arch}"
  bin="jot"; [ "$os" = windows ] && bin="jot.exe"

  mkdir -p "build/$name"
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$v" -o "build/$name/$bin" ./cmd/jot
  cp README.md LICENSE "build/$name/"
  if [ "$os" = windows ]; then
    (cd build && zip -qr "../dist/$name.zip" "$name")
  else
    tar -C build -czf "dist/$name.tar.gz" "$name"
  fi

  # MCP bundle: manifest.json + server/<binary>
  mcpb="jot-${v}-${os}-${arch}.mcpb"
  b="build/mcpb-$os-$arch"
  mkdir -p "$b/server"
  cp "build/$name/$bin" "$b/server/$bin"
  platform="$os"; [ "$os" = windows ] && platform="win32"
  jq --arg v "$v" --arg p "$platform" '.version = $v | .compatibility.platforms = [$p]' \
    packaging/mcpb/manifest.json > "$b/manifest.json"
  (cd "$b" && zip -qr -X "../../dist/$mcpb" manifest.json server)
  sha=$(sha256sum "dist/$mcpb" | cut -d' ' -f1)
  pkgs=$(jq --arg id "$repo/$mcpb" --arg sha "$sha" \
    '. + [{registryType: "mcpb", identifier: $id, fileSha256: $sha, transport: {type: "stdio"}}]' <<<"$pkgs")
done
jq --arg v "$v" --argjson p "$pkgs" '.version = $v | .packages = $p' server.json > dist/server.json
(cd dist && sha256sum *.tar.gz *.zip *.mcpb > checksums.txt)
