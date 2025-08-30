package pulseaudio

import (
	"fmt"
	"slices"

	"github.com/0h41/pulsekontrol/src/configuration"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
	"github.com/the-jonsey/pulseaudio"
)

type Stream struct {
	Name       string
	FullName   string
	BinaryName string
	paStream   interface{}
}

type AudioSource struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	BinaryName string `json:"binaryName"`
	Type       string `json:"type"`
	Volume     int    `json:"volume"`
}

// StreamEventCallback is called when a new stream is detected
type StreamEventCallback func(stream Stream, streamType configuration.PulseAudioTargetType)

type PAClient struct {
	log                    zerolog.Logger
	context                *pulseaudio.Client
	outputs                []Stream
	playbackStreams        []Stream
	inputs                 []Stream
	recordStreams          []Stream
	previousPlaybackIDs    map[string]bool
	previousRecordIDs      map[string]bool
	newStreamCallback      StreamEventCallback
	monitoringEnabled      bool
}

func NewPAClient() *PAClient {
	context, err := pulseaudio.NewClient()
	if err != nil {
		panic(err)
	}
	client := &PAClient{
		log:                    log.With().Str("module", "PulseAudio").Logger(),
		context:                context,
		outputs:                []Stream{},
		playbackStreams:        []Stream{},
		inputs:                 []Stream{},
		recordStreams:          []Stream{},
		previousPlaybackIDs:    make(map[string]bool),
		previousRecordIDs:      make(map[string]bool),
		newStreamCallback:      nil,
		monitoringEnabled:      false,
	}
	return client
}

// GetAudioSources returns all audio sources in a format suitable for the UI
func (client *PAClient) GetAudioSources() []AudioSource {
	client.refreshStreams()

	// Collect all sources
	sources := []AudioSource{}

	// Add outputs (sinks)
	lo.ForEach(client.outputs, func(stream Stream, i int) {
		// Default volume
		volume := 75

		// We use estimated values since we can't directly access the volume properties
		// In a real implementation, we would need to query the actual volume
		// using the pulseaudio library's methods

		sources = append(sources, AudioSource{
			ID:         stream.FullName,
			Name:       stream.Name,
			BinaryName: stream.BinaryName,
			Type:       "OutputDevice",
			Volume:     volume,
		})
	})

	// Add inputs (sources)
	lo.ForEach(client.inputs, func(stream Stream, i int) {
		// Default volume
		volume := 75

		sources = append(sources, AudioSource{
			ID:         stream.FullName,
			Name:       stream.Name,
			BinaryName: stream.BinaryName,
			Type:       "InputDevice",
			Volume:     volume,
		})
	})

	// Add playback streams (sink inputs)
	lo.ForEach(client.playbackStreams, func(stream Stream, i int) {
		// Default volume
		volume := 75

		sources = append(sources, AudioSource{
			ID:         stream.FullName,
			Name:       stream.Name,
			BinaryName: stream.BinaryName,
			Type:       "PlaybackStream",
			Volume:     volume,
		})
	})

	// Add record streams (source outputs)
	lo.ForEach(client.recordStreams, func(stream Stream, i int) {
		// Default volume
		volume := 75

		sources = append(sources, AudioSource{
			ID:         stream.FullName,
			Name:       stream.Name,
			BinaryName: stream.BinaryName,
			Type:       "RecordStream",
			Volume:     volume,
		})
	})

	return sources
}

func (client *PAClient) List() {
	client.refreshStreams()
	// List sinks
	lo.ForEach(client.outputs, func(stream Stream, i int) {
		client.log.Info().Msgf("Found output device:\t%s", stream.Name)
	})
	// List sources
	lo.ForEach(client.inputs, func(stream Stream, i int) {
		client.log.Info().Msgf("Found input device:\t%s", stream.Name)
	})
	// List sinks inputs
	lo.ForEach(client.playbackStreams, func(stream Stream, i int) {
		displayName := stream.Name
		if stream.BinaryName != "" {
			displayName = fmt.Sprintf("%s (%s)", stream.Name, stream.BinaryName)
		}
		client.log.Info().Msgf("Found playback stream:\t%s", displayName)
	})
	// List sources
	lo.ForEach(client.recordStreams, func(stream Stream, i int) {
		displayName := stream.Name
		if stream.BinaryName != "" {
			displayName = fmt.Sprintf("%s (%s)", stream.Name, stream.BinaryName)
		}
		client.log.Info().Msgf("Found record stream:\t%s", displayName)
	})
}

// ListDetailed shows detailed information about streams including all properties
func (client *PAClient) ListDetailed() {
	client.refreshStreams()
	
	// List detailed playback streams
	client.log.Info().Msg("=== Detailed Playback Streams ===")
	lo.ForEach(client.playbackStreams, func(stream Stream, i int) {
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
	})
	
	// Also list other types with basic info for completeness
	if len(client.outputs) > 0 {
		client.log.Info().Msg("=== Output Devices ===")
		lo.ForEach(client.outputs, func(stream Stream, i int) {
			client.log.Info().Msgf("Device %d: %s (ID: %s)", i+1, stream.Name, stream.FullName)
		})
	}
	
	if len(client.inputs) > 0 {
		client.log.Info().Msg("=== Input Devices ===")
		lo.ForEach(client.inputs, func(stream Stream, i int) {
			client.log.Info().Msgf("Device %d: %s (ID: %s)", i+1, stream.Name, stream.FullName)
		})
	}
	
	if len(client.recordStreams) > 0 {
		client.log.Info().Msg("=== Record Streams ===")
		lo.ForEach(client.recordStreams, func(stream Stream, i int) {
			if sourceOutput, ok := stream.paStream.(pulseaudio.SourceOutput); ok {
				client.log.Info().Msgf("Stream %d: %s", i+1, stream.Name)
				client.log.Info().Msgf("  Full Name: %s", stream.FullName)
				client.log.Info().Msg("  Properties:")
				for key, value := range sourceOutput.PropList {
					client.log.Info().Msgf("    %s: %s", key, value)
				}
				client.log.Info().Msg("---")
			}
		})
	}
}

func (client *PAClient) refreshStreams() error {
	// Sinks
	sinks, err := client.context.Sinks()
	if err != nil {
		panic(err)
	}
	client.outputs = lo.Map(sinks, func(sink pulseaudio.Sink, i int) Stream {
		return Stream{
			Name:     sink.Description,
			FullName: sink.Name,
			paStream: sink,
		}
	})
	// Sources
	sources, err := client.context.Sources()
	if err != nil {
		panic(err)
	}
	client.inputs = lo.Map(sources, func(source pulseaudio.Source, i int) Stream {
		return Stream{
			Name:     source.Description,
			FullName: source.Name,
			paStream: source,
		}
	})
	// Sinks inputs
	sinksInputs, err := client.context.SinkInputs()
	if err != nil {
		panic(err)
	}
	client.playbackStreams = lo.Map(sinksInputs, func(sinkInput pulseaudio.SinkInput, i int) Stream {
		var name string
		name = sinkInput.PropList["application.name"]
		if len(name) < 1 {
			name = sinkInput.PropList["media.name"]
		}
		binaryName := sinkInput.PropList["application.process.binary"]
		
		// Create unique ID by combining stream restore ID with object ID
		objectId := sinkInput.PropList["object.id"]
		uniqueId := sinkInput.PropList["module-stream-restore.id"]
		if objectId != "" {
			uniqueId = uniqueId + ":" + objectId
		}
		
		return Stream{
			Name:       name,
			FullName:   uniqueId,
			BinaryName: binaryName,
			paStream:   sinkInput,
		}
	})
	// Sources outputs
	sourcesOutputs, err := client.context.SourceOutputs()
	if err != nil {
		panic(err)
	}
	client.recordStreams = lo.Map(sourcesOutputs, func(sourceOutput pulseaudio.SourceOutput, i int) Stream {
		var name string
		name = sourceOutput.PropList["application.name"]
		if len(name) < 1 {
			name = sourceOutput.PropList["media.name"]
		}
		binaryName := sourceOutput.PropList["application.process.binary"]
		
		// Create unique ID by combining stream restore ID with object ID
		objectId := sourceOutput.PropList["object.id"]
		uniqueId := sourceOutput.PropList["module-stream-restore.id"]
		if objectId != "" {
			uniqueId = uniqueId + ":" + objectId
		}
		
		return Stream{
			Name:       name,
			FullName:   uniqueId,
			BinaryName: binaryName,
			paStream:   sourceOutput,
		}
	})
	return nil
}

// SmartMatchStreams is a public wrapper for smart matching by source type and name
func (client *PAClient) SmartMatchStreams(sourceType configuration.PulseAudioTargetType, sourceName string) ([]Stream, *Stream) {
	client.refreshStreams()
	
	target := &configuration.TypedTarget{
		Type: sourceType,
		Name: sourceName,
		BinaryName: "", // Always empty for migration detection
	}
	
	switch sourceType {
	case configuration.PlaybackStream:
		return client.smartMatchStreams(client.playbackStreams, target)
	case configuration.RecordStream:
		return client.smartMatchStreams(client.recordStreams, target)
	default:
		return nil, nil // No migration needed for device types
	}
}

// smartMatchStreams implements the smart matching logic for application streams
func (client *PAClient) smartMatchStreams(streams []Stream, target *configuration.TypedTarget) ([]Stream, *Stream) {
	var matchedStreams []Stream
	var migrationStream *Stream

	client.log.Debug().
		Str("targetName", target.Name).
		Str("targetBinaryName", target.BinaryName).
		Int("totalStreams", len(streams)).
		Msg("smartMatchStreams called")

	for _, stream := range streams {
		client.log.Debug().
			Str("streamName", stream.Name).
			Str("streamBinaryName", stream.BinaryName).
			Str("targetName", target.Name).
			Str("targetBinaryName", target.BinaryName).
			Msg("Checking stream for match")
			
		if target.BinaryName != "" {
			// Enhanced config: exact match required
			if stream.Name == target.Name && stream.BinaryName == target.BinaryName {
				client.log.Debug().
					Str("streamName", stream.Name).
					Str("streamBinaryName", stream.BinaryName).
					Msg("EXACT MATCH found")
				matchedStreams = append(matchedStreams, stream)
			}
		} else {
			// Legacy config: name match triggers migration
			if stream.Name == target.Name {
				client.log.Debug().
					Str("streamName", stream.Name).
					Str("streamBinaryName", stream.BinaryName).
					Msg("LEGACY MATCH found")
				matchedStreams = append(matchedStreams, stream)
				if migrationStream == nil && stream.BinaryName != "" {
					migrationStream = &stream
				}
			}
		}
	}
	
	client.log.Debug().
		Int("matchedCount", len(matchedStreams)).
		Msg("smartMatchStreams result")
	
	return matchedStreams, migrationStream
}

func (client *PAClient) ProcessVolumeAction(action configuration.Action, volumePercent float32) error {
	var streams []Stream
	client.refreshStreams()
	switch target := action.Target.(type) {
	case *configuration.TypedTarget:
		if target.Type == configuration.OutputDevice {
			if target.Name == "Default" {
				if defaultSink, err := client.context.GetDefaultSink(); err == nil {
					streams = slices.Concat(streams, lo.Filter(client.outputs, func(stream Stream, i int) bool {
						return stream.FullName == defaultSink.Name
					}))
				}
			} else {
				streams = slices.Concat(streams, lo.Filter(client.outputs, func(stream Stream, i int) bool {
					return stream.Name == target.Name
				}))
			}
		} else if target.Type == configuration.InputDevice {
			if target.Name == "Default" {
				if defaultSource, err := client.context.GetDefaultSource(); err == nil {
					streams = slices.Concat(streams, lo.Filter(client.inputs, func(stream Stream, i int) bool {
						return stream.FullName == defaultSource.Name
					}))
				}
			} else {
				streams = slices.Concat(streams, lo.Filter(client.inputs, func(stream Stream, i int) bool {
					return stream.Name == target.Name
				}))
			}
		} else if target.Type == configuration.PlaybackStream {
			matchedStreams, migrationNeeded := client.smartMatchStreams(client.playbackStreams, target)
			if migrationNeeded != nil {
				// TODO: Trigger migration callback here
				// For now, just log that migration would be needed
				client.log.Info().
					Str("targetName", target.Name).
					Str("streamBinary", migrationNeeded.BinaryName).
					Msg("Config migration needed: would set binaryName")
			}
			streams = slices.Concat(streams, matchedStreams)
		} else if target.Type == configuration.RecordStream {
			matchedStreams, migrationNeeded := client.smartMatchStreams(client.recordStreams, target)
			if migrationNeeded != nil {
				client.log.Info().
					Str("targetName", target.Name).
					Str("streamBinary", migrationNeeded.BinaryName).
					Msg("Config migration needed: would set binaryName")
			}
			streams = slices.Concat(streams, matchedStreams)
		}
	case *configuration.Target:
	default:
	}
	lo.ForEach(streams, func(stream Stream, index int) {
		switch st := stream.paStream.(type) {
		case pulseaudio.Sink:
			st.SetVolume(volumePercent)
			client.log.Debug().Msgf("Set %s volume to %f", stream.Name, volumePercent)
		case pulseaudio.SinkInput:
			st.SetVolume(volumePercent)
			client.log.Debug().Msgf("Set %s volume to %f", stream.Name, volumePercent)
		case pulseaudio.Source:
			st.SetVolume(volumePercent)
			client.log.Debug().Msgf("Set %s volume to %f", stream.Name, volumePercent)
		case pulseaudio.SourceOutput:
			st.SetVolume(volumePercent)
			client.log.Debug().Msgf("Set %s volume to %f", stream.Name, volumePercent)
		}
	})
	return nil
}

func (client *PAClient) SetDefaultOutput(action configuration.Action) error {
	client.refreshStreams()
	switch target := action.Target.(type) {
	case *configuration.Target:
		if target.Name == "" {
			return nil
		}

		// Find the output device
		for _, stream := range client.outputs {
			if stream.Name == target.Name {
				client.log.Debug().Msgf("Setting %s as default output", stream.Name)
				// The pulseaudio library expects a name string, not a Sink object
				return client.context.SetDefaultSink(stream.FullName)
			}
		}
	default:
	}
	return nil
}

// SetNewStreamCallback sets the callback function that will be called when new streams are detected
func (client *PAClient) SetNewStreamCallback(callback StreamEventCallback) {
	client.newStreamCallback = callback
}

// StartStreamMonitoring begins monitoring for new audio streams
func (client *PAClient) StartStreamMonitoring() error {
	if client.monitoringEnabled {
		return nil
	}

	// Subscribe to sink input and source output events (new streams)
	subscriptionMask := pulseaudio.SUBSCRIPTION_MASK_SINK_INPUT | pulseaudio.SUBSCRIPTION_MASK_SOURCE_OUTPUT
	updates, err := client.context.UpdatesByType(pulseaudio.DevType(subscriptionMask))
	if err != nil {
		return fmt.Errorf("failed to subscribe to PulseAudio events: %w", err)
	}

	// Initialize the previous stream IDs by getting current state
	client.refreshStreams()
	client.updatePreviousStreamIDs()

	client.monitoringEnabled = true
	client.log.Info().Msg("Started monitoring for new audio streams")

	// Start goroutine to handle updates
	go func() {
		for range updates {
			if !client.monitoringEnabled {
				break
			}
			client.handleStreamUpdate()
		}
	}()

	return nil
}

// StopStreamMonitoring stops monitoring for new audio streams
func (client *PAClient) StopStreamMonitoring() {
	if !client.monitoringEnabled {
		return
	}
	client.monitoringEnabled = false
	client.log.Info().Msg("Stopped monitoring for new audio streams")
}

// updatePreviousStreamIDs updates the tracking maps with current stream IDs
func (client *PAClient) updatePreviousStreamIDs() {
	// Clear previous IDs
	client.previousPlaybackIDs = make(map[string]bool)
	client.previousRecordIDs = make(map[string]bool)

	// Add current playback streams
	for _, stream := range client.playbackStreams {
		client.previousPlaybackIDs[stream.FullName] = true
	}

	// Add current record streams
	for _, stream := range client.recordStreams {
		client.previousRecordIDs[stream.FullName] = true
	}
}

// handleStreamUpdate is called when PulseAudio sends an update event
func (client *PAClient) handleStreamUpdate() {
	// Refresh to get latest streams
	client.refreshStreams()

	// Check for new playback streams
	for _, stream := range client.playbackStreams {
		if !client.previousPlaybackIDs[stream.FullName] {
			client.log.Info().
				Str("streamName", stream.Name).
				Str("binaryName", stream.BinaryName).
				Str("streamID", stream.FullName).
				Msg("New playback stream detected")
			
			if client.newStreamCallback != nil {
				client.newStreamCallback(stream, configuration.PlaybackStream)
			}
		}
	}

	// Check for new record streams
	for _, stream := range client.recordStreams {
		if !client.previousRecordIDs[stream.FullName] {
			client.log.Info().
				Str("streamName", stream.Name).
				Str("binaryName", stream.BinaryName).
				Str("streamID", stream.FullName).
				Msg("New record stream detected")
			
			if client.newStreamCallback != nil {
				client.newStreamCallback(stream, configuration.RecordStream)
			}
		}
	}

	// Update previous IDs for next comparison
	client.updatePreviousStreamIDs()
}
