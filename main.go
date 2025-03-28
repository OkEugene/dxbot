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
	activeChats   = make(map[int64]bool)
	subscribers   = make(map[int64]bool)
	adminSessions = make(map[int64]int64) // userID -> adminID
	mutex         sync.Mutex
)

func main() {
	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	adminID, _ := strconv.ParseInt(os.Getenv("ADMIN_ID"), 10, 64)

	if botToken == "" {
		log.Fatal("Требуется TELEGRAM_BOT_TOKEN")
	}
	if adminID == 0 {
		log.Fatal("Требуется ADMIN_ID")
	}

	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Fatal(err)
	}
	bot.Debug = true
	log.Printf("Авторизован как @%s", bot.Self.UserName)

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
			broadcastToSubscribers(bot, msg)
			return
		}
		handleAdminReply(bot, msg)
		return
	}

	mutex.Lock()
	isActive := activeChats[chatID]
	mutex.Unlock()

	if msg.Text == "/stop" && isActive {
		endChatSession(bot, chatID, adminID)
		return
	}

	if isActive {
		if msg.Text == "/start" {
			sendMainMenu(bot, chatID)
			return
		}
		forwardToAdmin(bot, chatID, msg.MessageID, adminID)
		return
	}

	switch msg.Text {
	case "/start":
		sendMainMenu(bot, chatID)
	default:
		sendMainMenu(bot, chatID)
	}
}

// Реализация недостающих функций:

func handleAdminReply(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if msg.ReplyToMessage == nil {
		return
	}

	// Ищем оригинальное сообщение пользователя
	mutex.Lock()
	var userID int64
	for u, a := range adminSessions {
		if a == msg.Chat.ID {
			userID = u
			break
		}
	}
	mutex.Unlock()

	if userID != 0 {
		reply := tgbotapi.NewMessage(userID, msg.Text)
		bot.Send(reply)
	}
}

func endChatSession(bot *tgbotapi.BotAPI, userID, adminID int64) {
	mutex.Lock()
	delete(activeChats, userID)
	delete(adminSessions, userID)
	mutex.Unlock()

	bot.Send(tgbotapi.NewMessage(userID, "🗣 Чат с менеджером завершен"))
	bot.Send(tgbotapi.NewMessage(adminID, fmt.Sprintf("Чат с %d завершен", userID)))
	sendMainMenu(bot, userID)
}

func forwardToAdmin(bot *tgbotapi.BotAPI, chatID int64, messageID int, adminID int64) {
	mutex.Lock()
	adminSessions[chatID] = adminID
	mutex.Unlock()

	forward := tgbotapi.NewForward(adminID, chatID, messageID)
	bot.Send(forward)
}

func broadcastToSubscribers(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	mutex.Lock()
	defer mutex.Unlock()

	if len(subscribers) == 0 {
		bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Нет подписчиков для рассылки"))
		return
	}

	caption := "Добрый день! Новое поступление на склад:"
	if msg.Caption != "" {
		caption = msg.Caption
	}

	for userID := range subscribers {
		if len(msg.Photo) > 0 {
			photo := msg.Photo[len(msg.Photo)-1]
			photoMsg := tgbotapi.NewPhoto(userID, tgbotapi.FileID(photo.FileID))
			photoMsg.Caption = caption
			bot.Send(photoMsg)
		} else if msg.Document != nil {
			docMsg := tgbotapi.NewDocument(userID, tgbotapi.FileID(msg.Document.FileID))
			docMsg.Caption = caption
			bot.Send(docMsg)
		}
	}
}

func handleCallback(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery, adminID int64) {
	chatID := query.Message.Chat.ID
	data := query.Data

	bot.Send(tgbotapi.NewCallback(query.ID, ""))

	switch data {
	case "subscribe":
		mutex.Lock()
		subscribers[chatID] = true
		mutex.Unlock()
		bot.Send(tgbotapi.NewMessage(chatID, "✅ Вы подписались на обновления"))
		sendMainMenu(bot, chatID)

	case "unsubscribe":
		mutex.Lock()
		delete(subscribers, chatID)
		mutex.Unlock()
		bot.Send(tgbotapi.NewMessage(chatID, "❌ Вы отписались от обновлений"))
		sendMainMenu(bot, chatID)

	case "contact_manager":
		mutex.Lock()
		activeChats[chatID] = true
		adminSessions[chatID] = adminID
		mutex.Unlock()
		msg := tgbotapi.NewMessage(chatID, "💬 Чат с менеджером открыт. Пишите ваши вопросы!")
		bot.Send(msg)
		bot.Send(tgbotapi.NewMessage(adminID, fmt.Sprintf("Новый чат с пользователем %d", chatID)))

	case "end_chat":
		endChatSession(bot, chatID, adminID)

	case "close":
		bot.Send(tgbotapi.NewMessage(chatID, "Меню закрыто. Используйте /start для открытия"))
	}
}

func sendMainMenu(bot *tgbotapi.BotAPI, chatID int64) {
	mutex.Lock()
	isSubscribed := subscribers[chatID]
	mutex.Unlock()

	subscribeBtn := tgbotapi.NewInlineKeyboardButtonData("📩 Подписаться", "subscribe")
	if isSubscribed {
		subscribeBtn = tgbotapi.NewInlineKeyboardButtonData("❌ Отписаться", "unsubscribe")
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(subscribeBtn),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💬 Написать менеджеру", "contact_manager"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, "Главное меню:")
	msg.ReplyMarkup = keyboard
	bot.Send(msg)
}