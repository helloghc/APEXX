#!/usr/bin/env bash
set -euo pipefail; cd "$(dirname "$0")"; source .env.demo; exec ./apexx-local serve
