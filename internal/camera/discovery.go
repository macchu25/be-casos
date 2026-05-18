package camera

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// DiscoverCameras thực hiện quét dải IP nội bộ để tìm Camera RTSP (Port 554)
func DiscoverCameras() []string {
	localIP := getLocalIP()
	if localIP == "" || localIP == "127.0.0.1" {
		return []string{}
	}

	parts := strings.Split(localIP, ".")
	if len(parts) != 4 {
		return []string{}
	}

	baseIP := fmt.Sprintf("%s.%s.%s.", parts[0], parts[1], parts[2])
	foundIPs := make([]string, 0) // Khởi tạo danh sách trống, không để nil
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Quét nhanh từ .1 đến .254
	for i := 1; i <= 254; i++ {
		wg.Add(1)
		go func(ipSuffix int) {
			defer wg.Done()
			ip := fmt.Sprintf("%s%d", baseIP, ipSuffix)
			
			// Thử kết nối cổng 554 với timeout cực ngắn (300ms)
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:554", ip), 300*time.Millisecond)
			if err == nil {
				conn.Close()
				mu.Lock()
				foundIPs = append(foundIPs, ip)
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()
	return foundIPs
}

func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return ""
}
