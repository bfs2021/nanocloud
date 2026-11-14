# nano-cloud metrics: postgres
apt_install prometheus-postgres-exporter
# The exporter runs as the prometheus system user, and PostgreSQL lets that
# user in as the prometheus role over the local socket (peer), with no
# password to keep. pg_monitor reads statistics, never data.
runuser -u postgres -- psql -v ON_ERROR_STOP=1 -q <<'SQL'
DO $$ BEGIN
  CREATE ROLE prometheus LOGIN;
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
GRANT pg_monitor TO prometheus;
SQL
cat > /etc/default/prometheus-postgres-exporter <<'CONF'
# Written by nano-cloud.
DATA_SOURCE_NAME='user=prometheus host=/run/postgresql dbname=postgres sslmode=disable'
ARGS=''
CONF
systemctl enable prometheus-postgres-exporter
systemctl restart prometheus-postgres-exporter
answers http://127.0.0.1:9187/metrics 'pg_up 1'
