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
	log.Printf("Authorized on account %s", bot.Self.UserName)

	// --- BACKGROUND WORKER (AUTO DELETE & AUTO BACKUP) ---
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

func handleMessage(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, adminID int64) {
	if msg.From.ID != adminID { return }

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
	if query.From.ID != adminID { return }

	switch {
	case strings.HasPrefix(query.Data, "menu_trial_"):
		days, _ := strconv.Atoi(strings.TrimPrefix(query.Data, "menu_trial_"))
		createGenericTrialUser(bot, query.Message.Chat.ID, days)
	case query.Data == "menu_create":
		userStates[query.From.ID] = "create_username"
		tempUserData[query.From.ID] = make(map[string]string)
		sendMessage(bot, query.Message.Chat.ID, "🔑 *CREATE USER*\nSilakan masukkan **PASSWORD**:")
	case query.Data == "menu_delete":
		showUserSelection(bot, query.Message.Chat.ID, 1, "delete")
	case query.Data == "menu_renew":
		showUserSelection(bot, query.Message.Chat.ID, 1, "renew")
	case query.Data == "menu_list":
		listUsers(bot, query.Message.Chat.ID)
	case query.Data == "menu_info":
		systemInfo(bot, query.Message.Chat.ID)
	case query.Data == "menu_backup":
		bot.Request(tgbotapi.NewCallback(query.ID, "Memproses Backup..."))
		autoBackup(bot, adminID)
	case query.Data == "menu_restore":
		userStates[query.From.ID] = "wait_restore_file"
		msg := tgbotapi.NewMessage(query.Message.Chat.ID, "📥 *RESTORE DATA*\nSilakan kirimkan file backup `.json` Anda:")
		msg.ParseMode = "Markdown"
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("❌ Batal Restore", "cancel")),
		)
		sendAndTrack(bot, msg)
	case query.Data == "menu_clear_expired":
		autoDeleteExpiredUsers(bot, adminID, false)
	case query.Data == "cancel":
		resetState(query.From.ID)
		showMainMenu(bot, query.Message.Chat.ID)
	case strings.HasPrefix(query.Data, "confirm_delete:"):
		deleteUser(bot, query.Message.Chat.ID, strings.TrimPrefix(query.Data, "confirm_delete:"))
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
		sendMessage(bot, msg.Chat.ID, "⏳ Masukkan **Durasi** (Hari):")
	case "create_days":
		tempUserData[userID]["days"] = text
		userStates[userID] = "create_iplimit"
		sendMessage(bot, msg.Chat.ID, "📱 Masukkan **Limit IP** (Contoh: 2):")
	case "create_iplimit":
		username := tempUserData[userID]["username"]
		days, _ := strconv.Atoi(tempUserData[userID]["days"])
		ipLimit, _ := strconv.Atoi(text)
		createUser(bot, msg.Chat.ID, username, days, ipLimit)
		resetState(userID)
	case "renew_days":
		days, _ := strconv.Atoi(text)
		renewUser(bot, msg.Chat.ID, tempUserData[userID]["username"], days)
		resetState(userID)
	}
}

func showMainMenu(bot *tgbotapi.BotAPI, chatID int64) {
	ipInfo, _ := getIpInfo()
	users, _ := getUsers()
	msgText := fmt.Sprintf("✨ *ZIVPN PANEL FINAL*\n\n• 🌐 ISP: `%s`\n• 📍 Lokasi: `%s`\n• 👤 Total: `%d` Akun", ipInfo.Isp, ipInfo.City, len(users))
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("➕ Buat Akun", "menu_create"), tgbotapi.NewInlineKeyboardButtonData("🚀 Trial 1 Hari", "menu_trial_1")),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🔄 Perpanjang", "menu_renew"), tgbotapi.NewInlineKeyboardButtonData("🗑️ Hapus User", "menu_delete")),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("📋 Daftar Akun", "menu_list"), tgbotapi.NewInlineKeyboardButtonData("📊 Info Server", "menu_info")),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("📂 Backup", "menu_backup"), tgbotapi.NewInlineKeyboardButtonData("📥 Restore", "menu_restore")),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🔥 Hapus Semua Expired", "menu_clear_expired")),
	)
	deleteLastMessage(bot, chatID)
	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(MenuPhotoURL))
	photo.Caption = msgText
	photo.ParseMode = "Markdown"
	photo.ReplyMarkup = keyboard
	sent, _ := bot.Send(photo)
	lastMessageIDs[chatID] = sent.MessageID
}

func systemInfo(bot *tgbotapi.BotAPI, chatID int64) {
	res, _ := apiCall("GET", "/info", nil)
	if res["success"] == true {
		d := res["data"].(map[string]interface{})
		msg := fmt.Sprintf("⚙️ *SERVER INFO*\n\n🌐 Domain: `%v`\n🔌 Public IP: `%v` \n🔧 Service: `%v`\n🔑 *API Key*: `%s`", 
			d["domain"], d["public_ip"], d["service"], ApiKey)
		sendMessage(bot, chatID, msg)
	}
	showMainMenu(bot, chatID)
}

func createUser(bot *tgbotapi.BotAPI, chatID int64, username string, days int, ipLimit int) {
	res, err := apiCall("POST", "/user/create", map[string]interface{}{"password": username, "days": days, "iplimit": ipLimit})
	if err == nil && res["success"] == true {
		data := res["data"].(map[string]interface{})
		msg := fmt.Sprintf("✅ *AKUN BERHASIL DIBUAT*\n\n🔑 Password: `%s`\n⏳ Durasi: `%d Hari`\n📱 Limit: `%d HP`\n🗓️ Expired: `%s`", data["password"], days, ipLimit, data["expired"])
		sendMessage(bot, chatID, msg)
	} else {
		sendMessage(bot, chatID, "❌ Gagal membuat akun. Periksa API Anda.")
	}
	showMainMenu(bot, chatID)
}

// --- FUNGSI API & HELPER ---
func apiCall(method, endpoint string, payload interface{}) (map[string]interface{}, error) {
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequest(method, ApiUrl+endpoint, bytes.NewBuffer(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", ApiKey)
	resp, err := (&http.Client{}).Do(req)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	var res map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&res)
	return res, nil
}

func getUsers() ([]UserData, error) {
	res, err := apiCall("GET", "/users", nil)
	if err != nil { return nil, err }
	var u []UserData
	b, _ := json.Marshal(res["data"])
	json.Unmarshal(b, &u)
	return u, nil
}

func getIpInfo() (IpInfo, error) {
	resp, _ := http.Get("http://ip-api.com/json/")
	var i IpInfo
	json.NewDecoder(resp.Body).Decode(&i)
	return i, nil
}

func autoDeleteExpiredUsers(bot *tgbotapi.BotAPI, adminID int64, silent bool) {
	users, _ := getUsers()
	dCount := 0
	for _, u := range users {
		if u.Status == "Expired" {
			res, _ := apiCall("POST", "/user/delete", map[string]interface{}{"password": u.Password})
			if res["success"] == true { dCount++ }
		}
	}
	if dCount > 0 && !silent {
		sendMessage(bot, adminID, fmt.Sprintf("🗑️ Berhasil menghapus `%d` akun expired.", dCount))
		showMainMenu(bot, adminID)
	}
}

func autoBackup(bot *tgbotapi.BotAPI, adminID int64) {
	users, _ := getUsers()
	jsonData, _ := json.MarshalIndent(users, "", "  ")
	file := tgbotapi.FileBytes{Name: "backup_" + time.Now().Format("2006-01-02") + ".json", Bytes: jsonData}
	doc := tgbotapi.NewDocument(adminID, file)
	doc.Caption = "📂 *Backup System*"
	bot.Send(doc)
}

func handleRestoreFile(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	url, _ := bot.GetFileDirectURL(msg.Document.FileID)
	resp, _ := http.Get(url)
	defer resp.Body.Close()
	var users []UserData
	json.NewDecoder(resp.Body).Decode(&users)
	s, f := 0, 0
	for _, u := range users {
		exp, _ := time.Parse("2006-01-02 15:04:05", u.Expired)
		days := int(time.Until(exp).Hours() / 24)
		if days < 1 { days = 1 }
		res, _ := apiCall("POST", "/user/create", map[string]interface{}{"password": u.Password, "days": days, "iplimit": 2})
		if res["success"] == true { s++ } else { f++ }
	}
	sendMessage(bot, msg.Chat.ID, fmt.Sprintf("✅ Restore Selesai\n🟢 Berhasil: %d\n🔴 Gagal: %d", s, f))
	showMainMenu(bot, msg.Chat.ID)
}

func createGenericTrialUser(bot *tgbotapi.BotAPI, chatID int64, days int) {
	pass := fmt.Sprintf("trial%d", rand.Intn(9999))
	createUser(bot, chatID, pass, days, 2)
}

func showUserSelection(bot *tgbotapi.BotAPI, chatID int64, page int, action string) {
	users, _ := getUsers()
	if len(users) == 0 { sendMessage(bot, chatID, "📂 Kosong."); return }
	start := (page - 1) * 10
	end := start + 10
	if end > len(users) { end = len(users) }
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, u := range users[start:end] {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(u.Password, "select_"+action+":"+u.Password)))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("❌ Batal", "cancel")))
	msg := tgbotapi.NewMessage(chatID, "Pilih User:")
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	sendAndTrack(bot, msg)
}

func deleteUser(bot *tgbotapi.BotAPI, chatID int64, username string) {
	apiCall("POST", "/user/delete", map[string]interface{}{"password": username})
	sendMessage(bot, chatID, "✅ User dihapus.")
	showMainMenu(bot, chatID)
}

func renewUser(bot *tgbotapi.BotAPI, chatID int64, username string, days int) {
	apiCall("POST", "/user/renew", map[string]interface{}{"password": username, "days": days})
	sendMessage(bot, chatID, "✅ Diperpanjang.")
	showMainMenu(bot, chatID)
}

func listUsers(bot *tgbotapi.BotAPI, chatID int64) {
	users, _ := getUsers()
	txt := "📋 *LIST USER*\n"
	for i, u := range users { txt += fmt.Sprintf("%d. %s [%s]\n", i+1, u.Password, u.Status) }
	sendMessage(bot, chatID, txt)
}

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
	f, _ := ioutil.ReadFile(BotConfigFile)
	json.Unmarshal(f, &c)
	return c, nil
}