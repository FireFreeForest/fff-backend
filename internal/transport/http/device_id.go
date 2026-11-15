package httpapi

import (
	"crypto/rand"
	"fmt"
)

const (
	deviceIDPrefix   = "FFF-"
	deviceIDLength   = 8
	deviceIDAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

func generateDeviceID() (string, error) {
	buf := make([]byte, deviceIDLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate device id: %w", err)
	}
	for i, b := range buf {
		buf[i] = deviceIDAlphabet[int(b)%len(deviceIDAlphabet)]
	}
	return deviceIDPrefix + string(buf), nil
}
