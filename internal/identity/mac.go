package identity

import (
	"fmt"
	"net"
	"strings"
)

func ClientID(value string) (string, error) {
	hw, err := net.ParseMAC(strings.TrimSpace(value))
	if err != nil || len(hw) != 6 {
		return "", fmt.Errorf("invalid 48-bit MAC address")
	}
	return "mac:" + strings.ToLower(hw.String()), nil
}
