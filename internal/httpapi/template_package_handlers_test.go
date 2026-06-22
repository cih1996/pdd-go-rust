package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"unified-server/internal/template"
)

func TestBuildTemplatePackage_FallsBackToImageDirWhenImagePathIsStale(t *testing.T) {
	root := t.TempDir()
	imageDir := filepath.Join(root, "images")
	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		t.Fatalf("mkdir image dir: %v", err)
	}
	imageName := "tpl-click.png"
	imagePath := filepath.Join(imageDir, imageName)
	imageBytes := []byte("png-bytes")
	if err := os.WriteFile(imagePath, imageBytes, 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}

	packageBytes, err := buildTemplatePackage([]template.Record{
		{
			ID:                "tpl-click",
			Label:             "点击图",
			TemplateType:      "click_image",
			RecognitionEngine: "opencv",
			ImageName:         imageName,
			ImagePath:         filepath.Join(root, "stale", imageName),
		},
	}, imageDir)
	if err != nil {
		t.Fatalf("build template package: %v", err)
	}

	reader, err := zip.NewReader(bytes.NewReader(packageBytes), int64(len(packageBytes)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	files := make(map[string]*zip.File, len(reader.File))
	for _, file := range reader.File {
		files[file.Name] = file
	}
	imageFile := files["images/"+imageName]
	if imageFile == nil {
		t.Fatalf("expected exported image file %s", imageName)
	}
	gotBytes, err := readZipFile(imageFile)
	if err != nil {
		t.Fatalf("read exported image: %v", err)
	}
	if !bytes.Equal(gotBytes, imageBytes) {
		t.Fatalf("expected exported image bytes to match original")
	}
}

func TestImportTemplatePackage_NormalizesImageNameAndRestoresImage(t *testing.T) {
	buffer := &bytes.Buffer{}
	zipWriter := zip.NewWriter(buffer)
	records := []template.Record{
		{
			ID:                "tpl-click",
			Label:             "点击图",
			TemplateType:      "click_image",
			RecognitionEngine: "opencv",
			ImageName:         "images\\tpl-click.png",
		},
	}
	if err := writeZipJSON(zipWriter, "templates.json", records); err != nil {
		t.Fatalf("write templates.json: %v", err)
	}
	if err := writeZipJSON(zipWriter, "meta.json", map[string]any{"template_count": 1}); err != nil {
		t.Fatalf("write meta.json: %v", err)
	}
	entry, err := zipWriter.Create("images/tpl-click.png")
	if err != nil {
		t.Fatalf("create image entry: %v", err)
	}
	if _, err := entry.Write([]byte("png-bytes")); err != nil {
		t.Fatalf("write image entry: %v", err)
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}

	imageDir := filepath.Join(t.TempDir(), "images")
	imported, err := importTemplatePackage(buffer.Bytes(), imageDir)
	if err != nil {
		t.Fatalf("import template package: %v", err)
	}
	if len(imported) != 1 {
		t.Fatalf("expected 1 imported template, got %d", len(imported))
	}
	if imported[0].ImageName != "tpl-click.png" {
		t.Fatalf("expected normalized image name, got %q", imported[0].ImageName)
	}
	if imported[0].ImageURL != "/api/assets/templates/tpl-click.png" {
		t.Fatalf("unexpected image url: %q", imported[0].ImageURL)
	}
	if imported[0].ImagePath == "" {
		t.Fatalf("expected image path to be restored")
	}
	gotBytes, err := os.ReadFile(imported[0].ImagePath)
	if err != nil {
		t.Fatalf("read restored image: %v", err)
	}
	if string(gotBytes) != "png-bytes" {
		t.Fatalf("unexpected restored image bytes: %q", string(gotBytes))
	}
}

func TestBuildTemplatePackage_WritesSanitizedTemplatesJSON(t *testing.T) {
	root := t.TempDir()
	imageDir := filepath.Join(root, "images")
	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		t.Fatalf("mkdir image dir: %v", err)
	}
	imageName := "tpl-click.png"
	if err := os.WriteFile(filepath.Join(imageDir, imageName), []byte("png-bytes"), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}

	packageBytes, err := buildTemplatePackage([]template.Record{
		{
			ID:                "tpl-click",
			Label:             "点击图",
			TemplateType:      "click_image",
			RecognitionEngine: "opencv",
			ImageName:         "folder/" + imageName,
			ImagePath:         filepath.Join(imageDir, imageName),
		},
	}, imageDir)
	if err != nil {
		t.Fatalf("build template package: %v", err)
	}

	reader, err := zip.NewReader(bytes.NewReader(packageBytes), int64(len(packageBytes)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	var templatesFile *zip.File
	for _, file := range reader.File {
		if file.Name == "templates.json" {
			templatesFile = file
			break
		}
	}
	if templatesFile == nil {
		t.Fatalf("templates.json not found")
	}
	data, err := readZipFile(templatesFile)
	if err != nil {
		t.Fatalf("read templates.json: %v", err)
	}
	var exported []template.Record
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatalf("unmarshal templates.json: %v", err)
	}
	if exported[0].ImageName != imageName {
		t.Fatalf("expected sanitized image name, got %q", exported[0].ImageName)
	}
	if exported[0].ImagePath != "" {
		t.Fatalf("expected image path to be stripped, got %q", exported[0].ImagePath)
	}
}
