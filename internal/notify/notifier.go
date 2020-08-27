package notify

import (
	"bytes"
	"fmt"
	"html/template"
	"log"

	"github.com/gen2brain/beeep"
	"github.com/knadh/niltalk/internal/hub"
)

type Notifier struct {
	Enabler string
	Title   string
	Message string
	Icon    string
	BaseURL string
	RoomID  string
	Logger  *log.Logger
}

func (n Notifier) OnPeerMessage(msg string, p *hub.Peer) {
	if msg != n.Enabler {
		return
	}
	body := n.Message
	t, err := template.New("").Parse(n.Message)
	if err != nil {
		if n.Logger != nil {
			n.Logger.Printf("error compiling growl template for room %q: %v", n.RoomID, err)
		}
	} else {
		var s bytes.Buffer
		err := t.Execute(&s, map[string]interface{}{
			"URL":      fmt.Sprintf("%v/r/%v", n.BaseURL, n.RoomID),
			"UserName": p.Handle,
		})
		if err != nil {
			n.Logger.Printf("error executing growl template for room %q: %v", n.RoomID, err)
		} else {
			body = s.String()
		}
	}
	err = beeep.Notify(n.Title, body, "")
	if err != nil {
		n.Logger.Printf("error sending notification for room %q: %v", n.RoomID, err)
	}
}
