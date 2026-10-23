package pulseaudio

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/0h41/pulsekontrol/src/configuration"
	"github.com/godbus/dbus/v5"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
	"github.com/the-jonsey/pulseaudio"
)

// How long to wait after a PulseAudio event before rescanning streams, so that
// bursts of events (e.g. volume changes while a slider moves) cause one rescan
const streamUpdateSettleDelay = 100 * time.Millisecond

type Stream struct {
	Name       string
	FullName   string
	BinaryName string
	MediaName  string
	ProcessID  int
	paStream   interface{}
}

type AudioSource struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	BinaryName string `json:"binaryName"`
	Type       string `json:"type"`
	Volume     int    `json:"volume"`
}

// Matches reports whether this audio source is an instance of the configured source.
// Sources with a binary name require an exact binary match; legacy sources match by name.
func (s AudioSource) Matches(source configuration.Source) bool {
	if !strings.EqualFold(s.Type, string(source.Type)) || s.Name != source.Name {
		return false
	}
	return source.BinaryName == "" || s.BinaryName == source.BinaryName
}

// StreamEventCallback is called when a new stream is detected
type StreamEventCallback func(stream Stream, streamType configuration.PulseAudioTargetType)

// MediaStatusCallback is called when media playback status changes
type MediaStatusCallback func(isPlaying bool)

// PAClient wraps the PulseAudio connection. It keeps no shared stream state:
// every operation queries PulseAudio for the objects it needs, so methods are
// safe to call from multiple goroutines.
type PAClient struct {
	log     zerolog.Logger
	context *pulseaudio.Client
	// Callbacks must be set before monitoring is started
	newStreamCallback     StreamEventCallback
	removedStreamCallback StreamEventCallback
	mediaStatusCallback   MediaStatusCallback
	monitoringEnabled     atomic.Bool
}

func NewPAClient() *PAClient {
	context, err := pulseaudio.NewClient()
	if err != nil {
		panic(err)
	}
	return &PAClient{
		log:     log.With().Str("module", "PulseAudio").Logger(),
		context: context,
	}
}

func (client *PAClient) outputs() ([]Stream, error) {
	sinks, err := client.context.Sinks()
	if err != nil {
		return nil, fmt.Errorf("failed to list sinks: %w", err)
	}
	return lo.Map(sinks, func(sink pulseaudio.Sink, i int) Stream {
		return Stream{
			Name:     sink.Description,
			FullName: sink.Name,
			paStream: sink,
		}
	}), nil
}

func (client *PAClient) inputs() ([]Stream, error) {
	sources, err := client.context.Sources()
	if err != nil {
		return nil, fmt.Errorf("failed to list sources: %w", err)
	}
	return lo.Map(sources, func(source pulseaudio.Source, i int) Stream {
		return Stream{
			Name:     source.Description,
			FullName: source.Name,
			paStream: source,
		}
	}), nil
}

func (client *PAClient) playbackStreams() ([]Stream, error) {
	sinkInputs, err := client.context.SinkInputs()
	if err != nil {
		return nil, fmt.Errorf("failed to list sink inputs: %w", err)
	}
	return lo.Map(sinkInputs, func(sinkInput pulseaudio.SinkInput, i int) Stream {
		return newApplicationStream(sinkInput.PropList, sinkInput)
	}), nil
}

func (client *PAClient) recordStreams() ([]Stream, error) {
	sourceOutputs, err := client.context.SourceOutputs()
	if err != nil {
		return nil, fmt.Errorf("failed to list source outputs: %w", err)
	}
	return lo.Map(sourceOutputs, func(sourceOutput pulseaudio.SourceOutput, i int) Stream {
		return newApplicationStream(sourceOutput.PropList, sourceOutput)
	}), nil
}

// streams returns the PulseAudio objects of the given type
func (client *PAClient) streams(targetType configuration.PulseAudioTargetType) ([]Stream, error) {
	switch targetType {
	case configuration.OutputDevice:
		return client.outputs()
	case configuration.InputDevice:
		return client.inputs()
	case configuration.PlaybackStream:
		return client.playbackStreams()
	case configuration.RecordStream:
		return client.recordStreams()
	}
	return nil, fmt.Errorf("unknown target type %q", targetType)
}

func newApplicationStream(propList map[string]string, paStream interface{}) Stream {
	name := propList["application.name"]
	if name == "" {
		name = propList["media.name"]
	}

	// Create unique ID by combining stream restore ID with object ID
	uniqueId := propList["module-stream-restore.id"]
	if objectId := propList["object.id"]; objectId != "" {
		uniqueId = uniqueId + ":" + objectId
	}

	return Stream{
		Name:       name,
		FullName:   uniqueId,
		BinaryName: propList["application.process.binary"],
		MediaName:  propList["media.name"],
		ProcessID:  parseProcessID(propList["application.process.id"]),
		paStream:   paStream,
	}
}

func parseProcessID(value string) int {
	processID, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return processID
}

func (stream Stream) setVolume(volume float32) error {
	switch st := stream.paStream.(type) {
	case pulseaudio.Sink:
		return st.SetVolume(volume)
	case pulseaudio.SinkInput:
		return st.SetVolume(volume)
	case pulseaudio.Source:
		return st.SetVolume(volume)
	case pulseaudio.SourceOutput:
		return st.SetVolume(volume)
	}
	return nil
}

// GetAudioSources returns all audio sources in a format suitable for the UI
func (client *PAClient) GetAudioSources() []AudioSource {
	// Volume is a placeholder: the UI shows control values, not stream volumes
	const volume = 75

	sources := []AudioSource{}
	for _, targetType := range []configuration.PulseAudioTargetType{
		configuration.OutputDevice,
		configuration.InputDevice,
		configuration.PlaybackStream,
		configuration.RecordStream,
	} {
		streams, err := client.streams(targetType)
		if err != nil {
			client.log.Error().Err(err).Msg("Failed to list audio sources")
			continue
		}
		for _, stream := range streams {
			sources = append(sources, AudioSource{
				ID:         stream.FullName,
				Name:       stream.Name,
				BinaryName: stream.BinaryName,
				Type:       string(targetType),
				Volume:     volume,
			})
		}
	}

	return sources
}

func (client *PAClient) List() {
	outputs, _ := client.outputs()
	for _, stream := range outputs {
		client.log.Info().Msgf("Found output device:\t%s", stream.Name)
	}
	inputs, _ := client.inputs()
	for _, stream := range inputs {
		client.log.Info().Msgf("Found input device:\t%s", stream.Name)
	}
	playbackStreams, _ := client.playbackStreams()
	for _, stream := range playbackStreams {
		client.log.Info().Msgf("Found playback stream:\t%s", streamDisplayName(stream))
	}
	recordStreams, _ := client.recordStreams()
	for _, stream := range recordStreams {
		client.log.Info().Msgf("Found record stream:\t%s", streamDisplayName(stream))
	}
}

func streamDisplayName(stream Stream) string {
	if stream.BinaryName != "" {
		return fmt.Sprintf("%s (%s)", stream.Name, stream.BinaryName)
	}
	return stream.Name
}

// ListDetailed shows detailed information about streams including all properties
func (client *PAClient) ListDetailed() {
	playbackStreams, _ := client.playbackStreams()
	client.log.Info().Msg("=== Detailed Playback Streams ===")
	for i, stream := range playbackStreams {
		if sinkInput, ok := stream.paStream.(pulseaudio.SinkInput); ok {
			client.log.Info().Msgf("Stream %d: %s", i+1, stream.Name)
			client.log.Info().Msgf("  Full Name: %s", stream.FullName)
			if stream.BinaryName != "" {
				client.log.Info().Msgf("  Binary Name: %s", stream.BinaryName)
			}
			client.log.Info().Msg("  Properties:")
			for key, value := range sinkInput.PropList {
				client.log.Info().Msgf("    %s: %s", key, value)
			}
			client.log.Info().Msg("---")
		}
	}

	// Also list other types with basic info for completeness
	if outputs, _ := client.outputs(); len(outputs) > 0 {
		client.log.Info().Msg("=== Output Devices ===")
		for i, stream := range outputs {
			client.log.Info().Msgf("Device %d: %s (ID: %s)", i+1, stream.Name, stream.FullName)
		}
	}

	if inputs, _ := client.inputs(); len(inputs) > 0 {
		client.log.Info().Msg("=== Input Devices ===")
		for i, stream := range inputs {
			client.log.Info().Msgf("Device %d: %s (ID: %s)", i+1, stream.Name, stream.FullName)
		}
	}

	if recordStreams, _ := client.recordStreams(); len(recordStreams) > 0 {
		client.log.Info().Msg("=== Record Streams ===")
		for i, stream := range recordStreams {
			if sourceOutput, ok := stream.paStream.(pulseaudio.SourceOutput); ok {
				client.log.Info().Msgf("Stream %d: %s", i+1, stream.Name)
				client.log.Info().Msgf("  Full Name: %s", stream.FullName)
				client.log.Info().Msg("  Properties:")
				for key, value := range sourceOutput.PropList {
					client.log.Info().Msgf("    %s: %s", key, value)
				}
				client.log.Info().Msg("---")
			}
		}
	}
}

// SmartMatchStreams is a public wrapper for smart matching by source type and name
func (client *PAClient) SmartMatchStreams(sourceType configuration.PulseAudioTargetType, sourceName string) ([]Stream, *Stream) {
	if sourceType != configuration.PlaybackStream && sourceType != configuration.RecordStream {
		return nil, nil // No migration needed for device types
	}

	streams, err := client.streams(sourceType)
	if err != nil {
		client.log.Error().Err(err).Msg("Failed to list streams for matching")
		return nil, nil
	}

	return smartMatchStreams(streams, &configuration.TypedTarget{
		Type:       sourceType,
		Name:       sourceName,
		BinaryName: "", // Always empty for migration detection
	})
}

// smartMatchStreams implements the smart matching logic for application streams.
// It also returns the first legacy-matched stream that has a binary name, which
// can be used to migrate a legacy config entry.
func smartMatchStreams(streams []Stream, target *configuration.TypedTarget) ([]Stream, *Stream) {
	var matchedStreams []Stream
	var migrationStream *Stream

	for _, stream := range streams {
		if stream.Name != target.Name {
			continue
		}
		if target.BinaryName != "" {
			// Enhanced config: exact match required
			if stream.BinaryName == target.BinaryName {
				matchedStreams = append(matchedStreams, stream)
			}
			continue
		}
		// Legacy config: name match triggers migration
		matchedStreams = append(matchedStreams, stream)
		if migrationStream == nil && stream.BinaryName != "" {
			migrationStream = &stream
		}
	}

	return matchedStreams, migrationStream
}

// matchTarget returns the objects among streams (all of target.Type) that the target refers to
func (client *PAClient) matchTarget(streams []Stream, target *configuration.TypedTarget) []Stream {
	switch target.Type {
	case configuration.OutputDevice, configuration.InputDevice:
		name := target.Name
		matchFullName := false
		if name == "Default" {
			var err error
			if target.Type == configuration.OutputDevice {
				var sink pulseaudio.Sink
				sink, err = client.context.GetDefaultSink()
				name = sink.Name
			} else {
				var source pulseaudio.Source
				source, err = client.context.GetDefaultSource()
				name = source.Name
			}
			if err != nil {
				return nil
			}
			matchFullName = true
		}
		return lo.Filter(streams, func(stream Stream, i int) bool {
			if matchFullName {
				return stream.FullName == name
			}
			return stream.Name == name
		})
	case configuration.PlaybackStream, configuration.RecordStream:
		matchedStreams, migrationStream := smartMatchStreams(streams, target)
		if migrationStream != nil {
			client.log.Debug().
				Str("targetName", target.Name).
				Str("streamBinary", migrationStream.BinaryName).
				Msg("Config migration needed: would set binaryName")
		}
		return matchedStreams
	}
	return nil
}

// ProcessVolumeAction sets the volume of everything the action's target refers to
func (client *PAClient) ProcessVolumeAction(action configuration.Action, volumePercent float32) error {
	return client.ProcessVolumeActions([]configuration.Action{action}, volumePercent)
}

// ProcessVolumeActions sets the volume for several actions, querying each kind of
// PulseAudio object at most once
func (client *PAClient) ProcessVolumeActions(actions []configuration.Action, volumePercent float32) error {
	streamsByType := make(map[configuration.PulseAudioTargetType][]Stream)
	var errs []error

	for _, action := range actions {
		target, ok := action.Target.(*configuration.TypedTarget)
		if !ok {
			continue
		}

		streams, fetched := streamsByType[target.Type]
		if !fetched {
			var err error
			if streams, err = client.streams(target.Type); err != nil {
				errs = append(errs, err)
				continue
			}
			streamsByType[target.Type] = streams
		}

		for _, stream := range client.matchTarget(streams, target) {
			if err := stream.setVolume(volumePercent); err != nil {
				errs = append(errs, fmt.Errorf("failed to set volume of %s: %w", stream.Name, err))
				continue
			}
			client.log.Debug().Msgf("Set %s volume to %f", stream.Name, volumePercent)
		}
	}

	return errors.Join(errs...)
}

func (client *PAClient) SetDefaultOutput(action configuration.Action) error {
	target, ok := action.Target.(*configuration.Target)
	if !ok || target.Name == "" {
		return nil
	}

	outputs, err := client.outputs()
	if err != nil {
		return err
	}

	for _, stream := range outputs {
		if stream.Name == target.Name {
			client.log.Debug().Msgf("Setting %s as default output", stream.Name)
			// The pulseaudio library expects a name string, not a Sink object
			return client.context.SetDefaultSink(stream.FullName)
		}
	}
	return nil
}

// SetNewStreamCallback sets the callback function that will be called when new streams are detected
func (client *PAClient) SetNewStreamCallback(callback StreamEventCallback) {
	client.newStreamCallback = callback
}

// SetRemovedStreamCallback sets the callback function that will be called when streams are removed
func (client *PAClient) SetRemovedStreamCallback(callback StreamEventCallback) {
	client.removedStreamCallback = callback
}

// SetMediaStatusCallback sets the callback function that will be called when media playback status changes
func (client *PAClient) SetMediaStatusCallback(callback MediaStatusCallback) {
	client.mediaStatusCallback = callback
}

// StartStreamMonitoring begins monitoring for new audio streams
func (client *PAClient) StartStreamMonitoring() error {
	if !client.monitoringEnabled.CompareAndSwap(false, true) {
		return nil
	}

	// Subscribe to sink input and source output events (new streams)
	subscriptionMask := pulseaudio.SUBSCRIPTION_MASK_SINK_INPUT | pulseaudio.SUBSCRIPTION_MASK_SOURCE_OUTPUT
	updates, err := client.context.UpdatesByType(pulseaudio.DevType(subscriptionMask))
	if err != nil {
		client.monitoringEnabled.Store(false)
		return fmt.Errorf("failed to subscribe to PulseAudio events: %w", err)
	}

	// Initialize the known stream IDs from the current state
	playbackIDs, recordIDs, err := client.applicationStreamIDs()
	if err != nil {
		client.monitoringEnabled.Store(false)
		return err
	}

	client.log.Info().Msg("Started monitoring for new audio streams")

	go func() {
		for range updates {
			// The library keeps at most one pending event, so waiting here
			// coalesces bursts of events into a single rescan
			time.Sleep(streamUpdateSettleDelay)
			if !client.monitoringEnabled.Load() {
				return
			}
			if newPlaybackIDs, newRecordIDs, err := client.handleStreamUpdate(playbackIDs, recordIDs); err != nil {
				client.log.Error().Err(err).Msg("Failed to handle stream update")
			} else {
				playbackIDs, recordIDs = newPlaybackIDs, newRecordIDs
			}
		}
	}()

	return nil
}

// StopStreamMonitoring stops monitoring for new audio streams
func (client *PAClient) StopStreamMonitoring() {
	if client.monitoringEnabled.CompareAndSwap(true, false) {
		client.log.Info().Msg("Stopped monitoring for new audio streams")
	}
}

func (client *PAClient) applicationStreamIDs() (map[string]bool, map[string]bool, error) {
	playbackStreams, err := client.playbackStreams()
	if err != nil {
		return nil, nil, err
	}
	recordStreams, err := client.recordStreams()
	if err != nil {
		return nil, nil, err
	}
	return streamIDs(playbackStreams), streamIDs(recordStreams), nil
}

func streamIDs(streams []Stream) map[string]bool {
	ids := make(map[string]bool, len(streams))
	for _, stream := range streams {
		ids[stream.FullName] = true
	}
	return ids
}

// handleStreamUpdate compares the current streams against the previously known
// ones, fires the new/removed callbacks and returns the current stream IDs
func (client *PAClient) handleStreamUpdate(previousPlaybackIDs, previousRecordIDs map[string]bool) (map[string]bool, map[string]bool, error) {
	playbackStreams, err := client.playbackStreams()
	if err != nil {
		return nil, nil, err
	}
	recordStreams, err := client.recordStreams()
	if err != nil {
		return nil, nil, err
	}

	client.notifyStreamChanges(playbackStreams, previousPlaybackIDs, configuration.PlaybackStream)
	client.notifyStreamChanges(recordStreams, previousRecordIDs, configuration.RecordStream)

	return streamIDs(playbackStreams), streamIDs(recordStreams), nil
}

func (client *PAClient) notifyStreamChanges(streams []Stream, previousIDs map[string]bool, streamType configuration.PulseAudioTargetType) {
	for _, stream := range streams {
		if previousIDs[stream.FullName] {
			continue
		}
		client.log.Info().
			Str("streamType", string(streamType)).
			Str("streamName", stream.Name).
			Str("binaryName", stream.BinaryName).
			Str("streamID", stream.FullName).
			Msg("New stream detected")

		if client.newStreamCallback != nil {
			client.newStreamCallback(stream, streamType)
		}
	}

	currentIDs := streamIDs(streams)
	for streamID := range previousIDs {
		if currentIDs[streamID] {
			continue
		}
		client.log.Info().
			Str("streamType", string(streamType)).
			Str("streamID", streamID).
			Msg("Stream removed")

		if client.removedStreamCallback != nil {
			// We only have the ID; the name can't be recovered after removal
			client.removedStreamCallback(Stream{
				FullName:   streamID,
				Name:       "Unknown",
				BinaryName: "Unknown",
			}, streamType)
		}
	}
}

// ProcessMediaControlAction handles media control actions like play/pause
func (client *PAClient) ProcessMediaControlAction(action configuration.Action) error {
	switch action.Type {
	case configuration.MediaPlayPause:
		client.log.Info().Msg("Executing media play/pause command")
		if err := exec.Command("playerctl", "play-pause").Run(); err != nil {
			return fmt.Errorf("failed to send media play/pause command: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported media control action: %s", action.Type)
	}
}

// StartMediaStatusMonitoring begins monitoring MPRIS media players for playback status changes
func (client *PAClient) StartMediaStatusMonitoring() error {
	conn, err := dbus.SessionBus()
	if err != nil {
		return fmt.Errorf("failed to connect to session bus: %w", err)
	}

	// Monitor PropertiesChanged signals from all MPRIS media players
	rule := "type='signal',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged',path='/org/mpris/MediaPlayer2'"
	call := conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, rule)
	if call.Err != nil {
		return fmt.Errorf("failed to add D-Bus match rule: %w", call.Err)
	}

	// Create channel for receiving signals
	ch := make(chan *dbus.Signal, 10)
	conn.Signal(ch)

	// Start goroutine to handle signals
	go func() {
		var currentPlayingStatus bool

		for signal := range ch {
			if signal.Name != "org.freedesktop.DBus.Properties.PropertiesChanged" || len(signal.Body) < 2 {
				continue
			}

			// Check if this is an MPRIS player signal
			if !strings.Contains(string(signal.Path), "/org/mpris/MediaPlayer2") {
				continue
			}

			interfaceName, ok := signal.Body[0].(string)
			if !ok || interfaceName != "org.mpris.MediaPlayer2.Player" {
				continue
			}

			properties, ok := signal.Body[1].(map[string]dbus.Variant)
			if !ok {
				continue
			}

			statusVariant, exists := properties["PlaybackStatus"]
			if !exists {
				continue
			}
			status, ok := statusVariant.Value().(string)
			if !ok {
				continue
			}

			// Only trigger callback if status actually changed
			newPlayingStatus := status == "Playing"
			if newPlayingStatus == currentPlayingStatus {
				continue
			}
			currentPlayingStatus = newPlayingStatus
			client.log.Debug().
				Bool("isPlaying", newPlayingStatus).
				Str("sender", string(signal.Sender)).
				Msg("Media playback status changed")

			if client.mediaStatusCallback != nil {
				client.mediaStatusCallback(newPlayingStatus)
			}
		}
	}()

	client.log.Info().Msg("MPRIS media status monitoring started")
	return nil
}
