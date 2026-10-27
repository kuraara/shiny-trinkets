#!/usr/bin/env bash
# Prepare a fresh Ubuntu VM (AWS Lightsail or EC2, 24.04) to run gadgets deployed from the build box.
# Run once, with sudo, passing the build box's public key:
#
#   curl -fsSL https://raw.githubusercontent.com/kuraara/shiny-trinkets/master/infra/cloud/setup-vm.sh \
#     | sudo bash -s -- "ssh-ed25519 AAAA... shiny-trinkets build box (cloud deploy)"
#
# It installs Docker (from Docker's own apt repository), creates a "trinket" user that may run
# Docker and log in only with that key, caps Docker's log files, adds swap on small machines and
# keeps security updates automatic. Running it again is safe.
#
# It doesn't open any ports: do that in the AWS console (Lightsail: the instance's Networking tab;
# EC2: its security group). Open 80 and 443 to everyone, and 22 only to your own address.
set -euo pipefail

KEY=${1:-}
[ "$(id -u)" -eq 0 ] || { echo "setup-vm: run it with sudo" >&2; exit 1; }
case $KEY in
  ssh-ed25519\ * | ssh-rsa\ * | ecdsa-sha2-*) ;;
  *) echo "usage: sudo bash setup-vm.sh \"<build box SSH public key>\"" >&2; exit 2 ;;
esac
. /etc/os-release
[ "$ID" = ubuntu ] || echo "setup-vm: written for Ubuntu; this is $PRETTY_NAME" >&2
export DEBIAN_FRONTEND=noninteractive

echo "== packages"
apt-get update -q
apt-get install -y -q ca-certificates curl unattended-upgrades >/dev/null
if ! command -v docker >/dev/null; then
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $VERSION_CODENAME stable" \
    > /etc/apt/sources.list.d/docker.list
  apt-get update -q
  apt-get install -y -q docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin >/dev/null
fi
# Security updates install themselves (Ubuntu's default; made explicit here).
[ -f /etc/apt/apt.conf.d/20auto-upgrades ] || printf 'APT::Periodic::Update-Package-Lists "1";\nAPT::Periodic::Unattended-Upgrade "1";\n' \
  > /etc/apt/apt.conf.d/20auto-upgrades

echo "== docker"
mkdir -p /etc/docker
if [ ! -f /etc/docker/daemon.json ]; then   # small disks: keep container logs to 3 × 10 MB each
  printf '{\n  "log-driver": "json-file",\n  "log-opts": { "max-size": "10m", "max-file": "3" }\n}\n' > /etc/docker/daemon.json
  systemctl restart docker
fi
systemctl enable --now docker >/dev/null 2>&1

echo "== trinket user (key login only)"
id trinket >/dev/null 2>&1 || useradd --create-home --shell /bin/bash trinket
usermod -p '*' -aG docker trinket            # no password at all; the key is the only way in
install -d -m 700 -o trinket -g trinket /home/trinket/.ssh
touch /home/trinket/.ssh/authorized_keys
grep -qxF "$KEY" /home/trinket/.ssh/authorized_keys || echo "$KEY" >> /home/trinket/.ssh/authorized_keys
chown trinket:trinket /home/trinket/.ssh/authorized_keys
chmod 600 /home/trinket/.ssh/authorized_keys

mem_mb=$(awk '/MemTotal/ { print int($2 / 1024) }' /proc/meminfo)
if [ "$mem_mb" -lt 2048 ] && [ -z "$(swapon --show)" ]; then
  echo "== swap (2 GB, as this machine has ${mem_mb} MB of memory)"
  fallocate -l 2G /swapfile
  chmod 600 /swapfile
  mkswap /swapfile >/dev/null
  swapon /swapfile
  grep -q '^/swapfile ' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
fi

sudo -u trinket docker info >/dev/null   # the trinket user can use Docker
ip=$(curl -fsS --max-time 5 https://checkip.amazonaws.com || echo unknown)
echo
echo "Ready: $(docker --version | cut -d' ' -f3 | tr -d ,) Docker, ${mem_mb} MB memory," \
  "$(df -h / | awk 'NR == 2 { print $4 }') disk free, public address $ip."
echo "Next: in the AWS console open ports 80 and 443 (and 22 only from your address),"
echo "then give that public address to whoever deploys the gadgets."
