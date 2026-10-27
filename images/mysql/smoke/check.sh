set -euo pipefail
ready() { mysqladmin --socket=/srv/mysql/run/mysqld.sock -uroot ping > /dev/null 2>&1; }
for _ in $(seq 90); do ready && break; sleep 1; done
ready
mysql --socket=/srv/mysql/run/mysqld.sock -uroot -Nse 'SELECT VERSION()' | grep -x "$DATABASIFY_RELEASE" > /dev/null
databasify ctl is-active --quiet server.service
[ "$(databasify ctl show -p NRestarts --value server)" = 0 ]
databasify ctl logs -u server -n 3
xtrabackup --version 2>&1 | tail -1
mysqlbinlog --version | grep -F "Ver $DATABASIFY_RELEASE "
mysqlsh --version
rclone version | sed -n 1p
zstd --version
echo "mysql $DATABASIFY_RELEASE answered through the wrapper"
[ "$(id -u mysql)" = 64999 ] && [ "$(id -g mysql)" = 64999 ]
