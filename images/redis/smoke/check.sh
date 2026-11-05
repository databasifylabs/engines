set -euo pipefail
ready() { [ "$(redis-cli -p 56379 ping 2> /dev/null)" = PONG ]; }
for _ in $(seq 60); do ready && break; sleep 1; done
ready
redis-cli -p 56379 INFO server | tr -d '\r' | grep -x "redis_version:$DATABASIFY_RELEASE" > /dev/null
databasify ctl is-active --quiet server.service
[ "$(databasify ctl show -p NRestarts --value server)" = 0 ]
databasify ctl logs -u server -n 3
redis-sentinel --version
for module in rejson redisearch redisbloom redistimeseries; do
    test -f "/usr/local/lib/redis/modules/$module.so" || { echo "missing module $module.so"; exit 1; }
    redis-cli -p 56379 --json MODULE LIST | jq -e --arg path "/usr/local/lib/redis/modules/$module.so" 'any(.[]; .path == $path)' > /dev/null \
        || { echo "module $module.so is not loaded"; exit 1; }
done
echo "rejson, redisearch, redisbloom and redistimeseries loaded"
redis-shake --version 2>&1 | sed -n 1p
echo "redis $DATABASIFY_RELEASE answered through the wrapper"
[ "$(id -u redis)" = 64999 ] && [ "$(id -g redis)" = 64999 ]
