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
	OpenCVBaseURL    string
	OCRBaseURL       string
	OCRModelDir      string
	OpenCVAssetDir   string
	RuntimeDir       string
	DebugAssetDir    string
	ADBPath          string
	SQLitePath       string
	EnableVisionMock bool
}

func Load() Config {
	runtimeDir := resolveRuntimeDir(strings.TrimSpace(os.Getenv("RUNTIME_DIR")))
	sqlitePath := getenv("SQLITE_PATH", filepath.Join(runtimeDir, "unified-server.db"))
	return Config{
		HTTPAddr:         getenv("UNIFIED_HTTP_ADDR", ":18080"),
		FrontendDistDir:  getenv("FRONTEND_DIST_DIR", "./frontend/dist"),
		AdapterBaseURL:   getenv("ADAPTER_BASE_URL", "http://127.0.0.1:8091"),
		OpenCVBaseURL:    getenv("OPENCV_BASE_URL", "http://127.0.0.1:7771"),
		OCRBaseURL:       getenv("OCR_BASE_URL", "http://127.0.0.1:5005"),
		OCRModelDir:      getenv("OCR_MODEL_DIR", "./assets/ocr"),
		OpenCVAssetDir:   getenv("OPENCV_ASSET_DIR", "./assets/opencv"),
		RuntimeDir:       runtimeDir,
		DebugAssetDir:    filepath.Join(runtimeDir, "debug"),
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

	if resolved, ok := findBundledADBPath(); ok {
		return resolved
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

func findBundledADBPath() (string, bool) {
	projectRoot, _ := findProjectRoot()
	executableDir := ""
	if executablePath, err := os.Executable(); err == nil {
		executableDir = filepath.Dir(executablePath)
	}
	return findBundledADBPathIn(projectRoot, executableDir)
}

func findBundledADBPathIn(projectRoot string, executableDir string) (string, bool) {
	candidates := make([]string, 0, 6)
	addCandidate := func(candidate string) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			return
		}
		candidates = append(candidates, filepath.Clean(candidate))
	}

	if projectRoot != "" {
		addCandidate(filepath.Join(projectRoot, "adb", adbExecutableName()))
		addCandidate(filepath.Join(projectRoot, adbExecutableName()))
		addCandidate(filepath.Join(projectRoot, "platform-tools", adbExecutableName()))
	}
	if executableDir != "" {
		addCandidate(filepath.Join(executableDir, "adb", adbExecutableName()))
		addCandidate(filepath.Join(executableDir, adbExecutableName()))
		addCandidate(filepath.Join(executableDir, "platform-tools", adbExecutableName()))
	}

	for _, candidate := range candidates {
		if fileExists(candidate) {
			return candidate, true
		}
	}
	return "", false
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

func resolveRuntimeDir(value string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return resolveProjectPath(value)
	}
	return filepath.Join(defaultAppDataDir(), "unified-server")
}

func defaultAppDataDir() string {
	for _, item := range []string{
		strings.TrimSpace(os.Getenv("LOCALAPPDATA")),
		strings.TrimSpace(os.Getenv("APPDATA")),
	} {
		if item != "" {
			return filepath.Join(item, "PddGoRust")
		}
	}
	if dir, err := os.UserConfigDir(); err == nil && strings.TrimSpace(dir) != "" {
		return filepath.Join(dir, "PddGoRust")
	}
	if dir, err := os.UserHomeDir(); err == nil && strings.TrimSpace(dir) != "" {
		return filepath.Join(dir, ".pdd-go-rust")
	}
	return filepath.Clean(".runtime")
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
