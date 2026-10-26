#!/bin/sh
# Issue (or renew) a server certificate from the home CA.
#   infra/ca/issue.sh <name> <subjectAltName>
#   infra/ca/issue.sh server "IP:<pc address>, IP:<pi address>, DNS:<pi name>.local"
#   infra/ca/issue.sh portainer "IP:<pi address>, DNS:<pi name>.local"
# Writes $CA_DIR/<name>.key, <name>.crt and <name>-fullchain.crt (certificate + CA). 800 days:
# Apple devices refuse longer-lived certificates from private authorities.
set -eu
CA_DIR=${CA_DIR:-/work/ca}
name=$1; san=$2
cn=$(echo "$san" | sed -E 's/^(IP|DNS):([^, ]+).*/\2/')
umask 077
cd "$CA_DIR"
cat > "$name.cnf" <<CNF
[req]
distinguished_name = dn
prompt = no
[dn]
CN = $cn
O = Home gadgets
[v3_srv]
basicConstraints = critical, CA:FALSE
keyUsage = critical, digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = $san
subjectKeyIdentifier = hash
authorityKeyIdentifier = keyid
CNF
[ -e "$name.key" ] || openssl genrsa -out "$name.key" 2048 2>/dev/null
openssl req -new -key "$name.key" -out "$name.csr" -config "$name.cnf"
openssl x509 -req -in "$name.csr" -CA ca.crt -CAkey ca.key -CAcreateserial -out "$name.crt" \
  -days 800 -sha256 -extfile "$name.cnf" -extensions v3_srv 2>/dev/null
cat "$name.crt" ca.crt > "$name-fullchain.crt"
chmod 644 "$name.crt" "$name-fullchain.crt"
openssl verify -x509_strict -CAfile ca.crt "$name.crt"
openssl x509 -in "$name.crt" -noout -enddate -ext subjectAltName
