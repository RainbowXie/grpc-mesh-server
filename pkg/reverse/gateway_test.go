package reverse

import (
	"context"
	"testing"

	"github.com/grpc-mesh/grpc-mesh-server/pkg/registry"
	"go.uber.org/zap"
)

// Spec scenario: dialing an unregistered peer must fail without creating a
// connection.
func TestDialUnknownPeer(t *testing.T) {
	g := NewGateway(registry.New(zap.NewNop()), zap.NewNop())

	if _, err := g.Dial(context.Background(), registry.PeerID("no-such-node")); err == nil {
		t.Fatal("dialing an unregistered peer must return an error")
	}
}
