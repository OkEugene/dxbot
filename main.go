package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var (
	subscribers = make(map[int64]bool) // Подписчики на рассылку
	activeChats = make(map[int64]bool) // Активные чаты с менеджером
	chatPairs   = make(map[int64]int64) // Связь пользователь-админ
	mutex       sync.Mutex             // Для потокобезопасности
)

func main() {
	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	adminID, _ := strconv.ParseInt(os.Getenv("ADMIN_ID"), 10, 64)

	if botToken == "" || adminID == 0 {
		log.Fatal("Требуются TELEGRAM_BOT_TOKEN и ADMIN_ID")
	}

	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Fatal(err)
	}

	bot.Debug = true
	log.Printf("Бот запущен: @%s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message != nil {
			handleMessage(bot, update.Message, adminID)
		} else if update.CallbackQuery != nil {
			handleCallback(bot, update.CallbackQuery, adminID)
		}
	}
}

func handleMessage(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, adminID int64) {
	chatID := msg.Chat.ID

	if chatID == adminID {
		if msg.Photo != nil || msg.Document != nil {
			sendStockUpdate(bot, msg)
			return
		}
		handleAdminReply(bot, msg)
		return
	}

	mutex.Lock()
	isActive := activeChats[chatID]
	mutex.Unlock()

	if isActive {
		forwardToAdmin(bot, chatID, msg.MessageID, adminID)
		return
	}

	if msg.Text == "/start" {
		sendMainMenu(bot, chatID)
	} else {
		sendMainMenu(bot, chatID)
	}
}

func handleCallback(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery, adminID int64) {
	chatID := query.Message.Chat.ID
	data := query.Data

	bot.Send(tgbotapi.NewCallback(query.ID, ""))

	switch data {
	case "subscribe_stock":
		mutex.Lock()
		subscribers[chatID] = true
		mutex.Unlock()

		user := query.From
		bot.Send(tgbotapi.NewMessage(chatID, "✅ Вы подписались на рассылку остатков"))
		
		adminMsg := fmt.Sprintf("🎉 Новый подписчик:\nID: %d\nUsername: @%s\nИмя: %s",
			user.ID,
			getUsername(user),
			getFullName(user),
		)
		bot.Send(tgbotapi.NewMessage(adminID, adminMsg))

		sendMainMenu(bot, chatID)

	case "contact_manager":
		mutex.Lock()
		activeChats[chatID] = true
		chatPairs[chatID] = adminID
		mutex.Unlock()

		bot.Send(tgbotapi.NewMessage(chatID, "📩 Чат с менеджером открыт. Отправьте ваше сообщение."))
		
		user := query.From
		adminMsg := fmt.Sprintf("❗ Новый чат:\nID: %d\nUsername: @%s\nИмя: %s",
			user.ID,
			getUsername(user),
			getFullName(user),
		)
		bot.Send(tgbotapi.NewMessage(adminID, adminMsg))

	case "close_menu":
		bot.Send(tgbotapi.NewMessage(chatID, "Меню закрыто. Для открытия напишите /start"))
	}
}

func sendMainMenu(bot *tgbotapi.BotAPI, chatID int64) {
	mutex.Lock()
	isSubscribed := subscribers[chatID]
	mutex.Unlock()

	subscribeBtn := tgbotapi.NewInlineKeyboardButtonData("📊 Подписаться на рассылку остатков", "subscribe_stock")
	if isSubscribed {
		subscribeBtn = tgbotapi.NewInlineKeyboardButtonData("✅ Вы подписаны на остатки", "subscribe_stock")
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(subscribeBtn),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("👨‍💼 Написать менеджеру", "contact_manager"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Закрыть меню", "close_menu"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, "🔷 *Главное меню* 🔷\nВыберите действие:")
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard
	bot.Send(msg)
}

func sendStockUpdate(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	mutex.Lock()
	defer mutex.Unlock()

	if len(subscribers) == 0 {
		bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "❌ Нет подписчиков для рассылки"))
		return
	}

	caption := "🛒 *Актуальные остатки на складе:*\n"
	if msg.Caption != "" {
		caption = msg.Caption
	}

	successCount := 0
	for userID := range subscribers {
		var err error
		
		if len(msg.Photo) > 0 {
			photo := msg.Photo[len(msg.Photo)-1]
			photoMsg := tgbotapi.NewPhoto(userID, tgbotapi.FileID(photo.FileID))
			photoMsg.Caption = caption
			photoMsg.ParseMode = "Markdown"
			_, err = bot.Send(photoMsg)
		} else if msg.Document != nil {
			docMsg := tgbotapi.NewDocument(userID, tgbotapi.FileID(msg.Document.FileID))
			docMsg.Caption = caption
			docMsg.ParseMode = "Markdown"
			_, err = bot.Send(docMsg)
		}

		if err != nil {
			log.Printf("Ошибка отправки пользователю %d: %v", userID, err)
		} else {
			successCount++
		}
	}

	bot.Send(tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("✅ Рассылка отправлена %d подписчикам", successCount)))
}

func handleAdminReply(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if msg.ReplyToMessage != nil && msg.ReplyToMessage.ForwardFrom != nil {
		userID := msg.ReplyToMessage.ForwardFrom.ID
		reply := tgbotapi.NewMessage(userID, "👨‍💼 Ответ менеджера:\n"+msg.Text)
		bot.Send(reply)
		return
	}

	mutex.Lock()
	defer mutex.Unlock()
	
	for userID, admID := range chatPairs {
		if admID == msg.Chat.ID {
			reply := tgbotapi.NewMessage(userID, "👨‍💼 Сообщение менеджера:\n"+msg.Text)
			bot.Send(reply)
			break
		}
	}
}

func forwardToAdmin(bot *tgbotapi.BotAPI, chatID int64, messageID int, adminID int64) {
	mutex.Lock()
	chatPairs[chatID] = adminID
	mutex.Unlock()

	forward := tgbotapi.NewForward(adminID, chatID, messageID)
	bot.Send(forward)
}

func getFullName(user *tgbotapi.User) string {
	name := user.FirstName
	if user.LastName != "" {
		name += " " + user.LastName
	}
	if name == "" {
		name = "Не указано"
	}
	return name
}

func getUsername(user *tgbotapi.User) string {
	if user.UserName == "" {
		return "нет username"
	}
	return user.UserName
}