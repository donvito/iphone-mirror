package settings

import "os"

// defaultName is the computer name.
func defaultName() string {
	host, _ := os.Hostname()
	if host == "" {
		host = "iPhone Mirror"
	}
	return host
}
