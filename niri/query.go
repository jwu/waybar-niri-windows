package niri

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"slices"
)

// Query sends a single request to niri and returns the "Ok" payload.
//
// Unlike Init it does not subscribe to the event stream; it is meant for
// one-shot snapshots, e.g. rendering a static image from a script. For a live
// view, use Init and the returned State.
func Query(request any) (json.RawMessage, error) {
	addr := os.Getenv("NIRI_SOCKET")
	if addr == "" {
		return nil, fmt.Errorf("NIRI_SOCKET not set")
	}

	conn, err := net.Dial("unix", addr)
	if err != nil {
		return nil, fmt.Errorf("connecting to NIRI_SOCKET: %w", err)
	}
	defer conn.Close()

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return nil, fmt.Errorf("writing request: %w", err)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	var envelope struct {
		Ok  json.RawMessage `json:"Ok"`
		Err string          `json:"Err"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return nil, fmt.Errorf("unmarshaling response: %w", err)
	}
	if envelope.Err != "" {
		return nil, fmt.Errorf("%s", envelope.Err)
	}
	return envelope.Ok, nil
}

func queryInto(request any, out any) error {
	raw, err := Query(request)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return fmt.Errorf("empty response for %v", request)
	}
	return json.Unmarshal(raw, out)
}

// Snapshot is a one-shot view of the compositor: every workspace with its
// windows, all outputs, and the focused output's name.
type Snapshot struct {
	Views         []WorkspaceView
	Outputs       map[string]Output
	FocusedOutput string
}

// QuerySnapshot fetches Workspaces, Windows, Outputs and FocusedOutput in one
// go and groups them into per-workspace views.
func QuerySnapshot() (*Snapshot, error) {
	var workspaces struct {
		Workspaces []*Workspace `json:"Workspaces"`
	}
	if err := queryInto("Workspaces", &workspaces); err != nil {
		return nil, err
	}

	var windows struct {
		Windows []*Window `json:"Windows"`
	}
	if err := queryInto("Windows", &windows); err != nil {
		return nil, err
	}

	var outputs struct {
		Outputs map[string]Output `json:"Outputs"`
	}
	if err := queryInto("Outputs", &outputs); err != nil {
		return nil, err
	}

	snapshot := &Snapshot{Outputs: outputs.Outputs}
	snapshot.Views = GroupWindows(workspaces.Workspaces, windows.Windows)

	// Best effort: an old niri may not answer this one.
	var focused struct {
		FocusedOutput *Output `json:"FocusedOutput"`
	}
	if err := queryInto("FocusedOutput", &focused); err == nil && focused.FocusedOutput != nil {
		snapshot.FocusedOutput = focused.FocusedOutput.Name
	}

	return snapshot, nil
}

// Output returns the named output, falling back to the focused output and then
// to the first output in name order. The bool is false when there are no
// outputs at all.
func (s *Snapshot) Output(name string) (Output, bool) {
	if name == "" {
		name = s.FocusedOutput
	}
	if out, ok := s.Outputs[name]; ok {
		return out, true
	}

	names := make([]string, 0, len(s.Outputs))
	for n := range s.Outputs {
		names = append(names, n)
	}
	slices.Sort(names)
	if len(names) == 0 {
		return Output{}, false
	}
	return s.Outputs[names[0]], true
}
