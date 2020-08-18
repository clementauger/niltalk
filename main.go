// Niltalk, April 2015
// License AGPL3

package main

import (
	"errors"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	rice "github.com/GeertJohan/go.rice"
	"github.com/alecthomas/units"
	"github.com/go-chi/chi"
	tparse "github.com/karrick/tparse/v2"
	"github.com/knadh/koanf"
	"github.com/knadh/koanf/parsers/toml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/posflag"
	"github.com/knadh/niltalk/internal/hub"
	"github.com/knadh/niltalk/internal/upload"
	"github.com/knadh/niltalk/store"
	"github.com/knadh/niltalk/store/mem"
	"github.com/knadh/niltalk/store/redis"
	flag "github.com/spf13/pflag"
)

var (
	logger = log.New(os.Stdout, "", log.Ldate|log.Ltime|log.Lshortfile)
	ko     = koanf.New(".")

	// Version of the build injected at build time.
	buildString = "unknown"
)

// App is the global app context that's passed around.
type App struct {
	hub    *hub.Hub
	cfg    *hub.Config
	tpl    *template.Template
	tplBox *rice.Box
	jit    bool
	logger *log.Logger
}

func loadConfig() {
	// Register --help handler.
	f := flag.NewFlagSet("config", flag.ContinueOnError)
	f.Usage = func() {
		fmt.Println(f.FlagUsages())
		os.Exit(0)
	}
	f.StringSlice("config", []string{"config.toml"},
		"Path to one or more TOML config files to load in order")
	f.Bool("new-config", false, "generate sample config file")
	f.Bool("new-unit", false, "generate systemd unit file")
	f.Bool("version", false, "Show build version")
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

	// Read the config files.
	cFiles, _ := f.GetStringSlice("config")
	for _, f := range cFiles {
		logger.Printf("reading config: %s", f)
		if err := ko.Load(file.Provider(f), toml.Parser()); err != nil {
			if os.IsNotExist(err) {
				logger.Fatal("config file not found. If there isn't one yet, run --new-config to generate one.")
			}
			logger.Fatalf("error loadng config from file: %v.", err)
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

// Catch OS interrupts and respond accordingly.
// This is not fool proof as http keeps listening while
// existing rooms are shut down.
func catchInterrupts() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL)
	go func() {
		for sig := range c {
			// Shutdown.
			logger.Printf("shutting down: %v", sig)
			os.Exit(0)
		}
	}()
}

func newConfigFile() error {
	if _, err := os.Stat("config.toml"); !os.IsNotExist(err) {
		return errors.New("config.toml exists. Remove it to generate a new one")
	}

	// Initialize the static file system into which all
	// required static assets (.sql, .js files etc.) are loaded.
	sampleBox := rice.MustFindBox("static/samples")
	b, err := sampleBox.Bytes("config.toml")
	if err != nil {
		return fmt.Errorf("error reading sample config (is binary stuffed?): %v", err)
	}

	return ioutil.WriteFile("config.toml", b, 0644)
}

func newUnitFile() error {
	if _, err := os.Stat("niltalk.service"); !os.IsNotExist(err) {
		return errors.New("niltalk.service exists. Remove it to generate a new one")
	}

	// Initialize the static file system into which all
	// required static assets (.sql, .js files etc.) are loaded.
	sampleBox := rice.MustFindBox("static/samples")
	b, err := sampleBox.Bytes("niltalk.service")
	if err != nil {
		return fmt.Errorf("error reading sample unit (is binary stuffed?): %v", err)
	}

	return ioutil.WriteFile("niltalk.service", b, 0644)
}

func main() {
	// Load configuration from files.
	loadConfig()

	// Load file system boxes
	rConf := rice.Config{LocateOrder: []rice.LocateMethod{rice.LocateWorkingDirectory, rice.LocateAppended}}
	tplBox := rConf.MustFindBox("static/templates")
	assetBox := rConf.MustFindBox("static/static")

	// Initialize global app context.
	app := &App{
		logger: logger,
		tplBox: tplBox,
	}
	if err := ko.Unmarshal("app", &app.cfg); err != nil {
		logger.Fatalf("error unmarshalling 'app' config: %v", err)
	}

	minTime := time.Duration(3) * time.Second
	if app.cfg.RoomAge < minTime || app.cfg.WSTimeout < minTime {
		logger.Fatal("app.websocket_timeout and app.roomage should be > 3s")
	}

	// Initialize store.
	var store store.Store
	if app.cfg.Storage == "redis" {
		var storeCfg redis.Config
		if err := ko.Unmarshal("store", &storeCfg); err != nil {
			logger.Fatalf("error unmarshalling 'store' config: %v", err)
		}

		s, err := redis.New(storeCfg)
		if err != nil {
			log.Fatalf("error initializing store: %v", err)
		}
		store = s

	} else if app.cfg.Storage == "memory" {
		var storeCfg mem.Config
		if err := ko.Unmarshal("store", &storeCfg); err != nil {
			logger.Fatalf("error unmarshalling 'store' config: %v", err)
		}

		s, err := mem.New(storeCfg)
		if err != nil {
			log.Fatalf("error initializing store: %v", err)
		}
		store = s

	} else {
		logger.Fatal("app.storage must be one of redis or memory")
	}
	app.hub = hub.NewHub(app.cfg, store, logger)

	// Compile static templates.
	tpl, err := app.buildTpl()
	if err != nil {
		logger.Fatalf("error compiling templates: %v", err)
	}
	app.jit = ko.Bool("jit")
	app.tpl = tpl

	// Setup the file upload store.
	var uploadCfg upload.Config
	if err := ko.Unmarshal("upload", &uploadCfg); err != nil {
		logger.Fatalf("error unmarshalling 'upload' config: %v", err)
	}
	var maxMemory int64 = 32 << 20
	if uploadCfg.MaxMemory != "" {
		x, err := units.ParseStrictBytes(uploadCfg.MaxMemory)
		if err != nil {
			logger.Fatalf("error unmarshalling 'upload.max-memory' config: %v", err)
		}
		maxMemory = x
	}

	uploadStore := upload.New(uploadCfg, maxMemory)

	var maxUploadSize int64 = 32 << 20
	if uploadCfg.MaxUploadSize != "" {
		x, err := units.ParseStrictBytes(uploadCfg.MaxUploadSize)
		if err != nil {
			logger.Fatalf("error unmarshalling 'upload.max-upload-size' config: %v", err)
		}
		maxUploadSize = x
	}

	var maxAge time.Duration = time.Hour * 24 * 30 * 12
	if uploadCfg.MaxAge != "" {
		x, err := tparse.AbsoluteDuration(time.Now(), uploadCfg.MaxAge)
		if err != nil {
			logger.Fatalf("error unmarshalling 'upload.max-age' config: %v", err)
		}
		maxAge = x
	}

	var rlPeriod time.Duration = time.Minute
	if uploadCfg.RateLimitPeriod != "" {
		x, err := tparse.AbsoluteDuration(time.Now(), uploadCfg.RateLimitPeriod)
		if err != nil {
			logger.Fatalf("error unmarshalling 'upload.rate-limit-period' config: %v", err)
		}
		rlPeriod = x
	}

	rlCount := 20.0
	if uploadCfg.RateLimitCount != "" {
		x, err := strconv.ParseFloat(uploadCfg.RateLimitCount, 64)
		if err != nil {
			logger.Fatalf("error unmarshalling 'upload.rate-limit-count' config: %v", err)
		}
		rlCount = x
	}

	rlBurst := 1
	if uploadCfg.RateLimitBurst != "" {
		x, err := strconv.Atoi(uploadCfg.RateLimitBurst)
		if err != nil {
			logger.Fatalf("error unmarshalling 'upload.rate-limit-burst' config: %v", err)
		}
		rlBurst = x
	}

	// Register HTTP routes.
	r := chi.NewRouter()
	r.Get("/", wrap(handleIndex, app, 0))
	r.Get("/ws/{roomID}", wrap(handleWS, app, hasAuth|hasRoom))

	// API.
	r.Post("/api/rooms/{roomID}/login", wrap(handleLogin, app, hasRoom))
	r.Delete("/api/rooms/{roomID}/login", wrap(handleLogout, app, hasAuth|hasRoom))
	r.Post("/api/rooms", wrap(handleCreateRoom, app, 0))

	r.Post("/api/upload/{roomID}", handleUpload(uploadStore, maxUploadSize, rlPeriod, rlCount, rlBurst))
	r.Get("/api/uploaded/{fileID}", handleUploaded(uploadStore, maxAge))

	// Views.
	r.Get("/r/{roomID}", wrap(handleRoomPage, app, hasAuth|hasRoom))

	// Assets.
	fs := http.StripPrefix("/static/", http.FileServer(assetBox.HTTPBox()))
	r.Get("/static/*", fs.ServeHTTP)

	// Start the app.
	var srv interface {
		ListenAndServe() error
	}

	if appAddress := ko.String("app.address"); appAddress == "tor" {
		pkPath := ko.String("app.privatekey")
		pk, err := getOrCreatePK(pkPath)
		if err != nil {
			logger.Fatalf("could not create the private key file: %v", err)
		}

		srv = &torServer{
			PrivateKey: pk,
			Handler:    r,
		}
		logger.Printf("starting server on http://%v.onion", onionAddr(pk))

	} else {
		srv = &http.Server{
			Addr:    appAddress,
			Handler: r,
		}
		logger.Printf("starting server on http://%v", appAddress)
	}

	if err := srv.ListenAndServe(); err != nil {
		logger.Fatalf("couldn't start server: %v", err)
	}
}

func (a *App) getTpl() (*template.Template, error) {
	if a.jit {
		return a.buildTpl()
	}
	return a.tpl, nil
}

func (a *App) buildTpl() (*template.Template, error) {
	tpl := template.New("")
	err := a.tplBox.Walk("/", func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}
		s, err := a.tplBox.String(path)
		if err != nil {
			return err
		}
		tpl, err = tpl.Parse(s)
		if err != nil {
			return err
		}
		return nil
	})
	return tpl, err
}
