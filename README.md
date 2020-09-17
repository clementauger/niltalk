# Niltalk

Niltalk is a web based disposable chat server. It allows users to create
password protected disposable, ephemeral chatrooms and invite peers to chat rooms. Rooms can
be disposed of at any time.

![niltalk](https://user-images.githubusercontent.com/547147/78459728-9f8c3180-76d8-11ea-8c0a-9cf9bfe64341.png)

## Features

- configuration less startup, single file executable
- embedded tor instance for instant connectivity
- ssl support with JIT self signed certificate generator,
loading of regular pregenerated signed certificate or letsencrypt
- work at home or in the cloud
- three different backend storage to handle various deployment scenarios.
- persistent and ephemeral rooms
- IM like notifications
- multi theming

## Installation

### Manual
- Download the [latest release](https://github.com/knadh/niltalk/releases) for your platform and extract the binary.
- Run `./niltalk --new-config` to generate a sample config.toml and add your configuration.
- Run `./niltalk` and visit http://localhost:9000.

### Docker
The official Docker image `niltalk:latest` is [available here](https://hub.docker.com/r/kailashnadh/niltalk). To try out the app, copy [docker-compose.yml](docker-compose.yml) and run `docker-compose run niltalk`.

### Systemd
- Run `niltalk --new-unit`, and follow [the guide](systemd.md)

### Customisation
To customize the user interface, start by extracting the embedded assets using `niltalk --extract-themes`.
Then you can edit existing themes or create a new one by adding a folder under `static/themes`.

To rebuild template JIT during development phase, use the `--jit` flag.

> This is a complete rewrite of the old version that had been dead and obsolete for several years (can be found in the `old` branch). These codebases are not compatible with each other and `master` has been overwritten.

Licensed under AGPL3
