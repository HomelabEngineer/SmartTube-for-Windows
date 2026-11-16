package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

const sampleRepo = `<?xml version="1.0" encoding="UTF-8" standalone="no"?>
<sdk:sdk-repository xmlns:common="http://schemas.android.com/repository/android/common/02" xmlns:generic="http://schemas.android.com/repository/android/generic/02" xmlns:sdk="http://schemas.android.com/sdk/android/repo/repository2/03" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
<license id="android-sdk-license" type="text">terms</license>
<channel id="channel-0">stable</channel>
<channel id="channel-3">canary</channel>
<remotePackage path="emulator">
 <type-details xsi:type="generic:genericDetailsType"/>
 <revision><major>36</major><minor>1</minor><micro>2</micro></revision>
 <display-name>Android Emulator</display-name>
 <channelRef ref="channel-3"/>
 <archives><archive><complete><size>1</size><checksum type="sha1">aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa</checksum><url>emulator-windows_x64-canary.zip</url></complete><host-os>windows</host-os><host-arch>x64</host-arch></archive></archives>
</remotePackage>
<remotePackage path="emulator">
 <revision><major>35</major><minor>6</minor><micro>11</micro></revision>
 <channelRef ref="channel-0"/>
 <archives>
  <archive><complete><size>2</size><checksum type="sha1">bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb</checksum><url>emulator-linux_x64-1.zip</url></complete><host-os>linux</host-os></archive>
  <archive><complete><size>3</size><checksum type="sha1">CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC</checksum><url>emulator-windows_x64-13025442.zip</url></complete><host-os>windows</host-os><host-arch>x64</host-arch></archive>
 </archives>
</remotePackage>
<remotePackage path="platform-tools">
 <revision><major>36</major><minor>0</minor><micro>0</micro></revision>
 <channelRef ref="channel-0"/>
 <archives><archive><complete><size>4</size><checksum>dddddddddddddddddddddddddddddddddddddddd</checksum><url>platform-tools_r36.0.0-win.zip</url></complete><host-os>windows</host-os></archive></archives>
</remotePackage>
</sdk:sdk-repository>`

const sampleTV = `<?xml version="1.0"?>
<sys-img:sdk-sys-img xmlns:sys-img="http://schemas.android.com/sdk/android/repo/sys-img2/03">
<channel id="channel-0">stable</channel>
<remotePackage path="system-images;android-34;android-tv;x86">
 <revision><major>3</major></revision><channelRef ref="channel-0"/>
 <archives><archive><complete><size>5</size><checksum type="sha1">eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee</checksum><url>x86-34_r03.zip</url></complete></archive></archives>
</remotePackage>
<remotePackage path="system-images;android-33;android-tv;x86">
 <revision><major>5</major></revision><channelRef ref="channel-0"/>
 <archives><archive><complete><size>5</size><checksum type="sha1">ffffffffffffffffffffffffffffffffffffffff</checksum><url>x86-33_r05.zip</url></complete></archive></archives>
</remotePackage>
<remotePackage path="system-images;android-34;google-tv;x86">
 <revision><major>3</major></revision><channelRef ref="channel-0"/>
 <archives><archive><complete><size>5</size><checksum type="sha1">1111111111111111111111111111111111111111</checksum><url>gtv.zip</url></complete></archive></archives>
</remotePackage>
</sys-img:sdk-sys-img>`

func TestRepo(t *testing.T) {
	r, err := parseRepo([]byte(sampleRepo))
	if err != nil {
		t.Fatal(err)
	}
	e, err := r.pickHostPackage("emulator", sdkRepoBase)
	if err != nil {
		t.Fatal(err)
	}
	if e.Revision != "35.6.11" || e.URL != sdkRepoBase+"emulator-windows_x64-13025442.zip" || e.SHA1 != "cccccccccccccccccccccccccccccccccccccccc" {
		t.Fatalf("emulator: %+v", e)
	}
	p, err := r.pickHostPackage("platform-tools", sdkRepoBase)
	if err != nil || p.SHA1 != "dddddddddddddddddddddddddddddddddddddddd" {
		t.Fatalf("pt: %+v %v", p, err)
	}
	tv, _ := parseRepo([]byte(sampleTV))
	img, api, abi, err := tv.pickTVImage(atvImageBase, 0)
	if err != nil || api != 34 || abi != "x86" || img.URL != atvImageBase+"x86-34_r03.zip" {
		t.Fatalf("img: %+v %d %s %v", img, api, abi, err)
	}
	img, api, _, _ = tv.pickTVImage(atvImageBase, 33)
	if api != 33 {
		t.Fatalf("want 33 got %d", api)
	}
}

func TestUnzipTop(t *testing.T) {
	d := t.TempDir()
	zp := filepath.Join(d, "a.zip")
	f, _ := os.Create(zp)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("emulator/emulator.exe")
	w.Write([]byte("x"))
	w, _ = zw.Create("emulator/lib/q.dll")
	w.Write([]byte("y"))
	zw.Close()
	f.Close()
	dest := filepath.Join(d, "sdk", "emulator")
	if err := unzipTop(zp, dest, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "lib", "q.dll")); err != nil {
		t.Fatal(err)
	}
}

const sampleReleases = `[
 {"tag_name":"latest","draft":false,"prerelease":true,"assets":[
   {"name":"smarttube_stable_x86.apk","browser_download_url":"https://x/latest/smarttube_stable_x86.apk"},
   {"name":"smarttube_beta.json","browser_download_url":"u"}]},
 {"tag_name":"32.74s","draft":false,"prerelease":false,"assets":[
   {"name":"SmartTube_stable_32.74_armeabi-v7a.apk","browser_download_url":"a"},
   {"name":"SmartTube_stable_32.74_x86.apk","browser_download_url":"https://x/32.74/x86.apk"},
   {"name":"SmartTube_stable_32.74_universal.apk","browser_download_url":"b"}]},
 {"tag_name":"32.80b","draft":false,"prerelease":true,"assets":[
   {"name":"SmartTube_beta_32.80_x86.apk","browser_download_url":"https://x/beta.apk"}]},
 {"tag_name":"32.70s","draft":false,"prerelease":false,"assets":[
   {"name":"SmartTube_stable_32.70_x86.apk","browser_download_url":"old"}]}
]`

func TestFindAPK(t *testing.T) {
	c, err := findAPK([]byte(sampleReleases), "stable")
	if err != nil || c.Version != "32.74" || c.URL != "https://x/32.74/x86.apk" {
		t.Fatalf("%+v %v", c, err)
	}
	c, _ = findAPK([]byte(sampleReleases), "beta")
	if c.Version != "32.80" {
		t.Fatalf("beta %+v", c)
	}
	c, _ = findAPK([]byte(`[{"tag_name":"latest","assets":[{"name":"smarttube_stable_x86.apk","browser_download_url":"r"}]}]`), "stable")
	if c == nil || c.Version != "" || c.URL != "r" {
		t.Fatalf("rolling %+v", c)
	}
	if compareVersions("32.100", "32.74") != 1 || compareVersions("32.74", "32.74.0") != 0 {
		t.Fatal("compare")
	}
}

func TestParse(t *testing.T) {
	if v := parseInstalledVersion("    versionCode=2464 minSdk=19\n    versionName=32.74\n"); v != "32.74" {
		t.Fatal(v)
	}
	d := "  mCurrentFocus=Window{4f2a1c u0 org.smarttube.stable/com.liskovsoft.smartyoutubetv2.tv.ui.main.SplashActivity}\n"
	if p := parseFocusedPackage(d); p != "org.smarttube.stable" {
		t.Fatal(p)
	}
	d = "  mFocusedApp=ActivityRecord{9d1 u0 com.google.android.tvlauncher/.MainActivity t3}\n"
	if p := parseFocusedPackage(d); !isHomeLauncher(p) {
		t.Fatal(p)
	}
}

func TestResumed(t *testing.T) {
	if p := parseResumedLine("I/wm_set_resumed_activity(  588): [0,com.google.android.tvlauncher/.MainActivity,resumeTopActivity - onActivityStateChanged]"); p != "com.google.android.tvlauncher" {
		t.Fatal(p)
	}
	if p := parseResumedLine("I/am_set_resumed_activity( 1): [0,org.smarttube.stable/com.liskovsoft.x.MainActivity,minimalResumeActivityLocked]"); p != "org.smarttube.stable" {
		t.Fatal(p)
	}
}

func TestEmulatorEnv(t *testing.T) {
	c := defaultConfig()
	e := emulatorEnv(c)
	if e["QEMU_DSOUND_LATENCY_MILLIS"] != "60" || e["QEMU_WINWAVE_DAC_SAMPLES"] != "2816" || e["PULSE_LATENCY_MSEC"] != "60" {
		t.Fatal(e)
	}
	c.AudioLatencyMs, c.AudioDriver, c.AudioSampleRate = 0, "winwave", 48000
	c.EmulatorEnv = map[string]string{"QEMU_AUDIO_TIMER_PERIOD": "200", "QEMU_AUDIO_DRV": "dsound"}
	e = emulatorEnv(c)
	if _, ok := e["QEMU_DSOUND_LATENCY_MILLIS"]; ok || e["QEMU_AUDIO_DRV"] != "dsound" || e["QEMU_AUDIO_DAC_FIXED_FREQ"] != "48000" || e["QEMU_AUDIO_TIMER_PERIOD"] != "200" {
		t.Fatal(e)
	}
}

func TestUINodes(t *testing.T) {
	xml := `<?xml version='1.0'?><hierarchy rotation="0"><node index="0" text="" checkable="false" checked="false" bounds="[0,0][1920,1080]"><node index="1" text="Codec selection: prefer avc over vp9" checkable="false" checked="false" bounds="[600,400][1300,440]" /><node index="2" text="" class="android.widget.CheckBox" checkable="true" checked="true" bounds="[1320,395][1380,445]" /><node text="Developer &amp; tweaks" checkable="false" checked="false" bounds="[10,10][20,20]"/></node></hierarchy>`
	nodes := parseUINodes(xml)
	n, ok := findText(nodes, "Codec selection: prefer avc over vp9")
	if !ok {
		t.Fatal("not found")
	}
	if x, y := n.center(); x != 950 || y != 420 {
		t.Fatal(x, y)
	}
	if c, ok := checkStateNear(nodes, n); !ok || !c {
		t.Fatal("check state")
	}
	if _, ok := findText(nodes, "Developer & tweaks"); !ok {
		t.Fatal("unescape")
	}
}

func TestFocusedOn(t *testing.T) {
	xml := `<hierarchy><node text="" focused="true" bounds="[0,500][400,560]"><node text="Settings" focused="false" bounds="[60,510][300,550]"/></node><node text="Home" focused="false" bounds="[60,100][300,140]"/></hierarchy>`
	nodes := parseUINodes(xml)
	if !focusedOn(nodes, "Settings") || focusedOn(nodes, "Home") {
		t.Fatal("focus")
	}
}

func TestUINodesMultiline(t *testing.T) {
	xml := `<hierarchy><node text="" class="android.widget.LinearLayout" checkable="false" checked="false" focused="true" bounds="[1100,880][1950,1010]"><node text="" class="android.widget.CheckBox" checkable="false" checked="false" bounds="[1140,920][1200,980]"/><node text="Codec selection: prefer avc over vp9&#10;WARN: 1080p maximum" class="android.widget.TextView" bounds="[1240,900][1900,990]"/></node></hierarchy>`
	nodes := parseUINodes(xml)
	n, ok := findText(nodes, "Codec selection: prefer avc over vp9")
	if !ok || !focusedOn(nodes, "Codec selection: prefer avc over vp9") {
		t.Fatal("find/focus")
	}
	if c, k := checkStateNear(nodes, n); !k || c {
		t.Fatal("state", c, k)
	}
}
