#!/bin/bash
set -euo pipefail
[[ "$(cat /data/params/d/IsOffroad)" == 1 && ( ( ! -e /data/params/d/IsOnroad && ! -L /data/params/d/IsOnroad ) || "$(cat /data/params/d/IsOnroad)" == 0 ) ]] || { echo 'Device no longer parked; staged files left inactive'; exit 1; }
# Replace durable boot files only after their complete contents are on storage.
# GNU sync with a path fsyncs that file/directory, not the entire filesystem.
dash_install_atomic() {
  local source="$1" destination="$2" mode="$3"
  sudo -n install -o root -g root -m "$mode" "$source" "$destination.next"
  sudo -n sync -- "$destination.next"
  sudo -n mv -Tf -- "$destination.next" "$destination"
  sudo -n sync -- "$(dirname "$destination")"
}
DASH_PREVIOUS="$(readlink /data/dash/current || true)"
dash_activation_failed() {
  trap - ERR
  sudo -n systemctl stop dash.service || true
  if [[ "$DASH_PREVIOUS" =~ ^releases/[a-zA-Z0-9.+_-]+$ ]]; then
    ln -sfn "$DASH_PREVIOUS" /data/dash/.current.new
    mv -Tf /data/dash/.current.new /data/dash/current
    sync -- /data/dash
  fi
  echo 'Activation failed; previous release restored when available. Dash remains stopped.' >&2
  exit 1
}
DASH_TAG="$(sed -n 's/.*"tag": *"\([a-zA-Z0-9.+_-]*\)".*/\1/p' /data/dash/.install.new/release.json)"
[[ "$DASH_TAG" =~ ^[a-zA-Z0-9][a-zA-Z0-9.+_-]{0,127}$ ]] || { echo 'Invalid staged tag'; exit 1; }
# Never remove a release that the boot symlink might still reference. An
# identical reinstall can reuse it; repair a damaged release with a fresh tag.
if [[ -e "/data/dash/releases/$DASH_TAG" ]]; then
  diff -qr /data/dash/.install.new "/data/dash/releases/$DASH_TAG" >/dev/null || { echo 'Existing release differs; install a fresh --source build or another release tag'; exit 1; }
fi
# Flush staged payloads before publishing their directory or boot pointer.
find /data/dash/.install.new -type f -exec sync -- {} +
find /data/dash/.install.new -depth -type d -exec sync -- {} +
trap dash_activation_failed ERR
if systemctl cat dash.service >/dev/null 2>&1; then sudo -n systemctl stop dash.service; fi
# The bootstrap is stable across OTA. Only an explicit SSH install replaces it.
dash_install_atomic /data/dash/.install.new/dash-backend /data/dash/dash-bootstrap 755
dash_install_atomic /data/dash/.install-service.new /data/dash/install.sh 755
dash_install_atomic /data/dash/.registration.new /data/dash/systemd-registration.sh 755
dash_install_atomic /data/dash/.uninstall-service.new /data/dash/uninstall.sh 755
dash_install_atomic /data/dash/.verify-service.new /data/dash/verify-service.sh 755
dash_install_atomic /data/dash/.boot-state.new /data/dash/boot-state.sh 755
sudo -n install -d -o root -g root -m 755 /data/dash/systemd
dash_install_atomic /data/dash/.service.new /data/dash/systemd/dash.service 644
if [[ -e "/data/dash/releases/$DASH_TAG" ]]; then
  rm -rf /data/dash/.install.new
else
  mv /data/dash/.install.new "/data/dash/releases/$DASH_TAG"
fi
sync -- /data/dash/releases
ln -sfn "releases/$DASH_TAG" /data/dash/.current.new
mv -Tf /data/dash/.current.new /data/dash/current
sync -- /data/dash
sync -- /data/dash/.update.new
mv /data/dash/.update.new /data/dash/update.json
sync -- /data/dash
rm -f /data/dash/pending.json /data/dash/activation.json /data/dash/ready.json
dash_install_atomic /data/dash/.startup.new /data/dash/startup-mode 644
rm -f /data/dash/.startup.new
DASH_INSTALL_FLAGS=()
if [[ "$(cat /data/dash/startup-mode)" == runtime ]]; then DASH_INSTALL_FLAGS=(--runtime); fi
if ! sudo -n bash /data/dash/install.sh "${DASH_INSTALL_FLAGS[@]}"; then
  sudo -n systemctl stop dash.service || true
  if [[ "$DASH_PREVIOUS" =~ ^releases/[a-zA-Z0-9.+_-]+$ ]]; then
    ln -s "$DASH_PREVIOUS" /data/dash/.current.new
    mv -Tf /data/dash/.current.new /data/dash/current
    sync -- /data/dash
  fi
  echo 'Installation failed; previous release restored when available. Service remains stopped.'
  exit 1
fi
trap - ERR
rm -f /data/dash/.registration.new /data/dash/.install-service.new /data/dash/.verify-service.new /data/dash/.uninstall-service.new /data/dash/.service.new /data/dash/.boot-state.new
