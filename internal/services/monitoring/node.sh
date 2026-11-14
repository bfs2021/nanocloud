# nano-cloud metrics: node
apt_install prometheus-node-exporter
systemctl enable --now prometheus-node-exporter
answers http://127.0.0.1:9100/metrics node_uname_info
