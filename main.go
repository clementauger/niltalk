// Niltalk, April 2015
// License AGPL3

package main

import (
	"crypto/tls"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	rice "github.com/GeertJohan/go.rice"
	"github.com/fsnotify/fsnotify"
	"github.com/go-chi/chi"
	"github.com/knadh/koanf"
	"github.com/knadh/koanf/parsers/toml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/posflag"
	"github.com/knadh/koanf/providers/rawbytes"
	"github.com/knadh/niltalk/internal/hub"
	"github.com/knadh/niltalk/internal/upload"
	flag "github.com/spf13/pflag"
	"golang.org/x/crypto/acme/autocert"
)

var (
	logger = log.New(os.Stderr, "", log.Ldate|log.Ltime|log.Lshortfile)
	ko     = koanf.New(".")

	// Version of the build injected at build time.
	buildString = "unknown"
)

// App is the global app context that's passed around.
type App struct {
	hub          *hub.Hub
	cfg          *hub.Config
	tpls         map[string]*template.Template
	themesBox    *rice.Box
	jit          bool
	logger       *log.Logger
	localAddress string
	qrConfig     qrConfig
}

func loadConfig() {
	// Register --help handler.
	f := flag.NewFlagSet("config", flag.ContinueOnError)
	f.Usage = func() {
		fmt.Println(f.FlagUsages())
		os.Exit(0)
	}
	f.StringSlice("config", []string{"config.toml"},
		"Path to one or more TOML config files to load in order. It loads the embedded default configuration file by default.")
	f.Bool("new-config", false, "generate sample config file")
	f.Bool("new-unit", false, "generate systemd unit file")
	f.Bool("onion", false, "Show the onion URL")
	f.Bool("onionpk", false, "Show the onion private key")
	f.Bool("version", false, "Show build version")
	f.Bool("extract-themes", false, "Extract themes assets")
	f.Bool("jit", defaultJIT, "build templates just in time")
	f.Parse(os.Args[1:])

	// Display version.
	if ok, _ := f.GetBool("version"); ok {
		fmt.Println(buildString)
		os.Exit(0)
	}

	// Generate new config.
	if ok, _ := f.GetBool("new-config"); ok {
		if err := newConfigFile(); err != nil {
			logger.Println(err)
			os.Exit(1)
		}
		logger.Println("generated config.toml. Edit and run the app.")
		os.Exit(0)
	}

	// Generate new unit.
	if ok, _ := f.GetBool("new-unit"); ok {
		if err := newUnitFile(); err != nil {
			logger.Println(err)
			os.Exit(1)
		}
		logger.Println("generated niltalk.service. Edit and install the service.")
		os.Exit(0)
	}

	// Exctrat assets.
	if ok, _ := f.GetBool("extract-themes"); ok {
		if err := extractThemes(); err != nil {
			logger.Println(err)
			os.Exit(1)
		}
		cwd, _ := os.Getwd()
		logger.Printf("Assets extracted to %v/static/themes", cwd)
		os.Exit(0)
	}

	// Read the config files.
	cFiles, _ := f.GetStringSlice("config")
	for _, f := range cFiles {
		if _, err := os.Stat(f); len(cFiles) == 1 && f == "config.toml" && os.IsNotExist(err) {
			continue
		}
		logger.Printf("reading config: %s", f)
		if err := ko.Load(file.Provider(f), toml.Parser()); err != nil {
			if os.IsNotExist(err) {
				logger.Fatal("config file not found. If there isn't one yet, run --new-config to generate one.")
			}
			logger.Fatalf("error loading config from file: %v.", err)
		}
	}

	// load the default configuration file
	if len(cFiles) < 1 {
		logger.Printf("loading default configuration from embedded assets")
		sampleBox := rice.MustFindBox("static/samples")
		b, err := sampleBox.Bytes("config.toml")
		if err != nil {
			logger.Fatalf("error reading embedded asset %q: %v.", "static/samples/config.toml", err)
		}
		err = ko.Load(rawbytes.Provider(b), toml.Parser())
		if err != nil {
			logger.Fatalf("error loading default configuration file: %v.", err)
		}
	}

	// Merge env flags into config.
	if err := ko.Load(env.Provider("NILTALK_", ".", func(s string) string {
		return strings.Replace(strings.ToLower(
			strings.TrimPrefix(s, "NILTALK_")), "__", ".", -1)
	}), nil); err != nil {
		logger.Printf("error loading env config: %v", err)
	}

	// Merge command line flags into config.
	ko.Load(posflag.Provider(f, ".", ko), nil)
}

func main() {
	// Load configuration from files.
	loadConfig()

	// Begin listening.
	lnAddr := ko.String("app.address")
	ln, err := net.Listen("tcp", lnAddr)
	if err != nil {
		logger.Fatalf("couldn't listen address %q: %v", lnAddr, err)
	}

	// Initialize global app context.
	app := &App{
		logger:       logger,
		localAddress: ln.Addr().String(),
	}
	if err := ko.Unmarshal("app", &app.cfg); err != nil {
		logger.Fatalf("error unmarshalling 'app' config: %v", err)
	}
	if app.cfg.Theme == "" {
		logger.Println("configuration directive 'app.theme' is empty, setting default to 'knadh'")
		app.cfg.Theme = "knadh"
	}

	// Load file system boxes
	rConf := rice.Config{LocateOrder: []rice.LocateMethod{rice.LocateWorkingDirectory, rice.LocateAppended}}
	themesBox := rConf.MustFindBox("static/themes")
	app.themesBox = themesBox

	minTime := time.Duration(3) * time.Second
	if app.cfg.RoomAge < minTime || app.cfg.WSTimeout < minTime {
		logger.Fatal("app.websocket_timeout and app.roomage should be > 3s")
	}

	// Initialize store.
	store, err := app.makeStore()
	if err != nil {
		logger.Fatalf("failed to create the store instance: %v", err)
	}

	var torCfg torCfg
	if err := ko.Unmarshal("tor", &torCfg); err != nil {
		logger.Fatalf("error unmarshalling 'tor' config: %v", err)
	}

	if ko.Bool("onion") {
		pk, err := loadTorPK(torCfg, store)
		if err != nil {
			logger.Fatalf("could not read or write the private key: %v", err)
		}
		fmt.Printf("http://%v.onion\n", onionAddr(pk))
		return // to allow for defers to execute
	}

	if ko.Bool("onionpk") {
		pk, err := loadTorPK(torCfg, store)
		if err != nil {
			logger.Fatalf("could not read or write the private key: %v", err)
		}
		pem, err := pemEncodeKey(pk)
		if err != nil {
			logger.Fatalf("could not PEM encode the private key: %v", err)
		}
		fmt.Printf("%s\n", pem)
		return // to allow for defers to execute
	}

	app.hub = hub.NewHub(app.cfg, store, logger)

	if err := ko.Unmarshal("rooms", &app.cfg.Rooms); err != nil {
		logger.Fatalf("error unmarshalling 'rooms' config: %v", err)
	}
	// setup predefined rooms
	err = app.loadPredefinedRooms(themesBox)
	if err != nil {
		logger.Fatalf("error loading predefined rooms: %v", err)
	}

	// Compile static templates.
	tpls, err := app.buildTpls()
	if err != nil {
		logger.Fatalf("error compiling templates: %v", err)
	}
	app.jit = ko.Bool("jit")
	app.tpls = tpls

	// Setup the file upload store.
	var uploadCfg upload.Config
	if err := ko.Unmarshal("upload", &uploadCfg); err != nil {
		logger.Fatalf("error unmarshalling 'upload' config: %v", err)
	}

	uploadStore := upload.New(uploadCfg)
	if err := uploadStore.Init(); err != nil {
		logger.Fatalf("error initializing upload store: %v", err)
	}

	// Register HTTP routes.
	r := chi.NewRouter()
	r.Get("/", wrap(handleIndex, app, 0))
	r.Get("/r/{roomID}/ws", wrap(handleWS, app, hasAuth|hasRoom))

	// API.
	r.Post("/api/rooms", wrap(handleCreateRoom, app, 0))
	r.Post("/r/{roomID}/login", wrap(handleLogin, app, hasRoom))
	r.Delete("/r/{roomID}/login", wrap(handleLogout, app, hasAuth|hasRoom))

	r.Post("/r/{roomID}/upload", handleUpload(uploadStore))
	r.Get("/r/{roomID}/uploaded/{fileID}", handleUploaded(uploadStore))

	// Views.
	r.Get("/r/{roomID}", wrap(handleRoomPage, app, hasAuth|hasRoom))

	// QRCode.
	if err := ko.Unmarshal("qr", &app.qrConfig); err != nil {
		logger.Fatalf("error unmarshalling 'qr' config: %v", err)
	}
	if !torCfg.Enabled {
		app.qrConfig.Tor = false //disable it anyways.
	}
	if app.qrConfig.Tor && torCfg.Enabled {
		pk, err := loadTorPK(torCfg, store)
		if err != nil {
			logger.Fatalf("could not read or write the private key: %v", err)
		}
		onion := fmt.Sprintf("http://%v.onion\n", onionAddr(pk))
		r.Get("/here.tor", genQRCode(onion))
	}
	if app.qrConfig.Clear != "" {
		r.Get("/here.clear", genQRCode(app.qrConfig.Clear))
	}

	// Assets.
	assets := http.StripPrefix("/static/", http.FileServer(themesBox.HTTPBox()))
	r.Get("/static/*", noDirListHandler(assets.ServeHTTP))

	// Start the app.
	if torCfg.Enabled {
		pk, err := loadTorPK(torCfg, store)
		if err != nil {
			logger.Fatalf("could not read or write the private key: %v", err)
		}

		srv := &torServer{
			PrivateKey: pk,
			Handler:    r,
		}
		defer srv.Close()

		onionAddr := onionAddr(pk) + ".onion"

		if torCfg.SSL {
			srv.TLSConfig = tlsConfig(getCertificate(onionAddr))
			srv.TLSNextProto = make(map[string]func(*http.Server, *tls.Conn, http.Handler), 0)
			logger.Printf("starting hidden service on https://%v", onionAddr)
		}
		logger.Printf("starting hidden service on http://%v", onionAddr)
		go func() {
			if err := srv.Serve(ln); err != nil {
				logger.Fatalf("couldn't serve: %v", err)
			}
		}()
	}

	srv := http.Server{
		Handler: r,
	}
	var sslCfg sslCfg
	if err := ko.Unmarshal("ssl", &sslCfg); err != nil {
		logger.Fatalf("error unmarshalling 'ssl' config: %v", err)
	}

	sslAddr := ":443"
	if sslCfg.Address != "" {
		sslAddr = sslCfg.Address
	}
	_, sslPort, err := net.SplitHostPort(sslAddr)
	if err != nil {
		logger.Fatalf("couldn't parse address %q: %v", sslAddr, err)
	}

	var certManager autocert.Manager
	if sslCfg.Enabled {
		if sslCfg.Kind == "letsencrypt" {
			var sslStorage autocert.Cache
			if sslCfg.Storage == "disk" {
				if sslCfg.Path == "" {
					sslCfg.Path = "certs"
				}
				sslStorage = autocert.DirCache(sslCfg.Path)
			} else if sslCfg.Storage == "store" {
				sslStorage = sslStore{prefix: "SSL:", store: store}
			} else {
				if app.cfg.Storage == "memory" {
					if sslCfg.Path == "" {
						sslCfg.Path = "certs"
					}
					sslStorage = autocert.DirCache(sslCfg.Path)
				} else {
					sslStorage = sslStore{prefix: "SSL:", store: store}
				}
			}
			certManager = autocert.Manager{
				Prompt:     autocert.AcceptTOS,
				HostPolicy: autocert.HostWhitelist(sslCfg.Domains...),
				Cache:      sslStorage,
				Email:      sslCfg.Email,
			}
			srv.Handler = certManager.HTTPHandler(handleHTTPRedirect(sslPort, srv.Handler))

		} else {
			srv.Handler = handleHTTPRedirect(sslPort, srv.Handler)
		}
	}

	logger.Printf("starting server on http://%v", ln.Addr().String())
	go func() {
		if err := srv.Serve(ln); err != nil {
			logger.Fatalf("couldn't serve: %v", err)
		}
	}()

	if sslCfg.Enabled {
		sln, err := net.Listen("tcp", sslAddr)
		if err != nil {
			logger.Fatalf("couldn't listen address %q: %v", sslAddr, err)
		}
		ssrv := http.Server{
			Handler: r,
		}
		if sslCfg.Kind == "auto" {
			ssrv.TLSConfig = tlsConfig(getCertificate(sslCfg.Domains))
			ssrv.TLSNextProto = make(map[string]func(*http.Server, *tls.Conn, http.Handler), 0)

		} else if sslCfg.Kind == "files" {
			ssrv.TLSConfig = tlsConfig(nil)
			ssrv.TLSNextProto = make(map[string]func(*http.Server, *tls.Conn, http.Handler), 0)

		} else if sslCfg.Kind == "letsencrypt" {
			tlsConfig := certManager.TLSConfig()
			tlsConfig.GetCertificate = certManager.GetCertificate
			srv.TLSConfig = tlsConfig
		}
		logger.Printf("starting server on https://%v", sln.Addr().String())
		go func() {
			var err error
			if sslCfg.Kind == "files" {
				err = ssrv.ServeTLS(sln, sslCfg.Certificate, sslCfg.PrivateKey)
			} else {
				err = ssrv.ServeTLS(sln, "", "")
			}
			if err != nil {
				logger.Fatalf("couldn't tls serve: %v", err)
			}
		}()
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL)
	var cFiles []string
	ko.Unmarshal("config", &cFiles)
	select {
	case <-fileWatcher(cFiles...):
	case sig := <-c:
		logger.Printf("shutting down: %v", sig)
	}
	go func() {
		d := time.Second * 10
		<-time.After(d)
		logger.Printf("%v seconds elapsed. Quiting now.", d)
		os.Exit(1)
	}()
}

func fileWatcher(files ...string) chan struct{} {
	out := make(chan struct{})
	if len(files) > 0 {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			logger.Printf("failed to initialize configuration file watcher: %v", err)
			return out
		}
		for _, f := range files {
			err = watcher.Add(f)
			if err != nil {
				logger.Printf("failed to add configuration file %q watcher: %v", f, err)
			}
		}
		go func() {
			for {
				select {
				case event, ok := <-watcher.Events:
					if !ok {
						return
					}
					// if event.Op&fsnotify.Write == fsnotify.Write {
					logger.Printf("configuration file %q was modified", event.Name)
					out <- struct{}{}
					// }
				case err, ok := <-watcher.Errors:
					if !ok {
						return
					}
					logger.Printf("watcher error: %v", err)
				}
			}
		}()
	}
	return out
}
