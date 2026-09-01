// Command sensitive_content_matcher is the binary-safe matching core shared by
// every sensitive-content scan mode. It intentionally scans the complete raw
// byte stream of validated images. It cannot OCR semantic text encoded inside
// compressed pixels; release review retains a separate visual/metadata check.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type rule struct {
	name    string
	pattern *regexp.Regexp
}

func main() {
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "invalid sensitive_content_matcher arguments")
		os.Exit(2)
	}
	rules, err := loadRules(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, "unable to load sensitive-content rules")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "--scan-file", "--scan-text":
		data, err := os.ReadFile(os.Args[4])
		if err != nil {
			fmt.Fprintln(os.Stderr, "unable to read scan input")
			os.Exit(2)
		}
		if os.Args[1] == "--scan-file" {
			if err := validateContent(os.Args[3], data); err != nil {
				fmt.Printf("binary-content\t%s\n", os.Args[3])
				os.Exit(1)
			}
		}
		if scanData(rules, os.Args[3], data) {
			os.Exit(1)
		}
	case "--scan-directory":
		exceptions, err := loadExceptions(os.Args[3])
		if err != nil {
			fmt.Fprintln(os.Stderr, "unable to load sensitive-content exceptions")
			os.Exit(2)
		}
		found, err := scanDirectory(rules, exceptions, os.Args[4])
		if err != nil {
			fmt.Fprintln(os.Stderr, "unable to enumerate scan directory")
			os.Exit(2)
		}
		if found {
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "invalid sensitive_content_matcher mode")
		os.Exit(2)
	}
}

func scanData(rules []rule, path string, data []byte) bool {
	found := false
	for _, candidate := range rules {
		if candidate.pattern.FindIndex(data) != nil {
			fmt.Printf("%s\t%s\n", candidate.name, path)
			found = true
		}
	}
	return found
}

func loadExceptions(path string) (map[string][sha256.Size]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	result := make(map[string][sha256.Size]byte)
	for _, raw := range strings.Split(string(data), "\n") {
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		name, digestText, ok := strings.Cut(raw, "\t")
		if !ok || name == "" || strings.Contains(name, "\\") || filepath.IsAbs(name) {
			return nil, fmt.Errorf("invalid exception")
		}
		decoded, err := hex.DecodeString(digestText)
		if err != nil || len(decoded) != sha256.Size {
			return nil, fmt.Errorf("invalid exception digest")
		}
		var digest [sha256.Size]byte
		copy(digest[:], decoded)
		if _, exists := result[name]; exists {
			return nil, fmt.Errorf("duplicate exception")
		}
		result[name] = digest
	}
	return result, nil
}

func scanDirectory(rules []rule, exceptions map[string][sha256.Size]byte, root string) (bool, error) {
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("invalid root")
	}
	found := false
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if rel == ".git" && entry.IsDir() {
			return filepath.SkipDir
		}
		for _, value := range []byte(rel) {
			if value < 32 || value == 127 {
				fmt.Println("unsafe-path\t[control-character-path]")
				found = true
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if entry.Type()&os.ModeSymlink != 0 {
			fmt.Printf("symlink\t%s\n", rel)
			found = true
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			fmt.Printf("unsupported-file-type\t%s\n", rel)
			found = true
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		if allowed, exists := exceptions[rel]; exists && allowed == digest {
			return nil
		}
		if err := validateContent(rel, data); err != nil {
			fmt.Printf("binary-content\t%s\n", rel)
			found = true
			return nil
		}
		if scanData(rules, rel, data) {
			found = true
		}
		return nil
	})
	return found, err
}

func loadRules(path string) ([]rule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var result []rule
	for _, raw := range strings.Split(string(data), "\n") {
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		name, expression, ok := strings.Cut(raw, "\t")
		if !ok || !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(name) || expression == "" {
			return nil, fmt.Errorf("invalid rule")
		}
		compiled, err := regexp.Compile(expression)
		if err != nil {
			return nil, err
		}
		result = append(result, rule{name: name, pattern: compiled})
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("empty rule set")
	}
	return result, nil
}

func validateContent(path string, data []byte) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return validatePNG(data)
	case ".ico":
		return validateICO(data)
	default:
		if bytes.IndexByte(data, 0) >= 0 {
			return fmt.Errorf("unsupported binary content")
		}
		return nil
	}
}

func validatePNG(data []byte) error {
	signature := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if len(data) < len(signature) || !bytes.Equal(data[:len(signature)], signature) {
		return fmt.Errorf("invalid PNG signature")
	}
	offset := len(signature)
	seenIHDR := false
	seenIDAT := false
	seenIEND := false
	for offset < len(data) {
		if len(data)-offset < 12 {
			return fmt.Errorf("truncated PNG chunk")
		}
		length := uint64(binary.BigEndian.Uint32(data[offset : offset+4]))
		chunkEnd := uint64(offset) + 12 + length
		if chunkEnd > uint64(len(data)) {
			return fmt.Errorf("truncated PNG payload")
		}
		chunkType := data[offset+4 : offset+8]
		payloadEnd := offset + 8 + int(length)
		wantCRC := binary.BigEndian.Uint32(data[payloadEnd : payloadEnd+4])
		if crc32.ChecksumIEEE(data[offset+4:payloadEnd]) != wantCRC {
			return fmt.Errorf("invalid PNG CRC")
		}
		switch string(chunkType) {
		case "IHDR":
			if seenIHDR || offset != len(signature) || length != 13 {
				return fmt.Errorf("invalid PNG IHDR")
			}
			seenIHDR = true
		case "IDAT":
			if !seenIHDR || seenIEND {
				return fmt.Errorf("invalid PNG IDAT order")
			}
			seenIDAT = true
		case "IEND":
			if !seenIHDR || !seenIDAT || seenIEND || length != 0 || int(chunkEnd) != len(data) {
				return fmt.Errorf("invalid PNG IEND")
			}
			seenIEND = true
		}
		offset = int(chunkEnd)
	}
	if !seenIHDR || !seenIDAT || !seenIEND {
		return fmt.Errorf("incomplete PNG")
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("invalid PNG image: %w", err)
	}
	return nil
}

func validateICO(data []byte) error {
	if len(data) < 6 || binary.LittleEndian.Uint16(data[0:2]) != 0 || binary.LittleEndian.Uint16(data[2:4]) != 1 {
		return fmt.Errorf("invalid ICO header")
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count == 0 || count > (len(data)-6)/16 {
		return fmt.Errorf("invalid ICO directory")
	}
	directoryEnd := 6 + count*16
	maxEnd := directoryEnd
	for index := 0; index < count; index++ {
		entry := data[6+index*16 : 6+(index+1)*16]
		size := uint64(binary.LittleEndian.Uint32(entry[8:12]))
		offset := uint64(binary.LittleEndian.Uint32(entry[12:16]))
		end := offset + size
		if size == 0 || offset < uint64(directoryEnd) || end < offset || end > uint64(len(data)) {
			return fmt.Errorf("invalid ICO image bounds")
		}
		resource := data[int(offset):int(end)]
		if bytes.HasPrefix(resource, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
			if err := validatePNG(resource); err != nil {
				return err
			}
		} else if err := validateDIB(resource); err != nil {
			return err
		}
		if int(end) > maxEnd {
			maxEnd = int(end)
		}
	}
	if maxEnd != len(data) {
		return fmt.Errorf("unreferenced ICO trailing bytes")
	}
	return nil
}

func validateDIB(data []byte) error {
	if len(data) < 40 {
		return fmt.Errorf("truncated ICO DIB")
	}
	headerSize := int(binary.LittleEndian.Uint32(data[0:4]))
	width := int32(binary.LittleEndian.Uint32(data[4:8]))
	doubledHeight := int32(binary.LittleEndian.Uint32(data[8:12]))
	planes := binary.LittleEndian.Uint16(data[12:14])
	bitsPerPixel := binary.LittleEndian.Uint16(data[14:16])
	compression := binary.LittleEndian.Uint32(data[16:20])
	if headerSize < 40 || headerSize > len(data) || width <= 0 || doubledHeight <= 0 || doubledHeight%2 != 0 || planes != 1 {
		return fmt.Errorf("invalid ICO DIB header")
	}
	if bitsPerPixel != 1 && bitsPerPixel != 4 && bitsPerPixel != 8 && bitsPerPixel != 24 && bitsPerPixel != 32 {
		return fmt.Errorf("unsupported ICO bit depth")
	}
	if compression != 0 && compression != 3 {
		return fmt.Errorf("unsupported ICO compression")
	}
	height := uint64(doubledHeight / 2)
	rowBits := uint64(width) * uint64(bitsPerPixel)
	xorBytes := ((rowBits + 31) / 32) * 4 * height
	maskBytes := ((uint64(width) + 31) / 32) * 4 * height
	minimum := uint64(headerSize) + xorBytes + maskBytes
	if minimum > uint64(len(data)) {
		return fmt.Errorf("truncated ICO DIB pixels")
	}
	return nil
}
