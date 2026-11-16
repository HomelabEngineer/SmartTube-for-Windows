# SmartTube for Windows

An installable Windows version of [SmartTube](https://github.com/yuliskov/SmartTube), the ad-free YouTube client for Android TV.

SmartTube itself isn't ported or modified. The launcher runs the official SmartTube **x86** APK (the build yuliskov publishes for x86 Android environments) inside Google's own Android Emulator with a stock Android TV image. That design is what keeps it current: **every time you open SmartTube, the launcher checks the SmartTube GitHub releases and installs anything newer before the app starts.** No rebuild of this installer is needed when SmartTube updates.

## Requirements
- 64-bit Windows 10 or 11
- Virtualization turned on in the BIOS/UEFI (AMD SVM / Intel VT-x; it's on by default on most PCs and handhelds)
- **Windows Hypervisor Platform**. The launcher detects when it's missing and offers to enable it, which needs admin rights and a restart.
- About 6 GB of free disk space and 8 GB+ of RAM

## Install
Run `SmartTube-Setup-x.y.z.exe`. It installs per-user (no admin). If the "Download Android TV runtime now" option is ticked, it downloads about 1.5 GB from Google (emulator, platform-tools and the Android TV system image). The first launch boots Android TV from scratch, which takes 1–2 minutes. After that, quick-boot snapshots open it in a few seconds.

## Using it
| Xbox / XInput controller | SmartTube |
|---|---|
| D-pad / left stick | Navigate |
| A | Select |
| B | Back |
| X | Play / pause |
| Y | Search |
| LB / RB | Seek back / forward |
| LT / RT | Previous / next video |
| Menu (Start) | Context menu |
| View + Menu together | Quit SmartTube |

Keyboard (with the SmartTube window focused): arrow keys to navigate, Enter to select, Esc for back. Android TV is built for a remote, so the mouse is of limited use.

Backing out of SmartTube to the Android TV home screen closes everything (and saves a quick-boot snapshot).

## Start-menu tools
- **Update SmartTube now**: forces an update check and reinstall.
- **Repair Android runtime**: downloads the newest emulator, platform tools and TV image again.
- **Reset SmartTube**: wipes the virtual TV, including SmartTube settings and sign-in.
- **Settings (config.json)**

## config.json (`%LOCALAPPDATA%\SmartTubeWin\config.json`)
| Key | Default | Meaning |
|---|---|---|
| `channel` | `stable` | `beta` installs SmartTube Beta instead |
| `autoUpdate` | `true` | Check GitHub for a new APK on every launch |
| `fullscreen` | `true` | Borderless full-screen window |
| `embedWindow` | `false` | Experimental: show SmartTube inside the launcher's own full-screen window |
| `exitOnHome` | `true` | Close when SmartTube returns to the TV home screen |
| `gamepad` | `true` | Controller → TV-remote mapping |
| `gamepadAlways` | `false` | Send controller input to SmartTube even when another window is focused |
| `inputMethod` | `auto` | How controller buttons reach Android: `auto` tests the emulator console and falls back to `keyboard` (keystrokes into the SmartTube window plus adb for media keys) |
| `ramMB` / `cores` | `3072` / `2` | Virtual device resources |
| `gpu` | `auto` | Emulator GPU mode (`host`, `angle_indirect`, `swiftshader_indirect`) |
| `width` / `height` / `density` | `1920` / `1080` / `320` | TV resolution (use `1280`/`720`/`213` on slow machines) |
| `apiLevel` | `0` | Force a specific Android TV version (0 = automatic, prefers Android 14) |
| `extraEmulatorArgs` | `[]` | Extra emulator command-line flags |
| `audioLatencyMs` | `60` | Audio buffer size for the emulator. It defaults to ~3 ms, which crackles on many drivers; try `120` if 60 still pops, or `0` for the emulator's default |
| `audioDriver` | `""` | Force the emulator's Windows audio backend: `dsound` or `winwave` |
| `audioSampleRate` | `0` | Force the emulator's output rate, e.g. `48000` to match your Windows output device |
| `emulatorEnv` | `{}` | Any extra environment variables for the emulator, e.g. `{"QEMU_AUDIO_TIMER_PERIOD": "200"}` |
| `videoDecoding` | `host` | `host` decodes video on the Windows side (emulator's decoders); `guest` decodes inside Android on its own virtual CPUs |
| `emulatorPriority` | `normal` | Windows priority for the emulator: `normal`, `above` or `high` |
| `audioOutput` | `scrcpy` | `scrcpy` captures Android's audio and plays it on Windows via [scrcpy](https://github.com/Genymobile/scrcpy) (downloaded automatically), bypassing the emulator's sound output, which can crackle. `emulator` uses the emulator's own sound device |
| `scrcpyAudioBufferMs` | `200` | Audio buffer for `scrcpy` mode; raise it if there are still dropouts |
| `preferAvc` | `true` | On first launch, switch on SmartTube's *Settings → Player → Developer options → Codec selection: prefer avc over vp9* automatically (smoother playback in the emulator) |

Log file: `%LOCALAPPDATA%\SmartTubeWin\launcher.log`.

## How updating works
1. **SmartTube app**: on launch, the launcher reads `api.github.com/repos/yuliskov/SmartTube/releases`, finds the newest `SmartTube_<channel>_<version>_x86.apk`, and compares it with the installed `versionName` (`adb dumpsys package`). If the release is newer, it downloads the APK while Android boots and runs `adb install -r` before opening the app. If the API isn't reachable, it falls back to the rolling `releases/download/latest/smarttube_<channel>_x86.apk`. SmartTube's own in-app updater also still works.
2. **Android runtime**: *Repair Android runtime* pulls the current stable emulator and image straight from Google's SDK repository XML. Java and the SDK manager aren't needed.
3. **This launcher**: tag a commit `vX.Y.Z` and the GitHub Action builds a new `SmartTube-Setup-X.Y.Z.exe` release.

## Building
On Linux (or WSL), with `go` 1.22+, `nsis` and `binutils-mingw-w64-x86-64`:
```
./build.sh 1.0.0
```
Output: `dist/SmartTube.exe` and `dist/SmartTube-Setup-1.0.0.exe`.

## Notes & limits
- Video plays in the emulator. If 4K or 60 fps stutters on your machine, set SmartTube → Settings → Player → Video codec to **AVC** (H.264), or lower the resolution.
- Hyper-V, WSL2, Windows Sandbox and other hypervisor-based features coexist with Windows Hypervisor Platform.
- Unofficial. SmartTube is © yuliskov (MIT License). The Android Emulator and Android TV images are downloaded from Google under the Android SDK license.
