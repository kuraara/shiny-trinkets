# Build box: does the heavy lifting (Go/Node builds, tests, headless Chromium, docker builds)
# on the PC so the Pi only plans. Reached over SSH (key only) on port 2223.
FROM golang:1.26-bookworm
ARG NODE_VERSION=24.11.1
RUN apt-get update && apt-get install -y --no-install-recommends \
      openssh-server git rsync curl ca-certificates jq xz-utils make \
      chromium fonts-noto-core fonts-noto-color-emoji imagemagick ffmpeg \
      docker.io docker-compose \
    && rm -rf /var/lib/apt/lists/* \
    && curl -fsSL https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-x64.tar.xz | tar -xJ -C /usr/local --strip-components=1 \
    && mkdir -p /opt/tools && cd /opt/tools && npm init -y >/dev/null && npm install --no-audit --no-fund puppeteer-core@24 \
    && ln -s /usr/local/go/bin/go /usr/local/go/bin/gofmt /usr/local/bin/ \
    && mkdir -p /run/sshd /root/.ssh && chmod 700 /root/.ssh \
    && sed -i -e 's/^#\?PasswordAuthentication.*/PasswordAuthentication no/' -e 's/^#\?PermitRootLogin.*/PermitRootLogin prohibit-password/' -e 's/^#\?Port .*/Port 2223/' /etc/ssh/sshd_config
ENV NODE_PATH=/opt/tools/node_modules PUPPETEER_EXECUTABLE_PATH=/usr/bin/chromium PATH=/usr/local/go/bin:/root/go/bin:$PATH
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
WORKDIR /work
EXPOSE 2223
ENTRYPOINT ["/entrypoint.sh"]
