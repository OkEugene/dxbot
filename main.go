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
		if msg.Photo != nil || msg.Document != nil {
			sendStockToSubscribers(bot, msg)
			return
		}
		handleAdminMessage(bot, msg)
		return
	}

	// Сообщения от пользователей
	mutex.Lock()
	_, isActive := activeChats[chatID]
	mutex.Unlock()

	if isActive {
		forwardToAdmin(bot, chatID, msg.MessageID, adminID)
		return
	}

	// Открываем меню при любом сообщении, если чат не активен
	sendMainMenu(bot, chatID)
}

func handleAdminMessage(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if msg.ReplyToMessage != nil && msg.ReplyToMessage.ForwardFrom != nil {
		// Ответ на пересланное сообщение
		userID := msg.ReplyToMessage.ForwardFrom.ID
		sendToUser(bot, userID, "👨‍💼 Ответ менеджера:\n"+msg.Text)
	} else {
		// Прямое сообщение (отправим последнему чату)
		mutex.Lock()
		for userID, admID := range activeChats {
			if admID == msg.Chat.ID {
				sendToUser(bot, userID, "👨‍💼 Сообщение менеджера:\n"+msg.Text)
				break
			}
		}
		mutex.Unlock()
	}
}

func handleCallback(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery, adminID int64) {
	chatID := query.Message.Chat.ID
	data := query.Data

	// Удаляем "часики" на кнопке
	bot.Send(tgbotapi.NewCallback(query.ID, ""))

	switch data {
	case "subscribe_news":
		mutex.Lock()
		subscribers[chatID] = true
		mutex.Unlock()
		bot.Send(tgbotapi.NewMessage(chatID, "✅ Вы подписались на рассылку новостей"))
		sendMainMenu(bot, chatID)

	case "unsubscribe_news":
		mutex.Lock()
		delete(subscribers, chatID)
		mutex.Unlock()
		bot.Send(tgbotapi.NewMessage(chatID, "❌ Вы отписались от рассылки новостей"))
		sendMainMenu(bot, chatID)

	case "contact_manager":
		mutex.Lock()
		activeChats[chatID] = adminID
		mutex.Unlock()
		bot.Send(tgbotapi.NewMessage(chatID, "📩 Чат с менеджером открыт. Напишите ваш вопрос."))
		bot.Send(tgbotapi.NewMessage(adminID, fmt.Sprintf("🔔 Новый чат с пользователем %d", chatID)))

	case "end_chat":
		endChatSession(bot, chatID, adminID)

	case "open_menu":
		sendMainMenu(bot, chatID)
	}
}

func sendMainMenu(bot *tgbotapi.BotAPI, chatID int64) {
	mutex.Lock()
	isSubscribed := subscribers[chatID]
	_, isActiveChat := activeChats[chatID]
	mutex.Unlock()

	// Основные кнопки меню
	var rows [][]tgbotapi.InlineKeyboardButton

	// Кнопки подписки/отписки
	if isSubscribed {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🚫 Отписаться от новостей", "unsubscribe_news"),
		))
	} else {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📰 Подписаться на новости", "subscribe_news"),
		))
	}

	// Кнопка чата с менеджером
	if isActiveChat {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔚 Завершить чат", "end_chat"),
		))
	} else {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("👨‍💼 Чат с менеджером", "contact_manager"),
		))
	}

	// Кнопка обновления меню
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔄 Обновить меню", "open_menu"),
	))

	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)

	msg := tgbotapi.NewMessage(chatID, "📋 *Главное меню*")
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard
	bot.Send(msg)
}

func sendStockToSubscribers(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
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

	bot.Send(tgbotapi.NewMessage(chatID, "🗣 Чат с менеджером завершен"))
	bot.Send(tgbotapi.NewMessage(adminID, fmt.Sprintf("🔕 Пользователь %d завершил чат", chatID)))
	sendMainMenu(bot, chatID)
}