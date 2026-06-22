package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
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
	packageBytes, err := buildTemplatePackage(records, d.Tpl.ImageDir())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	fileName := "templates-export-" + time.Now().UTC().Format("20060102-150405") + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fileName))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(packageBytes)
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
		records[i].ImageName = normalizeTemplateImageName(records[i].ImageName)
		if records[i].ImageName == "" {
			records[i].ImageURL = ""
			records[i].ImagePath = ""
			continue
		}
		imageFile := findImportedImageFile(files, records[i].ImageName)
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

func buildTemplatePackage(records []template.Record, imageDir string) ([]byte, error) {
	buffer := &bytes.Buffer{}
	zipWriter := zip.NewWriter(buffer)

	sanitized := make([]template.Record, 0, len(records))
	for _, item := range records {
		exportItem := item
		exportItem.ImageName = normalizeTemplateImageName(exportItem.ImageName)
		if exportItem.ImageName != "" {
			exportItem.ImageURL = "/api/assets/templates/" + exportItem.ImageName
		}
		exportItem.ImagePath = ""
		sanitized = append(sanitized, exportItem)
	}

	if err := writeZipJSON(zipWriter, "templates.json", sanitized); err != nil {
		return nil, err
	}
	if err := writeZipJSON(zipWriter, "meta.json", map[string]any{
		"exported_at":    time.Now().UTC().Format(time.RFC3339Nano),
		"template_count": len(sanitized),
	}); err != nil {
		return nil, err
	}
	for _, item := range sanitized {
		if item.ImageName == "" {
			continue
		}
		data, err := readTemplateImageForExport(item, imageDir)
		if err != nil {
			return nil, fmt.Errorf("export template image failed for %s: %w", item.Label, err)
		}
		entry, err := zipWriter.Create("images/" + item.ImageName)
		if err != nil {
			return nil, err
		}
		if _, err := entry.Write(data); err != nil {
			return nil, err
		}
	}
	if err := zipWriter.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func readTemplateImageForExport(item template.Record, imageDir string) ([]byte, error) {
	candidates := make([]string, 0, 2)
	if current := strings.TrimSpace(item.ImagePath); current != "" {
		candidates = append(candidates, current)
	}
	if item.ImageName != "" {
		fallback := filepath.Join(imageDir, item.ImageName)
		if len(candidates) == 0 || !sameFilePath(candidates[0], fallback) {
			candidates = append(candidates, fallback)
		}
	}
	for _, candidate := range candidates {
		data, err := os.ReadFile(candidate)
		if err == nil {
			return data, nil
		}
	}
	return nil, fmt.Errorf("missing image file: %s", item.ImageName)
}

func findImportedImageFile(files map[string]*zip.File, imageName string) *zip.File {
	candidates := []string{
		"images/" + imageName,
		imageName,
		"images/" + path.Base(strings.ReplaceAll(imageName, "\\", "/")),
		path.Base(strings.ReplaceAll(imageName, "\\", "/")),
	}
	for _, candidate := range candidates {
		if file := files[candidate]; file != nil {
			return file
		}
	}
	return nil
}

func normalizeTemplateImageName(name string) string {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	if name == "" {
		return ""
	}
	return path.Base(name)
}

func sameFilePath(left string, right string) bool {
	if strings.TrimSpace(left) == "" || strings.TrimSpace(right) == "" {
		return false
	}
	return filepath.Clean(left) == filepath.Clean(right)
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
