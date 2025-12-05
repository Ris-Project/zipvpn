#!/bin/bash

# =========================
# COLORS
# =========================
GREEN="\033[1;32m"
YELLOW="\033[1;33m"
CYAN="\033[1;36m"
RED="\033[1;31m"
BLUE="\033[1;34m"
RESET="\033[0m"
BOLD="\033[1m"
GRAY="\033[1;30m"

print_task() {
  echo -ne "${GRAY}•${RESET} $1..."
}

print_done() {
  echo -e "\r${GREEN}✓${RESET} $1      "
}

print_fail() {
  echo -e "\r${RED}✗${RESET} $1      "
  exit 1
}

run_silent() {
  local msg="$1"
  local cmd="$2"
  print_task "$msg"
  bash -c "$cmd" &>/tmp/zivpn_install.log
  if [ $? -eq 0 ]; then
    print_done "$msg"
  else
    print_fail "$msg (Check /tmp/zivpn_install.log)"
  fi
}

# =========================
# SAMBUTAN DI AWAL
# =========================
clear
echo -e "${CYAN}"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "   🎉  SELAMAT DATANG DI ZIVPN  🎉"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo -e "${RESET}"
echo -e "${YELLOW}💻 Installer Resmi ZiVPN UDP Server${RESET}"
echo -e "${BLUE}🚀 Powered By Ris-Project${RESET}"
echo ""
sleep 2

# =========================
# CEK OS
# =========================
if [[ "$(uname -s)" != "Linux" ]] || [[ "$(uname -m)" != "x86_64" ]]; then
  print_fail "System not supported (Linux AMD64 only)"
fi

# =========================
# VALIDASI IP VPS (ipvps)
# =========================
ALLOWED_IP_URL="https://raw.githubusercontent.com/Ris-Project/zipvpn/main/ipvps"

print_task "Validating VPS IP"

MYIP=$(curl -s ipv4.icanhazip.com)
ALLOWED_IPS=$(curl -s $ALLOWED_IP_URL)

if echo "$ALLOWED_IPS" | grep -qw "$MYIP"; then
    print_done "IP Authorized ($MYIP)"
else
    print_fail "IP NOT AUTHORIZED ($MYIP)"
fi

# =========================
# CEK INSTALASI
# =========================
if [ -f /usr/local/bin/zivpn ]; then
  print_fail "ZiVPN already installed"
fi

# =========================
# UPDATE SYSTEM
# =========================
run_silent "Updating system" "apt-get update"

if ! command -v go &> /dev/null; then
  run_silent "Installing dependencies" "apt-get install -y golang git curl wget ufw openssl"
else
  print_done "Dependencies ready"
fi

# =========================
# DOMAIN INPUT
# =========================
echo ""
echo -ne "${BOLD}Domain Configuration${RESET}\n"
while true; do
  read -p "Enter Domain: " domain
  if [[ -n "$domain" ]]; then
    break
  fi
done
echo ""

# =========================
# API KEY
# =========================
echo -ne "${BOLD}API Key Configuration${RESET}\n"
generated_key=$(openssl rand -hex 16)
echo -e "Generated Key: ${CYAN}$generated_key${RESET}"
read -p "Enter API Key (Press Enter to use generated): " input_key

if [[ -z "$input_key" ]]; then
  api_key="$generated_key"
else
  api_key="$input_key"
fi

echo -e "Using Key: ${GREEN}$api_key${RESET}"
echo ""

# =========================
# DOWNLOAD CORE
# =========================
systemctl stop zivpn.service &>/dev/null
run_silent "Downloading Core" \
"wget -q https://github.com/zahidbd2/udp-zivpn/releases/download/udp-zivpn_1.4.9/udp-zivpn-linux-amd64 -O /usr/local/bin/zivpn && chmod +x /usr/local/bin/zivpn"

mkdir -p /etc/zivpn
echo "$domain" > /etc/zivpn/domain
echo "$api_key" > /etc/zivpn/apikey

# =========================
# DOWNLOAD CONFIG
# =========================
run_silent "Configuring" \
"wget -q https://raw.githubusercontent.com/Ris-Project/zipvpn/main/config.json -O /etc/zivpn/config.json"

# =========================
# SSL
# =========================
run_silent "Generating SSL" \
"openssl req -new -newkey rsa:4096 -days 365 -nodes -x509 -subj '/C=ID/ST=Jawa Barat/L=Bandung/O=Ris-Project/OU=IT Department/CN=$domain' -keyout /etc/zivpn/zivpn.key -out /etc/zivpn/zivpn.crt"

# =========================
# SYSTEM TUNING
# =========================
sysctl -w net.core.rmem_max=16777216 &>/dev/null
sysctl -w net.core.wmem_max=16777216 &>/dev/null

# =========================
# SERVICE
# =========================
cat <<EOF > /etc/systemd/system/zivpn.service
[Unit]
Description=ZIVPN UDP VPN Server
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=/etc/zivpn
ExecStart=/usr/local/bin/zivpn server -c /etc/zivpn/config.json
Restart=always
RestartSec=3
Environment=ZIVPN_LOG_LEVEL=info
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_BIND_SERVICE CAP_NET_RAW
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE CAP_NET_RAW
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
EOF

# =========================
# API SETUP
# =========================
mkdir -p /etc/zivpn/api
run_silent "Setting up API" \
"wget -q https://raw.githubusercontent.com/Ris-Project/zipvpn/main/zivpn-api.go -O /etc/zivpn/api/zivpn-api.go && \
 wget -q https://raw.githubusercontent.com/Ris-Project/zipvpn/main/go.mod -O /etc/zivpn/api/go.mod"

cd /etc/zivpn/api
if go build -o zivpn-api zivpn-api.go &>/dev/null; then
  print_done "Compiling API"
else
  print_fail "Compiling API"
fi

cat <<EOF > /etc/systemd/system/zivpn-api.service
[Unit]
Description=ZiVPN Golang API Service
After=network.target zivpn.service

[Service]
Type=simple
User=root
WorkingDirectory=/etc/zivpn/api
ExecStart=/etc/zivpn/api/zivpn-api
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF

# =========================
# START SERVICES
# =========================
run_silent "Starting Services" \
"systemctl daemon-reload && systemctl enable zivpn && systemctl restart zivpn && systemctl enable zivpn-api && systemctl restart zivpn-api"

# =========================
# FIREWALL
# =========================
iface=$(ip -4 route ls | grep default | grep -Po '(?<=dev )(\S+)' | head -1)
iptables -t nat -A PREROUTING -i "$iface" -p udp --dport 6000:19999 -j DNAT --to-destination :5667 &>/dev/null
ufw allow 6000:19999/udp
ufw allow 5667/udp
ufw allow 8080/tcp

# =========================
# FINISH
# =========================
echo ""
echo -e "${GREEN}✅ INSTALLATION COMPLETE!${RESET}"
echo -e "🌐 Domain : ${CYAN}$domain${RESET}"
echo -e "⚙️  API    : ${CYAN}Port 8080${RESET}"
echo -e "🔐 Token  : ${CYAN}$api_key${RESET}"
echo ""
echo -e "${BLUE}🚀 Powered By Ris-Project${RESET}"
echo ""