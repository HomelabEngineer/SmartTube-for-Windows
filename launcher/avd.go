package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const avdName = "SmartTube"

// writeAVD creates the virtual device by hand (avdmanager needs Java).
// sysImgRel is relative to the SDK root, e.g. system-images\android-34\android-tv\x86\
func writeAVD(avdHome, sysImgRel string, api int, abi string, c Config) error {
	avdDir := filepath.Join(avdHome, avdName+".avd")
	if err := os.MkdirAll(avdDir, 0o755); err != nil {
		return err
	}
	ini := fmt.Sprintf("avd.ini.encoding=UTF-8\r\npath=%s\r\npath.rel=avd\\%s.avd\r\ntarget=android-%d\r\n",
		avdDir, avdName, api)
	if err := os.WriteFile(filepath.Join(avdHome, avdName+".ini"), []byte(ini), 0o644); err != nil {
		return err
	}
	return writeAVDConfig(avdDir, sysImgRel, abi, c)
}

func writeAVDConfig(avdDir, sysImgRel, abi string, c Config) error {
	if !strings.HasSuffix(sysImgRel, `\`) {
		sysImgRel += `\`
	}
	kv := [][2]string{
		{"AvdId", avdName},
		{"avd.ini.displayname", avdName},
		{"avd.ini.encoding", "UTF-8"},
		{"PlayStore.enabled", "false"},
		{"abi.type", abi},
		{"hw.cpu.arch", abi},
		{"hw.cpu.ncore", fmt.Sprint(c.Cores)},
		{"hw.ramSize", fmt.Sprint(c.RAMMB)},
		{"vm.heapSize", "512"},
		{"hw.lcd.width", fmt.Sprint(c.Width)},
		{"hw.lcd.height", fmt.Sprint(c.Height)},
		{"hw.lcd.density", fmt.Sprint(c.Density)},
		{"hw.initialOrientation", "landscape"},
		{"hw.keyboard", "yes"},
		{"hw.dPad", "yes"},
		{"hw.mainKeys", "yes"},
		{"hw.trackBall", "no"},
		{"hw.gpu.enabled", "yes"},
		{"hw.gpu.mode", c.GPU},
		{"hw.audioInput", "no"},
		{"hw.audioOutput", "yes"},
		{"hw.camera.back", "none"},
		{"hw.camera.front", "none"},
		{"hw.gps", "no"},
		{"hw.sensors.orientation", "no"},
		{"hw.sensors.proximity", "no"},
		{"hw.accelerometer", "no"},
		{"hw.sdCard", "no"},
		{"disk.dataPartition.size", "6G"},
		{"image.sysdir.1", sysImgRel},
		{"tag.id", "android-tv"},
		{"tag.display", "Android TV"},
		{"showDeviceFrame", "no"},
		{"skin.dynamic", "yes"},
		{"skin.name", fmt.Sprintf("%dx%d", c.Width, c.Height)},
		{"skin.path", "_no_skin"},
		{"fastboot.forceColdBoot", "no"},
		{"fastboot.forceFastBoot", "yes"},
		{"runtime.network.latency", "none"},
		{"runtime.network.speed", "full"},
	}
	var b strings.Builder
	for _, p := range kv {
		b.WriteString(p[0] + "=" + p[1] + "\r\n")
	}
	return os.WriteFile(filepath.Join(avdDir, "config.ini"), []byte(b.String()), 0o644)
}
