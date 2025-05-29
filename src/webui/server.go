package webui

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/0h41/pulsekontrol/src/configuration"
	"github.com/0h41/pulsekontrol/src/pulseaudio"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

//go:embed static
var staticFiles embed.FS

type WebUIServer struct {
	Addr           string
	upgrader       websocket.Upgrader
	clients        map[*websocket.Conn]bool
	broadcast      chan []byte
	configUpdateCh chan interface{}
	controlUpdateCh chan map[string]interface{}
	paClient       *pulseaudio.PAClient
	configManager  *configuration.ConfigManager
	stopChan       chan struct{}
}

func NewWebUIServer(addr string, paClient *pulseaudio.PAClient, configManager *configuration.ConfigManager) *WebUIServer {
	return &WebUIServer{
		Addr: addr,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all connections for now
			},
		},
		clients:         make(map[*websocket.Conn]bool),
		broadcast:       make(chan []byte),
		configUpdateCh:  make(chan interface{}),
		controlUpdateCh: make(chan map[string]interface{}),
		paClient:        paClient,
		configManager:   configManager,
		stopChan:        make(chan struct{}),
	}
}

func (s *WebUIServer) Start() error {
	// Create a file system with just the static files
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return fmt.Errorf("failed to create static filesystem: %w", err)
	}

	// Setup HTTP server and routes
	http.Handle("/", http.FileServer(http.FS(staticFS)))
	http.HandleFunc("/ws", s.handleWebSocket)

	// Start WebSocket broadcasting
	go s.handleBroadcasts()

	// Start audio sources monitoring
	go s.monitorAudioSources()

	// Start HTTP server
	log.Info().Msgf("Starting web server on %s", s.Addr)
	server := &http.Server{
		Addr:         s.Addr,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	return server.ListenAndServe()
}

// buildUIStateMessage creates a message with current UI state
func (s *WebUIServer) buildUIStateMessage(includeControlValues bool) ([]byte, error) {
	// Get audio sources
	sources := s.paClient.GetAudioSources()
	
	// Get control assignments
	config := s.configManager.GetConfig()
	
	// Map of slider assignments (controlId -> sourceIds)
	sliderAssignments := make(map[string][]string)
	var sliderValues map[string]int
	if includeControlValues {
		sliderValues = make(map[string]int)
	}
	
	for id, slider := range config.Controls.Sliders {
		sourceIds := []string{}
		// For each source in the slider, find the matching audio source
		for _, source := range slider.Sources {
			found := false
			// Find the source in our audio sources
			for _, audioSource := range sources {
				// Use lowercase comparison for source types
				sourceTypeLower := strings.ToLower(string(source.Type))
				audioSourceTypeLower := strings.ToLower(audioSource.Type)
				if audioSourceTypeLower == sourceTypeLower && audioSource.Name == source.Name {
					sourceIds = append(sourceIds, audioSource.ID)
					found = true
					break
				}
			}
			// If source not found in current sources, create a virtual ID for it
			if !found {
				virtualId := fmt.Sprintf("%s:%s", source.Type, source.Name)
				sourceIds = append(sourceIds, virtualId)
			}
		}
		sliderAssignments[id] = sourceIds
		if includeControlValues {
			sliderValues[id] = slider.Value
		}
	}
	
	// Map of knob assignments (controlId -> sourceIds)
	knobAssignments := make(map[string][]string)
	var knobValues map[string]int
	if includeControlValues {
		knobValues = make(map[string]int)
	}
	
	for id, knob := range config.Controls.Knobs {
		sourceIds := []string{}
		// For each source in the knob, find the matching audio source
		for _, source := range knob.Sources {
			found := false
			// Find the source in our audio sources
			for _, audioSource := range sources {
				// Use lowercase comparison for source types
				sourceTypeLower := strings.ToLower(string(source.Type))
				audioSourceTypeLower := strings.ToLower(audioSource.Type)
				if audioSourceTypeLower == sourceTypeLower && audioSource.Name == source.Name {
					sourceIds = append(sourceIds, audioSource.ID)
					found = true
					break
				}
			}
			// If source not found in current sources, create a virtual ID for it
			if !found {
				virtualId := fmt.Sprintf("%s:%s", source.Type, source.Name)
				sourceIds = append(sourceIds, virtualId)
			}
		}
		knobAssignments[id] = sourceIds
		if includeControlValues {
			knobValues[id] = knob.Value
		}
	}
	
	// Create message with sources and control mappings
	message := map[string]interface{}{
		"type":              "audioSourcesUpdate",
		"sources":           sources,
		"sliderAssignments": sliderAssignments,
		"knobAssignments":   knobAssignments,
	}
	
	// Only include control values if requested (for initial load)
	if includeControlValues {
		message["sliderValues"] = sliderValues
		message["knobValues"] = knobValues
	}
	
	// Convert to JSON
	return json.Marshal(message)
}

func (s *WebUIServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Upgrade HTTP connection to WebSocket
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error().Err(err).Msg("Failed to upgrade to websocket")
		return
	}
	defer conn.Close()

	// Register new client
	s.clients[conn] = true
	log.Info().Msgf("New WebSocket client connected: %s", conn.RemoteAddr())

	// Send initial state
	initialMsg := []byte(`{"type":"welcome","message":"Connected to pulsekontrol"}`)
	err = conn.WriteMessage(websocket.TextMessage, initialMsg)
	if err != nil {
		log.Error().Err(err).Msg("Failed to send welcome message")
		delete(s.clients, conn)
		return
	}

	// Handle client messages
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Info().Msgf("WebSocket client disconnected: %s", conn.RemoteAddr())
			delete(s.clients, conn)
			break
		}

		// Process messages from client
		log.Debug().Msgf("Received message: %s", string(message))
		
		// Parse the message
		var clientMsg map[string]interface{}
		if err := json.Unmarshal(message, &clientMsg); err != nil {
			log.Error().Err(err).Msg("Failed to parse client message")
			continue
		}
		
		// Handle based on message type
		msgType, ok := clientMsg["type"].(string)
		if !ok {
			log.Error().Msg("Message missing 'type' field")
			continue
		}
		
		switch msgType {
		case "getState":
			// Client is requesting initial state - send it immediately rather than waiting for next poll
			jsonData, err := s.buildUIStateMessage(true) // Include control values for initial load
			if err != nil {
				log.Error().Err(err).Msg("Failed to marshal audio sources and assignments")
				continue
			}
			
			// Send directly to this client
			log.Debug().Msg("Sending initial state to new client")
			err = conn.WriteMessage(websocket.TextMessage, jsonData)
			if err != nil {
				log.Error().Err(err).Msg("Failed to send initial state to client")
				delete(s.clients, conn)
				return
			}
		case "setVolume":
			// Client wants to change volume
			sourceId, ok := clientMsg["sourceId"].(string)
			if !ok {
				log.Error().Msg("setVolume missing sourceId")
				continue
			}
			
			volumeFloat, ok := clientMsg["volume"].(float64)
			if !ok {
				log.Error().Msg("setVolume missing volume or not a number")
				continue
			}
			
			volume := int(volumeFloat)
			log.Debug().Str("sourceId", sourceId).Int("volume", volume).Msg("Setting volume")
			
			// Get the sources and find the one with matching ID
			sources := s.paClient.GetAudioSources()
			var targetSource *pulseaudio.AudioSource
			
			for _, source := range sources {
				if source.ID == sourceId {
					targetSource = &source
					break
				}
			}
			
			if targetSource == nil {
				// It might be a virtual ID for an inactive source
				parts := strings.SplitN(sourceId, ":", 2)
				if len(parts) == 2 {
					// Cannot adjust volume of inactive sources
					log.Warn().Str("sourceId", sourceId).Msg("Cannot adjust volume of inactive source")
					continue
				}
				
				log.Error().Str("sourceId", sourceId).Msg("Source not found")
				continue
			}
			
			// Create an action to set volume
			var targetType configuration.PulseAudioTargetType
			// Convert to lowercase for case-insensitive comparison
			sourceTypeLower := strings.ToLower(targetSource.Type)
			switch sourceTypeLower {
			case "playback", "playbackstream":
				targetType = configuration.PlaybackStream
			case "record", "recordstream":
				targetType = configuration.RecordStream
			case "output", "outputdevice":
				targetType = configuration.OutputDevice
			case "input", "inputdevice":
				targetType = configuration.InputDevice
			default:
				log.Error().Str("type", targetSource.Type).Msg("Unknown source type")
				continue
			}
			
			action := configuration.Action{
				Type: configuration.SetVolume,
				Target: &configuration.TypedTarget{
					Type: targetType,
					Name: targetSource.Name,
				},
			}
			
			// Convert 0-100 volume to 0-1 for PulseAudio
			volumePercent := float32(volume) / 100.0
			
			// Set volume
			if err := s.paClient.ProcessVolumeAction(action, volumePercent); err != nil {
				log.Error().Err(err).Str("sourceId", sourceId).Msg("Failed to set volume")
			}
			
		case "updateControlValue":
			// Client wants to update a control's value
			controlId, ok := clientMsg["controlId"].(string)
			if !ok {
				log.Error().Msg("updateControlValue missing controlId")
				continue
			}
			
			controlType, ok := clientMsg["controlType"].(string)
			if !ok {
				log.Error().Msg("updateControlValue missing controlType")
				continue
			}
			
			valueFloat, ok := clientMsg["value"].(float64)
			if !ok {
				log.Error().Msg("updateControlValue missing value or not a number")
				continue
			}
			
			value := int(valueFloat)
			log.Debug().Str("controlId", controlId).Str("controlType", controlType).Int("value", value).Msg("Updating control value")
			
			// Update configuration
			s.configManager.UpdateControlValue(controlType, controlId, value)
			
		case "assignControl":
			// Client wants to assign a source to a control
			controlId, ok := clientMsg["controlId"].(string)
			if !ok {
				log.Error().Msg("assignControl missing controlId")
				continue
			}
			
			controlType, ok := clientMsg["controlType"].(string)
			if !ok {
				log.Error().Msg("assignControl missing controlType")
				continue
			}
			
			sourceId, ok := clientMsg["sourceId"].(string)
			if !ok {
				log.Error().Msg("assignControl missing sourceId")
				continue
			}
			
			log.Debug().
				Str("controlId", controlId).
				Str("controlType", controlType).
				Str("sourceId", sourceId).
				Msg("Assigning source to control")
			
			// Check if this is a real source or a virtual source
			sources := s.paClient.GetAudioSources()
			var sourceToAssign *pulseaudio.AudioSource
			
			// First check if it's a real available source
			for _, source := range sources {
				if source.ID == sourceId {
					sourceToAssign = &source
					break
				}
			}
			
			// If it's a real source, use it
			if sourceToAssign != nil {
				// Create configuration source
				configSource := configuration.Source{
					Type: configuration.PulseAudioTargetType(sourceToAssign.Type),
					Name: sourceToAssign.Name,
				}
				
				// Update configuration
				s.configManager.AssignSource(controlType, controlId, configSource)
			} else {
				// It might be a virtual ID for an inactive source
				parts := strings.SplitN(sourceId, ":", 2)
				if len(parts) == 2 {
					sourceType := parts[0]
					sourceName := parts[1]
					
					log.Debug().
						Str("sourceType", sourceType).
						Str("sourceName", sourceName).
						Msg("Assigning inactive source")
					
					// Convert source type to proper PulseAudioTargetType format
					var targetType configuration.PulseAudioTargetType
					// Convert to lowercase for case-insensitive comparison
					sourceTypeLower := strings.ToLower(sourceType)
					switch sourceTypeLower {
					case "playback", "playbackstream":
						targetType = configuration.PlaybackStream
					case "record", "recordstream":
						targetType = configuration.RecordStream
					case "output", "outputdevice":
						targetType = configuration.OutputDevice
					case "input", "inputdevice":
						targetType = configuration.InputDevice
					default:
						targetType = configuration.PulseAudioTargetType(sourceType)
					}
					
					// Create configuration source
					configSource := configuration.Source{
						Type: targetType,
						Name: sourceName,
					}
					
					// Update configuration
					s.configManager.AssignSource(controlType, controlId, configSource)
				} else {
					log.Error().Str("sourceId", sourceId).Msg("Invalid source ID format")
					continue
				}
			}
			
		case "unassignControl":
			// Client wants to remove a source from a control
			controlId, ok := clientMsg["controlId"].(string)
			if !ok {
				log.Error().Msg("unassignControl missing controlId")
				continue
			}
			
			controlType, ok := clientMsg["controlType"].(string)
			if !ok {
				log.Error().Msg("unassignControl missing controlType")
				continue
			}
			
			sourceId, ok := clientMsg["sourceId"].(string)
			if !ok {
				log.Error().Msg("unassignControl missing sourceId")
				continue
			}
			
			log.Debug().
				Str("controlId", controlId).
				Str("controlType", controlType).
				Str("sourceId", sourceId).
				Msg("Removing source from control")
			
			// Find the audio source in the available sources
			sources := s.paClient.GetAudioSources()
			var sourceToRemove *pulseaudio.AudioSource
			
			for _, source := range sources {
				if source.ID == sourceId {
					sourceToRemove = &source
					break
				}
			}
			
			if sourceToRemove != nil {
				// Source is active, unassign normally
				s.configManager.UnassignSource(
					controlType,
					controlId,
					configuration.PulseAudioTargetType(sourceToRemove.Type),
					sourceToRemove.Name,
				)
			} else {
				// Source might be a virtual ID for an inactive source
				// Parse the ID which should be in the format "type:name"
				parts := strings.SplitN(sourceId, ":", 2)
				if len(parts) == 2 {
					sourceType := parts[0]
					sourceName := parts[1]
					
					log.Debug().
						Str("sourceType", sourceType).
						Str("sourceName", sourceName).
						Msg("Unassigning inactive source")
					
					// Convert source type to proper PulseAudioTargetType format
					var targetType configuration.PulseAudioTargetType
					// Convert to lowercase for case-insensitive comparison
					sourceTypeLower := strings.ToLower(sourceType)
					switch sourceTypeLower {
					case "playback", "playbackstream":
						targetType = configuration.PlaybackStream
					case "record", "recordstream":
						targetType = configuration.RecordStream
					case "output", "outputdevice":
						targetType = configuration.OutputDevice
					case "input", "inputdevice":
						targetType = configuration.InputDevice
					default:
						targetType = configuration.PulseAudioTargetType(sourceType)
					}
					
					s.configManager.UnassignSource(
						controlType,
						controlId,
						targetType,
						sourceName,
					)
				} else {
					log.Error().Str("sourceId", sourceId).Msg("Invalid virtual source ID format")
					continue
				}
			}
			
		default:
			log.Debug().Str("type", msgType).Msg("Unknown message type")
		}
	}
}

func (s *WebUIServer) handleBroadcasts() {
	for {
		select {
		case message := <-s.broadcast:
			// Send to all connected clients
			log.Debug().Int("clientCount", len(s.clients)).Str("message", string(message)).Msg("Broadcasting message to WebSocket clients")
			for client := range s.clients {
				err := client.WriteMessage(websocket.TextMessage, message)
				if err != nil {
					log.Error().Err(err).Msg("Failed to send message to client")
					client.Close()
					delete(s.clients, client)
				} else {
					log.Debug().Msg("Successfully sent message to WebSocket client")
				}
			}
		case controlUpdate := <-s.controlUpdateCh:
			// Fast path for control value updates - send directly to clients
			log.Debug().Interface("controlUpdate", controlUpdate).Msg("Processing fast path control update")
			jsonData, err := json.Marshal(controlUpdate)
			if err != nil {
				log.Error().Err(err).Msg("Failed to marshal control value update")
				continue
			}
			log.Debug().Int("clientCount", len(s.clients)).Str("json", string(jsonData)).Msg("Sending fast path JSON directly to WebSocket clients")
			// Send directly to clients (avoid broadcast channel deadlock)
			for client := range s.clients {
				err := client.WriteMessage(websocket.TextMessage, jsonData)
				if err != nil {
					log.Error().Err(err).Msg("Failed to send fast path message to client")
					client.Close()
					delete(s.clients, client)
				} else {
					log.Debug().Msg("Successfully sent fast path message to WebSocket client")
				}
			}
		case update := <-s.configUpdateCh:
			// Handle config updates
			log.Debug().Interface("update", update).Msg("Config updated, notifying clients")
			
			// If this is a control value update, broadcast it immediately
			if updateMap, ok := update.(map[string]interface{}); ok {
				if updateMap["type"] != nil && updateMap["id"] != nil && updateMap["value"] != nil {
					// This is a control value update, broadcast it to clients
					message := map[string]interface{}{
						"type":        "controlValueUpdate",
						"controlType": updateMap["type"],
						"controlId":   updateMap["id"],
						"value":       updateMap["value"],
					}
					
					// Convert to JSON and broadcast
					jsonData, err := json.Marshal(message)
					if err != nil {
						log.Error().Err(err).Msg("Failed to marshal control value update")
						continue
					}
					
					s.broadcast <- jsonData
				}
			}
		case <-s.stopChan:
			return
		}
	}
}

// monitorAudioSources periodically fetches audio sources and broadcasts them to clients
func (s *WebUIServer) monitorAudioSources() {
	ticker := time.NewTicker(2 * time.Second) // Poll every 2s for structural changes (new/removed audio sources)
	defer ticker.Stop()

	// Store previous state as a hash of the JSON message
	var prevStateHash string

	for {
		select {
		case <-ticker.C:
			// Get current UI state message (exclude control values - fast path handles those)
			jsonData, err := s.buildUIStateMessage(false) // Only structural changes
			if err != nil {
				log.Error().Err(err).Msg("Failed to marshal audio sources and assignments")
				continue
			}

			// Calculate hash of the current state
			currentStateHash := fmt.Sprintf("%x", jsonData)
			
			// Check if anything has changed
			if prevStateHash == currentStateHash {
				// Nothing changed, skip the update
				continue
			}
			
			// Update previous state hash
			prevStateHash = currentStateHash
			
			// Broadcast to clients
			log.Debug().Msg("State changed, sending update to clients")
			s.broadcast <- jsonData
		case <-s.stopChan:
			return
		}
	}
}

// BroadcastMessage sends a message to all connected clients
func (s *WebUIServer) BroadcastMessage(message []byte) {
	s.broadcast <- message
}

// NotifyConfigUpdate sends a config update to all connected clients
func (s *WebUIServer) NotifyConfigUpdate(update interface{}) {
	s.configUpdateCh <- update
}

// NotifyControlValueUpdate sends a fast control value update to all connected clients
func (s *WebUIServer) NotifyControlValueUpdate(controlType, controlId string, value int) {
	update := map[string]interface{}{
		"type":        "controlValueUpdate",
		"controlType": controlType,
		"controlId":   controlId,
		"value":       value,
	}
	
	// Non-blocking send to avoid slowing down MIDI processing
	select {
	case s.controlUpdateCh <- update:
		// Sent successfully
	default:
		// Channel full, skip this update (next one will follow soon)
	}
}