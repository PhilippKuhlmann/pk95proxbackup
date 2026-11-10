package fake

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
)

// Telegram simulates the Bot API for the demo: one person and one group wrote to the bot,
// messages are logged instead of sent. Every well-formed token is accepted.
func Telegram() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		switch method {
		case "getUpdates":
			_, _ = io.WriteString(w, `{"ok":true,"result":[
				{"update_id":1,"message":{"chat":{"id":184467001,"type":"private","first_name":"Max","last_name":"Admin","username":"max_admin"}}},
				{"update_id":2,"my_chat_member":{"chat":{"id":-1002233445566,"type":"supergroup","title":"Backup Kunde Müller"}}}]}`)
		case "sendMessage", "sendPhoto", "sendDocument":
			log.Printf("[demo] Telegram %s", method)
			_, _ = io.WriteString(w, `{"ok":true,"result":{}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"ok":false,"description":"Not Found: method not found"}`)
		}
	}))
}
