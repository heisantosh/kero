#!/bin/sh
# Checks that a kerod hands its pane to the kerod that follows it: the
# daemon keeps its PID, the shell and the program in it keep theirs, and
# the screen comes back. Run it on the machine whose build it tests:
#
#   mise x go@1.27.3 -- sh scripts/test-handoff.sh
#
# On illumos, build on a machine with the toolchain the module needs, copy
# build/server/illumos-amd64 there, and run:
#
#   KERO_STAGE=/tmp/illumos-amd64 sh scripts/test-handoff.sh
set -eu

root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$root"

case "$(uname -s)" in
Darwin) os=darwin ;;
Linux) os=linux ;;
SunOS) os=illumos ;;
*) echo "test-handoff: not made for $(uname -s)" >&2; exit 2 ;;
esac
case "$(uname -m)" in
x86_64 | amd64 | i86pc) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) echo "test-handoff: not made for $(uname -m)" >&2; exit 2 ;;
esac

stage="${KERO_STAGE:-build/server/$os-$arch}"
if [ ! -x "$stage/kero" ]; then
	sh scripts/build-server.sh "$os/$arch"
fi

home="$(mktemp -d "${TMPDIR:-/tmp}/kero-handoff.XXXXXX")"
trap 'HOME="$home" "$home/.local/lib/kero/kero" daemon stop >/dev/null 2>&1 || true; rm -rf "$home"' EXIT

export HOME="$home"
unset KERO_HOME KERO_SOCKET KERO_PANE KERO_SESSION MYGO_GHOSTTY_VT
mkdir -p "$home/.local/lib"
cp -R "$stage" "$home/.local/lib/kero"
kero="$home/.local/lib/kero/kero"

"$kero" new -n handoff -- sh -c 'echo handoff-ok; sleep 12345'
sleep 1
before_daemon="$("$kero" daemon status | sed -n 's/.*pid \([0-9]*\).*/\1/p')"
before_program="$(pgrep -f 'sleep 12345' | head -n 1)"
[ -n "$before_daemon" ] && [ -n "$before_program" ] || {
	echo "test-handoff: no daemon or program" >&2
	"$kero" daemon status >&2
	exit 1
}
"$kero" read handoff >/dev/null

"$kero" daemon restart
sleep 2

after_daemon="$("$kero" daemon status | sed -n 's/.*pid \([0-9]*\).*/\1/p')"
after_program="$(pgrep -f 'sleep 12345' | head -n 1)"
[ "$before_daemon" = "$after_daemon" ] || {
	echo "test-handoff: daemon was $before_daemon, is $after_daemon" >&2
	exit 1
}
[ "$before_program" = "$after_program" ] || {
	echo "test-handoff: program was $before_program, is $after_program" >&2
	exit 1
}
if ! "$kero" read handoff | grep -q handoff-ok; then
	echo "test-handoff: the screen did not come back" >&2
	"$kero" read handoff >&2
	exit 1
fi
echo "handoff ok: kerod $after_daemon, program $after_program"
