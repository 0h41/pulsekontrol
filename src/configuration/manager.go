package configuration

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

// ConfigManager handles the runtime configuration with persistence
type ConfigManager struct {
	config        *Config
	configPath    string
	saveMutex     sync.Mutex
	saveDebouncer *time.Timer
	subscribers   map[string][]func(interface{})
}

// NewConfigManager creates a new configuration manager with the loaded configuration
func NewConfigManager(config Config, configPath string) *ConfigManager {
	return &ConfigManager{
		config:      &config,
		configPath:  configPath,
		subscribers: make(map[string][]func(interface{})),
	}
}

// GetConfig returns the current configuration
func (cm *ConfigManager) GetConfig() *Config {
	return cm.config
}

// Subscribe registers a callback for configuration changes
func (cm *ConfigManager) Subscribe(topic string, callback func(interface{})) {
	cm.subscribers[topic] = append(cm.subscribers[topic], callback)
}

// Notify sends updates to subscribers
func (cm *ConfigManager) Notify(topic string, data interface{}) {
	for _, callback := range cm.subscribers[topic] {
		callback(data)
	}
}

// SaveWithDebounce schedules a save after a brief delay, debouncing multiple rapid changes
func (cm *ConfigManager) SaveWithDebounce() {
	// Cancel existing timer if any
	if cm.saveDebouncer != nil {
		cm.saveDebouncer.Stop()
	}

	// Set new timer - save after 2 seconds of no changes
	cm.saveDebouncer = time.AfterFunc(2*time.Second, func() {
		cm.SaveNow()
	})
}

// SaveNow immediately saves the configuration to disk
func (cm *ConfigManager) SaveNow() {
	cm.saveMutex.Lock()
	defer cm.saveMutex.Unlock()

	log.Debug().Msg("Saving configuration to disk")

	// Marshal to YAML
	data, err := yaml.Marshal(cm.config)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal configuration")
		return
	}

	// Write to temporary file first
	tempPath := cm.configPath + ".tmp"
	err = os.WriteFile(tempPath, data, 0644)
	if err != nil {
		log.Error().Err(err).Str("path", tempPath).Msg("Failed to write temporary configuration file")
		return
	}

	// Rename to actual config file (atomic operation)
	err = os.Rename(tempPath, cm.configPath)
	if err != nil {
		log.Error().Err(err).
			Str("temp", tempPath).
			Str("config", cm.configPath).
			Msg("Failed to rename configuration file")
		return
	}

	log.Info().Str("path", cm.configPath).Msg("Configuration saved")
}

// UpdateControlValue updates a control's value (0-100)
func (cm *ConfigManager) UpdateControlValue(controlType string, controlId string, value int) {
	cm.saveMutex.Lock()
	defer cm.saveMutex.Unlock()

	switch controlType {
	case "slider":
		if slider, ok := cm.config.Controls.Sliders[controlId]; ok {
			slider.Value = value
			cm.config.Controls.Sliders[controlId] = slider
		} else {
			// Create the slider if it doesn't exist (when no sources are assigned)
			groupNumber := strings.TrimPrefix(controlId, "slider")
			newSlider := SliderConfig{
				Path:    fmt.Sprintf("Group%s/Slider", groupNumber),
				Value:   value,
				Sources: []Source{},
			}
			cm.config.Controls.Sliders[controlId] = newSlider
		}
	case "knob":
		if knob, ok := cm.config.Controls.Knobs[controlId]; ok {
			knob.Value = value
			cm.config.Controls.Knobs[controlId] = knob
		} else {
			// Create the knob if it doesn't exist (when no sources are assigned)
			groupNumber := strings.TrimPrefix(controlId, "knob")
			newKnob := KnobConfig{
				Path:    fmt.Sprintf("Group%s/Knob", groupNumber),
				Value:   value,
				Sources: []Source{},
			}
			cm.config.Controls.Knobs[controlId] = newKnob
		}
	}

	// Notify subscribers immediately with real-time changes
	cm.Notify("control.value.updated", map[string]interface{}{
		"type":  controlType,
		"id":    controlId,
		"value": value,
	})

	// Schedule save - but don't let this slow down the UI updates
	cm.SaveWithDebounce()
}

// AssignSource assigns an audio source to a control
func (cm *ConfigManager) AssignSource(controlType string, controlId string, source Source) {
	cm.saveMutex.Lock()
	defer cm.saveMutex.Unlock()

	var currentValue int = 0
	
	switch controlType {
	case "slider":
		if slider, ok := cm.config.Controls.Sliders[controlId]; ok {
			// Check if source is already assigned to this control
			for _, existingSource := range slider.Sources {
				if existingSource.Type == source.Type && existingSource.Name == source.Name && existingSource.BinaryName == source.BinaryName {
					// Already assigned, do nothing
					return
				}
			}
			// Add the source
			slider.Sources = append(slider.Sources, source)
			cm.config.Controls.Sliders[controlId] = slider
			currentValue = slider.Value
		}
	case "knob":
		if knob, ok := cm.config.Controls.Knobs[controlId]; ok {
			// Check if source is already assigned to this control
			for _, existingSource := range knob.Sources {
				if existingSource.Type == source.Type && existingSource.Name == source.Name && existingSource.BinaryName == source.BinaryName {
					// Already assigned, do nothing
					return
				}
			}
			// Add the source
			knob.Sources = append(knob.Sources, source)
			cm.config.Controls.Knobs[controlId] = knob
			currentValue = knob.Value
		}
	}

	// Notify subscribers
	cm.Notify("source.assigned", map[string]interface{}{
		"controlType": controlType,
		"controlId":   controlId,
		"source":      source,
		"initialValue": currentValue, // Include the current value for immediate volume setting
	})

	// Schedule save
	cm.SaveWithDebounce()
}

// UnassignSource removes an audio source from a control
func (cm *ConfigManager) UnassignSource(controlType string, controlId string, source Source) {
	cm.saveMutex.Lock()
	defer cm.saveMutex.Unlock()

	switch controlType {
	case "slider":
		if slider, ok := cm.config.Controls.Sliders[controlId]; ok {
			// Filter out the source to remove
			var updatedSources []Source
			for _, existingSource := range slider.Sources {
				if !(existingSource.Type == source.Type && existingSource.Name == source.Name && existingSource.BinaryName == source.BinaryName) {
					updatedSources = append(updatedSources, existingSource)
				}
			}
			slider.Sources = updatedSources
			cm.config.Controls.Sliders[controlId] = slider
		}
	case "knob":
		if knob, ok := cm.config.Controls.Knobs[controlId]; ok {
			// Filter out the source to remove
			var updatedSources []Source
			for _, existingSource := range knob.Sources {
				if !(existingSource.Type == source.Type && existingSource.Name == source.Name && existingSource.BinaryName == source.BinaryName) {
					updatedSources = append(updatedSources, existingSource)
				}
			}
			knob.Sources = updatedSources
			cm.config.Controls.Knobs[controlId] = knob
		}
	}

	// Notify subscribers
	cm.Notify("source.unassigned", map[string]interface{}{
		"controlType": controlType,
		"controlId":   controlId,
		"sourceType":  source.Type,
		"sourceName":  source.Name,
	})

	// Schedule save
	cm.SaveWithDebounce()
}

// MigrateSourceBinaryName updates an existing source to include binary name for specificity
func (cm *ConfigManager) MigrateSourceBinaryName(controlType string, controlId string, sourceType PulseAudioTargetType, sourceName string, binaryName string) {
	// First unassign the old source (without binary name)
	oldSource := Source{
		Type:       sourceType,
		Name:       sourceName,
		BinaryName: "", // Legacy source without binary name
	}
	cm.UnassignSource(controlType, controlId, oldSource)
	
	// Then assign the new source (with binary name)  
	newSource := Source{
		Type:       sourceType,
		Name:       sourceName,
		BinaryName: binaryName,
	}
	cm.AssignSource(controlType, controlId, newSource)
	
	log.Info().
		Str("controlType", controlType).
		Str("sourceName", sourceName).
		Str("binaryName", binaryName).
		Msg("Migrated source to include binary name")
}