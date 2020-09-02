package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi"
	"github.com/gorilla/websocket"
	"github.com/knadh/niltalk/internal/hub"
	"github.com/knadh/niltalk/internal/upload"
	"golang.org/x/time/rate"
)

const (
	hasAuth = 1 << iota
	hasRoom
)

type sess struct {
	ID     string
	Handle string
}

// reqCtx is the context injected into every request.
type reqCtx struct {
	app  *App
	room *hub.Room
	sess sess
}

// jsonResp is the envelope for all JSON API responses.
type jsonResp struct {
	Error *string     `json:"error"`
	Data  interface{} `json:"data"`
}

type tplData struct {
	Title       string
	Description string
	Room        interface{}
	Auth        bool
}

type reqRoom struct {
	Name     string `json:"name"`
	Handle   string `json:"handle"`
	Password string `json:"password"`
	UserPwd  string `json:"userpwd"`
}

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool {
	return true
}}

// handleIndex renders the homepage.
func handleIndex(w http.ResponseWriter, r *http.Request) {
	var (
		ctx = r.Context().Value("ctx").(*reqCtx)
		app = ctx.app
	)
	respondHTML("index", tplData{
		Title: app.cfg.Name,
	}, http.StatusOK, w, app)
}

// handleRoomPage renders the chat room page.
func handleRoomPage(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context().Value("ctx").(*reqCtx)
		app  = ctx.app
		room = ctx.room
	)

	if room == nil {
		respondHTML("room-not-found", tplData{}, http.StatusNotFound, w, app)
		return
	}

	out := tplData{
		Title: room.Name,
		Room:  room,
	}
	if ctx.sess.ID != "" {
		out.Auth = true
	}

	// Disable browser caching.
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	respondHTML("room", out, http.StatusOK, w, app)
}

// handleLogin authenticates a peer into a room.
func handleLogin(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context().Value("ctx").(*reqCtx)
		app  = ctx.app
		room = ctx.room
	)

	if room == nil {
		respondJSON(w, nil, errors.New("room is invalid or has expired"), http.StatusBadRequest)
		return
	}

	var req reqRoom
	if err := readJSONReq(r, &req); err != nil {
		respondJSON(w, nil, errors.New("error parsing JSON request"), http.StatusBadRequest)
		return
	}

	if req.Handle == "" {
		h, err := hub.GenerateGUID(8)
		if err != nil {
			app.logger.Printf("error generating uniq handle: %v", err)
			respondJSON(w, nil, errors.New("error generating uniq handle"), http.StatusInternalServerError)
			return
		}
		req.Handle = h
	}

	sessID, err := room.Login(req.Password, req.Handle, req.UserPwd, app.cfg.RoomAge)
	if err == hub.ErrInvalidRoomPassword || err == hub.ErrInvalidUserPassword {
		respondJSON(w, nil, errors.New("incorrect password"), http.StatusForbidden)
		return
	} else if err != nil {
		respondJSON(w, nil, err, http.StatusInternalServerError)
		return
	}

	// Set the session cookie.
	ck := &http.Cookie{Name: app.cfg.SessionCookie, Value: sessID, Path: fmt.Sprintf("/r/%v", room.ID)}
	http.SetCookie(w, ck)
	respondJSON(w, true, nil, http.StatusOK)
}

// handleLogout logs out a peer.
func handleLogout(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context().Value("ctx").(*reqCtx)
		app  = ctx.app
		room = ctx.room
	)

	if room == nil {
		respondJSON(w, nil, errors.New("room is invalid or has expired"), http.StatusBadRequest)
		return
	}

	if err := app.hub.Store.RemoveSession(ctx.sess.ID, room.ID); err != nil {
		app.logger.Printf("error removing session: %v", err)
		respondJSON(w, nil, errors.New("error removing session"), http.StatusInternalServerError)
		return
	}

	// Delete the session cookie.
	ck := &http.Cookie{Name: app.cfg.SessionCookie, Value: "", MaxAge: -1, Path: fmt.Sprintf("/r/%v", room.ID)}
	http.SetCookie(w, ck)
	respondJSON(w, true, nil, http.StatusOK)
}

// handleWS handles incoming connections.
func handleWS(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context().Value("ctx").(*reqCtx)
		app  = ctx.app
		room = ctx.room
	)

	if ctx.sess.ID == "" {
		respondJSON(w, nil, errors.New("invalid session"), http.StatusForbidden)
		return
	}

	// Create the WS connection.
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		app.logger.Printf("Websocket upgrade failed: %s: %v", r.RemoteAddr, err)
		return
	}

	// Create a new peer instance and add to the room.
	room.AddPeer(ctx.sess.ID, ctx.sess.Handle, ws)
}

// respondJSON responds to an HTTP request with a generic payload or an error.
func respondJSON(w http.ResponseWriter, data interface{}, err error, statusCode int) {
	if statusCode == 0 {
		statusCode = http.StatusOK
	}

	w.WriteHeader(statusCode)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	out := jsonResp{Data: data}
	if err != nil {
		e := err.Error()
		out.Error = &e
	}
	b, err := json.Marshal(out)
	if err != nil {
		logger.Printf("error marshalling JSON response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	w.Write(b)
}

// respondHTML responds to an HTTP request with the HTML output of a given template.
func respondHTML(tplName string, data tplData, statusCode int, w http.ResponseWriter, app *App) {
	if statusCode > 0 {
		w.WriteHeader(statusCode)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tpl, err := app.getTpl()
	if err != nil {
		app.logger.Printf("error compiling template %s: %s", tplName, err)
		w.Write([]byte("error compiling template"))
		return
	}
	err = tpl.ExecuteTemplate(w, tplName, struct {
		Config *hub.Config
		Data   tplData
	}{
		Config: app.cfg,
		Data:   data,
	})
	if err != nil {
		app.logger.Printf("error rendering template %s: %s", tplName, err)
		w.Write([]byte("error rendering template"))
	}
}

// handleCreateRoom handles the creation of a new room.
func handleCreateRoom(w http.ResponseWriter, r *http.Request) {
	var (
		ctx = r.Context().Value("ctx").(*reqCtx)
		app = ctx.app
	)

	var req reqRoom
	if err := readJSONReq(r, &req); err != nil {
		respondJSON(w, nil, errors.New("error parsing JSON request"), http.StatusBadRequest)
		return
	}

	if req.Name != "" && (len(req.Name) < 3 || len(req.Name) > 100) {
		respondJSON(w, nil, errors.New("invalid room name (6 - 100 chars)"), http.StatusBadRequest)
		return
	}

	if len(req.Password) < 6 || len(req.Password) > 100 {
		respondJSON(w, nil, errors.New("invalid password (6 - 100 chars)"), http.StatusBadRequest)
		return
	}

	// Create and activate the new room.
	room, err := app.hub.AddRoom(req.Name, req.Password)
	if err != nil {
		respondJSON(w, nil, err, http.StatusInternalServerError)
		return
	}

	respondJSON(w, struct {
		ID string `json:"id"`
	}{room.ID}, nil, http.StatusOK)
}

// wrap is a middleware that handles auth and room check for various HTTP handlers.
// It attaches the app and room contexts to handlers.
func wrap(next http.HandlerFunc, app *App, opts uint8) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var (
			req    = &reqCtx{app: app}
			roomID = chi.URLParam(r, "roomID")
		)

		// Check if the request is authenticated.
		if opts&hasAuth != 0 {
			ck, _ := r.Cookie(app.cfg.SessionCookie)
			if ck != nil && ck.Value != "" {
				s, err := app.hub.Store.GetSession(ck.Value, roomID)
				if err != nil {
					app.logger.Printf("error checking session: %v", err)
					respondJSON(w, nil, errors.New("error checking session"), http.StatusForbidden)
					return
				}
				req.sess = sess{
					ID:     s.ID,
					Handle: s.Handle,
				}
			}
		}

		// Check if the room is valid and active.
		if opts&hasRoom != 0 {
			// If the room's not found, req.room will be null in the target
			// handler. It's the handler's responsibility to throw an error,
			// API or HTML response.
			room, err := app.hub.ActivateRoom(roomID)
			if err == nil {
				req.room = room
			}
		}

		// Attach the request context.
		ctx := context.WithValue(r.Context(), "ctx", req)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// readJSONReq reads the JSON body from a request and unmarshals it to the given target.
func readJSONReq(r *http.Request, o interface{}) error {
	defer r.Body.Close()
	b, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, o)
}

// handleUpload handles file uploads.
func handleUpload(store *upload.Store) func(w http.ResponseWriter, r *http.Request) {

	type roomLimiter struct {
		limiter *rate.Limiter
		expire  time.Time
	}
	var mu sync.Mutex
	roomLimiters := map[string]roomLimiter{}
	go func() {
		t := time.NewTicker(store.RlPeriod + (time.Minute))
		defer t.Stop()
		for range t.C {
			now := time.Now()
			mu.Lock()
			for k, r := range roomLimiters {
				if r.expire.Before(now) {
					delete(roomLimiters, k)
				}
			}
			mu.Unlock()
		}
	}()

	return func(w http.ResponseWriter, r *http.Request) {
		err := r.ParseMultipartForm(store.MaxUploadSize)

		if err == nil {
			roomID := chi.URLParam(r, "roomID")
			mu.Lock()
			// no defer here becasue file upload can be slow, thus lock for too long
			x, ok := roomLimiters[roomID]
			if !ok {
				x = roomLimiter{
					limiter: rate.NewLimiter(rate.Every(store.RlPeriod/time.Duration(store.RlCount)), store.RlBurst),
					expire:  time.Now().Add(time.Minute * 10),
				}
				roomLimiters[roomID] = x
			}
			x.expire = time.Now().Add(time.Minute * 10)
			roomLimiters[roomID] = x
			mu.Unlock()
			if !x.limiter.Allow() {
				err = errors.New(http.StatusText(http.StatusTooManyRequests))
			}
		}

		type fileRes struct {
			ID  string
			Err string
		}
		res := map[string]fileRes{}
		if err == nil {
			var files []multipart.File
			var handlers []*multipart.FileHeader
			for i := 0; i < 20; i++ {
				key := fmt.Sprintf("file%v", i)
				file, handler, e := r.FormFile(key)
				if e == http.ErrMissingFile {
					// all files were processed.
					break
				}
				if e != nil {
					err = e
				}
				if err != nil {
					break
				}
				defer file.Close()
				files = append(files, file)
				handlers = append(handlers, handler)
			}
			if err == nil {
				for i, file := range files {
					handler := handlers[i]
					b, e := ioutil.ReadAll(file)
					if e != nil {
						res[handler.Filename] = fileRes{Err: e.Error()}
						continue
					}
					mimeType := http.DetectContentType(b)
					if mimeType == "image/gif" || mimeType == "image/jpeg" || mimeType == "image/png" {
						name := handler.Filename
						up, e := store.Add(name, mimeType, b)
						if err != nil {
							res[handler.Filename] = fileRes{Err: e.Error()}
							continue
						}
						res[handler.Filename] = fileRes{ID: fmt.Sprintf("%v_%v", up.ID, up.Name)}
					} else {
						res[handler.Filename] = fileRes{Err: "invalid file type"}
					}
				}
			}
		}

		s := http.StatusOK
		if err != nil {
			s = http.StatusBadRequest
		}
		respondJSON(w, res, err, s)
	}
}

// handleUploaded uploaded files display.
func handleUploaded(store *upload.Store) func(w http.ResponseWriter, r *http.Request) {
	maxAgeHeader := fmt.Sprintf("max-age=%v", int64(store.MaxAge/time.Second))
	return func(w http.ResponseWriter, r *http.Request) {
		fileID := chi.URLParam(r, "fileID")
		fileID = strings.Split(fileID, "_")[0]
		up, err := store.Get(fileID)
		if err != nil {
			log.Println(err)
			respondJSON(w, nil, err, http.StatusNotFound)
			return
		}
		w.Header().Add("Content-Type", up.MimeType)
		w.Header().Add("Content-Length", fmt.Sprint(len(up.Data)))
		if store.MaxAge > 0 {
			w.Header().Add("Cache-Control", maxAgeHeader)
		}
		w.WriteHeader(http.StatusOK)
		w.Write(up.Data)
	}
}
