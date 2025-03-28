package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
)

// Структуры для хранения данных
var (
	activeChats = struct {
		sync.Mutex
		users map[int64]bool
	}{users: make(map[int64]bool)}

	userToAdmin = struct {
		sync.Mutex
		chats map[int64]int64
	}{chats: make(map[int64]int64)}

	subscribers = struct {
		sync.Mutex
		users map[int64]bool
	}{users: make(map[int64]bool)}
)

func main() {
	// Загрузка конфигурации
	err := godotenv.Load()
	if err != nil {
		log.Print("Файл .env не найден, используются переменные окружения")
	}

	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	adminIDStr := os.Getenv("ADMIN_ID")

	if botToken == "" || adminIDStr == "" {
		log.Fatal("Требуются TELEGRAM_BOT_TOKEN и ADMIN_ID")
	}

	adminID, err := strconv.ParseInt(adminIDStr, 10, 64)
	if err != nil {
		log.Fatal("Неверный формат ADMIN_ID")
	}

	// Инициализация бота
	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Fatal(err)
	}
	bot.Debug = true

	log.Printf("Бот @%s запущен", bot.Self.UserName)

	// Настройка обновлений
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	// Обработка входящих сообщений
	for update := range updates {
		if update.Message != nil {
			handleMessage(bot, update.Message, adminID)
		} else if update.CallbackQuery != nil {
			handleCallback(bot, update.CallbackQuery, adminID)
		}
	}
}

func handleMessage(bot *tgbotapi.BotAPI, message *tgbotapi.Message, adminID int64) {
	chatID := message.Chat.ID

	// Обработка сообщений от администратора
	if chatID == adminID {
		// Рассылка контента подписчикам
		if message.Photo != nil || message.Document != nil {
			sendToSubscribers(bot, message, adminID)
			return
		}
		handleAdminMessage(bot, message)
		return
	}

	// Проверка активного чата с менеджером
	activeChats.Lock()
	isChatActive := activeChats.users[chatID]
	activeChats.Unlock()

	// Обработка команды /stop
	if message.Text == "/stop" && isChatActive {
		endChatWithManager(bot, chatID, adminID)
		return
	}

	// Если активен чат с менеджером
	if isChatActive {
		if message.Text == "/start" {
			sendMainMenu(bot, chatID)
			return
		}

		// Пересылка сообщения администратору
		forwardedMsg := tgbotapi.NewForward(adminID, chatID, message.MessageID)
		if _, err := bot.Send(forwardedMsg); err != nil {
			log.Printf("Ошибка пересылки: %v", err)
		}
		
		userToAdmin.Lock()
		userToAdmin.chats[chatID] = adminID
		userToAdmin.Unlock()
		return
	}

	// Обработка команд пользователя
	switch message.Text {
	case "/start":
		sendMainMenu(bot, chatID)
	default:
		msg := tgbotapi.NewMessage(chatID, "Используйте кнопки меню (/start)")
		if _, err := bot.Send(msg); err != nil {
			log.Printf("Ошибка отправки: %v", err)
		}
		sendMainMenu(bot, chatID)
	}
}

// Рассылка контента подписчикам
func sendToSubscribers(bot *tgbotapi.BotAPI, message *tgbotapi.Message, adminID int64) {
	subscribers.Lock()
	defer subscribers.Unlock()

	if len(subscribers.users) == 0 {
		msg := tgbotapi.NewMessage(adminID, "Нет подписчиков для рассылки")
		if _, err := bot.Send(msg); err != nil {
			log.Printf("Ошибка отправки: %v", err)
		}
		return
	}

	caption := "Добрый день! У нас новое поступление. Ознакомьтесь с обновлением!"
	if message.Caption != "" {
		caption = message.Caption
	}

	successCount := 0
	failCount := 0

	for userID := range subscribers.users {
		var err error
		
		if len(message.Photo) > 0 {
			// Отправка фото
			photo := message.Photo[len(message.Photo)-1]
			photoMsg := tgbotapi.NewPhoto(userID, tgbotapi.FileID(photo.FileID))
			photoMsg.Caption = caption
			_, err = bot.Send(photoMsg)
		} else if message.Document != nil {
			// Отправка документа
			docMsg := tgbotapi.NewDocument(userID, tgbotapi.FileID(message.Document.FileID))
			docMsg.Caption = caption
			_, err = bot.Send(docMsg)
		}

		if err != nil {
			log.Printf("Ошибка отправки для %d: %v", userID, err)
			failCount++
			// Удаляем заблокировавших бота пользователей
			if err.Error() == "Forbidden: bot was blocked by the user" {
				delete(subscribers.users, userID)
			}
		} else {
			successCount++
		}
	}

	// Отчет администратору
	report := fmt.Sprintf("Рассылка завершена:\nУспешно: %d\nНе удалось: %d", successCount, failCount)
	if failCount > 0 {
		report += "\n\nПримечание: Заблокировавшие бота пользователи удалены из подписчиков"
	}
	if _, err := bot.Send(tgbotapi.NewMessage(adminID, report)); err != nil {
		log.Printf("Ошибка отправки отчета: %v", err)
	}
}

// Завершение диалога с менеджером
func endChatWithManager(bot *tgbotapi.BotAPI, userID, adminID int64) {
	activeChats.Lock()
	delete(activeChats.users, userID)
	activeChats.Unlock()

	userToAdmin.Lock()
	delete(userToAdmin.chats, userID)
	userToAdmin.Unlock()

	msg := tgbotapi.NewMessage(userID, "🗣 Диалог с менеджером завершен. /start - открыть меню")
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Ошибка отправки: %v", err)
	}

	adminMsg := tgbotapi.NewMessage(adminID, fmt.Sprintf("⚠ Диалог с пользователем %d завершен", userID))
	if _, err := bot.Send(adminMsg); err != nil {
		log.Printf("Ошибка отправки: %v", err)
	}

	sendMainMenu(bot, userID)
}

// Обработка сообщений от администратора
func handleAdminMessage(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	// Ответ на пересланное сообщение
	if message.ReplyToMessage != nil && message.ReplyToMessage.ForwardFrom != nil {
		userID := message.ReplyToMessage.ForwardFrom.ID
		msg := tgbotapi.NewMessage(userID, message.Text)
		if _, err := bot.Send(msg); err != nil {
			log.Printf("Ошибка отправки: %v", err)
		}
		return
	}

	// Прямой ответ пользователю
	userToAdmin.Lock()
	defer userToAdmin.Unlock()
	
	for u, a := range userToAdmin.chats {
		if a == message.Chat.ID {
			msg := tgbotapi.NewMessage(u, message.Text)
			if _, err := bot.Send(msg); err != nil {
				log.Printf("Ошибка отправки: %v", err)
			}
			break
		}
	}
}

// Обработка нажатий кнопок
func handleCallback(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery, adminID int64) {
	chatID := query.Message.Chat.ID
	data := query.Data

	// Ответ на callback (убираем "часики")
	if _, err := bot.Send(tgbotapi.NewCallback(query.ID, "")); err != nil {
		log.Printf("Ошибка callback: %v", err)
	}

	switch data {
	case "subscribe":
		subscribers.Lock()
		subscribers.users[chatID] = true
		subscribers.Unlock()
		
		msg := tgbotapi.NewMessage(chatID, "✅ Вы успешно подписались на обновления!")
		if _, err := bot.Send(msg); err != nil {
			log.Printf("Ошибка отправки: %v", err)
		}
		sendMainMenu(bot, chatID)

	case "unsubscribe":
		subscribers.Lock()
		delete(subscribers.users, chatID)
		subscribers.Unlock()
		
		msg := tgbotapi.NewMessage(chatID, "❌ Вы отписались от обновлений")
		if _, err := bot.Send(msg); err != nil {
			log.Printf("Ошибка отправки: %v", err)
		}
		sendMainMenu(bot, chatID)

	case "contact_manager":
		activeChats.Lock()
		activeChats.users[chatID] = true
		activeChats.Unlock()

		userToAdmin.Lock()
		userToAdmin.chats[chatID] = adminID
		userToAdmin.Unlock()

		msg := tgbotapi.NewMessage(chatID, "📩 Теперь вы можете писать менеджеру. /stop - завершить диалог")
		buttons := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔴 Завершить диалог", "end_chat"),
			),
		)
		msg.ReplyMarkup = buttons
		if _, err := bot.Send(msg); err != nil {
			log.Printf("Ошибка отправки: %v", err)
		}

		adminMsg := tgbotapi.NewMessage(adminID, fmt.Sprintf("❗ Чат с пользователем %d", chatID))
		if _, err := bot.Send(adminMsg); err != nil {
			log.Printf("Ошибка отправки: %v", err)
		}

	case "end_chat":
		endChatWithManager(bot, chatID, adminID)

	case "close":
		msg := tgbotapi.NewMessage(chatID, "🔒 Меню закрыто. /start - открыть снова")
		if _, err := bot.Send(msg); err != nil {
			log.Printf("Ошибка отправки: %v", err)
		}
	}
}

// Отправка главного меню
func sendMainMenu(bot *tgbotapi.BotAPI, chatID int64) {
	// Проверка статуса подписки
	subscribers.Lock()
	isSubscribed := subscribers.users[chatID]
	subscribers.Unlock()

	// Текст кнопки подписки
	var subscribeBtnText, subscribeBtnData string
	if isSubscribed {
		subscribeBtnText = "❌ Отписаться"
		subscribeBtnData = "unsubscribe"
	} else {
		subscribeBtnText = "📩 Подписаться"
		subscribeBtnData = "subscribe"
	}

	// Создание клавиатуры
	buttons := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(subscribeBtnText, subscribeBtnData),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💬 Написать менеджеру", "contact_manager"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Закрыть", "close"),
		),
	)

	// Отправка меню
	msg := tgbotapi.NewMessage(chatID, "Выберите действие:")
	msg.ReplyMarkup = buttons
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Ошибка отправки меню: %v", err)
	}
}