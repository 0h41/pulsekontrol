package midi

import (
	"fmt"
	"sync"
	"time"

	"github.com/0h41/pulsekontrol/src/configuration"
	korgNanokontrol2 "github.com/0h41/pulsekontrol/src/device/korg/nanokontrol2"
	"github.com/0h41/pulsekontrol/src/pulseaudio"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
	"gitlab.com/gomidi/midi/v2"
	"gitlab.com/gomidi/midi/v2/drivers"

	driver "gitlab.com/gomidi/midi/v2/drivers/portmididrv"
)

// SysEx responses are only read during device initialization. Later responses
// (e.g. LED mode acknowledgments) are dropped once this buffer is full so they
// can never block the MIDI listener.
const sysExBufferSize = 16

func listDevices() ([]string, []string, error) {
	drv, err := driver.New()
	if err != nil {
		return nil, nil, err
	}
	// make sure to close all open ports at the end
	defer drv.Close()
	// MIDI in
	ins, err := drv.Ins()
	if err != nil {
		return nil, nil, err
	}
	// MIDI out
	outs, err := drv.Outs()
	if err != nil {
		return nil, nil, err
	}
	// Get names
	inNames := make([]string, 0, len(ins))
	outNames := make([]string, 0, len(outs))
	for _, port := range ins {
		inNames = append(inNames, port.String())
	}
	for _, port := range outs {
		outNames = append(outNames, port.String())
	}
	return inNames, outNames, nil
}

func List() {
	log := log.Logger.With().Str("module", "Midi").Logger()
	ins, outs, err := listDevices()
	if err != nil {
		log.Error().Err(err).Msg("Failed to list MIDI devices")
		return
	}
	// List input ports
	for _, port := range ins {
		log.Info().Msgf("Found midi in device:\t%s", port)
	}
	// List output ports
	for _, port := range outs {
		log.Info().Msgf("Found midi out device:\t%s", port)
	}
}

type VolumeRequest struct {
	Rule      configuration.Rule
	Value     uint8
	Timestamp time.Time
}

type MidiClient struct {
	log           zerolog.Logger
	PAClient      *pulseaudio.PAClient
	MidiDevice    configuration.MidiDevice
	ConfigManager *configuration.ConfigManager

	rulesMutex sync.RWMutex
	rules      []configuration.Rule

	volumeChannels map[string]chan VolumeRequest
	channelsMutex  sync.Mutex

	// ledMutex guards midiOut and nanoDevice and serializes LED updates
	ledMutex   sync.Mutex
	midiOut    drivers.Out
	nanoDevice *korgNanokontrol2.KorgNanoKontrol2

	// focusAssignMutex lets only one focused-window assignment run at a time
	focusAssignMutex sync.Mutex
}

func NewMidiClient(paClient *pulseaudio.PAClient, device configuration.MidiDevice, rules []configuration.Rule, configManager *configuration.ConfigManager) *MidiClient {
	return &MidiClient{
		log:            log.With().Str("module", "Midi").Str("device", device.Name).Logger(),
		PAClient:       paClient,
		MidiDevice:     device,
		rules:          rules,
		ConfigManager:  configManager,
		volumeChannels: make(map[string]chan VolumeRequest),
	}
}

func (client *MidiClient) getRules() []configuration.Rule {
	client.rulesMutex.RLock()
	defer client.rulesMutex.RUnlock()
	return client.rules
}

// UpdateRules updates the rules for the MIDI client dynamically.
// The rules slice must not be modified after it is passed in.
func (client *MidiClient) UpdateRules(rules []configuration.Rule) {
	client.rulesMutex.Lock()
	previousCount := len(client.rules)
	client.rules = rules
	client.rulesMutex.Unlock()

	client.log.Info().Msgf("Updated MIDI rules - previous: %d, new: %d", previousCount, len(rules))
}

// getOrCreateVolumeChannel gets or creates a volume channel for a rule. Each channel
// has a dedicated worker, so slow PulseAudio calls for one control never delay another.
func (client *MidiClient) getOrCreateVolumeChannel(ruleKey string) chan VolumeRequest {
	client.channelsMutex.Lock()
	defer client.channelsMutex.Unlock()

	ch, exists := client.volumeChannels[ruleKey]
	if !exists {
		ch = make(chan VolumeRequest, 1) // Buffer size 1 for latest value
		client.volumeChannels[ruleKey] = ch
		go func() {
			for req := range ch {
				client.processVolumeRequest(req)
			}
		}()
	}
	return ch
}

// queueVolumeRequest hands the request to the rule's worker, replacing any request
// still waiting there so that only the latest value is applied
func (client *MidiClient) queueVolumeRequest(req VolumeRequest) {
	ch := client.getOrCreateVolumeChannel(req.Rule.MidiMessage.DeviceControlPath)
	for {
		select {
		case ch <- req:
			return
		default:
		}
		select {
		case <-ch: // Drop the stale value
		default:
		}
	}
}

// processVolumeRequest handles a single volume request
func (client *MidiClient) processVolumeRequest(req VolumeRequest) {
	client.log.Debug().Msgf("Processing volume request for rule: %s", req.Rule.MidiMessage.DeviceControlPath)

	minValue := req.Rule.MidiMessage.MinValue
	maxValue := req.Rule.MidiMessage.MaxValue
	if maxValue == 0 {
		maxValue = 0x7f
	}
	value := min(max(req.Value, minValue), maxValue)
	volumePercent := float32(value-minValue) / float32(maxValue-minValue)

	var volumeActions []configuration.Action
	for _, action := range req.Rule.Actions {
		switch action.Type {
		case configuration.SetVolume:
			volumeActions = append(volumeActions, action)
		case configuration.SetDefaultOutput:
			if req.Value == 0 {
				continue
			}
			if err := client.PAClient.SetDefaultOutput(action); err != nil {
				client.log.Error().Err(err).Msg("Failed to set default output")
			}
		default:
			client.log.Error().Msgf("Unknown action type %s in rule %+v", action.Type, req.Rule)
		}
	}

	if len(volumeActions) == 0 {
		return
	}
	client.log.Debug().
		Str("path", req.Rule.MidiMessage.DeviceControlPath).
		Int("targets", len(volumeActions)).
		Float32("volume", volumePercent).
		Msg("Setting volume")
	if err := client.PAClient.ProcessVolumeActions(volumeActions, volumePercent); err != nil {
		client.log.Error().Err(err).Msg("Failed to set volume")
	}
}

// doActions executes the actions of a matched rule
func (client *MidiClient) doActions(rule configuration.Rule, value uint8) {
	client.log.Debug().Msgf("Received action for rule: %s", rule.MidiMessage.DeviceControlPath)

	// Volume rules go through the per-control worker for coalescing
	if lo.ContainsBy(rule.Actions, func(action configuration.Action) bool {
		return action.Type == configuration.SetVolume
	}) {
		client.queueVolumeRequest(VolumeRequest{
			Rule:      rule,
			Value:     value,
			Timestamp: time.Now(),
		})
		return
	}

	// Handle non-volume actions immediately. Button actions only trigger on
	// press, not release, and slow ones run in the background so they don't
	// hold up MIDI input.
	for _, action := range rule.Actions {
		switch action.Type {
		case configuration.SetDefaultOutput:
			if value == 0 {
				continue
			}
			if err := client.PAClient.SetDefaultOutput(action); err != nil {
				client.log.Error().Err(err).Msg("Failed to set default output")
			}
		case configuration.MediaPlayPause:
			if value == 0 {
				continue
			}
			go func() {
				if err := client.PAClient.ProcessMediaControlAction(action); err != nil {
					client.log.Error().Err(err).Msg("Failed to process media control action")
				}
			}()
		case configuration.AssignFocusedWindowPlaybackStreams:
			if value == 0 {
				continue
			}
			go func() {
				if err := client.assignFocusedWindowPlaybackStreams(action); err != nil {
					client.log.Error().Err(err).Msg("Failed to assign focused window playback streams")
				}
			}()
		default:
			client.log.Error().Msgf("Unknown action type %s in rule %+v", action.Type, rule)
		}
	}
}

func (client *MidiClient) assignFocusedWindowPlaybackStreams(action configuration.Action) error {
	if client.ConfigManager == nil {
		return fmt.Errorf("no config manager available")
	}

	target, ok := action.Target.(*configuration.ControlTarget)
	if !ok || target == nil {
		return fmt.Errorf("invalid control target for focused window assignment")
	}

	if !client.focusAssignMutex.TryLock() {
		client.log.Debug().Msg("Focused window assignment already in progress, ignoring button press")
		return nil
	}
	defer client.focusAssignMutex.Unlock()

	sources, err := client.PAClient.FocusedWindowPlaybackSources()
	if err != nil {
		return err
	}

	client.ConfigManager.AssignSources(target.ControlType, target.ControlID, sources...)

	client.log.Info().
		Str("controlType", target.ControlType).
		Str("controlID", target.ControlID).
		Int("sources", len(sources)).
		Msg("Assigned focused window playback streams to control")

	return nil
}

// UpdateLEDIndicators updates the LED indicators based on current configuration
func (client *MidiClient) UpdateLEDIndicators() error {
	if client.MidiDevice.Type != configuration.KorgNanoKontrol2 {
		return nil // Only support nanoKONTROL2
	}

	if client.ConfigManager == nil {
		return fmt.Errorf("no config manager available")
	}

	// Gather state before taking the LED lock so it is only held while writing MIDI
	config := client.ConfigManager.GetConfig()
	sources := client.PAClient.GetAudioSources()

	client.ledMutex.Lock()
	defer client.ledMutex.Unlock()

	if client.nanoDevice == nil || client.midiOut == nil {
		return fmt.Errorf("MIDI device not initialized")
	}

	if err := client.nanoDevice.UpdateSourceIndicatorLEDs(client.midiOut, config, sources); err != nil {
		return fmt.Errorf("failed to update LED indicators: %w", err)
	}

	client.log.Debug().Msg("Updated LED indicators")
	return nil
}

// UpdatePlayButtonLED updates only the play button LED based on media status
func (client *MidiClient) UpdatePlayButtonLED(isPlaying bool) error {
	client.ledMutex.Lock()
	defer client.ledMutex.Unlock()

	if client.nanoDevice == nil || client.midiOut == nil {
		return fmt.Errorf("MIDI device not initialized")
	}

	// Enable external LED mode first (in case device was power cycled)
	if err := client.nanoDevice.EnableExternalLEDMode(client.midiOut); err != nil {
		return fmt.Errorf("failed to enable external LED mode for play button: %w", err)
	}

	// Control Play button LED (controller 41) based on media status
	playController := uint8(41) // Play button controller number
	if err := client.nanoDevice.SetButtonLED(client.midiOut, playController, isPlaying); err != nil {
		return fmt.Errorf("failed to set play button LED: %w", err)
	}

	client.log.Debug().Msgf("Play button LED updated: %v", isPlaying)
	return nil
}

// handleControlChange updates the stored control value and runs the matching rules
func (client *MidiClient) handleControlChange(channel, controller, ccValue uint8) {
	client.log.Debug().Msgf("CC message: channel=%d, controller=%d, value=%d", channel, controller, ccValue)

	// First, update config values for sliders and knobs
	if client.ConfigManager != nil && client.MidiDevice.Type == configuration.KorgNanoKontrol2 {
		// Convert 0-127 MIDI value to 0-100 percentage
		value := int((float64(ccValue) / 127.0) * 100.0)

		// Directly map controller numbers for the nanoKONTROL2 in default mode.
		// This is more reliable than trying to match rules.
		switch {
		case controller <= 7: // Sliders: controllers 0-7 → slider1-8
			client.ConfigManager.UpdateControlValue("slider", fmt.Sprintf("slider%d", controller+1), value)
		case controller >= 16 && controller <= 23: // Knobs: controllers 16-23 → knob1-8
			client.ConfigManager.UpdateControlValue("knob", fmt.Sprintf("knob%d", controller-16+1), value)
		}
	}

	// Then, perform actions based on rules
	for _, rule := range client.getRules() {
		if rule.MidiMessage.Type == configuration.ControlChange &&
			rule.MidiMessage.Channel == channel &&
			rule.MidiMessage.Controller == controller {
			client.doActions(rule, ccValue)
		}
	}
}

func (client *MidiClient) handleNote(channel, note, velocity uint8) {
	client.log.Debug().Msgf("Note message: channel=%d, note=%d, velocity=%d", channel, note, velocity)

	for _, rule := range client.getRules() {
		if rule.MidiMessage.Type == configuration.Note &&
			rule.MidiMessage.Channel == channel &&
			rule.MidiMessage.Note == note {
			client.doActions(rule, velocity)
		}
	}
}

func (client *MidiClient) handleProgramChange(channel, program uint8) {
	for _, rule := range client.getRules() {
		if rule.MidiMessage.Type == configuration.ProgramChange &&
			rule.MidiMessage.Channel == channel &&
			rule.MidiMessage.Program == program {
			client.doActions(rule, 0x7f)
		}
	}
}

func (client *MidiClient) onMessage(sysExChannel chan []byte) func(msg midi.Message, timestampMs int32) {
	return func(message midi.Message, timestampMs int32) {
		var channel, key, value uint8
		switch {
		case message.GetNoteOn(&channel, &key, &value):
			client.handleNote(channel, key, value)
		case message.GetNoteOff(&channel, &key, &value):
			client.handleNote(channel, key, 0)
		case message.GetControlChange(&channel, &key, &value):
			client.handleControlChange(channel, key, value)
		case message.GetProgramChange(&channel, &key):
			client.handleProgramChange(channel, key)
		default:
			var bytes []byte
			if !message.GetSysEx(&bytes) {
				return
			}
			select {
			case sysExChannel <- bytes:
			default:
				client.log.Debug().Msgf("Dropped unsolicited SysEx message: % X", bytes)
			}
		}
	}
}

func (client *MidiClient) Run() error {
	drv, err := driver.New()
	if err != nil {
		return fmt.Errorf("failed to create MIDI driver: %w", err)
	}

	// make sure to close all open ports at the end
	defer drv.Close()

	in, err := midi.FindInPort(client.MidiDevice.MidiInName)
	if err != nil {
		return fmt.Errorf("could not find MIDI In %s: %w", client.MidiDevice.MidiInName, err)
	}

	out, err := midi.FindOutPort(client.MidiDevice.MidiOutName)
	if err != nil {
		return fmt.Errorf("could not find MIDI Out %s: %w", client.MidiDevice.MidiOutName, err)
	}

	if err := in.Open(); err != nil {
		return fmt.Errorf("could not open MIDI In %s: %w", client.MidiDevice.MidiInName, err)
	}
	defer in.Close()

	if err := out.Open(); err != nil {
		return fmt.Errorf("could not open MIDI Out %s: %w", client.MidiDevice.MidiOutName, err)
	}
	defer out.Close()

	sysExChannel := make(chan []byte, sysExBufferSize)

	if _, err = midi.ListenTo(in, client.onMessage(sysExChannel), midi.UseSysEx()); err != nil {
		return fmt.Errorf("could not listen to MIDI In %s: %w", client.MidiDevice.MidiInName, err)
	}

	// Only support KORG nanoKONTROL2
	if client.MidiDevice.Type == configuration.KorgNanoKontrol2 {
		device := korgNanokontrol2.New(client.MidiDevice.Name)

		// Enable external LED mode FIRST, before reading scene data.
		// This is critical because enabling LED mode changes the device's
		// MIDI channel from 0 to 15. If we read scene data before enabling
		// LED mode, we get the wrong channel configuration.
		// Use the WithChannel variant to consume the acknowledgment response.
		if err := device.EnableExternalLEDModeWithChannel(sysExChannel, out); err != nil {
			client.log.Warn().Err(err).Msg("Failed to enable external LED mode before scene read")
		}

		// Drain any stale SysEx messages that may have queued up from previous sessions.
		// The device can send multiple LED mode acknowledgments if it was in an odd state.
		device.DrainSysExChannel(sysExChannel, 100*time.Millisecond)

		// Now read scene data and update rules with correct channel info
		client.UpdateRules(device.UpdateRules(client.getRules(), sysExChannel, out))

		// Store references for LED control
		client.ledMutex.Lock()
		client.midiOut = out
		client.nanoDevice = device
		client.ledMutex.Unlock()

		// Initialize LED indicators based on current configuration
		if err := client.UpdateLEDIndicators(); err != nil {
			client.log.Error().Err(err).Msg("Failed to initialize LED indicators")
		}
	}

	select {}
}
