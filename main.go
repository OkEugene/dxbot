package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Структуры для хранения данных
var (
	subscribers = make(map[int64]bool) // Подписчики на рассылку
	activeChats = make(map[int64]bool) // Активные чаты с менеджером
	chatPairs   = make(map[int64]int64) // Связь пользователь-админ
	mutex       sync.Mutex              // Для потокобезопасности
)

func main() {
	// Получаем токен бота и ID администратора
	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	adminID, _ := strconv.ParseInt(os.Getenv("ADMIN_ID"), 10, 64)

	if botToken == "" || adminID == 0 {
		log.Fatal("Требуются TELEGRAM_BOT_TOKEN и ADMIN_ID")
	}

	// Инициализация бота
	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Fatal(err)
	}

	bot.Debug = true
	log.Printf("Бот запущен: @%s", bot.Self.UserName)

	// Настройка обработки сообщений
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

	// Обработка сообщений от администратора
	if chatID == adminID {
		// Рассылка подписчикам
		if msg.Photo != nil || msg.Document != nil {
			sendToSubscribers(bot, msg)
			return
		}
		// Ответ пользователю
		handleAdminReply(bot, msg)
		return
	}

	// Проверка активного чата
	mutex.Lock()
	isActive := activeChats[chatID]
	mutex.Unlock()

	// Если чат активен - пересылаем сообщение админу
	if isActive {
		forwardToAdmin(bot, chatID, msg.MessageID, adminID)
		return
	}

	// Обработка команд пользователя
	switch msg.Text {
	case "/start":
		sendMainMenu(bot, chatID)
	default:
		sendMainMenu(bot, chatID)
	}
}

func handleCallback(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery, adminID int64) {
	chatID := query.Message.Chat.ID
	data := query.Data

	// Убираем "часики" на кнопке
	bot.Send(tgbotapi.NewCallback(query.ID, ""))

	switch data {
	case "subscribe":
		mutex.Lock()
		subscribers[chatID] = true
		mutex.Unlock()
		
		// Получаем информацию о пользователе
		user := query.From
		userName := getUserName(user)
		
		// Уведомление пользователю
		msg := tgbotapi.NewMessage(chatID, "✅ Вы подписались на рассылку поступлений")
		bot.Send(msg)
		
		// Уведомление админу о новом подписчике
		adminMsg := tgbotapi.NewMessage(
			adminID,
			fmt.Sprintf("🎉 Новый подписчик:\nID: %d\nUsername: @%s\nИмя: %s",
				user.ID,
				getUserUsername(user),
				userName,
			),
		)
		bot.Send(adminMsg)
		
		sendMainMenu(bot, chatID)

	case "contact_manager":
		mutex.Lock()
		activeChats[chatID] = true
		chatPairs[chatID] = adminID
		mutex.Unlock()

		// Уведомление пользователю
		msg := tgbotapi.NewMessage(chatID, "📩 Теперь вы можете писать менеджеру. Отправьте ваше сообщение.")
		bot.Send(msg)
		
		// Получаем информацию о пользователе
		user := query.From
		userName := getUserName(user)
		
		// Уведомление админу
		adminMsg := tgbotapi.NewMessage(
			adminID, 
			fmt.Sprintf("❗ Новый чат:\nID: %d\nUsername: @%s\nИмя: %s",
				user.ID,
				getUserUsername(user),
				userName,
			),
		)
		bot.Send(adminMsg)

	case "close":
		msg := tgbotapi.NewMessage(chatID, "Меню закрыто. Для открытия напишите /start")
		bot.Send(msg)
	}
}

// Вспомогательные функции для получения информации о пользователе
func getUserName(user *tgbotapi.User) string {
	name := user.FirstName
	if user.LastName != "" {
		name += " " + user.LastName
	}
	if name == "" {
		name = "Не указано"
	}
	return name
}

func getUserUsername(user *tgbotapi.User) string {
	if user.UserName == "" {
		return "нет username"
	}
	return user.UserName
}

func sendMainMenu(bot *tgbotapi.BotAPI, chatID int64) {
	// Проверяем статус подписки
	mutex.Lock()
	isSubscribed := subscribers[chatID]
	mutex.Unlock()

	// Текст кнопки подписки
	subscribeBtn := tgbotapi.NewInlineKeyboardButtonData("📩 Подписаться на рассылку поступлений", "subscribe")
	if isSubscribed {
		subscribeBtn = tgbotapi.NewInlineKeyboardButtonData("✅ Вы подписаны на рассылку", "subscribe")
	}

	// Создаем меню с тремя кнопками
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			subscribeBtn,
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("👨‍💼 Написать менеджеру", "contact_manager"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Закрыть", "close"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, "🔷 *Главное меню* 🔷\nВыберите действие:")
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard
	bot.Send(msg)
}

func sendToSubscribers(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	mutex.Lock()
	defer mutex.Unlock()

	if len(subscribers) == 0 {
		bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Нет подписчиков для рассылки"))
		return
	}

	// Стандартный текст рассылки
	caption := "Добрый день! У нас новое поступление. Ознакомьтесь с обновлением!"
	if msg.Caption != "" {
		caption = msg.Caption
	}

	successCount := 0
	for userID := range subscribers {
		var err error
		
		if len(msg.Photo) > 0 {
			// Отправка фото
			photo := msg.Photo[len(msg.Photo)-1]
			photoMsg := tgbotapi.NewPhoto(userID, tgbotapi.FileID(photo.FileID))
			photoMsg.Caption = caption
			_, err = bot.Send(photoMsg)
		} else if msg.Document != nil {
			// Отправка документа
			docMsg := tgbotapi.NewDocument(userID, tgbotapi.FileID(msg.Document.FileID))
			docMsg.Caption = caption
			_, err = bot.Send(docMsg)
		}

		if err != nil {
			log.Printf("Ошибка отправки пользователю %d: %v", userID, err)
		} else {
			successCount++
		}
	}

	// Отчет администратору
	report := fmt.Sprintf("Рассылка отправлена %d подписчикам", successCount)
	bot.Send(tgbotapi.NewMessage(msg.Chat.ID, report))
}

func handleAdminReply(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	// Ответ на пересланное сообщение
	if msg.ReplyToMessage != nil && msg.ReplyToMessage.ForwardFrom != nil {
		userID := msg.ReplyToMessage.ForwardFrom.ID
		reply := tgbotapi.NewMessage(userID, "👨‍💼 Ответ менеджера:\n"+msg.Text)
		bot.Send(reply)
		return
	}

	// Прямой ответ через сохраненные сессии
	mutex.Lock()
	defer mutex.Unlock()
	
	for userID, adminID := range chatPairs {
		if adminID == msg.Chat.ID {
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