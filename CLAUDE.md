# PulseKontrol Development Guide

## Build & Run Commands
- Build: `make`
- Clean: `make clean`
- Run: `./pulsekontrol`
- List MIDI devices: `./pulsekontrol --list-midi`
- List PulseAudio objects: `./pulsekontrol --list-pulse`
- View help: `./pulsekontrol --help`

## Development Goals
- Create a web GUI
- Focus on supporting KORG nanoKONTROL2
- Enable dynamic assignment of audio sources to sliders
- Allow on-the-fly configuration changes without editing YAML files

## Project Requirements
- Go 1.22+ required
- PulseAudio and portmidi libraries required
- Configuration file: `$HOME/.config/pulsekontrol/config.yaml`
- Error messages and MIDI control messages print to stderr

## Repo Structure
- `main.go`: Entry point
- `src/`: Core implementation
- `src/configuration/`: Config parsing
- `src/device/`: Device-specific code
- `src/device/korg/nanokontrol2/`: KORG nanoKONTROL2 implementation
- `src/midi/`: MIDI client implementation
- `src/pulseaudio/`: PulseAudio client
- `src/webui/`: Web interface implementation
- `config-examples/`: Sample configuration files (see `korg-nanokontrol2.yaml`)

## Configuration Management Design
- Hybrid approach: YAML file + in-memory model
- Runtime changes saved immediately to disk with debouncing
- Systemd service compatibility with proper signal handling
- Implementation notes:
  - Use mutex to protect config access
  - Debounce rapid changes (2-second window)
  - Write to temp file then rename for atomic updates
  - Handle SIGINT/SIGTERM for clean shutdown

## Web UI Design Notes
- The sliders and knobs in the web UI are read-only displays
- They show the current levels set by the MIDI device
- Users cannot adjust the levels directly from the web UI
- The web UI is for visualizing current state and managing assignments only
