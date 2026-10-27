set -euo pipefail
ready() { mariadb-admin --socket=/srv/mariadb/run/mariadbd.sock -uroot ping > /dev/null 2>&1; }
for _ in $(seq 90); do ready && break; sleep 1; done
ready
mariadb --socket=/srv/mariadb/run/mariadbd.sock -uroot -Nse 'SELECT VERSION()' | grep "^$DATABASIFY_RELEASE-MariaDB" > /dev/null
databasify ctl is-active --quiet server.service
[ "$(databasify ctl show -p NRestarts --value server)" = 0 ]
databasify ctl logs -u server -n 3
mariadb-backup --version 2>&1 | tail -1
test -f /usr/lib/galera/libgalera_smm.so
rclone version | sed -n 1p
socat -V | sed -n 2p
zstd --version
echo "mariadb $DATABASIFY_RELEASE answered through the wrapper"
[ "$(id -u mysql)" = 64999 ] && [ "$(id -g mysql)" = 64999 ]
