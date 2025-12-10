package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	BotConfigFile = "/etc/zivpn/bot-config.json"
	ApiUrl        = "http://127.0.0.1:8080/api"
	ApiKeyFile    = "/etc/zivpn/apikey"
	// !!! GANTI INI DENGAN URL GAMBAR MENU ANDA !!!
	MenuPhotoURL    = "https://h.uguu.se/ePURTlNf.jpg"
	
	// --- KONSTANTA TOP UP MANUAL ---
    // Ganti dengan username Telegram Admin Anda
    ADMIN_USERNAME = "@JesVpnt" 
    // HARGA AKUN TETAP 
    PRICE_30_DAYS  = 12000 // Rp. 12.000
    PRICE_15_DAYS  = 6000  // Rp. 6.000
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
	Password string `json:"password"`
	Expired  string `json:"expired"`
	Status   string `json:"status"`
}

var userStates = make(map[int64]string)
var tempUserData = make(map[int64]map[string]string)
var lastMessageIDs = make(map[int64]int)

func main() {
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
	// --- Logika State Khusus Admin/Top Up (diproses duluan) ---
	state, exists := userStates[msg.From.ID]
	if exists {
		handleState(bot, msg, state)
		return
	}

	// --- Logika Command Umum ---
	if msg.IsCommand() {
		switch msg.Command() {
		case "start":
			showMainMenu(bot, msg.Chat.ID) // Semua user bisa mengakses start
		default:
			if msg.From.ID == adminID {
				msg := tgbotapi.NewMessage(msg.Chat.ID, "Perintah tidak dikenal.")
				sendAndTrack(bot, msg)
			} else {
				msg := tgbotapi.NewMessage(msg.Chat.ID, "Perintah tidak dikenal. Silakan gunakan /start untuk melihat menu.")
				sendAndTrack(bot, msg)
			}
		}
	} else if msg.From.ID != adminID {
		// Non-admin mengirim pesan teks biasa di luar state
		reply := tgbotapi.NewMessage(msg.Chat.ID, "⛔ Akses Ditolak. Silakan gunakan /start untuk melihat menu.")
		sendAndTrack(bot, reply)
	}
}

func handleCallback(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery, adminID int64) {
	is_admin := query.From.ID == adminID

	// Filter menu admin jika non-admin mencoba mengakses
	if !is_admin && (query.Data == "menu_create" || strings.HasPrefix(query.Data, "select_renew:") || strings.HasPrefix(query.Data, "select_delete:") || strings.HasPrefix(query.Data, "confirm_delete:") || query.Data == "menu_delete" || query.Data == "menu_renew" || query.Data == "menu_list") {
		bot.Request(tgbotapi.NewCallback(query.ID, "⛔ Akses Ditolak untuk menu admin."))
		return
	}

	switch {
	// --- ALUR TOP UP MANUAL BARU ---
    case query.Data == "menu_topup_manual":
        resetState(query.From.ID)
        userStates[query.From.ID] = "request_topup_manual_amount"
		
        // Hapus pesan terakhir sebelum mengirim menu baru
        deleteLastMessage(bot, query.Message.Chat.ID) 
        
		sendMessage(bot, query.Message.Chat.ID, "💳 *TOP UP SALDO (Manual)*\n\n" +
            "Silakan masukkan *nominal* Top Up yang diinginkan (Contoh: 10000). Nominal ini akan dikonfirmasi Admin:")
            
    case query.Data == "menu_buy_15_days":
        showManualPurchaseConfirmation(bot, query.Message.Chat.ID, 15)

    case query.Data == "menu_buy_30_days":
        showManualPurchaseConfirmation(bot, query.Message.Chat.ID, 30)

	// --- Menu Admin Lama ---
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
	case query.Data == "cancel":
		delete(userStates, query.From.ID)
		delete(tempUserData, query.From.ID)
		showMainMenu(bot, query.Message.Chat.ID)
	case strings.HasPrefix(query.Data, "page_"):
		parts := strings.Split(query.Data, ":")
		action := parts[0][5:] // remove "page_"
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
    // --- STATE BARU: MEMINTA NOMINAL TOP UP MANUAL ---
    case "request_topup_manual_amount":
		amount, err := strconv.Atoi(text)
		if err != nil || amount <= 0 { 
			sendMessage(bot, msg.Chat.ID, "❌ Nominal harus berupa angka positif. Coba lagi:")
			return
		}

        sendManualTopUpRequestToAdmin(bot, msg.Chat.ID, msg.From.FirstName, userID, amount)
        resetState(userID) 
        
    // --- STATE ADMIN LAMA ---
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

// --- FUNGSI BARU UNTUK PROSES TOP UP MANUAL ---
func sendManualTopUpRequestToAdmin(bot *tgbotapi.BotAPI, chatID int64, name string, userID int64, amount int) {
    // Pesan ke user yang meminta top up
    userMsg := fmt.Sprintf("✅ *PERMINTAAN TOP UP SALDO*\n" +
        "━━━━━━━━━━━━━━━━━━━━━━━━━\n" +
        "💰 *Nominal Top Up*: `Rp%d`\n\n" +
        "Permintaan Anda telah dikirim ke Admin. Silakan hubungi Admin `%s` untuk melanjutkan pembayaran dan konfirmasi. " +
        "Admin akan memprosesnya secara manual setelah Anda transfer.",
        amount, ADMIN_USERNAME)
        
	reply := tgbotapi.NewMessage(chatID, userMsg)
	reply.ParseMode = "Markdown"
	deleteLastMessage(bot, chatID)
	bot.Send(reply) 

    // Pesan notifikasi ke Admin
    adminNotifyMsg := fmt.Sprintf("🔔 *NOTIFIKASI TOP UP MANUAL*\n" +
        "━━━━━━━━━━━━━━━━━━━━━━━━━\n" +
        "👤 *Nama User*: `%s`\n" +
        "🆔 *ID Telegram*: `%d`\n" +
        "💰 *Nominal Diminta*: `Rp%d`\n" +
        "━━━━━━━━━━━━━━━━━━━━━━━━━\n" +
        "Admin, harap konfirmasi dan proses Top Up ini secara manual.",
        name, userID, amount)
        
    config, _ := loadConfig() // Ambil AdminID
    if config.AdminID != 0 {
        adminReply := tgbotapi.NewMessage(config.AdminID, adminNotifyMsg)
        adminReply.ParseMode = "Markdown"
        bot.Send(adminReply)
    }

	showMainMenu(bot, chatID)
}

// --- FUNGSI BARU UNTUK KONFIRMASI PEMBELIAN AKUN ---
func showManualPurchaseConfirmation(bot *tgbotapi.BotAPI, chatID int64, days int) {
    price := PRICE_15_DAYS
    if days == 30 {
        price = PRICE_30_DAYS
    }
    
    msg := fmt.Sprintf("💳 *PEMBELIAN AKUN %d HARI*\n" +
        "━━━━━━━━━━━━━━━━━━━━━━━━━\n" +
        "💰 *Harga*: `Rp%d`\n\n" +
        "Untuk membeli akun, Anda harus melakukan Top Up Saldo terlebih dahulu. " +
        "Setelah saldo Anda mencukupi, silakan gunakan menu 'Buat Akun' dan masukkan durasi %d hari.\n\n" +
        "Hubungi Admin %s untuk pembayaran.",
        days, price, days, ADMIN_USERNAME)
        
    reply := tgbotapi.NewMessage(chatID, msg)
    reply.ParseMode = "Markdown"
    
    // Tombol untuk langsung ke Top Up Manual
    reply.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("💳 Lanjut ke Top Up Manual", "menu_topup_manual"),
        ),
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("⬅️ Kembali ke Menu Utama", "cancel"), 
        ),
    )
    
	deleteLastMessage(bot, chatID)
	sendAndTrack(bot, reply)
}

// --- FUNGSI UTILITY ---

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

// GANTI func showMainMenu (Penambahan tombol Top Up dan Beli Akun)
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

    // Ambil Total Akun
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
        "Untuk Top Up atau bantuan, hubungi Admin: %s\n\n" +
		"Silakan pilih menu di bawah ini:",
		domain, ipInfo.City, ipInfo.Isp, totalUsers, ADMIN_USERNAME)
    
	// Hapus pesan terakhir sebelum mengirim menu baru
    deleteLastMessage(bot, chatID) 

    // Buat keyboard inline
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
        // --- MENU TOP UP DAN BELI AKUN UNTUK SEMUA USER ---
        tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💳 Top Up Saldo (Manual)", "menu_topup_manual"), 
		),
        tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("🔑 Beli Akun 15 Hari (Rp%d)", PRICE_15_DAYS), "menu_buy_15_days"), 
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("🔑 Beli Akun 30 Hari (Rp%d)", PRICE_30_DAYS), "menu_buy_30_days"), 
		),
        // --- MENU ADMIN LAMA ---
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ Buat Akun", "menu_create"),
			tgbotapi.NewInlineKeyboardButtonData("🔄 Renew Akun", "menu_renew"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 Daftar Akun", "menu_list"),
			tgbotapi.NewInlineKeyboardButtonData("🗑️ Hapus Akun", "menu_delete"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📊 Info Server", "menu_info"),
		),
	)

    // Buat pesan foto dari URL
	photoMsg := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(MenuPhotoURL))
	photoMsg.Caption = msgText
	photoMsg.ParseMode = "Markdown"
	photoMsg.ReplyMarkup = keyboard
	
    // Kirim foto
	sentMsg, err := bot.Send(photoMsg)
	if err == nil {
        // Track ID pesan yang baru dikirim (foto)
		lastMessageIDs[chatID] = sentMsg.MessageID
	} else {
        // Fallback jika pengiriman foto gagal
        log.Printf("Gagal mengirim foto menu dari URL (%s): %v. Mengirim sebagai teks biasa.", MenuPhotoURL, err)
        
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
		return nil, fmt.Errorf("failed to get users")
	}

	var users []UserData
	dataBytes, _ := json.Marshal(res["data"])
	json.Unmarshal(dataBytes, &users)
	return users, nil
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
			"━━━━━━━━━━━━━━━━━━━━━━━━━",
			data["password"], data["domain"], data["expired"], ipInfo.City, ipInfo.Isp)
		
		reply := tgbotapi.NewMessage(chatID, msg)
		reply.ParseMode = "Markdown"
		deleteLastMessage(bot, chatID)
		bot.Send(reply)
		showMainMenu(bot, chatID)
	} else {
		sendMessage(bot, chatID, fmt.Sprintf("❌ Gagal: %s", res["message"]))
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
		sendMessage(bot, chatID, fmt.Sprintf("❌ Gagal menghapus: %s", res["message"]))
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
		sendMessage(bot, chatID, fmt.Sprintf("❌ Gagal memperpanjang: %s", res["message"]))
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
			msg += fmt.Sprintf("%d. %s `%s`\n   _Kadaluarsa: %s_\n", i+1, statusIcon, user["password"], user["expired"])
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