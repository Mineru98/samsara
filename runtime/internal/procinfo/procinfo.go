// Package procinfo records process identity beyond the bare PID: boot id,
// start time and owner, so PID reuse is never mistaken for the original
// process (01 §15.1).
package procinfo

import (
	"bytes"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// Identity is a comparable process identity.
type Identity struct {
	PID       int    `json:"pid"`
	StartTime string `json:"start_time"`
	BootID    string `json:"boot_id,omitempty"`
	UID       int    `json:"uid"`
}

// Map renders the identity as string fields.
func (i Identity) Map() map[string]string {
	return map[string]string{"pid": strconv.Itoa(i.PID), "start_time": i.StartTime, "boot_id": i.BootID, "uid": strconv.Itoa(i.UID)}
}

// FromMap parses Map output.
func FromMap(m map[string]string) (Identity, bool) {
	pid, err := strconv.Atoi(m["pid"])
	if err != nil || pid <= 0 {
		return Identity{}, false
	}
	uid, _ := strconv.Atoi(m["uid"])
	return Identity{PID: pid, StartTime: m["start_time"], BootID: m["boot_id"], UID: uid}, true
}

// BootID returns the host boot identity where available.
func BootID() string {
	if b, err := os.ReadFile("/proc/sys/kernel/random/boot_id"); err == nil {
		return strings.TrimSpace(string(b))
	}
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("sysctl", "-n", "kern.boottime").Output(); err == nil {
			return strings.TrimSpace(string(out))
		}
	}
	return ""
}

// Of returns the identity of a live pid, or ok=false.
func Of(pid int) (Identity, bool) {
	if pid <= 0 {
		return Identity{}, false
	}
	st := startTime(pid)
	if st == "" {
		return Identity{}, false
	}
	id := Identity{PID: pid, StartTime: st, BootID: BootID(), UID: -1}
	if fi, err := os.Stat("/proc/" + strconv.Itoa(pid)); err == nil {
		if s, ok := fi.Sys().(*syscall.Stat_t); ok {
			id.UID = int(s.Uid)
		}
	} else if out, err := exec.Command("ps", "-o", "uid=", "-p", strconv.Itoa(pid)).Output(); err == nil {
		id.UID, _ = strconv.Atoi(strings.TrimSpace(string(out)))
	}
	return id, true
}

// Status of an identity check.
type Status int

const (
	Gone    Status = iota // the process is gone or the pid was reused
	Running               // the same process is alive
	Unknown               // cannot tell (other boot id unavailable, permission)
)

// Check compares a recorded identity with the live system.
func Check(id Identity) Status {
	if id.PID <= 0 {
		return Unknown
	}
	if id.BootID != "" {
		if b := BootID(); b != "" && b != id.BootID {
			return Gone
		}
	}
	cur := startTime(id.PID)
	if cur == "" {
		if err := syscall.Kill(id.PID, 0); err == syscall.EPERM {
			return Unknown
		}
		return Gone
	}
	if id.StartTime == "" {
		return Unknown
	}
	if cur != id.StartTime {
		return Gone
	}
	return Running
}

// Parent returns the parent pid.
func Parent(pid int) int {
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		f := statFields(b)
		if len(f) > 1 {
			p, _ := strconv.Atoi(f[1])
			return p
		}
		return 0
	}
	out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}
	p, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return p
}

// Ancestors returns the pid chain from pid's parent upwards (bounded).
func Ancestors(pid int) []int {
	var out []int
	for i := 0; i < 64; i++ {
		pid = Parent(pid)
		if pid <= 1 {
			break
		}
		out = append(out, pid)
	}
	return out
}

func startTime(pid int) string {
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		f := statFields(b)
		// statFields drops pid and comm; starttime is field 22 overall → index 19.
		if len(f) > 19 {
			return f[19]
		}
		return ""
	}
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// statFields returns /proc/<pid>/stat fields after the comm field.
func statFields(b []byte) []string {
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return nil
	}
	return strings.Fields(string(b[i+1:]))
}
