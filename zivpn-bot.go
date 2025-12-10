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
	
	// --- TAMBAHAN HARGA AKUN ---
	HARGA_30_HARI_IDR = 12000
	HARGA_5000_IDR   = 5000 // <-- HARGA BARU
	HARI_5000        = 12   // <-- HARI BARU
	HARGA_8000_IDR   = 8000 // <-- HARGA BARU
	HARI_8000        = 20   // <-- HARI BARU
	
	ADMIN_TELEGRAM_ID = "7545471466" 
	QRIS_PHOTO_URL    = "https://o.uguu.se/WRYXatAe.png" 
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
	case query.Data == "menu_topup": 
		userStates[query.From.ID] = "topup_nominal" 
		
		msgText := fmt.Sprintf("💸 *MENU TOP UP MANUAL*\n\n" +
			"Silakan lakukan pembayaran via QRIS di bawah. \n" +
			"Setelah transfer, masukkan **nominal top up** (contoh: `12000`) untuk konfirmasi admin atau membuat akun 30 hari:")
		
		deleteLastMessage(bot, query.Message.Chat.ID) 

		photoMsg := tgbotapi.NewPhoto(query.Message.Chat.ID, tgbotapi.FileURL(QRIS_PHOTO_URL))
		photoMsg.Caption = msgText
		photoMsg.ParseMode = "Markdown"

		cancelKb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("❌ Batal", "cancel")),
		)
		photoMsg.ReplyMarkup = cancelKb
		
		sentMsg, err := bot.Send(photoMsg)
		if err == nil {
			lastMessageIDs[query.Message.Chat.ID] = sentMsg.MessageID
		} else {
			log.Printf("Gagal mengirim foto QRIS (%s): %v. Mengirim sebagai teks biasa.", QRIS_PHOTO_URL, err)
			sendMessage(bot, query.Message.Chat.ID, msgText)
		}
		
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
	case strings.HasPrefix(query.Data, "admin_confirm_topup:"):
		parts := strings.Split(query.Data, ":")
		targetID, _ := strconv.ParseInt(parts[1], 10, 64)
		nominal, _ := strconv.Atoi(parts[2])
		confirmTopup(bot, query, targetID, nominal, true)
	case strings.HasPrefix(query.Data, "admin_cancel_topup:"):
		parts := strings.Split(query.Data, ":")
		targetID, _ := strconv.ParseInt(parts[1], 10, 64)
		nominal, _ := strconv.Atoi(parts[2])
		confirmTopup(bot, query, targetID, nominal, false)
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
        
    case "topup_nominal": 
        nominal, err := strconv.Atoi(text)
        if err != nil {
            sendMessage(bot, msg.Chat.ID, "❌ Nominal harus angka. Coba lagi:")
            return
        }
        
        var autoDays int
        
        // Cek nominal 12000 (30 hari)
        if nominal == HARGA_30_HARI_IDR {
            autoDays = 30
        } else if nominal == HARGA_5000_IDR { // <-- OPSI BARU
            autoDays = HARI_5000 
        } else if nominal == HARGA_8000_IDR { // <-- OPSI BARU
            autoDays = HARI_8000
        }
        
        if autoDays > 0 {
             // Lanjutkan ke state input password untuk auto-create
             tempUserData[userID] = map[string]string{"nominal": text, "days": strconv.Itoa(autoDays)}
             userStates[userID] = "create_username_auto"
             sendMessage(bot, msg.Chat.ID, fmt.Sprintf("✅ Nominal **Rp %d** (%d Hari) diterima.\nMembuat akun.\nSilakan masukkan **PASSWORD** yang diinginkan:", nominal, autoDays))
        } else {
            // Lanjutkan ke admin confirmation untuk nominal lain
            notifyAdminTopup(bot, msg.From.ID, msg.From.UserName, nominal, msg.Chat.ID)
            sendMessage(bot, msg.ChatID, fmt.Sprintf("⏳ Permintaan Top Up **Rp %d** berhasil dikirim ke admin. Mohon tunggu konfirmasi.", nominal))
            resetState(userID)
        }
    
    case "create_username_auto":
        daysStr, _ := tempUserData[userID]["days"]
        days, _ := strconv.Atoi(daysStr)
        
        tempUserData[userID]["username"] = text
        createUser(bot, msg.Chat.ID, tempUserData[userID]["username"], days) 
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

	msgText := fmt.Sprintf("✨ *WELCOME TO BOT PGETUNNEL UDP ZIVPN*\n\n" +
		"Server Info:\n" +
		"•  🌐 *Domain*: `%s`\n" +
		"•  📍 *Lokasi*: `%s`\n" +
		"•  📡 *ISP*: `%s`\n" +
        "•  👤 *Total Akun*: `%d`\n\n" + 
        "Untuk bantuan, hubungi Admin: %s\n\n" + 
		"Silakan pilih menu di bawah ini:",
		domain, ipInfo.City, ipInfo.Isp, totalUsers, ADMIN_TELEGRAM_ID)
    
	// Hapus pesan terakhir sebelum mengirim menu baru
    deleteLastMessage(bot, chatID) 

    // Buat keyboard inline
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ Buat Akun", "menu_create"),
			tgbotapi.NewInlineKeyboardButtonData("🔄 Renew Akun", "menu_renew"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 Daftar Akun", "menu_list"),
			tgbotapi.NewInlineKeyboardButtonData("🗑️ Hapus Akun", "menu_delete"),
		),
		tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("💸 Top Up Manual", "menu_topup"),
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
		
		ipInfo, _ := getIpInfo() // Abaikan kesalahan, cukup tampilkan kosong jika gagal
		
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
		
		ipInfo, _ := getIpInfo() // Abaikan kesalahan

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

// --- Fungsi Baru untuk Topup Manual ---

// Mengirim notifikasi topup ke Admin
func notifyAdminTopup(bot *tgbotapi.BotAPI, userID int64, username string, nominal int, chatID int64) {
	config, err := loadConfig()
    if err != nil {
        log.Printf("Error: Gagal memuat AdminID untuk notifikasi topup: %v", err)
        return
    }
    adminID := config.AdminID
	
	msgText := fmt.Sprintf("⚠️ *NOTIFIKASI TOP UP MANUAL*\n\n" +
		"User: [%s](tg://user?id=%d)\n" +
		"Nominal: *Rp %s*\n" +
        "Chat ID User: `%d`\n\n" +
		"Mohon konfirmasi setelah dana diterima.", 
		username, userID, strconv.Itoa(nominal), chatID)
	
	msg := tgbotapi.NewMessage(adminID, msgText)
	msg.ParseMode = "Markdown"
	msg.DisableWebPagePreview = true // Optional

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Konfirmasi Top Up", fmt.Sprintf("admin_confirm_topup:%d:%d", userID, nominal)),
			tgbotapi.NewInlineKeyboardButtonData("❌ Batalkan Top Up", fmt.Sprintf("admin_cancel_topup:%d:%d", userID, nominal)),
		),
	)
	msg.ReplyMarkup = keyboard
	
	bot.Send(msg)
}

// Menangani konfirmasi/pembatalan topup oleh Admin
func confirmTopup(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery, targetUserID int64, nominal int, success bool) {
	editMsg := tgbotapi.NewEditMessageText(query.Message.Chat.ID, query.Message.MessageID, "")
	editMsg.ParseMode = "Markdown"

	if success {
		// Kirim notifikasi ke user (targetUserID)
		userNotif := fmt.Sprintf("✅ *TOP UP BERHASIL*\n\nTop Up **Rp %d** telah dikonfirmasi oleh admin. Saldo Anda telah diperbarui.", nominal)
		sendMessage(bot, targetUserID, userNotif)

		// Edit pesan admin
		editMsg.Text = fmt.Sprintf("✅ *TOP UP DIKONFIRMASI*\n\n" +
			"Top Up **Rp %d** untuk user `%d` telah dikonfirmasi oleh admin.", nominal, targetUserID)
	} else {
		// Kirim notifikasi ke user (targetUserID)
		userNotif := fmt.Sprintf("❌ *TOP UP DIBATALKAN*\n\nTop Up **Rp %d** dibatalkan oleh admin. Silakan cek kembali transfer Anda.", nominal)
		sendMessage(bot, targetUserID, userNotif)

		// Edit pesan admin
		editMsg.Text = fmt.Sprintf("❌ *TOP UP DIBATALKAN*\n\n" +
			"Top Up **Rp %d** untuk user `%d` telah dibatalkan oleh admin.", nominal, targetUserID)
	}

	// Hapus keyboard setelah aksi
	editMsg.ReplyMarkup = &tgbotapi.InlineKeyboardMarkup{} 
	bot.Request(editMsg)
}