#!/bin/sh
# Put the registry's certificate into the gadget-tls Docker volume and restart the registry.
# Run on the build box (its docker CLI talks to the PC's Docker) after infra/ca/issue.sh server ….
set -eu
CA_DIR=${CA_DIR:-/work/ca}
docker volume create gadget-tls >/dev/null
docker rm -f tls-install >/dev/null 2>&1 || true
docker create --name tls-install -v gadget-tls:/t alpine:3.22 true >/dev/null
docker cp "$CA_DIR/server-fullchain.crt" tls-install:/t/fullchain.crt
docker cp "$CA_DIR/server.key" tls-install:/t/server.key
docker cp "$CA_DIR/ca.crt" tls-install:/t/ca.crt
docker rm tls-install >/dev/null
docker run --rm -v gadget-tls:/t alpine:3.22 sh -c 'chown 0:0 /t/*; chmod 600 /t/server.key; chmod 644 /t/*.crt'
docker restart gadget-registry >/dev/null 2>&1 && echo "registry restarted with the new certificate" || echo "certificate installed (registry not running)"
