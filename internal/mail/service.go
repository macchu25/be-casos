package mail

import (
	"fmt"
	"net/smtp"
	"os"
	"time"

	"go-backend/internal/logger"
)

type Service struct {
	host     string
	port     string
	email    string
	password string
}

func NewService() *Service {
	host := os.Getenv("SMTP_HOST")
	if host == "" {
		host = "smtp.gmail.com" // Default to Gmail
	}
	port := os.Getenv("SMTP_PORT")
	if port == "" {
		port = "587"
	}
	return &Service{
		host:     host,
		port:     port,
		email:    os.Getenv("SMTP_EMAIL"),
		password: os.Getenv("SMTP_PASSWORD"),
	}
}

func (s *Service) SendSubscriptionEmail(toEmail, userName, planName string, paidAt, expiresAt time.Time, bankRef string) error {
	if s.email == "" || s.password == "" {
		logger.Log.Warn("Lưu ý: Không thể gửi email do thiếu SMTP_EMAIL hoặc SMTP_PASSWORD trong .env")
		return nil
	}

	subject := "Chúc mừng! Bạn đã đăng ký gói cước thành công tại Casos AI"
	refLine := ""
	if bankRef != "" {
		refLine = fmt.Sprintf("- Mã tham chiếu NG: %s\n", bankRef)
	}
	layout := "02/01/2006 15:04 (GMT+7)"
	paidTxt := paidAt.In(time.FixedZone("ICT", 7*3600)).Format(layout)
	expTxt := expiresAt.In(time.FixedZone("ICT", 7*3600)).Format(layout)

	body := fmt.Sprintf(`Chào %s,

Chúc mừng bạn đã kích hoạt gói %s.

Thông tin gói:
- Gói: %s
- Trạng thái: Đang hoạt động
- Thanh toán/nâng cấp: %s
- Hết hạn gói: %s
%s
Các tính năng nâng cao đã mở cho tài khoản của bạn. Bạn cũng sẽ thấy thông báo trong trung tâm cảnh báo của ứng dụng.

Cảm ơn bạn đã đồng hành cùng Casos AI Studio.

Trân trọng,
Đội ngũ Casos AI Studio`, userName, planName, planName, paidTxt, expTxt, refLine)

	msg := []byte(fmt.Sprintf("To: %s\r\n"+
		"Subject: %s\r\n"+
		"\r\n"+
		"%s\r\n", toEmail, subject, body))

	auth := smtp.PlainAuth("", s.email, s.password, s.host)

	addr := fmt.Sprintf("%s:%s", s.host, s.port)
	err := smtp.SendMail(addr, auth, s.email, []string{toEmail}, msg)

	if err != nil {
		logger.Log.Error("Lỗi gửi email: ", err)
		return err
	}

	logger.Log.Info("📧 [EMAIL SENT SUCCESSFULLY] To: " + toEmail)
	return nil
}

func (s *Service) SendOTPCancelEmail(toEmail, userName, otp string) error {
	if s.email == "" || s.password == "" {
		logger.Log.Warn("Lưu ý: Không thể gửi email OTP do thiếu SMTP_EMAIL hoặc SMTP_PASSWORD")
		return nil
	}

	subject := "[Casos AI] Mã xác nhận hủy gói cước"
	body := fmt.Sprintf(`Chào %s,

Bạn vừa yêu cầu hủy gói cước tại Casos AI. Để hoàn tất, vui lòng sử dụng mã xác nhận dưới đây:

Mã xác nhận của bạn là: %s

Mã này sẽ hết hạn trong vòng 10 phút. Nếu không phải bạn thực hiện yêu cầu này, vui lòng bỏ qua email hoặc liên hệ bộ phận hỗ trợ.

Trân trọng,
Đội ngũ Casos AI Studio`, userName, otp)

	msg := []byte(fmt.Sprintf("To: %s\r\n"+
		"Subject: %s\r\n"+
		"\r\n"+
		"%s\r\n", toEmail, subject, body))

	auth := smtp.PlainAuth("", s.email, s.password, s.host)
	addr := fmt.Sprintf("%s:%s", s.host, s.port)
	err := smtp.SendMail(addr, auth, s.email, []string{toEmail}, msg)

	if err != nil {
		logger.Log.Error("Lỗi gửi email OTP: ", err)
		return err
	}

	logger.Log.Info("📧 [OTP EMAIL SENT] To: " + toEmail)
	return nil
}
