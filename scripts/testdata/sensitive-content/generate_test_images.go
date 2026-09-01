package main

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) != 3 {
		panic("usage: generate_test_images <output-dir> <fixture>")
	}
	fixtureData, err := os.ReadFile(os.Args[2])
	if err != nil {
		panic(err)
	}
	secret := strings.SplitN(string(fixtureData), "\n", 2)[0]
	if secret == "" {
		panic("fixture first line is empty")
	}
	if err := os.MkdirAll(os.Args[1], 0o755); err != nil {
		panic(err)
	}

	safePNG := makePNG()
	mustWrite(filepath.Join(os.Args[1], "safe.png"), safePNG)
	mustWrite(filepath.Join(os.Args[1], "sensitive.png"), insertPNGText(safePNG, secret))
	mustWrite(filepath.Join(os.Args[1], "safe.ico"), makeICO(""))
	mustWrite(filepath.Join(os.Args[1], "sensitive.ico"), makeICO(secret))
}

func makePNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff})
	var output bytes.Buffer
	if err := png.Encode(&output, img); err != nil {
		panic(err)
	}
	return output.Bytes()
}

func insertPNGText(input []byte, text string) []byte {
	iend := bytes.LastIndex(input, []byte{'I', 'E', 'N', 'D'})
	if iend < 4 {
		panic("encoded PNG lacks IEND")
	}
	chunkStart := iend - 4
	payload := append([]byte("Comment\x00"), []byte(text)...)
	var chunk bytes.Buffer
	_ = binary.Write(&chunk, binary.BigEndian, uint32(len(payload)))
	chunk.WriteString("tEXt")
	chunk.Write(payload)
	crcInput := append([]byte("tEXt"), payload...)
	_ = binary.Write(&chunk, binary.BigEndian, crc32.ChecksumIEEE(crcInput))
	result := append([]byte(nil), input[:chunkStart]...)
	result = append(result, chunk.Bytes()...)
	result = append(result, input[chunkStart:]...)
	return result
}

func makeICO(text string) []byte {
	const width = 64
	const height = 1
	pixels := make([]byte, width*height*4)
	copy(pixels, text)
	andMask := make([]byte, ((width+31)/32)*4*height)
	imageData := make([]byte, 40+len(pixels)+len(andMask))
	binary.LittleEndian.PutUint32(imageData[0:4], 40)
	binary.LittleEndian.PutUint32(imageData[4:8], width)
	binary.LittleEndian.PutUint32(imageData[8:12], height*2)
	binary.LittleEndian.PutUint16(imageData[12:14], 1)
	binary.LittleEndian.PutUint16(imageData[14:16], 32)
	binary.LittleEndian.PutUint32(imageData[20:24], uint32(len(pixels)))
	copy(imageData[40:], pixels)

	result := make([]byte, 6+16+len(imageData))
	binary.LittleEndian.PutUint16(result[2:4], 1)
	binary.LittleEndian.PutUint16(result[4:6], 1)
	result[6] = width
	result[7] = height
	binary.LittleEndian.PutUint16(result[10:12], 1)
	binary.LittleEndian.PutUint16(result[12:14], 32)
	binary.LittleEndian.PutUint32(result[14:18], uint32(len(imageData)))
	binary.LittleEndian.PutUint32(result[18:22], 22)
	copy(result[22:], imageData)
	return result
}

func mustWrite(path string, data []byte) {
	if err := os.WriteFile(path, data, 0o644); err != nil {
		panic(err)
	}
}
