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

# Detect package manager
if command -v apt-get &> /dev/null; then
    PKG_MGR="apt-get"
elif command -v dnf &> /dev/null; then
    PKG_MGR="dnf"
elif command -v yum &> /dev/null; then
    PKG_MGR="yum"
else
    echo "Error: Supported package manager (apt/dnf/yum) not found."
    exit 1
fi

echo "[1/4] Installing base dependencies..."
if [ "$PKG_MGR" = "apt-get" ]; then
    $PKG_MGR update -y
    $PKG_MGR install -y apt-transport-https ca-certificates curl software-properties-common debian-keyring debian-archive-keyring
else
    $PKG_MGR install -y epel-release || true
    $PKG_MGR install -y curl ca-certificates wget
fi

# Install Docker if not present
if ! command -v docker &> /dev/null; then
    curl -fsSL https://get.docker.com -o get-docker.sh
    sh get-docker.sh || echo "Docker install script failed. Please install docker manually."
    rm -f get-docker.sh
    systemctl enable docker || true
    systemctl start docker || true
fi

# Install docker-compose if not present
if ! command -v docker-compose &> /dev/null; then
    curl -L "https://github.com/docker/compose/releases/download/v2.24.5/docker-compose-$(uname -s)-$(uname -m)" -o /usr/local/bin/docker-compose
    chmod +x /usr/local/bin/docker-compose
fi

# Install Caddy
echo "[2/4] Installing Caddy Reverse Proxy for $DOMAIN..."
if ! command -v caddy &> /dev/null; then
    if [ "$PKG_MGR" = "apt-get" ]; then
        curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg || true
        curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | tee /etc/apt/sources.list.d/caddy-stable.list
        $PKG_MGR update -y
        $PKG_MGR install caddy -y
    else
        $PKG_MGR install yum-plugin-copr 'dnf-command(copr)' -y || true
        $PKG_MGR copr enable @caddy/caddy -y || true
        $PKG_MGR install caddy -y
    fi
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
