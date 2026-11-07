#!/bin/sh
# Syncs this fork with egoist/kero: main becomes an exact mirror, work is
# rebased on it, the versions are checked, the tests run, and only then is
# work pushed. It does not publish a release: scripts/release-fork.sh does
# that after a human looks at the version and the changelog.
#
# It stops rather than guess when:
#   - the worktree is dirty or a rebase is in progress;
#   - main cannot fast-forward (it gained a fork-only commit);
#   - work's mygo.json version differs from upstream main's;
#   - go.mod asks for a MyGo or purego version our fork is not based on;
#   - the tests or the handoff check fail.
#
# A conflict leaves the rebase on work for a human, unless
# KERO_SYNC_ABORT_ON_CONFLICT=1, which aborts it and leaves work as it
# was. The launchd agent sets that.
set -eu

root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$root"

# The tests and the handoff check start daemons of their own; the app's
# variables would point them at the running ones.
unset KERO_PANE KERO_SESSION KERO_SOCKET KERO_HOME MYGO_GHOSTTY_VT || true

go() {
	if command -v mise >/dev/null 2>&1; then
		mise x go@1.27.3 -- go "$@"
	else
		command go "$@"
	fi
}

say() { printf '%s\n' "$*"; }
notify() {
	osascript -e "display notification \"$*\" with title \"Kero sync\"" >/dev/null 2>&1 || true
}
fail() {
	say "sync-upstream: $*"
	notify "$*"
	exit 1
}

[ -d .git ] || fail "not a git checkout"
command -v git >/dev/null 2>&1 || fail "git is not installed"
command -v jq >/dev/null 2>&1 || fail "jq is not installed"
command -v gh >/dev/null 2>&1 || fail "gh is not installed"

lock="$root/.git/kero-sync.lock"
if ! mkdir "$lock" 2>/dev/null; then
	fail "another sync runs"
fi
trap 'rmdir "$lock" 2>/dev/null || true' EXIT HUP INT TERM

if [ -d .git/rebase-merge ] || [ -d .git/rebase-apply ]; then
	fail "a rebase is already in progress on $(git branch --show-current)"
fi
if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
	fail "the worktree has changes; commit or stash them first"
fi

git remote get-url upstream >/dev/null 2>&1 ||
	git remote add upstream https://github.com/egoist/kero.git
say "fetching upstream"
git fetch --no-tags upstream || fail "could not fetch upstream"

say "updating main"
git switch main >/dev/null || fail "could not switch to main"
main_before=$(git rev-parse HEAD)
git merge --ff-only upstream/main >/dev/null || fail "main has diverged from upstream"
if [ "$(git rev-parse HEAD)" != "$main_before" ]; then
	git push origin main || fail "could not push main"
fi

say "rebasing work"
git switch work >/dev/null || fail "could not switch to work"
if ! git -c core.editor=true rebase main; then
	if [ "${KERO_SYNC_ABORT_ON_CONFLICT:-0}" = 1 ]; then
		git rebase --abort || true
		fail "rebase conflict; work left as it was"
	fi
	fail "rebase conflict; resolve it on work, then run this again"
fi

upstream_version=$(git show upstream/main:mygo.json | jq -r .version)
main_version=$(git show main:mygo.json | jq -r .version)
work_version=$(jq -r .version mygo.json)
[ "$main_version" = "$upstream_version" ] ||
	fail "main is Kero $main_version, upstream is $upstream_version"
[ "$work_version" = "$upstream_version" ] ||
	fail "work is Kero $work_version, upstream is $upstream_version"

if [ -f fork-deps.txt ]; then
	while read -r module required replaced; do
		case "${module:-}" in "" | \#*) continue ;; esac
		got=$(go list -m -json "$module" 2>/dev/null |
			jq -r '(.Version // "") + " " + (.Replace.Version // "")') ||
			fail "could not read $module from go.mod"
		[ "$got" = "$required $replaced" ] ||
			fail "$module is $got; expected $required $replaced: rebase the dependency fork and update go.mod and fork-deps.txt"
	done <fork-deps.txt
fi

say "running the tests"
go build ./... || fail "the build failed"
go test ./internal/... -skip TestBridgeScript || fail "the tests failed"
GOOS=illumos GOARCH=amd64 CGO_ENABLED=0 go build -o /dev/null ./cmd/kero-server ||
	fail "the illumos build failed"

say "checking a handoff"
rm -rf build/server/darwin-amd64
mise x go@1.27.3 -- sh scripts/build-server.sh darwin/amd64 >/dev/null ||
	fail "the macOS server build failed"
KERO_STAGE=build/server/darwin-amd64 sh scripts/test-handoff.sh >/dev/null ||
	fail "the handoff check failed"

say "pushing work"
git fetch --no-tags origin work >/dev/null 2>&1 || true
git push --force-with-lease origin work || fail "could not push work"

upstream_tag=$(gh release view --repo egoist/kero --json tagName --jq .tagName 2>/dev/null || true)
fork_tag=$(gh release view --repo heisantosh/kero --json tagName --jq .tagName 2>/dev/null || true)
if [ -z "$upstream_tag" ]; then
	summary="synced $work_version; could not check GitHub releases"
elif [ "$upstream_tag" = "v$work_version" ]; then
	if [ "$fork_tag" = "$upstream_tag" ]; then
		summary="in sync at $work_version"
	else
		summary="upstream $upstream_tag; fork release ${fork_tag:-none}: release needed"
	fi
else
	summary="work $work_version; upstream main is ahead of its latest release $upstream_tag: do not publish yet"
fi
say "sync-upstream: $summary"
notify "$summary"
