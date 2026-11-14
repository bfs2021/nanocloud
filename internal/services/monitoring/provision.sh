# nano-cloud monitoring setup
# Installs the configuration nano-cloud sends on standard input (a gzipped
# tar of /etc and /var/lib files), checking Prometheus's before using it.
set -e
d=$(mktemp -d)
trap 'rm -rf "$d"' EXIT
tar -xzf - -C "$d"
promtool check config "$d/etc/prometheus/prometheus.yml" >/dev/null
install -d -m 755 /etc/prometheus/nanocloud.d /var/lib/grafana/dashboards/nanocloud
# Keep whatever Prometheus was set up with before nano-cloud took it over.
[ -f /etc/prometheus/prometheus.yml.before-nanocloud ] ||
  cp /etc/prometheus/prometheus.yml /etc/prometheus/prometheus.yml.before-nanocloud
cp "$d/etc/prometheus/prometheus.yml" "$d/etc/prometheus/nanocloud-rules.yml" /etc/prometheus/
[ -f /etc/prometheus/nanocloud.d/targets.json ] || echo '[]' > /etc/prometheus/nanocloud.d/targets.json
install -d -m 755 /etc/grafana/provisioning/dashboards
cp "$d/etc/grafana/provisioning/dashboards/nanocloud.yaml" /etc/grafana/provisioning/dashboards/
rm -f /var/lib/grafana/dashboards/nanocloud/*.json
cp "$d"/var/lib/grafana/dashboards/nanocloud/*.json /var/lib/grafana/dashboards/nanocloud/
chmod 644 /etc/prometheus/prometheus.yml /etc/prometheus/nanocloud-rules.yml \
  /etc/grafana/provisioning/dashboards/nanocloud.yaml /var/lib/grafana/dashboards/nanocloud/*.json
home=GF_DASHBOARDS_DEFAULT_HOME_DASHBOARD_PATH=/var/lib/grafana/dashboards/nanocloud/overview.json
grep -qx "$home" /etc/default/grafana-server || echo "$home" >> /etc/default/grafana-server
systemctl reload prometheus || systemctl restart prometheus
# Grafana reads its provisioning when it starts.
systemctl restart grafana-server
