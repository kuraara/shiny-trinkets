# Home certificate authority

HTTPS for the gadgets (and Portainer) without a domain: a small private certificate authority
that phones and computers trust once, by installing its certificate
(`http://<pc>/ca.html` walks through it on each kind of device).

- It is **name-constrained**: it can only vouch for addresses on the home network (`NET` when it
  is made) and `.local`, `.lan`, `.home.arpa` names, never for a real website, so installing it can't be abused to impersonate
  anything outside the house.
- Its key lives on the build box in `/work/ca` and nowhere else. Nothing in this folder is secret.

## Scripts (run on the build box)

| Script | Does |
|---|---|
| `NET=<network>/<netmask> make-ca.sh` | creates the authority (once; refuses to replace an existing one). Valid 10 years. |
| `issue.sh <name> "<SANs>"` | issues or renews a server certificate, 800 days (Apple's limit for private CAs) |
| `install-registry.sh` | copies `server.*` into the `gadget-tls` volume and restarts the registry |

Two certificates: one for the registry (both machines' addresses, so the PC and the Pi can share
it) and one for Portainer on the Pi:

    infra/ca/issue.sh server    "IP:<pc address>, IP:<pi address>, DNS:<pi name>.local"
    infra/ca/issue.sh portainer "IP:<pi address>, DNS:<pi name>.local"

Portainer takes its certificate through its API (Settings → SSL certificate does the same):

    jq -n --rawfile c /work/ca/portainer-fullchain.crt --rawfile k /work/ca/portainer.key \
      '{cert:$c, key:$k, httpEnabled:true}' |
      curl -sk -X PUT -H "X-API-Key: …" -H 'Content-Type: application/json' --data @- \
        https://<pi>:9443/api/ssl

## Renewing

Server certificates were issued on 25 Oct 2026 and expire on **2 January 2029**; the authority
expires in October 2036. Renewing a server certificate = `issue.sh` again (same key, same name)
then `install-registry.sh` / the Portainer upload. Devices only need anything new when the
authority itself is replaced.
