package main

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "math/rand"
    "net/http"
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
    ApiUrl        = "http://127.0.0.1:8080/api"
    ApiKeyFile    = "/etc/zivpn/apikey"
    // !!! GANTI INI DENGAN URL GAMBAR MENU ANDA !!!
    MenuPhotoURL = "https://h.uguu.se/LfWhbfvw.png"

    // Interval untuk pengecekan dan penghapusan akun expired
    AutoDeleteInterval = 1 * time.Minute
    // Interval untuk Auto Backup (3 jam)
    AutoBackupInterval = 3 * time.Hour

    // Konfigurasi Backup dan Service
    BackupDir   = "/etc/zivpn/backups"
    ServiceName = "zivpn"
)

var ApiKey = "AutoFtBot-agskjgdvsbdreiWG1234512SDKrqw"

type BotConfig struct {
    BotToken string `json:"bot_token"`
    AdminID  int64  `json:"admin_id"`
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
    stateMutex     sync.RWMutex
    userStates     = make(map[int64]string)
    tempUserData   = make(map[int64]map[string]string)
    lastMessageIDs = make(map[int64]int)
)

func main() {
    rand.Seed(time.Now().UnixNano())

    if err := os.MkdirAll(BackupDir, 0755); err != nil {
        log.Printf("Gagal membuat direktori backup: %v", err)
    }

    if keyBytes, err := os.ReadFile(ApiKeyFile); err == nil {
        ApiKey = strings.TrimSpace(string(keyBytes))
    }
    config, err := loadConfig()
    if err != nil {
        log.Fatal("Gagal memuat konfigurasi bot:", err)
    }

    bot, err := tgbotapi.NewBotAPI(config.BotToken)
    if err != nil {
        log.Panic(err)
    }

    bot.Debug = false
    log.Printf("Authorized on account %s", bot.Self.UserName)

    // --- BACKGROUND WORKER (PENGHAPUSAN OTOMATIS) ---
    go func() {
        autoDeleteExpiredUsers(bot, config.AdminID, false)
        ticker := time.NewTicker(AutoDeleteInterval)
        for range ticker.C {
            autoDeleteExpiredUsers(bot, config.AdminID, false)
        }
    }()

    // --- BACKGROUND WORKER (AUTO BACKUP) ---
    go func() {
        performAutoBackup(bot, config.AdminID)
        ticker := time.NewTicker(AutoBackupInterval)
        for range ticker.C {
            performAutoBackup(bot, config.AdminID)
        }
    }()

    u := tgbotapi.NewUpdate(0)
    u.Timeout = 60

    updates := bot.GetUpdatesChan(u)

    for update := range updates {
        if update.Message != nil {
            handleMessage(bot, update.Message, config.AdminID)
        } else if update.CallbackQuery != nil {
            handleCallback(bot, update.CallbackQuery, config.AdminID)
        }
    }
}

// --- FUNGSI HANDLE MESSAGE DIPERBARUI (MENANGANI /PANEL) ---
func handleMessage(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, adminID int64) {
    if msg.From.ID != adminID {
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
            handleRestoreFromUpload(bot, msg)
        } else {
            sendMessage(bot, msg.Chat.ID, "❌ Mohon kirimkan file backup (.json).")
        }
        return
    }

    if exists {
        handleState(bot, msg, state)
        return
    }

    // Normalisasi teks menjadi huruf kecil
    text := strings.ToLower(msg.Text)

    if msg.IsCommand() {
        switch msg.Command() {
        case "start", "panel", "menu": // Menambahkan command /panel dan /menu
            showMainMenu(bot, msg.Chat.ID)
        default:
            reply := tgbotapi.NewMessage(msg.Chat.ID, "Perintah tidak dikenal. Ketik /panel untuk membuka menu.")
            sendAndTrack(bot, reply)
        }
    } else {
        // Jika tidak dalam state (idle) dan mengetik "panel" atau "menu"
        if text == "panel" || text == "menu" || text == "pull panel" {
            showMainMenu(bot, msg.Chat.ID)
        } else {
            // Pesan default jika bot idle dan menerima teks lain
            reply := tgbotapi.NewMessage(msg.Chat.ID, "⚠️ Sistem Siaga.\nKetik /panel atau ketik 'menu' untuk menampilkan panel utama.")
            sendAndTrack(bot, reply)
        }
    }
}

func handleCallback(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery, adminID int64) {
    if query.From.ID != adminID {
        bot.Request(tgbotapi.NewCallback(query.ID, "Akses Ditolak"))
        return
    }

    callbackData := query.Data

    switch {
    case callbackData == "menu_trial":
        // Fitur Trial 1 Hari
        randomPass := generateRandomPassword(4)
        // Format: createUser(bot, chatID, username, days, limitIP, limitQuota)
        sendMessage(bot, query.Message.Chat.ID, "⏳ Sedang membuat akun trial...")
        createUser(bot, query.Message.Chat.ID, randomPass, 1, 1, 1)
        
    case callbackData == "menu_create":
        setState(query.From.ID, "create_username")
        setTempData(query.From.ID, make(map[string]string))
        sendMessage(bot, query.Message.Chat.ID, "🔑 *MENU CREATE*\nSilakan masukkan **PASSWORD** yang diinginkan:")
    case callbackData == "menu_delete":
        showUserSelection(bot, query.Message.Chat.ID, 1, "delete")
    case callbackData == "menu_renew":
        showUserSelection(bot, query.Message.Chat.ID, 1, "renew")
    case callbackData == "menu_list":
        listUsers(bot, query.Message.Chat.ID)
    case callbackData == "menu_info":
        systemInfo(bot, query.Message.Chat.ID)

    case callbackData == "menu_backup":
        performManualBackup(bot, query.Message.Chat.ID)
    case callbackData == "menu_restore":
        setState(query.From.ID, "wait_restore_file")
        msg := tgbotapi.NewMessage(query.Message.Chat.ID, "📥 *RESTORE DATA*\n\nSilakan kirimkan file backup `.json` Anda.\n\nBot akan otomatis membuat ulang akun yang ada di dalam file tersebut.")
        msg.ParseMode = "Markdown"
        msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
            tgbotapi.NewInlineKeyboardRow(
                tgbotapi.NewInlineKeyboardButtonData("❌ Batal", "cancel"),
            ),
        )
        sendAndTrack(bot, msg)
    case callbackData == "menu_clean_restart":
        cleanAndRestartService(bot, query.Message.Chat.ID)

    case callbackData == "cancel":
        resetState(query.From.ID)
        showMainMenu(bot, query.Message.Chat.ID)
    case strings.HasPrefix(callbackData, "page_"):
        parts := strings.Split(callbackData, ":")
        action := parts[0][5:]
        page, _ := strconv.Atoi(parts[1])
        showUserSelection(bot, query.Message.Chat.ID, page, action)
    case strings.HasPrefix(callbackData, "select_renew:"):
        username := strings.TrimPrefix(callbackData, "select_renew:")
        setTempData(query.From.ID, map[string]string{"username": username})
        setState(query.From.ID, "renew_limit_ip")
        sendMessage(bot, query.Message.Chat.ID, fmt.Sprintf("🔄 *MENU RENEW*\nUser: `%s`\n\nMasukkan **Limit IP** (0 untuk Unlimited):", username))
    case strings.HasPrefix(callbackData, "select_delete:"):
        username := strings.TrimPrefix(callbackData, "select_delete:")
        msg := tgbotapi.NewMessage(query.Message.Chat.ID, fmt.Sprintf("❓ *KONFIRMASI HAPUS*\nAnda yakin ingin menghapus user `%s`?", username))
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
        deleteUser(bot, query.Message.Chat.ID, username)
    }

    bot.Request(tgbotapi.NewCallback(query.ID, ""))
}

// --- FUNGSI STATE DIPERBAIKI (FIXED RACE CONDITION) ---
func handleState(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, state string) {
    userID := msg.From.ID
    text := strings.TrimSpace(msg.Text)

    switch state {
    case "create_username":
        stateMutex.Lock()
        tempUserData[userID] = map[string]string{"username": text}
        stateMutex.Unlock()
        
        setState(userID, "create_limit_ip")
        sendMessage(bot, msg.Chat.ID, fmt.Sprintf("🔑 *CREATE USER*\nPassword: `%s`\n\nMasukkan **Limit IP** (0 untuk Unlimited):", text))

    case "create_limit_ip":
        if _, err := strconv.Atoi(text); err != nil {
            sendMessage(bot, msg.Chat.ID, "❌ Limit IP harus berupa angka. Coba lagi:")
            return
        }
        
        stateMutex.Lock()
        data, ok := tempUserData[userID]
        if !ok {
            stateMutex.Unlock()
            sendMessage(bot, msg.Chat.ID, "❌ Sesi berakhir. Silakan mulai dari awal.")
            resetState(userID)
            return
        }
        data["limit_ip"] = text
        stateMutex.Unlock()
        
        setState(userID, "create_limit_quota")
        sendMessage(bot, msg.Chat.ID, "💾 *CREATE USER*\n\nMasukkan **Limit Kuota** dalam GB (0 untuk Unlimited):")

    case "create_limit_quota":
        if _, err := strconv.Atoi(text); err != nil {
            sendMessage(bot, msg.Chat.ID, "❌ Limit Kuota harus berupa angka. Coba lagi:")
            return
        }
        
        stateMutex.Lock()
        data, ok := tempUserData[userID]
        if !ok {
            stateMutex.Unlock()
            sendMessage(bot, msg.Chat.ID, "❌ Sesi berakhir. Silakan mulai dari awal.")
            resetState(userID)
            return
        }
        data["limit_quota"] = text
        stateMutex.Unlock()
        
        setState(userID, "create_days")
        sendMessage(bot, msg.Chat.ID, "📅 *CREATE USER*\n\nMasukkan **Durasi** (*Hari*) pembuatan:")

    case "create_days":
        days, err := strconv.Atoi(text)
        if err != nil {
            sendMessage(bot, msg.Chat.ID, "❌ Durasi harus angka. Coba lagi:")
            return
        }
        
        stateMutex.Lock()
        data, ok := tempUserData[userID]
        if !ok {
            stateMutex.Unlock()
            sendMessage(bot, msg.Chat.ID, "❌ Sesi berakhir. Silakan mulai dari awal.")
            resetState(userID)
            return
        }
        limitIP, _ := strconv.Atoi(data["limit_ip"])
        limitQuota, _ := strconv.Atoi(data["limit_quota"])
        username := data["username"]
        stateMutex.Unlock()

        createUser(bot, msg.Chat.ID, username, days, limitIP, limitQuota)
        resetState(userID)

    case "renew_limit_ip":
        if _, err := strconv.Atoi(text); err != nil {
            sendMessage(bot, msg.Chat.ID, "❌ Limit IP harus berupa angka. Coba lagi:")
            return
        }
        
        stateMutex.Lock()
        data, ok := tempUserData[userID]
        if !ok {
            stateMutex.Unlock()
            sendMessage(bot, msg.Chat.ID, "❌ Sesi berakhir. Silakan mulai dari awal.")
            resetState(userID)
            return
        }
        data["limit_ip"] = text
        stateMutex.Unlock()
        
        setState(userID, "renew_limit_quota")
        sendMessage(bot, msg.Chat.ID, "💾 *MENU RENEW*\n\nMasukkan **Limit Kuota** dalam GB (0 untuk Unlimited):")

    case "renew_limit_quota":
        if _, err := strconv.Atoi(text); err != nil {
            sendMessage(bot, msg.Chat.ID, "❌ Limit Kuota harus berupa angka. Coba lagi:")
            return
        }
        
        stateMutex.Lock()
        data, ok := tempUserData[userID]
        if !ok {
            stateMutex.Unlock()
            sendMessage(bot, msg.Chat.ID, "❌ Sesi berakhir. Silakan mulai dari awal.")
            resetState(userID)
            return
        }
        data["limit_quota"] = text
        stateMutex.Unlock()
        
        setState(userID, "renew_days")
        sendMessage(bot, msg.Chat.ID, "📅 *MENU RENEW*\n\nMasukkan tambahan **Durasi** (*Hari*):")

    case "renew_days":
        days, err := strconv.Atoi(text)
        if err != nil {
            sendMessage(bot, msg.Chat.ID, "❌ Durasi harus angka. Coba lagi:")
            return
        }

        stateMutex.Lock()
        data, ok := tempUserData[userID]
        if !ok {
            stateMutex.Unlock()
            sendMessage(bot, msg.Chat.ID, "❌ Sesi berakhir. Silakan mulai dari awal.")
            resetState(userID)
            return
        }
        limitIP, _ := strconv.Atoi(data["limit_ip"])
        limitQuota, _ := strconv.Atoi(data["limit_quota"])
        username := data["username"]
        stateMutex.Unlock()

        renewUser(bot, msg.Chat.ID, username, days, limitIP, limitQuota)
        resetState(userID)
    }
}

func getTempData(userID int64) (map[string]string, bool) {
    stateMutex.RLock()
    defer stateMutex.RUnlock()
    data, ok := tempUserData[userID]
    return data, ok
}

func handleRestoreFromUpload(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
    resetState(msg.From.ID)
    sendMessage(bot, msg.Chat.ID, "⏳ Sedang mengunduh dan memproses file backup...")

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
        showMainMenu(bot, msg.Chat.ID)
        return
    }

    sendMessage(bot, msg.Chat.ID, fmt.Sprintf("⏳ Memproses %d user...\nMohon tunggu sebentar.", len(backupUsers)))

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

        res, err := apiCall("POST", "/user/create", map[string]interface{}{
            "password": u.Password,
            "days":     days,
        })

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

    msgResult := fmt.Sprintf("✅ *Restore Selesai*\n\n"+
        "📂 Total Data: %d\n"+
        "✅ Berhasil dibuat: %d\n"+
        "⚠️ Dilewati (Sudah ada/Expired): %d\n",
        len(backupUsers), successCount, skippedCount)

    if failedCount > 0 {
        msgResult += fmt.Sprintf("❌ Gagal (%d): %s", failedCount, strings.Join(failedUsers, ", "))
    }

    sendMessage(bot, msg.Chat.ID, msgResult)
    showMainMenu(bot, msg.Chat.ID)
}

func showUserSelection(bot *tgbotapi.BotAPI, chatID int64, page int, action string) {
    users, err := getUsers()
    if err != nil {
        sendMessage(bot, chatID, "❌ Gagal mengambil data user.")
        return
    }

    if len(users) == 0 {
        sendMessage(bot, chatID, "📂 Tidak ada user saat ini.")
        showMainMenu(bot, chatID)
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
        navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData("⬅️ Halaman Sebelumnya", fmt.Sprintf("page_%s:%d", action, page-1)))
    }
    if page < totalPages {
        navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData("Halaman Selanjutnya ➡️", fmt.Sprintf("page_%s:%d", action, page+1)))
    }
    if len(navRow) > 0 {
        rows = append(rows, navRow)
    }

    rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("⬅️ Kembali ke Menu Utama", "cancel")))

    title := ""
    switch action {
    case "delete":
        title = "🗑️ HAPUS AKUN"
    case "renew":
        title = "🔄 PERPANJANG AKUN"
    }

    msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("*%s*\nPilih user dari daftar di bawah (Halaman %d dari %d):", title, page, totalPages))
    msg.ParseMode = "Markdown"
    msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
    sendAndTrack(bot, msg)
}

func showMainMenu(bot *tgbotapi.BotAPI, chatID int64) {
    ipInfo, _ := getIpInfo()
    domain := "Unknown"

    if res, err := apiCall("GET", "/info", nil); err == nil && res["success"] == true {
        if data, ok := res["data"].(map[string]interface{}); ok {
            if d, ok := data["domain"].(string); ok {
                domain = d
            }
        }
    }

    totalUsers := 0
    if users, err := getUsers(); err == nil {
        totalUsers = len(users)
    }

    msgText := fmt.Sprintf("✨ *WELCOME TO BOT PGETUNNEL UDP ZIVPN*\n\n"+
        "Server Info:\n"+
        "•  🌐 *Domain*: `%s`\n"+
        "•  📍 *Lokasi*: `%s`\n"+
        "•  📡 *ISP*: `%s`\n"+
        "•  👤 *Total Akun*: `%d`\n\n"+
        "Untuk bantuan, hubungi Admin: @JesVpnt\n\n"+
        "Silakan pilih menu di bawah ini:",
        domain, ipInfo.City, ipInfo.Isp, totalUsers)

    deleteLastMessage(bot, chatID)

    keyboard := tgbotapi.NewInlineKeyboardMarkup(
        // Baris 1: Trial & Create
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("🎁 Trial 1 Hari", "menu_trial"),
            tgbotapi.NewInlineKeyboardButtonData("➕ Buat Akun", "menu_create"),
        ),
        // Baris 2: Renew & Delete
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("🔄 Renew Akun", "menu_renew"),
            tgbotapi.NewInlineKeyboardButtonData("🗑️ Hapus Akun", "menu_delete"),
        ),
        // Baris 3: List & Info
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("📋 Daftar Akun", "menu_list"),
            tgbotapi.NewInlineKeyboardButtonData("📊 Info Server", "menu_info"),
        ),
        // Baris 4: Backup & Restore
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("💾 Backup Data", "menu_backup"),
            tgbotapi.NewInlineKeyboardButtonData("♻️ Restore Data", "menu_restore"),
        ),
        // Baris 5: Clean
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("🧹 Hapus Expired & Restart", "menu_clean_restart"),
        ),
    )

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

func generateRandomPassword(length int) string {
    const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
    b := make([]byte, length)
    for i := range b {
        b[i] = charset[rand.Intn(len(charset))]
    }
    return string(b)
}

func saveBackupToFile() (string, error) {
    log.Println("=== [DEBUG 1] Memulai saveBackupToFile ===")

    if err := os.MkdirAll(BackupDir, 0755); err != nil {
        log.Printf("❌ [DEBUG 2] Gagal membuat folder %s: %v", BackupDir, err)
        return "", fmt.Errorf("gagal membuat folder backup: %v", err)
    }
    log.Printf("✅ [DEBUG 3] Folder %s siap/ditemukan.", BackupDir)

    users, err := getUsers()
    if err != nil {
        log.Printf("❌ [DEBUG 4] Gagal getUsers: %v", err)
        return "", fmt.Errorf("gagal ambil data user: %v", err)
    }

    if len(users) == 0 {
        log.Println("⚠️ [DEBUG 5] Data user kosong.")
        return "", fmt.Errorf("tidak ada user untuk dibackup")
    }
    log.Printf("✅ [DEBUG 6] Berhasil ambil %d user.", len(users))

    domain := "Unknown"
    if res, err := apiCall("GET", "/info", nil); err == nil && res["success"] == true {
        if data, ok := res["data"].(map[string]interface{}); ok {
            if d, ok := data["domain"].(string); ok {
                domain = d
            }
        }
    }

    for i := range users {
        users[i].Host = domain
    }

    filename := "backup_users.json"
    fullPath := filepath.Join(BackupDir, filename)

    log.Printf("✅ [DEBUG 7] Path tujuan file: %s", fullPath)

    data, err := json.MarshalIndent(users, "", "  ")
    if err != nil {
        log.Printf("❌ [DEBUG 8] Gagal marshal JSON: %v", err)
        return "", fmt.Errorf("gagal marshal data: %v", err)
    }

    if err := os.WriteFile(fullPath, data, 0644); err != nil {
        log.Printf("❌ [DEBUG 9] GAGAL WRITE FILE (Permission?): %v", err)
        return "", fmt.Errorf("GAGAL MENULIS FILE KE DISK: %v\nPastikan bot memiliki akses tulis ke folder: %s", err, BackupDir)
    }

    if _, err := os.Stat(fullPath); err != nil {
        log.Printf("❌ [DEBUG 10] File tidak ditemukan setelah write: %v", err)
        return "", fmt.Errorf("file tidak ditemukan setelah write: %v", err)
    }

    absPath, err := filepath.Abs(fullPath)
    if err != nil {
        return fullPath, nil
    }

    log.Printf("✅ [DEBUG 11] Berhasil membuat file di: %s", absPath)
    return absPath, nil
}

func performAutoBackup(bot *tgbotapi.BotAPI, adminID int64) {
    log.Println("🔄 [AutoBackup] Memulai proses backup otomatis...")

    filePath, err := saveBackupToFile()
    if err != nil {
        log.Printf("❌ [AutoBackup] Gagal menyimpan file ke disk: %v", err)
        return
    }

    fileInfo, err := os.Stat(filePath)
    if err != nil {
        log.Printf("❌ [AutoBackup] Gagal membaca file info: %v", err)
        return
    }

    if fileInfo.Size() > (50 * 1024 * 1024) {
        sizeInMb := fileInfo.Size() / 1024 / 1024
        log.Printf("⚠️ [AutoBackup] File terlalu besar (%d MB), melebihi limit Telegram.", sizeInMb)
        
        msg := tgbotapi.NewMessage(adminID, fmt.Sprintf("⚠️ *Auto Backup Gagal Terkirim*\n\nFile backup terlalu besar: **%d MB**.\nLimit Telegram: 50 MB.\n\nFile tersimpan di server:\n`%s`", sizeInMb, filePath))
        msg.ParseMode = "Markdown"
        bot.Send(msg)
        return
    }

    doc := tgbotapi.NewDocument(adminID, tgbotapi.FilePath(filePath))
    doc.Caption = fmt.Sprintf("💾 *AUTO BACKUP REPORT*\n📅 Waktu: `%s`\n📁 Ukuran: %.2f MB\n📂 Lokasi: `%s`",
        time.Now().Format("2006-01-02 15:04:05"),
        float64(fileInfo.Size())/1024/1024,
        filePath)
    doc.ParseMode = "Markdown"

    _, err = bot.Send(doc)
    if err != nil {
        log.Printf("❌ [AutoBackup] Gagal mengirim file ke Telegram: %v", err)
    } else {
        log.Printf("✅ [AutoBackup] Berhasil dikirim ke Admin Telegram.")
    }
}

func performManualBackup(bot *tgbotapi.BotAPI, chatID int64) {
    log.Println("=== [DEBUG START] Perintah Backup Manual Diterima ===")
    sendMessage(bot, chatID, "⏳ Sedang memproses backup...")

    filePath, err := saveBackupToFile()
    if err != nil {
        log.Printf("❌ [DEBUG END] Gagal di saveBackupToFile: %v", err)
        sendMessage(bot, chatID, "❌ **GAGAL MEMBUAT FILE**\n\nServer Error:\n`"+err.Error()+"`\n\n*Cek log terminal bot untuk detail lengkap.*")
        return
    }

    fileInfo, err := os.Stat(filePath)
    if os.IsNotExist(err) {
        log.Printf("❌ [DEBUG] File hilang setelah dibuat: %s", filePath)
        sendMessage(bot, chatID, "❌ Error Aneh: File backup hilang setelah dibuat.")
        return
    }

    log.Printf("✅ [DEBUG] File Info - Path: %s, Size: %d bytes", filePath, fileInfo.Size())

    if fileInfo.Size() > (50 * 1024 * 1024) {
        sizeInMb := fileInfo.Size() / 1024 / 1024
        sendMessage(bot, chatID, fmt.Sprintf("❌ **GAGAL KIRIM**\n\nFile terlalu besar: **%d MB**.\nLimit Telegram: 50 MB.\n\nAmbil file manual di server:\n`%s`", sizeInMb, filePath))
        showMainMenu(bot, chatID)
        return
    }

    log.Println("✅ [DEBUG] Mencoba mengirim file ke Telegram...")

    doc := tgbotapi.NewDocument(chatID, tgbotapi.FilePath(filePath))
    doc.Caption = fmt.Sprintf("💾 *Backup Data User*\n📁 Ukuran: %.2f MB\n📂 Lokasi: `%s`",
        float64(fileInfo.Size())/1024/1024,
        filePath)
    doc.ParseMode = "Markdown"

    deleteLastMessage(bot, chatID)

    _, err = bot.Send(doc)
    if err != nil {
        log.Printf("❌ [DEBUG END] GAGAL KIRIM KE TELEGRAM: %v", err)

        errorDetail := err.Error()
        if strings.Contains(errorDetail, "file not found") {
            errorDetail = "Bot tidak bisa membaca file tersebut (Permission Denied / Path Salah)."
        } else if strings.Contains(errorDetail, "wrong file identifier") {
            errorDetail = "Format file salah atau korup."
        }

        sendMessage(bot, chatID, fmt.Sprintf("❌ **GAGAL MENGIRIM KE TELEGRAM**\n\nError: %s\n\n**File tersimpan di server:**\n`%s`\n\nSilakan ambil via SSH jika perlu.", errorDetail, filePath))
        showMainMenu(bot, chatID)
        return
    }

    log.Println("✅ [DEBUG END] Backup sukses terkirim!")
    showMainMenu(bot, chatID)
}

func cleanAndRestartService(bot *tgbotapi.BotAPI, chatID int64) {
    sendMessage(bot, chatID, "🧹 Membersihkan akun expired & Restart Service...")

    go func() {
        autoDeleteExpiredUsers(bot, chatID, true)
    }()
}

func restartVpnService() error {
    cmd := exec.Command("systemctl", "restart", ServiceName)
    return cmd.Run()
}

func autoDeleteExpiredUsers(bot *tgbotapi.BotAPI, adminID int64, shouldRestart bool) {
    users, err := getUsers()
    if err != nil {
        log.Printf("❌ [AutoDelete] Gagal mengambil data user: %v", err)
        return
    }

    deletedCount := 0
    var deletedUsers []string

    for _, u := range users {
        if u.Status == "Expired" {
            res, err := apiCall("POST", "/user/delete", map[string]interface{}{
                "password": u.Password,
            })

            if err != nil {
                log.Printf("❌ [AutoDelete] Error API saat menghapus %s: %v", u.Password, err)
                continue
            }

            if res["success"] == true {
                deletedCount++
                deletedUsers = append(deletedUsers, u.Password)
                log.Printf("✅ [AutoDelete] User expired %s berhasil dihapus.", u.Password)
            } else {
                log.Printf("❌ [AutoDelete] Gagal menghapus %s: %s", u.Password, res["message"])
            }
        }
    }

    if shouldRestart {
        if deletedCount > 0 {
            log.Printf("🔄 [Restart Service] Melakukan restart service %s...", ServiceName)
            if err := restartVpnService(); err != nil {
                log.Printf("❌ Gagal restart service: %v", err)
                if bot != nil {
                    bot.Send(tgbotapi.NewMessage(adminID, "❌ Gagal merestart service. Cek log server."))
                }
            } else {
                log.Printf("✅ Service %s berhasil di-restart.", ServiceName)
                if bot != nil {
                    bot.Send(tgbotapi.NewMessage(adminID, fmt.Sprintf("🔄 %d akun expired dihapus & Service %s berhasil di-restart.", deletedCount, ServiceName)))
                }
            }
        } else {
            if bot != nil {
                bot.Send(tgbotapi.NewMessage(adminID, "✅ Tidak ada akun expired. Tidak perlu restart service."))
            }
        }
        return
    }

    if deletedCount > 0 {
        if bot != nil {
            msgText := fmt.Sprintf("🗑️ *PEMBERSIHAN AKUN OTOMATIS*\n\n"+
                "Total `%d` akun kedaluwarsa telah dihapus secara otomatis:\n- %s",
                deletedCount, strings.Join(deletedUsers, "\n- "))

            notification := tgbotapi.NewMessage(adminID, msgText)
            notification.ParseMode = "Markdown"
            bot.Send(notification)
        }
    }
}

// --- API Calls ---

func apiCall(method, endpoint string, payload interface{}) (map[string]interface{}, error) {
    var reqBody []byte
    var err error

    if payload != nil {
        reqBody, err = json.Marshal(payload)
        if err != nil {
            return nil, err
        }
    }

    client := &http.Client{Timeout: 10 * time.Second}

    req, err := http.NewRequest(method, ApiUrl+endpoint, bytes.NewBuffer(reqBody))
    if err != nil {
        return nil, err
    }

    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("X-API-Key", ApiKey)

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

func getUsers() ([]UserData, error) {
    res, err := apiCall("GET", "/users", nil)
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

func getNearExpiredUsers() ([]UserData, error) {
    users, err := getUsers()
    if err != nil {
        return nil, err
    }

    var nearExpired []UserData
    expiryThreshold := time.Now().Add(24 * time.Hour)

    layouts := []string{
        "2006-01-02",
        "2006-01-02 15:04:05",
        "2006-01-02T15:04:05Z",
        "2006-01-02T15:04:05+07:00",
    }

    for _, u := range users {
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
            continue
        }

        if expiredTime.After(time.Now()) && expiredTime.Before(expiryThreshold) {
            nearExpired = append(nearExpired, u)
        }
    }

    return nearExpired, nil
}

func createUser(bot *tgbotapi.BotAPI, chatID int64, username string, days int, limitIP int, limitQuota int) {
    res, err := apiCall("POST", "/user/create", map[string]interface{}{
        "password":    username,
        "days":        days,
        "limit_ip":    limitIP,
        "limit_quota": limitQuota,
    })

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
        
        title := "🎉 *AKUN BERHASIL DIBUAT*"
        if days == 1 {
            title = "🎁 *AKUN TRIAL 1 HARI*"
        }

        msg := fmt.Sprintf("%s\n"+
            "━━━━━━━━━━━━━━━━━━━━━━━━━\n"+
            "🔑 *Password*: `%s`\n"+
            "🌐 *Domain*: `%s`\n"+
            "🗓️ *Expired*: `%s`\n"+
            "🔢 *Limit IP*: `%d` Device\n"+
            "💾 *Limit Kuota*: `%d GB`\n"+
            "📍 *Lokasi Server*: `%s`\n"+
            "📡 *ISP Server*: `%s`\n"+
            "━━━━━━━━━━━━━━━━━━━━━━━━━\n"+
            "🔒 *Private Tidak Digunakan User Lain*\n"+
            "⚡ *Full Speed Anti Lemot Stabil 24 Jam*\n"+
            "━━━━━━━━━━━━━━━━━━━━━━━━━",
            title, data["password"], data["domain"], data["expired"], limitIP, limitQuota, ipInfo.City, ipInfo.Isp)

        reply := tgbotapi.NewMessage(chatID, msg)
        reply.ParseMode = "Markdown"
        deleteLastMessage(bot, chatID)
        bot.Send(reply)
        showMainMenu(bot, chatID)
    } else {
        errMsg, ok := res["message"].(string)
        if !ok {
            errMsg = "Pesan error tidak diketahui dari API."
        }
        sendMessage(bot, chatID, fmt.Sprintf("❌ Gagal: %s", errMsg))
        showMainMenu(bot, chatID)
    }
}

func deleteUser(bot *tgbotapi.BotAPI, chatID int64, username string) {
    res, err := apiCall("POST", "/user/delete", map[string]interface{}{
        "password": username,
    })

    if err != nil {
        sendMessage(bot, chatID, "❌ Error API: "+err.Error())
        return
    }

    if res["success"] == true {
        msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("✅ Password `%s` berhasil *DIHAPUS*.", username))
        msg.ParseMode = "Markdown"
        deleteLastMessage(bot, chatID)
        bot.Send(msg)
        showMainMenu(bot, chatID)
    } else {
        errMsg, ok := res["message"].(string)
        if !ok {
            errMsg = "Pesan error tidak diketahui dari API."
        }
        sendMessage(bot, chatID, fmt.Sprintf("❌ Gagal menghapus: %s", errMsg))
        showMainMenu(bot, chatID)
    }
}

func renewUser(bot *tgbotapi.BotAPI, chatID int64, username string, days int, limitIP int, limitQuota int) {
    res, err := apiCall("POST", "/user/renew", map[string]interface{}{
        "password":    username,
        "days":        days,
        "limit_ip":    limitIP,
        "limit_quota": limitQuota,
    })

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
        } else {
            if infoRes, err := apiCall("GET", "/info", nil); err == nil && infoRes["success"] == true {
                if infoData, ok := infoRes["data"].(map[string]interface{}); ok {
                    if d, ok := infoData["domain"].(string); ok {
                        domain = d
                    }
                }
            }
        }

        msg := fmt.Sprintf("✅ *AKUN BERHASIL DIPERPANJANG*\n"+
            "━━━━━━━━━━━━━━━━━━━━━━━━━\n"+
            "🔑 *Password*: `%s`\n"+
            "🌐 *Domain*: `%s`\n"+
            "🗓️ *Expired Baru*: `%s`\n"+
            "🔢 *Limit IP*: `%d` Device\n"+
            "💾 *Limit Kuota*: `%d GB`\n"+
            "📍 *Lokasi Server*: `%s`\n"+
            "📡 *ISP Server*: `%s`\n"+
            "━━━━━━━━━━━━━━━━━━━━━━━━━",
            days, data["password"], domain, data["expired"], limitIP, limitQuota, ipInfo.City, ipInfo.Isp)

        reply := tgbotapi.NewMessage(chatID, msg)
        reply.ParseMode = "Markdown"
        deleteLastMessage(bot, chatID)
        bot.Send(reply)
        showMainMenu(bot, chatID)
    } else {
        errMsg, ok := res["message"].(string)
        if !ok {
            errMsg = "Pesan error tidak diketahui dari API."
        }
        sendMessage(bot, chatID, fmt.Sprintf("❌ Gagal memperpanjang: %s", errMsg))
        showMainMenu(bot, chatID)
    }
}

func listUsers(bot *tgbotapi.BotAPI, chatID int64) {
    res, err := apiCall("GET", "/users", nil)
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
            sendMessage(bot, chatID, "📂 Tidak ada user saat ini.")
            showMainMenu(bot, chatID)
            return
        }

        msg := fmt.Sprintf("📋 *DAFTAR AKUN ZIVPN* (Total: %d)\n\n", len(users))
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

func systemInfo(bot *tgbotapi.BotAPI, chatID int64) {
    res, err := apiCall("GET", "/info", nil)
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
            "━━━━━━━━━━━━━━━━━━━━━━━━━\n"+
            "🌐 *Domain*: `%s`\n"+
            "🖥️ *IP Public*: `%s`\n"+
            "🔌 *Port*: `%s`\n"+
            "🔧 *Layanan*: `%s`\n"+
            "📍 *Lokasi Server*: `%s`\n"+
            "📡 *ISP Server*: `%s`\n"+
            "━━━━━━━━━━━━━━━━━━━━━━━━━",
            data["domain"], data["public_ip"], data["port"], data["service"], ipInfo.City, ipInfo.Isp)

        reply := tgbotapi.NewMessage(chatID, msg)
        reply.ParseMode = "Markdown"
        deleteLastMessage(bot, chatID)
        bot.Send(reply)
        showMainMenu(bot, chatID)
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