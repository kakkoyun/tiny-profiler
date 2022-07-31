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

r "pprof -symbolize=none -http=:4040 ./tmp/profiles/$PROFILE_FILE" "pprof -symbolize=none -http=:4040 ./tmp/profiles/$PROFILE_FILE"

r "Let's go to pprof UI" "echo http://localhost:4040"

# Last entry to run navigation mode.
navigate
