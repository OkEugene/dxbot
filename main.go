package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type ChatSession struct {
	UserID  int64
	AdminID int64
}

var (
	activeSessions = make(map[int64]ChatSession) // userID -> session
	subscribers    = make(map[int64]bool)
	mutex          sync.Mutex
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

	// Ответы админа
	if chatID == adminID {
		if msg.Photo != nil || msg.Document != nil {
			broadcastToSubscribers(bot, msg)
			return
		}
		handleAdminMessage(bot, msg)
		return
	}

	// Проверка активного чата
	mutex.Lock()
	session, exists := activeSessions[chatID]
	mutex.Unlock()

	if msg.Text == "/stop" && exists {
		endChatSession(bot, session)
		return
	}

	if exists {
		if msg.Text == "/start" {
			sendMainMenu(bot, chatID)
			return
		}
		forwardToAdmin(bot, msg, session.AdminID)
		return
	}

	switch msg.Text {
	case "/start":
		sendMainMenu(bot, chatID)
	default:
		bot.Send(tgbotapi.NewMessage(chatID, "Используйте кнопки меню (/start)"))
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
		activeSessions[chatID] = ChatSession{
			UserID:  chatID,
			AdminID: adminID,
		}
		mutex.Unlock()
		bot.Send(tgbotapi.NewMessage(chatID, "💬 Чат с менеджером открыт. Напишите ваш вопрос."))
		bot.Send(tgbotapi.NewMessage(adminID, fmt.Sprintf("Новый чат с пользователем %d", chatID)))

	case "end_chat":
		mutex.Lock()
		session := activeSessions[chatID]
		mutex.Unlock()
		endChatSession(bot, session)

	case "close":
		bot.Send(tgbotapi.NewMessage(chatID, "Меню закрыто. Используйте /start для открытия"))
	}
}

func endChatSession(bot *tgbotapi.BotAPI, session ChatSession) {
	mutex.Lock()
	delete(activeSessions, session.UserID)
	mutex.Unlock()

	bot.Send(tgbotapi.NewMessage(session.UserID, "🗣 Чат с менеджером завершен"))
	bot.Send(tgbotapi.NewMessage(session.AdminID, fmt.Sprintf("Чат с %d завершен", session.UserID)))
	sendMainMenu(bot, session.UserID)
}

func sendMainMenu(bot *tgbotapi.BotAPI, chatID int64) {
	mutex.Lock()
	isSubscribed := subscribers[chatID]
	mutex.Unlock()

	var subscribeBtn tgbotapi.InlineKeyboardButton
	if isSubscribed {
		subscribeBtn = tgbotapi.NewInlineKeyboardButtonData("❌ Отписаться от обновлений", "unsubscribe")
	} else {
		subscribeBtn = tgbotapi.NewInlineKeyboardButtonData("📩 Подписаться на обновления", "subscribe")
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			subscribeBtn,
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💬 Написать менеджеру", "contact_manager"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🚪 Закрыть меню", "close"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, "📱 Главное меню бота\n\nВыберите действие:")
	msg.ReplyMarkup = keyboard
	bot.Send(msg)
}

func handleAdminMessage(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if msg.ReplyToMessage != nil && msg.ReplyToMessage.ForwardFrom != nil {
		userID := msg.ReplyToMessage.ForwardFrom.ID
		sendToUser(bot, userID, msg.Text)
		return
	}

	mutex.Lock()
	defer mutex.Unlock()

	for userID, session := range activeSessions {
		if session.AdminID == msg.Chat.ID {
			sendToUser(bot, userID, msg.Text)
			break
		}
	}
}

func sendToUser(bot *tgbotapi.BotAPI, userID int64, text string) {
	msg := tgbotapi.NewMessage(userID, "👨‍💼 Ответ менеджера:\n"+text)
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Ошибка отправки пользователю %d: %v", userID, err)
	}
}

func forwardToAdmin(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, adminID int64) {
	mutex.Lock()
	if _, exists := activeSessions[msg.Chat.ID]; !exists {
		activeSessions[msg.Chat.ID] = ChatSession{
			UserID:  msg.Chat.ID,
			AdminID: adminID,
		}
	}
	mutex.Unlock()

	forward := tgbotapi.NewForward(adminID, msg.Chat.ID, msg.MessageID)
	if _, err := bot.Send(forward); err != nil {
		log.Printf("Ошибка пересылки: %v", err)
	}
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