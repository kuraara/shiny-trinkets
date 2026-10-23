#!/bin/bash
# redeploy.sh <gadget> [stackId]: copy the folder to the build box, build the image on the PC,
# then have Portainer redeploy the stack so the container picks up the new image.
set -euo pipefail
g=$1; id=$2
cd /workspace/gadgets/clowd-docker-stuff
tar -cf - "$g" | ssh -F ~/.ssh/config buildbox "cd /work/gadgets && rm -rf $g && tar -xf - && docker build -q -t $g:latest $g >/dev/null && echo built $g"
set -a; . ~/portainer-mcp.env; set +a
body=$(jq -n --rawfile c <(grep -v '^\s*build: \.$' "$g/docker-compose.yml") '{stackFileContent:$c, env:[], prune:false, pullImage:false}')
curl -sk -X PUT -H "X-API-Key: $PORTAINER_API_KEY" -H "Content-Type: application/json" --data "$body" "$PORTAINER_URL/api/stacks/$id?endpointId=4" | jq -c '{Id, Name, message}'
