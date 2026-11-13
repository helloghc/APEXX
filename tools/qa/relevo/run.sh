#!/usr/bin/env bash
# Relay test: two Apexx nodes behind Caddy (same Caddyfile rules as
# production), constant traffic, and each node replaced in turn, twice.
# Pass = zero failed requests.
#
# Needs: the apexx binary (APEXX_BIN), a caddy binary (CADDY_BIN), the
# server's environment already exported (DATABASE_URL, ...), and the
# APEXX_QA_* variables of tools/qa (store, owner, password, DB URL).
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
BIN=${APEXX_BIN:?path to the apexx binary}
CADDY=${CADDY_BIN:?path to a caddy binary}
WORK=${APEXX_QA_WORK:-$(mktemp -d)}
P1=${P1:-8181} P2=${P2:-8182} LB=${LB:-8190}
SECS=${SECS:-75}

cat > "$WORK/Caddyfile" <<CF
{
	admin off
	auto_https off
}
:$LB {
	reverse_proxy 127.0.0.1:$P1 127.0.0.1:$P2 {
		header_up X-Forwarded-For {header.X-Forwarded-For}
		lb_policy least_conn
		health_uri /readyz
		health_interval 2s
		health_timeout 2s
		health_fails 1
		health_passes 2
		fail_duration 10s
		lb_try_duration 10s
		lb_try_interval 100ms
	}
}
CF

start() { # port
	APEXX_ADDR=127.0.0.1:$1 APEXX_TRUST_PROXY=true nohup "$BIN" serve > "$WORK/node-$1.log" 2>&1 &
	echo $! > "$WORK/node-$1.pid"
	until curl -sf "http://127.0.0.1:$1/readyz" > /dev/null; do sleep 0.1; done
}
stop() { # port: SIGTERM, wait until the process is gone
	local pid; pid=$(cat "$WORK/node-$1.pid")
	kill -TERM "$pid"
	while kill -0 "$pid" 2> /dev/null; do sleep 0.1; done
}
start $P1
start $P2
"$CADDY" run --config "$WORK/Caddyfile" --adapter caddyfile > "$WORK/caddy.log" 2>&1 &
CPID=$!
trap 'kill $CPID 2>/dev/null; for p in $P1 $P2; do kill $(cat "$WORK/node-$p.pid") 2>/dev/null || true; done' EXIT
sleep 3

if [ "${APEXX_QA_SANDBOX:-}" = 1 ]; then
	# Sandboxes under the relay: traffic to a sandbox, a publish scheduled
	# for the middle of it, and a copy running on a node when it is replaced.
	eval "$(APEXX_QA_WORK=$WORK APEXX_QA_BASE=http://127.0.0.1:$LB node "$here/sandbox.js" setup)"
	export APEXX_QA_SBX_ID APEXX_QA_SBX_COOKIE APEXX_QA_SBX_INSIDE
fi
APEXX_QA_BASE=http://127.0.0.1:$LB node "$here/load.js" "$SECS" > "$WORK/load.json" &
LPID=$!
sleep 8
for round in 1 2; do
	for p in $P1 $P2; do
		if [ "${APEXX_QA_SANDBOX:-}" = 1 ] && [ $round = 1 ] && [ $p = $P1 ]; then
			APEXX_QA_WORK=$WORK APEXX_QA_BASE=http://127.0.0.1:$LB node "$here/sandbox.js" copy "http://127.0.0.1:$p"
		fi
		t0=$(date +%s.%N)
		stop $p
		t1=$(date +%s.%N)
		start $p
		t2=$(date +%s.%N)
		printf 'relay round %s node %s: drain+stop %.1fs, start->ready %.2fs\n' $round $p "$(echo "$t1 - $t0" | bc)" "$(echo "$t2 - $t1" | bc)"
		sleep 5 # two health checks before the other node goes
	done
done
wait $LPID
cat "$WORK/load.json"
if [ "${APEXX_QA_SANDBOX:-}" = 1 ]; then
	APEXX_QA_WORK=$WORK APEXX_QA_BASE=http://127.0.0.1:$LB node "$here/sandbox.js" check | tee "$WORK/sandbox.json"
fi
python3 - "$WORK/load.json" <<'PY'
import json, sys
r = json.load(open(sys.argv[1]))
print(f"requests {r['total']}  ok {r['ok']}  failed {r['fail']}  slowest {r['maxMs']} ms  over 1 s: {r['slow']}")
sys.exit(1 if r['fail'] else 0)
PY
