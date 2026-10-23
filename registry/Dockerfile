FROM nginx:1.30-alpine
RUN apk add --no-cache jq && rm /etc/nginx/conf.d/default.conf
COPY nginx/gadget-proxy.conf /etc/nginx/gadget-proxy.conf
COPY nginx/40-gadgets.sh /docker-entrypoint.d/40-gadgets.sh
COPY registry.json /etc/gadgets/registry.json
COPY html/ /usr/share/nginx/html/
EXPOSE 80 8101-8110
