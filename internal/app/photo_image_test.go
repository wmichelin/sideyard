package app

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func photoFixture(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			img.Set(x, y, color.NRGBA{R: uint8((y*3 + x + 1) * 30), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPhotoOrientationAndMetadata(t *testing.T) {
	src, _, err := image.Decode(bytes.NewReader(photoFixture(t)))
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"ABCDEF", "CBAFED", "FEDCBA", "DEFABC", "ADBECF", "DAEBFC", "FCEBDA", "CFBEAD"} {
		got := orientPhoto(src, i+1)
		var order string
		for y := 0; y < got.Bounds().Dy(); y++ {
			for x := 0; x < got.Bounds().Dx(); x++ {
				r, _, _, _ := got.At(x, y).RGBA()
				order += string(rune('A' + int(r>>8)/30 - 1))
			}
		}
		if order != want {
			t.Fatalf("orientation %d: %s want %s", i+1, order, want)
		}
	}
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, src, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	for _, endian := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		tiff := make([]byte, 26)
		if endian == binary.LittleEndian {
			copy(tiff, "II")
		} else {
			copy(tiff, "MM")
		}
		endian.PutUint16(tiff[2:], 42)
		endian.PutUint32(tiff[4:], 8)
		endian.PutUint16(tiff[8:], 1)
		endian.PutUint16(tiff[10:], 0x0112)
		endian.PutUint16(tiff[12:], 3)
		endian.PutUint32(tiff[14:], 1)
		endian.PutUint16(tiff[18:], 6)
		segment := append([]byte("Exif\x00\x00"), tiff...)
		segment = append(segment, []byte("GPS private location")...)
		header := []byte{0xff, 0xe1, 0, 0}
		binary.BigEndian.PutUint16(header[2:], uint16(len(segment)+2))
		data := append(append(append([]byte{}, jpg.Bytes()[:2]...), header...), segment...)
		data = append(data, jpg.Bytes()[2:]...)
		photo, err := normalizePhoto(data)
		if err != nil {
			t.Fatal(err)
		}
		config, _, err := image.DecodeConfig(bytes.NewReader(photo.Data))
		if err != nil || config.Width != 2 || config.Height != 3 || bytes.Contains(photo.Data, []byte("Exif")) || bytes.Contains(photo.Data, []byte("GPS")) {
			t.Fatal("orientation or metadata stripping failed")
		}
		endian.PutUint32(data[16:20], 0xffffffff)
		if _, err := normalizePhoto(data); err == nil {
			t.Fatal("malformed EXIF offset accepted")
		}
	}
	data := photoFixture(t)
	text := []byte("GPS\x00private location")
	chunk := make([]byte, 12+len(text))
	binary.BigEndian.PutUint32(chunk, uint32(len(text)))
	copy(chunk[4:], "tEXt")
	copy(chunk[8:], text)
	binary.BigEndian.PutUint32(chunk[len(chunk)-4:], crc32.ChecksumIEEE(chunk[4:len(chunk)-4]))
	withText := append(append(append([]byte{}, data[:33]...), chunk...), data[33:]...)
	photo, err := normalizePhoto(withText)
	if err != nil || bytes.Contains(photo.Data, []byte("private location")) {
		t.Fatal("PNG metadata retained")
	}
}

func TestPhotoImageRejectsUnboundedOrInvalidInputs(t *testing.T) {
	largeDimensions := append([]byte{}, photoFixture(t)...)
	binary.BigEndian.PutUint32(largeDimensions[16:20], 7000)
	binary.BigEndian.PutUint32(largeDimensions[29:33], crc32.ChecksumIEEE(largeDimensions[12:29]))
	for _, data := range [][]byte{nil, []byte("<svg onload='alert(1)'/>"), []byte("ftypheic"), photoFixture(t)[:40], bytes.Repeat([]byte{0}, photoUploadLimit+1), largeDimensions} {
		if _, err := normalizePhoto(data); err == nil {
			t.Fatal("invalid image accepted")
		}
	}
	photo, err := normalizePhoto(photoFixture(t))
	if err != nil || photo.ContentType != "image/png" {
		t.Fatal("valid PNG rejected")
	}
}
