# Gadget registry: home page, per-gadget reverse proxy (http and https on the same port) and the
# navbar. Built from the repo root, because it is assembled from every gadget's gadget.json:
#   docker build -f registry/Dockerfile -t gadget-registry:latest .
# (tools/trinket deploy registry does this on the build box).

# 1. registry.json + icons, from gadgets/*/gadget.json, registry/site.json (+ site.local.json, this
#    network's addresses; not in git) and services.json
FROM python:3.12-alpine AS assemble
WORKDIR /repo
COPY tools/trinket tools/trinket
COPY registry/site*.json registry/services.json registry/
COPY registry/icons/ registry/icons/
COPY gadgets/ gadgets/
RUN python3 tools/trinket check && python3 tools/trinket assemble --out /out

# 2. nginx, which writes its config from registry.json when it starts (nginx/40-gadgets.sh)
FROM nginx:1.30-alpine
RUN apk add --no-cache jq && rm /etc/nginx/conf.d/default.conf && mkdir -p /etc/nginx/stream.d /etc/nginx/gadgets.d
COPY registry/nginx/nginx.conf /etc/nginx/nginx.conf
COPY registry/nginx/gadget-proxy.conf /etc/nginx/gadget-proxy.conf
COPY registry/nginx/40-gadgets.sh /docker-entrypoint.d/40-gadgets.sh
COPY registry/html/ /usr/share/nginx/html/
COPY --from=assemble /out/registry.json /etc/gadgets/registry.json
COPY --from=assemble /out/icons/ /usr/share/nginx/html/_gadgets/icons/
EXPOSE 80 443 8101-8120
