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
	
	// URL GAMBAR MENU UTAMA
	MenuPhotoURL = "https://h.uguu.se/NgaOrSxG.png"
	
	// --- CONFIG QRIS ---
	QRIS_DATA   = "00020101021126670016COM.NOBUBANK.WWW01189360050300000879140214518329202796940303UMI51440014ID.CO.QRIS.WWW0215ID20222259294980303UMI5204481253033605802ID5909RIS STORE6011TASIKMALAYA61054611162070703A016304D2FC"
	
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
	rand.Seed(time.Now().UnixNano())

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

	// --- BACKGROUND WORKER ---
	go func() {
		for range time.Tick(AutoDeleteInterval) {
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

// --- HELPER UI ---

func sendTyping(bot *tgbotapi.BotAPI, chatID int64) {
	bot.Send(tgbotapi.NewChatAction(chatID, tgbotapi.ChatActionTyping))
}

func deleteLastMessage(bot *tgbotapi.BotAPI, chatID int64) {
	if id, ok := lastMessageIDs[chatID]; ok {
		bot.Send(tgbotapi.NewDeleteMessage(chatID, id))
		delete(lastMessageIDs, chatID)
	}
}

// --- MESSAGE HANDLER ---

func handleMessage(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, adminID int64) {
	userID := msg.From.ID
	sendTyping(bot, msg.Chat.ID)

	state, exists := userStates[userID]
	if exists && state == "waiting_for_receipt" {
		if msg.Photo != nil {
			photo := msg.Photo[len(msg.Photo)-1]
			daysStr := tempUserData[userID]["raw_days"]
			
			adminNotif := fmt.Sprintf("📸 *BUKTI PEMBAYARAN MASUK*\n\n"+
				"👤 User: `%s` (ID: `%d`)\n"+
				"📦 Paket: %s Hari\n"+
				"💰 Nominal: %s\n\n"+
				"Klik tombol di bawah untuk proses otomatis.", 
				msg.From.UserName, userID, daysStr, tempUserData[userID]["pkg_price"])

			forward := tgbotapi.NewPhoto(adminID, tgbotapi.FileID(photo.FileID))
			forward.Caption = adminNotif
			forward.ParseMode = "Markdown"
			forward.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("✅ SETUJUI & KIRIM AKUN", fmt.Sprintf("approve:%d:%s", userID, daysStr)),
					tgbotapi.NewInlineKeyboardButtonData("❌ TOLAK", "cancel"),
				),
			)
			bot.Send(forward)

			bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "✅ *Bukti terkirim!* Mohon tunggu sebentar, Admin akan segera memverifikasi."))
			resetState(userID)
			return
		}
		bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "❌ Mohon kirimkan bukti dalam bentuk Foto."))
		return
	}

	if userID == adminID {
		if exists { handleState(bot, msg, state); return }
		if msg.IsCommand() && msg.Command() == "start" { showMainMenu(bot, msg.Chat.ID) }
		return
	}

	if msg.IsCommand() && msg.Command() == "start" {
		showUserMenu(bot, msg.Chat.ID)
	}
}

// --- CALLBACK HANDLER ---

func handleCallback(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery, adminID int64) {
	chatID := query.Message.Chat.ID
	msgID := query.Message.MessageID
	userID := query.From.ID

	// --- AUTO-APPROVE LOGIC ---
	if strings.HasPrefix(query.Data, "approve:") {
		if userID != adminID { return }
		bot.Request(tgbotapi.NewCallback(query.ID, "Memproses Akun..."))
		
		parts := strings.Split(query.Data, ":")
		targetUserID, _ := strconv.ParseInt(parts[1], 10, 64)
		days, _ := strconv.Atoi(parts[2])
		
		newPass := fmt.Sprintf("RIS-%d", 1000+rand.Intn(8999))
		res, err := apiCall("POST", "/user/create", map[string]interface{}{"password": newPass, "days": days})
		
		if err == nil && res["success"] == true {
			data := res["data"].(map[string]interface{})
			ip, _ := getIpInfo()
			
			userMsg := fmt.Sprintf("🎉 *PEMBAYARAN DISETUJUI!*\n\n"+
				"🔑 *Password*: `%s`\n"+
				"🌐 *Domain*: `%s`\n"+
				"🗓️ *Expired*: %d Hari\n"+
				"📍 *Server*: %s\n\n"+
				"Terima kasih telah berlangganan!", 
				newPass, data["domain"], days, ip.City)
			
			bot.Send(tgbotapi.NewMessage(targetUserID, userMsg))
			
			// Update pesan admin agar tombol hilang
			edit := tgbotapi.NewEditMessageCaption(chatID, msgID, fmt.Sprintf("✅ Akun `%s` berhasil dikirim ke User `%d`.", newPass, targetUserID))
			bot.Send(edit)
		} else {
			bot.Send(tgbotapi.NewMessage(adminID, "❌ Gagal membuat akun. Periksa API server!"))
		}
		return
	}

	// --- USER BUY LOGIC ---
	switch {
	case query.Data == "user_buy_premium":
		deleteLastMessage(bot, chatID)
		msgText := "🛒 *PILIH PAKET PREMIUM*"
		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("⭐ 15 Hari - Rp 6k", "buy_pkg:15:6000")),
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🌟 30 Hari - Rp 12k", "buy_pkg:30:12000")),
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("⬅️ Kembali", "user_back")),
		)
		photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(MenuPhotoURL))
		photo.Caption = msgText
		photo.ReplyMarkup = kb
		sent, _ := bot.Send(photo)
		lastMessageIDs[chatID] = sent.MessageID

	case strings.HasPrefix(query.Data, "buy_pkg:"):
		parts := strings.Split(query.Data, ":")
		qrURL := fmt.Sprintf("https://api.qrserver.com/v1/create-qr-code/?size=300x300&data=%s", url.QueryEscape(QRIS_DATA))
		deleteLastMessage(bot, chatID)
		
		qrisMsg := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(qrURL))
		qrisMsg.Caption = fmt.Sprintf("💳 *PEMBAYARAN QRIS*\nPaket: %s Hari\nTotal: Rp %s\n\nSilakan transfer dan upload bukti foto.", parts[1], parts[2])
		qrisMsg.ParseMode = "Markdown"
		qrisMsg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("✅ SAYA SUDAH BAYAR", "confirm_pay:"+parts[1]+":"+parts[2])),
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("❌ Batal", "user_back")),
		)
		sent, _ := bot.Send(qrisMsg)
		lastMessageIDs[chatID] = sent.MessageID

	case strings.HasPrefix(query.Data, "confirm_pay:"):
		parts := strings.Split(query.Data, ":")
		userStates[userID] = "waiting_for_receipt"
		tempUserData[userID] = map[string]string{"raw_days": parts[1], "pkg_price": "Rp " + parts[2]}
		deleteLastMessage(bot, chatID)
		bot.Send(tgbotapi.NewMessage(chatID, "📸 Silakan kirimkan *Foto Bukti Transfer* Anda."))

	case query.Data == "user_back":
		deleteLastMessage(bot, chatID)
		showUserMenu(bot, chatID)

	// --- ADMIN LOGIC ---
	case query.Data == "menu_trial_1": createGenericTrialUser(bot, chatID, 1)
	case query.Data == "menu_trial_15": createGenericTrialUser(bot, chatID, 15)
	case query.Data == "menu_trial_30": createGenericTrialUser(bot, chatID, 30)
	case query.Data == "menu_create":
		userStates[userID] = "create_username"
		sendMessage(bot, chatID, "🔑 *CREATE USER*\nMasukkan **PASSWORD**:")
	case query.Data == "menu_list": listUsers(bot, chatID)
	case query.Data == "menu_delete": showUserSelection(bot, chatID, 1, "delete")
	case query.Data == "cancel": 
		resetState(userID)
		showMainMenu(bot, chatID)
	}
	bot.Request(tgbotapi.NewCallback(query.ID, ""))
}

// --- UI FUNCTIONS ---

func showUserMenu(bot *tgbotapi.BotAPI, chatID int64) {
	deleteLastMessage(bot, chatID)
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🛒 Beli Akun Premium", "user_buy_premium")),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonURL("👨‍💻 Hubungi Admin", "https://t.me/JesVpnt")),
	)
	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(MenuPhotoURL))
	photo.Caption = "👋 *WELCOME TO RIS STORE*\nBeli akun VPN Premium dengan mudah dan otomatis."
	photo.ReplyMarkup = kb
	sent, _ := bot.Send(photo)
	lastMessageIDs[chatID] = sent.MessageID
}

func showMainMenu(bot *tgbotapi.BotAPI, chatID int64) {
	deleteLastMessage(bot, chatID)
	ip, _ := getIpInfo()
	total := 0
	if u, err := getUsers(); err == nil { total = len(u) }
	
	msgText := fmt.Sprintf("✨ *ADMIN PANEL RIS STORE*\n\n📍 Lokasi: `%s` \n👤 Akun Aktif: `%d` \n⚡ Status: `Fast Response`", ip.City, total)
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("➕ Buat Akun", "menu_create"), tgbotapi.NewInlineKeyboardButtonData("🚀 Trial 1D", "menu_trial_1")),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("⭐ 15 Hari", "menu_trial_15"), tgbotapi.NewInlineKeyboardButtonData("🌟 30 Hari", "menu_trial_30")),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("📋 Daftar", "menu_list"), tgbotapi.NewInlineKeyboardButtonData("🗑️ Hapus", "menu_delete")),
	)
	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(MenuPhotoURL))
	photo.Caption = msgText
	photo.ParseMode = "Markdown"
	photo.ReplyMarkup = kb
	sent, _ := bot.Send(photo)
	lastMessageIDs[chatID] = sent.MessageID
}

// --- API & LOGIC ---

func handleState(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, state string) {
	id := msg.From.ID
	text := strings.TrimSpace(msg.Text)
	if state == "create_username" {
		tempUserData[id] = map[string]string{"username": text}
		userStates[id] = "create_days"
		sendMessage(bot, msg.Chat.ID, "⏳ Masukkan Durasi (Hari):")
	} else if state == "create_days" {
		d, _ := strconv.Atoi(text)
		createUser(bot, msg.Chat.ID, tempUserData[id]["username"], d)
		resetState(id)
	}
}

func apiCall(method, endpoint string, payload interface{}) (map[string]interface{}, error) {
	var body []byte
	if payload != nil { body, _ = json.Marshal(payload) }
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
	if resp != nil { json.NewDecoder(resp.Body).Decode(&info) }
	return info, nil
}

func getUsers() ([]UserData, error) {
	res, _ := apiCall("GET", "/users", nil)
	var u []UserData
	if res != nil {
		db, _ := json.Marshal(res["data"])
		json.Unmarshal(db, &u)
	}
	return u, nil
}

func createUser(bot *tgbotapi.BotAPI, chatID int64, user string, days int) {
	apiCall("POST", "/user/create", map[string]interface{}{"password": user, "days": days})
	sendMessage(bot, chatID, "✅ Akun Berhasil Dibuat.")
	showMainMenu(bot, chatID)
}

func createGenericTrialUser(bot *tgbotapi.BotAPI, chatID int64, days int) {
	pass := fmt.Sprintf("%d", 100000+rand.Intn(899999))
	apiCall("POST", "/user/create", map[string]interface{}{"password": pass, "days": days})
	sendMessage(bot, chatID, fmt.Sprintf("🚀 Trial %d Hari Berhasil: `%s` ", days, pass))
	showMainMenu(bot, chatID)
}

func listUsers(bot *tgbotapi.BotAPI, chatID int64) {
	users, _ := getUsers()
	txt := "📋 *LIST USER:*\n"
	for _, u := range users { txt += fmt.Sprintf("• `%s` (%s)\n", u.Password, u.Status) }
	sendMessage(bot, chatID, txt)
}

func autoDeleteExpiredUsers(bot *tgbotapi.BotAPI, adminID int64) {
	users, _ := getUsers()
	deletedCount := 0
	for _, u := range users {
		if strings.ToLower(u.Status) == "expired" {
			res, _ := apiCall("POST", "/user/delete", map[string]interface{}{"password": u.Password})
			if res != nil && res["success"] == true { deletedCount++ }
		}
	}
	if deletedCount > 0 {
		bot.Send(tgbotapi.NewMessage(adminID, fmt.Sprintf("🧹 *CLEANUP:* `%d` akun expired dihapus.", deletedCount)))
	}
}

func showUserSelection(bot *tgbotapi.BotAPI, chatID int64, page int, action string) {
	users, _ := getUsers()
	if len(users) == 0 { sendMessage(bot, chatID, "📂 Kosong."); return }
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, u := range users {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(u.Password, fmt.Sprintf("confirm_delete:%s", u.Password))))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("⬅️ Kembali", "cancel")))
	msg := tgbotapi.NewMessage(chatID, "🗑️ *PILIH AKUN UNTUK DIHAPUS:*")
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	bot.Send(msg)
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