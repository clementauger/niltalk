package main

import (
	rice "github.com/GeertJohan/go.rice"
	"github.com/knadh/niltalk/internal/hub"
	"github.com/knadh/niltalk/internal/notify"
)

// loadPredefinedRooms loads into curet hub the given list of predefind rooms.
// It must be called before starting the app and is not safe for concurrent use.
func (a *App) loadPredefinedRooms(assetBox *rice.Box) error {
	rooms := a.cfg.Rooms
	localURL := "http://" + a.localAddress
	for _, room := range rooms {
		r, err := a.hub.AddPredefinedRoom(room.ID, room.Name, room.Password)
		if err != nil {
			a.logger.Printf("error creating a predefined room %q: %v", room.Name, err)
			continue
		}
		r.PredefinedUsers = make([]hub.PredefinedUser, len(room.Users), len(room.Users))
		copy(r.PredefinedUsers, room.Users)
		var growl bool
		for _, u := range r.PredefinedUsers {
			if u.Growl {
				growl = true
				break
			}
		}
		if growl {
			n := notify.New(room.Growl, localURL, r.ID, a.logger, assetBox)
			if err = n.Init(); err != nil {
				a.logger.Printf("error setting up growl notifications for the predefined room %q: %v", room.Name, err)
				continue
			}
			r.GrowlHandler = n.OnGrowlMessage
		}
		_, err = a.hub.ActivateRoom(r.ID)
		if err != nil {
			a.logger.Printf("error activating a predefined room %q: %v", room.Name, err)
			continue
		}
	}
	return nil
}
