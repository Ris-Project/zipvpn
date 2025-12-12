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
	// URL QRIS
	QrisPhotoURL = "https://n.uguu.se/kpMRmnoX.png" // Tambahan: URL QRIS

	// Interval untuk pengecekan dan penghapusan akun expired (diubah menjadi 1 menit)
	[cite_start]AutoDeleteInterval = 1 * time.Minute // [cite: 1]
)

[cite_start]var ApiKey = "AutoFtBot-agskjgdvsbdreiWG1234512SDKrqw" // [cite: 1]

type BotConfig struct {
	BotToken string `json:"bot_token"`
	[cite_start]AdminID  int64  `json:"admin_id"` // [cite: 1]
}

type IpInfo struct {
	City string `json:"city"`
	[cite_start]Isp  string `json:"isp"` // [cite: 1]
}

type UserData struct {
	Password string `json:"password"`
	Expired  string `json:"expired"`
	[cite_start]Status   string `json:"status"` // [cite: 1]
}

[cite_start]var userStates = make(map[int64]string) // [cite: 1]
[cite_start]var tempUserData = make(map[int64]map[string]string) // [cite: 1]
[cite_start]var lastMessageIDs = make(map[int64]int) // [cite: 1]

func main() {
	[cite_start]rand.NewSource(time.Now().UnixNano()) // [cite: 1]

	if keyBytes, err := ioutil.ReadFile(ApiKeyFile); [cite_start]// [cite: 1]
	[cite_start]err == nil { // [cite: 2]
		[cite_start]ApiKey = strings.TrimSpace(string(keyBytes)) // [cite: 2]
	}
	[cite_start]config, err := loadConfig() // [cite: 2]
	[cite_start]if err != nil { // [cite: 2]
		[cite_start]log.Fatal("Gagal memuat konfigurasi bot:", err) // [cite: 2]
	}

	[cite_start]bot, err := tgbotapi.NewBotAPI(config.BotToken) // [cite: 2]
	[cite_start]if err != nil { // [cite: 2]
		[cite_start]log.Panic(err) // [cite: 2]
	}

	[cite_start]bot.Debug = false // [cite: 2]
	[cite_start]log.Printf("Authorized on account %s", bot.Self.UserName) // [cite: 2]

	// --- BACKGROUND WORKER (PENGHAPUSAN OTOMATIS) ---
	[cite_start]go func() { // [cite: 2]
		[cite_start]// Jalankan sekali saat startup // [cite: 2]
		[cite_start]autoDeleteExpiredUsers(bot, config.AdminID) // [cite: 2]

		[cite_start]// Buat Ticker untuk berjalan setiap interval (1 menit) // [cite: 2]
		[cite_start]ticker := time.NewTicker(AutoDeleteInterval) // [cite: 2]
		[cite_start]for range ticker.C { // [cite: 2]
			[cite_start]autoDeleteExpiredUsers(bot, config.AdminID) // [cite: 2]
		}
	}()
	// ------------------------------------------------

	[cite_start]u := tgbotapi.NewUpdate(0) // [cite: 2]
	[cite_start]u.Timeout = 60 // [cite: 2]

	[cite_start]updates := bot.GetUpdatesChan(u) // [cite: 2]

	[cite_start]for update := range updates { // [cite: 2]
		[cite_start]if update.Message != nil { // [cite: 2]
			[cite_start]handleMessage(bot, update.Message, config.AdminID) // [cite: 2]
		[cite_start]} else if update.CallbackQuery != nil { // [cite: 2]
			[cite_start]handleCallback(bot, update.CallbackQuery, config.AdminID) // [cite: 2]
		}
	}
}

func handleMessage(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, adminID int64) {
	[cite_start]state, exists := userStates[msg.From.ID] // [cite: 3]
	[cite_start]if exists { // [cite: 3]
		[cite_start]handleState(bot, msg, state) // [cite: 3]
		[cite_start]return // [cite: 3]
	}

	if msg.From.ID == adminID {
		// --- LOGIKA UNTUK ADMIN ---
		[cite_start]if msg.IsCommand() { // [cite: 3]
			[cite_start]switch msg.Command() { // [cite: 3]
			[cite_start]case "start": // [cite: 3]
				[cite_start]showMainMenu(bot, msg.Chat.ID) // [cite: 3]
			[cite_start]default: // [cite: 3]
				[cite_start]msg := tgbotapi.NewMessage(msg.Chat.ID, "Perintah tidak dikenal.") // [cite: 3]
				[cite_start]sendAndTrack(bot, msg) // [cite: 3]
			}
		}
	} else {
		// --- LOGIKA UNTUK NON-ADMIN ---
		handleNonAdminMessage(bot, msg)
	}
}

// FUNGSI BARU UNTUK NON-ADMIN
func handleNonAdminMessage(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if msg.IsCommand() && msg.Command() == "start" {
		showNonAdminMenu(bot, msg.Chat.ID)
	} else {
		reply := tgbotapi.NewMessage(msg.Chat.ID, "🚫 Anda tidak memiliki izin untuk menggunakan bot ini. Silakan gunakan perintah /start untuk melihat menu yang tersedia.")
		bot.Send(reply)
	}
}

func handleCallback(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery, adminID int64) {
	userID := query.From.ID
	isNonAdmin := userID != adminID

	switch {
	// KASUS NON-ADMIN: Trial 1 Hari (Diizinkan untuk non-admin)
	case query.Data == "menu_trial_1": 
		createGenericTrialUser(bot, query.Message.Chat.ID, 1) // Trial 1 Hari

	// KASUS NON-ADMIN: Menu Topup Mandiri (Tampilkan QRIS)
	case query.Data == "menu_topup_user":
		// Hapus pesan lama
		deleteLastMessage(bot, query.Message.ChatID)
		
		// 1. Inisialisasi State (Lanjut ke langkah input password/username)
		userStates[userID] = "topup_user_password"
		tempUserData[userID] = make(map[string]string)
		
		// 2. Buat Teks dan Keyboard
		msgText := "🔑 *TOPUP MANDIRI / PEMBELIAN AKUN*\n\n" + 
			"Untuk aktivasi akun, silakan lakukan pembayaran melalui **QRIS** di bawah ini:\n\n" +
			"💸 *DAFTAR HARGA:*\n" +
			"•  15 Hari: Rp 5.000\n" +
			"•  30 Hari: Rp 12.000\n" +
			"•  60 Hari: Rp 25.000\n" +
			"•  90 Hari: Rp 35.000\n\n" +
			"Setelah pembayaran, silakan **Lanjutkan** untuk memasukkan password dan durasi hari yang Anda bayar."
		
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("➡️ Lanjutkan Input Akun", "qris_continue_input"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("❌ Batalkan", "cancel"),
			),
		)

		// 3. Kirim Gambar QRIS
		photoMsg := tgbotapi.NewPhoto(query.Message.ChatID, tgbotapi.FileURL(QrisPhotoURL))
		photoMsg.Caption = msgText
		photoMsg.ParseMode = "Markdown"
		photoMsg.ReplyMarkup = keyboard

		// Kirim foto dan lacak ID-nya
		sentMsg, err := bot.Send(photoMsg)
		if err == nil {
			lastMessageIDs[query.Message.ChatID] = sentMsg.MessageID
		} else {
			// Fallback jika pengiriman foto gagal
			log.Printf("Gagal mengirim foto QRIS: %v. Mengirim sebagai teks biasa.", err)
			
			textMsg := tgbotapi.NewMessage(query.Message.ChatID, msgText)
			textMsg.ParseMode = "Markdown"
			textMsg.ReplyMarkup = keyboard
			sendAndTrack(bot, textMsg)
		}
	
	// KASUS NON-ADMIN: Penanganan Lanjut setelah melihat QRIS
	case query.Data == "qris_continue_input":
		// Hapus pesan QRIS
		deleteLastMessage(bot, query.Message.ChatID) 
		
		textInput := "Silakan masukkan **PASSWORD AKUN BARU** yang Anda inginkan (gunakan 8-10 karakter):"
		
		// Kita menggunakan sendMessage untuk memastikan tombol "❌ Batal" muncul
		sendMessage(bot, query.Message.ChatID, textInput) 

	case query.Data == "menu_non_admin_start":
		showNonAdminMenu(bot, query.Message.Chat.ID)
	
	// KASUS ADMIN (Semua menu setelah ini harus diperiksa admin)
	case isNonAdmin:
		bot.Request(tgbotapi.NewCallback(query.ID, "🚫 Akses Ditolak. Fitur ini hanya untuk Admin."))
		// Jika ini adalah non-admin, hentikan pemrosesan
		return
	
	[cite_start]case query.Data == "menu_trial_15": // [cite: 3]
		[cite_start]createGenericTrialUser(bot, query.Message.Chat.ID, 15) // Trial 15 Hari // [cite: 3]
	[cite_start]case query.Data == "menu_trial_30": // [cite: 3]
		[cite_start]createGenericTrialUser(bot, query.Message.Chat.ID, 30) // Trial 30 Hari // [cite: 3]
	[cite_start]case query.Data == "menu_trial_60": // [cite: 3]
		[cite_start]createGenericTrialUser(bot, query.Message.Chat.ID, 60) // Trial 60 Hari // [cite: 3]
	[cite_start]case query.Data == "menu_trial_90": // [cite: 3]
		[cite_start]createGenericTrialUser(bot, query.Message.Chat.ID, 90) // Trial 90 Hari // [cite: 3]
	[cite_start]case query.Data == "menu_create": // [cite: 3]
		[cite_start]userStates[query.From.ID] = "create_username" // [cite: 3]
		[cite_start]tempUserData[query.From.ID] = make(map[string]string) // [cite: 3]
		[cite_start]sendMessage(bot, query.Message.Chat.ID, "🔑 *MENU CREATE*\nSilakan masukkan **PASSWORD** yang diinginkan:") // [cite: 3]
	[cite_start]case query.Data == "menu_delete": // [cite: 4]
		[cite_start]showUserSelection(bot, query.Message.Chat.ID, 1, "delete") // [cite: 4]
	[cite_start]case query.Data == "menu_renew": // [cite: 4]
		[cite_start]showUserSelection(bot, query.Message.Chat.ID, 1, "renew") // [cite: 4]
	[cite_start]case query.Data == "menu_list": // [cite: 4]
		[cite_start]listUsers(bot, query.Message.Chat.ID) // [cite: 4]
	[cite_start]case query.Data == "menu_info": // [cite: 4]
		[cite_start]systemInfo(bot, query.Message.Chat.ID) // [cite: 4]
	[cite_start]case query.Data == "cancel": // [cite: 4]
		[cite_start]delete(userStates, query.From.ID) // [cite: 4]
		[cite_start]delete(tempUserData, query.From.ID) // [cite: 4]
		// Non-admin kembali ke menu non-admin, Admin kembali ke menu utama
		if isNonAdmin {
			showNonAdminMenu(bot, query.Message.Chat.ID)
		} else {
			[cite_start]showMainMenu(bot, query.Message.Chat.ID) // [cite: 4]
		}
	
	// KASUS ADMIN: Topup Manual
	case query.Data == "menu_topup":
		userStates[query.From.ID] = "topup_username"
		tempUserData[query.From.ID] = make(map[string]string)
		sendMessage(bot, query.Message.Chat.ID, "💰 *MENU TOPUP MANUAL*\nSilakan masukkan **PASSWORD** user yang akan di-topup:")
	
	[cite_start]case strings.HasPrefix(query.Data, "page_"): // [cite: 4]
		[cite_start]parts := strings.Split(query.Data, ":") // [cite: 4]
		[cite_start]action := parts[0][5:] // remove "page_" // [cite: 4]
		[cite_start]page, _ := strconv.Atoi(parts[1]) // [cite: 4]
		[cite_start]showUserSelection(bot, query.Message.Chat.ID, page, action) // [cite: 4]
	[cite_start]case strings.HasPrefix(query.Data, "select_renew:"): // [cite: 4]
		[cite_start]username := strings.TrimPrefix(query.Data, "select_renew:") // [cite: 4]
		[cite_start]tempUserData[query.From.ID] = map[string]string{"username": username} // [cite: 4]
		[cite_start]userStates[query.From.ID] = "renew_days" // [cite: 4]
		[cite_start]sendMessage(bot, query.Message.Chat.ID, fmt.Sprintf("🔄 *MENU RENEW*\nUser: `%s`\nMasukkan tambahan durasi (*Hari*):", username)) // [cite: 4]
	[cite_start]case strings.HasPrefix(query.Data, "select_delete:"): // [cite: 4]
		[cite_start]username := strings.TrimPrefix(query.Data, "select_delete:") // [cite: 4]
		[cite_start]msg := tgbotapi.NewMessage(query.Message.Chat.ID, fmt.Sprintf("❓ *KONFIRMASI HAPUS*\nAnda yakin ingin menghapus user `%s`?", username)) // [cite: 4]
		[cite_start]msg.ParseMode = "Markdown" // [cite: 4]
		[cite_start]msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup( // [cite: 4]
			[cite_start]tgbotapi.NewInlineKeyboardRow( // [cite: 4]
				[cite_start]tgbotapi.NewInlineKeyboardButtonData("✅ Ya, Hapus", "confirm_delete:"+username), // [cite: 4]
				[cite_start]tgbotapi.NewInlineKeyboardButtonData("❌ Batal", "cancel"), // [cite: 4]
			),
		)
		[cite_start]sendAndTrack(bot, msg) // [cite: 4]
	[cite_start]case strings.HasPrefix(query.Data, "confirm_delete:"): // [cite: 4]
		[cite_start]username := strings.TrimPrefix(query.Data, "confirm_delete:") // [cite: 4]
		[cite_start]deleteUser(bot, query.Message.Chat.ID, username) // [cite: 4]
	}

	[cite_start]bot.Request(tgbotapi.NewCallback(query.ID, "")) // [cite: 4]
}

func handleState(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, state string) {
	[cite_start]userID := msg.From.ID // [cite: 5]
	[cite_start]text := strings.TrimSpace(msg.Text) // [cite: 5]

	switch state {
	[cite_start]case "create_username": // [cite: 5]
		[cite_start]tempUserData[userID]["username"] = text // [cite: 5]
		[cite_start]userStates[userID] = "create_days" // [cite: 5]
		[cite_start]sendMessage(bot, msg.ChatID, fmt.Sprintf("⏳ *CREATE USER*\nPassword: `%s`\nMasukkan **Durasi** (*Hari*) pembuatan:", text)) // [cite: 5]

	[cite_start]case "create_days": // [cite: 5]
		[cite_start]days, err := strconv.Atoi(text) // [cite: 5]
		[cite_start]if err != nil { // [cite: 5]
			[cite_start]sendMessage(bot, msg.ChatID, "❌ Durasi harus angka. Coba lagi:") // [cite: 5, 6]
			[cite_start]return // [cite: 5]
		}
		[cite_start]createUser(bot, msg.ChatID, tempUserData[userID]["username"], days) // [cite: 6]
		[cite_start]resetState(userID) // [cite: 6]

	[cite_start]case "renew_days": // [cite: 6]
		[cite_start]days, err := strconv.Atoi(text) // [cite: 6]
		[cite_start]if err != nil { // [cite: 6]
			[cite_start]sendMessage(bot, msg.ChatID, "❌ Durasi harus angka. Coba lagi:") // [cite: 6]
			[cite_start]return // [cite: 6]
		}
		[cite_start]renewUser(bot, msg.ChatID, tempUserData[userID]["username"], days) // [cite: 6]
		[cite_start]resetState(userID) // [cite: 6]
	
	// KASUS ADMIN: Topup Manual
	case "topup_username":
		tempUserData[userID]["username"] = text
		userStates[userID] = "topup_days"
		sendMessage(bot, msg.ChatID, fmt.Sprintf("💰 *TOPUP MANUAL*\nUser: `%s`\nMasukkan tambahan durasi (*Hari*) untuk topup:", text))

	case "topup_days":
		days, err := strconv.Atoi(text)
		if err != nil {
			sendMessage(bot, msg.ChatID, "❌ Durasi harus angka. Coba lagi:")
			return
		}
		renewUser(bot, msg.ChatID, tempUserData[userID]["username"], days)
		resetState(userID)
	
	// --- KASUS BARU UNTUK ALUR TOPUP MANDIRI NON-ADMIN ---
	case "topup_user_password":
		// Simpan password/username yang diinput user
		tempUserData[userID]["username"] = text
		userStates[userID] = "topup_user_days"
		
		// Tampilkan menu durasi/harga
		msgText := fmt.Sprintf("🔑 *PASSWORD:* `%s`\n\n" +
			"💸 *DAFTAR HARGA:*\n" +
			"•  15 Hari: 5000\n" +
			"•  30 Hari: 12000\n" +
			"•  60 Hari: 25000\n" +
			"•  90 Hari: 35000\n\n" +
			"Silakan masukkan **DURASI HARI** yang Anda bayar (misalnya: *30*):", text)

		sendMessage(bot, msg.ChatID, msgText)

	case "topup_user_days":
		days, err := strconv.Atoi(text)
		if err != nil {
			sendMessage(bot, msg.ChatID, "❌ Durasi harus angka. Coba lagi:")
			return
		}
		
		// Cek apakah durasi yang dimasukkan sesuai dengan harga yang tersedia
		validDays := map[int]bool{15: true, 30: true, 60: true, 90: true}
		if _, ok := validDays[days]; !ok {
			sendMessage(bot, msg.ChatID, fmt.Sprintf("❌ Durasi hari `%d` tidak valid atau tidak sesuai daftar harga. Masukkan 15, 30, 60, atau 90.", days))
			return
		}
		
		// Panggil fungsi createUser yang sudah ada untuk membuat akun baru
		createUser(bot, msg.ChatID, tempUserData[userID]["username"], days)
		
		// Reset state dan kembali ke menu non-admin
		resetState(userID)
		showNonAdminMenu(bot, msg.ChatID) 
	}
}

[cite_start]func showUserSelection(bot *tgbotapi.BotAPI, chatID int64, page int, action string) { // [cite: 6]
	[cite_start]users, err := getUsers() // [cite: 6]
	[cite_start]if err != nil { // [cite: 6]
		[cite_start]sendMessage(bot, chatID, "❌ Gagal mengambil data user.") // [cite: 6]
		[cite_start]return // [cite: 6]
	}

	[cite_start]if len(users) == 0 { // [cite: 6]
		[cite_start]sendMessage(bot, chatID, "📂 Tidak ada user saat ini.") // [cite: 6]
		[cite_start]showMainMenu(bot, chatID) // [cite: 6]
		[cite_start]return // [cite: 6]
	}

	[cite_start]perPage := 10 // [cite: 6]
	[cite_start]totalPages := (len(users) + perPage - 1) / perPage // [cite: 6]

	[cite_start]if page < 1 { // [cite: 6]
		[cite_start]page = 1 // [cite: 6]
	}
	[cite_start]if page > totalPages { // [cite: 6]
		[cite_start]page = totalPages // [cite: 6]
	}

	[cite_start]start := (page - 1) * perPage // [cite: 6]
	[cite_start]end := start + perPage // [cite: 6]
	[cite_start]if end > len(users) { // [cite: 6]
		[cite_start]end = len(users) // [cite: 6]
	}

	[cite_start]var rows [][]tgbotapi.InlineKeyboardButton // [cite: 6]
	[cite_start]for _, u := range users[start:end] { // [cite: 7]
		[cite_start]statusIcon := "🟢" // [cite: 7]
		[cite_start]if u.Status == "Expired" { // [cite: 7]
			[cite_start]statusIcon = "🔴" // [cite: 7]
		}
		[cite_start]label := fmt.Sprintf("%s %s (Kadaluarsa: %s)", statusIcon, u.Password, u.Expired) // [cite: 7]
		[cite_start]data := fmt.Sprintf("select_%s:%s", action, u.Password) // [cite: 7]
		[cite_start]rows = append(rows, tgbotapi.NewInlineKeyboardRow( // [cite: 7]
			[cite_start]tgbotapi.NewInlineKeyboardButtonData(label, data), // [cite: 7]
		))
	}

	[cite_start]var navRow []tgbotapi.InlineKeyboardButton // [cite: 7]
	[cite_start]if page > 1 { // [cite: 7]
		[cite_start]navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData("⬅️ Halaman Sebelumnya", fmt.Sprintf("page_%s:%d", action, page-1))) // [cite: 7]
	}
	[cite_start]if page < totalPages { // [cite: 7]
		[cite_start]navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData("Halaman Selanjutnya ➡️", fmt.Sprintf("page_%s:%d", action, page+1))) // [cite: 7]
	}
	[cite_start]if len(navRow) > 0 { // [cite: 7]
		[cite_start]rows = append(rows, navRow) // [cite: 7]
	}

	[cite_start]rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("⬅️ Kembali ke Menu Utama", "cancel"))) // [cite: 7]

	[cite_start]title := "" // [cite: 7]
	[cite_start]switch action { // [cite: 7]
	[cite_start]case "delete": // [cite: 7]
		[cite_start]title = "🗑️ HAPUS AKUN" // [cite: 7]
	[cite_start]case "renew": // [cite: 7]
		[cite_start]title = "🔄 PERPANJANG AKUN" // [cite: 7]
	}

	[cite_start]msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("*%s*\nPilih user dari daftar di bawah (Halaman %d dari %d):", title, page, totalPages)) // [cite: 7]
	[cite_start]msg.ParseMode = "Markdown" // [cite: 7]
	[cite_start]msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...) // [cite: 8]
	[cite_start]sendAndTrack(bot, msg) // [cite: 8]
}

func showMainMenu(bot *tgbotapi.BotAPI, chatID int64) {
	[cite_start]ipInfo, _ := getIpInfo() // [cite: 8]
	[cite_start]domain := "Unknown" // [cite: 8]

	if res, err := apiCall("GET", "/info", nil); [cite_start]// [cite: 8]
	[cite_start]err == nil && res["success"] == true { // [cite: 9]
		[cite_start]if data, ok := res["data"].(map[string]interface{}); ok { // [cite: 9]
			if d, ok := data["domain"].(string); [cite_start]// [cite: 10]
			[cite_start]ok { // [cite: 10]
				[cite_start]domain = d // [cite: 10]
			}
		}
	}

	// Ambil Total Akun
	[cite_start]totalUsers := 0 // [cite: 10]
	if users, err := getUsers(); [cite_start]// [cite: 10]
	[cite_start]err == nil { // [cite: 11]
		[cite_start]totalUsers = len(users) // [cite: 11]
	}

	// --- Ambil User yang Akan Segera Kedaluwarsa (24 Jam) ---
	[cite_start]nearExpiredUsers, err := getNearExpiredUsers() // [cite: 11]
	[cite_start]expiredText := "" // [cite: 11]
	[cite_start]if err == nil && len(nearExpiredUsers) > 0 { // [cite: 11]
		[cite_start]expiredText += "\n\n⚠️ *AKUN AKAN SEGERA KADALUARSA (Dalam 24 Jam):*\n" // [cite: 11]
		[cite_start]for i, u := range nearExpiredUsers { // [cite: 11]
			[cite_start]if i >= 5 { // [cite: 11]
				[cite_start]expiredText += "... dan user lainnya\n" // [cite: 11]
				[cite_start]break // [cite: 11]
			}
			[cite_start]expiredText += fmt.Sprintf("•  `%s` (Expired: %s)\n", u.Password, u.Expired) // [cite: 11]
		}
	}
	// ----------------------------------------------------

	[cite_start]msgText := fmt.Sprintf("✨ *WELCOME TO BOT PGETUNNEL UDP ZIVPN*\n\n" + // [cite: 11]
		[cite_start]"Server Info:\n" + // [cite: 11]
		[cite_start]"•  🌐 *Domain*: `%s`\n" + // [cite: 11]
		[cite_start]"•  📍 *Lokasi*: `%s`\n" + // [cite: 11]
		[cite_start]"•  📡 *ISP*: `%s`\n" + // [cite: 11]
		[cite_start]"•  👤 *Total Akun*: `%d`\n\n" + // [cite: 11]
		[cite_start]"Untuk bantuan, hubungi Admin: @JesVpnt\n\n" + // [cite: 11]
		[cite_start]"Silakan pilih menu di bawah ini:", // [cite: 12]
		domain, ipInfo.City, ipInfo.Isp, totalUsers)

	[cite_start]msgText += expiredText // [cite: 12]

	// Hapus pesan
	[cite_start]deleteLastMessage(bot, chatID) // [cite: 12]

	// Buat keyboard inline
	[cite_start]keyboard := tgbotapi.NewInlineKeyboardMarkup( // [cite: 12]
		[cite_start]tgbotapi.NewInlineKeyboardRow( // [cite: 12]
			[cite_start]tgbotapi.NewInlineKeyboardButtonData("➕ Buat Akun", "menu_create"), // [cite: 12]
			[cite_start]tgbotapi.NewInlineKeyboardButtonData("🚀 Trial 1 Hari", "menu_trial_1"), // [cite: 12]
		),
		[cite_start]tgbotapi.NewInlineKeyboardRow( // [cite: 12]
			[cite_start]tgbotapi.NewInlineKeyboardButtonData("⭐ Buat 15 Hari", "menu_trial_15"), // [cite: 12]
			[cite_start]tgbotapi.NewInlineKeyboardButtonData("🌟 Buat 30 Hari", "menu_trial_30"), // [cite: 12]
		),
		[cite_start]tgbotapi.NewInlineKeyboardRow( // [cite: 12]
			[cite_start]tgbotapi.NewInlineKeyboardButtonData("✨ Buat 60 Hari", "menu_trial_60"), // [cite: 12]
			[cite_start]tgbotapi.NewInlineKeyboardButtonData("🔥 Buat 90 Hari", "menu_trial_90"), // [cite: 12]
		),
		[cite_start]tgbotapi.NewInlineKeyboardRow( // [cite: 12]
			tgbotapi.NewInlineKeyboardButtonData("💰 Topup Manual", "menu_topup"), // Tambahan: Topup Manual Admin
		),
		[cite_start]tgbotapi.NewInlineKeyboardRow( // [cite: 12]
			[cite_start]tgbotapi.NewInlineKeyboardButtonData("🔄 Renew Akun", "menu_renew"), // [cite: 12]
			[cite_start]tgbotapi.NewInlineKeyboardButtonData("🗑️ Hapus Akun", "menu_delete"), // [cite: 12]
		),
		[cite_start]tgbotapi.NewInlineKeyboardRow( // [cite: 12]
			[cite_start]tgbotapi.NewInlineKeyboardButtonData("📋 Daftar Akun", "menu_list"), // [cite: 12]
			[cite_start]tgbotapi.NewInlineKeyboardButtonData("📊 Info Server", "menu_info"), // [cite: 12]
		),
	)

	// Buat pesan foto dari URL
	[cite_start]photoMsg := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(MenuPhotoURL)) // [cite: 12]
	[cite_start]photoMsg.Caption = msgText // [cite: 12]
	[cite_start]photoMsg.ParseMode = "Markdown" // [cite: 12]
	[cite_start]photoMsg.ReplyMarkup = keyboard // [cite: 12]

	// Kirim foto
	[cite_start]sentMsg, err := bot.Send(photoMsg) // [cite: 12]
	[cite_start]if err == nil { // [cite: 12]
		// Track ID pesan yang baru dikirim (foto)
		[cite_start]lastMessageIDs[chatID] = sentMsg.MessageID // [cite: 12]
	[cite_start]} else { // [cite: 12]
		// Fallback jika pengiriman foto gagal
		[cite_start]log.Printf("Gagal mengirim foto menu dari URL (%s): %v. Mengirim sebagai teks biasa.", MenuPhotoURL, err) // [cite: 13]

		[cite_start]textMsg := tgbotapi.NewMessage(chatID, msgText) // [cite: 13]
		[cite_start]textMsg.ParseMode = "Markdown" // [cite: 13]
		[cite_start]textMsg.ReplyMarkup = keyboard // [cite: 13]
		[cite_start]sendAndTrack(bot, textMsg) // [cite: 13]
	}
}

// FUNGSI BARU UNTUK MENU NON-ADMIN
func showNonAdminMenu(bot *tgbotapi.BotAPI, chatID int64) {
	ipInfo, _ := getIpInfo()

	// Ambil Total Akun
	totalUsers := 0
	if users, err := getUsers(); err == nil {
		totalUsers = len(users)
	}

	msgText := fmt.Sprintf("✨ *WELCOME TO BOT ZIVPN*\n\n" +
		"Server Info:\n" +
		"•  📍 *Lokasi*: `%s`\n" +
		"•  📡 *ISP*: `%s`\n" +
		"•  👤 *Total Akun*: `%d`\n\n" +
		"Untuk bantuan, hubungi Admin: @JesVpnt\n\n" +
		"Silakan pilih menu di bawah ini untuk mendapatkan akses:",
		ipInfo.City, ipInfo.Isp, totalUsers)

	deleteLastMessage(bot, chatID)

	// Buat keyboard inline untuk non-admin
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🚀 Trial 1 Hari (Gratis)", "menu_trial_1"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💰 Beli/Topup Akun Mandiri", "menu_topup_user"),
		),
	)

	// Kirim sebagai teks
	textMsg := tgbotapi.NewMessage(chatID, msgText)
	textMsg.ParseMode = "Markdown"
	textMsg.ReplyMarkup = keyboard
	sendAndTrack(bot, textMsg)
}

[cite_start]func sendMessage(bot *tgbotapi.BotAPI, chatID int64, text string) { // [cite: 13]
	[cite_start]msg := tgbotapi.NewMessage(chatID, text) // [cite: 13]
	[cite_start]msg.ParseMode = "Markdown" // [cite: 13]
	[cite_start]if _, inState := userStates[chatID]; inState { // [cite: 13]
		[cite_start]cancelKb := tgbotapi.NewInlineKeyboardMarkup( // [cite: 13]
			[cite_start]tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("❌ Batal", "cancel")), // [cite: 13]
		)
		[cite_start]msg.ReplyMarkup = cancelKb // [cite: 13]
	}
	[cite_start]sendAndTrack(bot, msg) // [cite: 13]
}

[cite_start]func resetState(userID int64) { // [cite: 13]
	[cite_start]delete(userStates, userID) // [cite: 13]
	[cite_start]delete(tempUserData, userID) // [cite: 13]
}

[cite_start]func deleteLastMessage(bot *tgbotapi.BotAPI, chatID int64) { // [cite: 13]
	[cite_start]if msgID, ok := lastMessageIDs[chatID]; ok { // [cite: 13]
		[cite_start]deleteMsg := tgbotapi.NewDeleteMessage(chatID, msgID) // [cite: 13]
		[cite_start]bot.Request(deleteMsg) // [cite: 13]
		[cite_start]delete(lastMessageIDs, chatID) // [cite: 13]
	}
}

[cite_start]func sendAndTrack(bot *tgbotapi.BotAPI, msg tgbotapi.MessageConfig) { // [cite: 13]
	[cite_start]deleteLastMessage(bot, msg.ChatID) // [cite: 13]
	[cite_start]sentMsg, err := bot.Send(msg) // [cite: 13]
	[cite_start]if err == nil { // [cite: 13]
		[cite_start]lastMessageIDs[msg.ChatID] = sentMsg.MessageID // [cite: 13]
	}
}

// Fungsi untuk men-generate string acak sederhana
[cite_start]func generateRandomPassword(length int) string { // [cite: 13]
	[cite_start]const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789" // [cite: 13]
	[cite_start]seededRand := rand.New(rand.NewSource(time.Now().UnixNano())) // [cite: 13]
	[cite_start]b := make([]byte, length) // [cite: 13]
	[cite_start]for i := range b { // [cite: 13]
		[cite_start]b[i] = charset[seededRand.Intn(len(charset))] // [cite: 13]
	}
	[cite_start]return string(b) // [cite: 13]
}

[cite_start]// [cite: 14] Fungsi Background Worker untuk menghapus akun expired secara otomatis
[cite_start]func autoDeleteExpiredUsers(bot *tgbotapi.BotAPI, adminID int64) { // [cite: 14]
	[cite_start]users, err := getUsers() // [cite: 14]
	[cite_start]if err != nil { // [cite: 14]
		[cite_start]log.Printf("❌ [AutoDelete] Gagal mengambil data user: %v", err) // [cite: 14]
		[cite_start]return // [cite: 14]
	}

	[cite_start]deletedCount := 0 // [cite: 14]
	[cite_start]var deletedUsers []string // [cite: 14]

	[cite_start]for _, u := range users { // [cite: 14]
		[cite_start]if u.Status == "Expired" { // [cite: 14]
			// Memanggil endpoint delete API
			[cite_start]res, err := apiCall("POST", "/user/delete", map[string]interface{}{ // [cite: 14]
				[cite_start]"password": u.Password, // [cite: 14]
			})

			[cite_start]if err != nil { // [cite: 14]
				[cite_start]log.Printf("❌ [AutoDelete] Error API saat menghapus %s: %v", u.Password, err) // [cite: 14]
				[cite_start]continue // [cite: 14]
			}

			[cite_start]if res["success"] == true { // [cite: 14]
				[cite_start]deletedCount++ // [cite: 14]
				[cite_start]deletedUsers = append(deletedUsers, u.Password) // [cite: 14]
				[cite_start]log.Printf("✅ [AutoDelete] User expired %s berhasil dihapus.", u.Password) // [cite: 14]
			[cite_start]} else { // [cite: 14]
				[cite_start]log.Printf("❌ [AutoDelete] Gagal menghapus %s: %s", u.Password, res["message"]) // [cite: 14]
			}
		}
	}

	// Kirim notifikasi ke Admin jika ada akun yang dihapus
	[cite_start]if deletedCount > 0 { // [cite: 15]
		[cite_start]msgText := fmt.Sprintf("🗑️ *PEMBERSIHAN AKUN OTOMATIS*\n\n" + // [cite: 15]
			[cite_start]"Total `%d` akun kedaluwarsa telah dihapus secara otomatis:\n- %s", // [cite: 15]
			deletedCount, strings.Join(deletedUsers, "\n- "))

		[cite_start]notification := tgbotapi.NewMessage(adminID, msgText) // [cite: 15]
		[cite_start]notification.ParseMode = "Markdown" // [cite: 15]
		[cite_start]bot.Send(notification) // [cite: 15]
	}
}

// --- API Calls ---

[cite_start]func apiCall(method, endpoint string, payload interface{}) (map[string]interface{}, error) { // [cite: 15]
	[cite_start]var reqBody []byte // [cite: 15]
	[cite_start]var err error // [cite: 15]

	[cite_start]if payload != nil { // [cite: 15]
		[cite_start]reqBody, err = json.Marshal(payload) // [cite: 15]
		[cite_start]if err != nil { // [cite: 15]
			[cite_start]return nil, err // [cite: 15]
		}
	}

	[cite_start]client := &http.Client{} // [cite: 15]
	[cite_start]req, err := http.NewRequest(method, ApiUrl+endpoint, bytes.NewBuffer(reqBody)) // [cite: 15]
	[cite_start]if err != nil { // [cite: 15]
		[cite_start]return nil, err // [cite: 15]
	}

	[cite_start]req.Header.Set("Content-Type", "application/json") // [cite: 15]
	[cite_start]req.Header.Set("X-API-Key", ApiKey) // [cite: 15]

	[cite_start]resp, err := client.Do(req) // [cite: 15]
	[cite_start]if err != nil { // [cite: 15]
		[cite_start]return nil, err // [cite: 15]
	}
	[cite_start]defer resp.Body.Close() // [cite: 15]

	[cite_start]body, _ := ioutil.ReadAll(resp.Body) // [cite: 15]
	[cite_start]var result map[string]interface{} // [cite: 15]
	[cite_start]json.Unmarshal(body, &result) // [cite: 15]

	[cite_start]return result, nil // [cite: 15]
}

[cite_start]func getIpInfo() (IpInfo, error) { // [cite: 15]
	[cite_start]resp, err := http.Get("http://ip-api.com/json/") // [cite: 15]
	[cite_start]if err != nil { // [cite: 15]
		[cite_start]return IpInfo{}, err // [cite: 15]
	}
	[cite_start]defer resp.Body.Close() // [cite: 15]

	[cite_start]var info IpInfo // [cite: 16]
	[cite_start]if err := json.NewDecoder(resp.Body).Decode(&info); err != nil { // [cite: 16]
		[cite_start]return IpInfo{}, err // [cite: 16]
	}
	[cite_start]return info, nil // [cite: 16]
}

[cite_start]func getUsers() ([]UserData, error) { // [cite: 16]
	[cite_start]res, err := apiCall("GET", "/users", nil) // [cite: 16]
	[cite_start]if err != nil { // [cite: 16]
		[cite_start]return nil, err // [cite: 16]
	}

	[cite_start]if res["success"] != true { // [cite: 16]
		[cite_start]return nil, fmt.Errorf("failed to get users") // [cite: 16]
	}

	[cite_start]var users []UserData // [cite: 16]
	[cite_start]dataBytes, _ := json.Marshal(res["data"]) // [cite: 16]
	[cite_start]json.Unmarshal(dataBytes, &users) // [cite: 16]
	[cite_start]return users, nil // [cite: 16]
}

// Fungsi untuk mendapatkan user yang akan segera expired (dalam 24 jam)
[cite_start]func getNearExpiredUsers() ([]UserData, error) { // [cite: 16]
	[cite_start]users, err := getUsers() // [cite: 16]
	[cite_start]if err != nil { // [cite: 16]
		[cite_start]return nil, err // [cite: 16]
	}

	[cite_start]var nearExpired []UserData // [cite: 16]
	[cite_start]// Tentukan batas waktu: 24 jam dari sekarang // [cite: 16]
	[cite_start]expiryThreshold := time.Now().Add(24 * time.Hour) // [cite: 16]

	[cite_start]for _, u := range users { // [cite: 16]
		[cite_start]// Asumsi format expired: "YYYY-MM-DD hh:mm:ss" // [cite: 16]
		[cite_start]expiredTime, err := time.Parse("2006-01-02 15:04:05", u.Expired) // [cite: 16]
		[cite_start]if err != nil { // [cite: 17]
			[cite_start]continue // [cite: 17]
		}

		// Cek apakah waktu expired di masa depan DAN dalam 24 jam dari sekarang
		[cite_start]if expiredTime.After(time.Now()) && expiredTime.Before(expiryThreshold) { // [cite: 17]
			[cite_start]nearExpired = append(nearExpired, u) // [cite: 17]
		}
	}

	[cite_start]return nearExpired, nil // [cite: 17]
}

[cite_start]func createUser(bot *tgbotapi.BotAPI, chatID int64, username string, days int) { // [cite: 17]
	[cite_start]res, err := apiCall("POST", "/user/create", map[string]interface{}{ // [cite: 17]
		[cite_start]"password": username, // [cite: 17]
		[cite_start]"days":     days, // [cite: 17]
	})

	[cite_start]if err != nil { // [cite: 17]
		[cite_start]sendMessage(bot, chatID, "❌ Error API: "+err.Error()) // [cite: 17]
		[cite_start]return // [cite: 17]
	}

	[cite_start]if res["success"] == true { // [cite: 17]
		[cite_start]data := res["data"].(map[string]interface{}) // [cite: 17]

		[cite_start]ipInfo, _ := getIpInfo() // [cite: 17]

		[cite_start]msg := fmt.Sprintf("🎉 *AKUN BERHASIL DIBUAT*\n" + // [cite: 17]
			[cite_start]"━━━━━━━━━━━━━━━━━━━━━━━━━\n" + // [cite: 17]
			[cite_start]"🔑 *Password*: `%s`\n" + // [cite: 17]
			[cite_start]"🌐 *Domain*: `%s`\n" + // [cite: 17]
			[cite_start]"🗓️ *Kadaluarsa*: `%s`\n" + // [cite: 17]
			[cite_start]"📍 *Lokasi Server*: `%s`\n" + // [cite: 17]
			[cite_start]"📡 *ISP Server*: `%s`\n" + // [cite: 17]
			[cite_start]"━━━━━━━━━━━━━━━━━━━━━━━━━", // [cite: 17]
			data["password"], data["domain"], data["expired"], ipInfo.City, ipInfo.Isp)

		[cite_start]reply := tgbotapi.NewMessage(chatID, msg) // [cite: 17]
		[cite_start]reply.ParseMode = "Markdown" // [cite: 17]
		[cite_start]deleteLastMessage(bot, chatID) // [cite: 17]
		[cite_start]bot.Send(reply) // [cite: 17]
		
		// Jangan panggil showMainMenu di sini karena ini bisa dipanggil oleh non-admin
		// Panggilan ke showMainMenu atau showNonAdminMenu akan dilakukan di handleState atau handleCallback.
		
	[cite_start]} else { // [cite: 17]
		[cite_start]errMsg, ok := res["message"].(string) // [cite: 18]
		[cite_start]if !ok { // [cite: 18]
			errMsg = "Pesan error tidak diketahui dari API." [cite_start]// [cite: 19]
		}
		[cite_start]sendMessage(bot, chatID, fmt.Sprintf("❌ Gagal: %s", errMsg)) // [cite: 19]
		// Jika gagal, tampilkan main menu (admin) atau non-admin menu (non-admin)
		// Namun, karena fungsi ini dipanggil dari alur state, biarkan handleState yang mengontrol flow selanjutnya.
	}
}

// Fungsi umum untuk membuat akun trial dengan durasi hari yang ditentukan
[cite_start]func createGenericTrialUser(bot *tgbotapi.BotAPI, chatID int64, days int) { // [cite: 19]
	[cite_start]trialPassword := generateRandomPassword(8) // [cite: 19]

	[cite_start]res, err := apiCall("POST", "/user/create", map[string]interface{}{ // [cite: 19]
		[cite_start]"password": trialPassword, // [cite: 19]
		[cite_start]"minutes":  0, // [cite: 19]
		[cite_start]"days":     days, // [cite: 19]
	})

	[cite_start]if err != nil { // [cite: 19]
		[cite_start]sendMessage(bot, chatID, "❌ Error Komunikasi API: "+err.Error()) // [cite: 19]
		[cite_start]return // [cite: 19]
	}

	[cite_start]if res["success"] == true { // [cite: 19]
		[cite_start]data, ok := res["data"].(map[string]interface{}) // [cite: 20]
		[cite_start]if !ok { // [cite: 20]
			[cite_start]sendMessage(bot, chatID, "❌ Gagal: Format data respons dari API tidak valid.") // [cite: 20]
			// Cek apakah user admin atau non-admin
			config, _ := loadConfig()
			if chatID == config.AdminID {
				showMainMenu(bot, chatID)
			} else {
				showNonAdminMenu(bot, chatID)
			}
			[cite_start]return // [cite: 20]
		}

		// --- EKSTRAKSI DATA DENGAN PENGECEKAN TIPE (ROBUST) ---
		[cite_start]ipInfo, _ := getIpInfo() // [cite: 20]

		[cite_start]password := "N/A" // [cite: 20]
		if p, ok := data["password"].(string); [cite_start]// [cite: 20]
		[cite_start]ok { // [cite: 20]
			[cite_start]password = p // [cite: 20]
		}

		[cite_start]expired := "N/A" // [cite: 20]
		if e, ok := data["expired"].(string); [cite_start]// [cite: 21]
		[cite_start]ok { // [cite: 21]
			[cite_start]expired = e // [cite: 21]
		}

		// Ambil Domain (Prioritas 1: dari respons create)
		[cite_start]domain := "Unknown" // [cite: 21]
		if d, ok := data["domain"].(string); [cite_start]// [cite: 22]
		[cite_start]ok && d != "" { // [cite: 22]
			[cite_start]domain = d // [cite: 22]
		} else {
			// Prioritas 2: Fallback dengan memanggil /info API
			if infoRes, err := apiCall("GET", "/info", nil); [cite_start]// [cite: 23]
			[cite_start]err == nil && infoRes["success"] == true { // [cite: 23]
				[cite_start]if infoData, ok := infoRes["data"].(map[string]interface{}); ok { // [cite: 23]
					if d, ok := infoData["domain"].(string); [cite_start]// [cite: 24]
					[cite_start]ok { // [cite: 24]
						[cite_start]domain = d // [cite: 24]
					}
				}
			}
		}
		// --- END EKSTRAKSI DATA ---

		// 3. Susun dan Kirim Pesan Sukses
		[cite_start]msg := fmt.Sprintf("🚀 *BUAT %d HARI BERHASIL DIBUAT*\n" + // [cite: 24]
			[cite_start]"━━━━━━━━━━━━━━━━━━━━━━━━━\n" + // [cite: 24]
			[cite_start]"🔑 *Password*: `%s`\n" + // [cite: 24]
			[cite_start]"🌐 *Domain*: `%s`\n" + // [cite: 24]
			[cite_start]"⏳ *Durasi*: `%d Hari`\n" + // [cite: 24]
			[cite_start]"🗓️ *Kadaluarsa*: `%s`\n" + // [cite: 24]
			[cite_start]"📍 *Lokasi Server*: `%s`\n" + // [cite: 24]
			[cite_start]"📡 *ISP Server*: `%s`\n" + // [cite: 24]
			[cite_start]"━━━━━━━━━━━━━━━━━━━━━━━━━\n" + // [cite: 24]
			[cite_start]"❗️ *PERHATIAN: Akun ini hanya berlaku %d hari!*", // [cite: 24]
			days, password, domain, days, expired, ipInfo.City, ipInfo.Isp, days)

		[cite_start]reply := tgbotapi.NewMessage(chatID, msg) // [cite: 24]
		[cite_start]reply.ParseMode = "Markdown" // [cite: 24]
		[cite_start]deleteLastMessage(bot, chatID) // [cite: 24]
		[cite_start]bot.Send(reply) // [cite: 24]
		
		// Cek apakah user admin atau non-admin
		config, _ := loadConfig()
		if chatID == config.AdminID {
			showMainMenu(bot, chatID)
		} else {
			showNonAdminMenu(bot, chatID)
		}
	} else {
		// 4. Penanganan Kegagalan API
		[cite_start]errMsg, ok := res["message"].(string) // [cite: 25]
		[cite_start]if !ok { // [cite: 25]
			errMsg = "Respon kegagalan dari API tidak diketahui." [cite_start]// [cite: 25]
		}
		[cite_start]sendMessage(bot, chatID, fmt.Sprintf("❌ Gagal membuat Trial: %s", errMsg)) // [cite: 25]
		
		// Cek apakah user admin atau non-admin
		config, _ := loadConfig()
		if chatID == config.AdminID {
			showMainMenu(bot, chatID)
		} else {
			showNonAdminMenu(bot, chatID)
		}
	}
}

// FUNGSI createTrialUser YANG LAMA DIHAPUS/DIUBAH KE createGenericTrialUser(..., 1)


[cite_start]func deleteUser(bot *tgbotapi.BotAPI, chatID int64, username string) { // [cite: 25]
	[cite_start]res, err := apiCall("POST", "/user/delete", map[string]interface{}{ // [cite: 25]
		[cite_start]"password": username, // [cite: 25]
	})

	[cite_start]if err != nil { // [cite: 25]
		[cite_start]sendMessage(bot, chatID, "❌ Error API: "+err.Error()) // [cite: 25]
		[cite_start]return // [cite: 25]
	}

	[cite_start]if res["success"] == true { // [cite: 25]
		[cite_start]msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("✅ Password `%s` berhasil *DIHAPUS*.", username)) // [cite: 25]
		[cite_start]msg.ParseMode = "Markdown" // [cite: 25]
		[cite_start]deleteLastMessage(bot, chatID) // [cite: 25]
		[cite_start]bot.Send(msg) // [cite: 25]
		[cite_start]showMainMenu(bot, chatID) // [cite: 25]
	[cite_start]} else { // [cite: 25]
		[cite_start]errMsg, ok := res["message"].(string) // [cite: 26]
		[cite_start]if !ok { // [cite: 26]
			errMsg = "Pesan error tidak diketahui dari API." [cite_start]// [cite: 26]
		}
		[cite_start]sendMessage(bot, chatID, fmt.Sprintf("❌ Gagal menghapus: %s", errMsg)) // [cite: 26]
		[cite_start]showMainMenu(bot, chatID) // [cite: 26]
	}
}

[cite_start]func renewUser(bot *tgbotapi.BotAPI, chatID int64, username string, days int) { // [cite: 26]
	[cite_start]res, err := apiCall("POST", "/user/renew", map[string]interface{}{ // [cite: 26]
		[cite_start]"password": username, // [cite: 26]
		[cite_start]"days":     days, // [cite: 26]
	})

	[cite_start]if err != nil { // [cite: 26]
		[cite_start]sendMessage(bot, chatID, "❌ Error API: "+err.Error()) // [cite: 26]
		[cite_start]return // [cite: 26]
	}

	[cite_start]if res["success"] == true { // [cite: 26]
		[cite_start]data := res["data"].(map[string]interface{}) // [cite: 26]

		[cite_start]ipInfo, _ := getIpInfo() // [cite: 26]

		[cite_start]domain := "Unknown" // [cite: 26]
		if d, ok := data["domain"].(string); [cite_start]// [cite: 27]
		[cite_start]ok && d != "" { // [cite: 27]
			[cite_start]domain = d // [cite: 27]
		[cite_start]} else { // [cite: 27]
			if infoRes, err := apiCall("GET", "/info", nil); [cite_start]// [cite: 28]
			[cite_start]err == nil && infoRes["success"] == true { // [cite: 28]
				[cite_start]if infoData, ok := infoRes["data"].(map[string]interface{}); ok { // [cite: 28]
					if d, ok := infoData["domain"].(string); [cite_start]// [cite: 29]
					[cite_start]ok { // [cite: 29]
						[cite_start]domain = d // [cite: 29]
					}
				}
			}
		}

		[cite_start]msg := fmt.Sprintf("✅ *AKUN BERHASIL DIPERPANJANG* (%d Hari)\n" + // [cite: 29]
			[cite_start]"━━━━━━━━━━━━━━━━━━━━━━━━━\n" + // [cite: 29]
			[cite_start]"🔑 *Password*: `%s`\n" + // [cite: 29]
			[cite_start]"🌐 *Domain*: `%s`\n" + // [cite: 29]
			[cite_start]"🗓️ *Kadaluarsa Baru*: `%s`\n" + // [cite: 29]
			[cite_start]"📍 *Lokasi Server*: `%s`\n" + // [cite: 29]
			[cite_start]"📡 *ISP Server*: `%s`\n" + // [cite: 29]
			[cite_start]"━━━━━━━━━━━━━━━━━━━━━━━━━", // [cite: 29]
			days, data["password"], domain, data["expired"], ipInfo.City, ipInfo.Isp)

		[cite_start]reply := tgbotapi.NewMessage(chatID, msg) // [cite: 29]
		[cite_start]reply.ParseMode = "Markdown" // [cite: 29]
		[cite_start]deleteLastMessage(bot, chatID) // [cite: 29]
		[cite_start]bot.Send(reply) // [cite: 29]
		[cite_start]showMainMenu(bot, chatID) // [cite: 29]
	[cite_start]} else { // [cite: 29]
		[cite_start]errMsg, ok := res["message"].(string) // [cite: 30]
		[cite_start]if !ok { // [cite: 30]
			errMsg = "Pesan error tidak diketahui dari API." [cite_start]// [cite: 30]
		}
		[cite_start]sendMessage(bot, chatID, fmt.Sprintf("❌ Gagal memperpanjang: %s", errMsg)) // [cite: 30]
		[cite_start]showMainMenu(bot, chatID) // [cite: 30]
	}
}

[cite_start]func listUsers(bot *tgbotapi.BotAPI, chatID int64) { // [cite: 30]
	[cite_start]res, err := apiCall("GET", "/users", nil) // [cite: 30]
	[cite_start]if err != nil { // [cite: 30]
		[cite_start]sendMessage(bot, chatID, "❌ Error API: "+err.Error()) // [cite: 30]
		[cite_start]return // [cite: 30]
	}

	[cite_start]if res["success"] == true { // [cite: 30]
		[cite_start]users := res["data"].([]interface{}) // [cite: 30]
		[cite_start]if len(users) == 0 { // [cite: 30]
			[cite_start]sendMessage(bot, chatID, "📂 Tidak ada user saat ini.") // [cite: 30]
			[cite_start]showMainMenu(bot, chatID) // [cite: 30]
			[cite_start]return // [cite: 30]
		}

		[cite_start]msg := fmt.Sprintf("📋 *DAFTAR AKUN ZIVPN* (Total: %d)\n\n", len(users)) // [cite: 30]
		[cite_start]for i, u := range users { // [cite: 30]
			[cite_start]user := u.(map[string]interface{}) // [cite: 30]
			[cite_start]statusIcon := "🟢" // [cite: 30]
			[cite_start]if user["status"] == "Expired" { // [cite: 30]
				[cite_start]statusIcon = "🔴" // [cite: 30]
			}
			[cite_start]msg += fmt.Sprintf("%d. %s `%s`\n    _Kadaluarsa: %s_\n", i+1, statusIcon, user["password"], user["expired"]) // [cite: 30]
		}

		[cite_start]reply := tgbotapi.NewMessage(chatID, msg) // [cite: 30]
		[cite_start]reply.ParseMode = "Markdown" // [cite: 30]
		[cite_start]sendAndTrack(bot, reply) // [cite: 30]
	[cite_start]} else { // [cite: 30]
		[cite_start]sendMessage(bot, chatID, "❌ Gagal mengambil data daftar akun.") // [cite: 30]
	}
}

[cite_start]func systemInfo(bot *tgbotapi.BotAPI, chatID int64) { // [cite: 31]
	[cite_start]res, err := apiCall("GET", "/info", nil) // [cite: 31]
	[cite_start]if err != nil { // [cite: 31]
		[cite_start]sendMessage(bot, chatID, "❌ Error API: "+err.Error()) // [cite: 31]
		[cite_start]return // [cite: 31]
	}

	[cite_start]if res["success"] == true { // [cite: 31]
		[cite_start]data := res["data"].(map[string]interface{}) // [cite: 31]

		[cite_start]ipInfo, _ := getIpInfo() // [cite: 31]

		[cite_start]msg := fmt.Sprintf("⚙️ *INFORMASI DETAIL SERVER*\n" + // [cite: 31]
			[cite_start]"━━━━━━━━━━━━━━━━━━━━━━━━━\n" + // [cite: 31]
			[cite_start]"🌐 *Domain*: `%s`\n" + // [cite: 31]
			[cite_start]"🖥️ *IP Public*: `%s`\n" + // [cite: 31]
			[cite_start]"🔌 *Port*: `%s`\n" + // [cite: 31]
			[cite_start]"🔧 *Layanan*: `%s`\n" + // [cite: 31]
			[cite_start]"📍 *Lokasi Server*: `%s`\n" + // [cite: 31]
			[cite_start]"📡 *ISP Server*: `%s`\n" + // [cite: 31]
			[cite_start]"━━━━━━━━━━━━━━━━━━━━━━━━━", // [cite: 31]
			data["domain"], data["public_ip"], data["port"], data["service"], ipInfo.City, ipInfo.Isp)

		[cite_start]reply := tgbotapi.NewMessage(chatID, msg) // [cite: 31]
		[cite_start]reply.ParseMode = "Markdown" // [cite: 31]
		[cite_start]deleteLastMessage(bot, chatID) // [cite: 31]
		[cite_start]bot.Send(reply) // [cite: 31]
		[cite_start]showMainMenu(bot, chatID) // [cite: 31]
	[cite_start]} else { // [cite: 31]
		[cite_start]sendMessage(bot, chatID, "❌ Gagal mengambil info sistem.") // [cite: 31]
	}
}

[cite_start]func loadConfig() (BotConfig, error) { // [cite: 31]
	[cite_start]var config BotConfig // [cite: 31]
	[cite_start]file, err := ioutil.ReadFile(BotConfigFile) // [cite: 31]
	[cite_start]if err != nil { // [cite: 31]
		[cite_start]return config, err // [cite: 31]
	}
	[cite_start]err = json.Unmarshal(file, &config) // [cite: 31]
	[cite_start]return config, err // [cite: 31]
}