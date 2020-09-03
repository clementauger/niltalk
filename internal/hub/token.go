package hub

import (
	"sync"
	"time"
)

// loginToken handles login via temporary tokens passed in url.
type loginToken struct {
	handle  string
	token   string
	expires time.Time
}

type tokenStore struct {
	m      sync.Mutex
	tokens map[string]loginToken
}

func newTokenStore() *tokenStore {
	return &tokenStore{
		tokens: make(map[string]loginToken),
	}
}

func (t *tokenStore) getOrCreateToken(handle string) string {
	t.m.Lock()
	defer t.m.Unlock()
	tok, ok := t.tokens[handle]
	if ok && !tok.expires.Before(time.Now()) {
		tok.expires = tok.expires.Add(time.Minute * 10)
		t.tokens[handle] = tok
		return tok.token
	}
	uid, _ := GenerateGUID(32)
	tok = loginToken{
		expires: time.Now().Add(time.Minute * 10),
		handle:  handle,
		token:   uid,
	}
	t.tokens[handle] = tok
	return tok.token
}

func (t *tokenStore) checkToken(tokvalue string) string {
	t.m.Lock()
	defer t.m.Unlock()
	for _, tok := range t.tokens {
		if tok.token == tokvalue && !tok.expires.Before(time.Now()) {
			delete(t.tokens, tok.handle)
			return tok.handle
		}
	}
	return ""
}
