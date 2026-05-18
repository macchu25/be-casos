package telephony

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
)

const elevenLabsBaseURL = "https://api.elevenlabs.io/v1"

type ElevenLabsGateway struct {
	apiKey      string
	agentID     string
	phoneNumID  string // ID số điện thoại đã đăng ký trong ElevenLabs
}

func NewElevenLabsGateway() *ElevenLabsGateway {
	return &ElevenLabsGateway{
		apiKey:     os.Getenv("ELEVENLABS_API_KEY"),
		agentID:    os.Getenv("ELEVENLABS_AGENT_ID"),
		phoneNumID: os.Getenv("ELEVENLABS_PHONE_NUMBER_ID"),
	}
}

// IsConfigured kiểm tra xem ElevenLabs có được cấu hình đầy đủ không
func (e *ElevenLabsGateway) IsConfigured() bool {
	return e.apiKey != "" && e.agentID != "" && e.phoneNumID != ""
}

type outboundCallRequest struct {
	AgentID            string                 `json:"agent_id"`
	AgentPhoneNumberID string                 `json:"agent_phone_number_id"`
	ToNumber           string                 `json:"to_number"`
	ConversationConfig map[string]interface{} `json:"conversation_initiation_client_data,omitempty"`
}

type outboundCallResponse struct {
	ConversationID string `json:"conversation_id"`
}

// InitiateOutboundCall gọi điện outbound qua ElevenLabs Conversational AI
// Người nhận sẽ nghe thông báo khẩn cấp ngay khi bắt máy (100% đảm bảo)
func (e *ElevenLabsGateway) InitiateOutboundCall(ctx context.Context, toPhone, contactName, incidentType string) error {
	if !e.IsConfigured() {
		return fmt.Errorf("ElevenLabs chưa được cấu hình (thiếu API key / Agent ID / Phone Number ID)")
	}

	// Chuẩn hóa số điện thoại VN sang E.164 (+84...)
	phone := normalizeVietnamPhone(toPhone)
	log.Printf("[ElevenLabs] 📞 Gọi outbound đến %s (%s) | Lý do: %s\n", phone, contactName, incidentType)

	// Dynamic variables để agent biết context
	payload := outboundCallRequest{
		AgentID:            e.agentID,
		AgentPhoneNumberID: e.phoneNumID,
		ToNumber:           phone,
		ConversationConfig: map[string]interface{}{
			"dynamic_variables": map[string]string{
				"contact_name":  contactName,
				"incident_type": incidentType,
			},
		},
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST",
		elevenLabsBaseURL+"/convai/conversations/outbound_call",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("tạo request thất bại: %w", err)
	}
	req.Header.Set("xi-api-key", e.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("gọi ElevenLabs API thất bại: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("ElevenLabs trả lỗi %d: %s", resp.StatusCode, string(respBody))
	}

	var result outboundCallResponse
	json.Unmarshal(respBody, &result)
	log.Printf("[ElevenLabs] ✅ Cuộc gọi đã được khởi tạo! Conversation ID: %s\n", result.ConversationID)
	return nil
}

// normalizeVietnamPhone chuyển 09xxxxxxxx → +8409xxxxxxxx
func normalizeVietnamPhone(phone string) string {
	if len(phone) == 0 {
		return phone
	}
	// Đã có +84 rồi
	if phone[:3] == "+84" {
		return phone
	}
	// Bắt đầu bằng 0 → thay bằng +84
	if phone[0] == '0' {
		return "+84" + phone[1:]
	}
	// Bắt đầu bằng 84 (không có +)
	if len(phone) >= 2 && phone[:2] == "84" {
		return "+" + phone
	}
	return "+" + phone
}
