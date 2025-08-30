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
	opt.Bool("list-pulse-detailed", false, opt.Description("List PulseAudio objects with detailed properties"))
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
	if opt.Called("list-pulse-detailed") {
		paClient.ListDetailed()
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
		
		// Fast path for control value updates
		configManager.Subscribe("control.value.updated", func(data interface{}) {
			log.Debug().Interface("data", data).Msg("Received control.value.updated notification")
			if updateMap, ok := data.(map[string]interface{}); ok {
				if controlType, ok := updateMap["type"].(string); ok {
					if controlId, ok := updateMap["id"].(string); ok {
						if value, ok := updateMap["value"].(int); ok {
							log.Debug().Str("controlType", controlType).Str("controlId", controlId).Int("value", value).Msg("Sending fast path UI update")
							webServer.NotifyControlValueUpdate(controlType, controlId, value)
						}
					}
				}
			}
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

		// Extract assignment details
		assignData, ok := data.(map[string]interface{})
		if !ok {
			log.Error().Msg("Invalid data format from source.assigned event")
			return
		}

		// Immediately set the volume of the newly assigned source to match the control's current value
		if initialValue, hasValue := assignData["initialValue"].(int); hasValue {
			source, hasSource := assignData["source"].(configuration.Source)
			if hasSource {
				// Convert 0-100 value to 0.0-1.0 range for PulseAudio
				volumePercent := float32(initialValue) / 100.0
				
				// Create a temporary action to set the volume
				action := configuration.Action{
					Type: configuration.SetVolume,
					Target: &configuration.TypedTarget{
						Type: source.Type,
						Name: source.Name,
						BinaryName: source.BinaryName,
					},
				}
				
				// Process the volume action immediately
				log.Info().
					Str("sourceName", source.Name).
					Str("sourceType", string(source.Type)).
					Int("value", initialValue).
					Msg("Setting initial volume for newly assigned source")
					
				paClient.ProcessVolumeAction(action, volumePercent)
			}
		}

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

	// Trigger initial volume actions to perform any needed config migrations
	// and sync initial volumes to control positions
	triggerStartupVolumeActions(paClient, configManager)

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
				log.Debug().
					Str("sourceName", source.Name).
					Str("sourceBinaryName", source.BinaryName).
					Str("sourceType", string(source.Type)).
					Msg("Creating action for slider source")
				action := configuration.Action{
					Type: configuration.SetVolume,
					Target: &configuration.TypedTarget{
						Type: source.Type,
						Name: source.Name,
						BinaryName: source.BinaryName,
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
				log.Debug().
					Str("sourceName", source.Name).
					Str("sourceBinaryName", source.BinaryName).
					Str("sourceType", string(source.Type)).
					Msg("Creating action for knob source")
				action := configuration.Action{
					Type: configuration.SetVolume,
					Target: &configuration.TypedTarget{
						Type: source.Type,
						Name: source.Name,
						BinaryName: source.BinaryName,
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

// triggerStartupVolumeActions processes all slider/knob assignments at startup
// This triggers migration logic and syncs volumes to control positions
func triggerStartupVolumeActions(paClient *pulseaudio.PAClient, configManager *configuration.ConfigManager) {
	config := *configManager.GetConfig()
	log.Info().Msg("Processing startup volume actions for migration and sync")

	// Process all sliders
	for controlID, slider := range config.Controls.Sliders {
		if len(slider.Sources) > 0 {
			volumePercent := float32(slider.Value) / 100.0
			log.Debug().
				Str("control", controlID).
				Int("value", slider.Value).
				Int("sources", len(slider.Sources)).
				Msg("Processing startup slider")
			
			for _, source := range slider.Sources {
				// Check if migration is needed before processing
				if source.BinaryName == "" {
					// This is a legacy config - check if we need to migrate
					matchedStreams, migrationStream := paClient.SmartMatchStreams(source.Type, source.Name)
					if migrationStream != nil && len(matchedStreams) > 0 {
						// Perform migration immediately
						configManager.MigrateSourceBinaryName("slider", controlID, source.Type, source.Name, migrationStream.BinaryName)
						// Get updated config after migration
						config = *configManager.GetConfig()
						log.Info().Str("control", controlID).Str("source", source.Name).Str("binary", migrationStream.BinaryName).Msg("Performed migration during startup")
						continue // Skip to next source - the migrated source will be processed in the updated config
					}
				}
				
				action := configuration.Action{
					Type: configuration.SetVolume,
					Target: &configuration.TypedTarget{
						Type: source.Type,
						Name: source.Name,
						BinaryName: source.BinaryName,
					},
				}
				paClient.ProcessVolumeAction(action, volumePercent)
			}
		}
	}

	// Process all knobs  
	for controlID, knob := range config.Controls.Knobs {
		if len(knob.Sources) > 0 {
			volumePercent := float32(knob.Value) / 100.0
			log.Debug().
				Str("control", controlID).
				Int("value", knob.Value).
				Int("sources", len(knob.Sources)).
				Msg("Processing startup knob")
				
			for _, source := range knob.Sources {
				// Check if migration is needed before processing
				if source.BinaryName == "" {
					// This is a legacy config - check if we need to migrate
					matchedStreams, migrationStream := paClient.SmartMatchStreams(source.Type, source.Name)
					if migrationStream != nil && len(matchedStreams) > 0 {
						// Perform migration immediately
						configManager.MigrateSourceBinaryName("knob", controlID, source.Type, source.Name, migrationStream.BinaryName)
						// Get updated config after migration
						config = *configManager.GetConfig()
						log.Info().Str("control", controlID).Str("source", source.Name).Str("binary", migrationStream.BinaryName).Msg("Performed migration during startup")
						continue // Skip to next source - the migrated source will be processed in the updated config
					}
				}
				
				action := configuration.Action{
					Type: configuration.SetVolume,
					Target: &configuration.TypedTarget{
						Type: source.Type,
						Name: source.Name,
						BinaryName: source.BinaryName,
					},
				}
				paClient.ProcessVolumeAction(action, volumePercent)
			}
		}
	}
}
