package settings

import (
	"os"
	"os/exec"
	"strings"
	"sync"
)

// defaultName is "<Computer Name> Mirror". The plain computer name is already
// used by macOS's own AirPlay Receiver, so the iPhone would show two identical entries.
var defaultName = sync.OnceValue(func() string {
	out, _ := exec.Command("/usr/sbin/scutil", "--get", "ComputerName").Output()
	host := strings.TrimSpace(string(out))
	if host == "" {
		h, _ := os.Hostname()
		host, _, _ = strings.Cut(h, ".")
	}
	if host == "" {
		return "iPhone Mirror"
	}
	return host + " Mirror"
})
