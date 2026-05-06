#!/bin/bash
# install-update.sh — swap /Applications/Hygur.app with the version inside a DMG,
# then relaunch. Spawned by the app right before it terminates; outlives the
# parent process so it can replace the running binary.
#
# Usage: install-update.sh <parent_pid> <dmg_path>
#
# Logs to ~/Library/Logs/Hygur/installer.log so failures are debuggable after
# the parent app exits.

set -u

PARENT_PID="${1:-}"
DMG_PATH="${2:-}"

LOG_DIR="$HOME/Library/Logs/Hygur"
LOG="$LOG_DIR/installer.log"
mkdir -p "$LOG_DIR"
exec >> "$LOG" 2>&1

echo "------------------------------------------------------------"
echo "[$(date)] start: pid=$PARENT_PID dmg=$DMG_PATH"

if [ -z "$PARENT_PID" ] || [ -z "$DMG_PATH" ]; then
  echo "ERROR: missing arguments"
  exit 64
fi
if [ ! -f "$DMG_PATH" ]; then
  echo "ERROR: DMG not found at $DMG_PATH"
  exit 65
fi

# 1. Wait up to 15s for the parent (Hygur) to exit. macOS won't let us replace
#    a running .app cleanly, and we'd get a quarantine flag if we tried.
for _ in $(seq 1 30); do
  if ! kill -0 "$PARENT_PID" 2>/dev/null; then break; fi
  sleep 0.5
done
if kill -0 "$PARENT_PID" 2>/dev/null; then
  echo "WARN: parent still alive after 15s, killing"
  kill -9 "$PARENT_PID" 2>/dev/null || true
  sleep 1
fi

# 2. Mount DMG (no Finder window, no auto-open).
ATTACH_OUTPUT=$(hdiutil attach -nobrowse -noautoopen "$DMG_PATH" 2>&1)
ATTACH_STATUS=$?
if [ $ATTACH_STATUS -ne 0 ]; then
  echo "ERROR: hdiutil attach failed ($ATTACH_STATUS): $ATTACH_OUTPUT"
  exit 66
fi

# Last column of the last line containing /Volumes/ is the mount point.
MOUNT_POINT=$(echo "$ATTACH_OUTPUT" | awk -F'\t' '/\/Volumes\// {mp=$NF} END {print mp}')
if [ -z "$MOUNT_POINT" ] || [ ! -d "$MOUNT_POINT" ]; then
  echo "ERROR: failed to locate DMG mount point"
  exit 67
fi
echo "mounted: $MOUNT_POINT"

cleanup() {
  hdiutil detach "$MOUNT_POINT" -quiet 2>/dev/null || hdiutil detach "$MOUNT_POINT" -force 2>/dev/null || true
}
trap cleanup EXIT

# 3. Locate the app inside the DMG. The DMG is supposed to ship a single .app.
APP_SOURCE=$(find "$MOUNT_POINT" -maxdepth 2 -name "*.app" -type d | head -1)
if [ -z "$APP_SOURCE" ]; then
  echo "ERROR: no .app found inside $MOUNT_POINT"
  exit 68
fi
APP_NAME=$(basename "$APP_SOURCE")
APP_DEST="/Applications/$APP_NAME"
echo "source: $APP_SOURCE  dest: $APP_DEST"

# 4. Replace the existing install. We move-then-restore so that, on failure,
#    the user isn't left without an app at all.
BACKUP=""
if [ -d "$APP_DEST" ]; then
  BACKUP="${APP_DEST}.backup-$$"
  mv "$APP_DEST" "$BACKUP"
fi
if ! cp -R "$APP_SOURCE" "$APP_DEST"; then
  echo "ERROR: cp failed, restoring backup"
  [ -n "$BACKUP" ] && mv "$BACKUP" "$APP_DEST"
  exit 69
fi
[ -n "$BACKUP" ] && rm -rf "$BACKUP"

# 5. Strip Gatekeeper quarantine + ad-hoc re-sign so the new binary launches
#    without an extra "downloaded from internet" prompt. (Only meaningful for
#    unnotarised builds; once we add a Developer ID this becomes a no-op.)
xattr -dr com.apple.quarantine "$APP_DEST" 2>/dev/null || true
codesign --deep --force --sign "-" "$APP_DEST" 2>/dev/null || true

# 6. Cleanup: detach DMG, remove the temp DMG file.
cleanup
trap - EXIT
rm -f "$DMG_PATH" 2>/dev/null || true

# 7. Relaunch.
echo "[$(date)] relaunching $APP_DEST"
open "$APP_DEST"
echo "[$(date)] done"
exit 0
