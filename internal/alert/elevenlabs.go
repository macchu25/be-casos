package alert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// CallRelative sử dụng ElevenLabs TTS (ưu tiên) hoặc Stringee TTS (dự phòng)
func CallRelative(phoneNumber, patientName, incidentType string) error {
	apiKey := strings.TrimSpace(os.Getenv("ELEVENLABS_API_KEY"))
	voiceID := strings.TrimSpace(os.Getenv("ELEVENLABS_VOICE_ID"))
	
	text := fmt.Sprintf("Chào bạn, đây là thông báo khẩn cấp từ hệ thống Cardiac Alert. Người thân của bạn là %s %s. Vui lòng kiểm tra ngay lập tức.", patientName, incidentType)

	if apiKey != "" {
		if voiceID == "" {
			voiceID = "21m00Tcm4TlvDq8ikWAM"
		}
		
		audioURL := fmt.Sprintf("https://api.elevenlabs.io/v1/text-to-speech/%s", voiceID)
		payload := map[string]interface{}{
			"text":     text,
			"model_id": "eleven_multilingual_v2",
		}
		jsonData, _ := json.Marshal(payload)

		req, _ := http.NewRequest("POST", audioURL, bytes.NewBuffer(jsonData))
		req.Header.Set("xi-api-key", apiKey)
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Do(req)
		
		if err == nil && resp.StatusCode == 200 {
			defer resp.Body.Close()
			fileName := fmt.Sprintf("alert_%d.mp3", time.Now().Unix())
			filePath := filepath.Join("audio", fileName)
			os.MkdirAll("audio", 0755)
			out, _ := os.Create(filePath)
			io.Copy(out, resp.Body)
			out.Close()
			
			log.Println("🎨 [ElevenLabs] Tạo giọng nói thành công, đang thực hiện cuộc gọi...")
			return triggerStringeeCall(phoneNumber, fileName, "")
		}
	}

	return triggerStringeeCall(phoneNumber, "", text)
}

func triggerStringeeCall(toPhoneNumber, audioFileName, fallbackText string) error {
	keySid := strings.TrimSpace(os.Getenv("STRINGEE_KEY_SID"))
	keySecret := strings.TrimSpace(os.Getenv("STRINGEE_KEY_SECRET"))
	baseURL := strings.TrimSpace(os.Getenv("BASE_URL"))

	if keySid == "" || keySecret == "" {
		return fmt.Errorf("missing stringee config")
	}

	token, err := generateStringeeJWT(keySid, keySecret)
	if err != nil {
		return err
	}

	// Sử dụng Helper URL của Stringee để đọc văn bản (Bypass ngrok warning)
	var answerURL string
	if audioFileName != "" {
		answerURL = fmt.Sprintf("%s/audio/%s", strings.TrimSpace(baseURL), audioFileName)
	} else {
		// Dùng link công cộng của Stringee để đọc văn bản, tránh phụ thuộc vào ngrok
		answerURL = "https://developer.stringee.com/scco_helper/talk.php?text=" + url.QueryEscape(fallbackText)
	}

	targetNumber := toPhoneNumber
	if strings.HasPrefix(targetNumber, "0") {
		targetNumber = "84" + targetNumber[1:]
	}

	callPayload := map[string]interface{}{
		"from": map[string]interface{}{
			"type":   "external",
			"number": "842471078368", 
		},
		"to": []map[string]interface{}{
			{
				"type":   "external",
				"number": targetNumber,
			},
		},
		"answer_url": answerURL,
	}
	
	bodyData, _ := json.Marshal(callPayload)
	log.Printf("📤 [Stringee] Debug Payload: %s\n", string(bodyData))

	// Dùng URL chuẩn KHÔNG có dấu gạch chéo cuối
	req, _ := http.NewRequest("POST", "https://api.stringee.com/v1/call", bytes.NewBuffer(bodyData))
	req.Header.Set("X-STRINGEE-AUTH", token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("stringee api error: %s", string(respBody))
	}

	log.Printf("📞 [Stringee] API Response: %s\n", string(respBody))
	log.Printf("✅ [Stringee] Đã ra lệnh gọi đến %s thành công!\n", targetNumber)
	return nil
}

func generateStringeeJWT(keySid, keySecret string) (string, error) {
	claims := jwt.MapClaims{
		"jti":       fmt.Sprintf("%s-%d", keySid, time.Now().Unix()),
		"iss":       keySid,
		"exp":       time.Now().Add(time.Hour).Unix(),
		"rest_api":  true,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token.Header["cty"] = "stringee-auth;v=1"
	return token.SignedString([]byte(keySecret))
}
