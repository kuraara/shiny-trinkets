#!/bin/sh
# Make the home certificate authority (once). Run on the build box; keys stay in $CA_DIR.
# The authority is name-constrained: it can only vouch for the home network and local names,
# so installing it on a phone can't be used to impersonate a real website.
set -eu
CA_DIR=${CA_DIR:-/work/ca}
NET=${NET:-<network>/<netmask>}
umask 077
mkdir -p "$CA_DIR" && cd "$CA_DIR"
[ -e ca.key ] && { echo "$CA_DIR/ca.key already exists; not replacing it"; exit 1; }
cat > ca.cnf <<CNF
[req]
distinguished_name = dn
prompt = no
x509_extensions = v3_ca
[dn]
CN = Home Gadgets local CA
O = Home gadgets
[v3_ca]
basicConstraints = critical, CA:TRUE, pathlen:0
keyUsage = critical, keyCertSign, cRLSign
subjectKeyIdentifier = hash
nameConstraints = critical, permitted;IP:$NET, permitted;DNS:.local, permitted;DNS:.lan, permitted;DNS:.home.arpa
CNF
openssl req -x509 -new -newkey rsa:3072 -nodes -keyout ca.key -out ca.crt -days 3650 -sha256 -config ca.cnf
chmod 644 ca.crt
openssl x509 -in ca.crt -noout -subject -enddate -fingerprint -sha256
