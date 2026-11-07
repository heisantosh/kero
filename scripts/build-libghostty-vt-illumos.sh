#!/bin/sh
# Builds libghostty-vt for illumos/amd64, the build MyGo's terminal plugin
# binds, and puts it in build/illumos/libghostty-vt.so.
#
# Ghostty pins the Zig version and has no illumos build of its own. Zig
# 0.16 can target illumos, but does not ship an illumos libc, so this
# script copies the headers and libraries of a running illumos machine
# (ILLUMOS_HOST) and cross-compiles against them. The build runs on macOS:
# Zig's own helper programs are built for the host, and their libc comes
# from the macOS SDK.
#
# It patches a copy of Zig 0.16's standard library, never the installed
# one: 0.16's illumos support has two small bugs on this path.
#
#   ILLUMOS_HOST=helios.local sh scripts/build-libghostty-vt-illumos.sh
#
# The Ghostty commit comes from MyGo's mygo-plugin.json (the commit the Go
# bindings were generated for); the Zig version from build.zig.zon. Both
# are cached under ~/.cache/kero.
set -eu

root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$root"

cache="${XDG_CACHE_HOME:-$HOME/.cache}/kero/libghostty-vt-illumos"
mkdir -p "$cache"

host="${ILLUMOS_HOST:-helios.local}"
zig_version="${ZIG_VERSION:-0.16.0}"

say() { printf 'build-libghostty-vt-illumos: %s\n' "$*" >&2; }
fail() { say "$*"; exit 1; }

for c in go jq curl tar python3; do
	command -v "$c" >/dev/null 2>&1 || fail "$c is needed"
done
if command -v mise >/dev/null 2>&1; then
	zig() { mise x "zig@$zig_version" -- zig "$@"; }
else
	zig() { command -v zig >/dev/null 2>&1 || fail "neither mise nor zig is installed"; zig "$@"; }
fi
[ "$(zig version)" = "$zig_version" ] || fail "zig $zig_version is not available (got $(zig version))"
[ "$(uname -s)" = Darwin ] || fail "run this on macOS: the cross-build's host helpers need the macOS SDK"

manifest="$(go list -m -f '{{.Dir}}' github.com/egoist/mygo)/plugins/terminal/mygo-plugin.json"
commit="$(jq -r '.libraries[] | select(.name == "libghostty-vt") | .source' "$manifest" | sed 's#.*/tree/##')"
[ -n "$commit" ] && [ "$commit" != null ] || fail "MyGo's manifest names no libghostty-vt commit"
say "Ghostty $commit, Zig $zig_version"

# --- The target's headers and libraries --------------------------------
#
# The sysroot is the running host's /usr/include and its 64-bit libraries.
# Zig's host helpers link against the macOS SDK, so its libSystem stubs
# are added to the sysroot's lib directory: ZIG_LIBC applies to the host's
# compilations too.
sysroot="$cache/sysroot"
libc="$cache/illumos-libc.txt"
if [ ! -f "$sysroot/usr/include/stdio.h" ] || [ ! -f "$sysroot/usr/lib/amd64/libc.so.1" ]; then
	say "copying the illumos sysroot from $host"
	rm -rf "$sysroot"
	mkdir -p "$sysroot"
	ssh -o BatchMode=yes -o ConnectTimeout=10 "$host" \
		'cd / && tar cf - usr/include usr/lib/amd64 usr/lib/crt1.o usr/lib/crti.o usr/lib/crtn.o lib/amd64 lib/64/ld.so.1' |
		tar -xf - -C "$sysroot"
	ln -sfn amd64 "$sysroot/usr/lib/64"
fi
sdk="$(xcrun --show-sdk-path)"
for f in libSystem.tbd libSystem.B.tbd; do
	if [ -f "$sdk/usr/lib/$f" ]; then
		ln -sf "$sdk/usr/lib/$f" "$sysroot/usr/lib/$f"
	fi
done
cat >"$libc" <<EOF
include_dir=$sysroot/usr/include
sys_include_dir=$sysroot/usr/include
crt_dir=$sysroot/usr/lib/amd64
msvc_lib_dir=
kernel32_lib_dir=
gcc_dir=/usr/bin
EOF

# --- A patched copy of Zig 0.16's standard library ---------------------
zig_lib="$cache/zig-$zig_version-lib"
if [ ! -f "$zig_lib/std/c.zig" ]; then
	say "patching Zig $zig_version's standard library for illumos"
	src_lib="$(zig env | sed -n 's/^[[:space:]]*\.lib_dir = "\(.*\)",$/\1/p')"
	[ -n "$src_lib" ] || fail "could not read Zig's lib directory from zig env"
	rm -rf "$zig_lib"
	mkdir -p "$zig_lib"
	(cd "$src_lib" && tar cf - .) | tar -xf - -C "$zig_lib"
fi
python3 - "$zig_lib" <<'PY'
import pathlib
import sys

lib = pathlib.Path(sys.argv[1])

# Zig 0.16's illumos SIG enum has duplicate tag values, which Zig rejects.
c = lib / "std/c.zig"
s = c.read_text()
old = """        pub const POLL: SIG = .IO;

        HUP = 1,
        INT = 2,
        QUIT = 3,
        ILL = 4,
        TRAP = 5,
        IOT = 6,
        ABRT = 6,
        EMT = 7,
        FPE = 8,
        KILL = 9,
        BUS = 10,
        SEGV = 11,
        SYS = 12,
        PIPE = 13,
        ALRM = 14,
        TERM = 15,
        USR1 = 16,
        USR2 = 17,
        CLD = 18,
        CHLD = 18,"""
new = """        pub const POLL: SIG = .IO;
        pub const IOT: SIG = .ABRT;
        pub const CLD: SIG = .CHLD;

        HUP = 1,
        INT = 2,
        QUIT = 3,
        ILL = 4,
        TRAP = 5,
        ABRT = 6,
        EMT = 7,
        FPE = 8,
        KILL = 9,
        BUS = 10,
        SEGV = 11,
        SYS = 12,
        PIPE = 13,
        ALRM = 14,
        TERM = 15,
        USR1 = 16,
        USR2 = 17,
        CHLD = 18,"""
if old in s:
    c.write_text(s.replace(old, new, 1))
elif 'pub const IOT: SIG = .ABRT;' not in s:
    raise SystemExit("c.zig: the Zig 0.16 illumos SIG block changed; update this script")

# dirReadIllumos returns a narrower error set than posixStatFile.
t = lib / "std/Io/Threaded.zig"
s = t.read_text()
old = "        const stat = try posixStatFile(dr.dir.handle, name, posix.AT.SYMLINK_NOFOLLOW);"
new = "        const stat = posixStatFile(dr.dir.handle, name, posix.AT.SYMLINK_NOFOLLOW) catch return error.Unexpected;"
if old in s:
    t.write_text(s.replace(old, new, 1))
elif new not in s:
    raise SystemExit("Threaded.zig: the illumos directory reader changed; update this script")
PY

# --- The Ghostty source ------------------------------------------------
ghostty="$cache/ghostty-$commit"
if [ ! -f "$ghostty/build.zig" ]; then
	say "downloading Ghostty $commit"
	rm -rf "$ghostty" "$cache/ghostty.tar.gz"
	mkdir -p "$ghostty"
	curl -fsSL "https://github.com/ghostty-org/ghostty/archive/$commit.tar.gz" -o "$cache/ghostty.tar.gz"
	tar -xzf "$cache/ghostty.tar.gz" --strip-components=1 -C "$ghostty"
fi
# Zig 0.16 passes __illumos__ to the C compiler as if it were a source
# file. An empty file of that name satisfies the compiler; Zig 0.17 fixes
# the bug, but Ghostty's build requires 0.16.
: >"$ghostty/__illumos__"

out="$cache/out"
rm -rf "$out"
say "building libghostty-vt for x86_64-illumos"
(
	cd "$ghostty"
	ZIG_LIBC="$libc" zig build \
		--zig-lib-dir "$zig_lib" \
		-Dtarget=x86_64-illumos \
		-Dapp-runtime=none \
		-Demit-lib-vt=true \
		-Dsimd=false \
		-Doptimize=ReleaseFast \
		--prefix "$out"
)

so="$(find "$out/lib" -maxdepth 1 -name 'libghostty-vt.so.*' -type f | head -n 1)"
[ -n "$so" ] || fail "the build wrote no libghostty-vt.so"
mkdir -p build/illumos
cp "$so" build/illumos/libghostty-vt.so
chmod 755 build/illumos/libghostty-vt.so
printf '%s\n' "$commit" > build/illumos/libghostty-vt.commit
say "wrote build/illumos/libghostty-vt.so ($(du -h build/illumos/libghostty-vt.so | cut -f1))"
if command -v sha256sum >/dev/null 2>&1; then
	sha256sum build/illumos/libghostty-vt.so
else
	shasum -a 256 build/illumos/libghostty-vt.so
fi
