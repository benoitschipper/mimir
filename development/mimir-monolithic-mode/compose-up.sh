#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-only
# Provenance-includes-location: https://github.com/cortexproject/cortex/development/tsdb-blocks-storage-s3-single-binary/compose-up.sh
# Provenance-includes-license: Apache-2.0
# Provenance-includes-copyright: The Cortex Authors.

set -e

# newer compose is a subcommand of `docker`, not a hyphenated standalone command
docker_compose() {
    if [ -x "$(command -v docker-compose)" ]; then
        docker-compose "$@"
    else
        docker compose "$@"
    fi
}

SCRIPT_DIR=$(cd "$(dirname -- "$0")" && pwd)

PROFILES=()
ARGS=()
while [[ $# -gt 0 ]]; do
    key="$1"
    case $key in
        --profile)
            PROFILES+=("$1")
            shift
            PROFILES+=("$1")
            shift
            ;;
        *)
            ARGS+=("$1")
            shift
            ;;
    esac
done

DEFAULT_PROFILES=("--profile" "prometheus" "--profile" "grafana-agent-static")
if [ ${#PROFILES[@]} -eq 0 ]; then
    PROFILES=("${DEFAULT_PROFILES[@]}")
fi

# Optionally Build prometheus images
if [ "$1"x == "fullx" ] ; then
    shift
    cd /home/owilliams/src/grafana/mimir-prometheus
    go mod vendor
    nice -n 10 make build
    mkdir -p .build/linux-amd64/
    cp promtool .build/linux-amd64/
    cp prometheus .build/linux-amd64/
    cd -

    cd /home/owilliams/src/grafana/prometheus
    go mod vendor
    nice -n 10 make build
    mkdir -p .build/linux-amd64/
    cp promtool .build/linux-amd64/
    cp prometheus .build/linux-amd64/
    cd -
fi

cd /home/owilliams/src/grafana/agent
go mod vendor
cd -

cd /home/owilliams/src/grafana/mimir
go mod vendor
cd -

CGO_ENABLED=0 GOOS=linux go build -o "${SCRIPT_DIR}"/mimir "${SCRIPT_DIR}"/../../cmd/mimir && \
docker_compose -f "${SCRIPT_DIR}"/docker-compose.yml build prometheus && \
docker_compose -f "${SCRIPT_DIR}"/docker-compose.yml build grafana-agent && \
docker_compose -f "${SCRIPT_DIR}"/docker-compose.yml build mimir-1 && \
docker_compose -f "${SCRIPT_DIR}"/docker-compose.yml up -d "$@"
# docker_compose -f "${SCRIPT_DIR}"/docker-compose.yml "${PROFILES[@]}" up "${ARGS[@]}"
