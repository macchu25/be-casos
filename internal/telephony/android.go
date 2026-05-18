package telephony

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type CallType string

const (
	CallRelative CallType = "người thân"
	CallDoctor   CallType = "bác sĩ"
)

const adbExe = "C:\\adb\\adb.exe"

type Gateway struct {
	db     *mongo.Database
	twilio *TwilioGateway
}

func NewGateway(db *mongo.Database) *Gateway {
	return &Gateway{
		db:     db,
		twilio: NewTwilioGateway(),
	}
}

// getLatestAudioFile trả về file mp3 mới nhất trong thư mục audio/
func getLatestAudioFile() string {
	wd, _ := os.Getwd()
	// Nếu đang chạy trong go-backend, tìm folder audio ở thư mục cha
	audioDir := filepath.Join(wd, "audio")
	if _, err := os.Stat(audioDir); os.IsNotExist(err) {
		audioDir = filepath.Join(wd, "..", "audio")
	}
	pattern := filepath.Join(audioDir, "alert_*.mp3")
	log.Printf("[Android] 🔍 Tìm file audio theo pattern: %s\n", pattern)

	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		log.Printf("[Android] ❌ Không tìm thấy file nào! err=%v, matches=%v\n", err, matches)
		return ""
	}
	sort.Strings(matches)
	chosen := matches[len(matches)-1]
	log.Printf("[Android] ✅ Dùng file audio: %s\n", chosen)
	return chosen
}

func isDeviceConnected() bool {
	out, err := exec.Command(adbExe, "devices").Output()
	if err != nil {
		return false
	}
	lines := strings.Split(string(out), "\n")
	for _, line := range lines[1:] {
		if strings.Contains(line, "\tdevice") {
			return true
		}
	}
	return false
}

func (g *Gateway) InitiateAndroidCall(userID primitive.ObjectID, camID primitive.ObjectID, reason string, callType CallType, camName string, specificPhone ...string) {
	specificP := ""
	if len(specificPhone) > 0 {
		specificP = specificPhone[0]
	}
	phone, contactName := g.getEmergencyContact(userID, specificP)

	if phone == "" {
		log.Printf("[Telephony] BỎ QUA: Không tìm thấy số điện thoại cho user %s\n", userID.Hex())
		return
	}

	// 1. Ưu tiên dùng Twilio nếu đã cấu hình
	if g.twilio != nil && g.twilio.IsConfigured() {
		log.Printf("[Telephony] Sử dụng Twilio để gọi báo động...\n")
		err := g.twilio.InitiateOutboundCall(phone, contactName, camName, reason)
		if err == nil {
			return // Thành công thì dừng ở đây
		}
		log.Printf("[Telephony] ⚠️ Lỗi Twilio: %v. Fallback sang gọi bằng điện thoại Android qua ADB...\n", err)
	}

	// 2. Fallback sang ADB
	if !isDeviceConnected() {
		log.Printf("[Android] BỎ QUA: Không có thiết bị Android nào kết nối qua ADB\n")
		return
	}

	log.Printf("[Android] ĐANG GỌI 📞 %s (%s) từ Camera %s. Lý do: %s\n", phone, string(callType), camID.Hex(), reason)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Thực hiện cuộc gọi
	callCmd := fmt.Sprintf("am start -a android.intent.action.CALL -d tel:%s", phone)
	if err := exec.CommandContext(ctx, adbExe, "shell", callCmd).Run(); err != nil {
		log.Printf("[Android] Lỗi kích hoạt cuộc gọi: %v\n", err)
	}

	// 2. Phát âm thanh khi người kia BẮT MÁY
	go func() {
		log.Printf("[Android] ⏳ Đang chờ người dùng bắt máy (timeout 60s)...\n")

		// Poll trạng thái cuộc gọi mỗi giây
		// mForegroundCallState=4 = ACTIVE (người kia đã bắt máy)
		// mForegroundCallState=3 = ALERTING (đang reo bên kia)
		// mForegroundCallState=2 = DIALING
		deadline := time.Now().Add(60 * time.Second)
		answered := false
		for time.Now().Before(deadline) {
			time.Sleep(1 * time.Second)
			out, err := exec.Command(adbExe, "shell", "dumpsys", "telephony.registry").Output()
			if err != nil {
				continue
			}
			if strings.Contains(string(out), "mForegroundCallState=4") {
				answered = true
				log.Printf("[Android] ✅ Người dùng đã bắt máy (ACTIVE)! Bắt đầu phát âm thanh...\n")
				break
			}
		}

		if !answered {
			log.Printf("[Android] ⏱️ Hết thời gian chờ (60s), không ai bắt máy.\n")
			return
		}

		// Đợi 1 giây để cuộc gọi ổn định
		time.Sleep(1 * time.Second)

		playCtx, playCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer playCancel()

		// Tăng âm lượng cuộc gọi lên tối đa (stream 0 = STREAM_VOICE_CALL)
		exec.CommandContext(playCtx, adbExe, "shell", "media", "volume", "--set", "15", "--stream", "0", "--show").Run()
		// Tăng âm lượng media (stream 3) lên tối đa
		exec.CommandContext(playCtx, adbExe, "shell", "media", "volume", "--set", "15", "--stream", "3", "--show").Run()

		// Lấy file audio mới nhất từ thư mục audio/ của project
		localAudio := getLatestAudioFile()
		if localAudio == "" {
			log.Printf("[Android] Không tìm thấy file audio nào trong thư mục audio/\n")
			return
		}

		// Push file lên điện thoại
		audioTarget := "/sdcard/alert.mp3"
		pushErr := exec.CommandContext(playCtx, adbExe, "push", localAudio, audioTarget).Run()
		if pushErr != nil {
			log.Printf("[Android] ❌ Push file thất bại: %v\n", pushErr)
			return
		}
		log.Printf("[Android] ✅ Push file thành công: %s → %s\n", localAudio, audioTarget)

		// Khởi động AlertService trên điện thoại để phát vào luồng cuộc gọi
		playViaAlertApp(playCtx, audioTarget)

		log.Printf("[Android] ✅ Hoàn tất phát âm thanh cảnh báo!\n")
	}()
}

// playAudioOnPC phát file MP3 ra loa của máy tính Windows
// Micro điện thoại (đang cắm cạnh PC) sẽ thu lại và truyền vào cuộc gọi
func playAudioOnPC(audioPath string) {
	log.Printf("[Android] 🖥️ Bắt đầu phát audio trên Windows...\n")
	psCmd := fmt.Sprintf(`
Add-Type -AssemblyName presentationCore
$mp = [System.Windows.Media.MediaPlayer]::new()
$mp.Open([System.Uri]::new('%s'))
$mp.Play()
Start-Sleep -Seconds 30
$mp.Stop()
`, audioPath)
	err := exec.Command("powershell", "-NoProfile", "-Command", psCmd).Run()
	if err != nil {
		log.Printf("[Android] ❌ Lỗi phát audio trên PC: %v\n", err)
	} else {
		log.Printf("[Android] ✅ Đã phát audio trên PC xong!\n")
	}
}

// playViaAlertApp phát audio qua AlertService (Foreground Service)
func playViaAlertApp(ctx context.Context, audioPath string) {
	// Dùng start-foreground-service (bắt buộc trên Android 8+)
	out, err := exec.CommandContext(ctx, adbExe, "shell",
		"am", "start-foreground-service",
		"-n", "com.cardiac.alert/.AlertService",
		"--es", "file", audioPath,
	).CombinedOutput()

	if err != nil {
		log.Printf("[Android] ❌ start-foreground-service thất bại: %v | output: %s\n", err, string(out))
	} else {
		log.Printf("[Android] ✅ AlertService đã khởi động! output: %s\n", string(out))
	}
}


func (g *Gateway) TriggerLocalAlarm(userID primitive.ObjectID, camID primitive.ObjectID) {
	if !isDeviceConnected() {
		return
	}
	log.Printf("[Android] 🔊 Phát loa cảnh báo khẩn cấp tại chỗ (7s)!\n")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	exec.CommandContext(ctx, adbExe, "shell", "media", "volume", "--set", "15", "--stream", "3").Run()

	ttsAudioFile, err := GenerateEmergencySpeech("người thân", "té ngã")
	if err != nil || ttsAudioFile == "" {
		ttsAudioFile = "audio/emergency_vi.mp3" // Fallback
	} else {
		ttsAudioFile = filepath.Join("audio", ttsAudioFile)
	}
	
	audioTarget := "/sdcard/local_alert.mp3"
	exec.CommandContext(ctx, adbExe, "push", ttsAudioFile, audioTarget).Run()
	playViaAlertApp(ctx, audioTarget)
}

func (g *Gateway) getEmergencyContact(userID primitive.ObjectID, specificPhone string) (string, string) {
	var profile bson.M
	err := g.db.Collection("health_profiles").FindOne(context.Background(), bson.M{"user_id": userID}).Decode(&profile)
	if err != nil {
		return specificPhone, "Người thân"
	}
	if contacts, ok := profile["contacts"].(primitive.A); ok && len(contacts) > 0 {
		for _, contactInterface := range contacts {
			if contact, ok := contactInterface.(bson.M); ok {
				phone, _ := contact["phone"].(string)
				name, _ := contact["name"].(string)
				if name == "" { name = "Người thân" }
				if specificPhone != "" && phone == specificPhone {
					return phone, name
				}
			}
		}
		// Fallback to first contact if specificPhone is not found or not provided
		if first, ok := contacts[0].(bson.M); ok {
			phone, _ := first["phone"].(string)
			name, _ := first["name"].(string)
			if name == "" { name = "Người thân" }
			if specificPhone == "" {
				return phone, name
			}
		}
	}
	return specificPhone, "Người thân"
}
