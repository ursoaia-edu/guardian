package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func parseIntParam(param string) (int, error) {
	var value int
	_, err := fmt.Sscanf(param, "%d", &value)
	return value, err
}

func getCurrentTime() string {
	return time.Now().Format("2006-01-02T15:04:05Z07:00")
}

func getLocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		addrs, err := net.InterfaceAddrs()
		if err != nil {
			return "127.0.0.1"
		}
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
				if ipNet.IP.To4() != nil {
					return ipNet.IP.String()
				}
			}
		}
		return "127.0.0.1"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

func loadEnvFile(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			if len(value) >= 2 {
				if (strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"")) ||
					(strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) {
					value = value[1 : len(value)-1]
				}
			}
			if os.Getenv(key) == "" {
				os.Setenv(key, value)
			}
		}
	}
	return scanner.Err()
}

func getWebDir() string {
	if dir := os.Getenv("WEB_DIR"); dir != "" {
		return dir
	}
	return "web"
}

func serveStaticFiles() http.Handler {
	webDir := getWebDir()
	absWebDir, err := filepath.Abs(webDir)
	if err != nil {
		log.Printf("Warning: Could not resolve absolute path for web directory: %v", err)
		absWebDir = webDir
	}

	if _, err := os.Stat(absWebDir); os.IsNotExist(err) {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`<!DOCTYPE html><html><head><title>ProcSentinel</title></head><body>
				<h1>ProcSentinel Server</h1>
				<p>Web interface not available. Build the Flutter web app first.</p>
				<h2>API Endpoints:</h2>
				<ul>
					<li>/client/applications</li>
					<li>/status</li>
					<li>/info</li>
					<li>/health</li>
				</ul>
			</body></html>`))
		})
	}

	return http.FileServer(http.Dir(absWebDir))
}
