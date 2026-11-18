package prox

import (
	"net/http"
	"testing"
)

func TestAPIMessage(t *testing.T) {
	resp := &http.Response{StatusCode: 400, Status: "400 Bad Request"}
	for _, c := range []struct{ raw, want string }{
		// PBS: reason as plain text in the body
		{"parameter verification failed - 'gc-schedule': unable to parse calendar event at ''", "parameter verification failed - 'gc-schedule': unable to parse calendar event at ''"},
		// JSON message
		{`{"data":null,"message":"no such datastore"}`, "no such datastore"},
		// PVE: JSON without message, reason in the status line
		{`{"data":null}`, "Bad Request"},
		// an HTML error page of a proxy is not shown
		{"<html><body>bad</body></html>", "Bad Request"},
	} {
		if got := apiMessage(resp, []byte(c.raw)); got != c.want {
			t.Errorf("%q: got %q, want %q", c.raw, got, c.want)
		}
	}
}
