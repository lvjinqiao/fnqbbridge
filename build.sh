#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
mkdir -p .build
if [ ! -f web/trim-sdk.js ]; then
  npm pack @trimjs/web-app@0.6.0 --pack-destination .build
  tar -xzf .build/trimjs-web-app-0.6.0.tgz -C .build
  cp .build/package/dist/index.js web/trim-sdk.js
fi
if [ "$(uname -s)" = Linux ] && [ "$(uname -m)" = x86_64 ]; then
  cc -O2 -Wall -Wextra tools/native-map/main.c -ldl -o packaging/app/native-map
else
  : "${ZIG:=zig}"
  "$ZIG" cc -target x86_64-linux-gnu.2.31 -O2 -Wall -Wextra tools/native-map/main.c -ldl -o packaging/app/native-map
fi
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o packaging/app/fnqbbridge .
fnpack build -d packaging
