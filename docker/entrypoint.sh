#!/bin/sh
set -eu
# A restart always returns to unknown; the user explicitly selects parked.
printf '0' > /data/params/d/IsOffroad
printf '0' > /data/params/d/IsOnroad
exec dash-backend --listen 0.0.0.0:8088 --root /recordings --cache /data/dash/cache --params /data/params/d --app /app --firmware /nonexistent --simulation --simulation-usb-root /data/dash/usb-sim --require-container-limits
