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

# =========================
# INPUT BOT PANEL (ADMIN)
# =========================
clear
echo -ne "${BOLD}Telegram Bot Panel Configuration${RESET}\n"
echo -ne "${GRAY}(Untuk Bot Admin / Panel){RESET}\n"
read -p "Bot Panel Token : " BOT_PANEL_TOKEN
read -p "Admin Panel ID  : " ADMIN_PANEL_ID
echo ""

# =========================
# INPUT BOT NOTIF (SENSOR)
# =========================
echo -ne "${BOLD}Telegram Bot Notification Configuration${RESET}\n"
echo -ne "${GRAY}(Untuk Notifikasi Install / Sensor){RESET}\n"
read -p "Bot Notif Token : " BOT_NOTIF_TOKEN
read -p "Admin Notif ID  : " ADMIN_NOTIF_ID

if [[ -z "$BOT_NOTIF_TOKEN" || -z "$ADMIN_NOTIF_ID" ]]; then
  echo -e "${YELLOW}⚠️ Bot Notifikasi NONAKTIF${RESET}"
  TELEGRAM_ENABLED=0
else
  echo -e "${GREEN}✅ Bot Notifikasi AKTIF${RESET}"
  TELEGRAM_ENABLED=1
fi
echo ""

# =========================
# TELEGRAM FUNCTION (NOTIF)
# =========================
send_telegram() {
  [[ "$TELEGRAM_ENABLED" != "1" ]] && return 0

  local message="$1"
  curl -s -X POST "https://api.telegram.org/bot${BOT_NOTIF_TOKEN}/sendMessage" \
       -d chat_id="${ADMIN_NOTIF_ID}" \
       -d text="$message" \
       -d parse_mode="Markdown" >/dev/null
}

# =========================
# TOOL FUNCTIONS
# =========================
print_task() { echo -ne "${GRAY}•${RESET} $1..."; }
print_done() { echo -e "\r${GREEN}✓${RESET} $1      "; }
print_fail() { echo -e "\r${RED}✗${RESET} $1      "; exit 1; }

run_silent() {
  local msg="$1"
  local cmd="$2"
  print_task "$msg"
  bash -c "$cmd" &>/tmp/zivpn_install.log
  [[ $? -eq 0 ]] && print_done "$msg" || print_fail "$msg"
}

# =========================
# SENSOR API MASK
# =========================
mask_api_key() {
  local key="$1"
  echo "${key:0:4}****${key: -4}"
}

# =========================
# SAMBUTAN
# =========================
echo -e "${CYAN}"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "   🎉  SELAMAT DATANG DI ZIVPN RZ  🎉"
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
  print_fail "System not supported"
fi

# =========================
# VALIDASI IP
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
[ -f /usr/local/bin/zivpn ] && print_fail "ZiVPN already installed"

# =========================
# UPDATE & DEPENDENCIES
# =========================
run_silent "Updating system" "apt-get update"

if ! command -v go &> /dev/null; then
  run_silent "Installing dependencies" \
  "apt-get install -y golang git curl wget ufw openssl"
else
  print_done "Dependencies ready"
fi

# =========================
# INPUT DOMAIN
# =========================
echo ""
echo -ne "${BOLD}Domain Configuration${RESET}\n"
read -p "Enter Domain: " domain
echo ""

# =========================
# API KEY
# =========================
echo -ne "${BOLD}API Key Configuration${RESET}\n"
generated_key=$(openssl rand -hex 16)
echo -e "Generated Key: ${CYAN}$generated_key${RESET}"
read -p "Enter API Key (Enter = auto): " input_key

[[ -z "$input_key" ]] && api_key="$generated_key" || api_key="$input_key"
echo ""

# =========================
# DOWNLOAD CORE
# =========================
run_silent "Downloading Core" \
"wget -q https://github.com/zahidbd2/udp-zivpn/releases/download/udp-zivpn_1.4.9/udp-zivpn-linux-amd64 -O /usr/local/bin/zivpn && chmod +x /usr/local/bin/zivpn"

mkdir -p /etc/zivpn
echo "$domain" > /etc/zivpn/domain
echo "$api_key" > /etc/zivpn/apikey

# =========================
# DOWNLOAD CONFIG
# =========================
run_silent "Downloading Config" \
"wget -q https://raw.githubusercontent.com/Ris-Project/zipvpn/main/config.json -O /etc/zivpn/config.json"

# =========================
# SSL
# =========================
run_silent "Generating SSL" \
"openssl req -new -newkey rsa:4096 -days 365 -nodes -x509 \
-subj '/C=ID/ST=Jawa Barat/L=Bandung/O=Ris-Project/OU=IT/CN=$domain' \
-keyout /etc/zivpn/zivpn.key -out /etc/zivpn/zivpn.crt"

# =========================
# SERVICE
# =========================
cat > /etc/systemd/system/zivpn.service <<EOF
[Unit]
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/zivpn server -c /etc/zivpn/config.json
Restart=always

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable zivpn
systemctl restart zivpn

# =========================
# FIREWALL
# =========================
ufw allow 6000:19999/udp
ufw allow 5667/udp
ufw allow 8080/tcp

# =========================
# FINISH + TELEGRAM NOTIF
# =========================
SENSOR_KEY=$(mask_api_key "$api_key")

TELEGRAM_MSG="━━━━━━━━━━━━━━━━━━━━━━
🚀 *ZIVPN UDP SERVER*
✅ *INSTALL SUCCESS*

🌐 *Domain*  ➤ $domain
⚙️ *API*     ➤ Port 8080
🔐 *Token*   ➤ $SENSOR_KEY
🖥️ *VPS IP*  ➤ $MYIP
📅 *Installed* ➤ $(date '+%d-%m-%Y | %H:%M')

━━━━━━━━━━━━━━━━━━━━━━
🔥 *Powered By Ris-Project*"

send_telegram "$TELEGRAM_MSG"

echo ""
echo -e "${GREEN}✅ INSTALLATION COMPLETE!${RESET}"
echo -e "🌐 Domain : $domain"
echo -e "⚙️ API    : Port 8080"
echo -e "🔐 Token  : $api_key"
echo ""