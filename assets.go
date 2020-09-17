package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"

	rice "github.com/GeertJohan/go.rice"
)

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

func extractThemes() error {
	base := "static/themes"
	if _, err := os.Stat(base); !os.IsNotExist(err) {
		return fmt.Errorf("%v directory exists. Remove it to generate a new one", base)
	}

	themesBox := rice.MustFindBox("static/themes")
	return themesBox.Walk("", func(path string, info os.FileInfo, err error) error {
		if filepath.IsAbs(path) {
			// for some reasons this filepath.Walk spits out
			// the absolute path of the box at first.
			// We are not interested about it, and it appears
			// all others path are relative.
			return nil
		}
		p := filepath.Join(base, path)
		if info.IsDir() {
			return os.Mkdir(p, 0644)
		}
		b, err := themesBox.Bytes(path)
		if err != nil {
			return err
		}
		return ioutil.WriteFile(p, b, 0644)
	})
}

func noDirListHandler(next func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if filepath.Ext(r.URL.Path) == "" {
			w.Write([]byte{})
			return
		}
		next(w, r)
	}
}
