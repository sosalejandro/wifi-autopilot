#!/usr/bin/env bash
# Install wifi-autopilot as a systemd service. Build first as your user (`make build`),
# then run from the repo root: sudo ./install.sh
set -euo pipefail

[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
BIN=${BIN:-./wifi-autopilot}
ETC=/etc/wifi-autopilot
[[ -x $BIN ]] || { echo "$BIN not found; run 'make build' first" >&2; exit 1; }
for cmd in nmcli sops age-keygen; do
  command -v "$cmd" >/dev/null || { echo "missing dependency: $cmd" >&2; exit 1; }
done

install -Dm755 "$BIN" /usr/local/bin/wifi-autopilot
install -Dm644 deploy/wifi-autopilot.service /etc/systemd/system/wifi-autopilot.service
install -dm700 "$ETC"

if [[ ! -f $ETC/age.key ]]; then
  age-keygen -o "$ETC/age.key" 2>/dev/null
  chmod 600 "$ETC/age.key"
  echo "created age key $ETC/age.key (back it up: without it the config can't be decrypted)"
fi
PUB=$(age-keygen -y "$ETC/age.key")
[[ -f $ETC/.sops.yaml ]] || sed "s/AGE_PUBLIC_KEY/$PUB/" deploy/sops.yaml.example > "$ETC/.sops.yaml"

systemctl daemon-reload

if [[ -f $ETC/config.sops.yaml ]]; then
  systemctl enable wifi-autopilot >/dev/null
  systemctl restart wifi-autopilot
  echo "wifi-autopilot (re)started. Logs: journalctl -fu wifi-autopilot"
  exit 0
fi

cat <<EOF

Installed. Create your config next (age public key: $PUB):

  sudo cp deploy/config.example.yaml $ETC/config.sops.yaml
  sudoedit $ETC/config.sops.yaml                      # your networks and passwords
  (cd $ETC && sudo sops encrypt --in-place config.sops.yaml)
  sudo SOPS_AGE_KEY_FILE=$ETC/age.key wifi-autopilot -config $ETC/config.sops.yaml -once
  sudo systemctl enable --now wifi-autopilot
EOF
