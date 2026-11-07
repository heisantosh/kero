#!/bin/sh
# Installs a launchd agent that runs scripts/sync-upstream.sh every
# Monday at 09:00. It syncs main, rebases work, checks the versions and
# the dependency forks, runs the tests and a handoff check, and pushes
# work only when all of that passes. It never publishes a release.
#
#   sh scripts/install-sync-agent.sh
#   launchctl kickstart -k gui/$(id -u)/sh.kero.sync   # run it now
#   launchctl print gui/$(id -u)/sh.kero.sync
#   rm ~/Library/LaunchAgents/sh.kero.sync.plist
#   launchctl bootout gui/$(id -u)/sh.kero.sync
#
# A missed run, as while the Mac sleeps, runs when it next wakes.
set -eu

root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
label=sh.kero.sync
plist="$HOME/Library/LaunchAgents/$label.plist"
log="$HOME/Library/Logs/kero-sync.log"
path="$HOME/.local/share/mise/shims:$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
sock="${SSH_AUTH_SOCK:-}"

mkdir -p "$HOME/Library/LaunchAgents" "$HOME/Library/Logs"
chmod +x "$root/scripts/sync-upstream.sh"

{
	cat <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>$label</string>
	<key>ProgramArguments</key>
	<array>
		<string>/bin/sh</string>
		<string>$root/scripts/sync-upstream.sh</string>
	</array>
	<key>WorkingDirectory</key>
	<string>$root</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>$path</string>
		<key>KERO_SYNC_ABORT_ON_CONFLICT</key>
		<string>1</string>
EOF
	if [ -n "$sock" ]; then
		cat <<EOF
		<key>SSH_AUTH_SOCK</key>
		<string>$sock</string>
EOF
	fi
	cat <<EOF
	</dict>
	<key>StartCalendarInterval</key>
	<dict>
		<key>Weekday</key>
		<integer>1</integer>
		<key>Hour</key>
		<integer>9</integer>
		<key>Minute</key>
		<integer>0</integer>
	</dict>
	<key>RunAtLoad</key>
	<false/>
	<key>ProcessType</key>
	<string>Background</string>
	<key>LowPriorityIO</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>3600</integer>
	<key>StandardOutPath</key>
	<string>$log</string>
	<key>StandardErrorPath</key>
	<string>$log</string>
</dict>
</plist>
EOF
} >"$plist"

launchctl bootout "gui/$(id -u)" "$plist" >/dev/null 2>&1 || true
launchctl bootstrap "gui/$(id -u)" "$plist"
launchctl enable "gui/$(id -u)/$label" >/dev/null 2>&1 || true
printf 'Installed %s\n' "$plist"
printf 'Runs Mondays at 09:00, logging to %s\n' "$log"
printf 'Run it now: launchctl kickstart -k gui/%s/%s\n' "$(id -u)" "$label"
