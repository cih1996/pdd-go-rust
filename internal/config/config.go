package config

import "os"

type Config struct {
    HTTPAddr         string
    FrontendDistDir  string
    AdapterBaseURL   string
    OCRModelDir      string
    OpenCVAssetDir   string
    EnableVisionMock bool
}

func Load() Config {
    return Config{
        HTTPAddr:         getenv("UNIFIED_HTTP_ADDR", ":8080"),
        FrontendDistDir:  getenv("FRONTEND_DIST_DIR", "./web/dist"),
        AdapterBaseURL:   getenv("ADAPTER_BASE_URL", "http://127.0.0.1:8091"),
        OCRModelDir:      getenv("OCR_MODEL_DIR", "./assets/ocr"),
        OpenCVAssetDir:   getenv("OPENCV_ASSET_DIR", "./assets/opencv"),
        EnableVisionMock: getenv("ENABLE_VISION_MOCK", "true") == "true",
    }
}

func getenv(key string, fallback string) string {
    if value := os.Getenv(key); value != "" {
        return value
    }
    return fallback
}