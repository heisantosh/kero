#!/bin/sh
# Builds and publishes a Kero release of this fork: the macOS app, its
# signed update archive, the server builds for the hosts the app updates
# (Linux and illumos), and their install scripts, in the fork's GitHub
# release v<mygo.json's version>.
#
# It refuses to publish a version upstream has not released, or one that
# is not what main and work hold. KERO_ALLOW_UNRELEASED=1 overrides that
# for a deliberate hotfix. Run it under mise so go, gh and jq are there:
#
#   ILLUMOS_HOST=heisantosh@192.168.64.2 \
#     mise x go@1.27.3 -- sh scripts/release-fork.sh
#
# It needs gh signed in and the MyGo update key. The app is ad-hoc signed,
# so a first launch may need the usual "Open" of an unsigned app; its
# updates are signed with the key. Keep the key out of the repository.
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

# --- The version is the one upstream released, and it is what main and
# work hold. -------------------------------------------------------------
git fetch --no-tags upstream || fail "could not fetch upstream"
upstream_version=$(git show upstream/main:mygo.json | jq -r .version)
main_version=$(git show main:mygo.json | jq -r .version)
[ "$main_version" = "$upstream_version" ] ||
	fail "main is Kero $main_version, upstream is $upstream_version: sync first"
[ "$version" = "$upstream_version" ] ||
	fail "mygo.json is Kero $version, upstream is $upstream_version: sync first"
upstream_tag=$(gh release view --repo egoist/kero --json tagName --jq .tagName 2>/dev/null || true)
if [ "${KERO_ALLOW_UNRELEASED:-0}" != 1 ]; then
	[ "$upstream_tag" = "$tag" ] ||
		fail "upstream's latest release is ${upstream_tag:-none}, not $tag; wait for it (or set KERO_ALLOW_UNRELEASED=1)"
fi
if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
	fail "the worktree has changes; commit them before releasing"
fi
if [ -f fork-deps.txt ]; then
	while read -r module required replaced; do
		case "${module:-}" in "" | \#*) continue ;; esac
		got=$(go list -m -json "$module" 2>/dev/null |
			jq -r '(.Version // "") + " " + (.Replace.Version // "")') ||
			fail "could not read $module from go.mod"
		[ "$got" = "$required $replaced" ] ||
			fail "$module is $got; expected $required $replaced"
	done <fork-deps.txt
fi

# The tag exists on work before the release, so GitHub does not create it
# at main's head.
if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
	[ "$(git rev-list -n1 "$tag")" = "$(git rev-parse HEAD)" ] ||
		fail "$tag points elsewhere; move it to this commit or pick another version"
else
	git tag "$tag" HEAD
fi
git push origin "$tag" || fail "could not push $tag"

mygo_dir=$(go list -m -f '{{.Dir}}' github.com/egoist/mygo)
ghostty=$(jq -r '.libraries[] | select(.name == "libghostty-vt") | .source' "$mygo_dir/plugins/terminal/mygo-plugin.json" | sed 's#.*/tree/##')
if [ ! -f build/illumos/libghostty-vt.so ] ||
	[ "$(cat build/illumos/libghostty-vt.commit 2>/dev/null || true)" != "$ghostty" ]; then
	say "building libghostty-vt for illumos ($ghostty)"
	ILLUMOS_HOST="${ILLUMOS_HOST:-helios.local}" sh scripts/build-libghostty-vt-illumos.sh
fi

say "building Kero $version for macOS"
go tool mygo build -platform darwin/amd64 -upload

app="$root/build/darwin-amd64/Kero.app"
[ -d "$app" ] || fail "the app build wrote no Kero.app"
app_version=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$app/Contents/Info.plist")
[ "$app_version" = "$version" ] ||
	fail "the app is $app_version, mygo.json is $version"
update_json="$root/build/darwin-amd64/update-darwin-amd64.json"
[ -f "$update_json" ] || fail "the app build wrote no update manifest"
[ "$(jq -r .version "$update_json")" = "$version" ] ||
	fail "the app update manifest is not $version"

say "building the server builds"
sh scripts/build-server.sh linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 illumos/amd64
go run ./internal/update/sign build/server/kero-server-$version-*.tar.gz

[ "$(build/server/darwin-amd64/kero version | sed -n 's/^kero \([^,]*\),.*/\1/p')" = "$version" ] ||
	fail "the macOS server build is not $version"
grep -q "^version='$version'$" build/server/install-server.sh ||
	fail "install-server.sh is not $version"
for p in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 illumos/amd64; do
	os="${p%/*}"
	arch="${p#*/}"
	name="kero-server-$version-$os-$arch.tar.gz"
	for f in "$name" "$name.sig" "$name.sha256"; do
		[ -f "build/server/$f" ] || fail "no $f"
	done
done

say "uploading the server builds to $tag"
gh release view "$tag" --repo "$repo" >/dev/null 2>&1 ||
	gh release create "$tag" --repo "$repo" --title "Kero $version" --notes-file CHANGELOG.md --draft
gh release upload "$tag" --repo "$repo" --clobber \
	build/server/kero-server-$version-*.tar.gz \
	build/server/kero-server-$version-*.tar.gz.sig \
	build/server/kero-server-$version-*.tar.gz.sha256 \
	build/server/install-server.sh \
	build/server/install-server.ps1
gh release edit "$tag" --repo "$repo" --draft=false

say "published https://github.com/$repo/releases/tag/$tag"
