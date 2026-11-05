package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"kero/internal/proto"
)

// handoffVersion is the version of the state a kerod hands to the kerod
// that follows it. A newer kerod reads the versions before it; fields are
// added, never renamed or removed, so that an update can always read what
// the install it replaces wrote.
const handoffVersion = 1

// handoffState is what a kerod writes before execing the next one: the
// panes' terminals and programs, which the next one adopts, beside the
// state.json that holds the projects and sessions.
type handoffState struct {
	Version int           `json:"version"`
	Panes   []handoffPane `json:"panes"`
}

// handoffPane is a pane across a handoff: its terminal's screen, its
// program's PID and the fd of its PTY master, which exec keeps open.
type handoffPane struct {
	ID       string     `json:"id"`
	Info     proto.Pane `json:"info"`
	PID      int        `json:"pid,omitempty"`
	FD       int        `json:"fd,omitempty"`
	Snapshot string     `json:"snapshot,omitempty"`
	Exited   bool       `json:"exited,omitempty"`
	Code     int        `json:"code,omitempty"`
	Awaiting bool       `json:"awaiting,omitempty"`
}

func (d *Daemon) handoffPath() string { return filepath.Join(d.dir, "handoff.json") }
func (d *Daemon) handoffDir() string  { return filepath.Join(d.dir, "handoff") }

// CheckHandoff reads a handoff file for a kerod about to exec it, and
// reports whether it can: the old kerod runs `kero __handoff-check` on the
// new binary before replacing itself.
func CheckHandoff(path string) error {
	st, err := loadHandoff(path)
	if err != nil {
		return err
	}
	if st.Version < 1 || st.Version > handoffVersion {
		return fmt.Errorf("handoff version %d is not one this kero reads", st.Version)
	}
	for _, hp := range st.Panes {
		if hp.Snapshot == "" {
			continue
		}
		if _, err := os.Stat(hp.Snapshot); err != nil {
			return fmt.Errorf("pane %s: %w", hp.ID, err)
		}
	}
	return nil
}

func loadHandoff(path string) (*handoffState, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var st handoffState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("handoff %s: %w", path, err)
	}
	return &st, nil
}

func writeHandoff(path string, st *handoffState) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, b)
}
