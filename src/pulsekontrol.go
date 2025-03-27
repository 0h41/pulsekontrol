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

func Run() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})

	// Create PulseAudio client
	paClient := pulseaudio.NewPAClient()

	// Parse command line
	opt := getoptions.New()
	opt.Self("", "Control your PulseAudio mixer with MIDI controller(s)")
	opt.HelpSynopsisArg("", "")
	opt.HelpCommand("help", opt.Alias("h"), opt.Description("Show this help"))
	opt.Bool("list", false, opt.Alias("l"), opt.Description("List MIDI ports & PulseAudio objects"))
	opt.Bool("list-midi", false, opt.Alias("m"), opt.Description("List MIDI ports"))
	opt.Bool("list-pulse", false, opt.Alias("p"), opt.Description("List PulseAudio objects"))
	opt.Bool("version", false, opt.Alias("v"), opt.Description("Show version"))
	opt.Bool("no-webui", false, opt.Description("Disable web interface"))
	webAddr := opt.StringOptional("web-addr", "127.0.0.1:6080", opt.Description("Web interface address:port"))
	opt.Parse(os.Args[1:])
	if opt.Called("help") {
		fmt.Fprint(os.Stderr, opt.Help())
		os.Exit(0)
	}
	if opt.Called("list") {
		midi.List()
		paClient.List()
		os.Exit(0)
	}
	if opt.Called("list-midi") {
		midi.List()
		os.Exit(0)
	}
	if opt.Called("list-pulse") {
		paClient.List()
		os.Exit(0)
	}
	if opt.Called("version") {
		fmt.Printf("Version %s, commit %s, built on %s\n", version, commit, buildTime)
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
	var webServer *webui.WebUIServer
	if !opt.Called("no-webui") {
		webServer = webui.NewWebUIServer(*webAddr, paClient, configManager)

		// Set up configuration update notifications to WebUI
		configManager.Subscribe("mapping.updated", func(data interface{}) {
			// Convert to JSON and broadcast to clients
			// This is a simplified example - in a real implementation,
			// you would serialize the data and broadcast it
			webServer.NotifyConfigUpdate(data)
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

	// Create rules from control assignments
	rules := createRulesFromConfig(config, midiDevice)

	// Create MIDI client
	midiClients := make([]*midi.MidiClient, 0, 1)
	midiClient := midi.NewMidiClient(paClient, midiDevice, rules, configManager)
	midiClients = append(midiClients, midiClient)

	// Subscribe to configuration changes to update rules dynamically
	configManager.Subscribe("source.assigned", func(data interface{}) {
		// Regenerate rules when sources are assigned
		log.Info().Msg("Source assigned, updating MIDI rules")

		// Recreate rules from current configuration - get the latest config!
		currentConfig := configManager.GetConfig()
		newRules := createRulesFromConfig(*currentConfig, midiDevice)

		// Update the MIDI client with the new rules
		midiClient.UpdateRules(newRules)
	})

	configManager.Subscribe("source.unassigned", func(data interface{}) {
		// Regenerate rules when sources are unassigned
		log.Info().Msg("Source unassigned, updating MIDI rules")

		// Recreate rules from current configuration - get the latest config!
		currentConfig := configManager.GetConfig()
		newRules := createRulesFromConfig(*currentConfig, midiDevice)

		// Update the MIDI client with the new rules
		midiClient.UpdateRules(newRules)
	})

	go midiClient.Run()

	// Set up signal handling for graceful shutdown
	setupSignalHandling()

	// Wait for program to exit
	select {}
}

func setupSignalHandling() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		log.Info().Msgf("Received signal %s, shutting down...", sig)
		// TODO: Add cleanup logic here
		os.Exit(0)
	}()
}

// Helper to extract a group number from a control path
func extractGroupNumber(path string) (int, error) {
	var groupNumber int

	// Try to parse "GroupX/..." format
	_, err := fmt.Sscanf(path, "Group%d/", &groupNumber)
	if err != nil {
		return 0, fmt.Errorf("failed to parse group number from path %s: %w", path, err)
	}

	return groupNumber, nil
}

// createRulesFromConfig generates MIDI rules from the current configuration
func createRulesFromConfig(config configuration.Config, midiDevice configuration.MidiDevice) []configuration.Rule {
	var rules []configuration.Rule

	// Add slider rules
	for _, slider := range config.Controls.Sliders {
		if len(slider.Sources) > 0 {
			// Parse the slider path to get the group number
			groupNumber, err := extractGroupNumber(slider.Path)
			if err != nil {
				log.Error().Err(err).Str("path", slider.Path).Msg("Failed to parse slider path")
				continue
			}

			// For nanoKONTROL2, slider controllers are 0-7 for groups 1-8
			controller := uint8(groupNumber - 1)

			rule := configuration.Rule{
				MidiMessage: configuration.MidiMessage{
					DeviceName:        midiDevice.Name,
					DeviceControlPath: slider.Path,
					Type:              configuration.ControlChange,
					Channel:           0, // Default channel
					Controller:        controller,
				},
				Actions: []configuration.Action{},
			}

			// Add an action for each source
			for _, source := range slider.Sources {
				action := configuration.Action{
					Type: configuration.SetVolume,
					Target: &configuration.TypedTarget{
						Type: source.Type,
						Name: source.Name,
					},
				}
				rule.Actions = append(rule.Actions, action)
			}

			rules = append(rules, rule)
			log.Debug().
				Msgf("Added rule for slider path %s with %d sources (controller=%d)",
					slider.Path, len(slider.Sources), controller)
		}
	}

	// Add knob rules
	for _, knob := range config.Controls.Knobs {
		if len(knob.Sources) > 0 {
			// Parse the knob path to get the group number
			groupNumber, err := extractGroupNumber(knob.Path)
			if err != nil {
				log.Error().Err(err).Str("path", knob.Path).Msg("Failed to parse knob path")
				continue
			}

			// For nanoKONTROL2, knob controllers are 16-23 for groups 1-8
			controller := uint8(16 + groupNumber - 1)

			rule := configuration.Rule{
				MidiMessage: configuration.MidiMessage{
					DeviceName:        midiDevice.Name,
					DeviceControlPath: knob.Path,
					Type:              configuration.ControlChange,
					Channel:           0, // Default channel
					Controller:        controller,
				},
				Actions: []configuration.Action{},
			}

			// Add an action for each source
			for _, source := range knob.Sources {
				action := configuration.Action{
					Type: configuration.SetVolume,
					Target: &configuration.TypedTarget{
						Type: source.Type,
						Name: source.Name,
					},
				}
				rule.Actions = append(rule.Actions, action)
			}

			rules = append(rules, rule)
			log.Debug().
				Msgf("Added rule for knob path %s with %d sources (controller=%d)",
					knob.Path, len(knob.Sources), controller)
		}
	}

	return rules
}
