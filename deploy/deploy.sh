#!/bin/bash
set -e

echo "=========================================="
echo "    Beszel NMS Auto-Deployment Script     "
echo "=========================================="

if [ "$EUID" -ne 0 ]; then
  echo "Error: Please run as root (use sudo)"
  exit 1
fi

DOMAIN=$1
if [ -z "$DOMAIN" ]; then
    echo "Usage: sudo bash deploy.sh <your_domain_name>"
    echo "Example: sudo bash deploy.sh nms.example.com"
    exit 1
fi

echo "[1/4] Installing necessary packages (Docker, docker-compose, caddy)..."
apt-get update -y
apt-get install -y apt-transport-https ca-certificates curl software-properties-common debian-keyring debian-archive-keyring

# Install Docker if not present
if ! command -v docker &> /dev/null; then
    curl -fsSL https://get.docker.com -o get-docker.sh
    sh get-docker.sh
    rm get-docker.sh
fi

# Install docker-compose if not present
if ! command -v docker-compose &> /dev/null; then
    curl -L "https://github.com/docker/compose/releases/download/v2.24.5/docker-compose-$(uname -s)-$(uname -m)" -o /usr/local/bin/docker-compose
    chmod +x /usr/local/bin/docker-compose
fi

# Install Caddy natively using official repo
if ! command -v caddy &> /dev/null; then
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | tee /etc/apt/sources.list.d/caddy-stable.list
    apt-get update -y
    apt-get install caddy -y
fi

echo "[2/4] Setting up Caddy Reverse Proxy & SSL for $DOMAIN..."
cat <<EOF > /etc/caddy/Caddyfile
$DOMAIN {
    reverse_proxy localhost:8090
}
EOF

systemctl enable caddy
systemctl restart caddy

echo "[3/4] Building and starting Beszel NMS Docker..."
# Ensure we run docker-compose from the deploy directory containing the yml
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" &> /dev/null && pwd)"
cd "$SCRIPT_DIR"

docker-compose build
docker-compose up -d

echo "[4/4] Deployment Complete!"
echo "Beszel Hub NMS Docker is now running in the background."
echo "Secure Access: https://$DOMAIN"
echo "It may take a few minutes for Caddy to provision the SSL certificate initially."
