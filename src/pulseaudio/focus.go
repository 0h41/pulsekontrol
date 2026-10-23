package pulseaudio

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/0h41/pulsekontrol/src/configuration"
)

// Match scores for focused window playback stream candidates
const (
	scoreProcessRelated = 100 // stream comes from the window's process or one of its descendants
	scoreAppIDExact     = 90  // window app_id equals the stream's application or binary name
	scoreTitleExact     = 85  // window title equals the stream's application or media name
	scoreAppIDToken     = 80  // window app_id shares a meaningful word with the stream's application or binary name
	scoreTitleContains  = 70  // window title contains the stream's application or media name (or vice versa)

	minFocusScore = 70
	// Streams scoring within this distance of the best candidate are included too
	focusScoreWindow = 10

	niriTimeout = 2 * time.Second
	// Safety limit when walking up the process tree
	maxProcessDepth = 64
)

type focusedWindow struct {
	Title string `json:"title"`
	AppID string `json:"app_id"`
	PID   int    `json:"pid"`
}

// FocusedWindowPlaybackSources returns the playback sources that best match the focused niri window
func (client *PAClient) FocusedWindowPlaybackSources() ([]configuration.Source, error) {
	window, err := getFocusedWindow()
	if err != nil {
		return nil, err
	}

	streams, err := client.playbackStreams()
	if err != nil {
		return nil, err
	}

	matcher := newWindowMatcher(window)
	scores := make([]int, len(streams))
	bestScore := 0
	for i, stream := range streams {
		scores[i] = matcher.score(stream)
		bestScore = max(bestScore, scores[i])

		if scores[i] > 0 {
			client.log.Debug().
				Str("windowTitle", window.Title).
				Str("windowAppID", window.AppID).
				Int("windowPID", window.PID).
				Str("streamName", stream.Name).
				Str("streamMediaName", stream.MediaName).
				Str("streamBinaryName", stream.BinaryName).
				Int("streamPID", stream.ProcessID).
				Int("score", scores[i]).
				Msg("Focused window playback stream candidate")
		}
	}

	if bestScore < minFocusScore {
		return nil, fmt.Errorf("no playback streams matched focused window %q (%s)", window.Title, window.AppID)
	}

	cutoff := max(bestScore-focusScoreWindow, minFocusScore)
	var sources []configuration.Source
	for i, stream := range streams {
		if scores[i] < cutoff {
			continue
		}
		source := configuration.Source{
			Type:       configuration.PlaybackStream,
			Name:       stream.Name,
			BinaryName: stream.BinaryName,
		}
		if !slices.Contains(sources, source) {
			sources = append(sources, source)
		}
	}

	return sources, nil
}

func getFocusedWindow() (focusedWindow, error) {
	ctx, cancel := context.WithTimeout(context.Background(), niriTimeout)
	defer cancel()

	output, err := exec.CommandContext(ctx, "niri", "msg", "--json", "focused-window").Output()
	if err != nil {
		return focusedWindow{}, fmt.Errorf("failed to query focused niri window: %w", err)
	}

	// niri prints "null" when no window is focused, which leaves the struct empty
	var window focusedWindow
	if err := json.Unmarshal(output, &window); err != nil {
		return focusedWindow{}, fmt.Errorf("failed to parse focused niri window: %w", err)
	}

	if window.Title == "" && window.AppID == "" && window.PID == 0 {
		return focusedWindow{}, fmt.Errorf("no focused window")
	}

	return window, nil
}

// windowMatcher scores playback streams against a window. The normalized window
// strings and the process tree lookups are computed once per matcher.
type windowMatcher struct {
	window      focusedWindow
	title       string
	appID       string
	appIDTokens []string
	parentPIDs  map[int]int
}

func newWindowMatcher(window focusedWindow) *windowMatcher {
	return &windowMatcher{
		window:      window,
		title:       normalizeMatchString(window.Title),
		appID:       normalizeMatchString(window.AppID),
		appIDTokens: meaningfulTokens(window.AppID),
		parentPIDs:  make(map[int]int),
	}
}

func (m *windowMatcher) score(stream Stream) int {
	name := normalizeMatchString(stream.Name)
	mediaName := normalizeMatchString(stream.MediaName)
	binaryName := normalizeMatchString(stream.BinaryName)

	score := 0

	if m.isWindowProcessOrDescendant(stream.ProcessID) {
		score = max(score, scoreProcessRelated)
	}

	if isExactMatch(m.appID, name, binaryName) {
		score = max(score, scoreAppIDExact)
	} else if hasCommonToken(m.appIDTokens, meaningfulTokens(stream.Name, stream.BinaryName)) {
		score = max(score, scoreAppIDToken)
	}

	if isExactMatch(m.title, name, mediaName) {
		score = max(score, scoreTitleExact)
	} else if isContainsMatch(m.title, name, mediaName) {
		score = max(score, scoreTitleContains)
	}

	return score
}

// isWindowProcessOrDescendant reports whether pid is the window's process or one of its
// descendants (e.g. a browser's audio service process)
func (m *windowMatcher) isWindowProcessOrDescendant(pid int) bool {
	if m.window.PID <= 0 || pid <= 0 {
		return false
	}

	for depth := 0; pid > 1 && depth < maxProcessDepth; depth++ {
		if pid == m.window.PID {
			return true
		}
		pid = m.parentPID(pid)
	}

	return false
}

func (m *windowMatcher) parentPID(pid int) int {
	if parent, ok := m.parentPIDs[pid]; ok {
		return parent
	}
	parent := readParentPID(pid)
	m.parentPIDs[pid] = parent
	return parent
}

// readParentPID returns the parent PID from /proc, or 0 if it cannot be determined
func readParentPID(pid int) int {
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0
	}

	for _, line := range strings.Split(string(status), "\n") {
		if value, found := strings.CutPrefix(line, "PPid:"); found {
			parent, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return 0
			}
			return parent
		}
	}

	return 0
}

func isExactMatch(reference string, candidates ...string) bool {
	if reference == "" {
		return false
	}
	return slices.Contains(candidates, reference)
}

// isContainsMatch reports whether reference contains a candidate or vice versa.
// Short or generic strings (e.g. "audio stream") are ignored.
func isContainsMatch(reference string, candidates ...string) bool {
	if len(reference) < 4 || isGeneric(reference) {
		return false
	}

	for _, candidate := range candidates {
		if len(candidate) < 4 || isGeneric(candidate) {
			continue
		}
		if strings.Contains(candidate, reference) || strings.Contains(reference, candidate) {
			return true
		}
	}

	return false
}

func hasCommonToken(a []string, b []string) bool {
	for _, token := range a {
		if slices.Contains(b, token) {
			return true
		}
	}
	return false
}

// isGeneric reports whether a normalized string consists only of ignorable words
func isGeneric(normalized string) bool {
	for _, token := range strings.Fields(normalized) {
		if !isIgnoredToken(token) {
			return false
		}
	}
	return true
}

func meaningfulTokens(values ...string) []string {
	var tokens []string
	for _, value := range values {
		for _, token := range strings.Fields(normalizeMatchString(value)) {
			if !isIgnoredToken(token) && !slices.Contains(tokens, token) {
				tokens = append(tokens, token)
			}
		}
	}
	return tokens
}

// Words too common in application names, app IDs and titles to identify an application
var ignoredTokens = map[string]struct{}{
	"and": {}, "app": {}, "application": {}, "audio": {}, "bin": {}, "client": {},
	"com": {}, "desktop": {}, "exe": {}, "for": {}, "github": {}, "input": {},
	"io": {}, "media": {}, "net": {}, "org": {}, "output": {}, "playback": {},
	"player": {}, "sound": {}, "stable": {}, "stream": {}, "the": {}, "video": {},
	"with": {}, "you": {},
}

func isIgnoredToken(token string) bool {
	if len(token) < 3 {
		return true
	}
	if _, ignored := ignoredTokens[token]; ignored {
		return true
	}
	for _, r := range token {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// normalizeMatchString lowercases the value and collapses every run of
// non-alphanumeric characters into a single space
func normalizeMatchString(value string) string {
	var builder strings.Builder
	lastWasSeparator := true

	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
			lastWasSeparator = false
			continue
		}
		if !lastWasSeparator {
			builder.WriteRune(' ')
			lastWasSeparator = true
		}
	}

	return strings.TrimSpace(builder.String())
}
