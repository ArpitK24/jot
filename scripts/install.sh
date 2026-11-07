#!/bin/sh
# Install the jot CLI on macOS or Linux from a GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/ArpitK24/jot/main/scripts/install.sh | sh
#
# Environment:
#   JOT_VERSION      version to install, e.g. 0.3.0 (default: newest v* release)
#   JOT_INSTALL_DIR  where to put the binary (default: ~/.local/bin, or
#                    /usr/local/bin when run as root)
#
# It downloads jot_<version>_<os>_<arch>.tar.gz and checksums.txt from the
# release, checks the archive's SHA-256, and copies the jot binary into
# place. Nothing else on the system is changed.
set -eu

REPO="ArpitK24/jot"
API="${JOT_RELEASES_API:-https://api.github.com/repos/$REPO/releases}"   # overridable for tests and mirrors
DL="${JOT_DOWNLOAD_URL:-https://github.com/$REPO/releases/download}"

say() { printf '%s\n' "$*"; }
die() { printf 'jot install: %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

fetch() { # fetch URL FILE
  if have curl; then
    curl -fsSL --retry 2 -o "$2" "$1"
  elif have wget; then
    wget -q -O "$2" "$1"
  else
    die "needs curl or wget"
  fi
}

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) die "unsupported OS $(uname -s); on Windows use scripts/install.ps1" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) die "unsupported CPU $(uname -m); build from source with: go install github.com/$REPO/cmd/jot@latest" ;;
esac
have tar || die "needs tar"
if have sha256sum; then
  sha() { sha256sum "$1" | cut -d' ' -f1; }
elif have shasum; then
  sha() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
  die "needs sha256sum or shasum to check the download"
fi

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t jot)
trap 'rm -rf "$tmp"' EXIT INT TERM

version="${JOT_VERSION:-}"
version="${version#v}"
if [ -z "$version" ]; then
  # The newest CLI release. Desktop app releases are tagged desktop/v*,
  # so /releases/latest can't be trusted to point at the CLI.
  fetch "$API?per_page=30" "$tmp/releases.json" || die "could not reach the GitHub API"
  version=$(tr ',' '\n' <"$tmp/releases.json" |
    sed -n 's/^[[:space:]]*"tag_name":[[:space:]]*"v\([0-9][0-9.]*\)".*/\1/p' | head -n 1)
  [ -n "$version" ] || die "could not find a v* release of $REPO"
fi

name="jot_${version}_${os}_${arch}"
say "Installing jot $version ($os/$arch)"
fetch "$DL/v$version/$name.tar.gz" "$tmp/$name.tar.gz" || die "could not download $name.tar.gz from release v$version"
fetch "$DL/v$version/checksums.txt" "$tmp/checksums.txt" || die "could not download checksums.txt from release v$version"

want=$(awk -v f="$name.tar.gz" '$2 == f || $2 == "*" f { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || die "$name.tar.gz is not listed in checksums.txt"
got=$(sha "$tmp/$name.tar.gz")
[ "$want" = "$got" ] || die "checksum mismatch for $name.tar.gz (want $want, got $got); not installing"
say "Checksum OK"

tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
[ -f "$tmp/$name/jot" ] || die "archive has no $name/jot"

dir="${JOT_INSTALL_DIR:-}"
if [ -z "$dir" ]; then
  if [ "$(id -u)" = 0 ]; then dir=/usr/local/bin; else dir="$HOME/.local/bin"; fi
fi
mkdir -p "$dir" 2>/dev/null || die "cannot create $dir; set JOT_INSTALL_DIR to a folder you can write"
[ -w "$dir" ] || die "cannot write to $dir; set JOT_INSTALL_DIR, or rerun with sudo"
cp "$tmp/$name/jot" "$dir/jot.tmp"
chmod 755 "$dir/jot.tmp"
mv -f "$dir/jot.tmp" "$dir/jot"
say "Installed $("$dir/jot" version) to $dir/jot"

case ":$PATH:" in
  *":$dir:"*) ;;
  *)
    say ""
    say "$dir is not on your PATH. Add it, e.g. in ~/.bashrc or ~/.zshrc:"
    say "  export PATH=\"$dir:\$PATH\""
    ;;
esac

say ""
say "Next: connect your AI tools (it shows every change and asks first):"
say "  jot setup"
