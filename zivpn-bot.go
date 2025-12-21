package main

import (
    "bytes"
    "database/sql"
    "encoding/json"
    "fmt"
    "io/ioutil"
    "log"
    "math/rand"
    "net/http"
    "strconv"
    "strings"
    "time"

    tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
    _ "github.com/mattn/go-sqlite3"
)

const (
    BotConfigFile = "/etc/zivpn/bot-config.json"
    ApiUrl        = "http://127.0.0.1:8080/api"
    ApiKeyFile    = "/etc/zivpn/apikey"
    MenuPhotoURL  = "https://h.uguu.se/NgaOrSxG.png"
    AutoDeleteInterval = 1 * time.Minute
    PaymentCheckInterval = 30 * time.Second
    DbFile        = "/etc/zivpn/bot.db"
)

var ApiKey = "AutoFtBot-agskjgdvsbdreiWG1234512SDKrqw"

type BotConfig struct {
    BotToken string `json:"bot_token"`
    AdminID  int64  `json:"admin_id"`
}

type QrisConfig struct {
    InfoQris    string `json:"_INFO_QRIS"`
    DataQris    string `json:"DATA_QRIS"`
    MerchantID  string `json:"MERCHANT_ID"`
    ApiKey      string `json:"API_KEY"`
}

type PendingAccount struct {
    Username    string
    Days        int
    Amount      int
    UserID      int64
    PaymentID   string
    Status      string
    CreatedAt   time.Time
    ExpiresAt   time.Time
    QrMessageID int
}

type IpInfo struct {
    City string `json:"city"`
    Isp  string `json:"isp"`
}

type UserData struct {
    Password string `json:"password"`
    Expired  string `json:"expired"`
    Status   string `json:"status"`
}

var (
    userStates = make(map[int64]string)
    tempUserData = make(map[int64]map[string]string)
    lastMessageIDs = make(map[int64]int)
    pendingAccounts = make(map[string]*PendingAccount)
    db *sql.DB
    botConfig BotConfig
)

func isAdmin(userID int64) bool {
    return userID == botConfig.AdminID
}

func main() {
    rand.NewSource(time.Now().UnixNano())

    // Initialize database
    initDB()

    if keyBytes, err := ioutil.ReadFile(ApiKeyFile); err == nil {
        ApiKey = strings.TrimSpace(string(keyBytes))
    }
    config, err := loadConfig()
    if err != nil {
        log.Fatal("Gagal memuat konfigurasi bot:", err)
    }
    botConfig = config

    bot, err := tgbotapi.NewBotAPI(config.BotToken)
    if err != nil {
        log.Panic(err)
    }

    bot.Debug = false
    log.Printf("Authorized on account %s", bot.Self.UserName)

    // Background workers
    go func() {
        autoDeleteExpiredUsers(bot, config.AdminID)
        ticker := time.NewTicker(AutoDeleteInterval)
        for range ticker.C {
            autoDeleteExpiredUsers(bot, config.AdminID)
        }
    }()
    
    go startPaymentChecker(bot, config.AdminID)

    u := tgbotapi.NewUpdate(0)
    u.Timeout = 60
    updates := bot.GetUpdatesChan(u)

    for update := range updates {
        if update.Message != nil {
            handleMessage(bot, update.Message)
        } else if update.CallbackQuery != nil {
            handleCallback(bot, update.CallbackQuery)
        }
    }
}

func initDB() {
    var err error
    db, err = sql.Open("sqlite3", DbFile)
    if err != nil {
        log.Fatal("Gagal membuka database:", err)
    }

    createTableSQL := `
    CREATE TABLE IF NOT EXISTS pending_accounts (
        payment_id TEXT PRIMARY KEY,
        username TEXT,
        days INTEGER,
        amount INTEGER,
        user_id INTEGER,
        status TEXT,
        created_at INTEGER,
        expires_at INTEGER,
        qr_message_id INTEGER
    );`
    
    _, err = db.Exec(createTableSQL)
    if err != nil {
        log.Fatal("Gagal membuat tabel:", err)
    }

    // Load pending accounts from DB
    loadPendingAccounts()
}

func loadPendingAccounts() {
    rows, err := db.Query("SELECT payment_id, username, days, amount, user_id, status, created_at, expires_at, qr_message_id FROM pending_accounts WHERE status = 'pending'")
    if err != nil {
        log.Printf("Error loading pending accounts: %v", err)
        return
    }
    defer rows.Close()

    for rows.Next() {
        var account PendingAccount
        var createdAt, expiresAt int64
        err := rows.Scan(&account.PaymentID, &account.Username, &account.Days, &account.Amount, &account.UserID, &account.Status, &createdAt, &expiresAt, &account.QrMessageID)
        if err != nil {
            log.Printf("Error scanning account: %v", err)
            continue
        }
        account.CreatedAt = time.Unix(createdAt, 0)
        account.ExpiresAt = time.Unix(expiresAt, 0)
        pendingAccounts[account.PaymentID] = &account
    }
}

func savePendingAccount(account *PendingAccount) {
    _, err := db.Exec(`
        INSERT OR REPLACE INTO pending_accounts 
        (payment_id, username, days, amount, user_id, status, created_at, expires_at, qr_message_id) 
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
        account.PaymentID, account.Username, account.Days, account.Amount, account.UserID, account.Status, account.CreatedAt.Unix(), account.ExpiresAt.Unix(), account.QrMessageID)
    if err != nil {
        log.Printf("Error saving account: %v", err)
    }
}

func deletePendingAccount(paymentID string) {
    delete(pendingAccounts, paymentID)
    _, err := db.Exec("DELETE FROM pending_accounts WHERE payment_id = ?", paymentID)
    if err != nil {
        log.Printf("Error deleting account: %v", err)
    }
}

func checkApiStatus() bool {
    client := &http.Client{Timeout: 5 * time.Second}
    req, err := http.NewRequest("GET", "https://api.autoft.tech/cekmutasi", nil)
    if err != nil {
        return false
    }
    
    resp, err := client.Do(req)
    if err != nil {
        return false
    }
    defer resp.Body.Close()
    
    return resp.StatusCode == 200
}

func handleMessage(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
    state, exists := userStates[msg.From.ID]
    if exists {
        handleState(bot, msg, state)
        return
    }

    if msg.IsCommand() {
        switch msg.Command() {
        case "start":
            showMainMenu(bot, msg.Chat.ID)
        case "myaccounts":
            if !isAdmin(msg.From.ID) {
                showUserAccounts(bot, msg.Chat.ID, msg.From.ID)
            }
        default:
            if isAdmin(msg.From.ID) {
                msg := tgbotapi.NewMessage(msg.Chat.ID, "Perintah tidak dikenal.")
                sendAndTrack(bot, msg)
            }
        }
    }
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
        createPendingAccount(bot, msg.Chat.ID, userID, tempUserData[userID]["username"], days)
        resetState(userID)
    }
}

func handleCallback(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery) {
    // Admin-only functions
    if isAdmin(query.From.ID) {
        switch {
        case query.Data == "menu_delete":
            showUserSelection(bot, query.Message.Chat.ID, 1, "delete")
        case query.Data == "menu_renew":
            showUserSelection(bot, query.Message.Chat.ID, 1, "renew")
        case query.Data == "menu_list":
            listUsers(bot, query.Message.Chat.ID)
        case query.Data == "menu_info":
            systemInfo(bot, query.Message.Chat.ID)
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
    }

    // Functions for all users
    switch {
    case query.Data == "menu_trial_1": 
        createPendingAccount(bot, query.Message.Chat.ID, query.From.ID, generateRandomPassword(8), 1)
    case query.Data == "menu_trial_15":
        createPendingAccount(bot, query.Message.Chat.ID, query.From.ID, generateRandomPassword(8), 15)
    case query.Data == "menu_trial_30":
        createPendingAccount(bot, query.Message.Chat.ID, query.From.ID, generateRandomPassword(8), 30)
    case query.Data == "menu_trial_60":
        createPendingAccount(bot, query.Message.Chat.ID, query.From.ID, generateRandomPassword(8), 60)
    case query.Data == "menu_trial_90":
        createPendingAccount(bot, query.Message.Chat.ID, query.From.ID, generateRandomPassword(8), 90)
    case query.Data == "menu_create":
        userStates[query.From.ID] = "create_username"
        tempUserData[query.From.ID] = make(map[string]string)
        sendMessage(bot, query.Message.Chat.ID, "🔑 *MENU CREATE*\nSilakan masukkan **PASSWORD** yang diinginkan:")
    case query.Data == "menu_myaccounts":
        showUserAccounts(bot, query.Message.Chat.ID, query.From.ID)
    case query.Data == "cancel":
        delete(userStates, query.From.ID)
        delete(tempUserData, query.From.ID)
        showMainMenu(bot, query.Message.Chat.ID)
    case strings.HasPrefix(query.Data, "batal_bayar_"):
        paymentID := strings.TrimPrefix(query.Data, "batal_bayar_")
        cancelPayment(bot, query.Message.Chat.ID, paymentID)
    case strings.HasPrefix(query.Data, "cek_bayar_"):
        paymentID := strings.TrimPrefix(query.Data, "cek_bayar_")
        checkPaymentStatus(bot, query.Message.Chat.ID, paymentID)
    }

    bot.Request(tgbotapi.NewCallback(query.ID, ""))
}

func createPendingAccount(bot *tgbotapi.BotAPI, chatID int64, userID int64, username string, days int) {
    // Calculate amount based on days
    amount := calculateAmountFromDays(days)
    
    // Generate unique payment ID
    paymentID := fmt.Sprintf("pay-%d-%d", userID, time.Now().Unix())
    
    // Create pending account record
    account := &PendingAccount{
        Username:  username,
        Days:      days,
        Amount:    amount,
        UserID:    userID,
        PaymentID: paymentID,
        Status:    "pending",
        CreatedAt: time.Now(),
        ExpiresAt: time.Now().Add(10 * time.Minute), // Payment expires in 10 minutes
    }
    
    // Generate QRIS payment
    qrBuffer, err := generateQRIS(amount, paymentID)
    if err != nil {
        log.Printf("Error generating QRIS: %v", err)
        sendMessage(bot, chatID, "❌ Gagal generate QRIS. Mungkin server mutasi sedang bermasalah.")
        return
    }

    // Send confirmation message with QR
    msgText := fmt.Sprintf(`✅ *AKUN DIBUAT - MENUNGGU PEMBAYARAN*

🔑 *Username:* `%s`
⏳ *Durasi:* %d Hari
💰 *Harga:* Rp%d _(Wajib Persis)_
📅 *Batas Pembayaran:* 10 Menit

_Akun akan aktif setelah pembayaran berhasil._
_Silakan scan QRIS di bawah ini:_`, username, days, amount)

    keyboard := tgbotapi.NewInlineKeyboardMarkup(
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("🔄 Cek Status Pembayaran", fmt.Sprintf("cek_bayar_%s", paymentID)),
        ),
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("❌ Batal", fmt.Sprintf("batal_bayar_%s", paymentID)),
        ),
    )

    photoMsg := tgbotapi.NewPhoto(chatID, tgbotapi.FileBytes{
        Name:   "qris.png",
        Bytes:  qrBuffer,
    })
    photoMsg.Caption = msgText
    photoMsg.ParseMode = "Markdown"
    photoMsg.ReplyMarkup = keyboard

    sentMsg, err := bot.Send(photoMsg)
    if err != nil {
        log.Printf("Error sending QR: %v", err)
        sendMessage(bot, chatID, "❌ Gagal mengirim QRIS.")
        return
    }

    // Save to pending accounts
    account.QrMessageID = sentMsg.MessageID
    pendingAccounts[paymentID] = account
    savePendingAccount(account)
}

func cancelPayment(bot *tgbotapi.BotAPI, chatID int64, paymentID string) {
    deletePendingAccount(paymentID)
    
    // Try to delete the QR message
    if account, exists := pendingAccounts[paymentID]; exists && account.QrMessageID != 0 {
        deleteMsg := tgbotapi.NewDeleteMessage(chatID, account.QrMessageID)
        bot.Request(deleteMsg)
    }

    msgText := "❌ Pembayaran dibatalkan. Akun tidak jadi dibuat."
    keyboard := tgbotapi.NewInlineKeyboardMarkup(
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("🔙 Menu Utama", "cancel"),
        ),
    )

    msg := tgbotapi.NewMessage(chatID, msgText)
    msg.ReplyMarkup = keyboard
    sendAndTrack(bot, msg)
}

func checkPaymentStatus(bot *tgbotapi.BotAPI, chatID int64, paymentID string) {
    account, exists := pendingAccounts[paymentID]
    if !exists {
        sendMessage(bot, chatID, "❌ ID Pembayaran tidak ditemukan.")
        return
    }

    // Check if payment has expired
    if time.Now().After(account.ExpiresAt) {
        deletePendingAccount(paymentID)
        sendMessage(bot, chatID, "⏰ Pembayaran telah kadaluarsa. Silakan buat akun baru.")
        return
    }

    // Check payment status with QRIS API
    paymentStatus := checkPaymentStatusWithAPI(paymentID)
    
    if paymentStatus == "paid" {
        // Payment successful - activate the account
        activateAccount(bot, chatID, account)
        deletePendingAccount(paymentID)
    } else {
        // Still pending
        remainingTime := account.ExpiresAt.Sub(time.Now()).Round(time.Second)
        msgText := fmt.Sprintf("⏳ *PEMBAYARAN BELUM KONFIRMASI*\n\n" +
            "Username: `%s`\n" +
            "Status: Menunggu pembayaran\n" +
            "Sisa waktu: %v\n\n" +
            "Silakan selesaikan pembayaran dan cek kembali.",
            account.Username, remainingTime)

        msg := tgbotapi.NewMessage(chatID, msgText)
        msg.ParseMode = "Markdown"
        sendAndTrack(bot, msg)
    }
}

func activateAccount(bot *tgbotapi.BotAPI, chatID int64, account *PendingAccount) {
    // Create the actual account via API
    res, err := apiCall("POST", "/user/create", map[string]interface{}{
        "password": account.Username,
        "days":     account.Days,
    })

    if err != nil {
        sendMessage(bot, chatID, "❌ Error API saat mengaktifkan akun: "+err.Error())
        return
    }

    if res["success"] == true {
        data := res["data"].(map[string]interface{})
        ipInfo, _ := getIpInfo()

        msg := fmt.Sprintf("🎉 *AKUN BERHASIL DIAKTIFKAN*\n" +
            "━━━━━━━━━━━━━━━━━━━━━━━━━\n" +
            "🔑 *Password*: `%s`\n" +
            "🌐 *Domain*: `%s`\n" +
            "🗓️ *Kadaluarsa*: `%s`\n" +
            "📍 *Lokasi Server*: `%s`\n" +
            "📡 *ISP Server*: `%s`\n" +
            "━━━━━━━━━━━━━━━━━━━━━━━━━\n" +
            "🔒 *Private Tidak Digunakan User Lain*\n" +
            "⚡ *Full Speed Anti Lemot Stabil 24 Jam*\n" +
            "━━━━━━━━━━━━━━━━━━━━━━━━━",
            data["password"], data["domain"], data["expired"], ipInfo.City, ipInfo.Isp)

        reply := tgbotapi.NewMessage(chatID, msg)
        reply.ParseMode = "Markdown"
        
        // Delete the QR message first
        if account.QrMessageID != 0 {
            deleteMsg := tgbotapi.NewDeleteMessage(chatID, account.QrMessageID)
            bot.Request(deleteMsg)
        }
        
        bot.Send(reply)
        showMainMenu(bot, chatID)
    } else {
        errMsg, ok := res["message"].(string)
        if !ok {
            errMsg = "Pesan error tidak diketahui dari API."
        }
        sendMessage(bot, chatID, fmt.Sprintf("❌ Gagal mengaktifkan akun: %s", errMsg))
    }
}

func showUserAccounts(bot *tgbotapi.BotAPI, chatID int64, userID int64) {
    users, err := getUsers()
    if err != nil {
        sendMessage(bot, chatID, "❌ Gagal mengambil data user.")
        return
    }

    // Filter accounts for this user (you might need to add user_id to your user table)
    // For now, we'll show all accounts as an example
    if len(users) == 0 {
        sendMessage(bot, chatID, "📂 Anda belum memiliki akun.")
        showMainMenu(bot, chatID)
        return
    }

    msg := fmt.Sprintf("📋 *DAFTAR AKUN ANDA*\n\n")
    for i, u := range users {
        user := u.(map[string]interface{})
        statusIcon := "🟢"
        if user["status"] == "Expired" {
            statusIcon = "🔴"
        }
        msg += fmt.Sprintf("%d. %s `%s`\n    _Kadaluarsa: %s_\n", i+1, statusIcon, user["password"], user["expired"])
    }

    keyboard := tgbotapi.NewInlineKeyboardMarkup(
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("🔙 Kembali", "cancel"),
        ),
    )

    reply := tgbotapi.NewMessage(chatID, msg)
    reply.ParseMode = "Markdown"
    reply.ReplyMarkup = keyboard
    sendAndTrack(bot, reply)
}

func generateQRIS(amount int, paymentID string) ([]byte, error) {
    qrisConfig, err := loadQrisConfig()
    if err != nil {
        return nil, err
    }

    // In a real implementation, you would call the autoft-qris API
    qrText := fmt.Sprintf("%s?amount=%d&ref=%s", qrisConfig.DataQris, amount, paymentID)
    
    // Generate QR code from text
    // You would need to import a QR code library like github.com/boombuler/barcode
    // For now, return a placeholder
    return []byte("placeholder_qr_image"), nil
}

func checkPaymentStatusWithAPI(paymentID string) string {
    // In a real implementation, you would check the payment status with the QRIS API
    // For now, return "pending" to simulate ongoing payment
    return "pending"
}

func calculateAmountFromDays(days int) int {
    // Calculate amount based on days
    switch days {
    case 1:
        return 0 // Free trial
    case 15:
        return 6000
    case 30:
        return 12000
    case 60:
        return 24000
    case 90:
        return 35000
    default:
        return days * 400 // Default rate: 400 per day
    }
}

func startPaymentChecker(bot *tgbotapi.BotAPI, adminID int64) {
    ticker := time.NewTicker(PaymentCheckInterval)
    for range ticker.C {
        for paymentID, account := range pendingAccounts {
            // Check if payment has expired
            if time.Now().After(account.ExpiresAt) {
                deletePendingAccount(paymentID)
                
                msgText := fmt.Sprintf("⏰ *PEMBAYARAN KADALUARSA*\n\nUsername: `%s`\nPembayaran telah kadaluarsa.", account.Username)
                msg := tgbotapi.NewMessage(account.UserID, msgText)
                msg.ParseMode = "Markdown"
                bot.Send(msg)
                continue
            }

            // Check payment status with QRIS API
            paymentStatus := checkPaymentStatusWithAPI(paymentID)
            
            if paymentStatus == "paid" {
                // Payment successful - activate the account
                activateAccount(bot, account.UserID, account)
                deletePendingAccount(paymentID)
            }
        }
    }
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

    msgText := fmt.Sprintf("✨ *WELCOME TO BOT PGETUNNEL UDP ZIVPN*\n\n" +
        "Server Info:\n" +
        "•  🌐 *Domain*: `%s`\n" +
        "•  📍 *Lokasi*: `%s`\n" +
        "•  📡 *ISP*: `%s`\n" +
        "•  👤 *Total Akun*: `%d`\n\n" +
        "Untuk bantuan, hubungi Admin: @JesVpnt\n\n" +
        "Silakan pilih menu di bawah ini:",
        domain, ipInfo.City, ipInfo.Isp, totalUsers)

    deleteLastMessage(bot, chatID)

    // Different menus for admin and regular users
    var keyboard tgbotapi.InlineKeyboardMarkup
    
    if isAdmin(chatID) {
        keyboard = tgbotapi.NewInlineKeyboardMarkup(
            tgbotapi.NewInlineKeyboardRow(
                tgbotapi.NewInlineKeyboardButtonData("🚀 Trial 1 Hari (Gratis)", "menu_trial_1"),
                tgbotapi.NewInlineKeyboardButtonData("⭐ Buat 15 Hari 6k", "menu_trial_15"),
            ),
            tgbotapi.NewInlineKeyboardRow(
                tgbotapi.NewInlineKeyboardButtonData("🌟 Buat 30 Hari 12k", "menu_trial_30"),
                tgbotapi.NewInlineKeyboardButtonData("✨ Buat 60 Hari 24k", "menu_trial_60"),
            ),
            tgbotapi.NewInlineKeyboardRow(
                tgbotapi.NewInlineKeyboardButtonData("🔥 Buat 90 Hari 35k", "menu_trial_90"),
                tgbotapi.NewInlineKeyboardButtonData("➕ Buat Custom", "menu_create"),
            ),
            tgbotapi.NewInlineKeyboardRow(
                tgbotapi.NewInlineKeyboardButtonData("🔄 Renew Akun", "menu_renew"),
                tgbotapi.NewInlineKeyboardButtonData("🗑️ Hapus Akun", "menu_delete"),
            ),
            tgbotapi.NewInlineKeyboardRow(
                tgbotapi.NewInlineKeyboardButtonData("📋 Daftar Akun", "menu_list"),
                tgbotapi.NewInlineKeyboardButtonData("📊 Info Server", "menu_info"),
            ),
        )
    } else {
        keyboard = tgbotapi.NewInlineKeyboardMarkup(
            tgbotapi.NewInlineKeyboardRow(
                tgbotapi.NewInlineKeyboardButtonData("🚀 Trial 1 Hari (Gratis)", "menu_trial_1"),
                tgbotapi.NewInlineKeyboardButtonData("⭐ Buat 15 Hari 6k", "menu_trial_15"),
            ),
            tgbotapi.NewInlineKeyboardRow(
                tgbotapi.NewInlineKeyboardButtonData("🌟 Buat 30 Hari 12k", "menu_trial_30"),
                tgbotapi.NewInlineKeyboardButtonData("✨ Buat 60 Hari 24k", "menu_trial_60"),
            ),
            tgbotapi.NewInlineKeyboardRow(
                tgbotapi.NewInlineKeyboardButtonData("🔥 Buat 90 Hari 35k", "menu_trial_90"),
                tgbotapi.NewInlineKeyboardButtonData("➕ Buat Custom", "menu_create"),
            ),
            tgbotapi.NewInlineKeyboardRow(
                tgbotapi.NewInlineKeyboardButtonData("📋 Akun Saya", "menu_myaccounts"),
            ),
        )
    }

    photoMsg := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(MenuPhotoURL))
    photoMsg.Caption = msgText
    photoMsg.ParseMode = "Markdown"
    photoMsg.ReplyMarkup = keyboard

    sentMsg, err := bot.Send(photoMsg)
    if err == nil {
        lastMessageIDs[chatID] = sentMsg.MessageID
    } else {
        log.Printf("Gagal mengirim foto menu: %v", err)
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

func autoDeleteExpiredUsers(bot *tgbotapi.BotAPI, adminID int64) {
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

    if deletedCount > 0 {
        msgText := fmt.Sprintf("🗑️ *PEMBERSIHAN AKUN OTOMATIS*\n\n" +
            "Total `%d` akun kedaluwarsa telah dihapus secara otomatis:\n- %s",
            deletedCount, strings.Join(deletedUsers, "\n- "))

        notification := tgbotapi.NewMessage(adminID, msgText)
        notification.ParseMode = "Markdown"
        bot.Send(notification)
    }
}

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
        return nil, fmt.Errorf("failed to get users")
    }

    var users []UserData
    dataBytes, _ := json.Marshal(res["data"])
    json.Unmarshal(dataBytes, &users)
    return users, nil
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

func loadConfig() (BotConfig, error) {
    var config BotConfig
    file, err := ioutil.ReadFile(BotConfigFile)
    if err != nil {
        return config, err
    }
    err = json.Unmarshal(file, &config)
    return config, err
}

func loadQrisConfig() (QrisConfig, error) {
    var config QrisConfig
    // Load from file or use defaults
    config.InfoQris = "KONFIGURASI QRIS (Autoft-qris)"
    config.DataQris = "00020101021126670016COM.NOBUBANK.WWW01189360050300000879140214518329202796940303UMI51440014ID.CO.QRIS.WWW0215ID20222259294980303UMI5204481253033605802ID5909RIS STORE6011TASIKMALAYA61054611162070703A016304D2FC"
    config.MerchantID = "erisriswandi"
    config.ApiKey = "831307:FG5Cf2ua0HALYpDdhmZgJceVXiQOE4sr"
    return config, nil
}