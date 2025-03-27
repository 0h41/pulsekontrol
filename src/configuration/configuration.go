package configuration

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Default KORG nanoKONTROL2 configuration
func GetDefaultConfig() Config {
	return Config{
		Device: DeviceConfig{
			Name:    "KORG nanoKONTROL2",
			InPort:  "nanoKONTROL2 nanoKONTROL2 _ CTR",
			OutPort: "nanoKONTROL2 nanoKONTROL2 _ CTR",
		},
		Controls: Controls{
			Sliders: map[string]SliderConfig{
				"slider1": {
					Path:    "Group1/Slider",
					Value:   50,
					Sources: []Source{},
				},
				"slider2": {
					Path:    "Group2/Slider",
					Value:   50,
					Sources: []Source{},
				},
				"slider3": {
					Path:    "Group3/Slider",
					Value:   50,
					Sources: []Source{},
				},
				"slider4": {
					Path:    "Group4/Slider",
					Value:   50,
					Sources: []Source{},
				},
				"slider5": {
					Path:    "Group5/Slider",
					Value:   50,
					Sources: []Source{},
				},
				"slider6": {
					Path:    "Group6/Slider",
					Value:   50,
					Sources: []Source{},
				},
				"slider7": {
					Path:    "Group7/Slider",
					Value:   50,
					Sources: []Source{},
				},
				"slider8": {
					Path:    "Group8/Slider",
					Value:   50,
					Sources: []Source{},
				},
			},
			Knobs: map[string]KnobConfig{
				"knob1": {
					Path:    "Group1/Knob",
					Value:   50,
					Sources: []Source{},
				},
				"knob2": {
					Path:    "Group2/Knob",
					Value:   50,
					Sources: []Source{},
				},
				"knob3": {
					Path:    "Group3/Knob",
					Value:   50,
					Sources: []Source{},
				},
				"knob4": {
					Path:    "Group4/Knob",
					Value:   50,
					Sources: []Source{},
				},
				"knob5": {
					Path:    "Group5/Knob",
					Value:   50,
					Sources: []Source{},
				},
				"knob6": {
					Path:    "Group6/Knob",
					Value:   50,
					Sources: []Source{},
				},
				"knob7": {
					Path:    "Group7/Knob",
					Value:   50,
					Sources: []Source{},
				},
				"knob8": {
					Path:    "Group8/Knob",
					Value:   50,
					Sources: []Source{},
				},
			},
		},
	}
}

func Load() (Config, string, error) {
	var configPath string
	var content []byte
	var config Config

	// Read configuration file
	homeDir, _ := os.UserHomeDir()
	paths := [...]string{
		"./config.yaml",
		fmt.Sprintf("%s/.config/pulsekontrol/config.yaml", homeDir),
	}

	// Try to read from config paths
	for _, path := range paths {
		if content != nil {
			break
		}
		fileContent, err := os.ReadFile(path)
		if err == nil {
			configPath = path
			content = fileContent
		}
	}

	// If no config found, create a default one
	if content == nil {
		config = GetDefaultConfig()

		// Write the default config to the home directory path
		configPath = paths[1]

		// Ensure directory exists
		configDir := fmt.Sprintf("%s/.config/pulsekontrol", homeDir)
		if err := os.MkdirAll(configDir, 0755); err != nil {
			return config, "", fmt.Errorf("could not create config directory: %w", err)
		}

		// Marshal and save the default config
		data, err := yaml.Marshal(config)
		if err != nil {
			return config, "", fmt.Errorf("failed to marshal default config: %w", err)
		}

		if err := os.WriteFile(configPath, data, 0644); err != nil {
			return config, "", fmt.Errorf("failed to write default config: %w", err)
		}

		return config, configPath, nil
	}

	// First try parsing as new format
	err := yaml.Unmarshal(content, &config)
	if err == nil && config.Device.Name != "" {
		// Looks like the new format
		ensureDefaults(&config)
		return config, configPath, nil
	}

	// If that fails, try as legacy format
	var legacyConfig LegacyConfig
	if err := yaml.Unmarshal(content, &legacyConfig); err != nil {
		return GetDefaultConfig(), configPath, fmt.Errorf("error parsing config: %w", err)
	}

	// Convert legacy format to new format
	config = convertLegacyConfig(legacyConfig)

	// Set defaults for any missing fields
	ensureDefaults(&config)

	// Save in new format
	data, err := yaml.Marshal(config)
	if err == nil {
		// Create backup of old format
		backupPath := configPath + ".legacy"
		os.Rename(configPath, backupPath)

		// Write new format
		os.WriteFile(configPath, data, 0644)
	}

	return config, configPath, nil
}

// Convert legacy config format to new format
func convertLegacyConfig(legacyConfig LegacyConfig) Config {
	config := GetDefaultConfig()

	// Process only the first MIDI device (as we're limiting to nanoKONTROL2)
	if len(legacyConfig.MidiDevices) > 0 {
		device := legacyConfig.MidiDevices[0]
		if device.Type == KorgNanoKontrol2 {
			config.Device.Name = device.Name
			config.Device.InPort = device.MidiInName
			config.Device.OutPort = device.MidiOutName
		}
	}

	// Extract control assignments from the rules
	for _, rule := range legacyConfig.Rules {
		if rule.MidiMessage.DeviceControlPath != "" {
			// Extract path parts (e.g., "Group1/Slider" -> "Group1", "Slider")
			parts := strings.Split(rule.MidiMessage.DeviceControlPath, "/")
			if len(parts) != 2 {
				continue
			}

			// Skip non-standard controls for now
			if !strings.HasPrefix(parts[0], "Group") {
				continue
			}

			// Parse the group number
			var groupNum int
			_, err := fmt.Sscanf(parts[0], "Group%d", &groupNum)
			if err != nil || groupNum < 1 || groupNum > 8 {
				continue
			}

			controlPath := rule.MidiMessage.DeviceControlPath
			controlType := parts[1]
			controlId := fmt.Sprintf("%s%d", strings.ToLower(controlType), groupNum)

			// Handle different control types
			switch controlType {
			case "Slider":
				slider := config.Controls.Sliders[controlId]
				slider.Path = controlPath

				// Add sources from actions
				for _, action := range rule.Actions {
					if action.Type == SetVolume && action.Target != nil {
						if typedTarget, ok := action.Target.(*TypedTarget); ok {
							source := Source{
								Type: typedTarget.Type,
								Name: typedTarget.Name,
							}
							slider.Sources = append(slider.Sources, source)
						}
					}
				}

				config.Controls.Sliders[controlId] = slider

			case "Knob":
				knob := config.Controls.Knobs[controlId]
				knob.Path = controlPath

				// Add sources from actions
				for _, action := range rule.Actions {
					if action.Type == SetVolume && action.Target != nil {
						if typedTarget, ok := action.Target.(*TypedTarget); ok {
							source := Source{
								Type: typedTarget.Type,
								Name: typedTarget.Name,
							}
							knob.Sources = append(knob.Sources, source)
						}
					}
				}

				config.Controls.Knobs[controlId] = knob
			}
		}
	}

	return config
}

// Set default values for any missing parts of the config
func ensureDefaults(config *Config) {
	// Ensure device settings
	if config.Device.Name == "" {
		config.Device.Name = "KORG nanoKONTROL2"
	}
	if config.Device.InPort == "" {
		config.Device.InPort = "nanoKONTROL2 nanoKONTROL2 _ CTR"
	}
	if config.Device.OutPort == "" {
		config.Device.OutPort = "nanoKONTROL2 nanoKONTROL2 _ CTR"
	}

	// Initialize maps if they're nil
	if config.Controls.Sliders == nil {
		config.Controls.Sliders = make(map[string]SliderConfig)
	}
	if config.Controls.Knobs == nil {
		config.Controls.Knobs = make(map[string]KnobConfig)
	}

	// Add default sliders if missing
	defaultConfig := GetDefaultConfig()
	for id, slider := range defaultConfig.Controls.Sliders {
		if _, exists := config.Controls.Sliders[id]; !exists {
			config.Controls.Sliders[id] = slider
		}
	}

	// Add default knobs if missing
	for id, knob := range defaultConfig.Controls.Knobs {
		if _, exists := config.Controls.Knobs[id]; !exists {
			config.Controls.Knobs[id] = knob
		}
	}
}
