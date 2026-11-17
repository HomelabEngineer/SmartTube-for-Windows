package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config is stored as config.json in the data directory. Every field has a
// sensible default, so the file only needs the values a user wants to change.
type Config struct {
	Channel       string   `json:"channel"`       // "stable" or "beta"
	AutoUpdate    bool     `json:"autoUpdate"`    // check GitHub for a new SmartTube APK on every launch
	Fullscreen    bool     `json:"fullscreen"`    // borderless full-screen emulator window
	Embed         bool     `json:"embedWindow"`   // show the emulator inside the launcher's own full-screen window (needed for controllers in Xbox full-screen mode)
	ExitOnHome    bool     `json:"exitOnHome"`    // close everything when SmartTube is exited to the Android TV home screen
	Gamepad       bool     `json:"gamepad"`       // forward Xbox/XInput controllers as TV-remote keys
	InputMethod   string   `json:"inputMethod"`   // auto (verify console, else keyboard), console, keyboard
	GamepadAlways bool     `json:"gamepadAlways"` // forward controller input even when SmartTube isn't the focused window
	RAMMB         int      `json:"ramMB"`         // guest RAM
	Cores         int      `json:"cores"`         // guest CPU cores
	GPU           string   `json:"gpu"`           // emulator -gpu mode: auto, host, swiftshader_indirect, angle_indirect
	Width         int      `json:"width"`         // guest screen size
	Height        int      `json:"height"`        //
	Density       int      `json:"density"`       // guest DPI (320 = 1080p TV, 213 = 720p TV)
	APILevel      int      `json:"apiLevel"`      // 0 = pick automatically
	ExtraArgs     []string `json:"extraEmulatorArgs"`
	// Audio: the emulator asks the host for ~3 ms of audio buffering, which many
	// drivers can't sustain (crackles/pops). AudioLatencyMs forces a larger buffer.
	AudioLatencyMs  int               `json:"audioLatencyMs"`  // 0 = emulator default; try 60 or 120
	AudioDriver     string            `json:"audioDriver"`     // "" = default, or "dsound" / "winwave"
	AudioSampleRate int               `json:"audioSampleRate"` // 0 = default (44100); 48000 matches most Windows devices
	EmulatorEnv     map[string]string `json:"emulatorEnv"`     // extra environment variables for the emulator
	// Video: "host" decodes on the Windows side (emulator's goldfish codecs);
	// "guest" decodes inside Android on its own virtual CPUs.
	VideoDecoding    string `json:"videoDecoding"`
	EmulatorPriority string `json:"emulatorPriority"` // normal, above, high
	// Audio output: "emulator" (the emulator's own sound device) or "scrcpy"
	// (capture Android's audio and play it on Windows, bypassing the emulator).
	AudioOutput string `json:"audioOutput"`
	// SmartTube settings to apply once through SmartTube's own menus. Path is
	// the sidebar section, the settings card, any sub-menus, then the tickbox
	// label. Edit the path if SmartTube moves an option; [] disables this.
	SmartTubeSettings   []SmartTubeSetting `json:"smartTubeSettings"`
	Debug               bool               `json:"debug"` // detailed diagnostics in launcher.log plus emulator.log, scrcpy.log, smarttube-ui.xml
	ScrcpyAudioBufferMs int                `json:"scrcpyAudioBufferMs"`
	GitHubRepo          string             `json:"githubRepo"`     // where SmartTube releases are published
	ConsolePort         int                `json:"consolePort"`    // emulator console port (even number 5554-5584)
	BootTimeoutSec      int                `json:"bootTimeoutSec"` //
}

func defaultConfig() Config {
	return Config{
		Channel:             "stable",
		AutoUpdate:          true,
		Fullscreen:          true,
		Embed:               false,
		ExitOnHome:          true,
		Gamepad:             true,
		InputMethod:         "auto",
		RAMMB:               3072,
		Cores:               2,
		GPU:                 "auto",
		Width:               1920,
		Height:              1080,
		Density:             320,
		APILevel:            0,
		GitHubRepo:          "yuliskov/SmartTube",
		ConsolePort:         5554,
		BootTimeoutSec:      240,
		AudioLatencyMs:      60,
		VideoDecoding:       "host",
		AudioOutput:         "scrcpy",
		ScrcpyAudioBufferMs: 200,
		EmulatorPriority:    "normal",
		SmartTubeSettings: []SmartTubeSetting{{
			Path:    []string{"Settings", "Player", "Developer options", "Codec selection: prefer avc over vp9"},
			OpensTo: "Video presets",
			Checked: true,
		}},
		EmulatorEnv: map[string]string{},
	}
}

func (c Config) PackageName() string {
	if c.Channel == "beta" {
		return "org.smarttube.beta"
	}
	return "org.smarttube.stable"
}

func loadConfig(dir string) Config {
	c := defaultConfig()
	p := filepath.Join(dir, "config.json")
	b, err := os.ReadFile(p)
	if err == nil {
		if json.Unmarshal(b, &c) == nil {
			saveJSON(p, c) // adds settings introduced by newer versions, keeping the user's values
		}
	} else {
		// Write the defaults so the user has something to edit.
		saveJSON(p, c)
	}
	if c.Channel != "beta" {
		c.Channel = "stable"
	}
	if c.ConsolePort < 5554 || c.ConsolePort > 5584 || c.ConsolePort%2 != 0 {
		c.ConsolePort = 5554
	}
	if c.BootTimeoutSec < 60 {
		c.BootTimeoutSec = 240
	}
	if c.InputMethod != "console" && c.InputMethod != "keyboard" {
		c.InputMethod = "auto"
	}
	if c.AudioOutput != "emulator" {
		c.AudioOutput = "scrcpy"
	}
	if c.VideoDecoding != "guest" {
		c.VideoDecoding = "host"
	}
	if c.GitHubRepo == "" {
		c.GitHubRepo = "yuliskov/SmartTube"
	}
	return c
}

// SmartTubeSetting is a tickbox in SmartTube's settings, reached by Path.
type SmartTubeSetting struct {
	Path    []string `json:"path"`
	OpensTo string   `json:"opensTo"` // a label shown once the card's menu has opened (its first entry)
	Checked bool     `json:"checked"`
}

// settingsAutomationVersion is bumped when the automation itself improves, so
// settings that failed with an older launcher are tried again.
const settingsAutomationVersion = "v5:"

// settingsKey identifies a settings list, so a changed list is applied again.
func settingsKey(list []SmartTubeSetting) string {
	b, _ := json.Marshal(list)
	return settingsAutomationVersion + string(b)
}

// emulatorEnv returns the environment variables passed to the emulator.
// The emulator's audio is QEMU's: on Windows it plays through DirectSound
// ("dsound") or waveOut ("winwave"), both tuned with QEMU_* variables.
// PULSE_LATENCY_MSEC is the equivalent knob on Linux hosts.
func emulatorEnv(c Config) map[string]string {
	env := map[string]string{}
	if ms := c.AudioLatencyMs; ms > 0 {
		rate := c.AudioSampleRate
		if rate <= 0 {
			rate = 44100
		}
		samples := (rate*ms/1000 + 255) / 256 * 256 // per-buffer samples, multiple of 256
		bytes := samples * 4                        // 16-bit stereo
		env["QEMU_DSOUND_LATENCY_MILLIS"] = fmt.Sprint(ms)
		env["QEMU_DSOUND_BUFSIZE_OUT"] = fmt.Sprint(max(16384, bytes*2))
		env["QEMU_WINWAVE_DAC_SAMPLES"] = fmt.Sprint(samples)
		env["QEMU_WINWAVE_DAC_HEADERS"] = "4"
		env["PULSE_LATENCY_MSEC"] = fmt.Sprint(ms)
	}
	if c.AudioDriver != "" {
		env["QEMU_AUDIO_DRV"] = c.AudioDriver
	}
	if c.AudioSampleRate > 0 {
		env["QEMU_AUDIO_DAC_FIXED_SETTINGS"] = "1"
		env["QEMU_AUDIO_DAC_FIXED_FREQ"] = fmt.Sprint(c.AudioSampleRate)
	}
	for k, v := range c.EmulatorEnv { // explicit values win
		env[k] = v
	}
	return env
}

// State is launcher bookkeeping (not meant to be edited).
type State struct {
	SystemImage      string `json:"systemImage"`               // e.g. system-images;android-34;android-tv;x86
	ImageABI         string `json:"imageAbi"`                  // x86 / x86_64
	EmulatorRev      string `json:"emulatorRev"`               //
	InstalledAPK     string `json:"installedApk"`              // asset name of the last APK installed
	InstalledSHA     string `json:"installedSha"`              // sha256 of the last APK installed
	LastAPKCheck     int64  `json:"lastApkCheck"`              // unix seconds
	SetupComplete    bool   `json:"setupComplete"`             //
	SettingsApplied  string `json:"smartTubeSettingsApplied"`  // key of the settings list last applied
	SettingsKey      string `json:"smartTubeSettingsKey"`      // key of the list being attempted
	SettingsAttempts int    `json:"smartTubeSettingsAttempts"` // attempts for SettingsKey
}

func loadState(dir string) State {
	var s State
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func saveState(dir string, s State) { saveJSON(filepath.Join(dir, "state.json"), s) }

func saveJSON(path string, v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, path)
	}
}
