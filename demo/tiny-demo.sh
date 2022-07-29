#!/usr/bin/env bash

DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"

# Import magical bash library.
. "${DIR}/demo-nav.sh"

clear

r "Start our tiny profiler: ./dist/tiny-profiler &>/dev/null &" "./dist/tiny-profiler &>/dev/null &"

r "Also let's run a noisy tenant ./demo/pprof-example-app-go &>/dev/null &" "./demo/pprof-example-app-go &>/dev/null &"

r "Is everybody ok?" "jobs"

r "Are there any captured profiles already?" "ls -alh ./tmp/profiles"

PROFILE_FILE="$(ls ./tmp/profiles | shuf -n 1)"
r "Let's grab one and check" "echo $PROFILE_FILE"

r "pprof" "pprof -symbolize=none -http=:4040 $PROFILE_FILE"
r "Let's go to pprof UI" "http://localhost:4040"

r "pprof with symbols" "pprof -http=:4040 $PROFILE_FILE"
r "Let's go to pprof UI again" "http://localhost:4040"

rc "killall tiny-profiler" # "kill $(jobs -p)"

PARCA_ROOT=${PARCA_ROOT:-../../Projects/parca}
PARCA=${PARCA:-../../Projects/parca/bin/parca}

r "We need to do this for each process. Is there a better way?" "$PARCA --config-path="$PARCA_ROOT/parca.yaml""

r "Start the profiler to send profile to Parca" "./dist/tiny-profiler --node=local-test --remote-store-address=localhost:7070 --remote-store-insecure"
r "Let's see what it's look like" "https://localhost:7070"

# Last entry to run navigation mode.
navigate
