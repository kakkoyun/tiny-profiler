#!/usr/bin/env bash

DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"

# Import magical bash library.
. "${DIR}/demo-nav.sh"

clear

r "Start the tiny-profiler to send profile to Parca" "./dist/tiny-profiler --node=local-test --remote-store-address=localhost:7070 --remote-store-insecure &>/dev/null &"

r "Also let's run a noisy tenant ./demo/pprof-example-app-go &>/dev/null &" "./demo/pprof-example-app-go &>/dev/null &"

PARCA_ROOT=${PARCA_ROOT:-../../Projects/parca}
PARCA=${PARCA:-../../Projects/parca/bin/parca}

r "We need to do this for each process. Is there a better way?: Parca" "$PARCA --config-path="$PARCA_ROOT/parca.yaml" &>/dev/null &"

r "Is everybody ok?" "jobs"

r "Let's see what it's look like: https://localhost:7070" "echo "https://localhost:7070""

# Last entry to run navigation mode.
navigate