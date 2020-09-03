# setup a systemd service

A quick step by step helper to install niltalk as a service using systemd.

```sh
mkdir ~/niltalk
cd ~/niltalk
wget -O niltalk_0.1.0_linux_amd64.tar.gz https://github.com/knadh/niltalk/releases/download/v0.1.0/niltalk_0.1.0_linux_amd64.tar.gz
tar -xf niltalk_0.1.0_linux_amd64.tar.gz
rm -f niltalk_0.1.0_linux_amd64.tar.gz
./niltalk --new-config
./niltalk --new-unit
```

### Edit the config and unit files

You are required to provide a working directory.
This option must be set into the unit file.

```sh
mkdir -p ~/.config/systemd/user/
cp niltalk.service ~/.config/systemd/user/
systemctl --user enable niltalk.service
systemctl --user start niltalk.service
journalctl --user -fu niltalk.service
```

To disable the service

```sh
systemctl --user stop niltalk.service
systemctl --user disable niltalk.service
journalctl --user -fu niltalk.service
rm ~/.config/systemd/user/niltalk.service
```
