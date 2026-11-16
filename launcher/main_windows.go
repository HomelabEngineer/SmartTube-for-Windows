//go:build windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	version = "dev" // set by -ldflags at build time
	dataDir string
	sdkDir  string
	avdHome string
	emuHome string
	logger  *log.Logger
	adbPort = "5137" // private adb server port, so we never clash with other Android tools
)

func itoa(i int) string { return strconv.Itoa(i) }

func logf(format string, a ...any) {
	if logger != nil {
		logger.Printf(format, a...)
	}
}

// say prints to the (optional) progress console and the log file.
func say(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	fmt.Fprintln(os.Stdout, msg)
	logf("%s", msg)
	splashText(strings.TrimSpace(msg))
}

func main() {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		local, _ = os.UserConfigDir()
	}
	dataDir = filepath.Join(local, "SmartTubeWin")
	sdkDir = filepath.Join(dataDir, "sdk")
	avdHome = filepath.Join(dataDir, "avd")
	emuHome = filepath.Join(dataDir, "emuhome")
	_ = os.MkdirAll(dataDir, 0o755)
	// The controller test writes its own log so it never overwrites the launcher's;
	// the previous launcher log is kept as launcher.prev.log.
	logName := "launcher.log"
	if len(os.Args) > 1 && strings.Contains(strings.ToLower(os.Args[1]), "gamepad-test") {
		logName = "gamepad-test.log"
	} else {
		_ = os.Rename(filepath.Join(dataDir, "launcher.log"), filepath.Join(dataDir, "launcher.prev.log"))
	}
	if lf, err := os.OpenFile(filepath.Join(dataDir, logName), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err == nil {
		logger = log.New(lf, "", log.LstdFlags)
	} else {
		logger = log.New(io.Discard, "", 0)
	}
	logf("SmartTube for Windows launcher %s", version)

	arg := ""
	if len(os.Args) > 1 {
		arg = strings.ToLower(strings.TrimLeft(os.Args[1], "-/"))
	}
	cfg := loadConfig(dataDir)
	st := loadState(dataDir)

	if arg == "purge" { // called by the uninstaller
		_ = os.RemoveAll(dataDir)
		return
	}
	if arg == "gamepad-test" {
		gamepadTest()
		return
	}
	if !singleInstance() {
		if w, ok := findEmulatorWindow(cfg.ConsolePort); ok {
			focusWindow(w.hwnd)
		}
		return
	}
	setupEnv()

	switch arg {
	case "setup", "repair":
		ensureConsole("SmartTube setup")
		if err := runSetup(&cfg, &st, arg == "repair"); err != nil {
			fail(err)
		}
		say("\nSetup complete.")
		if arg == "repair" {
			pause()
		}
		return
	case "reset":
		if !askYesNo("Reset SmartTube?\n\nThis erases the virtual Android TV device (your SmartTube settings and sign-in). SmartTube is reinstalled on the next launch.") {
			return
		}
		_ = os.RemoveAll(avdHome)
		st.InstalledAPK, st.InstalledSHA, st.LastAPKCheck = "", "", 0
		st.DefaultsDone, st.DefaultsAttempts = false, 0
		st.SetupComplete = false
		saveState(dataDir, st)
		ensureConsole("SmartTube setup")
		if err := runSetup(&cfg, &st, false); err != nil {
			fail(err)
		}
		say("Reset complete.")
		closeConsole()
		showInfo("SmartTube has been reset.")
		return
	case "update":
		st.LastAPKCheck = 0
	}

	if setupLooksComplete(st) {
		// Our own window first, so Windows hands us the foreground (see splash_windows.go).
		showSplash("Starting SmartTube...")
	}
	if !setupLooksComplete(st) {
		ensureConsole("SmartTube setup")
		say("SmartTube for Windows needs to download the Android TV runtime (about 1.5 GB, one time only).\n")
		if err := runSetup(&cfg, &st, false); err != nil {
			fail(err)
		}
	}
	allowAnyForeground() // as early as possible, while the launching front-end still owns the foreground
	if err := launch(cfg, &st, arg == "update"); err != nil {
		fail(err)
	}
}

func fail(err error) {
	logf("FATAL: %v", err)
	closeSplash()
	closeConsole()
	showError(fmt.Sprintf("%v\n\nLog: %s", err, filepath.Join(dataDir, "launcher.log")))
	os.Exit(1)
}

func pause() {
	if consoleAllocated {
		fmt.Println("Press Enter to close...")
		_, _ = fmt.Scanln()
	}
}

func setupEnv() {
	_ = os.MkdirAll(emuHome, 0o755)
	os.Setenv("ANDROID_SDK_ROOT", sdkDir)
	os.Setenv("ANDROID_HOME", sdkDir)
	os.Setenv("ANDROID_AVD_HOME", avdHome)
	os.Setenv("ANDROID_EMULATOR_HOME", emuHome)
	os.Setenv("ANDROID_USER_HOME", emuHome)
	os.Setenv("ANDROID_ADB_SERVER_PORT", adbPort)
}

func emulatorExe() string { return filepath.Join(sdkDir, "emulator", "emulator.exe") }
func adbExe() string      { return filepath.Join(sdkDir, "platform-tools", "adb.exe") }

func sysImgRel(st State) string {
	// system-images;android-34;android-tv;x86 -> system-images\android-34\android-tv\x86
	return strings.ReplaceAll(st.SystemImage, ";", `\`)
}

func setupLooksComplete(st State) bool {
	if !st.SetupComplete || st.SystemImage == "" {
		return false
	}
	for _, p := range []string{emulatorExe(), adbExe(), filepath.Join(sdkDir, sysImgRel(st), "system.img"),
		filepath.Join(avdHome, avdName+".ini")} {
		if _, err := os.Stat(p); err != nil {
			logf("setup incomplete: missing %s", p)
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------- setup

func runSetup(cfg *Config, st *State, force bool) error {
	progress := func(s string) { say("%s", s) }
	say("[1/5] Reading the Android SDK catalogue from Google...")
	repoData, err := fetchBytes(sdkRepoXML, 60*time.Second)
	if err != nil {
		return fmt.Errorf("could not reach Google's Android SDK repository: %w", err)
	}
	repo, err := parseRepo(repoData)
	if err != nil {
		return fmt.Errorf("could not read SDK catalogue: %w", err)
	}
	emu, err := repo.pickHostPackage("emulator", sdkRepoBase)
	if err != nil {
		return err
	}
	pt, err := repo.pickHostPackage("platform-tools", sdkRepoBase)
	if err != nil {
		pt = &sdkArchive{Path: "platform-tools", URL: platformToolsFallback}
	}
	imgData, err := fetchBytes(atvImageXML, 60*time.Second)
	if err != nil {
		return fmt.Errorf("could not read Android TV image list: %w", err)
	}
	imgRepo, err := parseRepo(imgData)
	if err != nil {
		return err
	}
	img, api, abi, err := imgRepo.pickTVImage(atvImageBase, cfg.APILevel)
	if err != nil {
		return err
	}
	say("      Emulator %s, Android TV API %d (%s)", emu.Revision, api, abi)

	dl := filepath.Join(dataDir, "downloads")
	type item struct {
		a     *sdkArchive
		label string
		dest  string
		check string
	}
	imgDest := filepath.Join(sdkDir, strings.ReplaceAll(img.Path, ";", string(filepath.Separator)))
	items := []item{
		{pt, "Platform tools", filepath.Join(sdkDir, "platform-tools"), "adb.exe"},
		{emu, "Android Emulator", filepath.Join(sdkDir, "emulator"), "emulator.exe"},
		{img, "Android TV system image", imgDest, "system.img"},
	}
	for i, it := range items {
		say("[%d/5] %s", i+2, it.label)
		marker := filepath.Join(it.dest, ".stw-rev")
		if !force {
			if b, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(b)) == it.a.URL {
				if _, err := os.Stat(filepath.Join(it.dest, it.check)); err == nil {
					say("      up to date")
					continue
				}
			}
		}
		zipPath := filepath.Join(dl, filepath.Base(it.a.URL))
		if err := download(it.a.URL, zipPath, it.a.SHA1, "      "+it.label, progress); err != nil {
			return fmt.Errorf("download of %s failed: %w", it.label, err)
		}
		say("      extracting...")
		if err := unzipTop(zipPath, it.dest, progress); err != nil {
			return fmt.Errorf("extracting %s failed: %w", it.label, err)
		}
		_ = os.WriteFile(marker, []byte(it.a.URL), 0o644)
		_ = os.Remove(zipPath)
	}

	st.SystemImage = img.Path
	st.ImageABI = abi
	st.EmulatorRev = emu.Revision
	if err := writeAVD(avdHome, sysImgRel(*st), api, abi, *cfg); err != nil {
		return fmt.Errorf("creating virtual device: %w", err)
	}
	say("      Virtual Android TV device created.")
	if err := checkAcceleration(); err != nil {
		return err
	}
	st.SetupComplete = true
	saveState(dataDir, *st)
	return nil
}

// checkAcceleration makes sure Windows Hypervisor Platform (or AEHD) is usable.
func checkAcceleration() error {
	out, err := runHidden(30*time.Second, emulatorExe(), "-accel-check")
	logf("accel-check: %s (err=%v)", out, err)
	lo := strings.ToLower(out)
	if err == nil && strings.Contains(lo, "usable") && !strings.Contains(lo, "not usable") && !strings.Contains(lo, "not installed") {
		say("      Hardware acceleration: OK")
		return nil
	}
	closeConsole()
	msg := "SmartTube needs hardware virtualization (Windows Hypervisor Platform) to run Android.\n\n" +
		strings.TrimSpace(out) + "\n\nEnable Windows Hypervisor Platform now? (Requires administrator rights and a restart.)"
	if askYesNo(msg) {
		if shellExecuteAdmin("dism.exe", "/online /enable-feature /featurename:HypervisorPlatform /all /norestart") {
			showInfo("Windows is enabling Hypervisor Platform. When it finishes, restart your PC and open SmartTube again.\n\n" +
				"If it still fails after restarting, turn on virtualization (SVM / AMD-V or Intel VT-x) in your BIOS/UEFI.")
		}
	}
	os.Exit(2)
	return nil
}

// ---------------------------------------------------------------- launch

func launch(cfg Config, st *State, forceUpdate bool) error {
	// Keep the device config in sync with config.json (RAM, cores, GPU, resolution).
	_ = writeAVDConfig(filepath.Join(avdHome, avdName+".avd"), sysImgRel(*st), st.ImageABI, cfg)

	port := cfg.ConsolePort
	for p := port; p <= 5584; p += 2 {
		if portFree(p) && portFree(p+1) {
			port = p
			break
		}
	}
	serial := "emulator-" + itoa(port)
	logf("using port %d", port)
	allowAnyForeground()
	runHidden(15*time.Second, adbExe(), "start-server")

	// Look for a newer SmartTube while Android boots.
	type apkResult struct {
		path, name, version, sha string
		err                      error
	}
	apkCh := make(chan apkResult, 1)
	go func() {
		p, n, v, s, err := fetchLatestAPK(cfg, st, forceUpdate)
		apkCh <- apkResult{p, n, v, s, err}
	}()

	scrcpyReady := make(chan error, 1)
	if cfg.AudioOutput == "scrcpy" {
		go func() { scrcpyReady <- ensureScrcpy() }()
	}
	emu, exited, err := startEmulator(cfg, port, true)
	if err != nil {
		return err
	}

	// Window handling.
	var emuPID atomic.Uint32
	var emuHwnd atomic.Uintptr
	var embedded atomic.Bool
	// frontHwnd is the window that should own the foreground: the launcher's
	// host window when SmartTube is embedded in it, else the emulator window.
	frontHwnd := func() uintptr {
		if embedded.Load() {
			return splashHost()
		}
		return emuHwnd.Load()
	}
	var appStarted atomic.Bool
	revealed := make(chan struct{})
	go func() {
		defer close(revealed)
		applied := 0
		var foundAt time.Time
		for i := 0; i < 600 && applied < 3; i++ {
			select {
			case <-exited:
				return
			case <-time.After(500 * time.Millisecond):
			}
			if w, ok := findEmulatorWindow(port); ok {
				if emuPID.Load() != w.pid {
					setProcessPriority(w.pid, cfg.EmulatorPriority)
				}
				emuPID.Store(w.pid)
				emuHwnd.Store(w.hwnd)
				if applied == 0 {
					logf("focus: emulator window found; splash owns foreground=%v", splashOwnsForeground())
				}
				if cfg.Embed && cfg.Fullscreen && !embedded.Load() && splashHost() != 0 {
					hideEmulatorToolbar(w)
					if embedInSplash(w.hwnd) {
						embedded.Store(true)
						logf("embed: SmartTube shown inside the launcher window (launcher foreground=%v)", splashOwnsForeground())
					} else {
						logf("embed: failed; falling back to a separate full-screen window")
					}
				}
				if embedded.Load() {
					resizeEmbedded()
					applied++
					time.Sleep(time.Duration(applied) * 1500 * time.Millisecond)
				} else if cfg.Fullscreen {
					// Resize behind the start-up screen until the emulator has settled
					// (it re-lays itself out as the display comes up) and SmartTube is
					// starting; then show it already full screen.
					if foundAt.IsZero() {
						foundAt = time.Now()
					}
					makeFullscreen(w)
					applied++
					if applied >= 3 && !appStarted.Load() && splashHost() != 0 && time.Since(foundAt) < 150*time.Second {
						applied = 2 // keep the cover up until SmartTube is launching
					}
					if applied >= 3 {
						time.Sleep(700 * time.Millisecond) // let the last resize render
						makeFullscreen(w)
						revealWindow(w.hwnd)
						logf("focus: revealed full-screen SmartTube window (foreground=%v)", isForeground(w.hwnd))
						closeSplash()
						break
					}
					time.Sleep(time.Second)
				} else {
					focusWindow(w.hwnd)
					if isForeground(w.hwnd) {
						closeSplash()
					}
					applied = 3
				}
			}
		}
	}()

	say("Starting Android TV...")
	if err := waitForBoot(serial, time.Duration(cfg.BootTimeoutSec)*time.Second, exited); err != nil {
		_ = emu.Kill()
		return err
	}
	logf("boot completed")
	adb := func(t time.Duration, args ...string) (string, error) {
		return runHidden(t, adbExe(), append([]string{"-s", serial}, args...)...)
	}
	// TV-friendly tweaks: no screensaver/sleep while SmartTube is open.
	adb(10*time.Second, "shell", "settings", "put", "system", "screen_off_timeout", "2147483647")
	adb(10*time.Second, "shell", "settings", "put", "secure", "screensaver_enabled", "0")

	pkg := cfg.PackageName()
	applyVideoDecoding(cfg, serial, pkg)
	if cfg.AudioOutput == "scrcpy" {
		if err := <-scrcpyReady; err != nil {
			logf("scrcpy: unavailable, using the emulator's audio: %v", err)
		} else {
			go runScrcpyAudio(serial, cfg.ScrcpyAudioBufferMs, exited)
		}
	}
	dump, _ := adb(15*time.Second, "shell", "dumpsys", "package", pkg)
	installed := parseInstalledVersion(dump)
	freshInstall := installed == ""
	logf("installed %s version: %q", pkg, installed)

	var res apkResult
	select {
	case res = <-apkCh:
	case <-time.After(5 * time.Minute):
		res.err = errors.New("timed out downloading SmartTube")
	}
	if res.err != nil {
		logf("update check: %v", res.err)
	}
	if res.path != "" && (installed == "" || res.version == "" || compareVersions(res.version, installed) > 0) {
		say("Installing SmartTube %s...", res.version)
		out, err := adb(3*time.Minute, "install", "-r", "-g", res.path)
		logf("install: %s %v", out, err)
		if err != nil || !strings.Contains(out, "Success") {
			// -g is unsupported on a few builds; retry plainly
			out, err = adb(3*time.Minute, "install", "-r", res.path)
			logf("install retry: %s %v", out, err)
		}
		if err == nil && strings.Contains(out, "Success") {
			st.InstalledAPK, st.InstalledSHA = res.name, res.sha
			saveState(dataDir, *st)
			installed = res.version
			if installed == "" {
				installed = "latest"
			}
		}
	}
	if installed == "" && res.path == "" {
		// State thought SmartTube was installed but the device doesn't have it: fetch it now.
		say("Downloading SmartTube...")
		p, n, v, sum, err := fetchLatestAPK(cfg, st, true)
		res.err = err
		if p != "" {
			out, ierr := adb(3*time.Minute, "install", "-r", p)
			logf("install (recovery): %s %v", out, ierr)
			if ierr == nil && strings.Contains(out, "Success") {
				st.InstalledAPK, st.InstalledSHA = n, sum
				saveState(dataDir, *st)
				installed = v
			}
		}
	}
	if installed == "" {
		dump, _ = adb(15*time.Second, "shell", "dumpsys", "package", pkg)
		if parseInstalledVersion(dump) == "" {
			_ = emu.Kill()
			if res.err != nil {
				return fmt.Errorf("SmartTube is not installed yet and could not be downloaded: %w", res.err)
			}
			return errors.New("SmartTube could not be installed in the Android TV runtime")
		}
	}

	// Diagnostics: which video decoders Android has and whether the emulator's
	// host-side (goldfish) decoders are switched on.
	go func() {
		o, _ := adb(10*time.Second, "shell", "getprop | grep -iE 'hwcodec|media|codec|gfxstream|goldfish' ; ls /vendor/etc /system/etc /odm/etc 2>/dev/null | grep -i media_codecs")
		logf("codec props:\n%s", strings.TrimSpace(o))
		o, _ = adb(10*time.Second, "shell", "cat /vendor/etc/media_codecs*.xml 2>/dev/null | grep -oE 'name=\"[^\"]+\"' | sort -u")
		logf("codecs:\n%s", strings.TrimSpace(o))
	}()

	// Log which decoder each video actually uses (host-side "goldfish" vs
	// software "c2.android"/"OMX.google" inside Android).
	videoStarted := make(chan struct{}, 1)
	go followDecoderLog(serial, exited, videoStarted)
	// Audio diagnostics: Android's audio mixer state (sample rates, buffer
	// sizes, underrun counts) 30 s and 90 s into the first video.
	go func() {
		select {
		case <-videoStarted:
		case <-exited:
			return
		}
		for _, wait := range []time.Duration{30 * time.Second, 60 * time.Second} {
			select {
			case <-time.After(wait):
			case <-exited:
				return
			}
			o, _ := adb(15*time.Second, "shell", "dumpsys", "media.audio_flinger")
			logf("audio_flinger:\n%s", strings.TrimSpace(o))
		}
	}()

	out, err := adb(20*time.Second, "shell", "monkey", "-p", pkg, "-c", "android.intent.category.LEANBACK_LAUNCHER", "1")
	logf("start app: %s %v", strings.TrimSpace(out), err)
	if cfg.PreferAVC && !st.DefaultsDone && st.DefaultsAttempts < 2 {
		splashText("Setting up SmartTube...")
		st.DefaultsAttempts++
		st.DefaultsDone = applySmartTubeDefaults(serial, pkg, freshInstall)
		saveState(dataDir, *st)
	}
	appStarted.Store(true)
	if splash != nil && !embedded.Load() {
		if cfg.Fullscreen && emuHwnd.Load() != 0 {
			select { // the window goroutine reveals SmartTube once it is full screen
			case <-revealed:
			case <-time.After(8 * time.Second):
			}
		}
		if splash != nil {
			if h := emuHwnd.Load(); h != 0 {
				revealWindow(h)
			}
			logf("focus: closing splash at app start (SmartTube foreground=%v)", emuHwnd.Load() != 0 && isForeground(emuHwnd.Load()))
			closeSplash()
		}
	}
	closeConsole()

	// Remote control from Xbox-style controllers.
	stop := make(chan struct{})
	defer close(stop)
	// quit covers the screen with a black "Closing SmartTube..." screen and hides
	// the Android window first, so the Android TV home screen is never seen.
	var closing atomic.Bool
	quit := func() {
		if !closing.CompareAndSwap(false, true) {
			return
		}
		if splash == nil {
			showSplash("Closing SmartTube...")
		} else {
			splashText("Closing SmartTube...")
		}
		if h := emuHwnd.Load(); h != 0 {
			procShowWindow.Call(h, swHide)
			closeWindow(h)
		}
	}
	if cfg.Gamepad {
		focused := func() bool { return emulatorHasFocus(port, emuPID.Load(), uint32(emu.Pid)) }
		sendKey := keySender(port, serial, cfg.InputMethod, focused)
		allowed := func() bool { return cfg.GamepadAlways || focused() }
		lastRefocus := time.Time{}
		refocus := func() bool {
			if closing.Load() {
				return false
			}
			h := frontHwnd()
			if h == 0 || time.Since(lastRefocus) < 700*time.Millisecond {
				return false
			}
			lastRefocus = time.Now()
			focusWindow(h)
			if !isForeground(h) {
				clickToFocus(h)
				time.Sleep(100 * time.Millisecond)
			}
			return isForeground(h)
		}
		go runGamepad(allowed, sendKey, quit, refocus, stop)
		logf("gamepad: ready")
		// Xbox full-screen mode: controller arrives as VK_GAMEPAD_* keystrokes.
		hookLogged := 0
		viewHeld, menuHeld := false, false
		installKeyHook(func(vk uint32, down bool) bool {
			switch vk {
			case 0xD0: // View
				viewHeld = down
			case 0xCF:
				menuHeld = down
			}
			if vk < 0xC3 || vk > 0xDA || !focused() {
				return false
			}
			if hookLogged < 10 {
				hookLogged++
				logf("keyhook: gamepad vk 0x%02X down=%v", vk, down)
			}
			if viewHeld && menuHeld && down {
				quit()
				return true
			}
			if k, ok := gamepadVK[vk]; ok {
				sendKey(k, down)
			}
			return true // swallow so nothing else reacts
		})
	}

	// Front-ends like Playnite may take focus back right after launching us.
	go func() {
		clicks := 0
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			select {
			case <-exited:
				return
			case <-time.After(time.Second):
			}
			if h := frontHwnd(); h != 0 && !isForeground(h) && !closing.Load() {
				pid, title := foregroundInfo()
				focusWindow(h)
				how := "api"
				if !isForeground(h) && clicks < 4 {
					clicks++
					clickToFocus(h)
					time.Sleep(150 * time.Millisecond)
					how = "click"
				}
				logf("focus: front window was pid %d %s %q; took it back via %s (ok=%v)", pid, processName(pid), title, how, isForeground(h))
			}
		}
	}()

	// Watch for the user leaving SmartTube (backing out to the TV home screen).
	// Android logs every activity switch; following that log reacts within a
	// fraction of a second. Polling dumpsys every 2s remains as a fallback.
	resumed := make(chan string, 16)
	if cfg.ExitOnHome {
		go followResumedActivities(serial, resumed, exited)
	}
	sawApp := false
	homeCount := 0
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	shutdown := func(why string) error {
		logf("SmartTube closed (%s); shutting down", why)
		quit()
		select {
		case <-exited:
		case <-time.After(45 * time.Second):
			_ = emu.Kill()
		}
		runHidden(10*time.Second, adbExe(), "kill-server")
		return nil
	}
	for {
		select {
		case <-exited:
			logf("emulator exited")
			closeSplash()
			runHidden(10*time.Second, adbExe(), "kill-server")
			return nil
		case p := <-resumed:
			if p == pkg {
				sawApp = true
			} else if sawApp && isHomeLauncher(p) {
				return shutdown("home screen resumed: " + p)
			}
			continue
		case <-tick.C:
		}
		if !cfg.ExitOnHome || closing.Load() {
			continue
		}
		d, err := adb(5*time.Second, "shell", "dumpsys", "window", "displays")
		if err != nil {
			continue
		}
		f := parseFocusedPackage(d)
		if f == "" {
			d, _ = adb(5*time.Second, "shell", "dumpsys", "window")
			f = parseFocusedPackage(d)
		}
		if f == pkg {
			sawApp, homeCount = true, 0
			continue
		}
		if sawApp && isHomeLauncher(f) {
			homeCount++
			if homeCount >= 2 {
				return shutdown("focus=" + f)
			}
		} else {
			homeCount = 0
		}
	}
}

// followResumedActivities streams Android's events log and reports the
// package of every activity that comes to the front.
func followResumedActivities(serial string, out chan<- string, stop <-chan struct{}) {
	for {
		cmd := exec.Command(adbExe(), "-s", serial, "shell", "logcat", "-b", "events", "-v", "brief", "-T", "1",
			"wm_set_resumed_activity:I", "am_set_resumed_activity:I", "*:S")
		cmd.SysProcAttr = hiddenProcAttr()
		pipe, err := cmd.StdoutPipe()
		if err == nil {
			err = cmd.Start()
		}
		if err != nil {
			logf("activity log: %v", err)
			return
		}
		go func() { <-stop; _ = cmd.Process.Kill() }()
		sc := bufio.NewScanner(pipe)
		for sc.Scan() {
			if p := parseResumedLine(sc.Text()); p != "" {
				select {
				case out <- p:
				default:
				}
			}
		}
		_ = cmd.Wait()
		select {
		case <-stop:
			return
		case <-time.After(2 * time.Second): // adb hiccup: reconnect
		}
	}
}

func followDecoderLog(serial string, stop <-chan struct{}, videoStarted chan<- struct{}) {
	cmd := exec.Command(adbExe(), "-s", serial, "shell", "logcat", "-v", "brief", "-T", "1",
		"CCodec:I", "MediaCodec:I", "ACodec:I", "Codec2Client:I", "*:S")
	cmd.SysProcAttr = hiddenProcAttr()
	pipe, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		logf("decoder log: %v", err)
		return
	}
	go func() { <-stop; _ = cmd.Process.Kill() }()
	sc := bufio.NewScanner(pipe)
	n := 0
	for sc.Scan() && n < 40 {
		l := sc.Text()
		lo := strings.ToLower(l)
		if strings.Contains(lo, "allocate") || strings.Contains(lo, "decoder") || strings.Contains(lo, "goldfish") {
			n++
			logf("decoder: %s", strings.TrimSpace(l))
			if strings.Contains(lo, "created component") && (strings.Contains(lo, "h264") || strings.Contains(lo, "avc") || strings.Contains(lo, "vp9") || strings.Contains(lo, "av1") || strings.Contains(lo, "hevc")) {
				select {
				case videoStarted <- struct{}{}:
				default:
				}
			}
		}
	}
	_ = cmd.Process.Kill()
}

// applyVideoDecoding logs whether the host-side decoders ended up on or off.
func applyVideoDecoding(cfg Config, serial, pkg string) {
	o, _ := runHidden(10*time.Second, adbExe(), "-s", serial, "shell", "getprop", "ro.boot.qemu.hwcodec.avcdec")
	v := strings.TrimSpace(o)
	logf("video decoding: configured %q; host decoder property avcdec=%q (2 = Windows side, empty/0 = inside Android)", cfg.VideoDecoding, v)
}

func startEmulator(cfg Config, port int, withNoMetrics bool) (*os.Process, <-chan struct{}, error) {
	args := []string{"-avd", avdName, "-port", itoa(port), "-gpu", cfg.GPU, "-no-boot-anim",
		"-netdelay", "none", "-netspeed", "full"}
	if withNoMetrics {
		args = append(args, "-no-metrics")
	}
	if cfg.VideoDecoding == "guest" {
		// Don't enable the host-side (goldfish) decoders: the emulator only sets
		// ro.boot.qemu.hwcodec.* when its HardwareDecoder feature is on.
		args = append(args, "-feature", "-HardwareDecoder")
	}
	args = append(args, cfg.ExtraArgs...)
	cmd := exec.Command(emulatorExe(), args...)
	cmd.Dir = filepath.Join(sdkDir, "emulator")
	cmd.Env = os.Environ()
	for k, v := range emulatorEnv(cfg) {
		cmd.Env = append(cmd.Env, k+"="+v)
		logf("emulator env %s=%s", k, v)
	}
	cmd.SysProcAttr = noConsoleProcAttr() // no console, but the emulator window must stay visible
	out := &lockedBuffer{}
	cmd.Stdout, cmd.Stderr = out, out
	// Full emulator output goes to emulator.log (launcher.log keeps only the tail).
	if ef, err := os.Create(filepath.Join(dataDir, "emulator.log")); err == nil {
		w := io.MultiWriter(out, ef)
		cmd.Stdout, cmd.Stderr = w, w
	}
	logf("emulator %s", strings.Join(args, " "))
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("could not start the Android emulator: %w", err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		logf("emulator output (tail): %s", tail(out.String(), 3000))
		close(exited)
	}()
	// If the emulator dies right away, report it (and retry without newer-only flags).
	select {
	case <-exited:
		o := out.String()
		if withNoMetrics && strings.Contains(strings.ToLower(o), "metrics") {
			return startEmulator(cfg, port, false)
		}
		return nil, nil, fmt.Errorf("the Android emulator stopped during start-up:\n%s", tail(o, 800))
	case <-time.After(6 * time.Second):
	}
	return cmd.Process, exited, nil
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.b.Len() > 1<<20 { // keep memory bounded
		l.b.Reset()
	}
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func waitForBoot(serial string, timeout time.Duration, exited <-chan struct{}) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			return errors.New("the Android emulator closed while starting")
		default:
		}
		out, _ := runHidden(5*time.Second, adbExe(), "-s", serial, "shell", "getprop", "sys.boot_completed")
		if strings.TrimSpace(out) == "1" {
			return nil
		}
		time.Sleep(1500 * time.Millisecond)
	}
	return fmt.Errorf("Android TV did not finish booting within %s", timeout)
}

// fetchLatestAPK finds and downloads the newest SmartTube x86 APK.
// Returns local path, asset name, version and sha256.
func fetchLatestAPK(cfg Config, st *State, force bool) (string, string, string, string, error) {
	cacheDir := filepath.Join(dataDir, "apk")
	_ = os.MkdirAll(cacheDir, 0o755)
	notInstalled := st.InstalledAPK == ""
	if !cfg.AutoUpdate && !notInstalled && !force {
		return "", "", "", "", nil
	}
	api := fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=15", cfg.GitHubRepo)
	var cand *apkCandidate
	if data, err := fetchBytes(api, 20*time.Second); err == nil {
		cand, err = findAPK(data, cfg.Channel)
		if err != nil {
			logf("release scan: %v", err)
		}
	} else {
		logf("github api: %v", err)
	}
	if cand == nil {
		// Rolling "latest" asset: only re-download every 12h unless forced.
		if !force && !notInstalled && time.Since(time.Unix(st.LastAPKCheck, 0)) < 12*time.Hour {
			return "", "", "", "", nil
		}
		cand = &apkCandidate{Name: "smarttube_" + cfg.Channel + "_x86.apk", URL: rollingAPKURL(cfg.GitHubRepo, cfg.Channel)}
	}
	if cand.Version != "" && !force && !notInstalled && cand.Name == st.InstalledAPK {
		return "", "", "", "", nil // already have it
	}
	dest := filepath.Join(cacheDir, cand.Name)
	if _, err := os.Stat(dest); err != nil || cand.Version == "" {
		if err := download(cand.URL, dest, "", "SmartTube "+cand.Version, func(s string) { logf("%s", s) }); err != nil {
			return "", "", "", "", err
		}
	}
	sum, _ := fileHash(dest, sha256.New())
	st.LastAPKCheck = time.Now().Unix()
	saveState(dataDir, *st)
	if cand.Version == "" && sum == st.InstalledSHA && !force {
		return "", "", "", "", nil
	}
	// Keep only the newest APK in the cache.
	if ents, err := os.ReadDir(cacheDir); err == nil {
		for _, e := range ents {
			if e.Name() != cand.Name {
				_ = os.Remove(filepath.Join(cacheDir, e.Name()))
			}
		}
	}
	return dest, cand.Name, cand.Version, sum, nil
}

func runHidden(timeout time.Duration, exe string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.SysProcAttr = hiddenProcAttr()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func portFree(p int) bool {
	l, err := net.Listen("tcp", "127.0.0.1:"+itoa(p))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

func tail(s string, n int) string {
	if len(s) > n {
		return "..." + s[len(s)-n:]
	}
	return s
}
