# Cloud VM

Gadgets that need to be reachable from anywhere (Photo Drop for party guests, Photo Stitch) can
run on a small cloud VM as well as at home. The VM only runs containers: images are built on
the build box and deployed over SSH, so nothing is compiled there and no Claude runs there.

## Setting one up

1. Create an Ubuntu 24.04 VM (AWS Lightsail or EC2). x86 or ARM both work; a VM with 1 GB of
   memory is enough for the light gadgets (the script adds swap on small machines).
2. On the build box, the deploy key is `/work/keys/cloud-deploy` (made once with
   `ssh-keygen -t ed25519 -f /work/keys/cloud-deploy`). Copy its `.pub` line.
3. On the VM, run [`setup-vm.sh`](setup-vm.sh) with that line:

       curl -fsSL https://raw.githubusercontent.com/kuraara/shiny-trinkets/master/infra/cloud/setup-vm.sh \
         | sudo bash -s -- "ssh-ed25519 AAAA… shiny-trinkets build box (cloud deploy)"

   It installs Docker, creates a `trinket` user (Docker, key login only), caps log sizes,
   adds swap if memory is small, and keeps security updates automatic. Safe to run again.
4. In the AWS console, open ports **80** and **443** to everyone and **22** only to your own
   address (Lightsail: the instance's **Networking** tab; EC2: its security group). Docker
   publishes ports past `ufw`, so the AWS firewall is the one that counts.
5. Add the VM's address to `registry/site.local.json` as a host (not in git).

## Before a gadget goes public

At home, everything trusts the local network. On the internet it doesn't:

- **HTTPS** needs a public name (a domain, or a free `<ip>.sslip.io` name) and a Let's Encrypt
  certificate; the home certificate authority only covers the home network.
- **Anyone can reach it.** A gadget that lets visitors create things (Photo Drop rooms) needs a
  password for that; gadgets that expose private data (Quote Finder, Video Tools) stay at home.
