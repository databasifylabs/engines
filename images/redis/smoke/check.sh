set -euo pipefail
ready() { [ "$(redis-cli -p 56379 ping 2> /dev/null)" = PONG ]; }
for _ in $(seq 60); do ready && break; sleep 1; done
ready
redis-cli -p 56379 INFO server | tr -d '\r' | grep -x "redis_version:$DATABASIFY_RELEASE" > /dev/null
databasify ctl is-active --quiet server.service
[ "$(databasify ctl show -p NRestarts --value server)" = 0 ]
databasify ctl logs -u server -n 3
redis-sentinel --version
redis-shake --version 2>&1 | sed -n 1p
echo "redis $DATABASIFY_RELEASE answered through the wrapper"
[ "$(id -u redis)" = 64999 ] && [ "$(id -g redis)" = 64999 ]
