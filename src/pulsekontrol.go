package pulsekontrol

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/0h41/pulsekontrol/src/configuration"
	"github.com/0h41/pulsekontrol/src/midi"
	"github.com/0h41/pulsekontrol/src/pulseaudio"
	"github.com/0h41/pulsekontrol/src/webui"
	"github.com/DavidGamba/go-getoptions"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	commit    string
	version   string
	buildTime string
)

// nanoKONTROL2 uses channel 0 in internal mode, channel 15 in external (LED) mode
const nanoKontrol2Channel = 15

func Run() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})

	// Parse command line
	opt := getoptions.New()
	opt.Self("", "Control your PulseAudio mixer with MIDI controller(s)")
	opt.HelpSynopsisArg("", "")
	opt.HelpCommand("help", opt.Alias("h"), opt.Description("Show this help"))
	opt.Bool("list", false, opt.Alias("l"), opt.Description("List MIDI ports & PulseAudio objects"))
	opt.Bool("list-midi", false, opt.Alias("m"), opt.Description("List MIDI ports"))
	opt.Bool("list-pulse", false, opt.Alias("p"), opt.Description("List PulseAudio objects"))
	opt.Bool("list-pulse-detailed", false, opt.Description("List PulseAudio objects with detailed properties"))
	opt.Bool("version", false, opt.Alias("v"), opt.Description("Show version"))
	opt.Bool("no-webui", false, opt.Description("Disable web interface"))
	opt.Bool("debug", false, opt.Alias("d"), opt.Description("Enable debug logging"))
	webAddr := opt.StringOptional("web-addr", "127.0.0.1:6080", opt.Description("Web interface address:port"))
	opt.Parse(os.Args[1:])
	if opt.Called("help") {
		fmt.Fprint(os.Stderr, opt.Help())
		os.Exit(0)
	}
	if opt.Called("version") {
		fmt.Printf("Version %s, commit %s, built on %s\n", version, commit, buildTime)
		os.Exit(0)
	}

	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if opt.Called("debug") {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}

	if opt.Called("list-midi") {
		midi.List()
		os.Exit(0)
	}

	// Create PulseAudio client
	paClient := pulseaudio.NewPAClient()

	if opt.Called("list") {
		midi.List()
		paClient.List()
		os.Exit(0)
	}
	if opt.Called("list-pulse") {
		paClient.List()
		os.Exit(0)
	}
	if opt.Called("list-pulse-detailed") {
		paClient.ListDetailed()
		os.Exit(0)
	}

	// Configuration
	config, path, err := configuration.Load()
	if err != nil {
		log.Error().Msgf("Configuration error %+v", err)
		os.Exit(1)
	}
	log.Info().Msgf("Loaded configuration from %s", path)

	// Create configuration manager
	configManager := configuration.NewConfigManager(config, path)

	// Start web UI if enabled
	if !opt.Called("no-webui") {
		webServer := webui.NewWebUIServer(*webAddr, paClient, configManager)

		// Fast path for control value updates
		configManager.Subscribe(configuration.TopicControlValueUpdated, func(data interface{}) {
			update, ok := data.(map[string]interface{})
			if !ok {
				return
			}
			controlType, _ := update["type"].(string)
			controlId, _ := update["id"].(string)
			value, _ := update["value"].(int)
			webServer.NotifyControlValueUpdate(controlType, controlId, value)
		})

		go func() {
			if err := webServer.Start(); err != nil {
				log.Error().Err(err).Msg("Failed to start web server")
			}
		}()
		log.Info().Msgf("Web interface available at http://%s", *webAddr)
	}

	// Convert new config format to legacy format for MIDI client
	// This is temporary compatibility code until the MIDI client is updated
	midiDevice := configuration.MidiDevice{
		Name:        config.Device.Name,
		Type:        configuration.KorgNanoKontrol2, // Only support KORG nanoKONTROL2
		MidiInName:  config.Device.InPort,
		MidiOutName: config.Device.OutPort,
	}

	// Create MIDI client with rules from control assignments
	midiClient := midi.NewMidiClient(paClient, midiDevice, createRulesFromConfig(config, midiDevice), configManager)

	// Immediately set the volume of a newly assigned source to match the control's current value
	configManager.Subscribe(configuration.TopicSourceAssigned, func(data interface{}) {
		assignData, ok := data.(map[string]interface{})
		if !ok {
			log.Error().Msg("Invalid data format from source.assigned event")
			return
		}
		initialValue, hasValue := assignData["initialValue"].(int)
		source, hasSource := assignData["source"].(configuration.Source)
		if !hasValue || !hasSource {
			return
		}

		log.Info().
			Str("sourceName", source.Name).
			Str("sourceType", string(source.Type)).
			Int("value", initialValue).
			Msg("Setting initial volume for newly assigned source")

		if err := paClient.ProcessVolumeAction(volumeAction(source), float32(initialValue)/100.0); err != nil {
			log.Error().Err(err).Msg("Failed to set initial volume for newly assigned source")
		}
	})

	// Regenerate rules and LEDs once per assignment change
	configManager.Subscribe(configuration.TopicAssignmentsChanged, func(interface{}) {
		midiClient.UpdateRules(createRulesFromConfig(configManager.GetConfig(), midiDevice))

		if err := midiClient.UpdateLEDIndicators(); err != nil {
			log.Error().Err(err).Msg("Failed to update LED indicators after assignment change")
		}
	})

	go func() {
		if err := midiClient.Run(); err != nil {
			log.Error().Err(err).Msg("MIDI client failed")
			configManager.Flush()
			os.Exit(1)
		}
	}()

	// Perform any needed config migrations and sync initial volumes to control positions
	applyControlVolumes(paClient, configManager, nil)

	// Set up stream monitoring for automatic volume application and LED updates
	setupStreamMonitoring(paClient, configManager, midiClient)

	// Wait for a termination signal and shut down cleanly
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigChan
	log.Info().Msgf("Received signal %s, shutting down...", sig)

	paClient.StopStreamMonitoring()
	configManager.Flush()
}

// Helper to extract a group number from a control path
func extractGroupNumber(path string) (int, error) {
	var groupNumber int

	// Try to parse "GroupX/..." format
	_, err := fmt.Sscanf(path, "Group%d/", &groupNumber)
	if err != nil {
		return 0, fmt.Errorf("failed to parse group number from path %s: %w", path, err)
	}
	if groupNumber < 1 || groupNumber > 8 {
		return 0, fmt.Errorf("group number %d out of range in path %s", groupNumber, path)
	}

	return groupNumber, nil
}

func volumeAction(source configuration.Source) configuration.Action {
	return configuration.Action{
		Type: configuration.SetVolume,
		Target: &configuration.TypedTarget{
			Type:       source.Type,
			Name:       source.Name,
			BinaryName: source.BinaryName,
		},
	}
}

func controlChangeRule(midiDevice configuration.MidiDevice, path string, controller uint8, actions ...configuration.Action) configuration.Rule {
	return configuration.Rule{
		MidiMessage: configuration.MidiMessage{
			DeviceName:        midiDevice.Name,
			DeviceControlPath: path,
			Type:              configuration.ControlChange,
			Channel:           nanoKontrol2Channel,
			Controller:        controller,
		},
		Actions: actions,
	}
}

// volumeRules creates a volume rule for every control with assigned sources.
// The control of group N uses MIDI controller firstController+N-1.
func volumeRules(controls map[string]configuration.ControlConfig, midiDevice configuration.MidiDevice, firstController uint8) []configuration.Rule {
	var rules []configuration.Rule
	for _, control := range controls {
		if len(control.Sources) == 0 {
			continue
		}

		groupNumber, err := extractGroupNumber(control.Path)
		if err != nil {
			log.Error().Err(err).Str("path", control.Path).Msg("Failed to parse control path")
			continue
		}

		actions := make([]configuration.Action, 0, len(control.Sources))
		for _, source := range control.Sources {
			actions = append(actions, volumeAction(source))
		}

		controller := firstController + uint8(groupNumber-1)
		rules = append(rules, controlChangeRule(midiDevice, control.Path, controller, actions...))
		log.Debug().Msgf("Added rule for path %s with %d sources (controller=%d)", control.Path, len(control.Sources), controller)
	}
	return rules
}

// createRulesFromConfig generates MIDI rules from the current configuration
func createRulesFromConfig(config configuration.Config, midiDevice configuration.MidiDevice) []configuration.Rule {
	var rules []configuration.Rule

	// For nanoKONTROL2, slider controllers are 0-7 and knob controllers 16-23 for groups 1-8
	rules = append(rules, volumeRules(config.Controls.Sliders, midiDevice, 0)...)
	rules = append(rules, volumeRules(config.Controls.Knobs, midiDevice, 16)...)

	// Group buttons assign the focused window's playback streams:
	// Record (controllers 64-71) to the slider, Solo (controllers 32-39) to the knob
	for groupNumber := 1; groupNumber <= 8; groupNumber++ {
		group := uint8(groupNumber - 1)
		rules = append(rules,
			controlChangeRule(midiDevice, fmt.Sprintf("Group%d/Record", groupNumber), 64+group, configuration.Action{
				Type:   configuration.AssignFocusedWindowPlaybackStreams,
				Target: &configuration.ControlTarget{ControlType: "slider", ControlID: fmt.Sprintf("slider%d", groupNumber)},
			}),
			controlChangeRule(midiDevice, fmt.Sprintf("Group%d/Solo", groupNumber), 32+group, configuration.Action{
				Type:   configuration.AssignFocusedWindowPlaybackStreams,
				Target: &configuration.ControlTarget{ControlType: "knob", ControlID: fmt.Sprintf("knob%d", groupNumber)},
			}),
		)
	}

	// Transport buttons (hardcoded for now): Play is controller 41
	rules = append(rules, controlChangeRule(midiDevice, "Transport/Play", 41, configuration.Action{
		Type:   configuration.MediaPlayPause,
		Target: nil, // No specific target for media control
	}))

	return rules
}

// setupStreamMonitoring configures automatic volume application for new streams
func setupStreamMonitoring(paClient *pulseaudio.PAClient, configManager *configuration.ConfigManager, midiClient *midi.MidiClient) {
	// New streams get the volume of the control they are assigned to, and LEDs are updated
	paClient.SetNewStreamCallback(func(stream pulseaudio.Stream, streamType configuration.PulseAudioTargetType) {
		applyControlVolumes(paClient, configManager, &pulseaudio.AudioSource{
			Name:       stream.Name,
			BinaryName: stream.BinaryName,
			Type:       string(streamType),
		})

		if err := midiClient.UpdateLEDIndicators(); err != nil {
			log.Error().Err(err).Msg("Failed to update LED indicators after new stream detected")
		}
	})

	// Update LEDs when streams disappear
	paClient.SetRemovedStreamCallback(func(stream pulseaudio.Stream, streamType configuration.PulseAudioTargetType) {
		if err := midiClient.UpdateLEDIndicators(); err != nil {
			log.Error().Err(err).Msg("Failed to update LED indicators after stream removed")
		}
	})

	// Update the play button LED when media status changes
	paClient.SetMediaStatusCallback(func(isPlaying bool) {
		log.Info().
			Bool("isPlaying", isPlaying).
			Msg("Media status changed, updating play button LED")

		if err := midiClient.UpdatePlayButtonLED(isPlaying); err != nil {
			log.Error().Err(err).Msg("Failed to update play button LED after media status change")
		}
	})

	if err := paClient.StartStreamMonitoring(); err != nil {
		log.Error().Err(err).Msg("Failed to start stream monitoring")
		return
	}

	if err := paClient.StartMediaStatusMonitoring(); err != nil {
		log.Error().Err(err).Msg("Failed to start media status monitoring")
		return
	}

	log.Info().Msg("Stream monitoring enabled - new applications will automatically have volumes applied and LEDs updated")
}

// applyControlVolumes sets the volume of assigned sources to their control's position.
// Legacy sources without a binary name are migrated when a matching stream provides one.
// If onlyFor is non-nil, only sources matching that stream are processed.
func applyControlVolumes(paClient *pulseaudio.PAClient, configManager *configuration.ConfigManager, onlyFor *pulseaudio.AudioSource) {
	config := configManager.GetConfig()

	for _, controlType := range []string{"slider", "knob"} {
		controls := config.Controls.Sliders
		if controlType == "knob" {
			controls = config.Controls.Knobs
		}

		for controlId, control := range controls {
			var actions []configuration.Action
			for _, source := range control.Sources {
				if onlyFor != nil && !onlyFor.Matches(source) {
					continue
				}

				// Migrate legacy sources to include the binary name
				if source.BinaryName == "" {
					matchedStreams, migrationStream := paClient.SmartMatchStreams(source.Type, source.Name)
					if migrationStream != nil && len(matchedStreams) > 0 &&
						configManager.MigrateSourceBinaryName(controlType, controlId, source, migrationStream.BinaryName) {
						source.BinaryName = migrationStream.BinaryName
					}
				}

				actions = append(actions, volumeAction(source))
			}

			if len(actions) == 0 {
				continue
			}

			log.Debug().
				Str("control", controlId).
				Int("value", control.Value).
				Int("sources", len(actions)).
				Msg("Applying control volume")

			if err := paClient.ProcessVolumeActions(actions, float32(control.Value)/100.0); err != nil {
				log.Error().Err(err).Str("control", controlId).Msg("Failed to apply control volume")
			}
		}
	}
}
