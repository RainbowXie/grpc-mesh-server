package registry

import (
	"testing"

	"github.com/grpc-mesh/grpc-mesh-server/pkg/control"
)

func TestSessionMethods(t *testing.T) {
	state := &SessionState{
		Handshake: &control.Handshake{
			NodeID:  "node-a",
			Version: "1.0.0",
			Metadata: map[string]string{
				"mesh.methods": " calculator.v1.Calculator/Add , calculator.v1.Calculator/Health ,",
			},
		},
	}
	got := state.Methods()
	want := []string{"calculator.v1.Calculator/Add", "calculator.v1.Calculator/Health"}
	if len(got) != len(want) {
		t.Fatalf("Methods() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Methods()[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	empty := &SessionState{Handshake: &control.Handshake{NodeID: "node-b"}}
	if m := empty.Methods(); len(m) != 0 {
		t.Fatalf("Methods() without metadata = %v, want empty", m)
	}
	if m := (&SessionState{}).Methods(); len(m) != 0 {
		t.Fatalf("Methods() without handshake = %v, want empty", m)
	}
}
