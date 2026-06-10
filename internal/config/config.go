package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type Config struct {
	HTTPAddr         string
	FrontendDistDir  string
	AdapterBaseURL   string
	OCRModelDir      string
	OpenCVAssetDir   string
	ADBPath          string
	SQLitePath       string
	EnableVisionMock bool
}

func Load() Config {
	sqlitePath := getenv("SQLITE_PATH", "./.runtime/unified-server.db")
	return Config{
		HTTPAddr:         getenv("UNIFIED_HTTP_ADDR", ":18080"),
		FrontendDistDir:  getenv("FRONTEND_DIST_DIR", "./frontend/dist"),
		AdapterBaseURL:   getenv("ADAPTER_BASE_URL", "http://127.0.0.1:8091"),
		OCRModelDir:      getenv("OCR_MODEL_DIR", "./assets/ocr"),
		OpenCVAssetDir:   getenv("OPENCV_ASSET_DIR", "./assets/opencv"),
		ADBPath:          resolveADBPath(),
		SQLitePath:       resolveProjectPath(sqlitePath),
		EnableVisionMock: getenv("ENABLE_VISION_MOCK", "false") == "true",
	}
}

func getenv(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func resolveADBPath() string {
	if value := strings.TrimSpace(os.Getenv("ADB_PATH")); value != "" {
		return normalizeADBPath(value)
	}

	for _, root := range []string{
		strings.TrimSpace(os.Getenv("ANDROID_SDK_ROOT")),
		strings.TrimSpace(os.Getenv("ANDROID_HOME")),
	} {
		if root == "" {
			continue
		}
		candidate := filepath.Join(root, "platform-tools", adbExecutableName())
		if fileExists(candidate) {
			return candidate
		}
	}

	if resolved, err := exec.LookPath(adbExecutableName()); err == nil {
		return resolved
	}
	if resolved, err := exec.LookPath("adb"); err == nil {
		return resolved
	}
	return adbExecutableName()
}

func normalizeADBPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return adbExecutableName()
	}
	info, err := os.Stat(value)
	if err == nil && info.IsDir() {
		candidate := filepath.Join(value, adbExecutableName())
		if fileExists(candidate) {
			return candidate
		}
	}
	return value
}

func resolveProjectPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	if filepath.IsAbs(value) {
		return value
	}
	if root, ok := findProjectRoot(); ok {
		return filepath.Clean(filepath.Join(root, value))
	}
	if abs, err := filepath.Abs(value); err == nil {
		return abs
	}
	return value
}

func findProjectRoot() (string, bool) {
	wd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	current := wd
	for {
		if fileExists(filepath.Join(current, "go.mod")) {
			return current, true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", false
		}
		current = parent
	}
}

func adbExecutableName() string {
	if runtime.GOOS == "windows" {
		return "adb.exe"
	}
	return "adb"
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
