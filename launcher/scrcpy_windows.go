//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"
)

// Audio through scrcpy: scrcpy's server captures Android's audio output inside
// the guest (AudioPlaybackCapture, Android 11+), which also stops Android from
// playing it through the emulator's own sound device, and streams it over adb
// to scrcpy.exe on Windows, which plays it with a buffer we control. This
// bypasses the emulator's audio output, which crackles on some PCs.

func scrcpyDir() string { return filepath.Join(sdkDir, "scrcpy") }
func scrcpyExe() string { return filepath.Join(scrcpyDir(), "scrcpy.exe") }

var scrcpyAssetRe = regexp.MustCompile(`^scrcpy-win64-v[\d.]+\.zip$`)

// ensureScrcpy downloads the latest scrcpy for Windows if it isn't installed.
func ensureScrcpy() error {
	if _, err := os.Stat(scrcpyExe()); err == nil {
		return nil
	}
	data, err := fetchBytes("https://api.github.com/repos/Genymobile/scrcpy/releases/latest", 20*time.Second)
	if err != nil {
		return fmt.Errorf("scrcpy release lookup: %w", err)
	}
	var rel ghRelease
	if err := json.Unmarshal(data, &rel); err != nil {
		return err
	}
	for _, a := range rel.Assets {
		if scrcpyAssetRe.MatchString(a.Name) {
			zip := filepath.Join(dataDir, "downloads", a.Name)
			logf("scrcpy: downloading %s", a.Name)
			if err := download(a.URL, zip, "", "scrcpy", func(s string) { logf("%s", s) }); err != nil {
				return err
			}
			if err := unzipTop(zip, scrcpyDir(), nil); err != nil {
				return err
			}
			_ = os.Remove(zip)
			return nil
		}
	}
	return fmt.Errorf("no Windows scrcpy build in release %s", rel.TagName)
}

// runScrcpyAudio keeps an audio-only scrcpy running until stop is closed.
func runScrcpyAudio(serial string, bufferMs int, stop <-chan struct{}) {
	if bufferMs <= 0 {
		bufferMs = 120
	}
	logPath := filepath.Join(dataDir, "scrcpy.log")
	lf, _ := os.Create(logPath)
	defer func() {
		if lf != nil {
			lf.Close()
		}
	}()
	for attempt := 1; attempt <= 10; attempt++ {
		cmd := exec.Command(scrcpyExe(), "-s", serial,
			"--no-video", "--no-control", "--no-window",
			"--audio-source=output", "--audio-codec=raw",
			fmt.Sprintf("--audio-buffer=%d", bufferMs))
		cmd.Dir = scrcpyDir()
		cmd.Env = append(os.Environ(), "ADB="+adbExe())
		cmd.SysProcAttr = hiddenProcAttr()
		if lf != nil {
			cmd.Stdout, cmd.Stderr = lf, lf
		}
		if err := cmd.Start(); err != nil {
			logf("scrcpy: start failed: %v", err)
			return
		}
		logf("scrcpy: audio running (buffer %d ms, attempt %d)", bufferMs, attempt)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-stop:
			_ = cmd.Process.Kill()
			return
		case err := <-done:
			logf("scrcpy: exited (%v); see scrcpy.log", err)
		}
		select {
		case <-stop:
			return
		case <-time.After(2 * time.Second):
		}
	}
	logf("scrcpy: giving up after repeated exits")
}
