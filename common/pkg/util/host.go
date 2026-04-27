package util

import (
	"fmt"
	"os"
)

func Hostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		fmt.Printf("Error getting hostname: %s. Setting hostname to 'unknown'\n", err.Error())
		hostname = "unknown"
	}
	return hostname
}
