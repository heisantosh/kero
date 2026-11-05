#!/bin/sh
# Builds and publishes a Kero release of this fork: the macOS app, its
# signed update archive, the server builds for the hosts the app updates
# (Linux and illumos), and their install scripts, in the fork's GitHub
# release v<mygo.json's version>.
#
# It needs go (and mise, for Zig and Go if not on PATH), gh signed in, the
# MyGo update key, and an illumos machine to build the target's sysroot
# from. The app is ad-hoc signed, so a first launch may need the usual
# "Open" of an unsigned app; its updates are signed with the key.
#
#   ILLUMOS_HOST=heisantosh@192.168.64.2 sh scripts/release-fork.sh
#
# The key is ~/.config/kero-fork/update-keys/mygo-update.key by default;
# KERO_UPDATER_KEY names another. Keep it out of the repository.
set -eu

root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$root"

repo=heisantosh/kero
version="$(jq -r .version mygo.json)"
tag="v$version"
key="${KERO_UPDATER_KEY:-$HOME/.config/kero-fork/update-keys/mygo-update.key}"

say() { printf 'release-fork: %s\n' "$*" >&2; }
fail() { say "$*"; exit 1; }

[ -f "$key" ] || fail "no update key at $key (mygo keygen -o ~/.config/kero-fork/update-keys)"
command -v gh >/dev/null 2>&1 || fail "gh is needed: mise use -g gh, then gh auth login"
gh auth status -h github.com >/dev/null 2>&1 || fail "gh is not signed in: gh auth login"
export GH_TOKEN="${GH_TOKEN:-$(gh auth token)}"
export MYGO_UPDATER_PRIVATE_KEY="$(cat "$key")"

if [ ! -f build/illumos/libghostty-vt.so ]; then
	say "building libghostty-vt for illumos"
	ILLUMOS_HOST="${ILLUMOS_HOST:-helios.local}" sh scripts/build-libghostty-vt-illumos.sh
fi

say "building Kero $version for macOS"
go tool mygo build -platform darwin/amd64 -upload

say "building the server builds"
sh scripts/build-server.sh linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 illumos/amd64
go run ./internal/update/sign build/server/*.tar.gz

say "uploading the server builds to $tag"
gh release view "$tag" --repo "$repo" >/dev/null 2>&1 ||
	gh release create "$tag" --repo "$repo" --title "Kero $version" --notes-file CHANGELOG.md --draft
gh release upload "$tag" --repo "$repo" --clobber \
	build/server/kero-server-*.tar.gz \
	build/server/kero-server-*.tar.gz.sig \
	build/server/kero-server-*.tar.gz.sha256 \
	build/server/install-server.sh \
	build/server/install-server.ps1
gh release edit "$tag" --repo "$repo" --draft=false

say "published https://github.com/$repo/releases/tag/$tag"
