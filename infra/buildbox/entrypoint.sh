#!/bin/sh
# AUTHORIZED_KEYS comes from the stack environment.
printf '%s\n' "$AUTHORIZED_KEYS" > /root/.ssh/authorized_keys
chmod 600 /root/.ssh/authorized_keys
ssh-keygen -A >/dev/null
# sshd sessions don't inherit the container env; non-interactive ssh commands read ~/.bashrc
grep -q buildbox-env /root/.bashrc 2>/dev/null || {
  printf '%s\n' '# buildbox-env' \
    "export PATH=$PATH" "export NODE_PATH=$NODE_PATH" \
    "export PUPPETEER_EXECUTABLE_PATH=$PUPPETEER_EXECUTABLE_PATH" "export GOPATH=/root/go"
  cat /root/.bashrc 2>/dev/null
} > /tmp/bashrc && mv /tmp/bashrc /root/.bashrc
exec /usr/sbin/sshd -D -e
