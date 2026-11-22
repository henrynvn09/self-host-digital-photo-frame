#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVICE_PATH="/etc/systemd/system/digitalframe.service"
JOURNAL_CONF="/etc/systemd/journald.conf.d/digitalframe.conf"

if [[ "${1:-}" == "--uninstall" ]]; then
	sudo systemctl disable --now digitalframe.service 2>/dev/null || true
	sudo rm -f "$SERVICE_PATH" "$JOURNAL_CONF"
	sudo systemctl daemon-reload
	sudo systemctl restart systemd-journald
	echo "Uninstalled digitalframe.service."
	exit 0
fi

echo "1/4 Installing WebKitGTK, X11 DPMS (xset), and GPIO packages..."
sudo apt-get update -qq
sudo apt-get install -y python3-gi gir1.2-webkit2-4.1 gir1.2-gtk-3.0 x11-xserver-utils python3-gpiozero

echo "2/4 Ensuring config.conf exists..."
if [[ ! -f "${SCRIPT_DIR}/config.conf" ]]; then
	cp "${SCRIPT_DIR}/config.conf.sample" "${SCRIPT_DIR}/config.conf"
	echo "Created ${SCRIPT_DIR}/config.conf (edit SERVER_IP and schedule as needed)."
fi
chmod +x "${SCRIPT_DIR}/frame.sh" "${SCRIPT_DIR}/kiosk.py" "${SCRIPT_DIR}/pir.py"

echo "3/4 Configuring 64MB systemd-journald cap..."
sudo mkdir -p /etc/systemd/journald.conf.d
sudo tee "$JOURNAL_CONF" >/dev/null <<'EOF'
[Journal]
SystemMaxUse=64M
SystemMaxFileSize=8M
MaxRetentionSec=3day
EOF
sudo systemctl restart systemd-journald

echo "4/4 Installing and starting digitalframe.service..."
sed -e "s|__USER__|${USER}|g" -e "s|__CLIENT_DIR__|${SCRIPT_DIR}|g" \
	"${SCRIPT_DIR}/systemd/digitalframe.service" | sudo tee "$SERVICE_PATH" >/dev/null
sudo systemctl daemon-reload
sudo systemctl enable --now digitalframe.service

echo "Done! Check status with: sudo systemctl status digitalframe.service"
