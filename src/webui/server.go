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
		clients:        make(map[*websocket.Conn]bool),
		broadcast:      make(chan []byte),
		configUpdateCh: make(chan interface{}),
		paClient:       paClient,
		configManager:  configManager,
		stopChan:       make(chan struct{}),
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
			// Client is requesting initial state
			// Currently handled by monitorAudioSources periodic updates
			
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
			
			// TODO: Implement volume change via pulseaudio client
			
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
					
					// Create configuration source
					configSource := configuration.Source{
						Type: configuration.PulseAudioTargetType(sourceType),
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
					
					s.configManager.UnassignSource(
						controlType,
						controlId,
						configuration.PulseAudioTargetType(sourceType),
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
			for client := range s.clients {
				err := client.WriteMessage(websocket.TextMessage, message)
				if err != nil {
					log.Error().Err(err).Msg("Failed to send message to client")
					client.Close()
					delete(s.clients, client)
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
	ticker := time.NewTicker(200 * time.Millisecond) // Much faster polling (200ms)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Get audio sources
			sources := s.paClient.GetAudioSources()
			
			// Get control assignments
			config := s.configManager.GetConfig()
			
			// Map of slider assignments (controlId -> sourceIds)
			sliderAssignments := make(map[string][]string)
			sliderValues := make(map[string]int)
			for id, slider := range config.Controls.Sliders {
				sourceIds := []string{}
				// For each source in the slider, find the matching audio source
				for _, source := range slider.Sources {
					found := false
					// Find the source in our audio sources
					for _, audioSource := range sources {
						if audioSource.Type == string(source.Type) && audioSource.Name == source.Name {
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
				sliderValues[id] = slider.Value
			}
			
			// Map of knob assignments (controlId -> sourceIds)
			knobAssignments := make(map[string][]string)
			knobValues := make(map[string]int)
			for id, knob := range config.Controls.Knobs {
				sourceIds := []string{}
				// For each source in the knob, find the matching audio source
				for _, source := range knob.Sources {
					found := false
					// Find the source in our audio sources
					for _, audioSource := range sources {
						if audioSource.Type == string(source.Type) && audioSource.Name == source.Name {
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
				knobValues[id] = knob.Value
			}
			
			// Create message with sources and control mappings
			message := map[string]interface{}{
				"type":              "audioSourcesUpdate",
				"sources":           sources,
				"sliderAssignments": sliderAssignments,
				"sliderValues":      sliderValues,
				"knobAssignments":   knobAssignments,
				"knobValues":        knobValues,
			}
			
			// Convert to JSON
			jsonData, err := json.Marshal(message)
			if err != nil {
				log.Error().Err(err).Msg("Failed to marshal audio sources and assignments")
				continue
			}
			
			// Broadcast to clients
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