package mem

import (
	"fmt"
	"sync"
	"time"

	"github.com/knadh/niltalk/store"
)

// Config represents the InMemory store config structure.
type Config struct {
	PrefixRoom string `koanf:"prefix_room"`
}

// InMemory represents the in-memory implementation of the Store interface.
type InMemory struct {
	cfg   *Config
	rooms map[string]*room
	mu    sync.Mutex
}

type room struct {
	store.Room
	Sessions map[string]string
	Expire   time.Time
}

// New returns a new Redis store.
func New(cfg Config) (*InMemory, error) {
	store := &InMemory{
		cfg:   &cfg,
		rooms: map[string]*room{},
	}
	go store.watch()
	return store, nil
}

// watch the store to clean it up.
func (m *InMemory) watch() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		m.cleanup()
	}
}

// cleanup the store to removes expired items.
func (m *InMemory) cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()

	for id, r := range m.rooms {
		if r.Expire.Before(now) {
			delete(m.rooms, id)
			continue
		}
	}
}

// AddRoom adds a room to the store.
func (m *InMemory) AddRoom(r store.Room, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf(m.cfg.PrefixRoom, r.ID)
	m.rooms[key] = &room{
		Room:     r,
		Expire:   r.CreatedAt.Add(ttl),
		Sessions: map[string]string{},
	}

	return nil
}

// ExtendRoomTTL extends a room's TTL.
func (m *InMemory) ExtendRoomTTL(id string, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf(m.cfg.PrefixRoom, id)
	room, ok := m.rooms[key]
	if !ok {
		return store.ErrRoomNotFound
	}

	room.Expire = room.Expire.Add(ttl)
	m.rooms[key] = room
	return nil
}

// GetRoom gets a room from the store.
func (m *InMemory) GetRoom(id string) (store.Room, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf(m.cfg.PrefixRoom, id)
	out, ok := m.rooms[key]

	if !ok {
		return out.Room, store.ErrRoomNotFound
	}
	return out.Room, nil
}

// RoomExists checks if a room exists in the store.
func (m *InMemory) RoomExists(id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf(m.cfg.PrefixRoom, id)
	_, ok := m.rooms[key]

	return ok, nil
}

// RemoveRoom deletes a room from the store.
func (m *InMemory) RemoveRoom(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf(m.cfg.PrefixRoom, id)
	delete(m.rooms, key)

	return nil
}

// AddSession adds a sessionID room to the store.
func (m *InMemory) AddSession(sessID, handle, roomID string, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf(m.cfg.PrefixRoom, roomID)
	room, ok := m.rooms[key]

	if !ok {
		return store.ErrRoomNotFound
	}

	room.Sessions[sessID] = handle
	m.rooms[key] = room

	return nil
}

// GetSession retrieves a peer session from the store.
func (m *InMemory) GetSession(sessID, roomID string) (store.Sess, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf(m.cfg.PrefixRoom, roomID)
	room, ok := m.rooms[key]

	if !ok {
		return store.Sess{}, store.ErrRoomNotFound
	}

	handle, ok := room.Sessions[sessID]

	if !ok {
		return store.Sess{}, nil
	}

	return store.Sess{
		ID:     sessID,
		Handle: handle,
	}, nil
}

// RemoveSession deletes a session ID from a room.
func (m *InMemory) RemoveSession(sessID, roomID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf(m.cfg.PrefixRoom, roomID)
	room, ok := m.rooms[key]

	if !ok {
		return store.ErrRoomNotFound
	}

	delete(room.Sessions, sessID)
	m.rooms[key] = room

	return nil
}

// ClearSessions deletes all the sessions in a room.
func (m *InMemory) ClearSessions(roomID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf(m.cfg.PrefixRoom, roomID)
	room, ok := m.rooms[key]

	if !ok {
		return store.ErrRoomNotFound
	}

	room.Sessions = map[string]string{}

	m.rooms[key] = room

	return nil
}
