#!/usr/bin/env bash
set -euo pipefail; cd "$(dirname "$0")"; source .env.local; exec ./apexx-local serve
