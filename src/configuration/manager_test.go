package configuration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newTestManager(t *testing.T) *ConfigManager {
	t.Helper()
	config := GetDefaultConfig()
	return NewConfigManager(config, filepath.Join(t.TempDir(), "config.yaml"))
}

func countEvents(cm *ConfigManager, topic string) *int {
	count := new(int)
	cm.Subscribe(topic, func(interface{}) { *count++ })
	return count
}

var (
	firefox = Source{Type: PlaybackStream, Name: "Firefox", BinaryName: "firefox"}
	spotify = Source{Type: PlaybackStream, Name: "Spotify", BinaryName: "spotify"}
)

func TestAssignSourcesMovesSourceAndNotifiesOnce(t *testing.T) {
	cm := newTestManager(t)
	cm.AssignSource("knob", "knob1", firefox)

	assigned := countEvents(cm, TopicSourceAssigned)
	changed := countEvents(cm, TopicAssignmentsChanged)

	cm.AssignSources("slider", "slider2", firefox, spotify, spotify)

	config := cm.GetConfig()
	if got := config.Controls.Knobs["knob1"].Sources; len(got) != 0 {
		t.Errorf("knob1 sources = %v, want source moved away", got)
	}
	if got := config.Controls.Sliders["slider2"].Sources; len(got) != 2 || got[0] != firefox || got[1] != spotify {
		t.Errorf("slider2 sources = %v, want [firefox spotify]", got)
	}
	if *assigned != 2 {
		t.Errorf("source.assigned events = %d, want 2", *assigned)
	}
	if *changed != 1 {
		t.Errorf("assignments.changed events = %d, want 1", *changed)
	}

	// Assigning again changes nothing and publishes nothing
	cm.AssignSources("slider", "slider2", firefox)
	if *changed != 1 {
		t.Errorf("assignments.changed events after no-op = %d, want 1", *changed)
	}
}

func TestAssignSourceToUnknownControlKeepsExistingAssignment(t *testing.T) {
	cm := newTestManager(t)
	cm.AssignSource("slider", "slider1", firefox)
	cm.AssignSource("slider", "slider99", firefox)

	if got := cm.GetConfig().Controls.Sliders["slider1"].Sources; len(got) != 1 {
		t.Errorf("slider1 sources = %v, want firefox kept", got)
	}
}

func TestGetConfigReturnsDeepCopy(t *testing.T) {
	cm := newTestManager(t)
	cm.AssignSource("slider", "slider1", firefox)

	config := cm.GetConfig()
	slider := config.Controls.Sliders["slider1"]
	slider.Sources[0] = spotify
	config.Controls.Sliders["slider1"] = slider
	delete(config.Controls.Knobs, "knob1")

	fresh := cm.GetConfig()
	if fresh.Controls.Sliders["slider1"].Sources[0] != firefox {
		t.Error("modifying a returned config changed the manager's sources")
	}
	if _, ok := fresh.Controls.Knobs["knob1"]; !ok {
		t.Error("modifying a returned config changed the manager's knobs")
	}
}

func TestSubscriberMayCallBackIntoManager(t *testing.T) {
	cm := newTestManager(t)
	cm.Subscribe(TopicAssignmentsChanged, func(interface{}) {
		_ = cm.GetConfig()
		cm.UpdateControlValue("slider", "slider1", 10)
	})

	cm.AssignSource("slider", "slider1", firefox) // Would deadlock if notified under the lock

	if got := cm.GetConfig().Controls.Sliders["slider1"].Value; got != 10 {
		t.Errorf("slider1 value = %d, want 10", got)
	}
}

func TestMigrateSourceBinaryNameKeepsPosition(t *testing.T) {
	cm := newTestManager(t)
	legacy := Source{Type: PlaybackStream, Name: "Firefox"}
	cm.AssignSources("slider", "slider1", spotify, legacy)

	if !cm.MigrateSourceBinaryName("slider", "slider1", legacy, "firefox") {
		t.Fatal("migration reported no change")
	}
	got := cm.GetConfig().Controls.Sliders["slider1"].Sources
	if len(got) != 2 || got[0] != spotify || got[1] != firefox {
		t.Errorf("sources = %v, want [spotify firefox]", got)
	}

	if cm.MigrateSourceBinaryName("slider", "slider1", legacy, "firefox") {
		t.Error("second migration reported a change")
	}
}

func TestUnassignSource(t *testing.T) {
	cm := newTestManager(t)
	cm.AssignSources("knob", "knob3", firefox, spotify)
	changed := countEvents(cm, TopicAssignmentsChanged)

	cm.UnassignSource("knob", "knob3", firefox)
	cm.UnassignSource("knob", "knob3", firefox)

	if got := cm.GetConfig().Controls.Knobs["knob3"].Sources; len(got) != 1 || got[0] != spotify {
		t.Errorf("sources = %v, want [spotify]", got)
	}
	if *changed != 1 {
		t.Errorf("assignments.changed events = %d, want 1", *changed)
	}
}

func TestFlushWritesPendingChanges(t *testing.T) {
	cm := newTestManager(t)
	cm.UpdateControlValue("knob", "knob4", 42)
	cm.Flush()

	data, err := os.ReadFile(cm.configPath)
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if !strings.Contains(string(data), "value: 42") {
		t.Errorf("saved config does not contain the new value:\n%s", data)
	}
}

func TestConcurrentAccess(t *testing.T) {
	cm := newTestManager(t)
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				cm.UpdateControlValue("slider", fmt.Sprintf("slider%d", i%8+1), i%101)
				cm.AssignSource("knob", fmt.Sprintf("knob%d", i%8+1), firefox)
				config := cm.GetConfig()
				for range config.Controls.Knobs {
				}
			}
		}()
	}
	wg.Wait()
	cm.Flush()
}
