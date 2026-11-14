# nano-cloud metrics: mysql
apt_install prometheus-mysqld-exporter
# The exporter runs as the prometheus system user, and MySQL lets that user
# in as prometheus over the local socket (auth_socket), with no password to
# keep. The grants read statistics, never data.
mysql <<'SQL'
CREATE USER IF NOT EXISTS 'prometheus'@'localhost' IDENTIFIED WITH auth_socket;
GRANT PROCESS, REPLICATION CLIENT ON *.* TO 'prometheus'@'localhost';
SQL
install -d -m 755 /etc/prometheus
cat > /etc/prometheus/mysqld-exporter.cnf <<'CNF'
[client]
user = prometheus
socket = /run/mysqld/mysqld.sock
CNF
chmod 644 /etc/prometheus/mysqld-exporter.cnf
# DATA_SOURCE_NAME for exporters before 0.15, the file for the rest.
cat > /etc/default/prometheus-mysqld-exporter <<'CONF'
# Written by nano-cloud.
DATA_SOURCE_NAME='prometheus@unix(/run/mysqld/mysqld.sock)/'
ARGS='--config.my-cnf=/etc/prometheus/mysqld-exporter.cnf'
CONF
systemctl enable prometheus-mysqld-exporter
systemctl restart prometheus-mysqld-exporter
answers http://127.0.0.1:9104/metrics 'mysql_up 1'
