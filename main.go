package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

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
		users map[int64]SubscriberInfo
	}{users: make(map[int64]SubscriberInfo)}

	mailingStatus = struct {
		sync.Mutex
		active bool
	}{active: true}

	mediaGroups = struct {
		sync.Mutex
		groups map[string][]tgbotapi.Message
	}{groups: make(map[string][]tgbotapi.Message)}
)

type SubscriberInfo struct {
	Username string
	FullName string
}

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

	// Очистка старых медиагрупп каждые 10 минут
	go func() {
		for {
			time.Sleep(10 * time.Minute)
			cleanOldMediaGroups()
		}
	}()

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

func cleanOldMediaGroups() {
	mediaGroups.Lock()
	defer mediaGroups.Unlock()

	for groupID, messages := range mediaGroups.groups {
		if len(messages) > 0 {
			// Удаляем группы старше 1 часа
			if time.Since(messages[0].Time()).Hours() > 1 {
				delete(mediaGroups.groups, groupID)
			}
		}
	}
}

func handleMessage(bot *tgbotapi.BotAPI, message *tgbotapi.Message, adminID int64) {
	chatID := message.Chat.ID

	// Обработка сообщений от администратора
	if chatID == adminID {
		// Команда для просмотра подписчиков
		if message.Text == "/subscribers" {
			sendSubscribersList(bot, adminID)
			return
		}
		// Команда для управления рассылкой
		if message.Text == "/toggle_mailing" {
			toggleMailingStatus(bot, adminID)
			return
		}
		// Рассылка контента подписчикам
		if (len(message.Photo) > 0 || message.Document != nil || message.MediaGroupID != "") && isMailingActive() {
			handleAdminMedia(bot, message, adminID)
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

func handleAdminMedia(bot *tgbotapi.BotAPI, message *tgbotapi.Message, adminID int64) {
	// Если это медиагруппа
	if message.MediaGroupID != "" {
		mediaGroups.Lock()
		defer mediaGroups.Unlock()

		// Добавляем сообщение в группу
		mediaGroups.groups[message.MediaGroupID] = append(mediaGroups.groups[message.MediaGroupID], *message)

		// Проверяем, собраны ли все сообщения группы (эвристика - ждем 1 секунду)
		time.AfterFunc(1*time.Second, func() {
			mediaGroups.Lock()
			defer mediaGroups.Unlock()

			if messages, ok := mediaGroups.groups[message.MediaGroupID]; ok {
				// Проверяем, что это последнее сообщение в группе
				if len(messages) > 1 && isLastMediaGroupMessage(messages) {
					sendMediaGroupToSubscribers(bot, messages, adminID)
					delete(mediaGroups.groups, message.MediaGroupID)
				}
			}
		})
	} else {
		// Одиночное медиа
		sendToSubscribers(bot, message, adminID)
	}
}

func isLastMediaGroupMessage(messages []tgbotapi.Message) bool {
	// Простая проверка - если прошло больше 0.5 секунд с последнего сообщения
	if len(messages) == 0 {
		return false
	}
	lastTime := messages[len(messages)-1].Time()
	return time.Since(lastTime).Seconds() > 0.5
}

func sendMediaGroupToSubscribers(bot *tgbotapi.BotAPI, messages []tgbotapi.Message, adminID int64) {
	subscribers.Lock()
	defer subscribers.Unlock()

	if len(subscribers.users) == 0 {
		msg := tgbotapi.NewMessage(adminID, "Нет подписчиков для рассылки")
		bot.Send(msg)
		return
	}

	// Получаем подпись из первого сообщения с медиа
	caption := ""
	for _, msg := range messages {
		if msg.Caption != "" {
			caption = msg.Caption
			break
		}
	}
	if caption == "" {
		caption = "Добрый день! У нас новое поступление. Ознакомьтесь с обновлением!"
	}

	successCount := 0
	failCount := 0

	// Создаем медиагруппу для каждого подписчика
	for userID := range subscribers.users {
		mediaGroup := make([]interface{}, 0, len(messages))
		
		for i, msg := range messages {
			if len(msg.Photo) > 0 {
				photo := msg.Photo[len(msg.Photo)-1]
				inputMedia := tgbotapi.NewInputMediaPhoto(tgbotapi.FileID(photo.FileID))
				if i == 0 {
					inputMedia.Caption = caption
				}
				mediaGroup = append(mediaGroup, inputMedia)
			} else if msg.Document != nil {
				inputMedia := tgbotapi.NewInputMediaDocument(tgbotapi.FileID(msg.Document.FileID))
				if i == 0 {
					inputMedia.Caption = caption
				}
				mediaGroup = append(mediaGroup, inputMedia)
			}
		}

		if len(mediaGroup) > 0 {
			album := tgbotapi.NewMediaGroup(userID, mediaGroup)
			if _, err := bot.Send(album); err != nil {
				log.Printf("Ошибка отправки медиагруппы для %d: %v", userID, err)
				failCount++
				
				// Удаляем заблокировавших бота пользователей
				if err.Error() == "Forbidden: bot was blocked by the user" {
					delete(subscribers.users, userID)
				}
			} else {
				successCount++
			}
		}
	}

	// Отчет администратору
	report := fmt.Sprintf("Рассылка медиагруппы завершена:\nСообщений в группе: %d\nУспешно: %d\nНе удалось: %d", 
		len(messages), successCount, failCount)
	if failCount > 0 {
		report += "\n\nПримечание: Заблокировавшие бота пользователи удалены из подписчиков"
	}
	if _, err := bot.Send(tgbotapi.NewMessage(adminID, report)); err != nil {
		log.Printf("Ошибка отправки отчета: %v", err)
	}
}

func sendSubscribersList(bot *tgbotapi.BotAPI, adminID int64) {
	subscribers.Lock()
	defer subscribers.Unlock()

	if len(subscribers.users) == 0 {
		msg := tgbotapi.NewMessage(adminID, "Нет подписчиков")
		bot.Send(msg)
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📊 Всего подписчиков: %d\n\n", len(subscribers.users)))

	for id, info := range subscribers.users {
		sb.WriteString(fmt.Sprintf("🆔 ID: %d\n👤 Имя: %s\n📛 Юзернейм: @%s\n\n", 
			id, info.FullName, info.Username))
	}

	msg := tgbotapi.NewMessage(adminID, sb.String())
	msg.ParseMode = "HTML"
	bot.Send(msg)
}

func notifyNewSubscriber(bot *tgbotapi.BotAPI, adminID int64, userID int64, username string, fullName string) {
	msgText := fmt.Sprintf("🎉 Новый подписчик!\n\nID: %d\nИмя: %s\nЮзернейм: @%s", 
		userID, fullName, username)
	
	msg := tgbotapi.NewMessage(adminID, msgText)
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Ошибка отправки уведомления о новом подписчике: %v", err)
	}
}

func toggleMailingStatus(bot *tgbotapi.BotAPI, adminID int64) {
	mailingStatus.Lock()
	mailingStatus.active = !mailingStatus.active
	status := mailingStatus.active
	mailingStatus.Unlock()

	var text string
	if status {
		text = "✅ Рассылка активирована"
	} else {
		text = "❌ Рассылка приостановлена"
	}

	msg := tgbotapi.NewMessage(adminID, text)
	bot.Send(msg)
}

func isMailingActive() bool {
	mailingStatus.Lock()
	defer mailingStatus.Unlock()
	return mailingStatus.active
}

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
		// Проверяем, новый ли это подписчик
		isNewSubscriber := false
		if _, exists := subscribers.users[chatID]; !exists {
			isNewSubscriber = true
		}
		
		subscribers.users[chatID] = SubscriberInfo{
			Username: query.From.UserName,
			FullName: query.From.FirstName + " " + query.From.LastName,
		}
		subscribers.Unlock()
		
		msg := tgbotapi.NewMessage(chatID, "✅ Вы успешно подписались на рассылку!")
		if _, err := bot.Send(msg); err != nil {
			log.Printf("Ошибка отправки: %v", err)
		}
		
		// Уведомляем админа о новом подписчике
		if isNewSubscriber {
			notifyNewSubscriber(bot, adminID, chatID, query.From.UserName, 
				query.From.FirstName+" "+query.From.LastName)
		}
		
		sendMainMenu(bot, chatID)

	case "unsubscribe":
		subscribers.Lock()
		delete(subscribers.users, chatID)
		subscribers.Unlock()
		
		msg := tgbotapi.NewMessage(chatID, "❌ Вы отписались от рассылки")
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

		adminMsg := tgbotapi.NewMessage(adminID, fmt.Sprintf("❗ Чат с пользователем %d (@%s - %s)", 
			chatID, query.From.UserName, query.From.FirstName+" "+query.From.LastName))
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

func sendMainMenu(bot *tgbotapi.BotAPI, chatID int64) {
	// Проверка статуса подписки
	subscribers.Lock()
	_, isSubscribed := subscribers.users[chatID]
	subscribers.Unlock()

	// Текст кнопки подписки
	var subscribeBtnText, subscribeBtnData string
	if isSubscribed {
		subscribeBtnText = "❌ Отписаться от рассылки"
		subscribeBtnData = "unsubscribe"
	} else {
		subscribeBtnText = "📩 Подписаться на рассылку"
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