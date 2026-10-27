#!/usr/bin/env bash
# Rebuild the hub and the backends from this checkout and restart on :7777:
# a development hub, which installs these builds instead of a release's. The
# backends are built for every platform the hub can install them on (local
# and over SSH); the old local backend hangs up its tabs on the way out, the
# new one resumes them.
# Run it from a regular terminal: a session inside claude-mux is hung up by
# the restart along with the rest.
set -euo pipefail

dir=$(cd "$(dirname "$0")" && pwd)
url=http://127.0.0.1:7777
state=$HOME/.claude-mux
bin=$state/dev

mkdir -p "$bin"
CGO_CXXFLAGS=-Wno-deprecated-literal-operator go -C "$dir" build -o "$state/claude-mux.new" ./cmd/hub
for p in darwin-arm64 darwin-amd64 linux-amd64 linux-arm64; do
	CGO_ENABLED=0 GOOS=${p%-*} GOARCH=${p#*-} go -C "$dir" build -trimpath -ldflags=-s -o "$bin/claude-mux-backend-$p.new" ./cmd/backend
done

if pid=$(lsof -ti tcp:7777 -sTCP:LISTEN); then
	kill -INT "$pid"
	while kill -0 "$pid" 2>/dev/null; do sleep 0.1; done
fi

# Fresh files, not copies over the old ones: macOS kills a binary rewritten
# in place on its stale code signature.
mv -f "$state/claude-mux.new" "$state/claude-mux"
for f in "$bin"/*.new; do mv -f "$f" "${f%.new}"; done
rm -rf "$state/bin/dev" # where the builds were before the backends were installed by version
nohup "$state/claude-mux" -headless -backend-dir "$bin" -cwd "${CLAUDE_MUX_CWD:-$HOME/Documents/@Ankr/codespace}" >"$state/hub.log" 2>&1 </dev/null &
until [ "$(curl -s "$url/env/local/api/state" | jq -r '.sessions | type' 2>/dev/null)" = array ]; do sleep 0.2; done
curl -s "$url/env/local/api/state" | jq -r '.sessions[] | "resumed \(.name)"'
echo "claude-mux on $url"
