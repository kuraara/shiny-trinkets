#!/bin/sh
# SSH to the build box with the watcher's own key (made on first start; its public key is on the
# page and in the log, to add to the build box's AUTHORIZED_KEYS).
set -e
mkdir -p /data/ssh /root/.ssh
[ -f /data/ssh/id_ed25519 ] || ssh-keygen -q -t ed25519 -N '' -C trinket-watcher -f /data/ssh/id_ed25519
cat > /root/.ssh/config <<EOC
Host buildbox
  HostName ${BUILDBOX_HOST:?set BUILDBOX_HOST}
  Port ${BUILDBOX_PORT:-2223}
  User root
  IdentityFile /data/ssh/id_ed25519
  UserKnownHostsFile /data/ssh/known_hosts
  StrictHostKeyChecking accept-new
  ServerAliveInterval 30
EOC
chmod 600 /root/.ssh/config
echo "build box key: $(cat /data/ssh/id_ed25519.pub)"
exec python3 -u /app/watcher.py
