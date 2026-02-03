package main

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "log"
    mrand "math/rand" // Alias untuk menghindari konflik nama
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

    AutoDeleteInterval    = 30 * time.Second
    AutoBackupInterval    = 3 * time.Hour
    AutoTrialCheckInterval = 1 * time.Minute

    BackupDir   = "/etc/zivpn/backups"
    ServiceName = "zivpn"
)

var ApiKey = "AutoFtBot-agskjgdvsbdreiWG1234512SDKrqw"
var startTime time.Time

type BotConfig struct {
    BotToken       string `json:"bot_token"`
    AdminID        int64  `json:"admin_id"`
    NotifGroupID   int64  `json:"notif_group_id"`
    VpsExpiredDate string `json:"vps_expired_date"`
    AutoTrialTime  string `json:"auto_trial_time"`
}

type IpInfo struct {
    City string `json:"city"`
    Isp  string `json:"isp"`
}

type UserData struct {
    Host     string `json:"host"`
    Password string `json:"password"`
    Expired  string `json:"expired"`
    Status   string `json:"status"`
}

var (
    stateMutex     sync.RWMutex
    userStates     = make(map[int64]string)
    tempUserData   = make(map[int64]map[string]string)
    lastMessageIDs = make(map[int64]int)
    lastAutoTrialDate string
    lastTrialMutex    sync.Mutex
)

// Init Random Generator
var r = mrand.New(mrand.NewSource(time.Now().UnixNano()))

func main() {
    startTime = time.Now()

    // 1. Cek Prasyarat Sistem
    if err := checkSystemPrerequisites(); err != nil {
        log.Printf("❌ CEK GAGAL: %v", err)
        log.Println("Harap perbaiki error di atas sebelum menjalankan bot lagi.")
        time.Sleep(5 * time.Second) // Beri waktu baca error
        // return // Uncomment ini jika ingin bot berhenti total jika error fatal
    }

    // 2. Load Config
    config, err := loadConfig()
    if err != nil {
        log.Printf("WARNING: Gagal memuat config: %v", err)
        log.Println("Bot akan berjalan dengan config default (kosong). Anda harus setting manual jika perlu.")
    }

    // 3. Init Bot Telegram
    if config.BotToken == "" {
        log.Fatal("❌ FATAL: BotToken kosong di config. Silakan edit file /etc/zivpn/bot-config.json dan isi bot_token.")
    }

    bot, err := tgbotapi.NewBotAPI(config.BotToken)
    if err != nil {
        log.Panic("Gagal connect ke Telegram Bot API:", err)
    }

    bot.Debug = false
    log.Printf("✅ Authorized on account %s", bot.Self.UserName)

    // 4. Cek Koneksi API Panel
    if _, err := apiCall("GET", "/info", nil); err != nil {
        log.Printf("⚠️ WARNING: Tidak dapat terhubung ke Panel VPN API (%s). Pastikan Panel VPN berjalan!", ApiUrl)
    }

    // 5. Jalankan Background Workers
    go runAutoDeleteWorker(bot, config.AdminID)
    go runAutoBackupWorker(bot, config.AdminID)
    go startAutoTrialScheduler(bot, config.AdminID)

    // 6. Mulai Polling Updates
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

// --- CHECK PREREQUISITES ---
func checkSystemPrerequisites() error {
    // Cek folder backup
    if err := os.MkdirAll(BackupDir, 0755); err != nil {
        return fmt.Errorf("gagal membuat folder backup %s: %v", BackupDir, err)
    }

    // Load API Key
    if keyBytes, err := os.ReadFile(ApiKeyFile); err == nil {
        ApiKey = strings.TrimSpace(string(keyBytes))
    } else {
        log.Println("⚠️ API Key file tidak ditemukan, menggunakan default (mungkin tidak bisa akses panel).")
    }

    // Cek Config File
    if _, err := os.Stat(BotConfigFile); os.IsNotExist(err) {
        return fmt.Errorf("file config %s tidak ditemukan. Bot butuh token untuk jalan.", BotConfigFile)
    }

    return nil
}

// --- BACKGROUND WORKERS ---
func runAutoDeleteWorker(bot *tgbotapi.BotAPI, adminID int64) {
    defer func() {
        if r := recover(); r != nil {
            log.Printf("Panic recovered in AutoDelete: %v", r)
        }
    }()
    autoDeleteExpiredUsers(bot, adminID, false)
    ticker := time.NewTicker(AutoDeleteInterval)
    for range ticker.C {
        autoDeleteExpiredUsers(bot, adminID, false)
    }
}

func runAutoBackupWorker(bot *tgbotapi.BotAPI, adminID int64) {
    defer func() {
        if r := recover(); r != nil {
            log.Printf("Panic recovered in AutoBackup: %v", r)
        }
    }()
    performAutoBackup(bot, adminID)
    ticker := time.NewTicker(AutoBackupInterval)
    for range ticker.C {
        performAutoBackup(bot, adminID)
    }
}

// --- HANDLE MESSAGE ---
func handleMessage(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, adminID int64) {
    if msg.From.ID != adminID {
        sendMessage(bot, msg.Chat.ID, "⛔ Akses Ditolak. Anda bukan admin.")
        return
    }

    stateMutex.RLock()
    state, exists := userStates[msg.From.ID]
    stateMutex.RUnlock()

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

    text := strings.ToLower(msg.Text)
    if msg.IsCommand() {
        switch msg.Command() {
        case "start", "panel", "menu":
            showMainMenu(bot, msg.Chat.ID)
        case "setgroup":
            args := msg.CommandArguments()
            if args == "" {
                sendMessage(bot, msg.Chat.ID, "❌ Format salah. Usage: `/setgroup <ID_GRUP>`")
                return
            }
            groupID, err := strconv.ParseInt(args, 10, 64)
            if err != nil {
                sendMessage(bot, msg.Chat.ID, "❌ ID Grup harus berupa angka.")
                return
            }
            currentCfg, _ := loadConfig()
            currentCfg.NotifGroupID = groupID
            if err := saveConfig(currentCfg); err != nil {
                sendMessage(bot, msg.Chat.ID, "❌ Gagal menyimpan config.")
                return
            }
            sendMessage(bot, msg.Chat.ID, fmt.Sprintf("✅ Notifikasi Grup di set ke ID: `%d`", groupID))
        case "setvpsdate":
            setState(msg.From.ID, "set_vps_date")
            sendMessage(bot, msg.Chat.ID, "📅 Masukkan tanggal expired VPS (Format: YYYY-MM-DD)")
        default:
            sendMessage(bot, msg.Chat.ID, "Perintah tidak dikenal. Ketik /panel.")
        }
    } else {
        if text == "panel" || text == "menu" {
            showMainMenu(bot, msg.Chat.ID)
        } else {
            sendMessage(bot, msg.Chat.ID, "⚠️ Sistem Siaga. Ketik /panel.")
        }
    }
}

// --- HANDLE CALLBACK ---
func handleCallback(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery, adminID int64) {
    if query.From.ID != adminID {
        bot.Request(tgbotapi.NewCallback(query.ID, "Akses Ditolak"))
        return
    }
    // Answer callback immediately to prevent loading spinner
    bot.Request(tgbotapi.NewCallback(query.ID, ""))

    callbackData := query.Data

    switch {
    case callbackData == "menu_trial":
        randomPass := generateRandomPassword(4)
        sendMessage(bot, query.Message.Chat.ID, "⏳ Sedang membuat akun trial...")
        cfg, _ := loadConfig()
        createUser(bot, query.Message.Chat.ID, randomPass, 1, 1, 1, cfg)
    
    case callbackData == "menu_create":
        setState(query.From.ID, "create_username")
        setTempData(query.From.ID, make(map[string]string))
        sendMessage(bot, query.Message.Chat.ID, "🔑 *MENU CREATE*\nSilakan masukkan **PASSWORD**:")
    
    case callbackData == "menu_delete":
        showUserSelection(bot, query.Message.Chat.ID, 1, "delete")
    
    case callbackData == "menu_renew":
        showUserSelection(bot, query.Message.Chat.ID, 1, "renew")
    
    case callbackData == "menu_list":
        listUsers(bot, query.Message.Chat.ID, 1)
    
    case callbackData == "menu_info":
        systemInfo(bot, query.Message.Chat.ID)
    
    case callbackData == "menu_backup":
        performManualBackup(bot, query.Message.Chat.ID)
    
    case callbackData == "menu_restore":
        setState(query.From.ID, "wait_restore_file")
        msg := tgbotapi.NewMessage(query.Message.Chat.ID, "📥 *RESTORE DATA*\nSilakan kirimkan file backup `.json`.")
        msg.ParseMode = "Markdown"
        msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
            tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("❌ Batal", "cancel")),
        )
        sendAndTrack(bot, msg)
    
    case callbackData == "menu_settings":
        showSettingsMenu(bot, query.Message.Chat.ID)
    
    case callbackData == "menu_set_vps_date":
        setState(query.From.ID, "set_vps_date")
        sendMessage(bot, query.Message.Chat.ID, "📅 Masukkan tanggal expired VPS (Format: YYYY-MM-DD)")
    
    case callbackData == "menu_set_group":
        setState(query.From.ID, "set_group_id")
        sendMessage(bot, query.Message.Chat.ID, "🔔 Masukkan ID Grup Telegram (Contoh: -1001234567890)")
    
    case callbackData == "menu_set_trial_time":
        setState(query.From.ID, "set_auto_trial_time")
        cfg, _ := loadConfig()
        currentTime := "Default (07:02)"
        if cfg.AutoTrialTime != "" {
            currentTime = cfg.AutoTrialTime
        }
        sendMessage(bot, query.Message.Chat.ID, fmt.Sprintf("⏰ Jam saat ini: `%s`\n\nMasukkan jam baru (Format 24 Jam HH:MM):", currentTime))
    
    case callbackData == "menu_clean_restart":
        cleanAndRestartService(bot, query.Message.Chat.ID)
    
    case callbackData == "cancel":
        resetState(query.From.ID)
        showMainMenu(bot, query.Message.Chat.ID)
    
    case strings.HasPrefix(callbackData, "page_"):
        parts := strings.Split(callbackData, ":")
        if len(parts) < 2 {
            log.Printf("Error parsing page callback: %s", callbackData)
            return
        }
        action := parts[0][5:]
        page, _ := strconv.Atoi(parts[1])
        if action == "list" {
            listUsers(bot, query.Message.Chat.ID, page)
        } else {
            showUserSelection(bot, query.Message.Chat.ID, page, action)
        }
    
    case strings.HasPrefix(callbackData, "select_renew:"):
        username := strings.TrimPrefix(callbackData, "select_renew:")
        setTempData(query.From.ID, map[string]string{"username": username})
        setState(query.From.ID, "renew_limit_ip")
        sendMessage(bot, query.Message.Chat.ID, fmt.Sprintf("🔄 *MENU RENEW*\nUser: `%s`\n\nMasukkan **Limit IP**:", username))
    
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
}

// --- HANDLE STATE ---
func handleState(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, state string) {
    userID := msg.From.ID
    text := strings.TrimSpace(msg.Text)

    switch state {
    case "set_group_id":
        groupID, err := strconv.ParseInt(text, 10, 64)
        if err != nil {
            sendMessage(bot, msg.Chat.ID, "❌ ID Grup harus berupa angka.")
            return
        }
        currentCfg, err := loadConfig()
        if err == nil {
            currentCfg.NotifGroupID = groupID
            saveConfig(currentCfg)
        }
        resetState(userID)
        sendMessage(bot, msg.Chat.ID, fmt.Sprintf("✅ Notifikasi Grup berhasil diupdate ke ID: `%d`", groupID))
        showMainMenu(bot, msg.Chat.ID)

    case "set_vps_date":
        _, err := time.Parse("2006-01-02", text)
        if err != nil {
            sendMessage(bot, msg.Chat.ID, "❌ Format tanggal salah (YYYY-MM-DD).")
            return
        }
        currentCfg, err := loadConfig()
        if err == nil {
            currentCfg.VpsExpiredDate = text
            saveConfig(currentCfg)
        }
        resetState(userID)
        sendMessage(bot, msg.Chat.ID, fmt.Sprintf("✅ Tanggal Expired VPS diupdate ke: `%s`", text))
        showMainMenu(bot, msg.Chat.ID)

    case "set_auto_trial_time":
        currentCfg, err := loadConfig()
        if err == nil {
            if text == "" || text == "-" {
                currentCfg.AutoTrialTime = ""
            } else {
                if _, err := time.Parse("15:04", text); err == nil {
                    currentCfg.AutoTrialTime = text
                } else {
                    sendMessage(bot, msg.Chat.ID, "❌ Format jam salah (HH:MM).")
                    return
                }
            }
            saveConfig(currentCfg)
        }
        resetState(userID)
        sendMessage(bot, msg.Chat.ID, "✅ Pengaturan waktu berhasil disimpan.")
        showSettingsMenu(bot, msg.Chat.ID)

    case "create_username":
        setTempData(userID, map[string]string{"username": text})
        setState(userID, "create_limit_ip")
        sendMessage(bot, msg.Chat.ID, fmt.Sprintf("🔑 *CREATE USER*\nPassword: `%s`\n\nMasukkan **Limit IP**:", text))

    case "create_limit_ip":
        if _, err := strconv.Atoi(text); err != nil {
            sendMessage(bot, msg.Chat.ID, "❌ Limit IP harus angka.")
            return
        }
        data, _ := getTempData(userID)
        data["limit_ip"] = text
        setTempData(userID, data)
        setState(userID, "create_limit_quota")
        sendMessage(bot, msg.Chat.ID, "💾 *CREATE USER*\n\nMasukkan **Limit Kuota** (GB):")

    case "create_limit_quota":
        if _, err := strconv.Atoi(text); err != nil {
            sendMessage(bot, msg.Chat.ID, "❌ Limit Kuota harus angka.")
            return
        }
        data, _ := getTempData(userID)
        data["limit_quota"] = text
        setTempData(userID, data)
        setState(userID, "create_days")
        sendMessage(bot, msg.Chat.ID, "📅 *CREATE USER*\n\nMasukkan **Durasi** (*Hari*):")

    case "create_days":
        days, err := strconv.Atoi(text)
        if err != nil {
            sendMessage(bot, msg.Chat.ID, "❌ Durasi harus angka.")
            return
        }
        data, ok := getTempData(userID)
        if ok {
            limitIP, _ := strconv.Atoi(data["limit_ip"])
            limitQuota, _ := strconv.Atoi(data["limit_quota"])
            username := data["username"]
            currentCfg, _ := loadConfig()
            createUser(bot, msg.Chat.ID, username, days, limitIP, limitQuota, currentCfg)
            resetState(userID)
        }

    case "renew_limit_ip":
        if _, err := strconv.Atoi(text); err != nil {
            sendMessage(bot, msg.Chat.ID, "❌ Limit IP harus angka.")
            return
        }
        data, _ := getTempData(userID)
        data["limit_ip"] = text
        setTempData(userID, data)
        setState(userID, "renew_limit_quota")
        sendMessage(bot, msg.Chat.ID, "💾 *MENU RENEW*\n\nMasukkan **Limit Kuota** (GB):")

    case "renew_limit_quota":
        if _, err := strconv.Atoi(text); err != nil {
            sendMessage(bot, msg.Chat.ID, "❌ Limit Kuota harus angka.")
            return
        }
        data, _ := getTempData(userID)
        data["limit_quota"] = text
        setTempData(userID, data)
        setState(userID, "renew_days")
        sendMessage(bot, msg.Chat.ID, "📅 *MENU RENEW*\n\nMasukkan tambahan **Durasi** (*Hari*):")

    case "renew_days":
        days, err := strconv.Atoi(text)
        if err != nil {
            sendMessage(bot, msg.Chat.ID, "❌ Durasi harus angka.")
            return
        }
        data, ok := getTempData(userID)
        if ok {
            limitIP, _ := strconv.Atoi(data["limit_ip"])
            limitQuota, _ := strconv.Atoi(data["limit_quota"])
            username := data["username"]
            renewUser(bot, msg.Chat.ID, username, days, limitIP, limitQuota)
            resetState(userID)
        }
    }
}

// --- HELPER FUNCTIONS ---
func getTempData(userID int64) (map[string]string, bool) {
    stateMutex.RLock()
    defer stateMutex.RUnlock()
    data, ok := tempUserData[userID]
    return data, ok
}

// --- AUTO TRIAL ---
func startAutoTrialScheduler(bot *tgbotapi.BotAPI, adminID int64) {
    defer func() {
        if r := recover(); r != nil {
            log.Printf("Panic recovered in AutoTrialScheduler: %v", r)
        }
    }()

    ticker := time.NewTicker(AutoTrialCheckInterval)
    defer ticker.Stop()

    log.Println("🕒 [AutoTrial] Scheduler started.")

    for range ticker.C {
        cfg, err := loadConfig()
        if err != nil {
            continue
        }

        targetTimeStr := cfg.AutoTrialTime
        if targetTimeStr == "" {
            targetTimeStr = "07:02"
        }

        now := time.Now()
        nowTimeStr := now.Format("15:04")

        if nowTimeStr == targetTimeStr {
            lastTrialMutex.Lock()
            if lastAutoTrialDate == now.Format("2006-01-02 15:04") {
                lastTrialMutex.Unlock()
                continue
            }
            lastAutoTrialDate = now.Format("2006-01-02 15:04")
            lastTrialMutex.Unlock()

            log.Printf("🚀 [AutoTrial] Membuat akun trial...")
            randomPass := generateRandomPassword(4)
            sendMessage(bot, adminID, fmt.Sprintf("⏳ [AUTO TRIAL] Membuat akun trial..."))
            createUser(bot, adminID, randomPass, 1, 1, 1, cfg)
        }
    }
}

func showSettingsMenu(bot *tgbotapi.BotAPI, chatID int64) {
    cfg, _ := loadConfig()
    
    trialTime := "Default (07:02)"
    if cfg.AutoTrialTime != "" {
        trialTime = cfg.AutoTrialTime
    }

    keyboard := tgbotapi.NewInlineKeyboardMarkup(
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("🔄 Renew Akun", "menu_renew"),
            tgbotapi.NewInlineKeyboardButtonData("🗑️ Delete Akun", "menu_delete"),
        ),
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("📊 Info Server", "menu_info"),
        ),
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("💾 Backup User", "menu_backup"),
            tgbotapi.NewInlineKeyboardButtonData("♻️ Restore User", "menu_restore"),
        ),
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("⚠️ Set VPS Exp", "menu_set_vps_date"),
            tgbotapi.NewInlineKeyboardButtonData("🔔 Set Grup", "menu_set_group"),
        ),
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("⏰ Set Jam Auto Trial", "menu_set_trial_time"),
        ),
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("⬅️ Kembali ke Menu", "cancel"),
        ),
    )

    msgText := fmt.Sprintf("⚙️ *PENGATURAN SISTEM*\n\n⏰ Auto Trial: `%s`", trialTime)
    msg := tgbotapi.NewMessage(chatID, msgText)
    msg.ParseMode = "Markdown"
    msg.ReplyMarkup = keyboard
    deleteLastMessage(bot, chatID)

    sentMsg, err := bot.Send(msg)
    if err == nil {
        stateMutex.Lock()
        lastMessageIDs[chatID] = sentMsg.MessageID
        stateMutex.Unlock()
    }
}

func handleRestoreFromUpload(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
    resetState(msg.From.ID)
    sendMessage(bot, msg.Chat.ID, "⏳ Sedang mengunduh dan memproses file backup...")

    url, err := bot.GetFileDirectURL(msg.Document.FileID)
    if err != nil {
        sendMessage(bot, msg.Chat.ID, "❌ Gagal mengambil link file.")
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
        sendMessage(bot, msg.Chat.ID, "❌ Format file backup rusak.")
        return
    }

    sendMessage(bot, msg.Chat.ID, fmt.Sprintf("⏳ Memproses %d user...", len(backupUsers)))
    successCount := 0

    for _, u := range backupUsers {
        expiredTime, _ := time.Parse("2006-01-02", u.Expired)
        if expiredTime.IsZero() {
            expiredTime, _ = time.Parse("2006-01-02 15:04:05", u.Expired)
        }

        duration := time.Until(expiredTime)
        days := int(duration.Hours() / 24)

        if days > 0 {
            res, _ := apiCall("POST", "/user/create", map[string]interface{}{
                "password": u.Password,
                "days":     days,
            })
            if res["success"] == true {
                successCount++
            }
        }
    }
    sendMessage(bot, msg.Chat.ID, fmt.Sprintf("✅ Restore Selesai. Sukses: %d", successCount))
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

    perPage := 50
    totalPages := (len(users) + perPage - 1) / perPage
    if page < 1 { page = 1 }
    if page > totalPages { page = totalPages }

    start := (page - 1) * perPage
    end := start + perPage
    if end > len(users) { end = len(users) }

    var rows [][]tgbotapi.InlineKeyboardButton
    for _, u := range users[start:end] {
        statusIcon := "🟢"
        if u.Status == "Expired" { statusIcon = "🔴" }
        label := fmt.Sprintf("%s %s (%s)", statusIcon, u.Password, u.Expired)
        rows = append(rows, tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("select_%s:%s", action, u.Password)),
        ))
    }

    var navRow []tgbotapi.InlineKeyboardButton
    if page > 1 {
        navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData("⬅️ Prev", fmt.Sprintf("page_%s:%d", action, page-1)))
    }
    if page < totalPages {
        navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData("Next ➡️", fmt.Sprintf("page_%s:%d", action, page+1)))
    }
    if len(navRow) > 0 { rows = append(rows, navRow) }

    rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("⬅️ Menu", "cancel")))

    title := "🗑️ HAPUS"
    if action == "renew" { title = "🔄 RENEW" }

    msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("*%s*\nHalaman %d/%d", title, page, totalPages))
    msg.ParseMode = "Markdown"
    msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
    sendAndTrack(bot, msg)
}

func showMainMenu(bot *tgbotapi.BotAPI, chatID int64) {
    config, _ := loadConfig()
    ipInfo, _ := getIpInfo()
    
    domain := "Unknown"
    if res, err := apiCall("GET", "/info", nil); err == nil && res["success"] == true {
        if data, ok := res["data"].(map[string]interface{}); ok {
            if d, ok := data["domain"].(string); ok { domain = d }
        }
    }

    totalUsers := 0
    if users, _ := getUsers(); users != nil { totalUsers = len(users) }

    notifStatus := "❌ Off"
    if config.NotifGroupID != 0 { notifStatus = fmt.Sprintf("✅ Aktif (`%d`)", config.NotifGroupID) }

    uptimeDuration := time.Since(startTime)
    uptimeStr := fmt.Sprintf("%d Jam %d Menit", int(uptimeDuration.Hours()), int(uptimeDuration.Minutes())%60)
    if uptimeDuration.Hours() > 24 {
        days := int(uptimeDuration.Hours() / 24)
        hours := int(uptimeDuration.Hours()) % 24
        uptimeStr = fmt.Sprintf("%d Hari %d Jam", days, hours)
    }

    vpsInfo := "⚠️ Belum diatur"
    if config.VpsExpiredDate != "" {
        if expDate, err := time.Parse("2006-01-02", config.VpsExpiredDate); err == nil {
            daysLeft := int(time.Until(expDate).Hours() / 24)
            if daysLeft < 0 { vpsInfo = "🛑 VPS EXPIRED" } else { vpsInfo = fmt.Sprintf("⚠️ %d Hari Lagi", daysLeft) }
        }
    }

    trialInfo := "Default `07:02`"
    if config.AutoTrialTime != "" { trialInfo = fmt.Sprintf("`%s`", config.AutoTrialTime) }

    msgText := fmt.Sprintf("✨ *WELCOME TO BOT PGETUNNEL UDP ZIVPN*\n\n"+
        "• 🖥️ *Server Info:*\n• 🌐 *Domain*: `%s`\n• 📍 *Lokasi*: `%s`\n• 📡 *ISP*: `%s`\n"+
        "• 👤 *Total Akun*: `%d`\n• 🔔 *Notif*: %s\n• ⏰ *Auto Trial*: %s\n"+
        "• ⏳ *Bot Status:*\n• 🕒 *Uptime*: %s\n• ⚠️ *VPS Exp*: %s",
        domain, ipInfo.City, ipInfo.Isp, totalUsers, notifStatus, trialInfo, uptimeStr, vpsInfo)

    deleteLastMessage(bot, chatID)

    keyboard := tgbotapi.NewInlineKeyboardMarkup(
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("🎁 Trial Akun", "menu_trial"),
            tgbotapi.NewInlineKeyboardButtonData("➕ Create Akun", "menu_create"),
        ),
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("📋 List Akun", "menu_list"),
            tgbotapi.NewInlineKeyboardButtonData("⚙️ Pengaturan", "menu_settings"),
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
        textMsg := tgbotapi.NewMessage(chatID, msgText)
        textMsg.ParseMode = "Markdown"
        textMsg.ReplyMarkup = keyboard
        sendAndTrack(bot, textMsg)
    }
}

func listUsers(bot *tgbotapi.BotAPI, chatID int64, page int) {
    res, err := apiCall("GET", "/users", nil)
    if err != nil {
        sendMessage(bot, chatID, "❌ Error API: "+err.Error())
        return
    }

    users, ok := res["data"].([]interface{})
    if !ok || len(users) == 0 {
        sendMessage(bot, chatID, "📂 Tidak ada user saat ini.")
        showMainMenu(bot, chatID)
        return
    }

    perPage := 50
    totalPages := (len(users) + perPage - 1) / perPage
    if page < 1 { page = 1 }
    if page > totalPages { page = totalPages }

    start := (page - 1) * perPage
    end := start + perPage
    if end > len(users) { end = len(users) }

    msg := fmt.Sprintf("📋 *DAFTAR AKUN ZIVPN* (Hal: %d/%d)\n\n", page, totalPages)
    const maxMsgLen = 3800

    for i := start; i < end; i++ {
        user, ok := users[i].(map[string]interface{})
        if !ok { continue }
        
        statusIcon := "🟢"
        if user["status"] == "Expired" { statusIcon = "🔴" }
        
        line := fmt.Sprintf("%d. %s `%s`\n    _Kadaluarsa: %s_\n", (i+1), statusIcon, user["password"], user["expired"])
        
        if len(msg)+len(line) > maxMsgLen {
            msg += "\n... (List terlalu panjang)"
            break
        }
        msg += line
    }

    var rows [][]tgbotapi.InlineKeyboardButton
    var navRow []tgbotapi.InlineKeyboardButton

    if page > 1 { navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData("⬅️ Prev", fmt.Sprintf("page_list:%d", page-1))) }
    if page < totalPages { navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData("Next ➡️", fmt.Sprintf("page_list:%d", page+1))) }
    if len(navRow) > 0 { rows = append(rows, navRow) }

    rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("⬅️ Menu", "cancel")))

    reply := tgbotapi.NewMessage(chatID, msg)
    reply.ParseMode = "Markdown"
    reply.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
    sendAndTrack(bot, reply)
}

func sendMessage(bot *tgbotapi.BotAPI, chatID int64, text string) {
    msg := tgbotapi.NewMessage(chatID, text)
    msg.ParseMode = "Markdown"
    
    stateMutex.RLock()
    _, inState := userStates[chatID]
    stateMutex.RUnlock()
    
    if inState {
        msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("❌ Batal", "cancel")))
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
        if _, err := bot.Request(deleteMsg); err == nil {
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
    } else {
        log.Printf("Error sending message: %v", err)
    }
}

func generateRandomPassword(length int) string {
    const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
    b := make([]byte, length)
    for i := range b {
        b[i] = charset[r.Intn(len(charset))]
    }
    return string(b)
}

func saveConfig(config BotConfig) error {
    file, err := json.MarshalIndent(config, "", "  ")
    if err != nil {
        return err
    }
    return os.WriteFile(BotConfigFile, file, 0644)
}

// --- BACKUP FUNCTIONS ---

func saveBackupToFile() (string, error) {
    if err := os.MkdirAll(BackupDir, 0755); err != nil {
        return "", fmt.Errorf("gagal membuat folder backup: %v", err)
    }

    users, err := getUsers()
    if err != nil {
        return "", fmt.Errorf("gagal ambil data user: %v", err)
    }
    if len(users) == 0 {
        return "", fmt.Errorf("tidak ada user untuk dibackup")
    }

    domain := "Unknown"
    if res, err := apiCall("GET", "/info", nil); err == nil && res["success"] == true {
        if data, ok := res["data"].(map[string]interface{}); ok {
            if d, ok := data["domain"].(string); ok { domain = d }
        }
    }

    for i := range users { users[i].Host = domain }

    filename := "backup_users.json"
    fullPath := filepath.Join(BackupDir, filename)

    data, err := json.MarshalIndent(users, "", "  ")
    if err != nil {
        return "", fmt.Errorf("gagal marshal data: %v", err)
    }

    if err := os.WriteFile(fullPath, data, 0644); err != nil {
        return "", fmt.Errorf("GAGAL MENULIS FILE KE DISK: %v\nPastikan bot memiliki akses tulis ke folder: %s", err, BackupDir)
    }

    return fullPath, nil
}

func performAutoBackup(bot *tgbotapi.BotAPI, adminID int64) {
    log.Println("🔄 [AutoBackup] Memulai proses backup...")
    filePath, err := saveBackupToFile()
    if err != nil {
        log.Printf("❌ [AutoBackup] Gagal menyimpan file: %v", err)
        return
    }

    fileInfo, err := os.Stat(filePath)
    if err != nil {
        log.Printf("❌ [AutoBackup] Gagal membaca file: %v", err)
        return
    }

    if fileInfo.Size() > (50 * 1024 * 1024) {
        log.Printf("⚠️ [AutoBackup] File terlalu besar.")
        return
    }

    doc := tgbotapi.NewDocument(adminID, tgbotapi.FilePath(filePath))
    doc.Caption = fmt.Sprintf("💾 *AUTO BACKUP REPORT*\n📅 Waktu: `%s`\n📁 Ukuran: %.2f MB",
        time.Now().Format("2006-01-02 15:04:05"), float64(fileInfo.Size())/1024/1024)
    doc.ParseMode = "Markdown"
    
    if _, err := bot.Send(doc); err != nil {
        log.Printf("❌ [AutoBackup] Gagal kirim file: %v", err)
    }
}

func performManualBackup(bot *tgbotapi.BotAPI, chatID int64) {
    sendMessage(bot, chatID, "⏳ Sedang memproses backup...")
    filePath, err := saveBackupToFile()
    if err != nil {
        sendMessage(bot, chatID, "❌ **GAGAL MEMBUAT FILE**\n`"+err.Error()+"`")
        return
    }

    fileInfo, err := os.Stat(filePath)
    if os.IsNotExist(err) {
        sendMessage(bot, chatID, "❌ Error: File backup hilang setelah dibuat.")
        return
    }

    if fileInfo.Size() > (50 * 1024 * 1024) {
        sendMessage(bot, chatID, fmt.Sprintf("❌ File terlalu besar (>50MB). Ambil manual di server: `%s`", filePath))
        showMainMenu(bot, chatID)
        return
    }

    doc := tgbotapi.NewDocument(chatID, tgbotapi.FilePath(filePath))
    doc.Caption = fmt.Sprintf("💾 *Backup Data User*\n📁 Ukuran: %.2f MB", float64(fileInfo.Size())/1024/1024)
    doc.ParseMode = "Markdown"

    deleteLastMessage(bot, chatID)
    if _, err := bot.Send(doc); err != nil {
        log.Printf("Gagal kirim manual backup: %v", err)
        sendMessage(bot, chatID, "❌ Gagal mengirim file ke Telegram.")
    }
    showMainMenu(bot, chatID)
}

// --- SYSTEM & USER MANAGEMENT FUNCTIONS ---

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
        log.Printf("❌ [AutoDelete] Gagal ambil data: %v", err)
        return
    }

    deletedCount := 0

    for _, u := range users {
        expiredTime, _ := time.Parse("2006-01-02", u.Expired)
        if expiredTime.IsZero() {
            expiredTime, _ = time.Parse("2006-01-02 15:04:05", u.Expired)
        }

        if time.Now().After(expiredTime) {
            res, _ := apiCall("POST", "/user/delete", map[string]interface{}{"password": u.Password})
            if res["success"] == true {
                deletedCount++
            }
        }
    }

    if deletedCount > 0 {
        log.Printf("🔄 [AutoDelete] %d user dihapus. Restarting service...", deletedCount)
        if err := restartVpnService(); err != nil {
            log.Printf("❌ Gagal restart service: %v", err)
        } else {
            log.Printf("✅ Service %s di-restart.", ServiceName)
        }
    }

    if shouldRestart && bot != nil {
        if deletedCount > 0 {
            sendMessage(bot, adminID, fmt.Sprintf("🔄 %d akun expired dihapus & Service di-restart.", deletedCount))
        } else {
            sendMessage(bot, adminID, "✅ Tidak ada akun expired.")
        }
    }
}

func apiCall(method, endpoint string, payload interface{}) (map[string]interface{}, error) {
    var reqBody []byte
    var err error
    if payload != nil {
        reqBody, err = json.Marshal(payload)
        if err != nil { return nil, err }
    }

    client := &http.Client{Timeout: 10 * time.Second}
    req, err := http.NewRequest(method, ApiUrl+endpoint, bytes.NewBuffer(reqBody))
    if err != nil { return nil, err }

    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("X-API-Key", ApiKey)

    resp, err := client.Do(req)
    if err != nil { return nil, err }
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
    if err != nil { return IpInfo{}, err }
    defer resp.Body.Close()

    var info IpInfo
    if err := json.NewDecoder(resp.Body).Decode(&info); err != nil { return IpInfo{}, err }
    return info, nil
}

func getUsers() ([]UserData, error) {
    res, err := apiCall("GET", "/users", nil)
    if err != nil { return nil, err }
    if res["success"] != true { return nil, fmt.Errorf("API success is false") }

    var users []UserData
    if dataSlice, ok := res["data"].([]interface{}); ok {
        dataBytes, _ := json.Marshal(dataSlice)
        if err := json.Unmarshal(dataBytes, &users); err != nil {
            return nil, fmt.Errorf("gagal unmarshal data: %v", err)
        }
    } else {
        return []UserData{}, nil
    }
    return users, nil
}

func createUser(bot *tgbotapi.BotAPI, chatID int64, username string, days int, limitIP int, limitQuota int, config BotConfig) {
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
            sendMessage(bot, chatID, "❌ Format data API salah.")
            return
        }

        ipInfo, _ := getIpInfo()
        
        // Safe string extraction
        getStr := func(key string) string {
            if val, ok := data[key].(string); ok { return val }
            return "Unknown"
        }

        msg := fmt.Sprintf("🎉 *AKUN BERHASIL DIBUAT*\n"+
            "━━━━━━━━━━━━━━━━━━━━━━━━━\n"+
            "🔑 *Password*: `%s`\n"+
            "🌐 *Domain*: `%s`\n"+
            "🗓️ *Expired*: `%s`\n"+
            "🔢 *Limit IP*: `%d`\n"+
            "💾 *Limit Kuota*: `%d GB`\n"+
            "📍 *Lokasi*: `%s`\n"+
            "📡 *ISP*: `%s`\n"+
            "━━━━━━━━━━━━━━━━━━━━━━━━━",
            getStr("password"), getStr("domain"), getStr("expired"), limitIP, limitQuota, ipInfo.City, ipInfo.Isp)

        reply := tgbotapi.NewMessage(chatID, msg)
        reply.ParseMode = "Markdown"
        deleteLastMessage(bot, chatID)
        bot.Send(reply)

        if config.NotifGroupID != 0 {
            groupMsg := fmt.Sprintf("🎉 *AKUN BARU DIBUAT*\n"+
                "🔑 Password: `%s`\n🌐 Domain: `%s`\n🗓️ Expired: `%s`",
                getStr("password"), getStr("domain"), getStr("expired"))
            bot.Send(tgbotapi.NewMessage(config.NotifGroupID, groupMsg))
        }
        showMainMenu(bot, chatID)
    } else {
        sendMessage(bot, chatID, fmt.Sprintf("❌ Gagal: %v", res["message"]))
        showMainMenu(bot, chatID)
    }
}

func deleteUser(bot *tgbotapi.BotAPI, chatID int64, username string) {
    res, err := apiCall("POST", "/user/delete", map[string]interface{}{"password": username})
    if err != nil {
        sendMessage(bot, chatID, "❌ Error API: "+err.Error())
        return
    }

    if res["success"] == true {
        sendMessage(bot, chatID, fmt.Sprintf("✅ Password `%s` berhasil dihapus.", username))
        showMainMenu(bot, chatID)
    } else {
        sendMessage(bot, chatID, fmt.Sprintf("❌ Gagal menghapus: %v", res["message"]))
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
            sendMessage(bot, chatID, "❌ Format data API salah.")
            return
        }
        
        getStr := func(key string) string {
            if val, ok := data[key].(string); ok { return val }
            return "Unknown"
        }

        ipInfo, _ := getIpInfo()
        domain := getStr("domain")
        if domain == "Unknown" {
            if infoRes, _ := apiCall("GET", "/info", nil); infoRes["success"] == true {
                if infoData, ok := infoRes["data"].(map[string]interface{}); ok {
                    if d, ok := infoData["domain"].(string); ok { domain = d }
                }
            }
        }

        msg := fmt.Sprintf("✅ *BERHASIL DIPERPANJANG* (%d Hari)\n"+
            "━━━━━━━━━━━━━━━━━━━━━━━━━\n"+
            "🔑 *Password*: `%s`\n"+
            "🌐 *Domain*: `%s`\n"+
            "🗓️ *Expired Baru*: `%s`\n"+
            "━━━━━━━━━━━━━━━━━━━━━━━━━",
            days, getStr("password"), domain, getStr("expired"))

        reply := tgbotapi.NewMessage(chatID, msg)
        reply.ParseMode = "Markdown"
        deleteLastMessage(bot, chatID)
        bot.Send(reply)
        showMainMenu(bot, chatID)
    } else {
        sendMessage(bot, chatID, fmt.Sprintf("❌ Gagal memperpanjang: %v", res["message"]))
        showMainMenu(bot, chatID)
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

        getStr := func(key string) string {
            if val, ok := data[key].(string); ok { return val }
            return "Unknown"
        }

        ipInfo, _ := getIpInfo()
        msg := fmt.Sprintf("⚙️ *INFORMASI SERVER*\n"+
            "🌐 *Domain*: `%s`\n🖥️ *IP*: `%s`\n📍 *Lokasi*: `%s`\n📡 *ISP*: `%s`",
            getStr("domain"), getStr("public_ip"), ipInfo.City, ipInfo.Isp)

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
    if err != nil { return config, err }
    err = json.Unmarshal(file, &config)
    return config, err
}