package library

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	mimetype "github.com/gabriel-vasile/mimetype"
)

const (
	docxMIME = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	pptxMIME = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
)

var extensionRules = map[string]fileClassification{
	".jpg":      {Category: "images", Extension: ".jpg", MIMEType: "image/jpeg"},
	".jpeg":     {Category: "images", Extension: ".jpg", MIMEType: "image/jpeg"},
	".png":      {Category: "images", Extension: ".png", MIMEType: "image/png"},
	".gif":      {Category: "images", Extension: ".gif", MIMEType: "image/gif"},
	".webp":     {Category: "images", Extension: ".webp", MIMEType: "image/webp"},
	".pdf":      {Category: "pdf", Extension: ".pdf", MIMEType: "application/pdf"},
	".ppt":      {Category: "ppt", Extension: ".ppt", MIMEType: "application/vnd.ms-powerpoint"},
	".pptx":     {Category: "ppt", Extension: ".pptx", MIMEType: pptxMIME},
	".doc":      {Category: "doc", Extension: ".doc", MIMEType: "application/msword"},
	".docx":     {Category: "doc", Extension: ".docx", MIMEType: docxMIME},
	".md":       {Category: "md", Extension: ".md", MIMEType: "text/markdown"},
	".markdown": {Category: "md", Extension: ".md", MIMEType: "text/markdown"},
	".mp3":      {Category: "mp3", Extension: ".mp3", MIMEType: "audio/mpeg"},
}

func validateFile(filePath, filename, declaredMIME string) (fileClassification, error) {
	extension := strings.ToLower(filepath.Ext(filename))
	rule, ok := extensionRules[extension]
	if !ok {
		return fileClassification{}, ErrUnsupportedFileType
	}
	if err := validateDeclaredMIME(declaredMIME, rule); err != nil {
		return fileClassification{}, err
	}

	var err error
	switch extension {
	case ".jpg", ".jpeg", ".png", ".gif":
		err = validateImage(filePath, rule.MIMEType)
	case ".webp":
		err = validateWebP(filePath)
	case ".pdf":
		err = validatePDF(filePath)
	case ".docx":
		err = validateOOXML(filePath, "word/document.xml")
	case ".pptx":
		err = validateOOXML(filePath, "ppt/presentation.xml")
	case ".doc", ".ppt":
		err = validateOLE(filePath, rule.MIMEType)
	case ".md", ".markdown":
		err = validateMarkdown(filePath)
	case ".mp3":
		err = validateMP3(filePath)
	}
	if err != nil {
		if errors.Is(err, ErrFileTypeMismatch) || errors.Is(err, ErrInvalidFile) {
			return fileClassification{}, err
		}
		return fileClassification{}, fmt.Errorf("validate file content: %w", err)
	}
	return rule, nil
}

func validateDeclaredMIME(declared string, rule fileClassification) error {
	declared = strings.TrimSpace(strings.ToLower(strings.Split(declared, ";")[0]))
	if declared == "" || declared == "application/octet-stream" {
		return nil
	}
	if declared != strings.ToLower(rule.MIMEType) {
		if rule.MIMEType == "text/markdown" && (declared == "text/plain" || declared == "text/markdown") {
			return nil
		}
		return ErrFileTypeMismatch
	}
	return nil
}

func validateImage(filePath, expectedMIME string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	_, format, err := image.DecodeConfig(file)
	if err != nil {
		return ErrInvalidFile
	}
	if mime.TypeByExtension("."+format) != expectedMIME && !(format == "jpeg" && expectedMIME == "image/jpeg") {
		return ErrFileTypeMismatch
	}
	detected, err := mimetype.DetectFile(filePath)
	if err != nil || detected.String() != expectedMIME {
		return ErrFileTypeMismatch
	}
	return nil
}

func validateWebP(filePath string) error {
	content, err := readPrefix(filePath, 30)
	if err != nil || len(content) < 20 {
		return ErrInvalidFile
	}
	if string(content[:4]) != "RIFF" || string(content[8:12]) != "WEBP" {
		return ErrFileTypeMismatch
	}
	info, err := os.Stat(filePath)
	if err != nil || int64(binary.LittleEndian.Uint32(content[4:8]))+8 != info.Size() {
		return ErrInvalidFile
	}
	chunkSize := int64(binary.LittleEndian.Uint32(content[16:20]))
	if chunkSize <= 0 || 20+chunkSize > info.Size() {
		return ErrInvalidFile
	}
	switch string(content[12:16]) {
	case "VP8 ":
		if len(content) < 30 || !bytes.Equal(content[23:26], []byte{0x9d, 0x01, 0x2a}) {
			return ErrInvalidFile
		}
	case "VP8L":
		if len(content) < 25 || content[20] != 0x2f {
			return ErrInvalidFile
		}
	case "VP8X":
		if chunkSize < 10 || len(content) < 30 {
			return ErrInvalidFile
		}
	default:
		return ErrFileTypeMismatch
	}
	return nil
}

func validatePDF(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() < 10 {
		return ErrInvalidFile
	}
	prefix := make([]byte, 5)
	if _, err := io.ReadFull(file, prefix); err != nil || string(prefix) != "%PDF-" {
		return ErrFileTypeMismatch
	}
	tailSize := int64(1024)
	if info.Size() < tailSize {
		tailSize = info.Size()
	}
	if _, err := file.Seek(-tailSize, io.SeekEnd); err != nil {
		return ErrInvalidFile
	}
	tail, err := io.ReadAll(io.LimitReader(file, tailSize))
	if err != nil || !bytes.Contains(tail, []byte("%%EOF")) {
		return ErrInvalidFile
	}
	return nil
}

func validateOOXML(filePath, requiredEntry string) error {
	archive, err := zip.OpenReader(filePath)
	if err != nil {
		return ErrFileTypeMismatch
	}
	defer archive.Close()
	expectedMIME := docxMIME
	expectedRoot := "document"
	if requiredEntry == "ppt/presentation.xml" {
		expectedMIME = pptxMIME
		expectedRoot = "presentation"
	}
	foundType := false
	foundMain := false
	for _, entry := range archive.File {
		name := strings.ToLower(strings.ReplaceAll(entry.Name, "\\", "/"))
		if strings.HasSuffix(name, "vbaproject.bin") {
			return ErrUnsupportedFileType
		}
		switch name {
		case "[content_types].xml":
			content, err := readZipEntry(entry, 1<<20)
			if err != nil {
				return ErrInvalidFile
			}
			matched, macro, err := validateContentTypes(content, requiredEntry, expectedMIME)
			if err != nil {
				return ErrInvalidFile
			}
			if macro {
				return ErrUnsupportedFileType
			}
			foundType = matched
		case strings.ToLower(requiredEntry):
			content, err := readZipEntry(entry, 4<<20)
			if err != nil || !validXMLRoot(content, expectedRoot) {
				return ErrInvalidFile
			}
			foundMain = true
		}
	}
	if !foundType || !foundMain {
		return ErrFileTypeMismatch
	}
	return nil
}

func validateContentTypes(content []byte, requiredEntry, expectedMIME string) (bool, bool, error) {
	decoder := xml.NewDecoder(bytes.NewReader(content))
	matched := false
	macro := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return matched, macro, nil
		}
		if err != nil {
			return false, false, err
		}
		start, ok := token.(xml.StartElement)
		if !ok || strings.ToLower(start.Name.Local) != "override" {
			continue
		}
		var partName, contentType string
		for _, attribute := range start.Attr {
			switch strings.ToLower(attribute.Name.Local) {
			case "partname":
				partName = strings.TrimPrefix(strings.ToLower(attribute.Value), "/")
			case "contenttype":
				contentType = strings.ToLower(attribute.Value)
			}
		}
		if strings.Contains(contentType, "macroenabled") {
			macro = true
		}
		if partName == strings.ToLower(requiredEntry) && contentType == strings.ToLower(expectedMIME) {
			matched = true
		}
	}
}

func validXMLRoot(content []byte, expected string) bool {
	decoder := xml.NewDecoder(bytes.NewReader(content))
	for {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		if start, ok := token.(xml.StartElement); ok {
			return strings.EqualFold(start.Name.Local, expected)
		}
	}
}

func validateOLE(filePath, expectedMIME string) error {
	prefix, err := readPrefix(filePath, 8)
	if err != nil || len(prefix) < 8 {
		return ErrInvalidFile
	}
	if !bytes.Equal(prefix, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}) {
		return ErrFileTypeMismatch
	}
	detected, err := mimetype.DetectFile(filePath)
	if err != nil {
		return ErrInvalidFile
	}
	if detected.String() != expectedMIME {
		return ErrFileTypeMismatch
	}
	return nil
}

func validateMarkdown(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	buffer := make([]byte, 32*1024+utf8.UTFMax)
	carry := 0
	for {
		read, readErr := file.Read(buffer[carry : len(buffer)-utf8.UTFMax])
		end := carry + read
		content := buffer[:end]
		if bytes.IndexByte(content, 0) >= 0 {
			return ErrFileTypeMismatch
		}
		complete := end
		if readErr == nil {
			complete = validUTF8Prefix(content)
		}
		if !utf8.Valid(content[:complete]) {
			return ErrFileTypeMismatch
		}
		carry = copy(buffer, content[complete:])
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func validUTF8Prefix(content []byte) int {
	start := len(content) - 1
	limit := len(content) - utf8.UTFMax
	if limit < 0 {
		limit = 0
	}
	for start > limit && content[start]&0xc0 == 0x80 {
		start--
	}
	if start >= 0 && !utf8.FullRune(content[start:]) {
		return start
	}
	return len(content)
}

func validateMP3(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() < 4 {
		return ErrInvalidFile
	}
	offset := int64(0)
	header := make([]byte, 10)
	read, err := io.ReadFull(file, header)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return ErrInvalidFile
	}
	if read >= 3 && string(header[:3]) == "ID3" {
		if read < 10 || header[3] == 0xff || header[4] == 0xff || header[5]&0x0f != 0 || !syncSafe(header[6:10]) {
			return ErrInvalidFile
		}
		tagSize := int64(header[6])<<21 | int64(header[7])<<14 | int64(header[8])<<7 | int64(header[9])
		offset = 10 + tagSize
	}
	if offset+4 > info.Size() {
		return ErrFileTypeMismatch
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return ErrInvalidFile
	}
	frameHeader := make([]byte, 4)
	if _, err := io.ReadFull(file, frameHeader); err != nil {
		return ErrInvalidFile
	}
	frameLength, err := mp3FrameLength(frameHeader)
	if err != nil || offset+int64(frameLength) > info.Size() {
		return ErrFileTypeMismatch
	}
	return nil
}

func syncSafe(value []byte) bool {
	for _, b := range value {
		if b&0x80 != 0 {
			return false
		}
	}
	return true
}

func mp3FrameLength(header []byte) (int, error) {
	if len(header) < 4 || header[0] != 0xff || header[1]&0xe0 != 0xe0 {
		return 0, ErrFileTypeMismatch
	}
	version := (header[1] >> 3) & 0x03
	layer := (header[1] >> 1) & 0x03
	bitrateIndex := (header[2] >> 4) & 0x0f
	sampleIndex := (header[2] >> 2) & 0x03
	if version == 1 || layer != 1 || bitrateIndex == 0 || bitrateIndex == 15 || sampleIndex == 3 {
		return 0, ErrFileTypeMismatch
	}
	mpeg1Bitrates := [...]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
	mpeg2Bitrates := [...]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}
	sampleRates := [...]int{44100, 48000, 32000}
	bitrate := mpeg1Bitrates[bitrateIndex]
	sampleRate := sampleRates[sampleIndex]
	coefficient := 144
	if version != 3 {
		bitrate = mpeg2Bitrates[bitrateIndex]
		coefficient = 72
		if version == 2 {
			sampleRate /= 2
		} else {
			sampleRate /= 4
		}
	}
	padding := int((header[2] >> 1) & 0x01)
	return coefficient*bitrate*1000/sampleRate + padding, nil
}

func readPrefix(filePath string, size int64) ([]byte, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, size))
}

func readZipEntry(entry *zip.File, max int64) ([]byte, error) {
	reader, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	content, err := io.ReadAll(io.LimitReader(reader, max+1))
	if err != nil || int64(len(content)) > max {
		return nil, fmt.Errorf("zip entry too large")
	}
	return content, nil
}
