package configuration

import (
	"os"
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

// UpdateMidiDeviceMapping updates a MIDI device's mapping to audio sources
func (cm *ConfigManager) UpdateMidiDeviceMapping(deviceName string, controlId string, target interface{}) {
	cm.saveMutex.Lock()
	defer cm.saveMutex.Unlock()

	// TODO: Update the mapping in the configuration
	// This would depend on the detailed structure of your rules

	// Notify subscribers
	cm.Notify("mapping.updated", map[string]interface{}{
		"deviceName": deviceName,
		"controlId":  controlId,
		"target":     target,
	})

	// Schedule save
	cm.SaveWithDebounce()
}