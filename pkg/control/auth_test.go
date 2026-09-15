package control

import "testing"

func TestAuthorizeNode(t *testing.T) {
	bound := NewAuthPolicy(true, nil, map[string]string{
		"node-a": "tok-a",
		"node-b": "tok-b",
	})

	if err := bound.AuthorizeNode("node-a", "tok-a"); err != nil {
		t.Errorf("bound token rejected: %v", err)
	}
	// A valid token presented under the wrong identity must be rejected:
	// otherwise any token holder can impersonate (and replace) any node.
	if err := bound.AuthorizeNode("node-b", "tok-a"); err == nil {
		t.Error("token bound to node-a accepted for node-b")
	}
	if err := bound.AuthorizeNode("node-a", "tok-b"); err == nil {
		t.Error("wrong token accepted for node-a")
	}
	if err := bound.AuthorizeNode("node-c", "tok-a"); err == nil {
		t.Error("unknown node accepted")
	}

	legacy := NewAuthPolicy(true, []string{"shared"}, nil)
	if err := legacy.AuthorizeNode("anything", "shared"); err != nil {
		t.Errorf("legacy shared token rejected: %v", err)
	}
	if err := legacy.AuthorizeNode("anything", "other"); err == nil {
		t.Error("legacy policy accepted wrong token")
	}

	disabled := NewAuthPolicy(false, nil, nil)
	if err := disabled.AuthorizeNode("whatever", ""); err != nil {
		t.Errorf("disabled policy rejected: %v", err)
	}
}
