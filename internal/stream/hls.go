package stream

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"
	"go-backend/internal/logger"
)

type HLSServer struct {
	OutputDir  string
	ArchiveDir string
}

func NewHLSServer() (*HLSServer, error) {
	dir := filepath.Join(".", "tmp", "streams")
	archiveDir := filepath.Join(".", "storage", "archives")
	
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return nil, err
	}
	
	logger.Log.Infof("[HLS] Init Server. Stream: %s | Archive: %s", dir, archiveDir)
	s := &HLSServer{
		OutputDir:  dir,
		ArchiveDir: archiveDir,
	}
	go s.StartCleanupWorker()
	return s, nil
}

func (s *HLSServer) StartCleanupWorker() {
	ticker := time.NewTicker(30 * time.Minute)
	logger.Log.Info("[HLS] Cleanup Worker started (30m interval)")
	for range ticker.C {
		now := time.Now()
		err := filepath.Walk(s.OutputDir, func(path string, info os.FileInfo, err error) error {
			if err != nil { return err }
			// Skip directories and recently modified files (last 1 hour)
			if !info.IsDir() && now.Sub(info.ModTime()) > 1*time.Hour {
				if filepath.Ext(path) == ".ts" || filepath.Ext(path) == ".m3u8" {
					os.Remove(path)
				}
			}
			return nil
		})
		if err != nil {
			logger.Log.Errorf("[HLS] Cleanup Error: %v", err)
		} else {
			logger.Log.Info("[HLS] Cleanup cycle complete (1h retention)")
		}
	}
}

// ArchiveIncident copies current segments to a permanent folder
func (s *HLSServer) ArchiveIncident(camID string, incidentID string) {
	srcDir := filepath.Join(s.OutputDir, camID)
	destDir := filepath.Join(s.ArchiveDir, incidentID)
	os.MkdirAll(destDir, 0755)

	files, _ := filepath.Glob(filepath.Join(srcDir, "*.ts"))
	m3u8, _ := filepath.Glob(filepath.Join(srcDir, "*.m3u8"))
	files = append(files, m3u8...)

	for _, f := range files {
		input, _ := os.ReadFile(f)
		os.WriteFile(filepath.Join(destDir, filepath.Base(f)), input, 0644)
	}
	logger.Log.Infow("🛡️ Đã khóa bằng chứng video cho sự cố", "incidentID", incidentID, "camID", camID)
}

// StartHLS chạy dòng lệnh ffmpeg chuyển RTSP->HLS dưới dạng subprocess. Có cơ chế tự khôi phục khi crash.
func (s *HLSServer) StartHLS(ctx context.Context, camID string, rtspURL string) {
	if rtspURL == "" {
		logger.Log.Warnf("[HLS] RTSP URL trống cho camera %s", camID)
		return
	}

	camDir := filepath.Join(s.OutputDir, camID)
	os.MkdirAll(camDir, 0755)

	// ── TỐI ƯU: Xóa sạch rác cũ để tránh bị delay (Ghosting) ──
	files, _ := os.ReadDir(camDir)
	for _, f := range files {
		os.Remove(filepath.Join(camDir, f.Name()))
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			playlistPath := filepath.Join(camDir, "stream.m3u8")
			
			var args []string
			if len(rtspURL) >= 4 && rtspURL[:4] == "http" {
				// Cấu hình tối ưu cho luồng HTTP/MJPEG từ AI
				args = []string{
					"-y",
					"-f", "mjpeg",
					"-i", rtspURL,
					"-c:v", "libx264",
					"-preset", "ultrafast",
					"-tune", "zerolatency",
					"-b:v", "1000k",
					"-hls_time", "1",
					"-hls_list_size", "5",
					"-hls_flags", "delete_segments+append_list+omit_endlist+discont_start",
					"-f", "hls",
					playlistPath,
				}
			} else {
				// Cấu hình mặc định cho camera RTSP
				args = []string{
					"-y",
					"-rtsp_transport", "tcp",
					"-i", rtspURL,
					"-c:v", "libx264",
					"-preset", "ultrafast",
					"-tune", "zerolatency",
					"-b:v", "1000k",
					"-hls_time", "1",
					"-hls_list_size", "5",
					"-hls_flags", "delete_segments+append_list+omit_endlist+discont_start",
					"-f", "hls",
					playlistPath,
				}
			}

			logger.Log.Infof("[HLS] Bắt đầu FFMPEG cho camera %s", camID)
			cmd := exec.CommandContext(ctx, "ffmpeg", args...)
			err := cmd.Run()
			
			if ctx.Err() != nil { return }
			if err != nil {
				logger.Log.Errorf("[HLS] FFMPEG %s lỗi: %v. Restart in 5s...", camID, err)
				time.Sleep(5 * time.Second)
			}
		}
	}()
}
