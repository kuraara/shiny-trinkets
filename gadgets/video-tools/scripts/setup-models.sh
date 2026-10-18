#!/usr/bin/env bash
# Downloads the speech models into $MODELS_DIR (a volume), once. Idempotent.
# The server runs this in the background on start; its output lines show on the Status page.
#
# Each model is unpacked into a temporary folder and only moved into place when complete, so an
# interrupted download never leaves half-written files that look ready.
set -euo pipefail
M="${MODELS_DIR:-/data/models}"
mkdir -p "$M"
trap 'rm -rf "$M"/.tmp-whisper "$M"/.tmp-vosk' EXIT   # never leave partial downloads behind

W="$M/sherpa-onnx-whisper-small.en"
if [ ! -s "$W/small.en-encoder.int8.onnx" ] || [ ! -s "$W/small.en-decoder.int8.onnx" ]; then
  echo "Downloading Whisper small.en (about 640 MB, int8 parts kept)…"
  T="$M/.tmp-whisper"; rm -rf "$T"; mkdir -p "$T"
  curl -fsSL --retry 3 --retry-delay 5 \
    https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-whisper-small.en.tar.bz2 \
    | tar xj -C "$T" --exclude='*/small.en-encoder.onnx' --exclude='*/small.en-decoder.onnx' --exclude='*/test_wavs'
  rm -rf "$W"; mv "$T/sherpa-onnx-whisper-small.en" "$W"; rm -rf "$T"
fi

V="$M/vosk-model-small-en-us-0.15"
if [ ! -s "$V/am/final.mdl" ]; then
  echo "Downloading Vosk small-en (about 40 MB)…"
  T="$M/.tmp-vosk"; rm -rf "$T"; mkdir -p "$T"
  curl -fsSL --retry 3 --retry-delay 5 -o "$T/vosk.zip" https://alphacephei.com/vosk/models/vosk-model-small-en-us-0.15.zip
  echo "Unpacking Vosk…"
  python3 -I -m zipfile -e "$T/vosk.zip" "$T/"
  rm -rf "$V"; mv "$T/vosk-model-small-en-us-0.15" "$V"; rm -rf "$T"
fi
echo "Speech models ready."
