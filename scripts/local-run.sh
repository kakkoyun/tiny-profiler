#!/usr/bin/env bash

# Copyright (c) 2022 The Parca Authors
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#

################################################################################
#
# This script is meant to be run from the root of this project with the Makefile
#
################################################################################

# exit immediately when a command fails
set -e
# only exit with zero if all commands of the pipeline exit successfully
set -o pipefail
# error on unset variables
set -u

PARCA_ROOT=${PARCA_ROOT:-../../Projects/parca}
PARCA=${PARCA:-../../Projects/parca/bin/parca}
PROFILER=${PROFILER:-./dist/tiny-profiler}
DEBUG=${DEBUG:-false}

trap 'kill $(jobs -p); exit 0' EXIT

(
    $PARCA --config-path="$PARCA_ROOT/parca.yaml"
) &

(
    if [ "$DEBUG" = true ]; then
        dlv --listen=:40000 --headless=true --api-version=2 --accept-multiclient exec --continue -- \
            $PROFILER \
            --node=local-test \
            --log-level=debug \
            --remote-store-address=localhost:7070 \
            --remote-store-insecure
    else
        sudo $PROFILER \
            --node=local-test \
            --log-level=debug \
            --remote-store-address=localhost:7070 \
            --remote-store-insecure
    fi
)
