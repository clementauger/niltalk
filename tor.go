package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/clementauger/tor-prebuilt/embedded"
	"github.com/cretz/bine/tor"
	"github.com/cretz/bine/torutil"
	tued25519 "github.com/cretz/bine/torutil/ed25519"
	"github.com/knadh/niltalk/store"
)

type torCfg struct {
	Enabled    bool   `koan:"enabled"`
	SSL        bool   `koan:"ssl"`
	PrivateKey string `koan:"privatekey"`
}

func loadTorPK(cfg torCfg, store store.Store) (pk ed25519.PrivateKey, err error) {
	if cfg.PrivateKey != "" {
		return getOrCreatePKFile(cfg.PrivateKey)
	}
	return getOrCreatePK(store)
}

func getOrCreatePK(store store.Store) (privateKey ed25519.PrivateKey, err error) {
	key := "onionkey"
	d, err := store.Get(key)
	if len(d) == 0 || err != nil {
		_, privateKey, err = ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		var x509Encoded []byte
		x509Encoded, err = x509.MarshalPKCS8PrivateKey(privateKey)
		if err != nil {
			return nil, err
		}
		pemEncoded := pem.EncodeToMemory(&pem.Block{Type: "ED25519 PRIVATE KEY", Bytes: x509Encoded})
		err = store.Set(key, pemEncoded)
	} else {
		block, _ := pem.Decode(d)
		x509Encoded := block.Bytes
		var tPk interface{}
		tPk, err = x509.ParsePKCS8PrivateKey(x509Encoded)
		if err != nil {
			return nil, err
		}
		if x, ok := tPk.(ed25519.PrivateKey); ok {
			privateKey = x
		} else {
			err = fmt.Errorf("invalid key type %T wanted ed25519.PrivateKey", tPk)
		}
	}
	return privateKey, err
}

func getOrCreatePKFile(fpath string) (privateKey ed25519.PrivateKey, err error) {
	if _, err := os.Stat(fpath); os.IsNotExist(err) {
		_, privateKey, err = ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		var x509Encoded []byte
		x509Encoded, err = x509.MarshalPKCS8PrivateKey(privateKey)
		if err != nil {
			return nil, err
		}
		pemEncoded := pem.EncodeToMemory(&pem.Block{Type: "ED25519 PRIVATE KEY", Bytes: x509Encoded})
		ioutil.WriteFile(fpath, pemEncoded, os.ModePerm)
	} else {
		var d []byte
		d, err = ioutil.ReadFile(fpath)
		if err != nil {
			return nil, err
		}
		block, _ := pem.Decode(d)
		x509Encoded := block.Bytes
		var tPk interface{}
		tPk, err = x509.ParsePKCS8PrivateKey(x509Encoded)
		if err != nil {
			return nil, err
		}
		if x, ok := tPk.(ed25519.PrivateKey); ok {
			privateKey = x
		} else {
			return nil, fmt.Errorf("invalid key type %T wanted ed25519.PrivateKey", tPk)
		}
	}
	return privateKey, nil
}

type torServer struct {
	Handler http.Handler
	// PrivateKey path to a pem encoded ed25519 private key
	PrivateKey ed25519.PrivateKey
	tor        *tor.Tor
	onion      *tor.OnionService

	TLSConfig    *tls.Config
	TLSNextProto map[string]func(*http.Server, *tls.Conn, http.Handler)
}

func onionAddr(pk ed25519.PrivateKey) string {
	return torutil.OnionServiceIDFromV3PublicKey(tued25519.PublicKey([]byte(pk.Public().(ed25519.PublicKey))))
}

func (ts *torServer) Serve(ln net.Listener) error {
	d, err := ioutil.TempDir("", "")
	if err != nil {
		return err
	}

	// Start tor with default config (can set start conf's DebugWriter to os.Stdout for debug logs)
	// fmt.Println("Starting and registering onion service, please wait a couple of minutes...")
	t, err := tor.Start(nil, &tor.StartConf{TempDataDirBase: d, ProcessCreator: embedded.NewCreator(), NoHush: true})
	if err != nil {
		return fmt.Errorf("unable to start Tor: %v", err)
	}
	ts.tor = t
	// defer t.Close()

	// Wait at most a few minutes to publish the service
	listenCtx, listenCancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer listenCancel()
	// Create a v3 onion service to listen on any port but show as 80
	onion, err := t.Listen(listenCtx, &tor.ListenConf{
		LocalListener: ln,
		Key:           ts.PrivateKey,
		Version3:      true,
		RemotePorts:   []int{80, 443},
	})
	if err != nil {
		return fmt.Errorf("unable to create onion service: %v", err)
	}
	ts.onion = onion

	errc := make(chan error)
	if ts.TLSConfig != nil {
		x := http.Server{
			Handler:      ts.Handler,
			TLSConfig:    ts.TLSConfig,
			TLSNextProto: ts.TLSNextProto,
		}
		go func() {
			errc <- x.ServeTLS(ts.onion, "", "")
		}()
	}

	go func() {
		errc <- http.Serve(ts.onion, ts.Handler)
	}()
	return <-errc
}
func (ts *torServer) Close() error {
	if err := ts.onion.Close(); err != nil {
		return err
	}
	if err := ts.tor.Close(); err != nil {
		return err
	}
	return nil
}
