package telephony

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/twilio/twilio-go"
	openapi "github.com/twilio/twilio-go/rest/api/v2010"
)

type TwilioAccount struct {
	SID        string `json:"sid"`
	Token      string `json:"token"`
	From       string `json:"from"`
	VerifiedTo string `json:"verified_to"`
}

type TwilioGateway struct {
	accounts []TwilioAccount
}

func NewTwilioGateway() *TwilioGateway {
	jsonStr := os.Getenv("TWILIO_ACCOUNTS_JSON")
	var accounts []TwilioAccount
	
	if jsonStr != "" {
		err := json.Unmarshal([]byte(jsonStr), &accounts)
		if err != nil {
			log.Printf("[Twilio] ⚠️ Lỗi parse TWILIO_ACCOUNTS_JSON: %v", err)
		}
	}

	// Fallback đọc cấu hình cũ nếu không dùng JSON
	if len(accounts) == 0 {
		oldSid := os.Getenv("TWILIO_ACCOUNT_SID")
		if oldSid != "" {
			accounts = append(accounts, TwilioAccount{
				SID:   oldSid,
				Token: os.Getenv("TWILIO_AUTH_TOKEN"),
				From:  os.Getenv("TWILIO_PHONE_NUMBER"),
			})
		}
	}

	if len(accounts) == 0 {
		log.Printf("[Twilio] ⚠️ Thiếu cấu hình Twilio!")
	} else {
		log.Printf("[Twilio] ✅ Đã tải %d tài khoản Twilio.", len(accounts))
	}

	return &TwilioGateway{
		accounts: accounts,
	}
}

func (t *TwilioGateway) IsConfigured() bool {
	return len(t.accounts) > 0
}

func (t *TwilioGateway) InitiateOutboundCall(toPhone, contactName, camName, incidentType string) error {
	if !t.IsConfigured() {
		return fmt.Errorf("twilio chưa được cấu hình")
	}

	phone := normalizeVietnamPhone(toPhone)
	
	// Tìm tài khoản phù hợp với số điện thoại đích
	var selectedAcc *TwilioAccount
	for _, acc := range t.accounts {
		if normalizeVietnamPhone(acc.VerifiedTo) == phone {
			selectedAcc = &acc
			break
		}
	}

	if selectedAcc == nil {
		selectedAcc = &t.accounts[0]
		log.Printf("[Twilio] ⚠️ Không tìm thấy tài khoản cho %s, dùng mặc định tài khoản: %s", phone, selectedAcc.SID)
	} else {
		log.Printf("[Twilio] 🎯 Đã chọn đúng tài khoản Twilio cho số: %s", phone)
	}

	log.Printf("[Twilio] 📞 Gọi khẩn cấp đến %s (%s) | Camera: %s | Sự cố: %s\n", phone, contactName, camName, incidentType)

	client := twilio.NewRestClientWithParams(twilio.ClientParams{
		Username: selectedAcc.SID,
		Password: selectedAcc.Token,
	})

	now := time.Now().In(time.FixedZone("ICT", 7*3600)).Format("15 giờ 04 phút")
	message := fmt.Sprintf("Khẩn cấp, hệ thống Casốt thông báo. %s ơi, phát hiện có người bị ngã trong phạm vi quan sát của %s lúc %s. Hãy kiểm tra ngay lập tức. Đây là cuộc gọi tự động.", contactName, camName, now)
	
	twiml := fmt.Sprintf(`
		<Response>
			<Say language="vi-VN" voice="Google.vi-VN-Standard-A">%s</Say>
			<Pause length="2"/>
			<Say language="vi-VN" voice="Google.vi-VN-Standard-A">Tôi xin nhắc lại. %s</Say>
		</Response>
	`, message, message)

	params := &openapi.CreateCallParams{}
	params.SetTo(phone)
	params.SetFrom(selectedAcc.From)
	params.SetTwiml(twiml)

	resp, err := client.Api.CreateCall(params)
	if err != nil {
		return fmt.Errorf("lỗi khởi tạo cuộc gọi Twilio: %w", err)
	}

	if resp.Sid != nil {
		log.Printf("[Twilio] ✅ Cuộc gọi thành công! Call SID: %s\n", *resp.Sid)
	} else {
		log.Printf("[Twilio] ✅ Cuộc gọi đã được gửi!\n")
	}

	return nil
}
