#!/usr/bin/env bash
# Rolling deploy: the store keeps answering while the new version goes out.
#
#   APEXX_IMAGE=<new image> deploy/rolling.sh
#
# 1. Migrations run first. They must keep working with the version still
#    running (add columns and tables; drop only in a later release).
# 2. One node at a time is recreated with the new image. The old container
#    gets SIGTERM, answers /readyz with 503 for APEXX_DRAIN (Caddy takes it
#    out of rotation within 2 s), finishes requests in flight and hands its
#    background jobs to the other node. The other node serves everything.
# 3. The next node waits until the new one is healthy, so there is always
#    one node ready.
set -euo pipefail
cd "$(dirname "$0")/.."

wait_healthy() {
	local c=$1 deadline=$((SECONDS + 180))
	while :; do
		state=$(docker inspect -f '{{.State.Health.Status}}' "$(docker compose ps -q "$c")" 2>/dev/null || echo starting)
		[ "$state" = healthy ] && return 0
		if [ $SECONDS -ge $deadline ]; then
			echo "rolling: $c is $state after 180 s; stopping here (the other node keeps serving)" >&2
			exit 1
		fi
		sleep 1
	done
}

docker compose pull --quiet apexx apexx2 migrate || true
docker compose run --rm migrate
for node in apexx apexx2; do
	echo "rolling: replacing $node"
	docker compose up -d --no-deps --force-recreate "$node"
	wait_healthy "$node"
	sleep 3 # Caddy needs two passing checks (2 s apart) before sending traffic
done
echo "rolling: done"
