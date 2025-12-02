#!/bin/bash
# Skrip Menu Sederhana untuk Zivpn UDP Installer

# --- FUNGSI UNTUK MEMANGGIL INSTALASI (Skrip Asli Anda) ---
install_zivpn() {
    echo "======================================"
    echo "   MEMULAI INSTALASI ZIVPN UDP...   "
    echo "======================================"
    
    echo -e "Updating server"
    sudo apt-get update && apt-get upgrade -y
    systemctl stop zivpn.service 1> /dev/null 2> /dev/null
    
    echo -e "Downloading UDP Service"
    wget https://github.com/Ris-Project/zipvpn/releases/download/udp-zivpn_1.4.9/udp-zivpn-linux-amd64 -O /usr/local/bin/zivpn 1> /dev/null 2> /dev/null
    chmod +x /usr/local/bin/zivpn
    mkdir /etc/zivpn 1> /dev/null 2> /dev/null
    wget https://raw.githubusercontent.com/Ris-Project/zipvpn/main/config.json -O /etc/zivpn/config.json 1> /dev/null 2> /dev/null

    echo "Generating cert files:"
    openssl req -new -newkey rsa:4096 -days 365 -nodes -x509 -subj "/C=US/ST=California/L=Los Angeles/O=Example Corp/OU=IT Department/CN=zivpn" -keyout "/etc/zivpn/zivpn.key" -out "/etc/zivpn/zivpn.crt"
    sysctl -w net.core.rmem_max=16777216 1> /dev/null 2> /dev/null
    sysctl -w net.core.wmem_max=16777216 1> /dev/null 2> /dev/null

    cat <<EOF > /etc/systemd/system/zivpn.service
[Unit]
Description=zivpn VPN Server
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

    echo -e "ZIVPN UDP Passwords"
    read -p "Enter passwords separated by commas, example: pass1,pass2 (Press enter for Default 'zi'): " input_config

    if [ -n "$input_config" ]; then
        IFS=',' read -r -a config <<< "$input_config"
        if [ ${#config[@]} -eq 1 ]; then
            config+=(${config[0]})
        fi
    else
        config=("zi")
    fi

    new_config_str="\"config\": [$(printf "\"%s\"," "${config[@]}" | sed 's/,$//')]"

    sed -i -E "s/\"config\": ?\[[[:space:]]*\"zi\"[[:space:]]*\]/${new_config_str}/g" /etc/zivpn/config.json


    systemctl enable zivpn.service
    systemctl start zivpn.service
    iptables -t nat -A PREROUTING -i $(ip -4 route ls|grep default|grep -Po '(?<=dev )(\S+)'|head -1) -p udp --dport 6000:19999 -j DNAT --to-destination :5667
    ufw allow 6000:19999/udp
    ufw allow 5667/udp
    rm zi.* 1> /dev/null 2> /dev/null
    echo -e "======================================"
    echo -e "    ✅ ZIVPN UDP INSTALLED SUCCESSFULLY!"
    echo -e "======================================"
    read -p "Tekan Enter untuk kembali ke Menu..."
}

# --- FUNGSI UTAMA MENU ---
show_menu() {
    clear
    echo "======================================"
    echo "       🚀 ZIVPN UDP MODULE MENU      "
    echo "======================================"
    echo "1. ⚙️ Install/Re-Install Zivpn UDP Service"
    echo "2. ❌ Exit"
    echo "======================================"
    read -p "Pilih opsi [1-2]: " choice
    echo " "
}

# --- LOGIKA OTOMATIS: Beri Izin dan Jalankan Menu ---

# 1. Berikan Izin Eksekusi pada Skrip ini (Self-executing permission)
# $0 adalah variabel yang berisi nama skrip yang sedang dijalankan saat ini.
chmod +x "$0" 2>/dev/null 

# 2. Jalankan Logika Utama (Loop Menu)
while true; do
    show_menu
    case "$choice" in
        1)
            install_zivpn
            ;;
        2)
            echo "Keluar dari menu. Terima kasih."
            exit 0
            ;;
        *)
            echo "Pilihan tidak valid. Silakan coba lagi."
            sleep 2
            ;;
    esac
done
