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
	activeChats   = make(map[int64]int64) // userID -> adminID
	subscribers   = make(map[int64]bool)
	mutex         sync.Mutex
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

	// Сообщения от админа
	if chatID == adminID {
		if msg.ReplyToMessage != nil && msg.ReplyToMessage.ForwardFrom != nil {
			// Ответ на пересланное сообщение
			userID := msg.ReplyToMessage.ForwardFrom.ID
			sendToUser(bot, userID, "👨‍💼 Ответ менеджера:\n"+msg.Text)
		} else {
			// Прямое сообщение (отправим последнему чату)
			mutex.Lock()
			for userID, admID := range activeChats {
				if admID == adminID {
					sendToUser(bot, userID, "👨‍💼 Сообщение менеджера:\n"+msg.Text)
					break
				}
			}
			mutex.Unlock()
		}
		return
	}

	// Сообщения от пользователей
	mutex.Lock()
	_, isActive := activeChats[chatID]
	mutex.Unlock()

	if msg.Text == "/stop" && isActive {
		endChatSession(bot, chatID, adminID)
		return
	}

	if isActive {
		forwardToAdmin(bot, chatID, msg.MessageID, adminID)
		return
	}

	switch msg.Text {
	case "/start":
		sendMainMenu(bot, chatID)
	default:
		msg := tgbotapi.NewMessage(chatID, "ℹ️ Пожалуйста, используйте кнопки меню (/start)")
		bot.Send(msg)
	}
}

func handleCallback(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery, adminID int64) {
	chatID := query.Message.Chat.ID
	data := query.Data

	// Удаляем "часики" на кнопке
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
		activeChats[chatID] = adminID
		mutex.Unlock()
		bot.Send(tgbotapi.NewMessage(chatID, "📩 Теперь вы можете писать менеджеру. Отправьте ваше сообщение.\n\nЧтобы завершить диалог, отправьте /stop"))
		bot.Send(tgbotapi.NewMessage(adminID, fmt.Sprintf("🔔 Новый чат с пользователем %d", chatID)))

	case "close_menu":
		bot.Send(tgbotapi.NewMessage(chatID, "Меню закрыто. Для открытия отправьте /start"))
	}
}

func sendMainMenu(bot *tgbotapi.BotAPI, chatID int64) {
	mutex.Lock()
	isSubscribed := subscribers[chatID]
	mutex.Unlock()

	// Улучшенные кнопки с эмодзи и четкими названиями
	subscribeBtn := tgbotapi.NewInlineKeyboardButtonData("🔔 Подписаться на новости", "subscribe")
	if isSubscribed {
		subscribeBtn = tgbotapi.NewInlineKeyboardButtonData("🔕 Отписаться от новостей", "unsubscribe")
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(subscribeBtn),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💬 Чат с менеджером", "contact_manager"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Закрыть меню", "close_menu"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, "📱 *Главное меню*")
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard
	bot.Send(msg)
}

func sendToUser(bot *tgbotapi.BotAPI, userID int64, text string) {
	msg := tgbotapi.NewMessage(userID, text)
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Ошибка отправки пользователю %d: %v", userID, err)
	}
}

func forwardToAdmin(bot *tgbotapi.BotAPI, chatID int64, messageID int, adminID int64) {
	mutex.Lock()
	activeChats[chatID] = adminID
	mutex.Unlock()

	forward := tgbotapi.NewForward(adminID, chatID, messageID)
	if _, err := bot.Send(forward); err != nil {
		log.Printf("Ошибка пересылки: %v", err)
	}
}

func endChatSession(bot *tgbotapi.BotAPI, chatID int64, adminID int64) {
	mutex.Lock()
	delete(activeChats, chatID)
	mutex.Unlock()

	bot.Send(tgbotapi.NewMessage(chatID, "🗣 Чат с менеджером завершен. Для нового диалога используйте меню (/start)"))
	bot.Send(tgbotapi.NewMessage(adminID, fmt.Sprintf("🔕 Чат с пользователем %d завершен", chatID)))
	
}