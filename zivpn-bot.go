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
	BotConfigFile = "/etc/zivpn/bot-config.json"
	ApiUrl        = "http://127.0.0.1:8080/api"
	ApiKeyFile    = "/etc/zivpn/apikey"
	// !!! GANTI INI DENGAN URL GAMBAR MENU ANDA !!!
	MenuPhotoURL = "https://h.uguu.se/ePURTlNf.jpg"

	// Interval untuk pengecekan dan penghapusan akun expired (diubah menjadi 1 menit)
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
	log.Printf("Authorized on account %s", bot.Self.UserName)

	// --- BACKGROUND WORKER (PENGHAPUSAN OTOMATIS) ---
	go func() {
		// Jalankan sekali saat startup
		autoDeleteExpiredUsers(bot, config.AdminID)

		// Buat Ticker untuk berjalan setiap interval (1 menit)
		ticker := time.NewTicker(AutoDeleteInterval)
		for range ticker.C {
			autoDeleteExpiredUsers(bot, config.AdminID)
		}
	}()
	// ------------------------------------------------

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
	case query.Data == "menu_trial": // Handler untuk Trial 30 Menit
		createTrialUser(bot, query.Message.Chat.ID)

	case query.Data == "menu_create_dynamic": // Handler untuk Buat Akun (Dynamic)
		userStates[query.From.ID] = "create_username"
		tempUserData[query.From.ID] = make(map[string]string)
		sendMessage(bot, query.Message.Chat.ID, "🔑 *MENU CREATE (Dynamic 1 IP)*\nSilakan masukkan **PASSWORD** yang diinginkan:")

	case strings.HasPrefix(query.Data, "fixed_create:"): // Handler untuk Buat Akun (Fixed 2 IP)
		// Format: fixed_create:DAYS_MAXIP (e.g., fixed_create:15_2)
		parts := strings.Split(strings.TrimPrefix(query.Data, "fixed_create:"), "_")
		if len(parts) == 2 {
			days, maxIP := parts[0], parts[1]
			
			// Simpan durasi dan max_ip di tempUserData
			tempUserData[query.From.ID] = map[string]string{
				"days": days,
				"max_ip": maxIP,
			}
			userStates[query.From.ID] = "fixed_create_username"
			
			sendMessage(bot, query.Message.Chat.ID, fmt.Sprintf("🔑 *CREATE %s HARI (%s IP)*\nSilakan masukkan **PASSWORD** yang diinginkan:", days, maxIP))
		}

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
	case "create_username": // Flow Dynamic (1 IP default)
		tempUserData[userID]["username"] = text
		userStates[userID] = "create_days"
		sendMessage(bot, msg.Chat.ID, fmt.Sprintf("⏳ *CREATE USER (1 IP)*\nPassword: `%s`\nMasukkan **Durasi** (*Hari*) pembuatan:", text))

	case "create_days": // Flow Dynamic (1 IP default)
		days, err := strconv.Atoi(text)
		if err != nil {
			sendMessage(bot, msg.Chat.ID, "❌ Durasi harus angka. Coba lagi:")
			return
		}
		// Memanggil fungsi createUser (default: 1 IP)
		createUser(bot, msg.Chat.ID, tempUserData[userID]["username"], days) 
		resetState(userID)

	case "fixed_create_username": // Flow Fixed IP (2 IP)
		username := text
		days, _ := strconv.Atoi(tempUserData[userID]["days"])
		maxIP, _ := strconv.Atoi(tempUserData[userID]["max_ip"])

		// Memanggil fungsi baru untuk fixed IP
		createUserWithMaxIP(bot, msg.Chat.ID, username, days, maxIP) 
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

	// Ambil Total Akun
	totalUsers := 0
	if users, err := getUsers(); err == nil {
		totalUsers = len(users)
	}

	// --- Ambil User yang Akan Segera Kedaluwarsa (24 Jam) ---
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
	// ----------------------------------------------------

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

	// Hapus pesan
	deleteLastMessage(bot, chatID)

	// Buat keyboard inline
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		// Row 1: Trial & Dynamic Create
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🚀 Trial 30 Menit", "menu_trial"),
			tgbotapi.NewInlineKeyboardButtonData("➕ Buat Akun (Dynamic 1 IP)", "menu_create_dynamic"),
		),
		// Row 2: Fixed 2 IP Options (15 & 30 Hari)
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("15 Hari (2 IP)", "fixed_create:15_2"),
			tgbotapi.NewInlineKeyboardButtonData("30 Hari (2 IP)", "fixed_create:30_2"),
		),
		// Row 3: Fixed 2 IP Options (60 & 90 Hari)
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("60 Hari (2 IP)", "fixed_create:60_2"),
			tgbotapi.NewInlineKeyboardButtonData("90 Hari (2 IP)", "fixed_create:90_2"),
		),
		// Row 4: Management
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Renew Akun", "menu_renew"),
			tgbotapi.NewInlineKeyboardButtonData("🗑️ Hapus Akun", "menu_delete"),
		),
		// Row 5: Info
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 Daftar Akun", "menu_list"),
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

// Fungsi untuk men-generate string acak sederhana
func generateRandomPassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	seededRand := rand.New(rand.NewSource(time.Now().UnixNano()))
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[seededRand.Intn(len(charset))]
	}
	return string(b)
}

// Fungsi Background Worker untuk menghapus akun expired secara otomatis
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
			// Memanggil endpoint delete API
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

	// Kirim notifikasi ke Admin jika ada akun yang dihapus
	if deletedCount > 0 {
		msgText := fmt.Sprintf("🗑️ *PEMBERSIHAN AKUN OTOMATIS*\n\n" +
			"Total `%d` akun kedaluwarsa telah dihapus secara otomatis:\n- %s",
			deletedCount, strings.Join(deletedUsers, "\n- "))

		notification := tgbotapi.NewMessage(adminID, msgText)
		notification.ParseMode = "Markdown"
		bot.Send(notification)
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

// Fungsi untuk mendapatkan user yang akan segera expired (dalam 24 jam)
func getNearExpiredUsers() ([]UserData, error) {
	users, err := getUsers()
	if err != nil {
		return nil, err
	}

	var nearExpired []UserData
	// Tentukan batas waktu: 24 jam dari sekarang
	expiryThreshold := time.Now().Add(24 * time.Hour)

	for _, u := range users {
		// Asumsi format expired: "YYYY-MM-DD hh:mm:ss"
		expiredTime, err := time.Parse("2006-01-02 15:04:05", u.Expired)
		if err != nil {
			continue
		}

		// Cek apakah waktu expired di masa depan DAN dalam 24 jam dari sekarang
		if expiredTime.After(time.Now()) && expiredTime.Before(expiryThreshold) {
			nearExpired = append(nearExpired, u)
		}
	}

	return nearExpired, nil
}

// Fungsi untuk membuat user dengan durasi dinamis (default: 1 IP)
func createUser(bot *tgbotapi.BotAPI, chatID int64, username string, days int) {
	res, err := apiCall("POST", "/user/create", map[string]interface{}{
		"password": username,
		"days":     days,
		// max_ip dihilangkan, API diasumsikan default ke 1
	})

	if err != nil {
		sendMessage(bot, chatID, "❌ Error API: "+err.Error())
		return
	}

	if res["success"] == true {
		data := res["data"].(map[string]interface{})

		ipInfo, _ := getIpInfo()

		msg := fmt.Sprintf("🎉 *AKUN BERHASIL DIBUAT (1 IP)*\n" +
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
		errMsg, ok := res["message"].(string)
		if !ok {
			errMsg = "Pesan error tidak diketahui dari API."
		}
		sendMessage(bot, chatID, fmt.Sprintf("❌ Gagal: %s", errMsg))
		showMainMenu(bot, chatID)
	}
}

// FUNGSI BARU: Membuat user dengan batasan IP tetap (2 IP)
func createUserWithMaxIP(bot *tgbotapi.BotAPI, chatID int64, password string, days int, maxIP int) {
	res, err := apiCall("POST", "/user/create", map[string]interface{}{
		"password": password,
		"days":     days,
		"max_ip":   maxIP, // Parameter baru
	})

	if err != nil {
		sendMessage(bot, chatID, "❌ Error API: "+err.Error())
		return
	}

	if res["success"] == true {
		data := res["data"].(map[string]interface{})

		ipInfo, _ := getIpInfo()

		// Fallback untuk memastikan Domain terisi
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

		msg := fmt.Sprintf("🎉 *AKUN BERHASIL DIBUAT (%d IP)*\n" +
			"━━━━━━━━━━━━━━━━━━━━━━━━━\n" +
			"🔑 *Password*: `%s`\n" +
			"🌐 *Domain*: `%s`\n" +
			"🗓️ *Kadaluarsa*: `%s`\n" +
			"📍 *Lokasi Server*: `%s`\n" +
			"📡 *ISP Server*: `%s`\n" +
			"━━━━━━━━━━━━━━━━━━━━━━━━━",
			maxIP, password, domain, data["expired"], ipInfo.City, ipInfo.Isp)

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

// FUNGSI INI DIUBAH KEMBALI KE DURASI 30 MENIT
func createTrialUser(bot *tgbotapi.BotAPI, chatID int64) {
	trialPassword := generateRandomPassword(8)

	// Perubahan: Menggunakan "minutes": 30
	res, err := apiCall("POST", "/user/create", map[string]interface{}{
		"password": trialPassword,
		"minutes":  30, 
		"days":     0,
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

		// --- EKSTRAKSI DATA DENGAN PENGECEKAN TIPE (ROBUST) ---
		ipInfo, _ := getIpInfo()

		password := "N/A"
		if p, ok := data["password"].(string); ok {
			password = p
		}

		expired := "N/A"
		if e, ok := data["expired"].(string); ok {
			expired = e
		}

		// Ambil Domain (Prioritas 1: dari respons create)
		domain := "Unknown"
		if d, ok := data["domain"].(string); ok && d != "" {
			domain = d
		} else {
			// Prioritas 2: Fallback dengan memanggil /info API
			if infoRes, err := apiCall("GET", "/info", nil); err == nil && infoRes["success"] == true {
				if infoData, ok := infoRes["data"].(map[string]interface{}); ok {
					if d, ok := infoData["domain"].(string); ok {
						domain = d
					}
				}
			}
		}
		// --- END EKSTRAKSI DATA ---

		// 3. Susun dan Kirim Pesan Sukses
		msg := fmt.Sprintf("🚀 *TRIAL 30 MENIT BERHASIL DIBUAT*\n" +
			"━━━━━━━━━━━━━━━━━━━━━━━━━\n" +
			"🔑 *Password*: `%s`\n" +
			"🌐 *Domain*: `%s`\n" +
			"⏳ *Durasi*: `30 Menit`\n" +
			"🗓️ *Kadaluarsa*: `%s`\n" +
			"📍 *Lokasi Server*: `%s`\n" +
			"📡 *ISP Server*: `%s`\n" +
			"━━━━━━━━━━━━━━━━━━━━━━━━━\n" +
			"❗️ *PERHATIAN: Trial ini hanya berlaku 30 menit!*",
			password, domain, expired, ipInfo.City, ipInfo.Isp)

		reply := tgbotapi.NewMessage(chatID, msg)
		reply.ParseMode = "Markdown"
		deleteLastMessage(bot, chatID)
		bot.Send(reply)
		showMainMenu(bot, chatID)
	} else {
		// 4. Penanganan Kegagalan API
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