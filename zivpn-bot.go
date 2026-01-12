package main

import (
	"bytes"
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
)

const (
	BotConfigFile      = "/etc/zivpn/bot-config.json"
	ApiUrl             = "http://127.0.0.1:8080/api"
	ApiKeyFile         = "/etc/zivpn/apikey"
	MenuPhotoURL       = "https://h.uguu.se/NgaOrSxG.png"
	AutoDeleteInterval = 1 * time.Minute
	AutoBackupInterval = 24 * time.Hour
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
	log.Printf("Authorized on account %s", bot.Self.UserName)

	// Background Worker
	go func() {
		autoDeleteExpiredUsers(bot, config.AdminID, true)
		autoBackup(bot, config.AdminID)

		deleteTicker := time.NewTicker(AutoDeleteInterval)
		backupTicker := time.NewTicker(AutoBackupInterval)

		for {
			select {
			case <-deleteTicker.C:
				autoDeleteExpiredUsers(bot, config.AdminID, true)
			case <-backupTicker.C:
				autoBackup(bot, config.AdminID)
			}
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

// --- HANDLERS ---

func handleMessage(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, adminID int64) {
	if msg.From.ID != adminID {
		return
	}

	state, exists := userStates[msg.From.ID]
	if exists {
		if state == "wait_restore_file" {
			if msg.Document != nil {
				handleRestoreFile(bot, msg)
				resetState(msg.From.ID)
			} else {
				sendMessage(bot, msg.Chat.ID, "❌ Mohon kirimkan file backup (.json).")
			}
			return
		}
		handleState(bot, msg, state)
		return
	}

	if msg.IsCommand() && msg.Command() == "start" {
		showMainMenu(bot, msg.Chat.ID)
	}
}

func handleCallback(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery, adminID int64) {
	if query.From.ID != adminID {
		return
	}

	data := query.Data
	chatID := query.Message.Chat.ID

	switch {
	case strings.HasPrefix(data, "menu_trial_"):
		days, _ := strconv.Atoi(strings.TrimPrefix(data, "menu_trial_"))
		createGenericTrialUser(bot, chatID, days)

	case data == "menu_create":
		userStates[adminID] = "create_username"
		tempUserData[adminID] = make(map[string]string)
		sendMessage(bot, chatID, "🔑 *CREATE USER*\nSilakan masukkan **PASSWORD/USERNAME**:")

	case data == "menu_delete":
		showUserSelection(bot, chatID, 1, "delete")

	case data == "menu_renew":
		showUserSelection(bot, chatID, 1, "renew")

	case data == "menu_list":
		listUsers(bot, chatID)

	case data == "menu_info":
		systemInfo(bot, chatID)

	case data == "menu_backup":
		bot.AnswerCallbackQuery(tgbotapi.NewCallback(query.ID, "Memproses Backup..."))
		autoBackup(bot, adminID)

	case data == "menu_restore":
		userStates[adminID] = "wait_restore_file"
		sendMessage(bot, chatID, "📥 *RESTORE DATA*\nSilakan kirimkan file backup `.json` Anda:")

	case data == "menu_clear_expired":
		bot.AnswerCallbackQuery(tgbotapi.NewCallback(query.ID, "Membersihkan akun expired..."))
		autoDeleteExpiredUsers(bot, adminID, false)

	case data == "cancel":
		resetState(adminID)
		showMainMenu(bot, chatID)

	case strings.HasPrefix(data, "select_delete:"):
		user := strings.TrimPrefix(data, "select_delete:")
		deleteUser(bot, chatID, user)

	case strings.HasPrefix(data, "select_renew:"):
		user := strings.TrimPrefix(data, "select_renew:")
		tempUserData[adminID] = map[string]string{"username": user}
		userStates[adminID] = "renew_days"
		sendMessage(bot, chatID, "⏳ Masukkan jumlah hari perpanjangan untuk `"+user+"`:")
	}
	bot.AnswerCallbackQuery(tgbotapi.NewCallback(query.ID, ""))
}

func handleState(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, state string) {
	userID := msg.From.ID
	text := strings.TrimSpace(msg.Text)

	switch state {
	case "create_username":
		tempUserData[userID]["username"] = text
		userStates[userID] = "create_days"
		sendMessage(bot, msg.Chat.ID, "⏳ Masukkan **Durasi** (Hari):")

	case "create_days":
		if _, err := strconv.Atoi(text); err != nil {
			sendMessage(bot, msg.Chat.ID, "❌ Gunakan angka untuk hari!")
			return
		}
		tempUserData[userID]["days"] = text
		userStates[userID] = "create_iplimit"
		sendMessage(bot, msg.Chat.ID, "📱 Masukkan **Limit IP** (Contoh: 2):")

	case "create_iplimit":
		days, _ := strconv.Atoi(tempUserData[userID]["days"])
		ipLimit, _ := strconv.Atoi(text)
		createUser(bot, msg.Chat.ID, tempUserData[userID]["username"], days, ipLimit)
		resetState(userID)

	case "renew_days":
		days, _ := strconv.Atoi(text)
		renewUser(bot, msg.Chat.ID, tempUserData[userID]["username"], days)
		resetState(userID)
	}
}

// --- CORE FUNCTIONS ---

func showMainMenu(bot *tgbotapi.BotAPI, chatID int64) {
	ipInfo, _ := getIpInfo()
	users, _ := getUsers()

	msgText := fmt.Sprintf("✨ *ZIVPN PANEL FINAL*\n\n• 🌐 ISP: `%s`\n• 📍 Lokasi: `%s`\n• 👤 Total: `%d` Akun", ipInfo.Isp, ipInfo.City, len(users))

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ Buat Akun", "menu_create"),
			tgbotapi.NewInlineKeyboardButtonData("🚀 Trial 1 Hari", "menu_trial_1"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Perpanjang", "menu_renew"),
			tgbotapi.NewInlineKeyboardButtonData("🗑️ Hapus User", "menu_delete"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 Daftar Akun", "menu_list"),
			tgbotapi.NewInlineKeyboardButtonData("📊 Info Server", "menu_info"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📂 Backup", "menu_backup"),
			tgbotapi.NewInlineKeyboardButtonData("📥 Restore", "menu_restore"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔥 Hapus Semua Expired", "menu_clear_expired"),
		),
	)

	deleteLastMessage(bot, chatID)
	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(MenuPhotoURL))
	photo.Caption = msgText
	photo.ParseMode = "Markdown"
	photo.ReplyMarkup = keyboard
	sent, _ := bot.Send(photo)
	lastMessageIDs[chatID] = sent.MessageID
}

func createUser(bot *tgbotapi.BotAPI, chatID int64, username string, days int, ipLimit int) {
	res, err := apiCall("POST", "/user/create", map[string]interface{}{
		"password": username,
		"days":     days,
		"iplimit":  ipLimit,
	})

	if err == nil && res["success"] == true {
		var expiredStr string
		if data, ok := res["data"].(map[string]interface{}); ok {
			expiredStr, _ = data["expired"].(string)
		}

		msg := fmt.Sprintf("✅ *AKUN BERHASIL DIBUAT*\n\n🔑 Password: `%s`\n⏳ Durasi: `%d Hari`\n📱 Limit: `%d HP`\n🗓️ Expired: `%s`", username, days, ipLimit, expiredStr)
		
		// Kirim detail akun tanpa menghapus pesan sebelumnya (agar bisa di-copy)
		finalMsg := tgbotapi.NewMessage(chatID, msg)
		finalMsg.ParseMode = "Markdown"
		bot.Send(finalMsg)
	} else {
		sendMessage(bot, chatID, "❌ Gagal membuat akun. Silakan cek API.")
	}
	time.Sleep(1 * time.Second)
	showMainMenu(bot, chatID)
}

func createGenericTrialUser(bot *tgbotapi.BotAPI, chatID int64, days int) {
	pass := fmt.Sprintf("trial%d", rand.Intn(9999))
	createUser(bot, chatID, pass, days, 2)
}

func renewUser(bot *tgbotapi.BotAPI, chatID int64, username string, days int) {
	res, _ := apiCall("POST", "/user/renew", map[string]interface{}{"password": username, "days": days})
	if res["success"] == true {
		sendMessage(bot, chatID, "✅ User `"+username+"` berhasil diperpanjang.")
	} else {
		sendMessage(bot, chatID, "❌ Gagal memperpanjang user.")
	}
	time.Sleep(1 * time.Second)
	showMainMenu(bot, chatID)
}

func deleteUser(bot *tgbotapi.BotAPI, chatID int64, username string) {
	res, _ := apiCall("POST", "/user/delete", map[string]interface{}{"password": username})
	if res["success"] == true {
		sendMessage(bot, chatID, "✅ User `"+username+"` berhasil dihapus.")
	} else {
		sendMessage(bot, chatID, "❌ Gagal menghapus user.")
	}
	time.Sleep(1 * time.Second)
	showMainMenu(bot, chatID)
}

// --- API & UTILS ---

func apiCall(method, endpoint string, payload interface{}) (map[string]interface{}, error) {
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequest(method, ApiUrl+endpoint, bytes.NewBuffer(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", ApiKey)
	
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var res map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&res)
	return res, nil
}

func getUsers() ([]UserData, error) {
	res, err := apiCall("GET", "/users", nil)
	if err != nil {
		return nil, err
	}
	var u []UserData
	if data, ok := res["data"]; ok {
		b, _ := json.Marshal(data)
		json.Unmarshal(b, &u)
	}
	return u, nil
}

func getIpInfo() (IpInfo, error) {
	resp, err := http.Get("http://ip-api.com/json/")
	if err != nil {
		return IpInfo{City: "Unknown", Isp: "Unknown"}, err
	}
	defer resp.Body.Close()
	var i IpInfo
	json.NewDecoder(resp.Body).Decode(&i)
	return i, nil
}

func listUsers(bot *tgbotapi.BotAPI, chatID int64) {
	users, _ := getUsers()
	if len(users) == 0 {
		sendMessage(bot, chatID, "📂 Belum ada akun terdaftar.")
		showMainMenu(bot, chatID)
		return
	}
	txt := "📋 *DAFTAR AKUN ZIVPN*\n\n"
	for i, u := range users {
		icon := "🟢"
		if u.Status == "Expired" {
			icon = "🔴"
		}
		txt += fmt.Sprintf("%d. %s `%s` | Exp: `%s`\n", i+1, icon, u.Password, u.Expired)
	}
	sendMessage(bot, chatID, txt)
	time.Sleep(2 * time.Second)
	showMainMenu(bot, chatID)
}

func systemInfo(bot *tgbotapi.BotAPI, chatID int64) {
	res, _ := apiCall("GET", "/info", nil)
	if res != nil && res["success"] == true {
		d := res["data"].(map[string]interface{})
		msg := fmt.Sprintf("⚙️ *SYSTEM INFO*\n\n🌐 Domain: `%v`\n🔌 Public IP: `%v` \n🔧 Service: `%v`", d["domain"], d["public_ip"], d["service"])
		sendMessage(bot, chatID, msg)
	} else {
		sendMessage(bot, chatID, "❌ Gagal mengambil info sistem.")
	}
	time.Sleep(2 * time.Second)
	showMainMenu(bot, chatID)
}

func showUserSelection(bot *tgbotapi.BotAPI, chatID int64, page int, action string) {
	users, _ := getUsers()
	if len(users) == 0 {
		sendMessage(bot, chatID, "📂 Data user kosong.")
		showMainMenu(bot, chatID)
		return
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, u := range users {
		icon := "🟢"
		if u.Status == "Expired" {
			icon = "🔴"
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(icon+" "+u.Password, "select_"+action+":"+u.Password),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("❌ Batal", "cancel")))

	msg := tgbotapi.NewMessage(chatID, "👇 Pilih User untuk "+action+":")
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	sendAndTrack(bot, msg)
}

// --- AUTOMATION ---

func autoDeleteExpiredUsers(bot *tgbotapi.BotAPI, adminID int64, silent bool) {
	users, err := getUsers()
	if err != nil { return }
	deletedCount := 0
	for _, u := range users {
		if u.Status == "Expired" {
			res, _ := apiCall("POST", "/user/delete", map[string]interface{}{"password": u.Password})
			if res["success"] == true { deletedCount++ }
		}
	}
	if !silent && deletedCount > 0 {
		sendMessage(bot, adminID, fmt.Sprintf("🗑️ Berhasil menghapus `%d` akun expired.", deletedCount))
		showMainMenu(bot, adminID)
	}
}

func autoBackup(bot *tgbotapi.BotAPI, adminID int64) {
	users, err := getUsers()
	if err != nil { return }
	jsonData, _ := json.MarshalIndent(users, "", "  ")
	file := tgbotapi.FileBytes{Name: "backup_" + time.Now().Format("2006-01-02") + ".json", Bytes: jsonData}
	bot.Send(tgbotapi.NewDocument(adminID, file))
}

func handleRestoreFile(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	url, _ := bot.GetFileDirectURL(msg.Document.FileID)
	resp, _ := http.Get(url)
	defer resp.Body.Close()
	var users []UserData
	json.NewDecoder(resp.Body).Decode(&users)
	for _, u := range users {
		apiCall("POST", "/user/create", map[string]interface{}{"password": u.Password, "days": 30, "iplimit": 2})
	}
	sendMessage(bot, msg.Chat.ID, "✅ Restore selesai.")
	showMainMenu(bot, msg.Chat.ID)
}

// --- HELPERS ---

func sendMessage(bot *tgbotapi.BotAPI, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	sendAndTrack(bot, msg)
}

func sendAndTrack(bot *tgbotapi.BotAPI, msg tgbotapi.MessageConfig) {
	deleteLastMessage(bot, msg.ChatID)
	sent, _ := bot.Send(msg)
	lastMessageIDs[msg.ChatID] = sent.MessageID
}

func deleteLastMessage(bot *tgbotapi.BotAPI, chatID int64) {
	if id, ok := lastMessageIDs[chatID]; ok {
		bot.Request(tgbotapi.NewDeleteMessage(chatID, id))
	}
}

func resetState(id int64) { delete(userStates, id); delete(tempUserData, id) }

func loadConfig() (BotConfig, error) {
	var c BotConfig
	f, err := ioutil.ReadFile(BotConfigFile)
	if err != nil { return c, err }
	json.Unmarshal(f, &c)
	return c, nil
}