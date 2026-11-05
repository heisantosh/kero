# Kero on illumos, and this fork

This branch adds an **illumos/amd64** server build and **seamless server
updates** on macOS, Linux and illumos. It targets illumos (for example
Helios), not Solaris 11: MyGo does not build for Solaris, and Zig no
longer names it.

The fork's `main` is an exact mirror of `egoist/kero`; all of this lives on
`work`:

```
egoist/kero main ──●──●──●──●
                            │
heisantosh/kero main        ●──●──●──●     exact mirror, fast-forward only
                                    │
heisantosh/kero work                I1──I2──I3
                                          │
                                          └─ tag vX → fork release
```

## Syncing with upstream

```sh
git fetch --no-tags upstream
git switch main
git merge --ff-only upstream/main
git push origin main
git switch work
git rebase main
# test, then
git push --force-with-lease origin work
```

`main` is never committed to. Fork release tags are `v<version>`, the
version in `mygo.json`. The fork's `release.yml` is removed on `work`, so
a tag does not run upstream's signed macOS app pipeline; use
`scripts/release-fork.sh`.

## The two dependency forks

MyGo's terminal plugin and purego need small illumos changes:

- `heisantosh/purego` tag `v0.11.1-illumos.1`: illumos dlopen/dlsym,
  callbacks and struct arguments.
- `heisantosh/mygo` tag `v0.7.5-illumos.1`: three build tags in
  `plugins/terminal/internal/vt`.

`go.mod` replaces both with those tags. When upstream moves:

1. Fast-forward the fork's `main`; rebase its `illumos` branch.
2. Tag the new version.
3. Update the `replace` in `go.mod`; `go mod tidy`; test.

Both patches are small enough to send upstream; if they land, drop the
forks and the `replace` lines.

## libghostty-vt for illumos

MyGo publishes no illumos build. `scripts/build-libghostty-vt-illumos.sh`
builds one from the pinned Ghostty commit with Zig 0.16, cross-compiling
against a sysroot copied from a running illumos machine:

```sh
ILLUMOS_HOST=heisantosh@192.168.64.2 sh scripts/build-libghostty-vt-illumos.sh
```

It writes `build/illumos/libghostty-vt.so`. The server archive carries it
beside `kero`; `internal/daemon/lib_illumos.go` points MyGo's terminal
plugin at it. Rebuild it only when MyGo's manifest names another Ghostty
commit.

The script patches a copy of Zig 0.16's standard library (two illumos
bugs) and works around Zig 0.16 passing `__illumos__` as a source file.
Zig 0.17 fixes the latter but Ghostty's build requires 0.16.

## Building and installing the server

```sh
sh scripts/build-server.sh illumos/amd64
```

The archive is `build/server/kero-server-<version>-illumos-amd64.tar.gz`;
`install-server.sh` reads `SunOS` as illumos and `i86pc` as amd64, and
starts kerod as the app connects (there is no SMF unit without root).

The first install on a host can be done with the release's script; after
that the app's **Update Kero** updates it from the fork's signed release.

## Seamless updates

On Unix, kerod restarts by **execing the installed binary in place**. It
stops reading the panes, writes their screens and PIDs and clears
`FD_CLOEXEC` on the PTY masters, then `execve`s the next binary. The PID
stays the same, so the shells remain its children and `wait4` still
works; the next image adopts the PTY masters and feeds the snapshots into
new terminal emulators. The app reconnects; the programs never end.

The handoff file is versioned and additive: a newer kerod reads the
format of the install it replaces.

### The first update is disruptive once

A kerod from before this branch has no handoff. The first update to a
handoff-capable build (2.1.33) still starts the shells again. Every update
after it keeps the programs running. Install Helios fresh with 2.1.33;
update Lima and the Mac when their sessions are quiet, once.

Windows keeps the old restart for now: `syscall.Exec` has no equivalent,
and ConPTY handles would need the `SCM_RIGHTS`/`DuplicateHandle` variant.

## Releasing the fork

The update key is `~/.config/kero-fork/update-keys/mygo-update.key`
(`go tool mygo keygen`), whose public half is `mygo.json`'s
`updates.publicKey` and `internal/update/update.go`'s `PublicKey`. Never
commit the private key.

```sh
ILLUMOS_HOST=heisantosh@192.168.64.2 sh scripts/release-fork.sh
```

It builds the ad-hoc-signed macOS app and its signed update archive,
builds and signs the Linux and illumos servers, and publishes the GitHub
release `v<version>` through `gh`. The app's own update feed is that
release; so are the servers' updates.

The macOS app is ad-hoc signed, not notarized: the first launch may need
**Open** from the Finder's context menu. Its updates are still checked
against the Ed25519 key above.

## Checking a handoff

```sh
mise x go@1.27.3 -- sh scripts/test-handoff.sh
```

It starts a pane, records the kerod and program PIDs, restarts kerod, and
checks that both stayed and the screen came back. On illumos, build the
server on a machine with Go 1.27 and set `KERO_STAGE` to the copied stage.
