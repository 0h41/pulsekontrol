package midi

import (
	"fmt"
	"github.com/0h41/pulsekontrol/src/configuration"
	akaiLpd8 "github.com/0h41/pulsekontrol/src/device/akai/lpd8"
	korgNanokontrol2 "github.com/0h41/pulsekontrol/src/device/korg/nanokontrol2"
	"github.com/0h41/pulsekontrol/src/pulseaudio"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
	"gitlab.com/gomidi/midi/v2"

	driver "gitlab.com/gomidi/midi/v2/drivers/portmididrv"
)

func listDevices() ([]string, []string, error) {
	drv, err := driver.New()
	if err != nil {
		panic(err)
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
	inNames := make([]string, 0)
	outNames := make([]string, 0)
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
		panic(err)
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

type MidiClient struct {
	log           zerolog.Logger
	PAClient      *pulseaudio.PAClient
	MidiDevice    configuration.MidiDevice
	Rules         []configuration.Rule
	ConfigManager *configuration.ConfigManager
}

func NewMidiClient(paClient *pulseaudio.PAClient, device configuration.MidiDevice, rules []configuration.Rule, configManager *configuration.ConfigManager) *MidiClient {
	client := &MidiClient{
		log:           log.With().Str("module", "Midi").Str("device", device.Name).Logger(),
		PAClient:      paClient,
		MidiDevice:    device,
		Rules:         rules,
		ConfigManager: configManager,
	}
	return client
}

// UpdateRules updates the rules for the MIDI client dynamically
func (client *MidiClient) UpdateRules(rules []configuration.Rule) {
	client.log.Info().Msgf("Updating MIDI rules - previous: %d, new: %d", len(client.Rules), len(rules))
	
	// Log the old rules for comparison
	for i, rule := range client.Rules {
		if rule.MidiMessage.DeviceControlPath != "" {
			client.log.Debug().Msgf("OLD rule[%d]: path=%s, actions=%d", 
				i, rule.MidiMessage.DeviceControlPath, len(rule.Actions))
			
			// Also log controller number which is critical for matching
			client.log.Debug().Msgf("  Controller=%d, Channel=%d", 
				rule.MidiMessage.Controller, rule.MidiMessage.Channel)
			
			for j, action := range rule.Actions {
				if target, ok := action.Target.(*configuration.TypedTarget); ok {
					client.log.Debug().Msgf("  OLD action[%d]: type=%s, target=%s:%s", 
						j, action.Type, target.Type, target.Name)
				}
			}
		}
	}
	
	// Log the new rules
	for i, rule := range rules {
		if rule.MidiMessage.DeviceControlPath != "" {
			client.log.Debug().Msgf("NEW rule[%d]: path=%s, actions=%d", 
				i, rule.MidiMessage.DeviceControlPath, len(rule.Actions))
			
			// Also log controller number which is critical for matching
			client.log.Debug().Msgf("  Controller=%d, Channel=%d", 
				rule.MidiMessage.Controller, rule.MidiMessage.Channel)
			
			for j, action := range rule.Actions {
				if target, ok := action.Target.(*configuration.TypedTarget); ok {
					client.log.Debug().Msgf("  NEW action[%d]: type=%s, target=%s:%s", 
						j, action.Type, target.Type, target.Name)
				}
			}
		}
	}
	
	// Just assign the new rules directly without device-specific updates
	// since they require hardware communication
	client.Rules = rules
	client.log.Info().Msg("Updated rules without requerying device")
}

func (client *MidiClient) Run() {
	drv, err := driver.New()
	if err != nil {
		panic(err)
	}

	// make sure to close all open ports at the end
	defer drv.Close()

	in, err := midi.FindInPort(client.MidiDevice.MidiInName)
	if err != nil {
		// panic(err)
		client.log.Error().Msgf("Could not find MIDI In %s", client.MidiDevice.MidiInName)
	}

	out, err := midi.FindOutPort(client.MidiDevice.MidiOutName)
	if err != nil {
		// panic(err)
		client.log.Error().Msgf("Could not find MIDI Out %s", client.MidiDevice.MidiInName)
	}

	if in == nil || out == nil {
		return
	}

	if err := in.Open(); err != nil {
		panic(err)
	}

	if err := out.Open(); err != nil {
		panic(err)
	}

	defer in.Close()
	defer out.Close()

	onMessage := func(sysExChannel chan []byte) func(msg midi.Message, timestampMs int32) {
		var doActions = func(rule configuration.Rule, value uint8) {
			client.log.Debug().Msgf("Executing actions for rule: %s", rule.MidiMessage.DeviceControlPath)
			for _, action := range rule.Actions {
				switch action.Type {
				case configuration.SetVolume:
					var minValue uint8
					var maxValue uint8
					if rule.MidiMessage.MinValue != 0 {
						minValue = rule.MidiMessage.MinValue
					} else {
						minValue = 0
					}
					if rule.MidiMessage.MaxValue != 0 {
						maxValue = rule.MidiMessage.MaxValue
					} else {
						maxValue = 0x7f
					}
					volumePercent := float32(value) / float32(maxValue-minValue)
					
					// Better logging of volume change
					if target, ok := action.Target.(*configuration.TypedTarget); ok {
						client.log.Debug().
							Str("target", target.Name).
							Str("type", string(target.Type)).
							Float32("volume", volumePercent).
							Msg("Setting volume")
					}
					
					if err := client.PAClient.ProcessVolumeAction(action, volumePercent); err != nil {
						client.log.Error().Err(err)
					}
				case configuration.ToggleMute:
					if value == 0 {
						return
					}
					if err := client.PAClient.ProcessToggleMute(action); err != nil {
						client.log.Error().Err(err)
					}
				case configuration.SetDefaultOutput:
					if value == 0 {
						return
					}
					if err := client.PAClient.SetDefaultOutput(action); err != nil {
						client.log.Error().Err(err)
					}
				default:
					client.log.Error().Msgf("Unknown action type %s in rule %+v", action.Type, rule)
				}
			}
		}
		return func(message midi.Message, timestampMs int32) {
			client.log.Debug().Msgf("Received MIDI message (%s) from in port %v", message.String(), in)
			switch message.Type() {
			case midi.NoteOnMsg, midi.NoteOffMsg:
				var channel uint8
				var note uint8
				var velocity uint8
				message.GetNoteOn(&channel, &note, &velocity)
				
				client.log.Debug().Msgf("Note message: channel=%d, note=%d, velocity=%d", 
					channel, note, velocity)
				
				rules := lo.Filter(client.Rules, func(rule configuration.Rule, i int) bool {
					match := rule.MidiMessage.Type == configuration.Note &&
						rule.MidiMessage.Channel == channel &&
						rule.MidiMessage.Note == note
						
					if match {
						client.log.Debug().Msgf("MATCHED note rule: %s", rule.MidiMessage.DeviceControlPath)
					}
					
					return match
				})
				
				client.log.Debug().Msgf("Found %d matching note rules", len(rules))
				
				for _, rule := range rules {
					doActions(rule, velocity)
				}
			case midi.ControlChangeMsg:
				var channel uint8
				var controller uint8
				var ccValue uint8
				message.GetControlChange(&channel, &controller, &ccValue)
				
				// Log more details about the MIDI message
				client.log.Debug().Msgf("CC message: channel=%d, controller=%d, value=%d", 
					channel, controller, ccValue)
				
				// Show all rules for debugging
				client.log.Debug().Msgf("Looking for matching rules among %d rules", len(client.Rules))
				
				rules := lo.Filter(client.Rules, func(rule configuration.Rule, i int) bool {
					match := rule.MidiMessage.Type == configuration.ControlChange &&
						rule.MidiMessage.Channel == channel &&
						rule.MidiMessage.Controller == controller
					
					// Additional detailed logging
					if rule.MidiMessage.DeviceControlPath != "" {
						if match {
							client.log.Debug().
								Str("path", rule.MidiMessage.DeviceControlPath).
								Uint8("rule_controller", rule.MidiMessage.Controller).
								Uint8("msg_controller", controller).
								Uint8("rule_channel", rule.MidiMessage.Channel).
								Uint8("msg_channel", channel).
								Msg("MATCHED CC rule")
						} else if controller < 100 { // Only log for relevant controllers to reduce noise
							client.log.Debug().
								Str("path", rule.MidiMessage.DeviceControlPath).
								Uint8("rule_controller", rule.MidiMessage.Controller).
								Uint8("msg_controller", controller).
								Uint8("rule_channel", rule.MidiMessage.Channel).
								Uint8("msg_channel", channel).
								Msg("Rule did NOT match")
						}
					}
					
					return match
				})
				
				client.log.Debug().Msgf("Found %d matching CC rules", len(rules))
				
				// First, update config values for sliders and knobs
				if client.ConfigManager != nil {
					// Convert 0-127 MIDI value to 0-100 percentage
					value := int((float64(ccValue) / 127.0) * 100.0)
					
					// Directly map controller numbers for the nanoKONTROL2
					// This is more reliable than trying to match rules
					if client.MidiDevice.Type == configuration.KorgNanoKontrol2 {
						// Standard mapping for nanoKONTROL2 in default mode
						// For sliders: controllers 0-7 correspond to sliders 1-8
						// For knobs: controllers 16-23 correspond to knobs 1-8
						
						if controller >= 0 && controller <= 7 {
							// This is a slider (0-7 → slider1-8)
							groupNumber := controller + 1
							controlId := fmt.Sprintf("slider%d", groupNumber)
							
							client.log.Debug().
								Str("controlId", controlId).
								Str("controlType", "slider").
								Int("value", value).
								Msg("Updating slider value from MIDI via direct mapping")
							
							client.ConfigManager.UpdateControlValue("slider", controlId, value)
						} else if controller >= 16 && controller <= 23 {
							// This is a knob (16-23 → knob1-8)
							groupNumber := controller - 16 + 1
							controlId := fmt.Sprintf("knob%d", groupNumber)
							
							client.log.Debug().
								Str("controlId", controlId).
								Str("controlType", "knob").
								Int("value", value).
								Msg("Updating knob value from MIDI via direct mapping")
							
							client.ConfigManager.UpdateControlValue("knob", controlId, value)
						}
					}
				}
				
				// Then, perform actions based on rules
				for _, rule := range rules {
					doActions(rule, ccValue)
				}
			case midi.ProgramChangeMsg:
				var channel uint8
				var program uint8
				message.GetProgramChange(&channel, &program)
				rules := lo.Filter(client.Rules, func(rule configuration.Rule, i int) bool {
					return rule.MidiMessage.Type == configuration.ProgramChange &&
						rule.MidiMessage.Channel == channel &&
						rule.MidiMessage.Program == program
				})
				for _, rule := range rules {
					doActions(rule, 0x7f)
				}
			case midi.SysExMsg:
				var bytes []byte
				message.GetSysEx(&bytes)
				sysExChannel <- bytes
			}
		}
	}

	sysExChannel := make(chan []byte)

	if _, err = midi.ListenTo(in, onMessage(sysExChannel), midi.UseSysEx()); err != nil {
		panic(err)
	}

	if client.MidiDevice.Type == configuration.AkaiLpd8 {
		device := akaiLpd8.New(client.MidiDevice.Name)
		// client.log.Info().Msgf("device %+v", device)
		// device.OnStart(sysExChannel, out)
		client.Rules = device.UpdateRules(client.Rules, sysExChannel, out)
	} else if client.MidiDevice.Type == configuration.KorgNanoKontrol2 {
		device := korgNanokontrol2.New(client.MidiDevice.Name)
		// client.log.Info().Msgf("device %+v", device)
		// device.OnStart(sysExChannel, out)
		client.Rules = device.UpdateRules(client.Rules, sysExChannel, out)
	}

	select {}
}