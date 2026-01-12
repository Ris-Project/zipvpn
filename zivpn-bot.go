package main

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io/ioutil"
    "log"
    "math/rand"
    "net/http"
    "os"
    "os/exec"
    "path/filepath"
    "sort"
    "strconv"
    "strings"
    "time"

    tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
    BotConfigFile = "/etc/zivpn/bot-config.json"
    ApiUrl        = "http://127.0.0.1:8080/api"
    ApiKeyFile    = "/etc/zivpn/apikey"
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

var ApiKey = "AutoFtBot-agskjgdvsbdreiWG1234512SDKrqw"

type BotConfig struct {
    BotToken string `json:"bot_token"`
    AdminID  int64  `json:"admin_id"`
}

type IpInfo struct {
    City string `json:"city"`
    Isp  string `json:"isp"`
}

// Struct User Data (Disesuaikan dengan format JSON yang diinginkan)
type UserData struct {
    Host     string `json:"host"`
    Password string `json:"password"`
    Expired  string `json:"expired"`
    Status   string `json:"status"`
}

var userStates = make(map[int64]string)
var tempUserData = make(map[int64]map[string]string)
var lastMessageIDs = make(map[int64]int)

func main() {
    rand.NewSource(time.Now().UnixNano())

    // Pastikan direktori backup ada
    if err := os.MkdirAll(BackupDir, 0755); err != nil {
        log.Printf("Gagal membuat direktori backup: %v", err)
    }

    if keyBytes, err := ioutil.ReadFile(ApiKeyFile); err == nil {
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

    // --- BACKGROUND WORKER (AUTO BACKUP KE SERVER) ---
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

func handleMessage(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, adminID int64) {
    if msg.From.ID != adminID {
        reply := tgbotapi.NewMessage(msg.Chat.ID, "⛔ Akses Ditolak. Anda bukan admin.")
        sendAndTrack(bot, reply)
        return
    }

    state, exists := userStates[msg.From.ID]
    
    // --- HANDLE RESTORE DARI UPLOAD FILE ---
    if exists && state == "wait_restore_file" {
        if msg.Document != nil {
            handleRestoreFromUpload(bot, msg)
        } else {
            sendMessage(bot, msg.Chat.ID, "❌ Mohon kirimkan file backup (.json), bukan teks.")
        }
        // Jangan reset state disini jika gagal, biarkan user coba lagi.
        // Tapi jika sukses, handleRestoreFromUpload akan mereset state (lebih baik ditangani di situ)
        return
    }
    // ----------------------------------------

    if exists {
        handleState(bot, msg, state)
        return
    }

    if msg.IsCommand() {
        switch msg.Command() {
        case "start":
            showMainMenu(bot, msg.Chat.ID)
        default:
            msg := tgbotapi.NewMessage(msg.Chat.ID, "Perintah tidak dikenal.")
            sendAndTrack(bot, msg)
        }
    }
}

func handleCallback(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery, adminID int64) {
    if query.From.ID != adminID {
        bot.Request(tgbotapi.NewCallback(query.ID, "Akses Ditolak"))
        return
    }

    switch {
    case query.Data == "menu_trial_1":
        createGenericTrialUser(bot, query.Message.Chat.ID, 1)
    case query.Data == "menu_trial_15":
        createGenericTrialUser(bot, query.Message.Chat.ID, 15)
    case query.Data == "menu_trial_30":
        createGenericTrialUser(bot, query.Message.Chat.ID, 30)
    case query.Data == "menu_trial_60":
        createGenericTrialUser(bot, query.Message.Chat.ID, 60)
    case query.Data == "menu_trial_90":
        createGenericTrialUser(bot, query.Message.Chat.ID, 90)
    case query.Data == "menu_create":
        userStates[query.From.ID] = "create_username"
        tempUserData[query.From.ID] = make(map[string]string)
        sendMessage(bot, query.Message.Chat.ID, "🔑 *MENU CREATE*\nSilakan masukkan **PASSWORD** yang diinginkan:")
    case query.Data == "menu_delete":
        showUserSelection(bot, query.Message.Chat.ID, 1, "delete")
    case query.Data == "menu_renew":
        showUserSelection(bot, query.Message.Chat.ID, 1, "renew")
    case query.Data == "menu_list":
        listUsers(bot, query.Message.Chat.ID)
    case query.Data == "menu_info":
        systemInfo(bot, query.Message.Chat.ID)
    
    // --- MENU BACKUP, RESTORE, CLEAN ---
    case query.Data == "menu_backup":
        performManualBackup(bot, query.Message.Chat.ID)
    case query.Data == "menu_restore":
        // Ubah logika: Bot meminta user mengupload file dengan tombol batal
        userStates[query.From.ID] = "wait_restore_file"
        msg := tgbotapi.NewMessage(query.Message.Chat.ID, "📥 *RESTORE DARI FILE*\n\nSilakan kirimkan file backup `.json` Anda.\n\nBot akan otomatis membuat ulang akun yang ada di dalam file tersebut.")
        msg.ParseMode = "Markdown"
        // Tambahkan tombol Batal
        msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
            tgbotapi.NewInlineKeyboardRow(
                tgbotapi.NewInlineKeyboardButtonData("❌ Batal", "cancel"),
            ),
        )
        sendAndTrack(bot, msg)
    case query.Data == "menu_clean_restart":
        cleanAndRestartService(bot, query.Message.Chat.ID)
    // -------------------------------------

    case query.Data == "cancel":
        delete(userStates, query.From.ID)
        delete(tempUserData, query.From.ID)
        showMainMenu(bot, query.Message.Chat.ID)
    case strings.HasPrefix(query.Data, "page_"):
        parts := strings.Split(query.Data, ":")
        action := parts[0][5:] 
        page, _ := strconv.Atoi(parts[1])
        showUserSelection(bot, query.Message.Chat.ID, page, action)
    case strings.HasPrefix(query.Data, "select_renew:"):
        username := strings.TrimPrefix(query.Data, "select_renew:")
        tempUserData[query.From.ID] = map[string]string{"username": username}
        userStates[query.From.ID] = "renew_days"
        sendMessage(bot, query.Message.Chat.ID, fmt.Sprintf("🔄 *MENU RENEW*\nUser: `%s`\nMasukkan tambahan durasi (*Hari*):", username))
    case strings.HasPrefix(query.Data, "select_delete:"):
        username := strings.TrimPrefix(query.Data, "select_delete:")
        msg := tgbotapi.NewMessage(query.Message.Chat.ID, fmt.Sprintf("❓ *KONFIRMASI HAPUS*\nAnda yakin ingin menghapus user `%s`?", username))
        msg.ParseMode = "Markdown"
        msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
            tgbotapi.NewInlineKeyboardRow(
                tgbotapi.NewInlineKeyboardButtonData("✅ Ya, Hapus", "confirm_delete:"+username),
                tgbotapi.NewInlineKeyboardButtonData("❌ Batal", "cancel"),
            ),
        )
        sendAndTrack(bot, msg)
    case strings.HasPrefix(query.Data, "confirm_delete:"):
        username := strings.TrimPrefix(query.Data, "confirm_delete:")
        deleteUser(bot, query.Message.Chat.ID, username)
    }

    bot.Request(tgbotapi.NewCallback(query.ID, ""))
}

func handleState(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, state string) {
    userID := msg.From.ID
    text := strings.TrimSpace(msg.Text)

    switch state {
    case "create_username":
        tempUserData[userID]["username"] = text
        userStates[userID] = "create_days"
        sendMessage(bot, msg.Chat.ID, fmt.Sprintf("⏳ *CREATE USER*\nPassword: `%s`\nMasukkan **Durasi** (*Hari*) pembuatan:", text))

    case "create_days":
        days, err := strconv.Atoi(text)
        if err != nil {
            sendMessage(bot, msg.Chat.ID, "❌ Durasi harus angka. Coba lagi:")
            return
        }
        createUser(bot, msg.Chat.ID, tempUserData[userID]["username"], days)
        resetState(userID)

    case "renew_days":
        days, err := strconv.Atoi(text)
        if err != nil {
            sendMessage(bot, msg.Chat.ID, "❌ Durasi harus angka. Coba lagi:")
            return
        }
        renewUser(bot, msg.Chat.ID, tempUserData[userID]["username"], days)
        resetState(userID)
    }
}

// --- FITUR RESTORE DARI UPLOAD FILE ---

func handleRestoreFromUpload(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
    // Reset state dulu agar tidak spam jika user kirim ulang
    resetState(msg.From.ID) 

    sendMessage(bot, msg.Chat.ID, "⏳ Sedang mengunduh dan memproses file backup...")

    // 1. Ambil URL download file dari Telegram
    url, err := bot.GetFileDirectURL(msg.Document.FileID)
    if err != nil {
        sendMessage(bot, msg.Chat.ID, "❌ Gagal mengambil link file dari Telegram.")
        return
    }

    // 2. Download file
    resp, err := http.Get(url)
    if err != nil {
        sendMessage(bot, msg.Chat.ID, "❌ Gagal mendownload file.")
        return
    }
    defer resp.Body.Close()

    // 3. Decode JSON
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

    // Layout parsing tanggal (mendukung berbagai format)
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
            log.Printf("Restore: Gagal parse tanggal %s - %s", u.Password, u.Expired)
            failedCount++
            failedUsers = append(failedUsers, u.Password)
            continue
        }

        duration := time.Until(expiredTime)
        days := int(duration.Hours() / 24)

        // Jika sudah expired, skip
        if days <= 0 {
            skippedCount++
            continue
        }

        // Create User via API
        res, err := apiCall("POST", "/user/create", map[string]interface{}{
            "password": u.Password,
            "days":     days,
        })

        if err != nil {
            log.Printf("Restore: API Error %s: %v", u.Password, err)
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

        // Jeda kecil agar tidak spam API
        if i < len(backupUsers)-1 {
            time.Sleep(200 * time.Millisecond)
        }
    }

    msgResult := fmt.Sprintf("✅ *Restore Selesai*\n\n" +
        "📂 Total Data: %d\n" +
        "✅ Berhasil dibuat: %d\n" +
        "⚠️ Dilewati (Sudah ada/Expired): %d\n",
        len(backupUsers), successCount, skippedCount)

    if failedCount > 0 {
        msgResult += fmt.Sprintf("❌ Gagal (%d): %s", failedCount, strings.Join(failedUsers, ", "))
    }

    sendMessage(bot, msg.Chat.ID, msgResult)
    showMainMenu(bot, msg.Chat.ID)
}

// --- FUNGSI LAINNYA ---

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

    nearExpiredUsers, err := getNearExpiredUsers()
    expiredText := ""
    if err == nil && len(nearExpiredUsers) > 0 {
        expiredText += "\n\n⚠️ *AKUN AKAN SEGERA KADALUARSA (Dalam 24 Jam):*\n"
        for i, u := range nearExpiredUsers {
            if i >= 5 {
                expiredText += "... dan user lainnya\n"
                break
            }
            expiredText += fmt.Sprintf("•  `%s` (Expired: %s)\n", u.Password, u.Expired)
        }
    }

    msgText := fmt.Sprintf("✨ *WELCOME TO BOT PGETUNNEL UDP ZIVPN*\n\n" +
        "Server Info:\n" +
        "•  🌐 *Domain*: `%s`\n" +
        "•  📍 *Lokasi*: `%s`\n" +
        "•  📡 *ISP*: `%s`\n" +
        "•  👤 *Total Akun*: `%d`\n\n" +
        "Untuk bantuan, hubungi Admin: @JesVpnt\n\n" +
        "Silakan pilih menu di bawah ini:",
        domain, ipInfo.City, ipInfo.Isp, totalUsers)

    msgText += expiredText

    deleteLastMessage(bot, chatID)

    keyboard := tgbotapi.NewInlineKeyboardMarkup(
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("➕ Buat Akun", "menu_create"),
            tgbotapi.NewInlineKeyboardButtonData("🚀 Trial 1 Hari", "menu_trial_1"),
        ),
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("⭐ Buat 15 Hari 6k", "menu_trial_15"),
            tgbotapi.NewInlineKeyboardButtonData("🌟 Buat 30 Hari 12k", "menu_trial_30"),
        ),
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("✨ Buat 60 Hari 24k", "menu_trial_60"),
            tgbotapi.NewInlineKeyboardButtonData("🔥 Buat 90 Hari 35k", "menu_trial_90"),
        ),
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("🔄 Renew Akun", "menu_renew"),
            tgbotapi.NewInlineKeyboardButtonData("🗑️ Hapus Akun", "menu_delete"),
        ),
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("📋 Daftar Akun", "menu_list"),
            tgbotapi.NewInlineKeyboardButtonData("📊 Info Server", "menu_info"),
        ),
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("💾 Backup Data", "menu_backup"),
            tgbotapi.NewInlineKeyboardButtonData("♻️ Restore Data", "menu_restore"),
        ),
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
        lastMessageIDs[chatID] = sentMsg.MessageID
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
    if _, inState := userStates[chatID]; inState {
        cancelKb := tgbotapi.NewInlineKeyboardMarkup(
            tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("❌ Batal", "cancel")),
        )
        msg.ReplyMarkup = cancelKb
    }
    sendAndTrack(bot, msg)
}

func resetState(userID int64) {
    delete(userStates, userID)
    delete(tempUserData, userID)
}

func deleteLastMessage(bot *tgbotapi.BotAPI, chatID int64) {
    if msgID, ok := lastMessageIDs[chatID]; ok {
        deleteMsg := tgbotapi.NewDeleteMessage(chatID, msgID)
        bot.Request(deleteMsg)
        delete(lastMessageIDs, chatID)
    }
}

func sendAndTrack(bot *tgbotapi.BotAPI, msg tgbotapi.MessageConfig) {
    deleteLastMessage(bot, msg.ChatID)
    sentMsg, err := bot.Send(msg)
    if err == nil {
        lastMessageIDs[msg.ChatID] = sentMsg.MessageID
    }
}

func generateRandomPassword(length int) string {
    const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
    seededRand := rand.New(rand.NewSource(time.Now().UnixNano()))
    b := make([]byte, length)
    for i := range b {
        b[i] = charset[seededRand.Intn(len(charset))]
    }
    return string(b)
}

// --- BACKUP & CLEAN FUNCTIONS ---

func performAutoBackup(bot *tgbotapi.BotAPI, adminID int64) {
    filePath, err := saveBackupToFile()
    if err != nil {
        log.Printf("❌ [AutoBackup] Gagal: %v", err)
        return
    }
    log.Printf("✅ [AutoBackup] Berhasil disimpan: %s", filePath)
}

func performManualBackup(bot *tgbotapi.BotAPI, chatID int64) {
    sendMessage(bot, chatID, "⏳ Sedang membackup data user...")
    
    filePath, err := saveBackupToFile()
    if err != nil {
        sendMessage(bot, chatID, "❌ Gagal melakukan backup: "+err.Error())
        return
    }

    doc := tgbotapi.NewDocument(chatID, tgbotapi.FilePath(filePath))
    doc.Caption = fmt.Sprintf("💾 *Backup Data User*\nTanggal: %s\nFile: %s", time.Now().Format("2006-01-02 15:04:05"), filepath.Base(filePath))
    doc.ParseMode = "Markdown"
    
    deleteLastMessage(bot, chatID)
    if _, err := bot.Send(doc); err != nil {
        sendMessage(bot, chatID, "❌ Gagal mengirim file backup: "+err.Error())
    }
    showMainMenu(bot, chatID)
}

func saveBackupToFile() (string, error) {
    users, err := getUsers()
    if err != nil {
        return "", fmt.Errorf("gagal ambil data user: %v", err)
    }

    if len(users) == 0 {
        return "", fmt.Errorf("tidak ada user untuk dibackup")
    }

    // Ambil Domain Server untuk diisi ke dalam field "Host"
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

    timestamp := time.Now().Format("2006-01-02_15-04-05")
    filename := fmt.Sprintf("backup_users_%s.json", timestamp)
    fullPath := filepath.Join(BackupDir, filename)

    data, err := json.MarshalIndent(users, "", "  ")
    if err != nil {
        return "", fmt.Errorf("gagal marshal data: %v", err)
    }

    if err := ioutil.WriteFile(fullPath, data, 0644); err != nil {
        return "", fmt.Errorf("gagal menulis file: %v", err)
    }

    return fullPath, nil
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
            msgText := fmt.Sprintf("🗑️ *PEMBERSIHAN AKUN OTOMATIS*\n\n" +
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

    client := &http.Client{}
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

    body, _ := ioutil.ReadAll(resp.Body)
    var result map[string]interface{}
    json.Unmarshal(body, &result)

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
    
    // Handling data jika berupa slice langsung
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

func createUser(bot *tgbotapi.BotAPI, chatID int64, username string, days int) {
    res, err := apiCall("POST", "/user/create", map[string]interface{}{
        "password": username,
        "days":     days,
    })

    if err != nil {
        sendMessage(bot, chatID, "❌ Error API: "+err.Error())
        return
    }

    if res["success"] == true {
        data := res["data"].(map[string]interface{})
        ipInfo, _ := getIpInfo()

        msg := fmt.Sprintf("🎉 *AKUN BERHASIL DIBUAT*\n" +
            "━━━━━━━━━━━━━━━━━━━━━━━━━\n" +
            "🔑 *Password*: `%s`\n" +
            "🌐 *Domain*: `%s`\n" +
            "🗓️ *Kadaluarsa*: `%s`\n" +
            "📍 *Lokasi Server*: `%s`\n" +
            "📡 *ISP Server*: `%s`\n" +
            "━━━━━━━━━━━━━━━━━━━━━━━━━\n" +
          "🔒 *Private Tidak Digunakan User Lain*\n"+
          "⚡ *Full Speed Anti Lemot Stabil 24 Jam*\n"+
            "━━━━━━━━━━━━━━━━━━━━━━━━━",
            data["password"], data["domain"], data["expired"], ipInfo.City, ipInfo.Isp)

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

func createGenericTrialUser(bot *tgbotapi.BotAPI, chatID int64, days int) {
    trialPassword := generateRandomPassword(8)

    res, err := apiCall("POST", "/user/create", map[string]interface{}{
        "password": trialPassword,
        "minutes":  0, 
        "days":     days, 
    })

    if err != nil {
        sendMessage(bot, chatID, "❌ Error Komunikasi API: "+err.Error())
        return
    }

    if res["success"] == true {
        data, ok := res["data"].(map[string]interface{})
        if !ok {
            sendMessage(bot, chatID, "❌ Gagal: Format data respons dari API tidak valid.")
            showMainMenu(bot, chatID)
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
        } else {
            if infoRes, err := apiCall("GET", "/info", nil); err == nil && infoRes["success"] == true {
                if infoData, ok := infoRes["data"].(map[string]interface{}); ok {
                    if d, ok := infoData["domain"].(string); ok {
                        domain = d
                    }
                }
            }
        }

        msg := fmt.Sprintf("🚀 *AKUN %d HARI BERHASIL DIBUAT*\n" +
            "━━━━━━━━━━━━━━━━━━━━━━━━━\n" +
            "🔑 *Password*: `%s`\n" +
            "🌐 *Domain*: `%s`\n" +
            "⏳ *Aktip selama*: `%d Hari`\n" + 
            "🗓️ *Kadaluarsa*: `%s`\n" +
            "📍 *Lokasi Server*: `%s`\n" +
            "📡 *ISP Server*: `%s`\n" +
            "━━━━━━━━━━━━━━━━━━━━━━━━━\n" +
          "🔒 *Private Tidak Digunakan User Lain*\n"+
          "⚡ *Full Speed Anti Lemot Stabil 24 Jam*\n"+
          "❗️ *Akun ini aktif selama %d hari 2 hp*\n"+
        "━━━━━━━━━━━━━━━━━━━━━━━━━━",
            days, password, domain, days, expired, ipInfo.City, ipInfo.Isp, days)

        reply := tgbotapi.NewMessage(chatID, msg)
        reply.ParseMode = "Markdown"
        deleteLastMessage(bot, chatID)
        bot.Send(reply)
        showMainMenu(bot, chatID)
    } else {
        errMsg, ok := res["message"].(string)
        if !ok {
            errMsg = "Respon kegagalan dari API tidak diketahui."
        }
        sendMessage(bot, chatID, fmt.Sprintf("❌ Gagal membuat Trial: %s", errMsg))
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

func renewUser(bot *tgbotapi.BotAPI, chatID int64, username string, days int) {
    res, err := apiCall("POST", "/user/renew", map[string]interface{}{
        "password": username,
        "days":     days,
    })

    if err != nil {
        sendMessage(bot, chatID, "❌ Error API: "+err.Error())
        return
    }

    if res["success"] == true {
        data := res["data"].(map[string]interface{})
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

        msg := fmt.Sprintf("✅ *AKUN BERHASIL DIPERPANJANG* (%d Hari)\n" +
            "━━━━━━━━━━━━━━━━━━━━━━━━━\n" +
            "🔑 *Password*: `%s`\n" +
            "🌐 *Domain*: `%s`\n" +
            "🗓️ *Kadaluarsa Baru*: `%s`\n" +
            "📍 *Lokasi Server*: `%s`\n" +
            "📡 *ISP Server*: `%s`\n" +
            "━━━━━━━━━━━━━━━━━━━━━━━━━",
            days, data["password"], domain, data["expired"], ipInfo.City, ipInfo.Isp)

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
        users := res["data"].([]interface{})
        if len(users) == 0 {
            sendMessage(bot, chatID, "📂 Tidak ada user saat ini.")
            showMainMenu(bot, chatID)
            return
        }

        msg := fmt.Sprintf("📋 *DAFTAR AKUN ZIVPN* (Total: %d)\n\n", len(users))
        for i, u := range users {
            user := u.(map[string]interface{})
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
        data := res["data"].(map[string]interface{})
        ipInfo, _ := getIpInfo()

        msg := fmt.Sprintf("⚙️ *INFORMASI DETAIL SERVER*\n" +
            "━━━━━━━━━━━━━━━━━━━━━━━━━\n" +
            "🌐 *Domain*: `%s`\n" +
            "🖥️ *IP Public*: `%s`\n" +
            "🔌 *Port*: `%s`\n" +
            "🔧 *Layanan*: `%s`\n" +
            "📍 *Lokasi Server*: `%s`\n" +
            "📡 *ISP Server*: `%s`\n" +
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
    file, err := ioutil.ReadFile(BotConfigFile)
    if err != nil {
        return config, err
    }
    err = json.Unmarshal(file, &config)
    return config, err
}