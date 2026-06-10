package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"unified-server/internal/template"
)

func (d RouterDeps) handleImportTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if err := r.ParseMultipartForm(128 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid multipart form"})
		return
	}

	file, _, err := r.FormFile("package")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing package file"})
		return
	}
	defer file.Close()

	raw, err := io.ReadAll(file)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "read package failed"})
		return
	}
	records, err := importTemplatePackage(raw, d.Tpl.ImageDir())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	replaceExisting := parseBoolDefault(r.FormValue("replace_existing"), false)
	count := d.Tpl.ImportRecords(records, replaceExisting)
	writeJSON(w, http.StatusOK, map[string]any{
		"message":          "模板包导入成功",
		"imported_count":   count,
		"replace_existing": replaceExisting,
	})
}

func (d RouterDeps) handleExportTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	records := d.Tpl.List()
	buffer := &bytes.Buffer{}
	zipWriter := zip.NewWriter(buffer)

	sanitized := make([]template.Record, 0, len(records))
	for _, item := range records {
		exportItem := item
		if exportItem.ImageName != "" {
			exportItem.ImageURL = "/assets/templates/" + exportItem.ImageName
		}
		exportItem.ImagePath = ""
		sanitized = append(sanitized, exportItem)
	}

	if err := writeZipJSON(zipWriter, "templates.json", sanitized); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := writeZipJSON(zipWriter, "meta.json", map[string]any{
		"exported_at":    time.Now().UTC().Format(time.RFC3339Nano),
		"template_count": len(sanitized),
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, item := range records {
		if strings.TrimSpace(item.ImageName) == "" {
			continue
		}
		imagePath := item.ImagePath
		if imagePath == "" {
			imagePath = filepath.Join(d.Tpl.ImageDir(), item.ImageName)
		}
		data, err := os.ReadFile(imagePath)
		if err != nil {
			continue
		}
		entry, err := zipWriter.Create("images/" + item.ImageName)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if _, err := entry.Write(data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := zipWriter.Close(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	fileName := "templates-export-" + time.Now().UTC().Format("20060102-150405") + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fileName))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buffer.Bytes())
}

func importTemplatePackage(raw []byte, imageDir string) ([]template.Record, error) {
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("invalid template package zip")
	}

	files := make(map[string]*zip.File, len(reader.File))
	for _, file := range reader.File {
		files[file.Name] = file
	}

	templatesFile, ok := files["templates.json"]
	if !ok {
		return nil, fmt.Errorf("templates.json not found in package")
	}
	templateBytes, err := readZipFile(templatesFile)
	if err != nil {
		return nil, fmt.Errorf("read templates.json failed")
	}

	var records []template.Record
	if err := json.Unmarshal(templateBytes, &records); err != nil {
		return nil, fmt.Errorf("invalid templates.json")
	}

	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		return nil, err
	}
	for i := range records {
		if strings.TrimSpace(records[i].ImageName) == "" {
			records[i].ImageURL = ""
			records[i].ImagePath = ""
			continue
		}
		imageFile := files["images/"+records[i].ImageName]
		if imageFile == nil {
			imageFile = files[records[i].ImageName]
		}
		if imageFile == nil {
			return nil, fmt.Errorf("missing image file: %s", records[i].ImageName)
		}
		imageBytes, err := readZipFile(imageFile)
		if err != nil {
			return nil, fmt.Errorf("read image failed: %s", records[i].ImageName)
		}
		targetPath := filepath.Join(imageDir, records[i].ImageName)
		if err := os.WriteFile(targetPath, imageBytes, 0o644); err != nil {
			return nil, err
		}
		records[i].ImagePath = targetPath
		records[i].ImageURL = "/api/assets/templates/" + records[i].ImageName
	}

	return records, nil
}

func readZipFile(file *zip.File) ([]byte, error) {
	handle, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	return io.ReadAll(handle)
}

func writeZipJSON(writer *zip.Writer, name string, value any) error {
	entry, err := writer.Create(name)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = entry.Write(data)
	return err
}

func parseBoolDefault(value string, fallback bool) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}
