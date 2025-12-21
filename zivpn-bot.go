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

const (
	BotConfigFile = "/etc/zivpn/bot-config.json"
	ApiUrl        = "http://127.0.0.1:8080/api"
	ApiKeyFile    = "/etc/zivpn/apikey"
	
	// URL GAMBAR MENU UTAMA
	MenuPhotoURL = "https://h.uguu.se/NgaOrSxG.png"
	
	// --- CONFIG QRIS (RIS STORE) ---
	QRIS_DATA   = "00020101021126670016COM.NOBUBANK.WWW01189360050300000879140214518329202796940303UMI51440014ID.CO.QRIS.WWW0215ID20222259294980303UMI5204481253033605802ID5909RIS STORE6011TASIKMALAYA61054611162070703A016304D2FC"
	MERCHANT_ID = "erisriswandi"

	// Interval untuk pengecekan dan penghapusan akun expired (1 menit)
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
	log.Printf("RIS STORE System Started: %s", bot.Self.UserName)

	// --- BACKGROUND WORKER (Auto Delete Expired) ---
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

// --- MESSAGE HANDLER ---

func handleMessage(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, adminID int64) {
	userID := msg.From.ID
	state, exists := userStates[userID]

	// 1. STATE USER: UPLOAD BUKTI
	if exists && state == "waiting_for_receipt" {
		if msg.Photo != nil {
			photo := msg.Photo[len(msg.Photo)-1]
			daysStr := tempUserData[userID]["raw_days"]
			
			adminNotif := fmt.Sprintf("📸 *BUKTI PEMBAYARAN MASUK*\n\n"+
				"👤 Dari: `%s` (ID: `%d`)\n"+
				"📦 Paket: %s Hari\n"+
				"💰 Nominal: %s\n\n"+
				"Klik SETUJUI untuk meminta user membuat password.", 
				msg.From.UserName, userID, daysStr, tempUserData[userID]["pkg_price"])

			forward := tgbotapi.NewPhoto(adminID, tgbotapi.FileID(photo.FileID))
			forward.Caption = adminNotif
			forward.ParseMode = "Markdown"
			forward.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("✅ SETUJUI", fmt.Sprintf("approve:%d:%s", userID, daysStr)),
					tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("❌ TOLAK", "cancel")),
				),
			)
			bot.Send(forward)
			bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "✅ *Bukti dikirim!* Menunggu persetujuan Admin RIS STORE."))
			resetState(userID)
			return
		}
	}

	// 2. STATE USER: INPUT PASSWORD (SETELAH APPROVE)
	if exists && state == "user_choosing_password" {
		password := strings.TrimSpace(msg.Text)
		days, _ := strconv.Atoi(tempUserData[userID]["raw_days"])

		res, err := apiCall("POST", "/user/create", map[string]interface{}{"password": password, "days": days})
		if err == nil && res["success"] == true {
			data := res["data"].(map[string]interface{})
			ip, _ := getIpInfo()
			
			detail := fmt.Sprintf("🎉 *AKUN BERHASIL DIBUAT!*\n\n"+
				"🔑 *Password*: `%s`\n"+
				"🌐 *Domain*: `%s`\n"+
				"🗓️ *Kadaluarsa*: `%s`\n"+
				"📍 *Server*: `%s`\n\n"+
				"Terima kasih telah berlangganan!", 
				password, data["domain"], data["expired"], ip.City)
			bot.Send(tgbotapi.NewMessage(userID, detail))
			resetState(userID)
		} else {
			bot.Send(tgbotapi.NewMessage(userID, "❌ Password sudah ada atau API error. Gunakan password lain:"))
		}
		return
	}

	// 3. LOGIKA ADMIN (TETAP SEPERTI ASLI)
	if userID == adminID {
		if exists { handleState(bot, msg, state); return }
		if msg.IsCommand() && msg.Command() == "start" { showMainMenu(bot, msg.Chat.ID) }
		return
	}

	// 4. LOGIKA USER BIASA
	if msg.IsCommand() && msg.Command() == "start" {
		showUserMenu(bot, msg.Chat.ID)
	}
}

// --- CALLBACK HANDLER ---

func handleCallback(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery, adminID int64) {
	chatID := query.Message.Chat.ID
	msgID := query.Message.MessageID
	userID := query.From.ID

	// --- LOGIKA APPROVAL ---
	if strings.HasPrefix(query.Data, "approve:") {
		if userID != adminID { return }
		parts := strings.Split(query.Data, ":")
		targetUserID, _ := strconv.ParseInt(parts[1], 10, 64)
		
		userStates[targetUserID] = "user_choosing_password"
		tempUserData[targetUserID] = map[string]string{"raw_days": parts[2]}

		bot.Send(tgbotapi.NewMessage(targetUserID, "✅ *Pembayaran Disetujui!*\n\nSilakan ketik **PASSWORD** yang Anda inginkan sekarang:"))
		bot.Send(tgbotapi.NewEditMessageCaption(chatID, msgID, "✅ Disetujui. Menunggu user input password."))
		return
	}

	// --- LOGIKA USER ---
	switch {
	case query.Data == "user_buy_premium":
		msgText := "🛒 *PILIH PAKET PREMIUM*\nSilakan pilih durasi paket:"
		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("⭐ 15 Hari - Rp 6k", "buy_pkg:15:6000")),
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🌟 30 Hari - Rp 12k", "buy_pkg:30:12000")),
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("✨ 60 Hari - Rp 24k", "buy_pkg:60:24000")),
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("⬅️ Kembali", "user_back")),
		)
		bot.Send(tgbotapi.NewEditMessageCaption(chatID, msgID, msgText))
		bot.Send(tgbotapi.NewEditMessageReplyMarkup(chatID, msgID, kb))
		return

	case strings.HasPrefix(query.Data, "buy_pkg:"):
		parts := strings.Split(query.Data, ":")
		qrURL := fmt.Sprintf("https://api.qrserver.com/v1/create-qr-code/?size=300x300&data=%s", url.QueryEscape(QRIS_DATA))
		bot.Send(tgbotapi.NewDeleteMessage(chatID, msgID))
		photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(qrURL))
		photo.Caption = fmt.Sprintf("💳 *QRIS RIS STORE*\nPaket: %s Hari\nTotal: Rp %s\n\nScan dan kirim bukti.", parts[1], parts[2])
		photo.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("✅ KONFIRMASI PEMBAYARAN", "confirm_pay:"+parts[1]+":"+parts[2])),
		)
		bot.Send(photo)
		return

	case strings.HasPrefix(query.Data, "confirm_pay:"):
		parts := strings.Split(query.Data, ":")
		userStates[userID] = "waiting_for_receipt"
		tempUserData[userID] = map[string]string{"raw_days": parts[1], "pkg_price": "Rp " + parts[2]}
		bot.Send(tgbotapi.NewDeleteMessage(chatID, msgID))
		bot.Send(tgbotapi.NewMessage(chatID, "📸 Silakan kirimkan **Foto Bukti Transfer**."))
		return

	case query.Data == "user_back":
		bot.Send(tgbotapi.NewDeleteMessage(chatID, msgID))
		showUserMenu(bot, chatID)
		return
	}

	// --- LOGIKA ADMIN ASLI (TETAP UTUH) ---
	if userID != adminID { return }
	switch {
	case query.Data == "menu_trial_1": createGenericTrialUser(bot, chatID, 1)
	case query.Data == "menu_trial_15": createGenericTrialUser(bot, chatID, 15)
	case query.Data == "menu_trial_30": createGenericTrialUser(bot, chatID, 30)
	case query.Data == "menu_trial_60": createGenericTrialUser(bot, chatID, 60)
	case query.Data == "menu_trial_90": createGenericTrialUser(bot, chatID, 90)
	case query.Data == "menu_create":
		userStates[userID] = "create_username"
		tempUserData[userID] = make(map[string]string)
		sendMessage(bot, chatID, "🔑 *MENU CREATE*\nMasukkan **PASSWORD**:")
	case query.Data == "menu_delete": showUserSelection(bot, chatID, 1, "delete")
	case query.Data == "menu_renew": showUserSelection(bot, chatID, 1, "renew")
	case query.Data == "menu_list": listUsers(bot, chatID)
	case query.Data == "menu_info": systemInfo(bot, chatID)
	case query.Data == "cancel": resetState(userID); showMainMenu(bot, chatID)
	case strings.HasPrefix(query.Data, "page_"):
		parts := strings.Split(query.Data, ":")
		p, _ := strconv.Atoi(parts[1])
		showUserSelection(bot, chatID, p, parts[0][5:])
	case strings.HasPrefix(query.Data, "select_renew:"):
		username := strings.TrimPrefix(query.Data, "select_renew:")
		tempUserData[userID] = map[string]string{"username": username}
		userStates[userID] = "renew_days"
		sendMessage(bot, chatID, fmt.Sprintf("🔄 User: `%s`\nDurasi (Hari):", username))
	case strings.HasPrefix(query.Data, "confirm_delete:"):
		deleteUser(bot, chatID, strings.TrimPrefix(query.Data, "confirm_delete:"))
	}
	bot.Request(tgbotapi.NewCallback(query.ID, ""))
}

// --- FUNGSI UI ADMIN ASLI ---

func showMainMenu(bot *tgbotapi.BotAPI, chatID int64) {
	ip, _ := getIpInfo()
	domain := "Unknown"
	if res, err := apiCall("GET", "/info", nil); err == nil && res["success"] == true {
		if data, ok := res["data"].(map[string]interface{}); ok {
			if d, ok := data["domain"].(string); ok { domain = d }
		}
	}
	totalUsers := 0
	if users, err := getUsers(); err == nil { totalUsers = len(users) }

	msgText := fmt.Sprintf("✨ *WELCOME TO BOT PGETUNNEL UDP ZIVPN*\n\n"+
		"• 🌐 *Domain*: `%s` \n• 📍 *Lokasi*: `%s` \n• 📡 *ISP*: `%s` \n• 👤 *Total Akun*: `%d` \n\n"+
		"Pilih menu di bawah:", domain, ip.City, ip.Isp, totalUsers)

	deleteLastMessage(bot, chatID)
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("➕ Buat Akun", "menu_create"), tgbotapi.NewInlineKeyboardButtonData("🚀 Trial 1 Hari", "menu_trial_1")),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("⭐ Buat 15 Hari 6k", "menu_trial_15"), tgbotapi.NewInlineKeyboardButtonData("🌟 Buat 30 Hari 12k", "menu_trial_30")),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("✨ Buat 60 Hari 24k", "menu_trial_60"), tgbotapi.NewInlineKeyboardButtonData("🔥 Buat 90 Hari 35k", "menu_trial_90")),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🔄 Renew Akun", "menu_renew"), tgbotapi.NewInlineKeyboardButtonData("🗑️ Hapus Akun", "menu_delete")),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("📋 Daftar Akun", "menu_list"), tgbotapi.NewInlineKeyboardButtonData("📊 Info Server", "menu_info")),
	)
	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(MenuPhotoURL))
	photo.Caption = msgText
	photo.ParseMode = "Markdown"
	photo.ReplyMarkup = kb
	sent, _ := bot.Send(photo)
	lastMessageIDs[chatID] = sent.MessageID
}

func showUserMenu(bot *tgbotapi.BotAPI, chatID int64) {
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🛒 Beli Akun Premium", "user_buy_premium")),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonURL("👨‍💻 Hubungi Admin", "https://t.me/JesVpnt")),
	)
	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(MenuPhotoURL))
	photo.Caption = "👋 *WELCOME TO RIS STORE*\nBeli akun VPN Premium dengan mudah di sini."
	photo.ReplyMarkup = kb
	bot.Send(photo)
}

// --- API & LOGIC UTUH ---

func handleState(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, state string) {
	id := msg.From.ID
	if state == "create_username" {
		tempUserData[id]["username"] = msg.Text
		userStates[id] = "create_days"
		sendMessage(bot, msg.Chat.ID, "⏳ Masukkan Durasi (Hari):")
	} else if state == "create_days" {
		d, _ := strconv.Atoi(msg.Text)
		createUser(bot, msg.Chat.ID, tempUserData[id]["username"], d)
		resetState(id)
	} else if state == "renew_days" {
		d, _ := strconv.Atoi(msg.Text)
		renewUser(bot, msg.Chat.ID, tempUserData[id]["username"], d)
		resetState(id)
	}
}

func showUserSelection(bot *tgbotapi.BotAPI, chatID int64, page int, action string) {
	users, _ := getUsers()
	if len(users) == 0 { sendMessage(bot, chatID, "📂 Kosong."); return }
	perPage := 10
	start := (page - 1) * perPage
	end := start + perPage
	if end > len(users) { end = len(users) }
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, u := range users[start:end] {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(u.Password, fmt.Sprintf("select_%s:%s", action, u.Password))))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("⬅️ Kembali", "cancel")))
	msg := tgbotapi.NewMessage(chatID, "Pilih User:")
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	sendAndTrack(bot, msg)
}

func apiCall(method, endpoint string, payload interface{}) (map[string]interface{}, error) {
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest(method, ApiUrl+endpoint, bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", ApiKey)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	var res map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&res)
	return res, nil
}

func getIpInfo() (IpInfo, error) {
	resp, _ := http.Get("http://ip-api.com/json/")
	var info IpInfo
	json.NewDecoder(resp.Body).Decode(&info)
	return info, nil
}

func getUsers() ([]UserData, error) {
	res, _ := apiCall("GET", "/users", nil)
	var u []UserData
	db, _ := json.Marshal(res["data"])
	json.Unmarshal(db, &u)
	return u, nil
}

func createUser(bot *tgbotapi.BotAPI, chatID int64, user string, days int) {
	apiCall("POST", "/user/create", map[string]interface{}{"password": user, "days": days})
	sendMessage(bot, chatID, "✅ Akun Dibuat.")
	showMainMenu(bot, chatID)
}

func createGenericTrialUser(bot *tgbotapi.BotAPI, chatID int64, days int) {
	pass := fmt.Sprintf("%d", 100000+rand.Intn(899999))
	apiCall("POST", "/user/create", map[string]interface{}{"password": pass, "days": days})
	sendMessage(bot, chatID, fmt.Sprintf("🚀 Trial %d Hari Berhasil: `%s`", days, pass))
	showMainMenu(bot, chatID)
}

func renewUser(bot *tgbotapi.BotAPI, chatID int64, user string, days int) {
	apiCall("POST", "/user/renew", map[string]interface{}{"password": user, "days": days})
	sendMessage(bot, chatID, "✅ Diperpanjang.")
	showMainMenu(bot, chatID)
}

func deleteUser(bot *tgbotapi.BotAPI, chatID int64, user string) {
	apiCall("POST", "/user/delete", map[string]interface{}{"password": user})
	sendMessage(bot, chatID, "🗑️ Dihapus.")
	showMainMenu(bot, chatID)
}

func listUsers(bot *tgbotapi.BotAPI, chatID int64) {
	users, _ := getUsers()
	txt := "📋 *LIST USER:*\n"
	for _, u := range users { txt += fmt.Sprintf("• `%s` (%s)\n", u.Password, u.Status) }
	sendMessage(bot, chatID, txt)
}

func systemInfo(bot *tgbotapi.BotAPI, chatID int64) {
	res, _ := apiCall("GET", "/info", nil)
	d := res["data"].(map[string]interface{})
	sendMessage(bot, chatID, fmt.Sprintf("⚙️ IP Server: `%s` \nLayanan: `%s`", d["public_ip"], d["service"]))
}

func autoDeleteExpiredUsers(bot *tgbotapi.BotAPI, adminID int64) {
	users, _ := getUsers()
	deleted := 0
	for _, u := range users {
		if strings.ToLower(u.Status) == "expired" {
			apiCall("POST", "/user/delete", map[string]interface{}{"password": u.Password})
			deleted++
		}
	}
	if deleted > 0 { bot.Send(tgbotapi.NewMessage(adminID, fmt.Sprintf("🧹 Auto-Clean: %d akun expired dihapus.", deleted))) }
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