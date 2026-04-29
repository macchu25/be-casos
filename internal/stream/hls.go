package stream

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type HLSServer struct {
	OutputDir string
}

func NewHLSServer() *HLSServer {
	// Sử dụng ./tmp/streams làm output local
	dir := filepath.Join(".", "tmp", "streams")
	os.MkdirAll(dir, 0755)
	
	log.Printf("[HLS] Khởi tạo HLS Server. Thư mục chứa stream: %s\n", dir)
	return &HLSServer{
		OutputDir: dir,
	}
}

// StartHLS chạy dòng lệnh ffmpeg chuyển RTSP->HLS dưới dạng subprocess. Có cơ chế tự khôi phục khi crash.
func (s *HLSServer) StartHLS(ctx context.Context, camID string, rtspURL string) {
	if rtspURL == "" {
		log.Printf("[HLS] RTSP URL trống cho camera %s\n", camID)
		return
	}

	camDir := filepath.Join(s.OutputDir, camID)
	os.MkdirAll(camDir, 0755)

	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Printf("[HLS] Dừng vòng lặp transcode an toàn cho camera %s\n", camID)
				return
			default:
			}

			playlistPath := filepath.Join(camDir, "stream.m3u8")
			
			// Format arg để giảm latency xuống mức cấu hình (< 5s latency)
			args := []string{
				"-y", // Ghi đè file có sẵn
				"-rtsp_transport", "tcp",
				"-i", rtspURL,
				"-c:v", "libx264",
				"-preset", "ultrafast",
				"-tune", "zerolatency",
				"-b:v", "1000k", // Cố định bitrate để mạng yếu không bị giật
				"-hls_time", "2", // Mỗi chunk HLS dài 2 giây
				"-hls_list_size", "3", // Giữ 3 file m3u8 (6 giây)
				"-hls_flags", "delete_segments+append_list", // Xóa cache segment cũ
				"-f", "hls", // Format đầu ra là hls
				playlistPath,
			}

			log.Printf("[HLS] Bắt đầu FFMPEG stream cho camera %s\n", camID)
			cmd := exec.CommandContext(ctx, "ffmpeg", args...)

			// Chạy ffmpeg process
			err := cmd.Run()
			
			// Kiểm tra lỗi nếu process chết nhưng chưa nhận context Done ()
			if ctx.Err() != nil {
				return
			}
			
			if err != nil {
				log.Printf("[HLS] Tiến trình ffmpeg của %s gặp lỗi: %v. Chuẩn bị tự động khôi phục trong 5s...\n", camID, err)
				time.Sleep(5 * time.Second)
			}
		}
	}()
}
