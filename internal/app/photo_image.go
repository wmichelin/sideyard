package app

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
)

const photoUploadLimit = 5 << 20
const photoStoredLimit = 4 << 20
const photoShopBytes = 50 << 20
const photoShopCount = 100

var errPhotoImage = errors.New("Choose a JPEG or PNG up to 5 MB and 12 megapixels (maximum 6,000 pixels on either side). Export HEIC photos as JPEG or PNG first.")
var errPhotoOutput = errors.New("This photo is too large after processing. Export a smaller image and try again.")

type photoBuffer struct{ bytes.Buffer }

func (b *photoBuffer) Write(p []byte) (int, error) {
	if len(p) > photoStoredLimit-b.Len() {
		return 0, errPhotoOutput
	}
	return b.Buffer.Write(p)
}

type normalizedPhoto struct {
	Data        []byte
	ContentType string
}

// Decode before re-encoding: original filenames, EXIF/GPS, comments and other
// ancillary metadata never enter storage. Orientation is applied to the pixels.
func normalizePhoto(data []byte) (normalizedPhoto, error) {
	if len(data) == 0 || len(data) > photoUploadLimit {
		return normalizedPhoto{}, errPhotoImage
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	// Allow common 4032 x 3024 phone photos marketed as 12 megapixels.
	if err != nil || (format != "jpeg" && format != "png") || config.Width < 1 || config.Height < 1 || config.Width > 6000 || config.Height > 6000 || int64(config.Width)*int64(config.Height) > 12*1024*1024 {
		return normalizedPhoto{}, errPhotoImage
	}
	orientation := 1
	if format == "jpeg" {
		orientation, err = jpegOrientation(data)
		if err != nil {
			return normalizedPhoto{}, errPhotoImage
		}
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return normalizedPhoto{}, errPhotoImage
	}
	if orientation != 1 {
		decoded = orientPhoto(decoded, orientation)
	}
	var out photoBuffer
	contentType := "image/png"
	if format == "jpeg" {
		contentType = "image/jpeg"
		err = jpeg.Encode(&out, decoded, &jpeg.Options{Quality: 85})
	} else {
		err = png.Encode(&out, decoded)
	}
	if err != nil {
		if errors.Is(err, errPhotoOutput) {
			return normalizedPhoto{}, errPhotoOutput
		}
		return normalizedPhoto{}, errPhotoImage
	}
	return normalizedPhoto{Data: out.Bytes(), ContentType: contentType}, nil
}

func orientPhoto(src image.Image, orientation int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	outW, outH := w, h
	if orientation >= 5 {
		outW, outH = h, w
	}
	dst := image.NewNRGBA(image.Rect(0, 0, outW, outH))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := x, y
			switch orientation {
			case 2:
				dx = w - 1 - x
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dy = h - 1 - y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// Only the bounded TIFF orientation entry is interpreted; the decoder handles
// image validity. Malformed EXIF is rejected instead of trusting unchecked offsets.
func jpegOrientation(data []byte) (int, error) {
	for pos := 2; pos+4 <= len(data); {
		if data[pos] != 0xff {
			return 0, errPhotoImage
		}
		for pos < len(data) && data[pos] == 0xff {
			pos++
		}
		if pos >= len(data) {
			return 0, errPhotoImage
		}
		marker := data[pos]
		pos++
		if marker == 0xda || marker == 0xd9 {
			break
		}
		if pos+2 > len(data) {
			return 0, errPhotoImage
		}
		size := int(binary.BigEndian.Uint16(data[pos : pos+2]))
		if size < 2 || size > len(data)-pos {
			return 0, errPhotoImage
		}
		segment := data[pos+2 : pos+size]
		pos += size
		if marker != 0xe1 || !bytes.HasPrefix(segment, []byte("Exif\x00\x00")) {
			continue
		}
		tiff := segment[6:]
		if len(tiff) < 8 {
			return 0, errPhotoImage
		}
		var order binary.ByteOrder
		switch string(tiff[:2]) {
		case "II":
			order = binary.LittleEndian
		case "MM":
			order = binary.BigEndian
		default:
			return 0, errPhotoImage
		}
		if order.Uint16(tiff[2:4]) != 42 {
			return 0, errPhotoImage
		}
		offset := uint64(order.Uint32(tiff[4:8]))
		if offset > uint64(len(tiff)-2) {
			return 0, errPhotoImage
		}
		count := int(order.Uint16(tiff[offset : offset+2]))
		start := int(offset) + 2
		if count > (len(tiff)-start)/12 {
			return 0, errPhotoImage
		}
		for i := 0; i < count; i++ {
			entry := tiff[start+i*12 : start+(i+1)*12]
			if order.Uint16(entry[:2]) != 0x0112 {
				continue
			}
			if order.Uint16(entry[2:4]) != 3 || order.Uint32(entry[4:8]) != 1 {
				return 0, errPhotoImage
			}
			orientation := int(order.Uint16(entry[8:10]))
			if orientation < 1 || orientation > 8 {
				return 0, errPhotoImage
			}
			return orientation, nil
		}
	}
	return 1, nil
}
