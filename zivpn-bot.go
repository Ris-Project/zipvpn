package main

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "math/rand"
    "net/http"
    "net/url"
    "os"
    "os/exec"
    "path/filepath"
    "strconv"
    "strings"
    "sync"
    "time"

    tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
    BotConfigFile = "/etc/zivpn/bot-config.json"
    // !!! GANTI INI DENGAN URL GAMBAR MENU ANDA !!!
    MenuPhotoURL = "https://h.uguu.se/NgaOrSxG.png"

    // Interval untuk pengecekan dan penghapusan akun expired
    AutoDeleteInterval = 1 * time.Minute
    // Interval untuk Auto Backup (6 Jam)
    AutoBackupInterval = 6 * time.Hour

    // Konfigurasi Backup dan Service
    BackupDir   = "/etc/zivpn/backups"
    ServiceName = "zivpn"
)

type BotConfig struct {
    BotToken string         `json:"bot_token"`
    AdminID  int64          `json:"admin_id"`
    Servers  []ServerConfig `json:"servers"`
}

type ServerConfig struct {
    Name   string `json:"name"`
    Url    string `json:"url"`
    ApiKey string `json:"api_key"`
}

type IpInfo struct {
    City string `json:"city"`
    Isp  string `json:"isp"`
}

type UserData struct {
    Host     string `json:"host"` // Host untuk backup
    Password string `json:"password"`
    Expired  string `json:"expired"`
    Status   string `json:"status"`
}

// Variabel global dengan Mutex untuk keamanan konkurensi (Thread-Safe)
var (
    stateMutex        sync.RWMutex
    userStates        = make(map[int64]string)
    tempUserData      = make(map[int64]map[string]string)
    lastMessageIDs    = make(map[int64]int)
    userCurrentServer = make(map[int64]int) // Menyimpan index server yang dipilih user
)

func main() {
    rand.Seed(time.Now().UnixNano())

    if err := os.MkdirAll(BackupDir, 0755); err != nil {
        log.Printf("Gagal membuat direktori backup: %v", err)
    }

    config, err := loadConfig()
    if err != nil {
        log.Fatal("Gagal memuat konfigurasi bot:", err)
    }

    if len(config.Servers) == 0 {
        log.Fatal("Konfigurasi error: Tidak ada server yang didefinisikan di config.")
    }

    bot, err := tgbotapi.NewBotAPI(config.BotToken)
    if err != nil {
        log.Panic(err)
    }

    bot.Debug = false
    log.Printf("Authorized on account %s", bot.Self.UserName)

    // --- BACKGROUND WORKER (PENGHAPUSAN OTOMATIS) ---
    go func() {
        // Jalankan sekali saat startup
        for _, server := range config.Servers {
            autoDeleteExpiredUsers(bot, config.AdminID, false, server)
        }
        ticker := time.NewTicker(AutoDeleteInterval)
        for range ticker.C {
            // Reload config saat ticker berjalan agar server baru yang ditambahkan ikut diproses
            currentConfig, _ := loadConfig()
            for _, server := range currentConfig.Servers {
                autoDeleteExpiredUsers(bot, config.AdminID, false, server)
            }
        }
    }()

    // --- BACKGROUND WORKER (AUTO BACKUP) ---
    go func() {
        // Jalankan sekali saat startup
        for _, server := range config.Servers {
            performAutoBackup(bot, config.AdminID, server)
        }
        ticker := time.NewTicker(AutoBackupInterval)
        for range ticker.C {
            // Reload config saat ticker berjalan
            currentConfig, _ := loadConfig()
            for _, server := range currentConfig.Servers {
                performAutoBackup(bot, config.AdminID, server)
            }
        }
    }()

    u := tgbotapi.NewUpdate(0)
    u.Timeout = 60

    updates := bot.GetUpdatesChan(u)

    for update := range updates {
        if update.Message != nil {
            handleMessage(bot, update.Message, config)
        } else if update.CallbackQuery != nil {
            handleCallback(bot, update.CallbackQuery, config)
        }
    }
}

func handleMessage(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, config BotConfig) {
    if msg.From.ID != config.AdminID {
        reply := tgbotapi.NewMessage(msg.Chat.ID, "⛔ Akses Ditolak. Anda bukan admin.")
        sendAndTrack(bot, reply)
        return
    }

    stateMutex.RLock()
    state, exists := userStates[msg.From.ID]
    stateMutex.RUnlock()

    // Handle Restore dari Upload File
    if exists && state == "wait_restore_file" {
        if msg.Document != nil {
            // Dapatkan server aktif saat ini
            serverIdx := getServerIndex(msg.From.ID)
            if serverIdx >= len(config.Servers) {
                serverIdx = 0
            }
            handleRestoreFromUpload(bot, msg, config.Servers[serverIdx])
        } else {
            sendMessage(bot, msg.Chat.ID, "❌ Mohon kirimkan file backup (.json).")
        }
        return
    }

    if exists {
        handleState(bot, msg, state, config)
        return
    }

    if msg.IsCommand() {
        switch msg.Command() {
        case "start":
            // Reload config agar server baru langsung muncul
            newConfig, err := loadConfig()
            if err == nil {
                config = newConfig
            }
            
            // Jika belum ada server yang dipilih dan ada >1 server, tampilkan pilihan
            if len(config.Servers) > 1 {
                if !hasServerSelected(msg.From.ID) {
                    showServerSelection(bot, msg.Chat.ID, config)
                    return
                }
            }
            showMainMenu(bot, msg.Chat.ID, config)
        default:
            msg := tgbotapi.NewMessage(msg.Chat.ID, "Perintah tidak dikenal.")
            sendAndTrack(bot, msg)
        }
    }
}

func handleCallback(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery, config BotConfig) {
    if query.From.ID != config.AdminID {
        bot.Request(tgbotapi.NewCallback(query.ID, "Akses Ditolak"))
        return
    }

    callbackData := query.Data

    // Logic Switch Server
    if strings.HasPrefix(callbackData, "select_server:") {
        idxStr := strings.TrimPrefix(callbackData, "select_server:")
        idx, err := strconv.Atoi(idxStr)
        if err == nil && idx < len(config.Servers) {
            setServerIndex(query.From.ID, idx)
            showMainMenu(bot, query.Message.Chat.ID, config)
        }
        bot.Request(tgbotapi.NewCallback(query.ID, ""))
        return
    }

    if callbackData == "menu_change_server" {
        showServerSelection(bot, query.Message.Chat.ID, config)
        bot.Request(tgbotapi.NewCallback(query.ID, ""))
        return
    }

    // Logic Tambah Server
    if callbackData == "menu_add_server" {
        setState(query.From.ID, "add_server_name")
        msg := tgbotapi.NewMessage(query.Message.Chat.ID, "🆕 *TAMBAH SERVER BARU*\n\nLangkah 1/3:\nSilakan masukkan **NAMA SERVER**.\n\nContoh: `SG Premium 2`")
        msg.ParseMode = "Markdown"
        sendAndTrack(bot, msg)
        bot.Request(tgbotapi.NewCallback(query.ID, ""))
        return
    }

    // Dapatkan server aktif
    serverIdx := getServerIndex(query.From.ID)
    if serverIdx >= len(config.Servers) {
        sendMessage(bot, query.Message.Chat.ID, "⚠️ Error: Server tidak valid. Silakan pilih ulang server.")
        showServerSelection(bot, query.Message.Chat.ID, config)
        bot.Request(tgbotapi.NewCallback(query.ID, ""))
        return
    }
    activeServer := config.Servers[serverIdx]

    switch {
    case callbackData == "menu_trial_1":
        createGenericTrialUser(bot, query.Message.Chat.ID, 1, activeServer)
    case callbackData == "menu_trial_15":
        createGenericTrialUser(bot, query.Message.Chat.ID, 15, activeServer)
    case callbackData == "menu_trial_30":
        createGenericTrialUser(bot, query.Message.Chat.ID, 30, activeServer)
    case callbackData == "menu_trial_60":
        createGenericTrialUser(bot, query.Message.Chat.ID, 60, activeServer)
    case callbackData == "menu_trial_90":
        createGenericTrialUser(bot, query.Message.Chat.ID, 90, activeServer)
    case callbackData == "menu_create":
        setState(query.From.ID, "create_username")
        setTempData(query.From.ID, make(map[string]string))
        sendMessage(bot, query.Message.Chat.ID, "🔑 *MENU CREATE*\nSilakan masukkan **PASSWORD** yang diinginkan:")
    case callbackData == "menu_delete":
        showUserSelection(bot, query.Message.Chat.ID, 1, "delete", activeServer)
    case callbackData == "menu_renew":
        showUserSelection(bot, query.Message.Chat.ID, 1, "renew", activeServer)
    case callbackData == "menu_list":
        listUsers(bot, query.Message.Chat.ID, activeServer)
    case callbackData == "menu_info":
        systemInfo(bot, query.Message.Chat.ID, activeServer)

    case callbackData == "menu_backup":
        performManualBackup(bot, query.Message.Chat.ID, activeServer)
    case callbackData == "menu_restore":
        setState(query.From.ID, "wait_restore_file")
        msg := tgbotapi.NewMessage(query.Message.Chat.ID, "📥 *RESTORE DATA*\n\nTarget Server: `"+activeServer.Name+"`\nSilakan kirimkan file backup `.json` Anda.\n\nBot akan otomatis membuat ulang akun yang ada di dalam file tersebut ke server ini.")
        msg.ParseMode = "Markdown"
        msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
            tgbotapi.NewInlineKeyboardRow(
                tgbotapi.NewInlineKeyboardButtonData("❌ Batal", "cancel"),
            ),
        )
        sendAndTrack(bot, msg)
    case callbackData == "menu_clean_restart":
        cleanAndRestartService(bot, query.Message.Chat.ID, activeServer)

    case callbackData == "cancel":
        resetState(query.From.ID)
        showMainMenu(bot, query.Message.Chat.ID, config)
    case strings.HasPrefix(callbackData, "page_"):
        parts := strings.Split(callbackData, ":")
        action := parts[0][5:]
        page, _ := strconv.Atoi(parts[1])
        showUserSelection(bot, query.Message.Chat.ID, page, action, activeServer)
    case strings.HasPrefix(callbackData, "select_renew:"):
        username := strings.TrimPrefix(callbackData, "select_renew:")
        setTempData(query.From.ID, map[string]string{"username": username})
        setState(query.From.ID, "renew_days")
        sendMessage(bot, query.Message.Chat.ID, fmt.Sprintf("🔄 *MENU RENEW*\nUser: `%s`\nMasukkan tambahan durasi (*Hari*):", username))
    case strings.HasPrefix(callbackData, "select_delete:"):
        username := strings.TrimPrefix(callbackData, "select_delete:")
        msg := tgbotapi.NewMessage(query.Message.Chat.ID, fmt.Sprintf("❓ *KONFIRMASI HAPUS*\nAnda yakin ingin menghapus user `%s` dari server `%s`?", username, activeServer.Name))
        msg.ParseMode = "Markdown"
        msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
            tgbotapi.NewInlineKeyboardRow(
                tgbotapi.NewInlineKeyboardButtonData("✅ Ya, Hapus", "confirm_delete:"+username),
                tgbotapi.NewInlineKeyboardButtonData("❌ Batal", "cancel"),
            ),
        )
        sendAndTrack(bot, msg)
    case strings.HasPrefix(callbackData, "confirm_delete:"):
        username := strings.TrimPrefix(callbackData, "confirm_delete:")
        deleteUser(bot, query.Message.Chat.ID, username, activeServer)
    }

    bot.Request(tgbotapi.NewCallback(query.ID, ""))
}

func handleState(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, state string, config BotConfig) {
    userID := msg.From.ID
    text := strings.TrimSpace(msg.Text)

    // --- LOGIKA TAMBAH SERVER ---
    switch state {
    case "add_server_name":
        if text == "" {
            sendMessage(bot, msg.Chat.ID, "❌ Nama server tidak boleh kosong.")
            return
        }
        setTempData(userID, map[string]string{"name": text})
        setState(userID, "add_server_url")
        sendMessage(bot, msg.Chat.ID, "Langkah 2/3:\nSilakan masukkan **URL API SERVER**.\n\nContoh: `http://103.200.20.10:8080`\n\n*Mulai dengan http:// atau https://*")
        return

    case "add_server_url":
        _, err := url.Parse(text)
        if err != nil || (!strings.HasPrefix(text, "http://") && !strings.HasPrefix(text, "https://")) {
            sendMessage(bot, msg.Chat.ID, "❌ Format URL salah.\nPastikan dimulai dengan `http://` atau `https://`.")
            return
        }
        
        stateMutex.RLock()
        data, _ := tempUserData[userID]
        stateMutex.RUnlock()
        data["url"] = text
        setTempData(userID, data)
        
        setState(userID, "add_server_key")
        sendMessage(bot, msg.Chat.ID, "Langkah 3/3:\nSilakan masukkan **API KEY** server tersebut.")
        return

    case "add_server_key":
        if text == "" {
            sendMessage(bot, msg.Chat.ID, "❌ API Key tidak boleh kosong.")
            return
        }

        stateMutex.RLock()
        data, ok := tempUserData[userID]
        stateMutex.RUnlock()

        if !ok {
            sendMessage(bot, msg.Chat.ID, "❌ Sesi berakhir. Silakan ulangi.")
            resetState(userID)
            return
        }

        // Simpan ke file
        err := appendServerToConfig(data["name"], data["url"], text)
        if err != nil {
            sendMessage(bot, msg.Chat.ID, "❌ Gagal menyimpan konfigurasi: "+err.Error())
            resetState(userID)
            return
        }

        resetState(userID)
        msgSuccess := fmt.Sprintf("✅ *SERVER BERHASIL DITAMBAHKAN!*\n\nNama: `%s`\nURL: `%s`\n\nServer telah disimpan ke file konfigurasi.\n\n⚠️ **PENTING**: Silakan ketik `/start` untuk memuat ulang daftar server.", data["name"], data["url"])
        sendMessage(bot, msg.Chat.ID, msgSuccess)
        return
    }

    // --- LOGIKA USER LAMA (Create/Renew) ---
    serverIdx := getServerIndex(userID)
    if serverIdx >= len(config.Servers) {
        sendMessage(bot, msg.Chat.ID, "⚠️ Sesi server hilang. Silakan pilih ulang.")
        showServerSelection(bot, msg.Chat.ID, config)
        resetState(userID)
        return
    }
    activeServer := config.Servers[serverIdx]

    switch state {
    case "create_username":
        setTempData(userID, map[string]string{"username": text})
        setState(userID, "create_days")
        sendMessage(bot, msg.Chat.ID, fmt.Sprintf("⏳ *CREATE USER*\nPassword: `%s`\nMasukkan **Durasi** (*Hari*) pembuatan:", text))

    case "create_days":
        days, err := strconv.Atoi(text)
        if err != nil {
            sendMessage(bot, msg.Chat.ID, "❌ Durasi harus angka. Coba lagi:")
            return
        }
        stateMutex.RLock()
        data, ok := tempUserData[userID]
        stateMutex.RUnlock()

        if !ok {
            sendMessage(bot, msg.Chat.ID, "❌ Sesi berakhir. Silakan mulai dari awal.")
            resetState(userID)
            return
        }

        createUser(bot, msg.Chat.ID, data["username"], days, activeServer)
        resetState(userID)

    case "renew_days":
        days, err := strconv.Atoi(text)
        if err != nil {
            sendMessage(bot, msg.Chat.ID, "❌ Durasi harus angka. Coba lagi:")
            return
        }

        stateMutex.RLock()
        data, ok := tempUserData[userID]
        stateMutex.RUnlock()

        if !ok {
            sendMessage(bot, msg.Chat.ID, "❌ Sesi berakhir. Silakan mulai dari awal.")
            resetState(userID)
            return
        }

        renewUser(bot, msg.Chat.ID, data["username"], days, activeServer)
        resetState(userID)
    }
}

func handleRestoreFromUpload(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, server ServerConfig) {
    resetState(msg.From.ID)
    sendMessage(bot, msg.Chat.ID, "⏳ Sedang mengunduh dan memproses file backup ke server "+server.Name+"...")

    url, err := bot.GetFileDirectURL(msg.Document.FileID)
    if err != nil {
        sendMessage(bot, msg.Chat.ID, "❌ Gagal mengambil link file dari Telegram.")
        return
    }

    resp, err := http.Get(url)
    if err != nil {
        sendMessage(bot, msg.Chat.ID, "❌ Gagal mendownload file.")
        return
    }
    defer resp.Body.Close()

    var backupUsers []UserData
    if err := json.NewDecoder(resp.Body).Decode(&backupUsers); err != nil {
        sendMessage(bot, msg.Chat.ID, "❌ Format file backup rusak atau bukan JSON yang valid.")
        return
    }

    if len(backupUsers) == 0 {
        sendMessage(bot, msg.Chat.ID, "⚠️ File backup kosong.")
        return
    }

    sendMessage(bot, msg.Chat.ID, fmt.Sprintf("⏳ Memproses %d user untuk server %s...\nMohon tunggu sebentar.", len(backupUsers), server.Name))

    successCount := 0
    skippedCount := 0
    failedCount := 0
    var failedUsers []string

    layouts := []string{
        "2006-01-02",
        "2006-01-02 15:04:05",
        "2006-01-02T15:04:05Z",
        "2006-01-02T15:04:05+07:00",
    }

    for i, u := range backupUsers {
        var expiredTime time.Time
        var parseErr error
        parsed := false

        for _, layout := range layouts {
            expiredTime, parseErr = time.Parse(layout, u.Expired)
            if parseErr == nil {
                parsed = true
                break
            }
        }

        if !parsed {
            failedCount++
            failedUsers = append(failedUsers, u.Password)
            continue
        }

        duration := time.Until(expiredTime)
        days := int(duration.Hours() / 24)

        if days <= 0 {
            skippedCount++
            continue
        }

        // API Call ke server yang dipilih
        res, err := apiCall("POST", "/user/create", map[string]interface{}{
            "password": u.Password,
            "days":     days,
        }, server)

        if err != nil {
            failedCount++
            failedUsers = append(failedUsers, u.Password)
        } else if res["success"] == true {
            successCount++
        } else {
            if msg, ok := res["message"].(string); ok {
                if strings.Contains(strings.ToLower(msg), "already exists") || strings.Contains(strings.ToLower(msg), "sudah ada") {
                    skippedCount++
                } else {
                    failedCount++
                    failedUsers = append(failedUsers, fmt.Sprintf("%s(%s)", u.Password, msg))
                }
            } else {
                failedCount++
            }
        }

        if i < len(backupUsers)-1 {
            time.Sleep(200 * time.Millisecond)
        }
    }

    msgResult := fmt.Sprintf("✅ *Restore Selesai di Server %s*\n\n"+
        "📂 Total Data: %d\n"+
        "✅ Berhasil dibuat: %d\n"+
        "⚠️ Dilewati (Sudah ada/Expired): %d\n",
        server.Name, len(backupUsers), successCount, skippedCount)

    if failedCount > 0 {
        msgResult += fmt.Sprintf("❌ Gagal (%d): %s", failedCount, strings.Join(failedUsers, ", "))
    }

    sendMessage(bot, msg.Chat.ID, msgResult)
}

func showServerSelection(bot *tgbotapi.BotAPI, chatID int64, config BotConfig) {
    var rows [][]tgbotapi.InlineKeyboardButton

    for i, srv := range config.Servers {
        // Cek apakah ini server yang aktif
        isActive := false
        if idx, ok := userCurrentServer[chatID]; ok && idx == i {
            isActive = true
        }

        label := srv.Name
        if isActive {
            label = "✅ " + label
        }

        rows = append(rows, tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("select_server:%d", i)),
        ))
    }

    msg := tgbotapi.NewMessage(chatID, "🌐 *PILIH SERVER*\nSilakan pilih server yang ingin Anda kelola:")
    msg.ParseMode = "Markdown"
    msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
    sendAndTrack(bot, msg)
}

func showUserSelection(bot *tgbotapi.BotAPI, chatID int64, page int, action string, server ServerConfig) {
    users, err := getUsers(server)
    if err != nil {
        sendMessage(bot, chatID, "❌ Gagal mengambil data user dari server "+server.Name+".")
        return
    }

    if len(users) == 0 {
        sendMessage(bot, chatID, "📂 Tidak ada user saat ini di server "+server.Name+".")
        return
    }

    perPage := 10
    totalPages := (len(users) + perPage - 1) / perPage

    if page < 1 {
        page = 1
    }
    if page > totalPages {
        page = totalPages
    }

    start := (page - 1) * perPage
    end := start + perPage
    if end > len(users) {
        end = len(users)
    }

    var rows [][]tgbotapi.InlineKeyboardButton
    for _, u := range users[start:end] {
        statusIcon := "🟢"
        if u.Status == "Expired" {
            statusIcon = "🔴"
        }
        label := fmt.Sprintf("%s %s (Kadaluarsa: %s)", statusIcon, u.Password, u.Expired)
        data := fmt.Sprintf("select_%s:%s", action, u.Password)
        rows = append(rows, tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData(label, data),
        ))
    }

    var navRow []tgbotapi.InlineKeyboardButton
    if page > 1 {
        navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData("⬅️ Sebelumnya", fmt.Sprintf("page_%s:%d", action, page-1)))
    }
    if page < totalPages {
        navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData("Selanjutnya ➡️", fmt.Sprintf("page_%s:%d", action, page+1)))
    }
    if len(navRow) > 0 {
        rows = append(rows, navRow)
    }

    rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("⬅️ Kembali ke Menu", "cancel")))

    title := ""
    switch action {
    case "delete":
        title = "🗑️ HAPUS AKUN"
    case "renew":
        title = "🔄 PERPANJANG AKUN"
    }

    msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("*%s - %s*\nPilih user (Halaman %d/%d):", title, server.Name, page, totalPages))
    msg.ParseMode = "Markdown"
    msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
    sendAndTrack(bot, msg)
}

func showMainMenu(bot *tgbotapi.BotAPI, chatID int64, config BotConfig) {
    serverIdx := getServerIndex(chatID)
    if serverIdx >= len(config.Servers) {
        serverIdx = 0
        setServerIndex(chatID, 0) // Default to first server if invalid
    }
    activeServer := config.Servers[serverIdx]

    ipInfo, _ := getIpInfo()

    // Info dari API Server Spesifik
    domain := "Unknown"
    totalUsers := 0
    if res, err := apiCall("GET", "/info", nil, activeServer); err == nil && res["success"] == true {
        if data, ok := res["data"].(map[string]interface{}); ok {
            if d, ok := data["domain"].(string); ok {
                domain = d
            }
        }
    }
    if users, err := getUsers(activeServer); err == nil {
        totalUsers = len(users)
    }

    msgText := fmt.Sprintf("✨ *WELCOME TO BOT PGETUNNEL UDP ZIVPN*\n\n"+
        "🌐 *Server Aktif*: `%s`\n"+
        "Server Info:\n"+
        "•  🌐 *Domain*: `%s`\n"+
        "•  📍 *Lokasi*: `%s`\n"+
        "•  📡 *ISP*: `%s`\n"+
        "•  👤 *Total Akun*: `%d`\n\n"+
        "Untuk bantuan, hubungi Admin: @JesVpnt\n\n"+
        "Silakan pilih menu di bawah ini:",
        activeServer.Name, domain, ipInfo.City, ipInfo.Isp, totalUsers)

    deleteLastMessage(bot, chatID)

    var keyboardRows [][]tgbotapi.InlineKeyboardButton

    // Row 1: Server Switch & Add Server
    if len(config.Servers) > 1 {
        keyboardRows = append(keyboardRows, tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("🔄 Ganti Server", "menu_change_server"),
            tgbotapi.NewInlineKeyboardButtonData("➕ Tambah Server", "menu_add_server"),
        ))
    } else {
        // Jika cuma 1 server, tombol tambah tetap muncul
        keyboardRows = append(keyboardRows, tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("➕ Tambah Server", "menu_add_server"),
        ))
    }

    // Row 2: Create
    keyboardRows = append(keyboardRows, tgbotapi.NewInlineKeyboardRow(
        tgbotapi.NewInlineKeyboardButtonData("➕ Buat Akun", "menu_create"),
        tgbotapi.NewInlineKeyboardButtonData("🚀 Trial 1 Hari", "menu_trial_1"),
    ))

    // Row 3: Trial Paid
    keyboardRows = append(keyboardRows, tgbotapi.NewInlineKeyboardRow(
        tgbotapi.NewInlineKeyboardButtonData("⭐ 15 Hari 6k", "menu_trial_15"),
        tgbotapi.NewInlineKeyboardButtonData("🌟 30 Hari 12k", "menu_trial_30"),
    ))

    // Row 4: Trial Paid 2
    keyboardRows = append(keyboardRows, tgbotapi.NewInlineKeyboardRow(
        tgbotapi.NewInlineKeyboardButtonData("✨ 60 Hari 24k", "menu_trial_60"),
        tgbotapi.NewInlineKeyboardButtonData("🔥 90 Hari 35k", "menu_trial_90"),
    ))

    // Row 5: Management
    keyboardRows = append(keyboardRows, tgbotapi.NewInlineKeyboardRow(
        tgbotapi.NewInlineKeyboardButtonData("🔄 Renew Akun", "menu_renew"),
        tgbotapi.NewInlineKeyboardButtonData("🗑️ Hapus Akun", "menu_delete"),
    ))

    // Row 6: Info & List
    keyboardRows = append(keyboardRows, tgbotapi.NewInlineKeyboardRow(
        tgbotapi.NewInlineKeyboardButtonData("📋 Daftar Akun", "menu_list"),
        tgbotapi.NewInlineKeyboardButtonData("📊 Info Server", "menu_info"),
    ))

    // Row 7: Backup
    keyboardRows = append(keyboardRows, tgbotapi.NewInlineKeyboardRow(
        tgbotapi.NewInlineKeyboardButtonData("💾 Backup Data", "menu_backup"),
        tgbotapi.NewInlineKeyboardButtonData("♻️ Restore Data", "menu_restore"),
    ))

    // Row 8: Tools
    keyboardRows = append(keyboardRows, tgbotapi.NewInlineKeyboardRow(
        tgbotapi.NewInlineKeyboardButtonData("🧹 Hapus Expired & Restart", "menu_clean_restart"),
    ))

    keyboard := tgbotapi.NewInlineKeyboardMarkup(keyboardRows...)

    photoMsg := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(MenuPhotoURL))
    photoMsg.Caption = msgText
    photoMsg.ParseMode = "Markdown"
    photoMsg.ReplyMarkup = keyboard

    sentMsg, err := bot.Send(photoMsg)
    if err == nil {
        stateMutex.Lock()
        lastMessageIDs[chatID] = sentMsg.MessageID
        stateMutex.Unlock()
    } else {
        log.Printf("Gagal mengirim foto menu: %v. Mengirim teks biasa.", err)
        textMsg := tgbotapi.NewMessage(chatID, msgText)
        textMsg.ParseMode = "Markdown"
        textMsg.ReplyMarkup = keyboard
        sendAndTrack(bot, textMsg)
    }
}

func sendMessage(bot *tgbotapi.BotAPI, chatID int64, text string) {
    msg := tgbotapi.NewMessage(chatID, text)
    msg.ParseMode = "Markdown"

    stateMutex.RLock()
    _, inState := userStates[chatID]
    stateMutex.RUnlock()

    if inState {
        cancelKb := tgbotapi.NewInlineKeyboardMarkup(
            tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("❌ Batal", "cancel")),
        )
        msg.ReplyMarkup = cancelKb
    }
    sendAndTrack(bot, msg)
}

// --- Helper Functions for Safe Map Access ---

func setState(userID int64, state string) {
    stateMutex.Lock()
    defer stateMutex.Unlock()
    userStates[userID] = state
}

func resetState(userID int64) {
    stateMutex.Lock()
    defer stateMutex.Unlock()
    delete(userStates, userID)
    delete(tempUserData, userID)
}

func setTempData(userID int64, data map[string]string) {
    stateMutex.Lock()
    defer stateMutex.Unlock()
    tempUserData[userID] = data
}

func setServerIndex(userID int64, index int) {
    stateMutex.Lock()
    defer stateMutex.Unlock()
    userCurrentServer[userID] = index
}

func getServerIndex(userID int64) int {
    stateMutex.RLock()
    defer stateMutex.RUnlock()
    return userCurrentServer[userID]
}

func hasServerSelected(userID int64) bool {
    stateMutex.RLock()
    defer stateMutex.RUnlock()
    _, ok := userCurrentServer[userID]
    return ok
}

func deleteLastMessage(bot *tgbotapi.BotAPI, chatID int64) {
    stateMutex.RLock()
    msgID, ok := lastMessageIDs[chatID]
    stateMutex.RUnlock()

    if ok {
        deleteMsg := tgbotapi.NewDeleteMessage(chatID, msgID)
        _, err := bot.Request(deleteMsg)
        if err == nil {
            stateMutex.Lock()
            delete(lastMessageIDs, chatID)
            stateMutex.Unlock()
        }
    }
}

func sendAndTrack(bot *tgbotapi.BotAPI, msg tgbotapi.MessageConfig) {
    deleteLastMessage(bot, msg.ChatID)
    sentMsg, err := bot.Send(msg)
    if err == nil {
        stateMutex.Lock()
        lastMessageIDs[msg.ChatID] = sentMsg.MessageID
        stateMutex.Unlock()
    }
}

// --- END Helpers ---

func generateRandomPassword(length int) string {
    const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
    b := make([]byte, length)
    for i := range b {
        b[i] = charset[rand.Intn(len(charset))]
    }
    return string(b)
}

// --- FUNGSI TAMBAH SERVER KE FILE KONFIGURASI ---
func appendServerToConfig(name, url, apiKey string) error {
    // 1. Baca config yang ada
    currentConfig, err := loadConfig()
    if err != nil {
        return fmt.Errorf("gagal membaca config lama: %v", err)
    }

    // 2. Tambahkan server baru
    newServer := ServerConfig{
        Name:   name,
        Url:    url,
        ApiKey: apiKey,
    }
    currentConfig.Servers = append(currentConfig.Servers, newServer)

    // 3. Marshal kembali ke JSON
    data, err := json.MarshalIndent(currentConfig, "", "  ")
    if err != nil {
        return fmt.Errorf("gagal marshal config: %v", err)
    }

    // 4. Tulis ke file
    if err := os.WriteFile(BotConfigFile, data, 0644); err != nil {
        return fmt.Errorf("gagal menulis file config: %v", err)
    }

    log.Printf("✅ Server baru ditambahkan: %s (%s)", name, url)
    return nil
}

// --- FITUR BACKUP & RESTORE ---

func saveBackupToFile(server ServerConfig) (string, error) {
    log.Printf("=== [BACKUP] Memulai backup untuk server: %s ===", server.Name)

    if err := os.MkdirAll(BackupDir, 0755); err != nil {
        return "", fmt.Errorf("gagal membuat folder backup: %v", err)
    }

    users, err := getUsers(server)
    if err != nil {
        return "", fmt.Errorf("gagal ambil data user: %v", err)
    }

    if len(users) == 0 {
        return "", fmt.Errorf("tidak ada user untuk dibackup")
    }

    domain := "Unknown"
    if res, err := apiCall("GET", "/info", nil, server); err == nil && res["success"] == true {
        if data, ok := res["data"].(map[string]interface{}); ok {
            if d, ok := data["domain"].(string); ok {
                domain = d
            }
        }
    }

    for i := range users {
        users[i].Host = domain
    }

    // Nama file disesuaikan dengan nama server agar unik
    safeName := strings.ReplaceAll(server.Name, " ", "_")
    filename := fmt.Sprintf("backup_%s_users.json", safeName)
    fullPath := filepath.Join(BackupDir, filename)

    data, err := json.MarshalIndent(users, "", "  ")
    if err != nil {
        return "", fmt.Errorf("gagal marshal data: %v", err)
    }

    if err := os.WriteFile(fullPath, data, 0644); err != nil {
        return "", fmt.Errorf("GAGAL MENULIS FILE KE DISK: %v", err)
    }

    return fullPath, nil
}

func performAutoBackup(bot *tgbotapi.BotAPI, adminID int64, server ServerConfig) {
    filePath, err := saveBackupToFile(server)
    if err != nil {
        log.Printf("❌ [AutoBackup] Gagal Server %s: %v", server.Name, err)
        return
    }
    log.Printf("✅ [AutoBackup] Berhasil Server %s: %s", server.Name, filePath)
}

func performManualBackup(bot *tgbotapi.BotAPI, chatID int64, server ServerConfig) {
    log.Printf("=== [MANUAL BACKUP] Start for %s ===", server.Name)
    sendMessage(bot, chatID, fmt.Sprintf("⏳ Sedang memproses backup untuk server **%s**...", server.Name))

    filePath, err := saveBackupToFile(server)
    if err != nil {
        log.Printf("❌ [MANUAL BACKUP] Gagal: %v", err)
        sendMessage(bot, chatID, "❌ **GAGAL MEMBUAT FILE**\n\nServer Error:\n`"+err.Error()+"`")
        return
    }

    fileInfo, err := os.Stat(filePath)
    if os.IsNotExist(err) {
        sendMessage(bot, chatID, "❌ Error Aneh: File backup hilang setelah dibuat.")
        return
    }

    if fileInfo.Size() > (50 * 1024 * 1024) {
        sizeInMb := fileInfo.Size() / 1024 / 1024
        sendMessage(bot, chatID, fmt.Sprintf("❌ **GAGAL KIRIM**\n\nFile terlalu besar: **%d MB**.\nLimit Telegram: 50 MB.\n\nAmbil file manual di server:\n`%s`", sizeInMb, filePath))
        return
    }

    doc := tgbotapi.NewDocument(chatID, tgbotapi.FilePath(filePath))
    doc.Caption = fmt.Sprintf("💾 *Backup Data User*\n🖥️ Server: `%s`\n📁 Ukuran: %.2f MB",
        server.Name,
        float64(fileInfo.Size())/1024/1024)
    doc.ParseMode = "Markdown"

    deleteLastMessage(bot, chatID)

    _, err = bot.Send(doc)
    if err != nil {
        sendMessage(bot, chatID, fmt.Sprintf("❌ **GAGAL MENGIRIM KE TELEGRAM**\n\nError: %s\n\n**File tersimpan di server:**\n`%s`", err.Error(), filePath))
        return
    }
    log.Printf("✅ [MANUAL BACKUP] Sukses %s", server.Name)
}

func cleanAndRestartService(bot *tgbotapi.BotAPI, chatID int64, server ServerConfig) {
    sendMessage(bot, chatID, fmt.Sprintf("🧹 Membersihkan akun expired & Restart Service di server `%s`...", server.Name))
    go func() {
        autoDeleteExpiredUsers(bot, chatID, true, server)
    }()
}

func restartVpnService() error {
    cmd := exec.Command("systemctl", "restart", ServiceName)
    return cmd.Run()
}

// Catatan: Restart service hanya bisa dilakukan jika bot berjalan di mesin yang sama.
// Jika bot di VPS terpisah dengan panel VPN, fitur restart ini tidak akan berfungsi (hanya delete user yang jalan).
func autoDeleteExpiredUsers(bot *tgbotapi.BotAPI, adminID int64, shouldRestart bool, server ServerConfig) {
    users, err := getUsers(server)
    if err != nil {
        log.Printf("❌ [AutoDelete] Gagal ambil data user di %s: %v", server.Name, err)
        return
    }

    deletedCount := 0
    var deletedUsers []string

    for _, u := range users {
        if u.Status == "Expired" {
            res, err := apiCall("POST", "/user/delete", map[string]interface{}{
                "password": u.Password,
            }, server)

            if err != nil {
                continue
            }

            if res["success"] == true {
                deletedCount++
                deletedUsers = append(deletedUsers, u.Password)
                log.Printf("✅ [AutoDelete] User expired %s dihapus dari %s.", u.Password, server.Name)
            }
        }
    }

    if shouldRestart {
        if deletedCount > 0 {
            log.Printf("🔄 [Restart Service] Melakukan restart service %s...", ServiceName)
            // Cek apakah URL adalah localhost, jika bukan skip restart
            if !strings.Contains(server.Url, "127.0.0.1") && !strings.Contains(server.Url, "localhost") {
                if bot != nil {
                    bot.Send(tgbotapi.NewMessage(adminID, fmt.Sprintf("⚠️ Server `%s` adalah remote. Hapus %d akun sukses, tapi Service Restart dilewati (Bot tidak ada di server tersebut).", server.Name, deletedCount)))
                }
                return
            }

            if err := restartVpnService(); err != nil {
                log.Printf("❌ Gagal restart service: %v", err)
                if bot != nil {
                    bot.Send(tgbotapi.NewMessage(adminID, "❌ Gagal merestart service. Cek log server."))
                }
            } else {
                log.Printf("✅ Service %s berhasil di-restart.", ServiceName)
                if bot != nil {
                    bot.Send(tgbotapi.NewMessage(adminID, fmt.Sprintf("🔄 [%s] %d akun expired dihapus & Service di-restart.", server.Name, deletedCount)))
                }
            }
        } else {
            if bot != nil {
                bot.Send(tgbotapi.NewMessage(adminID, fmt.Sprintf("✅ [%s] Tidak ada akun expired.", server.Name)))
            }
        }
        return
    }

    if deletedCount > 0 {
        if bot != nil {
            msgText := fmt.Sprintf("🗑️ *PEMBERSIHAN AKUN OTOMATIS* [%s]\n\n"+
                "Total `%d` akun kedaluwarsa telah dihapus:\n- %s",
                server.Name, deletedCount, strings.Join(deletedUsers, "\n- "))
            notification := tgbotapi.NewMessage(adminID, msgText)
            notification.ParseMode = "Markdown"
            bot.Send(notification)
        }
    }
}

// --- API Calls ---

func apiCall(method, endpoint string, payload interface{}, server ServerConfig) (map[string]interface{}, error) {
    var reqBody []byte
    var err error

    if payload != nil {
        reqBody, err = json.Marshal(payload)
        if err != nil {
            return nil, err
        }
    }

    client := &http.Client{Timeout: 10 * time.Second}

    // Gunakan URL dari config server yang dipilih
    req, err := http.NewRequest(method, server.Url+endpoint, bytes.NewBuffer(reqBody))
    if err != nil {
        return nil, err
    }

    req.Header.Set("Content-Type", "application/json")
    // Gunakan ApiKey dari config server yang dipilih
    req.Header.Set("X-API-Key", server.ApiKey)

    resp, err := client.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("API returned status: %d", resp.StatusCode)
    }

    body, _ := io.ReadAll(resp.Body)
    var result map[string]interface{}
    if err := json.Unmarshal(body, &result); err != nil {
        return nil, fmt.Errorf("failed to decode API response: %v", err)
    }

    return result, nil
}

func getIpInfo() (IpInfo, error) {
    resp, err := http.Get("http://ip-api.com/json/")
    if err != nil {
        return IpInfo{}, err
    }
    defer resp.Body.Close()

    var info IpInfo
    if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
        return IpInfo{}, err
    }
    return info, nil
}

func getUsers(server ServerConfig) ([]UserData, error) {
    res, err := apiCall("GET", "/users", nil, server)
    if err != nil {
        return nil, err
    }

    if res["success"] != true {
        return nil, fmt.Errorf("API success is false")
    }

    var users []UserData
    if dataSlice, ok := res["data"].([]interface{}); ok {
        dataBytes, err := json.Marshal(dataSlice)
        if err != nil {
            return nil, fmt.Errorf("gagal marshal data: %v", err)
        }
        if err := json.Unmarshal(dataBytes, &users); err != nil {
            return nil, fmt.Errorf("gagal unmarshal data ke UserData: %v", err)
        }
    } else {
        return []UserData{}, nil
    }

    return users, nil
}

func createUser(bot *tgbotapi.BotAPI, chatID int64, username string, days int, server ServerConfig) {
    res, err := apiCall("POST", "/user/create", map[string]interface{}{
        "password": username,
        "days":     days,
    }, server)

    if err != nil {
        sendMessage(bot, chatID, "❌ Error API: "+err.Error())
        return
    }

    if res["success"] == true {
        data, ok := res["data"].(map[string]interface{})
        if !ok {
            sendMessage(bot, chatID, "❌ Gagal: Format data respons dari API tidak valid.")
            return
        }

        ipInfo, _ := getIpInfo()

        msg := fmt.Sprintf("🎉 *AKUN BERHASIL DIBUAT*\n"+
            "━━━━━━━━━━━━━━━━━━━━━━━━━\n"+
            "🖥️ *Server*: `%s`\n"+
            "🔑 *Password*: `%s`\n"+
            "🌐 *Domain*: `%s`\n"+
            "🗓️ *Kadaluarsa*: `%s`\n"+
            "📍 *Lokasi Server*: `%s`\n"+
            "📡 *ISP Server*: `%s`\n"+
            "━━━━━━━━━━━━━━━━━━━━━━━━━\n"+
            "🔒 *Private Tidak Digunakan User Lain*\n"+
            "⚡ *Full Speed Anti Lemot Stabil 24 Jam*\n"+
            "━━━━━━━━━━━━━━━━━━━━━━━━━",
            server.Name, data["password"], data["domain"], data["expired"], ipInfo.City, ipInfo.Isp)

        reply := tgbotapi.NewMessage(chatID, msg)
        reply.ParseMode = "Markdown"
        deleteLastMessage(bot, chatID)
        bot.Send(reply)
        // Reload config untuk ambil state terbaru server list
        config, _ := loadConfig()
        showMainMenu(bot, chatID, config)
    } else {
        errMsg, ok := res["message"].(string)
        if !ok {
            errMsg = "Pesan error tidak diketahui dari API."
        }
        sendMessage(bot, chatID, fmt.Sprintf("❌ Gagal: %s", errMsg))
        config, _ := loadConfig()
        showMainMenu(bot, chatID, config)
    }
}

func createGenericTrialUser(bot *tgbotapi.BotAPI, chatID int64, days int, server ServerConfig) {
    trialPassword := generateRandomPassword(8)

    res, err := apiCall("POST", "/user/create", map[string]interface{}{
        "password": trialPassword,
        "minutes":  0,
        "days":     days,
    }, server)

    if err != nil {
        sendMessage(bot, chatID, "❌ Error Komunikasi API: "+err.Error())
        return
    }

    if res["success"] == true {
        data, ok := res["data"].(map[string]interface{})
        if !ok {
            sendMessage(bot, chatID, "❌ Gagal: Format data respons dari API tidak valid.")
            config, _ := loadConfig()
            showMainMenu(bot, chatID, config)
            return
        }

        ipInfo, _ := getIpInfo()

        password := "N/A"
        if p, ok := data["password"].(string); ok {
            password = p
        }

        expired := "N/A"
        if e, ok := data["expired"].(string); ok {
            expired = e
        }

        domain := "Unknown"
        if d, ok := data["domain"].(string); ok && d != "" {
            domain = d
        }

        msg := fmt.Sprintf("🚀 *AKUN %d HARI BERHASIL DIBUAT*\n"+
            "━━━━━━━━━━━━━━━━━━━━━━━━━\n"+
            "🖥️ *Server*: `%s`\n"+
            "🔑 *Password*: `%s`\n"+
            "🌐 *Domain*: `%s`\n"+
            "⏳ *Aktip selama*: `%d Hari`\n"+
            "🗓️ *Kadaluarsa*: `%s`\n"+
            "📍 *Lokasi Server*: `%s`\n"+
            "📡 *ISP Server*: `%s`\n"+
            "━━━━━━━━━━━━━━━━━━━━━━━━━\n"+
            "🔒 *Private Tidak Digunakan User Lain*\n"+
            "⚡ *Full Speed Anti Lemot Stabil 24 Jam*\n"+
            "❗️ *Akun ini aktif selama %d hari 2 hp*\n"+
            "━━━━━━━━━━━━━━━━━━━━━━━━━━",
            days, server.Name, password, domain, days, expired, ipInfo.City, ipInfo.Isp, days)

        reply := tgbotapi.NewMessage(chatID, msg)
        reply.ParseMode = "Markdown"
        deleteLastMessage(bot, chatID)
        bot.Send(reply)
        config, _ := loadConfig()
        showMainMenu(bot, chatID, config)
    } else {
        errMsg, ok := res["message"].(string)
        if !ok {
            errMsg = "Respon kegagalan dari API tidak diketahui."
        }
        sendMessage(bot, chatID, fmt.Sprintf("❌ Gagal membuat Trial: %s", errMsg))
        config, _ := loadConfig()
        showMainMenu(bot, chatID, config)
    }
}

func deleteUser(bot *tgbotapi.BotAPI, chatID int64, username string, server ServerConfig) {
    res, err := apiCall("POST", "/user/delete", map[string]interface{}{
        "password": username,
    }, server)

    if err != nil {
        sendMessage(bot, chatID, "❌ Error API: "+err.Error())
        return
    }

    if res["success"] == true {
        msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("✅ Password `%s` berhasil *DIHAPUS* dari server `%s`.", username, server.Name))
        msg.ParseMode = "Markdown"
        deleteLastMessage(bot, chatID)
        bot.Send(msg)
        config, _ := loadConfig()
        showMainMenu(bot, chatID, config)
    } else {
        errMsg, ok := res["message"].(string)
        if !ok {
            errMsg = "Pesan error tidak diketahui dari API."
        }
        sendMessage(bot, chatID, fmt.Sprintf("❌ Gagal menghapus: %s", errMsg))
        config, _ := loadConfig()
        showMainMenu(bot, chatID, config)
    }
}

func renewUser(bot *tgbotapi.BotAPI, chatID int64, username string, days int, server ServerConfig) {
    res, err := apiCall("POST", "/user/renew", map[string]interface{}{
        "password": username,
        "days":     days,
    }, server)

    if err != nil {
        sendMessage(bot, chatID, "❌ Error API: "+err.Error())
        return
    }

    if res["success"] == true {
        data, ok := res["data"].(map[string]interface{})
        if !ok {
            sendMessage(bot, chatID, "❌ Gagal: Format data respons dari API tidak valid.")
            return
        }

        ipInfo, _ := getIpInfo()

        domain := "Unknown"
        if d, ok := data["domain"].(string); ok && d != "" {
            domain = d
        }

        msg := fmt.Sprintf("✅ *AKUN BERHASIL DIPERPANJANG* (%d Hari)\n"+
            "━━━━━━━━━━━━━━━━━━━━━━━━━\n"+
            "🖥️ *Server*: `%s`\n"+
            "🔑 *Password*: `%s`\n"+
            "🌐 *Domain*: `%s`\n"+
            "🗓️ *Kadaluarsa Baru*: `%s`\n"+
            "📍 *Lokasi Server*: `%s`\n"+
            "📡 *ISP Server*: `%s`\n"+
            "━━━━━━━━━━━━━━━━━━━━━━━━━",
            days, server.Name, data["password"], domain, data["expired"], ipInfo.City, ipInfo.Isp)

        reply := tgbotapi.NewMessage(chatID, msg)
        reply.ParseMode = "Markdown"
        deleteLastMessage(bot, chatID)
        bot.Send(reply)
        config, _ := loadConfig()
        showMainMenu(bot, chatID, config)
    } else {
        errMsg, ok := res["message"].(string)
        if !ok {
            errMsg = "Pesan error tidak diketahui dari API."
        }
        sendMessage(bot, chatID, fmt.Sprintf("❌ Gagal memperpanjang: %s", errMsg))
        config, _ := loadConfig()
        showMainMenu(bot, chatID, config)
    }
}

func listUsers(bot *tgbotapi.BotAPI, chatID int64, server ServerConfig) {
    res, err := apiCall("GET", "/users", nil, server)
    if err != nil {
        sendMessage(bot, chatID, "❌ Error API: "+err.Error())
        return
    }

    if res["success"] == true {
        users, ok := res["data"].([]interface{})
        if !ok {
            sendMessage(bot, chatID, "❌ Format data user salah.")
            return
        }

        if len(users) == 0 {
            sendMessage(bot, chatID, fmt.Sprintf("📂 Tidak ada user saat ini di server `%s`.", server.Name))
            return
        }

        msg := fmt.Sprintf("📋 *DAFTAR AKUN ZIVPN - %s* (Total: %d)\n\n", server.Name, len(users))
        for i, u := range users {
            user, ok := u.(map[string]interface{})
            if !ok {
                continue
            }

            statusIcon := "🟢"
            if user["status"] == "Expired" {
                statusIcon = "🔴"
            }
            msg += fmt.Sprintf("%d. %s `%s`\n    _Kadaluarsa: %s_\n", i+1, statusIcon, user["password"], user["expired"])
        }

        reply := tgbotapi.NewMessage(chatID, msg)
        reply.ParseMode = "Markdown"
        sendAndTrack(bot, reply)
    } else {
        sendMessage(bot, chatID, "❌ Gagal mengambil data daftar akun.")
    }
}

func systemInfo(bot *tgbotapi.BotAPI, chatID int64, server ServerConfig) {
    res, err := apiCall("GET", "/info", nil, server)
    if err != nil {
        sendMessage(bot, chatID, "❌ Error API: "+err.Error())
        return
    }

    if res["success"] == true {
        data, ok := res["data"].(map[string]interface{})
        if !ok {
            sendMessage(bot, chatID, "❌ Format data salah.")
            return
        }

        ipInfo, _ := getIpInfo()

        msg := fmt.Sprintf("⚙️ *INFORMASI DETAIL SERVER*\n"+
            "🖥️ *Node*: `%s`\n"+
            "━━━━━━━━━━━━━━━━━━━━━━━━━\n"+
            "🌐 *Domain*: `%s`\n"+
            "🖥️ *IP Public*: `%s`\n"+
            "🔌 *Port*: `%s`\n"+
            "🔧 *Layanan*: `%s`\n"+
            "📍 *Lokasi Server*: `%s`\n"+
            "📡 *ISP Server*: `%s`\n"+
            "━━━━━━━━━━━━━━━━━━━━━━━━━",
            server.Name, data["domain"], data["public_ip"], data["port"], data["service"], ipInfo.City, ipInfo.Isp)

        reply := tgbotapi.NewMessage(chatID, msg)
        reply.ParseMode = "Markdown"
        deleteLastMessage(bot, chatID)
        bot.Send(reply)
        config, _ := loadConfig()
        showMainMenu(bot, chatID, config)
    } else {
        sendMessage(bot, chatID, "❌ Gagal mengambil info sistem.")
    }
}

func loadConfig() (BotConfig, error) {
    var config BotConfig
    file, err := os.ReadFile(BotConfigFile)
    if err != nil {
        return config, err
    }
    err = json.Unmarshal(file, &config)
    return config, err
}