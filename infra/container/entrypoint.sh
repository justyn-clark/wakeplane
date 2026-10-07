#!/bin/sh
set -eu
# Railway mounts volumes as root. Initialize only the mount directory, then
# replace this bootstrap process with the unprivileged application.
if [ "$(id -u)" = 0 ]; then
  chown 10001:10001 /data
  exec setpriv --reuid=10001 --regid=10001 --clear-groups -- "$@"
fi
exec "$@"
