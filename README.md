# PulseKontrol

PulseKontrol is a tool that lets you control your PulseAudio mixer with MIDI controllers.

## Features

- Control PulseAudio source volumes using a MIDI controller
- Web configuration interface
- Focuses on supporting KORG nanoKONTROL2
- Feel free to fork/contribute with support for other devices.

## Installation

Prerequisites:
- Go 1.22 or newer
- PulseAudio
- portmidi library

```bash
# Install portmidi
# For Arch Linux:
pacman -S portmidi
# For Debian/Ubuntu:
apt-get install libportmidi-dev

# Clone and build
git clone https://github.com/0h41/pulsekontrol
cd pulsekontrol
make
```

## Web Interface

Access the configuration web interface:

- Run `./pulsekontrol` 
- Open http://127.0.0.1:6080 in your browser

Interface options:
```bash
# Change interface address/port
./pulsekontrol --web-addr 0.0.0.0:6080

# Disable web interface
./pulsekontrol --no-webui
```

## Configuration

Configuration is meant to be done using the web interface, but the settings are stored in a yaml config file located at: 
`$HOME/.config/pulsekontrol/config.yaml`, and can be modified manually (make sure to stop the program first). 

Check the [config-examples](https://github.com/0h41/pulsekontrol/tree/master/config-examples) directory for templates.

## Usage

```bash
# List available MIDI devices
./pulsekontrol --list-midi

# List available PulseAudio objects
./pulsekontrol --list-pulse

# Show all options
./pulsekontrol --help
```

## Credits

PulseKontrol is based on [pamixermidicontrol](https://github.com/fluciotto/pamixermidicontrol), which was based on [pamidicontrol](https://github.com/solarnz/pamidicontrol/) itself.
