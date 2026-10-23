package pulseaudio

import (
	"os"
	"testing"

	"github.com/0h41/pulsekontrol/src/configuration"
)

func TestWindowMatcherScores(t *testing.T) {
	tests := []struct {
		name   string
		window focusedWindow
		stream Stream
		want   func(score int) bool
	}{
		{
			name:   "app id equals binary name",
			window: focusedWindow{Title: "The Office - Netflix — Mozilla Firefox", AppID: "firefox"},
			stream: Stream{Name: "Firefox", BinaryName: "firefox"},
			want:   func(score int) bool { return score == scoreAppIDExact },
		},
		{
			name:   "common words in title and media name do not match",
			window: focusedWindow{Title: "The Office - Netflix — Mozilla Firefox", AppID: "firefox"},
			stream: Stream{Name: "Spotify", BinaryName: "spotify", MediaName: "The Beatles - Help"},
			want:   func(score int) bool { return score < minFocusScore },
		},
		{
			name:   "flatpak app id shares application word",
			window: focusedWindow{Title: "Spotify Premium", AppID: "com.spotify.Client"},
			stream: Stream{Name: "spotify", BinaryName: "spotify"},
			want:   func(score int) bool { return score >= scoreAppIDToken },
		},
		{
			name:   "generic stream names are not contained in titles",
			window: focusedWindow{Title: "Audio Stream Settings", AppID: "org.pulseaudio.pavucontrol"},
			stream: Stream{Name: "Audio Stream"},
			want:   func(score int) bool { return score < minFocusScore },
		},
		{
			name:   "media name contained in browser title",
			window: focusedWindow{Title: "Lofi Radio - YouTube — Chromium", AppID: "org.example.Browser"},
			stream: Stream{Name: "Chromium", BinaryName: "chrome", MediaName: "Lofi Radio - YouTube"},
			want:   func(score int) bool { return score == scoreTitleContains },
		},
		{
			name:   "stream from a child of the window process",
			window: focusedWindow{Title: "Terminal", AppID: "kitty", PID: os.Getppid()},
			stream: Stream{Name: "mpv", ProcessID: os.Getpid()},
			want:   func(score int) bool { return score == scoreProcessRelated },
		},
		{
			name:   "stream from a parent of the window process",
			window: focusedWindow{Title: "Game", AppID: "game", PID: os.Getpid()},
			stream: Stream{Name: "Launcher", ProcessID: os.Getppid()},
			want:   func(score int) bool { return score < minFocusScore },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if score := newWindowMatcher(test.window).score(test.stream); !test.want(score) {
				t.Errorf("score = %d", score)
			}
		})
	}
}

func TestNormalizeMatchString(t *testing.T) {
	if got := normalizeMatchString("  Lofi Radio -- YouTube — Mozilla_Firefox "); got != "lofi radio youtube mozilla firefox" {
		t.Errorf("normalizeMatchString = %q", got)
	}
}

func TestAudioSourceMatches(t *testing.T) {
	active := AudioSource{Type: "PlaybackStream", Name: "Firefox", BinaryName: "firefox"}

	tests := []struct {
		source configuration.Source
		want   bool
	}{
		{configuration.Source{Type: configuration.PlaybackStream, Name: "Firefox", BinaryName: "firefox"}, true},
		{configuration.Source{Type: configuration.PlaybackStream, Name: "Firefox"}, true}, // legacy
		{configuration.Source{Type: "playbackstream", Name: "Firefox"}, true},
		{configuration.Source{Type: configuration.PlaybackStream, Name: "Firefox", BinaryName: "firefox-bin"}, false},
		{configuration.Source{Type: configuration.RecordStream, Name: "Firefox"}, false},
	}
	for _, test := range tests {
		if got := active.Matches(test.source); got != test.want {
			t.Errorf("Matches(%+v) = %v, want %v", test.source, got, test.want)
		}
	}
}
