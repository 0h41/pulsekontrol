package pulsekontrol

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/DavidGamba/go-getoptions"
	"github.com/0h41/pulsekontrol/src/configuration"
	"github.com/0h41/pulsekontrol/src/midi"
	"github.com/0h41/pulsekontrol/src/pulseaudio"
	"github.com/0h41/pulsekontrol/src/webui"
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
		Type:        configuration.KorgNanoKontrol2,
		MidiInName:  config.Device.InPort,
		MidiOutName: config.Device.OutPort,
	}
	
	// Create rules from control assignments
	var rules []configuration.Rule
	
	// Add slider rules
	// First, add a hardcoded rule for the first slider to control Chromium
	firstSliderRule := configuration.Rule{
		MidiMessage: configuration.MidiMessage{
			DeviceName:        midiDevice.Name,
			DeviceControlPath: "Group1/Slider", // First slider
		},
		Actions: []configuration.Action{
			{
				Type: configuration.SetVolume,
				Target: &configuration.TypedTarget{
					Type: configuration.PlaybackStream,
					Name: "Chromium", // Hardcoded to control Chromium source
				},
			},
		},
	}
	rules = append(rules, firstSliderRule)
	log.Info().Msg("Hardcoded first slider to control volume of Chromium")
	
	// Then process the rest of the sliders (starting from second)
	sliderCount := 0
	for _, slider := range config.Controls.Sliders {
		sliderCount++
		if sliderCount == 1 {
			// Skip the first slider as we've already hardcoded it
			continue
		}
		
		if len(slider.Sources) > 0 {
			rule := configuration.Rule{
				MidiMessage: configuration.MidiMessage{
					DeviceName:        midiDevice.Name,
					DeviceControlPath: slider.Path,
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
		}
	}
	
	// Add knob rules
	for _, knob := range config.Controls.Knobs {
		if len(knob.Sources) > 0 {
			rule := configuration.Rule{
				MidiMessage: configuration.MidiMessage{
					DeviceName:        midiDevice.Name,
					DeviceControlPath: knob.Path,
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
		}
	}
	
	// Add button rules
	for _, button := range config.Controls.Buttons {
		rule := configuration.Rule{
			MidiMessage: configuration.MidiMessage{
				DeviceName:        midiDevice.Name,
				DeviceControlPath: button.Path,
			},
			Actions: []configuration.Action{},
		}
		
		// Convert action type
		var actionType configuration.PulseAudioActionType
		switch button.Action {
		case configuration.ToggleMuteAction:
			actionType = configuration.ToggleMute
		case configuration.SetDefaultOutputAction:
			actionType = configuration.SetDefaultOutput
		default:
			continue // Skip unsupported actions
		}
		
		// Add the action
		action := configuration.Action{
			Type: actionType,
		}
		
		// Add target based on action type
		if actionType == configuration.SetDefaultOutput {
			action.Target = &configuration.Target{
				Name: button.Target.Name,
			}
		} else {
			action.Target = &configuration.TypedTarget{
				Type: configuration.OutputDevice,
				Name: button.Target.Name,
			}
		}
		
		rule.Actions = append(rule.Actions, action)
		rules = append(rules, rule)
	}

	// Create MIDI client
	midiClients := make([]*midi.MidiClient, 0, 1)
	midiClient := midi.NewMidiClient(paClient, midiDevice, rules, configManager)
	midiClients = append(midiClients, midiClient)
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