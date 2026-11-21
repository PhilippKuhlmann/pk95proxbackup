package notify

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Webhook errors land in the event log every account reads: no token, no URL.
func TestHookErrorHidesSecrets(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, kind := range []string{"gotify", "ntfy", "webhook"} {
		s := Settings{HookEnabled: true, HookKind: kind, HookURL: "http://127.0.0.1:1/geheim-pfad-123/message", HookToken: "Token+Geheim/42"}
		err := sendHook(ctx, s, Message{Title: "t", Severity: "info"})
		if err == nil {
			t.Fatalf("%s: no error", kind)
		}
		for _, secret := range []string{"Token+Geheim/42", "Token%2BGeheim%2F42", "geheim-pfad-123"} {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("%s: error shows %q: %v", kind, secret, err)
			}
		}
	}
}
