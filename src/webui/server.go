package webui

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/0h41/pulsekontrol/src/configuration"
	"github.com/0h41/pulsekontrol/src/pulseaudio"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

//go:embed static
var staticFiles embed.FS

const (
	// Poll interval for structural changes (new/removed audio sources, assignments)
	statePollInterval = 2 * time.Second
	// A client that cannot accept a message within this time is disconnected
	clientWriteTimeout = 5 * time.Second
	// Control value updates queued for broadcasting; more are dropped
	controlUpdateBufferSize = 256
)

type WebUIServer struct {
	Addr            string
	upgrader        websocket.Upgrader
	controlUpdateCh chan map[string]interface{}
	paClient        *pulseaudio.PAClient
	configManager   *configuration.ConfigManager
	stopChan        chan struct{}

	// clientsMutex guards clients and serializes all websocket writes
	// (gorilla/websocket connections support only one concurrent writer)
	clientsMutex sync.Mutex
	clients      map[*websocket.Conn]bool
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
		controlUpdateCh: make(chan map[string]interface{}, controlUpdateBufferSize),
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
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(staticFS)))
	mux.HandleFunc("/ws", s.handleWebSocket)

	// Start WebSocket broadcasting
	go s.handleBroadcasts()

	// Start audio sources monitoring
	go s.monitorAudioSources()

	// Start HTTP server
	log.Info().Msgf("Starting web server on %s", s.Addr)
	server := &http.Server{
		Addr:         s.Addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	return server.ListenAndServe()
}

// controlAssignments maps each control to the IDs of its assigned sources. Active sources
// use their PulseAudio ID; inactive ones get a virtual "type:name[:binaryName]" ID.
func controlAssignments(controls map[string]configuration.ControlConfig, sources []pulseaudio.AudioSource) map[string][]string {
	assignments := make(map[string][]string, len(controls))
	for id, control := range controls {
		sourceIds := []string{}
		for _, source := range control.Sources {
			sourceIds = append(sourceIds, assignedSourceID(source, sources))
		}
		assignments[id] = sourceIds
	}
	return assignments
}

func assignedSourceID(source configuration.Source, sources []pulseaudio.AudioSource) string {
	for _, audioSource := range sources {
		if audioSource.Matches(source) {
			return audioSource.ID
		}
	}
	if source.BinaryName != "" {
		return fmt.Sprintf("%s:%s:%s", source.Type, source.Name, source.BinaryName)
	}
	return fmt.Sprintf("%s:%s", source.Type, source.Name)
}

func controlValues(controls map[string]configuration.ControlConfig) map[string]int {
	values := make(map[string]int, len(controls))
	for id, control := range controls {
		values[id] = control.Value
	}
	return values
}

// buildUIStateMessage creates a message with current UI state
func (s *WebUIServer) buildUIStateMessage(includeControlValues bool) ([]byte, error) {
	sources := s.paClient.GetAudioSources()
	config := s.configManager.GetConfig()

	message := map[string]interface{}{
		"type":              "audioSourcesUpdate",
		"sources":           sources,
		"sliderAssignments": controlAssignments(config.Controls.Sliders, sources),
		"knobAssignments":   controlAssignments(config.Controls.Knobs, sources),
	}

	// Only include control values if requested (for initial load)
	if includeControlValues {
		message["sliderValues"] = controlValues(config.Controls.Sliders)
		message["knobValues"] = controlValues(config.Controls.Knobs)
	}

	return json.Marshal(message)
}

// parseSourceType converts a UI source type (case-insensitive, short or long form)
// to a PulseAudioTargetType
func parseSourceType(sourceType string) (configuration.PulseAudioTargetType, bool) {
	switch strings.ToLower(sourceType) {
	case "playback", "playbackstream":
		return configuration.PlaybackStream, true
	case "record", "recordstream":
		return configuration.RecordStream, true
	case "output", "outputdevice":
		return configuration.OutputDevice, true
	case "input", "inputdevice":
		return configuration.InputDevice, true
	}
	return configuration.PulseAudioTargetType(sourceType), false
}

// parseVirtualSourceID parses a "type:name" or "type:name:binaryName" ID of an inactive source
func parseVirtualSourceID(sourceId string) (configuration.Source, bool) {
	parts := strings.SplitN(sourceId, ":", 3)
	if len(parts) < 2 {
		return configuration.Source{}, false
	}
	sourceType, _ := parseSourceType(parts[0])
	source := configuration.Source{
		Type: sourceType,
		Name: parts[1],
	}
	if len(parts) == 3 {
		source.BinaryName = parts[2]
	}
	return source, true
}

func findAudioSource(sources []pulseaudio.AudioSource, sourceId string) (pulseaudio.AudioSource, bool) {
	for _, source := range sources {
		if source.ID == sourceId {
			return source, true
		}
	}
	return pulseaudio.AudioSource{}, false
}

// writeMessage sends a message to a single client, dropping the client on failure
func (s *WebUIServer) writeMessage(conn *websocket.Conn, message []byte) bool {
	s.clientsMutex.Lock()
	defer s.clientsMutex.Unlock()
	return s.writeLocked(conn, message)
}

// broadcastMessage sends a message to all connected clients
func (s *WebUIServer) broadcastMessage(message []byte) {
	s.clientsMutex.Lock()
	defer s.clientsMutex.Unlock()
	for conn := range s.clients {
		s.writeLocked(conn, message)
	}
}

// writeLocked writes to a client; must be called with clientsMutex held
func (s *WebUIServer) writeLocked(conn *websocket.Conn, message []byte) bool {
	conn.SetWriteDeadline(time.Now().Add(clientWriteTimeout))
	if err := conn.WriteMessage(websocket.TextMessage, message); err != nil {
		log.Error().Err(err).Msg("Failed to send message to client")
		conn.Close()
		delete(s.clients, conn)
		return false
	}
	return true
}

func (s *WebUIServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Upgrade HTTP connection to WebSocket
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error().Err(err).Msg("Failed to upgrade to websocket")
		return
	}
	defer func() {
		s.clientsMutex.Lock()
		delete(s.clients, conn)
		s.clientsMutex.Unlock()
		conn.Close()
	}()

	// Register new client
	s.clientsMutex.Lock()
	s.clients[conn] = true
	s.clientsMutex.Unlock()
	log.Info().Msgf("New WebSocket client connected: %s", conn.RemoteAddr())

	// Send initial state
	if !s.writeMessage(conn, []byte(`{"type":"welcome","message":"Connected to pulsekontrol"}`)) {
		return
	}

	// Handle client messages
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Info().Msgf("WebSocket client disconnected: %s", conn.RemoteAddr())
			return
		}

		log.Debug().Msgf("Received message: %s", string(message))

		var clientMsg map[string]interface{}
		if err := json.Unmarshal(message, &clientMsg); err != nil {
			log.Error().Err(err).Msg("Failed to parse client message")
			continue
		}

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
			if !s.writeMessage(conn, jsonData) {
				return
			}
		case "setVolume":
			s.handleSetVolume(clientMsg)
		case "updateControlValue":
			s.handleUpdateControlValue(clientMsg)
		case "assignControl":
			s.handleAssignControl(clientMsg)
		case "unassignControl":
			s.handleUnassignControl(clientMsg)
		default:
			log.Debug().Str("type", msgType).Msg("Unknown message type")
		}
	}
}

// stringFields extracts the named string fields from a client message
func stringFields(msgType string, clientMsg map[string]interface{}, names ...string) ([]string, bool) {
	values := make([]string, len(names))
	for i, name := range names {
		value, ok := clientMsg[name].(string)
		if !ok {
			log.Error().Msgf("%s missing %s", msgType, name)
			return nil, false
		}
		values[i] = value
	}
	return values, true
}

func (s *WebUIServer) handleSetVolume(clientMsg map[string]interface{}) {
	fields, ok := stringFields("setVolume", clientMsg, "sourceId")
	if !ok {
		return
	}
	sourceId := fields[0]

	volumeFloat, ok := clientMsg["volume"].(float64)
	if !ok {
		log.Error().Msg("setVolume missing volume or not a number")
		return
	}
	volume := int(volumeFloat)
	log.Debug().Str("sourceId", sourceId).Int("volume", volume).Msg("Setting volume")

	targetSource, found := findAudioSource(s.paClient.GetAudioSources(), sourceId)
	if !found {
		if _, isVirtual := parseVirtualSourceID(sourceId); isVirtual {
			log.Warn().Str("sourceId", sourceId).Msg("Cannot adjust volume of inactive source")
		} else {
			log.Error().Str("sourceId", sourceId).Msg("Source not found")
		}
		return
	}

	targetType, ok := parseSourceType(targetSource.Type)
	if !ok {
		log.Error().Str("type", targetSource.Type).Msg("Unknown source type")
		return
	}

	action := configuration.Action{
		Type: configuration.SetVolume,
		Target: &configuration.TypedTarget{
			Type: targetType,
			Name: targetSource.Name,
		},
	}

	// Convert 0-100 volume to 0-1 for PulseAudio
	if err := s.paClient.ProcessVolumeAction(action, float32(volume)/100.0); err != nil {
		log.Error().Err(err).Str("sourceId", sourceId).Msg("Failed to set volume")
	}
}

func (s *WebUIServer) handleUpdateControlValue(clientMsg map[string]interface{}) {
	fields, ok := stringFields("updateControlValue", clientMsg, "controlId", "controlType")
	if !ok {
		return
	}
	controlId, controlType := fields[0], fields[1]

	valueFloat, ok := clientMsg["value"].(float64)
	if !ok {
		log.Error().Msg("updateControlValue missing value or not a number")
		return
	}
	value := int(valueFloat)
	log.Debug().Str("controlId", controlId).Str("controlType", controlType).Int("value", value).Msg("Updating control value")

	s.configManager.UpdateControlValue(controlType, controlId, value)
}

func (s *WebUIServer) handleAssignControl(clientMsg map[string]interface{}) {
	fields, ok := stringFields("assignControl", clientMsg, "controlId", "controlType", "sourceId")
	if !ok {
		return
	}
	controlId, controlType, sourceId := fields[0], fields[1], fields[2]

	log.Debug().
		Str("controlId", controlId).
		Str("controlType", controlType).
		Str("sourceId", sourceId).
		Msg("Assigning source to control")

	// Prefer a currently active source; fall back to a virtual ID for an inactive one
	var source configuration.Source
	if audioSource, found := findAudioSource(s.paClient.GetAudioSources(), sourceId); found {
		source = configuration.Source{
			Type:       configuration.PulseAudioTargetType(audioSource.Type),
			Name:       audioSource.Name,
			BinaryName: audioSource.BinaryName,
		}
	} else if source, ok = parseVirtualSourceID(sourceId); !ok {
		log.Error().Str("sourceId", sourceId).Msg("Invalid source ID format")
		return
	}

	s.configManager.AssignSource(controlType, controlId, source)
}

func (s *WebUIServer) handleUnassignControl(clientMsg map[string]interface{}) {
	fields, ok := stringFields("unassignControl", clientMsg, "controlId", "controlType", "sourceId")
	if !ok {
		return
	}
	controlId, controlType, sourceId := fields[0], fields[1], fields[2]

	log.Debug().
		Str("controlId", controlId).
		Str("controlType", controlType).
		Str("sourceId", sourceId).
		Msg("Removing source from control")

	audioSource, found := findAudioSource(s.paClient.GetAudioSources(), sourceId)
	if !found {
		// Inactive source: the virtual ID encodes the configured source exactly
		source, ok := parseVirtualSourceID(sourceId)
		if !ok {
			log.Error().Str("sourceId", sourceId).Msg("Invalid virtual source ID format")
			return
		}
		s.configManager.UnassignSource(controlType, controlId, source)
		return
	}

	// Active source: remove every configured source it was displayed for, which
	// includes legacy entries without a binary name
	config := s.configManager.GetConfig()
	var controls map[string]configuration.ControlConfig
	switch controlType {
	case "slider":
		controls = config.Controls.Sliders
	case "knob":
		controls = config.Controls.Knobs
	}
	for _, source := range controls[controlId].Sources {
		if audioSource.Matches(source) {
			s.configManager.UnassignSource(controlType, controlId, source)
		}
	}
}

func (s *WebUIServer) handleBroadcasts() {
	for {
		select {
		case controlUpdate := <-s.controlUpdateCh:
			// Fast path for control value updates
			jsonData, err := json.Marshal(controlUpdate)
			if err != nil {
				log.Error().Err(err).Msg("Failed to marshal control value update")
				continue
			}
			s.broadcastMessage(jsonData)
		case <-s.stopChan:
			return
		}
	}
}

// monitorAudioSources periodically fetches audio sources and broadcasts them to clients
func (s *WebUIServer) monitorAudioSources() {
	ticker := time.NewTicker(statePollInterval)
	defer ticker.Stop()

	var previousState []byte

	for {
		select {
		case <-ticker.C:
			s.clientsMutex.Lock()
			clientCount := len(s.clients)
			s.clientsMutex.Unlock()
			if clientCount == 0 {
				// Nobody is watching; force a full update for the next client
				previousState = nil
				continue
			}

			// Exclude control values - the fast path handles those
			jsonData, err := s.buildUIStateMessage(false)
			if err != nil {
				log.Error().Err(err).Msg("Failed to marshal audio sources and assignments")
				continue
			}

			if bytes.Equal(previousState, jsonData) {
				continue
			}
			previousState = jsonData

			log.Debug().Msg("State changed, sending update to clients")
			s.broadcastMessage(jsonData)
		case <-s.stopChan:
			return
		}
	}
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
	default:
		// Queue full, skip this update
	}
}
