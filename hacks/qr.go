package hacks

import (
	qr "github.com/skip2/go-qrcode"
)

//GenerateQR generates QR code with url encoded
func GenerateQR(content string) ([]byte, error) {
	data, err := qr.Encode(content, qr.Medium, 256)
	if err != nil {
		return nil, err
	}
	return data, nil
}
