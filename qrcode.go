package main

type qrConfig struct {
	Tor   bool   `koanf:"tor"`
	Clear string `koanf:"clear"`
}
