package main

import (
	"log"

	"github.com/knadh/niltalk/store"
	"github.com/knadh/niltalk/store/fs"
	"github.com/knadh/niltalk/store/mem"
	"github.com/knadh/niltalk/store/redis"
)

// makeStore creates a new store.Store instance
// according to configuration options
func (a *App) makeStore() (store.Store, error) {
	var store store.Store
	if a.cfg.Storage == "redis" {
		var storeCfg redis.Config
		if err := ko.Unmarshal("store", &storeCfg); err != nil {
			logger.Fatalf("error unmarshalling 'store' config: %v", err)
		}

		s, err := redis.New(storeCfg)
		if err != nil {
			log.Fatalf("error initializing store: %v", err)
		}
		store = s

	} else if a.cfg.Storage == "memory" {
		var storeCfg mem.Config
		if err := ko.Unmarshal("store", &storeCfg); err != nil {
			logger.Fatalf("error unmarshalling 'store' config: %v", err)
		}

		s, err := mem.New(storeCfg)
		if err != nil {
			log.Fatalf("error initializing store: %v", err)
		}
		store = s

	} else if a.cfg.Storage == "fs" {
		var storeCfg fs.Config
		if err := ko.Unmarshal("store", &storeCfg); err != nil {
			logger.Fatalf("error unmarshalling 'store' config: %v", err)
		}

		s, err := fs.New(storeCfg, logger)
		if err != nil {
			log.Fatalf("error initializing store: %v", err)
		}
		store = s
		defer s.Close()

	} else {
		logger.Fatal("app.storage must be one of redis|memory|fs")
	}
	return store, nil
}
