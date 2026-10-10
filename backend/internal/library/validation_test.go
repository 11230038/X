package library

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateFileRecognizesSupportedTypes(t *testing.T) {
	pngContent := makePNG(t)
	docxContent := makeOOXML(t, "word/document.xml", false)
	pptxContent := makeOOXML(t, "ppt/presentation.xml", false)

	tests := []struct {
		name     string
		filename string
		content  []byte
		category string
		mimeType string
	}{
		{name: "png", filename: "photo.png", content: pngContent, category: "images", mimeType: "image/png"},
		{name: "pdf", filename: "paper.pdf", content: []byte("%PDF-1.7\n%%EOF\n"), category: "pdf", mimeType: "application/pdf"},
		{name: "docx", filename: "notes.docx", content: docxContent, category: "doc", mimeType: docxMIME},
		{name: "pptx", filename: "slides.pptx", content: pptxContent, category: "ppt", mimeType: pptxMIME},
		{name: "markdown", filename: "readme.md", content: []byte("# Heading\n"), category: "md", mimeType: "text/markdown"},
		{name: "mp3 id3", filename: "audio.mp3", content: makeMP3WithID3(), category: "mp3", mimeType: "audio/mpeg"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tt.filename)
			if err := os.WriteFile(path, tt.content, 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := validateFile(path, tt.filename, "")
			if err != nil {
				t.Fatalf("validateFile() error = %v", err)
			}
			if got.Category != tt.category || got.MIMEType != tt.mimeType {
				t.Errorf("classification = %+v, want category=%s mime=%s", got, tt.category, tt.mimeType)
			}
		})
	}
}

func TestValidateFileRejectsDisguisedAndMacroFiles(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		content  []byte
	}{
		{name: "png named pdf", filename: "fake.pdf", content: makePNG(t)},
		{name: "arbitrary zip named docx", filename: "fake.docx", content: makeOOXML(t, "other.txt", false)},
		{name: "macro docx", filename: "macro.docx", content: makeOOXML(t, "word/document.xml", true)},
		{name: "invalid main xml", filename: "fake.docx", content: makeOOXMLWithContent(t, "word/document.xml", "not xml", false)},
		{name: "nul markdown", filename: "bad.md", content: []byte("hello\x00world")},
		{name: "id3 without audio", filename: "bad.mp3", content: append([]byte("ID3\x04\x00\x00\x00\x00\x00\x00"), []byte("not audio")...)},
		{name: "short mpeg header", filename: "bad.mp3", content: []byte{0xff, 0xfb}},
		{name: "short webp header", filename: "bad.webp", content: []byte("RIFF\x08\x00\x00\x00WEBPVP8 ")},
		{name: "svg", filename: "image.svg", content: []byte("<svg></svg>")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tt.filename)
			if err := os.WriteFile(path, tt.content, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := validateFile(path, tt.filename, ""); err == nil {
				t.Fatal("validateFile() error = nil, want rejection")
			}
		})
	}
}

func TestValidateMarkdownHandlesUTF8AcrossBufferBoundary(t *testing.T) {
	content := append(bytes.Repeat([]byte{'a'}, 32*1024-1), []byte("你")...)
	path := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := validateFile(path, "notes.md", ""); err != nil {
		t.Fatalf("validateFile() error = %v", err)
	}
}

func TestValidateFileRejectsDeclaredMIMEMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "paper.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7\n%%EOF\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := validateFile(path, "paper.pdf", "image/png"); err == nil {
		t.Fatal("validateFile() error = nil, want declared MIME mismatch")
	}
}

func TestValidateFileRecognizesAllSupportedExtensions(t *testing.T) {
	tests := []struct {
		filename string
		content  []byte
	}{
		{filename: "photo.jpg", content: makeJPEG(t)},
		{filename: "photo.jpeg", content: makeJPEG(t)},
		{filename: "photo.gif", content: makeGIF(t)},
		{filename: "photo.webp", content: makeWebP()},
		{filename: "notes.markdown", content: []byte("# Heading\n")},
	}
	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tt.filename)
			if err := os.WriteFile(path, tt.content, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := validateFile(path, tt.filename, ""); err != nil {
				t.Fatalf("validateFile() error = %v", err)
			}
		})
	}
}

func makePNG(t *testing.T) []byte {
	t.Helper()
	img := testImage()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func makeJPEG(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, testImage(), nil); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func makeGIF(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := gif.Encode(&buffer, testImage(), nil); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func testImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	return img
}

func makeOOXML(t *testing.T, mainEntry string, macro bool) []byte {
	t.Helper()
	mainContent := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body/></w:document>`
	contentType := docxMIME
	if mainEntry == "ppt/presentation.xml" {
		mainContent = `<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"/>`
		contentType = pptxMIME
	}
	return makeOOXMLPackage(t, mainEntry, mainContent, contentType, macro)
}

func makeOOXMLWithContent(t *testing.T, mainEntry, mainContent string, macro bool) []byte {
	t.Helper()
	return makeOOXMLPackage(t, mainEntry, mainContent, docxMIME, macro)
}

func makeOOXMLPackage(t *testing.T, mainEntry, mainContent, contentType string, macro bool) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	contentTypes := `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/` + mainEntry + `" ContentType="` + contentType + `"/></Types>`
	for name, content := range map[string]string{
		"[Content_Types].xml": contentTypes,
		mainEntry:             mainContent,
	} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if macro {
		entry, err := writer.Create("word/vbaProject.bin")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte("macro")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func makeWebP() []byte {
	content := make([]byte, 30)
	copy(content[0:4], "RIFF")
	binary.LittleEndian.PutUint32(content[4:8], uint32(len(content)-8))
	copy(content[8:12], "WEBP")
	copy(content[12:16], "VP8 ")
	binary.LittleEndian.PutUint32(content[16:20], 10)
	copy(content[23:26], []byte{0x9d, 0x01, 0x2a})
	return content
}

func makeMP3WithID3() []byte {
	frame := make([]byte, 417)
	binary.BigEndian.PutUint32(frame[:4], 0xfffb9000)
	return append([]byte("ID3\x04\x00\x00\x00\x00\x00\x00"), frame...)
}
