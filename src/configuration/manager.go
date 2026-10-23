package configuration

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

// Save after 2 seconds of no changes
const saveDebounce = 2 * time.Second

// Notification topics published by the ConfigManager
const (
	// TopicControlValueUpdated carries map{"type", "id", "value"} for a moved slider/knob
	TopicControlValueUpdated = "control.value.updated"
	// TopicSourceAssigned carries map{"controlType", "controlId", "source", "initialValue"}
	// for every source newly assigned to a control
	TopicSourceAssigned = "source.assigned"
	// TopicAssignmentsChanged is published once (with nil data) after any change to
	// source assignments, so subscribers can rebuild derived state a single time
	TopicAssignmentsChanged = "assignments.changed"
)

// ConfigManager handles the runtime configuration with persistence.
// All methods are safe for concurrent use. Subscribers are called without
// any internal lock held, so they may call back into the manager.
type ConfigManager struct {
	mu          sync.Mutex
	config      Config
	configPath  string
	saveTimer   *time.Timer
	subscribers map[string][]func(interface{})

	// writeMu serializes disk writes; always acquired before mu, never after
	writeMu sync.Mutex
}

type notification struct {
	topic string
	data  interface{}
}

// NewConfigManager creates a new configuration manager with the loaded configuration
func NewConfigManager(config Config, configPath string) *ConfigManager {
	return &ConfigManager{
		config:      config.Clone(),
		configPath:  configPath,
		subscribers: make(map[string][]func(interface{})),
	}
}

// GetConfig returns a deep copy of the current configuration
func (cm *ConfigManager) GetConfig() Config {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.config.Clone()
}

// Subscribe registers a callback for configuration changes
func (cm *ConfigManager) Subscribe(topic string, callback func(interface{})) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.subscribers[topic] = append(cm.subscribers[topic], callback)
}

// notify sends updates to subscribers. Must be called without cm.mu held.
func (cm *ConfigManager) notify(notifications ...notification) {
	for _, n := range notifications {
		cm.mu.Lock()
		callbacks := slices.Clone(cm.subscribers[n.topic])
		cm.mu.Unlock()

		for _, callback := range callbacks {
			callback(n.data)
		}
	}
}

// controls returns the control map for the given control type ("slider" or "knob").
// Must be called with cm.mu held.
func (cm *ConfigManager) controls(controlType string) map[string]ControlConfig {
	switch controlType {
	case "slider":
		return cm.config.Controls.Sliders
	case "knob":
		return cm.config.Controls.Knobs
	}
	return nil
}

// scheduleSave schedules a save after a brief delay, debouncing multiple rapid changes.
// Must be called with cm.mu held.
func (cm *ConfigManager) scheduleSave() {
	if cm.saveTimer == nil {
		cm.saveTimer = time.AfterFunc(saveDebounce, cm.SaveNow)
		return
	}
	cm.saveTimer.Reset(saveDebounce)
}

// Flush writes any pending debounced changes to disk and waits for an in-progress save
func (cm *ConfigManager) Flush() {
	cm.mu.Lock()
	pending := cm.saveTimer != nil && cm.saveTimer.Stop()
	cm.mu.Unlock()

	if pending {
		cm.SaveNow()
		return
	}

	// Wait for a save that may already be running
	cm.writeMu.Lock()
	cm.writeMu.Unlock()
}

// SaveNow immediately saves the configuration to disk
func (cm *ConfigManager) SaveNow() {
	cm.writeMu.Lock()
	defer cm.writeMu.Unlock()

	cm.mu.Lock()
	data, err := yaml.Marshal(&cm.config)
	cm.mu.Unlock()
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal configuration")
		return
	}

	if err := writeFileAtomic(cm.configPath, data); err != nil {
		log.Error().Err(err).Str("path", cm.configPath).Msg("Failed to save configuration")
		return
	}

	log.Info().Str("path", cm.configPath).Msg("Configuration saved")
}

// writeFileAtomic writes to a temporary file first, then renames it over the target
func writeFileAtomic(path string, data []byte) error {
	tempPath := path + ".tmp"
	file, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

// UpdateControlValue updates a control's value (0-100)
func (cm *ConfigManager) UpdateControlValue(controlType string, controlId string, value int) {
	cm.mu.Lock()
	controls := cm.controls(controlType)
	if controls == nil {
		cm.mu.Unlock()
		return
	}

	control, ok := controls[controlId]
	if ok && control.Value == value {
		cm.mu.Unlock()
		return
	}
	if !ok {
		// Create the control if it doesn't exist (when no sources are assigned)
		groupNumber := strings.TrimPrefix(controlId, controlType)
		control = ControlConfig{
			Path:    fmt.Sprintf("Group%s/%s", groupNumber, strings.ToUpper(controlType[:1])+controlType[1:]),
			Sources: []Source{},
		}
	}
	control.Value = value
	controls[controlId] = control
	cm.scheduleSave()
	cm.mu.Unlock()

	cm.notify(notification{TopicControlValueUpdated, map[string]interface{}{
		"type":  controlType,
		"id":    controlId,
		"value": value,
	}})
}

// AssignSource assigns an audio source to a control
func (cm *ConfigManager) AssignSource(controlType string, controlId string, source Source) {
	cm.AssignSources(controlType, controlId, source)
}

// AssignSources assigns audio sources to a control. A source can only be assigned
// to one control at a time, so it is moved away from any other control first.
func (cm *ConfigManager) AssignSources(controlType string, controlId string, sources ...Source) {
	cm.mu.Lock()
	controls := cm.controls(controlType)
	control, ok := controls[controlId]
	if !ok {
		cm.mu.Unlock()
		log.Warn().Str("controlType", controlType).Str("controlId", controlId).Msg("Cannot assign source to unknown control")
		return
	}

	changed := false
	var notifications []notification
	for _, source := range sources {
		if cm.removeFromOtherControls(controlType, controlId, source) {
			changed = true
		}
		if slices.Contains(control.Sources, source) {
			continue
		}
		control.Sources = append(control.Sources, source)
		changed = true
		notifications = append(notifications, notification{TopicSourceAssigned, map[string]interface{}{
			"controlType":  controlType,
			"controlId":    controlId,
			"source":       source,
			"initialValue": control.Value, // Include the current value for immediate volume setting
		}})
	}

	if !changed {
		cm.mu.Unlock()
		return
	}
	controls[controlId] = control
	cm.scheduleSave()
	cm.mu.Unlock()

	cm.notify(append(notifications, notification{TopicAssignmentsChanged, nil})...)
}

// UnassignSource removes an audio source from a control
func (cm *ConfigManager) UnassignSource(controlType string, controlId string, source Source) {
	cm.mu.Lock()
	controls := cm.controls(controlType)
	control, ok := controls[controlId]
	if !ok {
		cm.mu.Unlock()
		return
	}

	filteredSources, removed := filterSource(control.Sources, source)
	if !removed {
		cm.mu.Unlock()
		return
	}
	control.Sources = filteredSources
	controls[controlId] = control
	cm.scheduleSave()
	cm.mu.Unlock()

	cm.notify(notification{TopicAssignmentsChanged, nil})
}

// MigrateSourceBinaryName replaces a legacy source (without binary name) on a control
// with the same source including the binary name, keeping its position. It reports
// whether the legacy source was found and migrated.
func (cm *ConfigManager) MigrateSourceBinaryName(controlType string, controlId string, legacySource Source, binaryName string) bool {
	legacySource.BinaryName = ""
	migratedSource := legacySource
	migratedSource.BinaryName = binaryName

	cm.mu.Lock()
	controls := cm.controls(controlType)
	control, ok := controls[controlId]
	index := slices.Index(control.Sources, legacySource)
	if !ok || index < 0 {
		cm.mu.Unlock()
		return false
	}

	if slices.Contains(control.Sources, migratedSource) {
		control.Sources = slices.Delete(control.Sources, index, index+1)
	} else {
		control.Sources[index] = migratedSource
	}
	cm.removeFromOtherControls(controlType, controlId, migratedSource)
	controls[controlId] = control
	cm.scheduleSave()
	cm.mu.Unlock()

	log.Info().
		Str("controlType", controlType).
		Str("controlId", controlId).
		Str("sourceName", legacySource.Name).
		Str("binaryName", binaryName).
		Msg("Migrated source to include binary name")

	cm.notify(notification{TopicAssignmentsChanged, nil})
	return true
}

// removeFromOtherControls removes the source from every control except the target one
// and reports whether anything was removed. Must be called with cm.mu held.
func (cm *ConfigManager) removeFromOtherControls(targetControlType string, targetControlId string, source Source) bool {
	removedAny := false
	for _, controlType := range []string{"slider", "knob"} {
		controls := cm.controls(controlType)
		for controlId, control := range controls {
			if controlType == targetControlType && controlId == targetControlId {
				continue
			}
			filteredSources, removed := filterSource(control.Sources, source)
			if !removed {
				continue
			}
			control.Sources = filteredSources
			controls[controlId] = control
			removedAny = true
		}
	}
	return removedAny
}

func filterSource(sources []Source, target Source) ([]Source, bool) {
	filteredSources := make([]Source, 0, len(sources))
	removed := false

	for _, source := range sources {
		if source == target {
			removed = true
			continue
		}
		filteredSources = append(filteredSources, source)
	}

	return filteredSources, removed
}
