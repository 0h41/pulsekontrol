package pulseaudio

import (
	"slices"

	"github.com/0h41/pulsekontrol/src/configuration"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
	"github.com/the-jonsey/pulseaudio"
)

type Stream struct {
	name     string
	fullName string
	paStream interface{}
}

type AudioSource struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Volume int    `json:"volume"`
}

type PAClient struct {
	log             zerolog.Logger
	context         *pulseaudio.Client
	outputs         []Stream
	playbackStreams []Stream
	inputs          []Stream
	recordStreams   []Stream
}

func NewPAClient() *PAClient {
	context, err := pulseaudio.NewClient()
	if err != nil {
		panic(err)
	}
	client := &PAClient{
		log:             log.With().Str("module", "PulseAudio").Logger(),
		context:         context,
		outputs:         []Stream{},
		playbackStreams: []Stream{},
		inputs:          []Stream{},
		recordStreams:   []Stream{},
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
			ID:     stream.fullName,
			Name:   stream.name,
			Type:   "OutputDevice",
			Volume: volume,
		})
	})

	// Add inputs (sources)
	lo.ForEach(client.inputs, func(stream Stream, i int) {
		// Default volume
		volume := 75

		sources = append(sources, AudioSource{
			ID:     stream.fullName,
			Name:   stream.name,
			Type:   "InputDevice",
			Volume: volume,
		})
	})

	// Add playback streams (sink inputs)
	lo.ForEach(client.playbackStreams, func(stream Stream, i int) {
		// Default volume
		volume := 75

		sources = append(sources, AudioSource{
			ID:     stream.fullName,
			Name:   stream.name,
			Type:   "PlaybackStream",
			Volume: volume,
		})
	})

	// Add record streams (source outputs)
	lo.ForEach(client.recordStreams, func(stream Stream, i int) {
		// Default volume
		volume := 75

		sources = append(sources, AudioSource{
			ID:     stream.fullName,
			Name:   stream.name,
			Type:   "RecordStream",
			Volume: volume,
		})
	})

	return sources
}

func (client *PAClient) List() {
	client.refreshStreams()
	// List sinks
	lo.ForEach(client.outputs, func(stream Stream, i int) {
		client.log.Info().Msgf("Found output device:\t%s", stream.name)
	})
	// List sources
	lo.ForEach(client.inputs, func(stream Stream, i int) {
		client.log.Info().Msgf("Found input device:\t%s", stream.name)
	})
	// List sinks inputs
	lo.ForEach(client.playbackStreams, func(stream Stream, i int) {
		client.log.Info().Msgf("Found playback stream:\t%s", stream.name)
	})
	// List sources
	lo.ForEach(client.recordStreams, func(stream Stream, i int) {
		client.log.Info().Msgf("Found record stream:\t%s", stream.name)
	})
}

// ListDetailed shows detailed information about streams including all properties
func (client *PAClient) ListDetailed() {
	client.refreshStreams()
	
	// List detailed playback streams
	client.log.Info().Msg("=== Detailed Playback Streams ===")
	lo.ForEach(client.playbackStreams, func(stream Stream, i int) {
		if sinkInput, ok := stream.paStream.(pulseaudio.SinkInput); ok {
			client.log.Info().Msgf("Stream %d: %s", i+1, stream.name)
			client.log.Info().Msgf("  Full Name: %s", stream.fullName)
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
			client.log.Info().Msgf("Device %d: %s (ID: %s)", i+1, stream.name, stream.fullName)
		})
	}
	
	if len(client.inputs) > 0 {
		client.log.Info().Msg("=== Input Devices ===")
		lo.ForEach(client.inputs, func(stream Stream, i int) {
			client.log.Info().Msgf("Device %d: %s (ID: %s)", i+1, stream.name, stream.fullName)
		})
	}
	
	if len(client.recordStreams) > 0 {
		client.log.Info().Msg("=== Record Streams ===")
		lo.ForEach(client.recordStreams, func(stream Stream, i int) {
			if sourceOutput, ok := stream.paStream.(pulseaudio.SourceOutput); ok {
				client.log.Info().Msgf("Stream %d: %s", i+1, stream.name)
				client.log.Info().Msgf("  Full Name: %s", stream.fullName)
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
			name:     sink.Description,
			fullName: sink.Name,
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
			name:     source.Description,
			fullName: source.Name,
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
		return Stream{
			name:     name,
			fullName: sinkInput.PropList["module-stream-restore.id"],
			paStream: sinkInput,
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
		return Stream{
			name:     name,
			fullName: sourceOutput.PropList["module-stream-restore.id"],
			paStream: sourceOutput,
		}
	})
	return nil
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
						return stream.fullName == defaultSink.Name
					}))
				}
			} else {
				streams = slices.Concat(streams, lo.Filter(client.outputs, func(stream Stream, i int) bool {
					return stream.name == target.Name
				}))
			}
		} else if target.Type == configuration.InputDevice {
			if target.Name == "Default" {
				if defaultSource, err := client.context.GetDefaultSource(); err == nil {
					streams = slices.Concat(streams, lo.Filter(client.inputs, func(stream Stream, i int) bool {
						return stream.fullName == defaultSource.Name
					}))
				}
			} else {
				streams = slices.Concat(streams, lo.Filter(client.inputs, func(stream Stream, i int) bool {
					return stream.name == target.Name
				}))
			}
		} else if target.Type == configuration.PlaybackStream {
			streams = slices.Concat(streams, lo.Filter(client.playbackStreams, func(stream Stream, i int) bool {
				return stream.name == target.Name
			}))
		} else if target.Type == configuration.RecordStream {
			streams = slices.Concat(streams, lo.Filter(client.recordStreams, func(stream Stream, i int) bool {
				return stream.name == target.Name
			}))
		}
	case *configuration.Target:
	default:
	}
	lo.ForEach(streams, func(stream Stream, index int) {
		switch st := stream.paStream.(type) {
		case pulseaudio.Sink:
			st.SetVolume(volumePercent)
			client.log.Debug().Msgf("Set %s volume to %f", stream.name, volumePercent)
		case pulseaudio.SinkInput:
			st.SetVolume(volumePercent)
			client.log.Debug().Msgf("Set %s volume to %f", stream.name, volumePercent)
		case pulseaudio.Source:
			st.SetVolume(volumePercent)
			client.log.Debug().Msgf("Set %s volume to %f", stream.name, volumePercent)
		case pulseaudio.SourceOutput:
			st.SetVolume(volumePercent)
			client.log.Debug().Msgf("Set %s volume to %f", stream.name, volumePercent)
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
			if stream.name == target.Name {
				client.log.Debug().Msgf("Setting %s as default output", stream.name)
				// The pulseaudio library expects a name string, not a Sink object
				return client.context.SetDefaultSink(stream.fullName)
			}
		}
	default:
	}
	return nil
}
