#!/usr/bin/env bash
# Make the Go module importable by other projects.
#
# The module is currently named just "queueforge", which `go get` cannot fetch. Run this ONCE with the
# path where you will host the repository, then build and test:
#
#   ./scripts/set-module-path.sh github.com/YOUR_USER/queueforge
#   go mod tidy && go build ./... && go test ./...
#
# Afterwards other projects can `go get <path>/sdk/go/queueforge`, `<path>/workerkit`, `<path>/outbox`.
set -euo pipefail
NEW="${1:?usage: $0 github.com/you/queueforge}"
cd "$(dirname "$0")/.."
OLD="queueforge"
[[ "$NEW" == */* ]] || { echo "module path should look like github.com/you/queueforge"; exit 1; }
grep -q "^module ${OLD}\$" go.mod || { echo "go.mod module is not '${OLD}' (already renamed?)"; exit 1; }
# import paths in Go files ("queueforge/..." -> "NEW/...")
grep -rl --include='*.go' "\"${OLD}/" . | xargs sed -i "s#\"${OLD}/#\"${NEW}/#g"
sed -i "s#^module ${OLD}\$#module ${NEW}#" go.mod
sed -i "s#^module ${OLD}/bench/headtohead\$#module ${NEW}/bench/headtohead#" bench/headtohead/go.mod
# docs/examples mentioning the old import path
grep -rl --include='*.md' "queueforge/sdk/go\|queueforge/workerkit\|queueforge/outbox" . | xargs -r sed -i "s#queueforge/sdk/go#${NEW}/sdk/go#g; s#queueforge/workerkit#${NEW}/workerkit#g; s#queueforge/outbox#${NEW}/outbox#g"
echo "Module is now ${NEW}. Next: go mod tidy && gofmt -l . && go vet ./... && go test ./..."
