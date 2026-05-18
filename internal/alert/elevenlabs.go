package alert

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"
	"net/url"
	"io"
)

// CallRelative sử dụng ID duy nhất và có thời gian chờ để xếp hàng cuộc gọi
func CallRelative(phoneNumber, contactName, camName, incidentType string) error {
	// Dùng UnixNano để đảm bảo ID tuyệt đối không trùng lặp dù gọi cực nhanh
	uniqueID := time.Now().UnixNano()
	
	sentences := []string{
		fmt.Sprintf("Xin chào người nhà %s.", contactName),
		fmt.Sprintf("Phát hiện có người bị %s tại camera %s.", incidentType, camName),
		"Vui lòng kiểm tra và phát tín hiệu hệ thống.",
		"Đây là cuộc gọi tự động.",
	}

	audioDir := "audio"
	os.MkdirAll(audioDir, 0755)
	finalPath := filepath.Join(audioDir, fmt.Sprintf("alert_%d.mp3", uniqueID))
	
	out, err := os.Create(finalPath)
	if err != nil {
		return err
	}
	defer out.Close()

	for i, s := range sentences {
		tempFile := filepath.Join(audioDir, fmt.Sprintf("part_%d_%d.mp3", uniqueID, i))
		encodedText := url.QueryEscape(s)
		ttsURL := fmt.Sprintf("https://translate.google.com/translate_tts?ie=UTF-8&q=%s&tl=vi&client=tw-ob", encodedText)
		
		cmd := exec.Command("curl", "-L", "-A", "Mozilla/5.0", ttsURL, "-o", tempFile)
		if err := cmd.Run(); err == nil {
			fPart, _ := os.Open(tempFile)
			io.Copy(out, fPart)
			fPart.Close()
			os.Remove(tempFile)
		}
		time.Sleep(200 * time.Millisecond)
	}
	
	err = triggerAndroidCall(phoneNumber, finalPath, uniqueID)
	
	// QUAN TRỌNG: Đợi thêm 15 giây để cuộc gọi kết thúc hẳn trước khi cho phép cuộc tiếp theo bắt đầu
	log.Println("⏳ Đang đợi 15 giây để cuộc gọi kết thúc (Cool-down)...")
	time.Sleep(15 * time.Second)
	
	return err
}

func triggerAndroidCall(toPhoneNumber, audioPath string, uniqueID int64) error {
	workingDir, _ := os.Getwd()
	adbPath := filepath.Join(workingDir, "..", "platform-tools", "adb.exe")
	if _, err := os.Stat(adbPath); err != nil {
		adbPath = filepath.Join("C:\\cardiac-alert", "platform-tools", "adb.exe")
	}

	remotePath := fmt.Sprintf("/sdcard/alert_%d.mp3", uniqueID)

	exec.Command(adbPath, "shell", "input", "keyevent", "KEYCODE_WAKEUP").Run()
	exec.Command(adbPath, "shell", "am", "force-stop", "com.android.music").Run()
	exec.Command(adbPath, "shell", "am", "force-stop", "com.google.android.music").Run()
	exec.Command(adbPath, "shell", "rm /sdcard/alert_*.mp3").Run()

	log.Println("📤 Đang nạp âm thanh vào điện thoại...")
	exec.Command(adbPath, "push", audioPath, remotePath).Run()

	log.Printf("📞 Đang gọi %s...\n", toPhoneNumber)
	exec.Command(adbPath, "shell", "am", "start", "-a", "android.intent.action.CALL", "-d", "tel:"+toPhoneNumber).Run()

	time.Sleep(7 * time.Second)

	exec.Command(adbPath, "shell", "media", "volume", "--stream", "0", "--set", "15").Run()
	exec.Command(adbPath, "shell", "media", "volume", "--stream", "3", "--set", "15").Run()
	exec.Command(adbPath, "shell", "input", "keyevent", "KEYCODE_SPEAKERPHONE_ON").Run()

	log.Println("📢 Bắt đầu phát thông báo...")
	exec.Command(adbPath, "shell", "am", "start", "-a", "android.intent.action.VIEW", "-d", "file://"+remotePath, "-t", "audio/mp3").Run()
	
	time.Sleep(1500 * time.Millisecond)
	
	for i := 0; i < 3; i++ {
		exec.Command(adbPath, "shell", "input", "keyevent", "126").Run()
		time.Sleep(500 * time.Millisecond)
	}

	return nil
}
