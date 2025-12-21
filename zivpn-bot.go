package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// --- KONFIGURASI & KONSTANTA ---
const (
	BotConfigFile = "/etc/zivpn/bot-config.json"
	ApiUrl        = "http://127.0.0.1:8080/api"
	ApiKeyFile    = "/etc/zivpn/apikey"
	
	// URL GAMBAR MENU & QRIS (RIS STORE)
	MenuPhotoURL = "https://h.uguu.se/NgaOrSxG.png"
	
	// --- CONFIG QRIS (RIS STORE) ---
	QRIS_DATA   = "00020101021126670016COM.NOBUBANK.WWW01189360050300000879140214518329202796940303UMI51440014ID.CO.QRIS.WWW0215ID20222259294980303UMI5204481253033605802ID5909RIS STORE6011TASIKMALAYA61054611162070703A016304D2FC"
	MERCHANT_ID = "erisriswandi"

	// Interval untuk pengecekan dan penghapusan akun expired
	AutoDeleteInterval = 1 * time.Minute
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
	rand.NewSource(time.Now().UnixNano())

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
	log.Printf("RIS STORE Authorized on account %s", bot.Self.UserName)

	// --- BACKGROUND WORKER (PENGHAPUSAN OTOMATIS & NOTIFIKASI) ---
	go func() {
		autoDeleteExpiredUsers(bot, config.AdminID)
		ticker := time.NewTicker(AutoDeleteInterval)
		for range ticker.C {
			autoDeleteExpiredUsers(bot, config.AdminID)
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

// --- LOGIKA PESAN (MESSAGE HANDLER) ---

func handleMessage(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, adminID int64) {
	userID := msg.From.ID

	// 1. CEK APAKAH USER SEDANG UPLOAD BUKTI (Untuk User Biasa)
	state, exists := userStates[userID]
	if exists && state == "waiting_for_receipt" {
		if msg.Photo != nil {
			photo := msg.Photo[len(msg.Photo)-1]
			
			// Kirim Bukti ke Admin
			adminNotif := fmt.Sprintf("📸 *BUKTI PEMBAYARAN MASUK*\n\n"+
				"👤 Dari: `%s` (ID: `%d`)\n"+
				"📦 Paket: %s\n"+
				"💰 Nominal: %s\n"+
				"🆔 Merchant: %s", 
				msg.From.UserName, userID, 
				tempUserData[userID]["pkg_days"], 
				tempUserData[userID]["pkg_price"], MERCHANT_ID)

			forward := tgbotapi.NewPhoto(adminID, tgbotapi.FileID(photo.FileID))
			forward.Caption = adminNotif
			forward.ParseMode = "Markdown"
			forward.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("✅ Proses & Buat Akun", "menu_create")),
			)
			bot.Send(forward)

			bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "✅ *Bukti terkirim!* Admin RIS STORE akan memverifikasi dan mengirimkan detail akun Anda segera."))
			resetState(userID)
			return
		}
		bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "❌ Mohon kirimkan bukti dalam bentuk **Foto/Screenshot**."))
		return
	}

	// 2. LOGIKA ADMIN (TETAP SEPERTI ASLI)
	if userID == adminID {
		if exists {
			handleState(bot, msg, state)
			return
		}
		if msg.IsCommand() && msg.Command() == "start" {
			showMainMenu(bot, msg.Chat.ID)
		}
		return
	}

	// 3. LOGIKA USER BIASA (MENU BELI)
	if msg.IsCommand() && msg.Command() == "start" {
		showUserMenu(bot, msg.Chat.ID)
	}
}

// --- LOGIKA TOMBOL (CALLBACK HANDLER) ---

func handleCallback(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery, adminID int64) {
	chatID := query.Message.Chat.ID
	msgID := query.Message.MessageID
	userID := query.From.ID

	// --- FITUR PEMBELIAN USER ---
	switch {
	case query.Data == "user_buy_premium":
		msgText := "🛒 *DAFTAR HARGA RIS STORE*\n\nSilakan pilih durasi paket premium:"
		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("⭐ 15 Hari - Rp 6.000", "buy_pkg:15:6000")),
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🌟 30 Hari - Rp 12.000", "buy_pkg:30:12000")),
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("✨ 60 Hari - Rp 24.000", "buy_pkg:60:24000")),
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🔥 90 Hari - Rp 35.000", "buy_pkg:90:35000")),
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("⬅️ Kembali", "user_back")),
		)
		editMsg := tgbotapi.NewEditMessageCaption(chatID, msgID, msgText)
		editMsg.ParseMode = "Markdown"
		editMsg.ReplyMarkup = &kb
		bot.Send(editMsg)
		bot.Request(tgbotapi.NewCallback(query.ID, ""))
		return

	case strings.HasPrefix(query.Data, "buy_pkg:"):
		parts := strings.Split(query.Data, ":")
		hari, harga := parts[1], parts[2]
		
		// Generate QRIS Image via API
		qrURL := fmt.Sprintf("https://api.qrserver.com/v1/create-qr-code/?size=300x300&data=%s", url.QueryEscape(QRIS_DATA))

		msgText := fmt.Sprintf("💳 *PEMBAYARAN QRIS (RIS STORE)*\n\n"+
			"📦 Paket: *%s Hari*\n"+
			"💰 Total: *Rp %s*\n"+
			"🏪 Merchant: *%s*\n\n"+
			"1. Scan QRIS di atas.\n2. Bayar sesuai nominal.\n3. Jika sudah, klik tombol di bawah untuk kirim bukti.", hari, harga, MERCHANT_ID)
		
		bot.Send(tgbotapi.NewDeleteMessage(chatID, msgID))
		qrisMsg := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(qrURL))
		qrisMsg.Caption = msgText
		qrisMsg.ParseMode = "Markdown"
		qrisMsg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("✅ SAYA SUDAH BAYAR", "confirm_pay:"+hari+":"+harga)),
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("❌ Batal", "user_back")),
		)
		bot.Send(qrisMsg)
		bot.Request(tgbotapi.NewCallback(query.ID, ""))
		return

	case strings.HasPrefix(query.Data, "confirm_pay:"):
		parts := strings.Split(query.Data, ":")
		userStates[userID] = "waiting_for_receipt"
		tempUserData[userID] = map[string]string{"pkg_days": parts[1] + " Hari", "pkg_price": "Rp " + parts[2]}
		
		bot.Send(tgbotapi.NewDeleteMessage(chatID, msgID))
		msg := tgbotapi.NewMessage(chatID, "📸 *UPLOAD BUKTI*\n\nSilakan kirimkan **Foto/Screenshot** bukti transfer Anda sekarang.")
		msg.ParseMode = "Markdown"
		bot.Send(msg)
		bot.Request(tgbotapi.NewCallback(query.ID, "Ditunggu fotonya"))
		return

	case query.Data == "user_back":
		bot.Send(tgbotapi.NewDeleteMessage(chatID, msgID))
		showUserMenu(bot, chatID)
		return
	}

	// --- PROTEKSI ADMIN & FITUR ADMIN ASLI ---
	if userID != adminID {
		bot.Request(tgbotapi.NewCallback(query.ID, "⛔ Akses Ditolak"))
		return
	}

	switch {
	case query.Data == "menu_trial_1": 
		createGenericTrialUser(bot, chatID, 1)
	case query.Data == "menu_trial_15":
		createGenericTrialUser(bot, chatID, 15)
	case query.Data == "menu_trial_30":
		createGenericTrialUser(bot, chatID, 30)
	case query.Data == "menu_trial_60":
		createGenericTrialUser(bot, chatID, 60)
	case query.Data == "menu_trial_90":
		createGenericTrialUser(bot, chatID, 90)
	case query.Data == "menu_create":
		userStates[userID] = "create_username"
		tempUserData[userID] = make(map[string]string)
		sendMessage(bot, chatID, "🔑 *MENU CREATE*\nSilakan masukkan **PASSWORD** yang diinginkan:")
	case query.Data == "menu_delete":
		showUserSelection(bot, chatID, 1, "delete")
	case query.Data == "menu_renew":
		showUserSelection(bot, chatID, 1, "renew")
	case query.Data == "menu_list":
		listUsers(bot, chatID)
	case query.Data == "menu_info":
		systemInfo(bot, chatID)
	case query.Data == "cancel":
		resetState(userID)
		showMainMenu(bot, chatID)
	case strings.HasPrefix(query.Data, "page_"):
		parts := strings.Split(query.Data, ":")
		action := parts[0][5:]
		page, _ := strconv.Atoi(parts[1])
		showUserSelection(bot, chatID, page, action)
	case strings.HasPrefix(query.Data, "select_renew:"):
		username := strings.TrimPrefix(query.Data, "select_renew:")
		tempUserData[userID] = map[string]string{"username": username}
		userStates[userID] = "renew_days"
		sendMessage(bot, chatID, fmt.Sprintf("🔄 *MENU RENEW*\nUser: `%s`\nMasukkan tambahan durasi (*Hari*):", username))
	case strings.HasPrefix(query.Data, "select_delete:"):
		username := strings.TrimPrefix(query.Data, "select_delete:")
		msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("❓ *KONFIRMASI HAPUS*\nAnda yakin ingin menghapus user `%s`?", username))
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
		deleteUser(bot, chatID, username)
	}
	bot.Request(tgbotapi.NewCallback(query.ID, ""))
}

// --- FUNGSI TAMPILAN (UI) ---

func showUserMenu(bot *tgbotapi.BotAPI, chatID int64) {
	msgText := "👋 *SELAMAT DATANG DI RIS STORE*\n\nButuh internet kencang & stabil?\nBeli akun premium sekarang melalui menu di bawah!"
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🛒 Beli Akun Premium (QRIS)", "user_buy_premium")),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonURL("👨‍💻 Hubungi Admin", "https://t.me/JesVpnt")),
	)
	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(MenuPhotoURL))
	photo.Caption = msgText
	photo.ParseMode = "Markdown"
	photo.ReplyMarkup = kb
	bot.Send(photo)
}

func showMainMenu(bot *tgbotapi.BotAPI, chatID int64) {
	ipInfo, _ := getIpInfo()
	domain := "Unknown"
	if res, err := apiCall("GET", "/info", nil); err == nil && res["success"] == true {
		if data, ok := res["data"].(map[string]interface{}); ok {
			if d, ok := data["domain"].(string); ok { domain = d }
		}
	}
	totalUsers := 0
	if users, err := getUsers(); err == nil { totalUsers = len(users) }

	nearExpiredUsers, _ := getNearExpiredUsers()
	expiredText := ""
	if len(nearExpiredUsers) > 0 {
		expiredText += "\n\n⚠️ *AKUN AKAN SEGERA KADALUARSA:*\n"
		for i, u := range nearExpiredUsers {
			if i >= 5 { expiredText += "... dan user lainnya\n"; break }
			expiredText += fmt.Sprintf("• `%s` (Expired: %s)\n", u.Password, u.Expired)
		}
	}

	msgText := fmt.Sprintf("✨ *WELCOME TO BOT PGETUNNEL UDP ZIVPN*\n\n"+
		"Server Info:\n"+
		"• 🌐 *Domain*: `%s`\n"+
		"• 📍 *Lokasi*: `%s`\n"+
		"• 📡 *ISP*: `%s`\n"+
		"• 👤 *Total Akun*: `%d`"+expiredText+"\n\n"+
		"Silakan pilih menu di bawah ini:",
		domain, ipInfo.City, ipInfo.Isp, totalUsers)

	deleteLastMessage(bot, chatID)
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("➕ Buat Akun", "menu_create"), tgbotapi.NewInlineKeyboardButtonData("🚀 Trial 1 Hari", "menu_trial_1")),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("⭐ Buat 15 Hari 6k", "menu_trial_15"), tgbotapi.NewInlineKeyboardButtonData("🌟 Buat 30 Hari 12k", "menu_trial_30")),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("✨ Buat 60 Hari 24k", "menu_trial_60"), tgbotapi.NewInlineKeyboardButtonData("🔥 Buat 90 Hari 35k", "menu_trial_90")),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🔄 Renew Akun", "menu_renew"), tgbotapi.NewInlineKeyboardButtonData("🗑️ Hapus Akun", "menu_delete")),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("📋 Daftar Akun", "menu_list"), tgbotapi.NewInlineKeyboardButtonData("📊 Info Server", "menu_info")),
	)

	photoMsg := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(MenuPhotoURL))
	photoMsg.Caption = msgText
	photoMsg.ParseMode = "Markdown"
	photoMsg.ReplyMarkup = keyboard
	sentMsg, _ := bot.Send(photoMsg)
	lastMessageIDs[chatID] = sentMsg.MessageID
}

// --- FUNGSI LOGIKA PANEL & API ---

func handleState(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, state string) {
	userID := msg.From.ID
	text := strings.TrimSpace(msg.Text)
	switch state {
	case "create_username":
		tempUserData[userID]["username"] = text
		userStates[userID] = "create_days"
		sendMessage(bot, msg.Chat.ID, fmt.Sprintf("⏳ *CREATE USER*\nPassword: `%s`\nMasukkan **Durasi** (*Hari*):", text))
	case "create_days":
		days, _ := strconv.Atoi(text)
		createUser(bot, msg.Chat.ID, tempUserData[userID]["username"], days)
		resetState(userID)
	case "renew_days":
		days, _ := strconv.Atoi(text)
		renewUser(bot, msg.Chat.ID, tempUserData[userID]["username"], days)
		resetState(userID)
	}
}

func showUserSelection(bot *tgbotapi.BotAPI, chatID int64, page int, action string) {
	users, err := getUsers()
	if err != nil || len(users) == 0 {
		sendMessage(bot, chatID, "📂 Tidak ada user saat ini.")
		return
	}
	perPage := 10
	totalPages := (len(users) + perPage - 1) / perPage
	if page < 1 { page = 1 }; if page > totalPages { page = totalPages }
	start := (page - 1) * perPage
	end := start + perPage
	if end > len(users) { end = len(users) }

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, u := range users[start:end] {
		icon := "🟢"; if u.Status == "Expired" { icon = "🔴" }
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%s %s (Exp: %s)", icon, u.Password, u.Expired), fmt.Sprintf("select_%s:%s", action, u.Password))))
	}
	var navRow []tgbotapi.InlineKeyboardButton
	if page > 1 { navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData("⬅️ Prev", fmt.Sprintf("page_%s:%d", action, page-1))) }
	if page < totalPages { navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData("Next ➡️", fmt.Sprintf("page_%s:%d", action, page+1))) }
	if len(navRow) > 0 { rows = append(rows, navRow) }
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("⬅️ Kembali ke Menu Utama", "cancel")))

	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Pilih user untuk %s (Hal %d/%d):", action, page, totalPages))
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	sendAndTrack(bot, msg)
}

func autoDeleteExpiredUsers(bot *tgbotapi.BotAPI, adminID int64) {
	users, _ := getUsers()
	deletedCount := 0
	var deletedList []string
	for _, u := range users {
		if strings.ToLower(u.Status) == "expired" {
			res, _ := apiCall("POST", "/user/delete", map[string]interface{}{"password": u.Password})
			if res["success"] == true {
				deletedCount++
				deletedList = append(deletedList, u.Password)
			}
		}
	}
	if deletedCount > 0 {
		report := fmt.Sprintf("🧹 *AUTO-CLEANUP BERHASIL*\n\nSistem telah menghapus `%d` akun expired:\n- %s", deletedCount, strings.Join(deletedList, "\n- "))
		msg := tgbotapi.NewMessage(adminID, report)
		msg.ParseMode = "Markdown"
		bot.Send(msg)
	}
}

func apiCall(method, endpoint string, payload interface{}) (map[string]interface{}, error) {
	var reqBody []byte
	if payload != nil { reqBody, _ = json.Marshal(payload) }
	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest(method, ApiUrl+endpoint, bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", ApiKey)
	resp, err := client.Do(req)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	return result, nil
}

func getIpInfo() (IpInfo, error) {
	resp, _ := http.Get("http://ip-api.com/json/")
	var info IpInfo
	json.NewDecoder(resp.Body).Decode(&info)
	return info, nil
}

func getUsers() ([]UserData, error) {
	res, err := apiCall("GET", "/users", nil)
	if err != nil { return nil, err }
	var users []UserData
	dataBytes, _ := json.Marshal(res["data"])
	json.Unmarshal(dataBytes, &users)
	return users, nil
}

func getNearExpiredUsers() ([]UserData, error) {
	users, _ := getUsers()
	var near []UserData
	limit := time.Now().Add(24 * time.Hour)
	for _, u := range users {
		exp, _ := time.Parse("2006-01-02 15:04:05", u.Expired)
		if exp.After(time.Now()) && exp.Before(limit) { near = append(near, u) }
	}
	return near, nil
}

func createUser(bot *tgbotapi.BotAPI, chatID int64, username string, days int) {
	apiCall("POST", "/user/create", map[string]interface{}{"password": username, "days": days})
	sendMessage(bot, chatID, "✅ Akun Berhasil Dibuat!")
	showMainMenu(bot, chatID)
}

func createGenericTrialUser(bot *tgbotapi.BotAPI, chatID int64, days int) {
	pass := fmt.Sprintf("%d", 100000+rand.Intn(899999))
	apiCall("POST", "/user/create", map[string]interface{}{"password": pass, "days": days})
	sendMessage(bot, chatID, fmt.Sprintf("🚀 Trial %d Hari Berhasil: `%s`", days, pass))
	showMainMenu(bot, chatID)
}

func renewUser(bot *tgbotapi.BotAPI, chatID int64, username string, days int) {
	apiCall("POST", "/user/renew", map[string]interface{}{"password": username, "days": days})
	sendMessage(bot, chatID, "✅ Akun Diperpanjang!")
	showMainMenu(bot, chatID)
}

func deleteUser(bot *tgbotapi.BotAPI, chatID int64, username string) {
	apiCall("POST", "/user/delete", map[string]interface{}{"password": username})
	sendMessage(bot, chatID, "🗑️ User Telah Dihapus.")
	showMainMenu(bot, chatID)
}

func listUsers(bot *tgbotapi.BotAPI, chatID int64) {
	users, _ := getUsers()
	txt := "📋 *DAFTAR AKUN ZIVPN:*\n\n"
	for i, u := range users { txt += fmt.Sprintf("%d. `%s` (%s)\n", i+1, u.Password, u.Status) }
	sendMessage(bot, chatID, txt)
}

func systemInfo(bot *tgbotapi.BotAPI, chatID int64) {
	res, _ := apiCall("GET", "/info", nil)
	if d, ok := res["data"].(map[string]interface{}); ok {
		txt := fmt.Sprintf("⚙️ *INFO SERVER*\nDomain: `%s` \nIP: `%s` \nPort: `%s` \nLayanan: `%s`", d["domain"], d["public_ip"], d["port"], d["service"])
		sendMessage(bot, chatID, txt)
	}
}

func deleteLastMessage(bot *tgbotapi.BotAPI, chatID int64) {
	if id, ok := lastMessageIDs[chatID]; ok { bot.Request(tgbotapi.NewDeleteMessage(chatID, id)) }
}

func sendAndTrack(bot *tgbotapi.BotAPI, msg tgbotapi.MessageConfig) {
	deleteLastMessage(bot, msg.ChatID)
	sent, _ := bot.Send(msg)
	lastMessageIDs[msg.ChatID] = sent.MessageID
}

func sendMessage(bot *tgbotapi.BotAPI, chatID int64, txt string) {
	m := tgbotapi.NewMessage(chatID, txt)
	m.ParseMode = "Markdown"
	bot.Send(m)
}

func resetState(id int64) { delete(userStates, id); delete(tempUserData, id) }

func loadConfig() (BotConfig, error) {
	var c BotConfig
	f, _ := ioutil.ReadFile(BotConfigFile)
	json.Unmarshal(f, &c)
	return c, nil
}