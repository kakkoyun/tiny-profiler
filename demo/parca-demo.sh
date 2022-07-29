#!/usr/bin/env bash

DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"

# Import magical bash library.
. "${DIR}/demo-nav.sh"

clear

# Alternative lima approach!
# https://github.com/lima-vm/lima/blob/master/examples/k8s.yaml
# limactl start k8s
# export KUBECONFIG="/Users/kakkoyun/.lima/k8s/conf/kubeconfig.yaml"

r "Let's start a new cluster!" "minikube start --driver=virtualbox --kubernetes-version=v1.23.3 --cpus=4 --memory=16gb --disk-size=20gb --docker-opt dns=8.8.8.8 --docker-opt default-ulimit=memlock=9223372036854775807:9223372036854775807"

r "kubectl create namespace parca"

# curl -LO https://github.com/parca-dev/parca/releases/download/v0.12.1/kubernetes-manifest.yaml
r "kubectl apply -f https://github.com/parca-dev/parca/releases/download/v0.12.1/kubernetes-manifest.yaml" "kubectl apply -f demo/parca-server-kubernetes-manifest.yaml"

# curl -LO https://github.com/parca-dev/parca-agent/releases/download/v0.9.1/kubernetes-manifest.yaml
r "kubectl apply -f https://github.com/parca-dev/parca-agent/releases/download/v0.9.1/kubernetes-manifest.yaml" "kubectl apply -f demo/parca-agent-kubernetes-manifest.yaml"

r "kubectl -n parca port-forward service/parca 7070"

# Last entry to run navigation mode.
navigate