set -e
export DEBIAN_FRONTEND=noninteractive
# apt_install installs packages, refreshing the package lists only when the
# VM's are too old to have them. It waits for another apt to finish first.
apt_install() {
  apt-get -o DPkg::Lock::Timeout=300 install -y --no-install-recommends "$@" ||
    { apt-get -o DPkg::Lock::Timeout=300 update &&
      apt-get -o DPkg::Lock::Timeout=300 install -y --no-install-recommends "$@"; }
}
# answers waits for a metrics page to report a line starting with $2, so the
# setup only succeeds once Prometheus will find what it looks for.
answers() {
  for _ in $(seq 1 60); do
    if curl -sf "$1" | grep -q "^$2"; then return 0; fi
    sleep 2
  done
  echo "$1 does not report $2" >&2
  return 1
}
