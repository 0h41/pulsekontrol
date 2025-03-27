# PulseKontrol

PulseKontrol is a tool that lets you control your PulseAudio mixer using KORG nanoKONTROL2 MIDI controller, 
with a web interface that allows on-the-fly configuration changes.
It was almost fully developed by Claude Code, so the programming is mostly hot garbage. But it works.
Initial sources used: [pamixermidicontrol](https://github.com/fluciotto/pamixermidicontrol), 
itself based on [pamidicontrol](https://github.com/solarnz/pamidicontrol).

## Why

Windows has a nice piece of software called midi-mixer, but the best I could find for linux was [pamixermidicontrol](https://github.com/fluciotto/pamixermidicontrol).
It works, but I really didn't like having to edit the config manually and restart it every time I wanted to assign a new source.

## What changed

- Much simplified config format
- New web UI that allows (re)assigning audio sources to sliders/knobs on the fly (http://127.0.0.1:6080)
- Direct support only for KORG nanoKONTROL2 to simplify for the ai coder (modifying for other devices shouldn't be too hard)
- Removed support for (mute) buttons to simplify further

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

## Configuration

Config location is `$HOME/.config/pulsekontrol/config.yaml`.
If it's not found on startup, a default one wil be created automatically (just a scaffold without any assignments).
It's meant to be changed using the web interface, but if you make sure the program is not running you can edit it manually.

## Usage

- Run `./pulsekontrol` 
- Open http://127.0.0.1:6080 in your browser
- Run ./pulsekontrol --help for available options (like changing the web ui port)
