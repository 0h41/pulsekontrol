package configuration

import "gopkg.in/yaml.v3"

// Legacy types - keep for compatibility during transition
type MidiDeviceType string

const (
	Generic          MidiDeviceType = "Generic"
	KorgNanoKontrol2 MidiDeviceType = "KorgNanoKontrol2"
)

type MidiDevice struct {
	Name        string         `yaml:"name"`
	Type        MidiDeviceType `yaml:"type"`
	MidiInName  string         `yaml:"midiInName"`
	MidiOutName string         `yaml:"midiOutName"`
}

type MidiMessageType string

const (
	None          MidiMessageType = ""
	Note          MidiMessageType = "Note"
	ControlChange MidiMessageType = "ControlChange"
	ProgramChange MidiMessageType = "ProgramChange"
)

type MidiMessage struct {
	DeviceName        string          `yaml:"deviceName"`
	DeviceControlPath string          `yaml:"deviceControlPath"`
	Type              MidiMessageType `yaml:"type"`
	Channel           uint8           `yaml:"channel"`
	Note              uint8           `yaml:"note"`
	Controller        uint8           `yaml:"controller"`
	Program           uint8           `yaml:"program"`
	MinValue          uint8           `yaml:"minValue"`
	MaxValue          uint8           `yaml:"maxValue"`
}

type PulseAudioActionType string

const (
	SetVolume        PulseAudioActionType = "SetVolume"
	SetDefaultOutput PulseAudioActionType = "SetDefaultOutput"
	MediaPlayPause   PulseAudioActionType = "MediaPlayPause"
)

type Target struct {
	Name string `yaml:"name"`
}

type TypedTarget struct {
	Type       PulseAudioTargetType `yaml:"type"`
	Name       string               `yaml:"name"`
	BinaryName string               `yaml:"binaryName,omitempty"`
}

type Action struct {
	Type      PulseAudioActionType `yaml:"type"`
	RawTarget yaml.Node            `yaml:"target"`
	Target    interface{}          `yaml:"-"`
}

type Rule struct {
	MidiMessage MidiMessage `yaml:"midiMessage"`
	Actions     []Action    `yaml:"actions"`
}

// Legacy Config structure
type LegacyConfig struct {
	MidiDevices []MidiDevice `yaml:"midiDevices"`
	Rules       []Rule       `yaml:"rules"`
}

// New configuration format
// Audio Source Target Types
type PulseAudioTargetType string

const (
	PlaybackStream PulseAudioTargetType = "PlaybackStream"
	RecordStream   PulseAudioTargetType = "RecordStream"
	OutputDevice   PulseAudioTargetType = "OutputDevice"
	InputDevice    PulseAudioTargetType = "InputDevice"
)

// Source represents an audio source or destination
type Source struct {
	Type       PulseAudioTargetType `yaml:"type"`
	Name       string               `yaml:"name"`
	BinaryName string               `yaml:"binaryName,omitempty"`
}

// Button action types
type ActionType string

const (
	SetDefaultOutputAction ActionType = "SetDefaultOutput"
	SetDefaultInputAction  ActionType = "SetDefaultInput"
	PlayPauseTransport     ActionType = "PlayPause"
	StopTransport          ActionType = "Stop"
)

// ButtonTarget is the target for button actions
type ButtonTarget struct {
	Name string `yaml:"name"`
}

// SliderConfig represents a slider on the MIDI controller
type SliderConfig struct {
	Path    string   `yaml:"path"`    // The MIDI control path (e.g., "Group1/Slider")
	Value   int      `yaml:"value"`   // Current value (0-100)
	Sources []Source `yaml:"sources"` // Audio sources controlled by this slider
}

// KnobConfig represents a knob on the MIDI controller
type KnobConfig struct {
	Path    string   `yaml:"path"`    // The MIDI control path (e.g., "Group1/Knob")
	Value   int      `yaml:"value"`   // Current value (0-100)
	Sources []Source `yaml:"sources"` // Audio sources controlled by this knob
}

// DeviceConfig contains MIDI device settings
type DeviceConfig struct {
	Name    string `yaml:"name"`    // Display name for the device
	InPort  string `yaml:"inPort"`  // MIDI input port name
	OutPort string `yaml:"outPort"` // MIDI output port name
}

// Controls contains all controller mappings
type Controls struct {
	Sliders map[string]SliderConfig `yaml:"sliders,omitempty"`
	Knobs   map[string]KnobConfig   `yaml:"knobs,omitempty"`
}

// Config is the root configuration structure
type Config struct {
	Device   DeviceConfig `yaml:"device"`   // MIDI device settings
	Controls Controls     `yaml:"controls"` // Controller mappings
}
