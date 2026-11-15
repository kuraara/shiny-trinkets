# ATV TV Sync actions: the atv-tv-sync image (pyatv, broadlink) plus the current monitor.py (the Apple
# TV address, pairing and IR code), the web endpoint and the noise player.
# Classic builder, because the base image is local:  DOCKER_BUILDKIT=0 docker build -t atv-actions:latest .
FROM atv-tv-sync:latest
COPY monitor.py actions.py noise.py /app/
WORKDIR /app
CMD ["python3", "-u", "actions.py"]
