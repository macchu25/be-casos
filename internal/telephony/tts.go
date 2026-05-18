package telephony

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"
	"io"
)

// GenerateEmergencySpeech tạo file âm thanh cứu hộ tiếng Việt
func GenerateEmergencySpeech(contactName, incidentType string) (string, error) {
	uniqueID := time.Now().UnixNano()
	filename := fmt.Sprintf("alert_%d.mp3", uniqueID)
	
	sentences := []string{
		fmt.Sprintf("Xin chào %s.", contactName),
		fmt.Sprintf("Hệ thống Casos phát hiện có người bị %s.", incidentType),
		"Vui lòng kiểm tra ngay lập tức.",
		"Tôi nhắc lại, có sự cố khẩn cấp.",
	}

	audioDir := "audio"
	os.MkdirAll(audioDir, 0755)
	finalPath := filepath.Join(audioDir, filename)
	
	out, err := os.Create(finalPath)
	if err != nil {
		return "", err
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
		time.Sleep(100 * time.Millisecond)
	}
	
	return filename, nil
}
