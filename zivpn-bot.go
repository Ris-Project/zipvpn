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
	[cite_start]BotConfigFile = "/etc/zivpn/bot-config.json" [cite: 1]
	[cite_start]ApiUrl        = "http://127.0.0.1:8080/api" [cite: 1]
	[cite_start]ApiKeyFile    = "/etc/zivpn/apikey" [cite: 1]
	// !!! [cite_start]GANTI INI DENGAN URL GAMBAR MENU ANDA !!! [cite: 1]
	[cite_start]MenuPhotoURL = "https://h.uguu.se/ePURTlNf.jpg" [cite: 1]

	[cite_start]// Interval untuk pengecekan dan penghapusan akun expired (diubah menjadi 1 menit) [cite: 1]
	[cite_start]AutoDeleteInterval = 1 * time.Minute [cite: 1]
)

[cite_start]var ApiKey = "AutoFtBot-agskjgdvsbdreiWG1234512SDKrqw" [cite: 1]

type BotConfig struct {
	[cite_start]BotToken string `json:"bot_token"` [cite: 1]
	[cite_start]AdminID  int64  `json:"admin_id"` [cite: 1]
}

type IpInfo struct {
	[cite_start]City string `json:"city"` [cite: 1]
	[cite_start]Isp  string `json:"isp"` [cite: 1]
}

type UserData struct {
	[cite_start]Password string `json:"password"` [cite: 1]
	[cite_start]Expired  string `json:"expired"` [cite: 1]
	[cite_start]Status   string `json:"status"` [cite: 1]
}

[cite_start]var userStates = make(map[int64]string) [cite: 1]
[cite_start]var tempUserData = make(map[int64]map[string]string) [cite: 1]
[cite_start]var lastMessageIDs = make(map[int64]int) [cite: 1]

func main() {
	[cite_start]rand.NewSource(time.Now().UnixNano()) [cite: 1]

	[cite_start]if keyBytes, err := ioutil.ReadFile(ApiKeyFile); [cite: 1]
	[cite_start]err == nil { [cite: 2]
		[cite_start]ApiKey = strings.TrimSpace(string(keyBytes)) [cite: 2]
	}
	[cite_start]config, err := loadConfig() [cite: 2]
	if err != nil {
		[cite_start]log.Fatal("Gagal memuat konfigurasi bot:", err) [cite: 2]
	}

	[cite_start]bot, err := tgbotapi.NewBotAPI(config.BotToken) [cite: 2]
	if err != nil {
		[cite_start]log.Panic(err) [cite: 2]
	}

	[cite_start]bot.Debug = false [cite: 2]
	[cite_start]log.Printf("Authorized on account %s", bot.Self.UserName) [cite: 2]

	[cite_start]// --- BACKGROUND WORKER (PENGHAPUSAN OTOMATIS) --- [cite: 2]
	[cite_start]go func() { [cite: 2]
		[cite_start]// Jalankan sekali saat startup [cite: 2]
		[cite_start]autoDeleteExpiredUsers(bot, config.AdminID) [cite: 2]

		[cite_start]// Buat Ticker untuk berjalan setiap interval (1 menit) [cite: 2]
		[cite_start]ticker := time.NewTicker(AutoDeleteInterval) [cite: 2]
		for range ticker.C {
			[cite_start]autoDeleteExpiredUsers(bot, config.AdminID) [cite: 2]
		}
	}()
	[cite_start]// ------------------------------------------------ [cite: 2]

	[cite_start]u := tgbotapi.NewUpdate(0) [cite: 2]
	[cite_start]u.Timeout = 60 [cite: 2]

	[cite_start]updates := bot.GetUpdatesChan(u) [cite: 2]

	for update := range updates {
		if update.Message != nil {
			[cite_start]handleMessage(bot, update.Message, config.AdminID) [cite: 2]
		} else if update.CallbackQuery != nil {
			[cite_start]handleCallback(bot, update.CallbackQuery, config.AdminID) [cite: 2]
		}
	}
}

func handleMessage(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, adminID int64) {
	if msg.From.ID != adminID {
		[cite_start]reply := tgbotapi.NewMessage(msg.Chat.ID, "⛔ Akses Ditolak. [cite: 2] [cite_start]Anda bukan admin.") [cite: 3]
		[cite_start]sendAndTrack(bot, reply) [cite: 3]
		return
	}

	[cite_start]state, exists := userStates[msg.From.ID] [cite: 3]
	if exists {
		[cite_start]handleState(bot, msg, state) [cite: 3]
		return
	}

	if msg.IsCommand() {
		[cite_start]switch msg.Command() { [cite: 3]
		case "start":
			[cite_start]showMainMenu(bot, msg.Chat.ID) [cite: 3]
		default:
			[cite_start]msg := tgbotapi.NewMessage(msg.Chat.ID, "Perintah tidak dikenal.") [cite: 3]
			[cite_start]sendAndTrack(bot, msg) [cite: 3]
		}
	}
}

func handleCallback(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery, adminID int64) {
	if query.From.ID != adminID {
		[cite_start]bot.Request(tgbotapi.NewCallback(query.ID, "Akses Ditolak")) [cite: 3]
		return
	}

	switch {
	[cite_start]case query.Data == "menu_trial": // Handler untuk Trial [cite: 3]
		createTrialUser(bot, query.Message.Chat.ID)
	case query.Data == "menu_viip": // Handler untuk VIIP 15 Hari (DITAMBAHKAN)
		createViipRandomUser(bot, query.Message.Chat.ID)
	case query.Data == "menu_create":
		[cite_start]userStates[query.From.ID] = "create_username" [cite: 3]
		[cite_start]tempUserData[query.From.ID] = make(map[string]string) [cite: 3]
		[cite_start]sendMessage(bot, query.Message.Chat.ID, "🔑 *MENU CREATE*\nSilakan masukkan **PASSWORD** yang diinginkan:") [cite: 3]
	case query.Data == "menu_delete":
		[cite_start]showUserSelection(bot, query.Message.Chat.ID, 1, "delete") [cite: 3]
	case query.Data == "menu_renew":
		[cite_start]showUserSelection(bot, query.Message.Chat.ID, 1, "renew") [cite: 3]
	case query.Data == "menu_list":
		[cite_start]listUsers(bot, query.Message.Chat.ID) [cite: 3]
	case query.Data == "menu_info":
		[cite_start]systemInfo(bot, query.Message.Chat.ID) [cite: 3]
	case query.Data == "cancel":
		[cite_start]delete(userStates, query.From.ID) [cite: 3]
		[cite_start]delete(tempUserData, query.From.ID) [cite: 3]
		[cite_start]showMainMenu(bot, query.Message.Chat.ID) [cite: 3]
	case strings.HasPrefix(query.Data, "page_"):
		[cite_start]parts := strings.Split(query.Data, ":") [cite: 3]
		[cite_start]action := parts[0][5:] // remove "page_" [cite: 4]
		[cite_start]page, _ := strconv.Atoi(parts[1]) [cite: 4]
		[cite_start]showUserSelection(bot, query.Message.Chat.ID, [cite: 4]
			[cite_start]page, action) [cite: 4]
	case strings.HasPrefix(query.Data, "select_renew:"):
		[cite_start]username := strings.TrimPrefix(query.Data, "select_renew:") [cite: 4]
		[cite_start]tempUserData[query.From.ID] = map[string]string{"username": username} [cite: 4]
		[cite_start]userStates[query.From.ID] = "renew_days" [cite: 4]
		[cite_start]sendMessage(bot, query.Message.Chat.ID, fmt.Sprintf("🔄 *MENU RENEW*\nUser: `%s`\nMasukkan tambahan durasi (*Hari*):", username)) [cite: 4]
	case strings.HasPrefix(query.Data, "select_delete:"):
		[cite_start]username := strings.TrimPrefix(query.Data, "select_delete:") [cite: 4]
		[cite_start]msg := tgbotapi.NewMessage(query.Message.Chat.ID, fmt.Sprintf("❓ *KONFIRMASI HAPUS*\nAnda yakin ingin menghapus user `%s`?", username)) [cite: 4]
		[cite_start]msg.ParseMode = "Markdown" [cite: 4]
		[cite_start]msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup( [cite: 4]
			tgbotapi.NewInlineKeyboardRow(
				[cite_start]tgbotapi.NewInlineKeyboardButtonData("✅ Ya, Hapus", "confirm_delete:"+username), [cite: 4]
				[cite_start]tgbotapi.NewInlineKeyboardButtonData("❌ Batal", "cancel"), [cite: 4]
			),
		)
		[cite_start]sendAndTrack(bot, msg) [cite: 4]
	case strings.HasPrefix(query.Data, "confirm_delete:"):
		[cite_start]username := strings.TrimPrefix(query.Data, "confirm_delete:") [cite: 4]
		[cite_start]deleteUser(bot, query.Message.Chat.ID, username) [cite: 4]
	}

	[cite_start]bot.Request(tgbotapi.NewCallback(query.ID, "")) [cite: 4]
}

func handleState(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, state string) {
	[cite_start]userID := msg.From.ID [cite: 5]
	[cite_start]text := strings.TrimSpace(msg.Text) [cite: 5]

	switch state {
	case "create_username":
		[cite_start]tempUserData[userID]["username"] = text [cite: 5]
		[cite_start]userStates[userID] = "create_days" [cite: 5]
		[cite_start]sendMessage(bot, msg.Chat.ID, fmt.Sprintf("⏳ *CREATE USER*\nPassword: `%s`\nMasukkan **Durasi** (*Hari*) pembuatan:", text)) [cite: 5]

	case "create_days":
		[cite_start]days, err := strconv.Atoi(text) [cite: 5]
		if err != nil {
			[cite_start]sendMessage(bot, msg.Chat.ID, "❌ Durasi harus angka. [cite: 5] [cite_start]Coba lagi:") [cite: 5]
			return
		}
		[cite_start]createUser(bot, msg.Chat.ID, tempUserData[userID]["username"], days) [cite: 5]
		[cite_start]resetState(userID) [cite: 5]

	case "renew_days":
		[cite_start]days, err := strconv.Atoi(text) [cite: 5]
		if err != nil {
			[cite_start]sendMessage(bot, msg.Chat.ID, "❌ Durasi harus angka. Coba lagi:") [cite: 5]
			return
		}
		[cite_start]renewUser(bot, msg.Chat.ID, tempUserData[userID]["username"], days) [cite: 5]
		[cite_start]resetState(userID) [cite: 5]
	}
}

func showUserSelection(bot *tgbotapi.BotAPI, chatID int64, page int, action string) {
	[cite_start]users, err := getUsers() [cite: 5]
	if err != nil {
		[cite_start]sendMessage(bot, chatID, "❌ Gagal mengambil data user.") [cite: 5]
		return
	}

	if len(users) == 0 {
		[cite_start]sendMessage(bot, chatID, "📂 Tidak ada user saat ini.") [cite: 5]
		[cite_start]showMainMenu(bot, chatID) [cite: 5]
		return
	}

	[cite_start]perPage := 10 [cite: 5]
	[cite_start]totalPages := (len(users) + perPage - 1) / perPage [cite: 5]

	if page < 1 {
		[cite_start]page = 1 [cite: 5]
	}
	if page > totalPages {
		[cite_start]page = totalPages [cite: 5]
	}

	[cite_start]start := (page - 1) * perPage [cite: 5]
	[cite_start]end := start + perPage [cite: 5]
	if end > len(users) {
		[cite_start]end = len(users) [cite: 5]
	}

	[cite_start]var rows [][]tgbotapi.InlineKeyboardButton [cite: 5]
	[cite_start]for _, u := [cite: 5]
	[cite_start]range users[start:end] { [cite: 6]
		[cite_start]statusIcon := "🟢" [cite: 6]
		if u.Status == "Expired" {
			[cite_start]statusIcon = "🔴" [cite: 6]
		}
		[cite_start]label := fmt.Sprintf("%s %s (Kadaluarsa: %s)", statusIcon, u.Password, u.Expired) [cite: 6]
		[cite_start]data := fmt.Sprintf("select_%s:%s", action, u.Password) [cite: 6]
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			[cite_start]tgbotapi.NewInlineKeyboardButtonData(label, data), [cite: 6]
		))
	}

	[cite_start]var navRow []tgbotapi.InlineKeyboardButton [cite: 6]
	if page > 1 {
		[cite_start]navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData("⬅️ Halaman Sebelumnya", fmt.Sprintf("page_%s:%d", action, page-1))) [cite: 6]
	}
	if page < totalPages {
		[cite_start]navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData("Halaman Selanjutnya ➡️", fmt.Sprintf("page_%s:%d", action, page+1))) [cite: 6]
	}
	if len(navRow) > 0 {
		[cite_start]rows = append(rows, navRow) [cite: 6]
	}

	[cite_start]rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("⬅️ Kembali ke Menu Utama", "cancel"))) [cite: 6]

	[cite_start]title := "" [cite: 6]
	switch action {
	case "delete":
		[cite_start]title = "🗑️ HAPUS AKUN" [cite: 6]
	case "renew":
		[cite_start]title = "🔄 PERPANJANG AKUN" [cite: 6]
	}

	[cite_start]msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("*%s*\nPilih user dari daftar di bawah (Halaman %d dari %d):", title, page, totalPages)) [cite: 6]
	[cite_start]msg.ParseMode = "Markdown" [cite: 7]
	[cite_start]msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...) [cite: 7]
	[cite_start]sendAndTrack(bot, msg) [cite: 7]
}

func showMainMenu(bot *tgbotapi.BotAPI, chatID int64) {
	[cite_start]ipInfo, _ := getIpInfo() [cite: 7]
	[cite_start]domain := "Unknown" [cite: 7]

	[cite_start]if res, err := apiCall("GET", "/info", nil); [cite: 7]
	[cite_start]err == nil && res["success"] == true { [cite: 8]
		[cite_start]if data, ok := res["data"].(map[string]interface{}); ok { [cite: 8]
			[cite_start]if d, ok := data["domain"].(string); [cite: 8]
			[cite_start]ok { [cite: 9]
				[cite_start]domain = d [cite: 9]
			}
		}
	}

	[cite_start]// Ambil Total Akun [cite: 9]
	[cite_start]totalUsers := 0 [cite: 9]
	[cite_start]if users, err := getUsers(); [cite: 9]
	[cite_start]err == nil { [cite: 10]
		[cite_start]totalUsers = len(users) [cite: 10]
	}

	[cite_start]// --- Ambil User yang Akan Segera Kedaluwarsa (24 Jam) --- [cite: 10]
	[cite_start]nearExpiredUsers, err := getNearExpiredUsers() [cite: 10]
	[cite_start]expiredText := "" [cite: 10]
	if err == nil && len(nearExpiredUsers) > 0 {
		[cite_start]expiredText += "\n\n⚠️ *AKUN AKAN SEGERA KADALUARSA (Dalam 24 Jam):*\n" [cite: 10]
		for i, u := range nearExpiredUsers {
			if i >= 5 {
				[cite_start]expiredText += "... dan user lainnya\n" [cite: 10]
				break
			}
			[cite_start]expiredText += fmt.Sprintf("•  `%s` (Expired: %s)\n", u.Password, u.Expired) [cite: 10]
		}
	}
	// ----------------------------------------------------

	[cite_start]msgText := fmt.Sprintf("✨ *WELCOME TO BOT PGETUNNEL UDP ZIVPN*\n\n" + [cite: 10]
		"Server Info:\n" +
		"•  🌐 *Domain*: `%s`\n" +
		"•  📍 *Lokasi*: `%s`\n" +
		"•  📡 *ISP*: `%s`\n" +
		"•  👤 *Total Akun*: `%d`\n\n" +
		"Untuk bantuan, hubungi Admin: @JesVpnt\n\n" +
		[cite_start]"Silakan pilih [cite: 11] menu di bawah ini:",
		domain, ipInfo.City, ipInfo.Isp, totalUsers)

	[cite_start]msgText += expiredText [cite: 11]

	[cite_start]// Hapus pesan [cite: 11]
	[cite_start]deleteLastMessage(bot, chatID) [cite: 11]

	// Buat keyboard inline
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ Buat Akun", "menu_create"),
			[cite_start]tgbotapi.NewInlineKeyboardButtonData("🚀 Trial 1 Hari", "menu_trial"), // Perubahan teks tombol [cite: 11]
		),
		tgbotapi.NewInlineKeyboardRow( // DITAMBAHKAN
			tgbotapi.NewInlineKeyboardButtonData("👑 VIIP 15 Hari", "menu_viip"), // Tombol baru VIIP
		),
		tgbotapi.NewInlineKeyboardRow(
			[cite_start]tgbotapi.NewInlineKeyboardButtonData("🔄 Renew Akun", "menu_renew"), [cite: 11]
			[cite_start]tgbotapi.NewInlineKeyboardButtonData("🗑️ Hapus Akun", "menu_delete"), [cite: 11]
		),
		tgbotapi.NewInlineKeyboardRow(
			[cite_start]tgbotapi.NewInlineKeyboardButtonData("📋 Daftar Akun", "menu_list"), [cite: 11]
			[cite_start]tgbotapi.NewInlineKeyboardButtonData("📊 Info Server", "menu_info"), [cite: 11]
		),
	)

	[cite_start]// Buat pesan foto dari URL [cite: 11]
	[cite_start]photoMsg := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(MenuPhotoURL)) [cite: 11]
	[cite_start]photoMsg.Caption = msgText [cite: 11]
	[cite_start]photoMsg.ParseMode = "Markdown" [cite: 11]
	[cite_start]photoMsg.ReplyMarkup = keyboard [cite: 11]

	// Kirim foto
	[cite_start]sentMsg, err := bot.Send(photoMsg) [cite: 11]
	if err == nil {
		[cite_start]// Track ID pesan yang baru dikirim (foto) [cite: 12]
		[cite_start]lastMessageIDs[chatID] = sentMsg.MessageID [cite: 12]
	} else {
		[cite_start]// Fallback jika pengiriman foto gagal [cite: 12]
		[cite_start]log.Printf("Gagal mengirim foto menu dari URL (%s): %v. [cite: 12] [cite_start]Mengirim sebagai teks biasa.", MenuPhotoURL, err) [cite: 12]

		[cite_start]textMsg := tgbotapi.NewMessage(chatID, msgText) [cite: 12]
		[cite_start]textMsg.ParseMode = "Markdown" [cite: 12]
		[cite_start]textMsg.ReplyMarkup = keyboard [cite: 12]
		[cite_start]sendAndTrack(bot, textMsg) [cite: 12]
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

[cite_start]// Fungsi untuk men-generate string acak sederhana [cite: 12]
func generateRandomPassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	seededRand := rand.New(rand.NewSource(time.Now().UnixNano()))
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[seededRand.Intn(len(charset))]
	}
	return string(b)
}

[cite_start]// [cite: 12] [cite_start]Fungsi Background Worker untuk menghapus akun expired secara otomatis [cite: 13]
func autoDeleteExpiredUsers(bot *tgbotapi.BotAPI, adminID int64) {
	[cite_start]users, err := getUsers() [cite: 13]
	if err != nil {
		[cite_start]log.Printf("❌ [AutoDelete] Gagal mengambil data user: %v", err) [cite: 13]
		return
	}

	[cite_start]deletedCount := 0 [cite: 13]
	[cite_start]var deletedUsers []string [cite: 13]

	for _, u := range users {
		[cite_start]if u.Status == "Expired" { [cite: 13]
			[cite_start]// Memanggil endpoint delete API [cite: 13]
			res, err := apiCall("POST", "/user/delete", map[string]interface{}{
				[cite_start]"password": u.Password, [cite: 13]
			})

			if err != nil {
				[cite_start]log.Printf("❌ [AutoDelete] Error API saat menghapus %s: %v", u.Password, err) [cite: 13]
				continue
			}

			if res["success"] == true {
				[cite_start]deletedCount++ [cite: 13]
				[cite_start]deletedUsers = append(deletedUsers, u.Password) [cite: 13]
				[cite_start]log.Printf("✅ [AutoDelete] User expired %s berhasil dihapus.", u.Password) [cite: 13]
			} else {
				[cite_start]log.Printf("❌ [AutoDelete] Gagal menghapus %s: %s", u.Password, res["message"]) [cite: 13]
			}
		}
	}

	[cite_start]// Kirim notifikasi ke Admin jika ada akun yang dihapus [cite: 14]
	[cite_start]if deletedCount > 0 [cite: 14]
	[cite_start]{ [cite: 14]
		msgText := fmt.Sprintf("🗑️ *PEMBERSIHAN AKUN OTOMATIS*\n\n" +
			[cite_start]"Total `%d` akun kedaluwarsa telah dihapus secara otomatis:\n- %s", [cite: 14]
			[cite_start]deletedCount, strings.Join(deletedUsers, "\n- ")) [cite: 14]

		[cite_start]notification := tgbotapi.NewMessage(adminID, msgText) [cite: 14]
		[cite_start]notification.ParseMode = "Markdown" [cite: 14]
		[cite_start]bot.Send(notification) [cite: 14]
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

	[cite_start]var info [cite: 15] IpInfo
	[cite_start]if err := json.NewDecoder(resp.Body).Decode(&info); err != nil { [cite: 15]
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

[cite_start]// Fungsi untuk mendapatkan user yang akan segera expired (dalam 24 jam) [cite: 15]
func getNearExpiredUsers() ([]UserData, error) {
	[cite_start]users, err := getUsers() [cite: 16]
	if err != nil {
		return nil, err
	}

	[cite_start]var nearExpired []UserData [cite: 16]
	[cite_start]// Tentukan batas waktu: 24 jam dari sekarang [cite: 16]
	[cite_start]expiryThreshold := time.Now().Add(24 * time.Hour) [cite: 16]

	for _, u := range users {
		[cite_start]// Asumsi format expired: "YYYY-MM-DD hh:mm:ss" [cite: 16]
		[cite_start]expiredTime, err := time.Parse("2006-01-02 15:04:05", u.Expired) [cite: 16]
		[cite_start]if err != nil [cite: 16]
		[cite_start]{ [cite: 16]
			continue
		}

		[cite_start]// Cek apakah waktu expired di masa depan DAN dalam 24 jam dari sekarang [cite: 16]
		if expiredTime.After(time.Now()) && expiredTime.Before(expiryThreshold) {
			[cite_start]nearExpired = append(nearExpired, u) [cite: 16]
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
			"━━━━━━━━━━━━━━━━━━━━━━━━━",
			data["password"], data["domain"], data["expired"], ipInfo.City, ipInfo.Isp)

		reply := tgbotapi.NewMessage(chatID, msg)
		reply.ParseMode = "Markdown"
		deleteLastMessage(bot, chatID)
		bot.Send(reply)
		showMainMenu(bot, chatID)
	} else {
		[cite_start]errMsg, ok := [cite: 17] res["message"].(string)
		if !ok {
			[cite_start]errMsg = "Pesan error tidak diketahui dari API." [cite: 17]
		[cite_start]} [cite: 18]
		[cite_start]sendMessage(bot, chatID, fmt.Sprintf("❌ Gagal: %s", errMsg)) [cite: 18]
		[cite_start]showMainMenu(bot, chatID) [cite: 18]
	}
}

[cite_start]// FUNGSI INI SUDAH DIUBAH KE DURASI 1 HARI (24 JAM) [cite: 18]
func createTrialUser(bot *tgbotapi.BotAPI, chatID int64) {
	[cite_start]trialPassword := generateRandomPassword(8) [cite: 18]

	[cite_start]// Perubahan: Menggunakan "days": 1 dan "minutes": 0 untuk durasi 1 hari [cite: 18]
	res, err := apiCall("POST", "/user/create", map[string]interface{}{
		"password": trialPassword,
		[cite_start]"minutes":  0, [cite: 18]
		[cite_start]"days":     1, // Trial 1 hari [cite: 18]
	})

	if err != nil {
		[cite_start]sendMessage(bot, chatID, "❌ Error Komunikasi API: "+err.Error()) [cite: 18]
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
		[cite_start]if p, ok := data["password"].(string); [cite: 19] ok {
			[cite_start]password = p [cite: 19]
		}

		expired := "N/A"
		[cite_start]if e, ok := data["expired"].(string); [cite: 19] [cite_start]ok { [cite: 20]
			[cite_start]expired = e [cite: 20]
		}

		// Ambil Domain (Prioritas 1: dari respons create)
		[cite_start]domain := "Unknown" [cite: 20]
		[cite_start]if d, ok := data["domain"].(string); [cite: 20] [cite_start]ok && d != "" { [cite: 21]
			[cite_start]domain = d [cite: 21]
		} else {
			[cite_start]// Prioritas 2: Fallback dengan memanggil /info API [cite: 21]
			[cite_start]if infoRes, err := apiCall("GET", "/info", nil); [cite: 21] [cite_start]err == nil && infoRes["success"] == true { [cite: 22]
				[cite_start]if infoData, ok := infoRes["data"].(map[string]interface{}); ok { [cite: 22]
					[cite_start]if d, ok := infoData["domain"].(string); [cite: 22] [cite_start]ok { [cite: 23]
						[cite_start]domain = d [cite: 23]
					}
				}
			}
		}
		// --- END EKSTRAKSI DATA ---

		// 3. Susun dan Kirim Pesan Sukses
		[cite_start]msg := fmt.Sprintf("🚀 *TRIAL 1 HARI BERHASIL DIBUAT*\n" + [cite: 23]
			"━━━━━━━━━━━━━━━━━━━━━━━━━\n" +
			"🔑 *Password*: `%s`\n" +
			"🌐 *Domain*: `%s`\n" +
			"⏳ *Durasi*: `1 Hari`\n" + // Perubahan di sini
			"🗓️ *Kadaluarsa*: `%s`\n" +
			"📍 *Lokasi Server*: `%s`\n" +
			"📡 *ISP Server*: `%s`\n" +
			"━━━━━━━━━━━━━━━━━━━━━━━━━\n" +
			"❗️ *PERHATIAN: Trial ini hanya berlaku 1 hari!*", // Perubahan di sini
			password, domain, expired, ipInfo.City, ipInfo.Isp)

		reply := tgbotapi.NewMessage(chatID, msg)
		reply.ParseMode = "Markdown"
		deleteLastMessage(bot, chatID)
		bot.Send(reply)
		showMainMenu(bot, chatID)
	} else {
		[cite_start]// 4. Penanganan Kegagalan API [cite: 24]
		[cite_start]errMsg, ok := res["message"].(string) [cite: 24]
		if !ok {
			[cite_start]errMsg = "Respon kegagalan dari API tidak diketahui." [cite: 24]
		}
		[cite_start]sendMessage(bot, chatID, fmt.Sprintf("❌ Gagal membuat Trial: %s", errMsg)) [cite: 24]
		[cite_start]showMainMenu(bot, chatID) [cite: 24]
	}
}

func createViipRandomUser(bot *tgbotapi.BotAPI, chatID int64) {
	viipPassword := generateRandomPassword(8) // Generate password acak 8 karakter
	const viipDays = 15 // Durasi VIIP: 15 Hari

	res, err := apiCall("POST", "/user/create", map[string]interface{}{
		"password": viipPassword,
		"days":     viipDays, // 15 Hari
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

		// Susun dan Kirim Pesan Sukses
		msg := fmt.Sprintf("👑 *AKUN VIIP 15 HARI BERHASIL DIBUAT*\n" +
			"━━━━━━━━━━━━━━━━━━━━━━━━━\n" +
			"🔑 *Password*: `%s`\n" +
			"🌐 *Domain*: `%s`\n" +
			"⏳ *Durasi*: `%d Hari`\n" +
			"🗓️ *Kadaluarsa*: `%s`\n" +
			"📍 *Lokasi Server*: `%s`\n" +
			"📡 *ISP Server*: `%s`\n" +
			"━━━━━━━━━━━━━━━━━━━━━━━━━",
			password, domain, viipDays, expired, ipInfo.City, ipInfo.Isp)

		reply := tgbotapi.NewMessage(chatID, msg)
		reply.ParseMode = "Markdown"
		deleteLastMessage(bot, chatID)
		bot.Send(reply)
		showMainMenu(bot, chatID)
	} else {
		// Penanganan Kegagalan API
		errMsg, ok := res["message"].(string)
		if !ok {
			errMsg = "Respon kegagalan dari API tidak diketahui."
		}
		sendMessage(bot, chatID, fmt.Sprintf("❌ Gagal membuat Akun VIIP: %s", errMsg))
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
			[cite_start]errMsg = "Pesan error tidak diketahui dari API." [cite: 25]
		}
		[cite_start]sendMessage(bot, chatID, fmt.Sprintf("❌ Gagal menghapus: %s", errMsg)) [cite: 25]
		[cite_start]showMainMenu(bot, chatID) [cite: 25]
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
		[cite_start]if d, ok := data["domain"].(string); [cite: 26] [cite_start]ok && d != "" { [cite: 26]
			domain = d
		} else {
			[cite_start]if infoRes, err := apiCall("GET", "/info", nil); [cite: 26] [cite_start]err == nil && infoRes["success"] == true { [cite: 27]
				[cite_start]if infoData, ok := infoRes["data"].(map[string]interface{}); ok { [cite: 27]
					[cite_start]if d, ok := infoData["domain"].(string); [cite: 27] [cite_start]ok { [cite: 28]
						[cite_start]domain = d [cite: 28]
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
			[cite_start]errMsg = "Pesan error tidak diketahui dari API." [cite: 29]
		}
		[cite_start]sendMessage(bot, chatID, fmt.Sprintf("❌ Gagal memperpanjang: %s", errMsg)) [cite: 29]
		[cite_start]showMainMenu(bot, chatID) [cite: 29]
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

[cite_start]func systemInfo(bot *tgbotapi.BotAPI, chatID [cite: 29] [cite_start]int64) { [cite: 30]
	[cite_start]res, err := apiCall("GET", "/info", nil) [cite: 30]
	if err != nil {
		[cite_start]sendMessage(bot, chatID, "❌ Error API: "+err.Error()) [cite: 30]
		return
	}

	if res["success"] == true {
		[cite_start]data := res["data"].(map[string]interface{}) [cite: 30]

		[cite_start]ipInfo, _ := getIpInfo() [cite: 30]

		msg := fmt.Sprintf("⚙️ *INFORMASI DETAIL SERVER*\n" +
			"━━━━━━━━━━━━━━━━━━━━━━━━━\n" +
			"🌐 *Domain*: `%s`\n" +
			"🖥️ *IP Public*: `%s`\n" +
			"🔌 *Port*: `%s`\n" +
			"🔧 *Layanan*: `%s`\n" +
			"📍 *Lokasi Server*: `%s`\n" +
			"📡 *ISP Server*: `%s`\n" +
			"━━━━━━━━━━━━━━━━━━━━━━━━━",
			[cite_start]data["domain"], data["public_ip"], data["port"], data["service"], ipInfo.City, ipInfo.Isp) [cite: 30]

		[cite_start]reply := tgbotapi.NewMessage(chatID, msg) [cite: 30]
		[cite_start]reply.ParseMode = "Markdown" [cite: 30]
		[cite_start]deleteLastMessage(bot, chatID) [cite: 30]
		[cite_start]bot.Send(reply) [cite: 30]
		[cite_start]showMainMenu(bot, chatID) [cite: 30]
	} else {
		[cite_start]sendMessage(bot, chatID, "❌ Gagal mengambil info sistem.") [cite: 30]
	}
}

func loadConfig() (BotConfig, error) {
	[cite_start]var config BotConfig [cite: 30]
	[cite_start]file, err := ioutil.ReadFile(BotConfigFile) [cite: 30]
	if err != nil {
		[cite_start]return config, err [cite: 30]
	}
	[cite_start]err = json.Unmarshal(file, &config) [cite: 30]
	[cite_start]return config, err [cite: 30]
}