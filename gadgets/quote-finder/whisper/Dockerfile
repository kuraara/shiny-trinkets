FROM debian:bookworm-slim AS build
RUN apt-get update && apt-get install -y --no-install-recommends git cmake build-essential ca-certificates && rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 https://github.com/ggml-org/whisper.cpp /w && cd /w \
 && cmake -B build -DGGML_NATIVE=ON -DBUILD_SHARED_LIBS=OFF -DWHISPER_BUILD_TESTS=OFF \
 && cmake --build build -j8 --config Release --target whisper-cli
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg python3 ca-certificates curl && rm -rf /var/lib/apt/lists/*
COPY --from=build /w/build/bin/whisper-cli /usr/local/bin/whisper-cli
COPY transcribe.py translate.py deliver.py /
ENTRYPOINT ["python3", "-u", "/transcribe.py"]
