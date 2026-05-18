package telephony

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type InlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineButton `json:"inline_keyboard"`
}

var httpClient = &http.Client{Timeout: 35 * time.Second}

// SendTelegramAlertCustom gửi tin nhắn văn bản với các nút bấm tùy chọn
func SendTelegramAlertCustom(chatID string, message string, buttons interface{}) error {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" || chatID == "" {
		return nil
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	payload := map[string]interface{}{
		"chat_id":                  chatID,
		"text":                     message,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	if buttons != nil {
		payload["reply_markup"] = buttons
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[Telegram] Lỗi marshal payload: %v\n", err)
		return err
	}

	resp, err := httpClient.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		log.Printf("[Telegram] Lỗi kết nối HTTP: %v\n", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("[Telegram] Telegram trả về Status %d cho Chat %s\n", resp.StatusCode, chatID)
		return fmt.Errorf("telegram status: %d", resp.StatusCode)
	}

	return nil
}

// SendTelegramPhotoCustom gửi ảnh bằng byte data
func SendTelegramPhotoCustom(chatID string, caption string, imgData []byte, buttons interface{}) error {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" || chatID == "" {
		return nil
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("photo", "alert.jpg")
	if err != nil {
		log.Printf("[Telegram] Lỗi tạo form file: %v\n", err)
		return err
	}
	if _, err := io.Copy(part, bytes.NewReader(imgData)); err != nil {
		log.Printf("[Telegram] Lỗi copy ảnh vào form: %v\n", err)
		return err
	}

	_ = writer.WriteField("chat_id", chatID)
	_ = writer.WriteField("caption", caption)
	_ = writer.WriteField("parse_mode", "HTML")

	if buttons != nil {
		btnData, err := json.Marshal(buttons)
		if err != nil {
			log.Printf("[Telegram] Lỗi marshal buttons: %v\n", err)
		} else {
			_ = writer.WriteField("reply_markup", string(btnData))
		}
	}
	writer.Close()

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendPhoto", token)
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("[Telegram] Lỗi gửi ảnh: %v\n", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("[Telegram] Lỗi gửi ảnh, status: %d\n", resp.StatusCode)
		return fmt.Errorf("telegram status: %d", resp.StatusCode)
	}
	return nil
}

// StartBotListener lắng nghe các sự kiện callback từ Telegram
func StartBotListener(onAction func(senderChatID string, action, data string)) {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		return
	}

	log.Println("🛡️ [Telegram] Bot Listener đang hoạt động...")
	offset := 0

	for {
		url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=30", token, offset)
		resp, err := httpClient.Get(url)
		if err != nil {
			log.Printf("[Telegram] Lỗi GetUpdates: %v\n", err)
			time.Sleep(5 * time.Second)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			log.Printf("[Telegram] GetUpdates trả về status %d\n", resp.StatusCode)
			resp.Body.Close()
			time.Sleep(10 * time.Second)
			continue
		}

		var result struct {
			Ok     bool `json:"ok"`
			Result []struct {
				UpdateID      int `json:"update_id"`
				CallbackQuery struct {
					ID   string `json:"id"`
					Data string `json:"data"`
					From struct {
						ID int64 `json:"id"`
					} `json:"from"`
				} `json:"callback_query"`
			} `json:"result"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			log.Printf("[Telegram] Lỗi decode update: %v\n", err)
			resp.Body.Close()
			time.Sleep(5 * time.Second)
			continue
		}
		resp.Body.Close()

		if !result.Ok {
			log.Printf("[Telegram] GetUpdates trả về lỗi Ok=false\n")
			time.Sleep(5 * time.Second)
			continue
		}

		for _, update := range result.Result {
			if update.CallbackQuery.Data != "" {
				senderID := strconv.FormatInt(update.CallbackQuery.From.ID, 10)
				parts := strings.SplitN(update.CallbackQuery.Data, ":", 2)
				if len(parts) == 2 {
					onAction(senderID, parts[0], parts[1])

					ackURL := fmt.Sprintf("https://api.telegram.org/bot%s/answerCallbackQuery?callback_query_id=%s", token, update.CallbackQuery.ID)
					if ackResp, err := httpClient.Get(ackURL); err == nil {
						ackResp.Body.Close()
					}
				}
			}
			offset = update.UpdateID + 1
		}
	}
}
