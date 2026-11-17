package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// SmartTube publishes an x86 APK in every release (built "for WSL"/x86
// devices), plus a rolling "latest" release with fixed file names.
type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

type apkCandidate struct {
	Name    string
	URL     string
	Version string // empty if the file name carries no version (rolling "latest" asset)
}

var versionRe = regexp.MustCompile(`(\d+(?:\.\d+)+)`)

// findAPK picks the newest x86 APK for the channel from a GitHub releases listing.
func findAPK(data []byte, channel string) (*apkCandidate, error) {
	var rels []ghRelease
	if err := json.Unmarshal(data, &rels); err != nil {
		return nil, err
	}
	nameRe := regexp.MustCompile(`(?i)^smarttube[_-]?` + regexp.QuoteMeta(channel) + `.*[_-]x86\.apk$`)
	var best *apkCandidate
	var fallback *apkCandidate
	for _, r := range rels {
		if r.Draft {
			continue
		}
		for _, a := range r.Assets {
			if !nameRe.MatchString(a.Name) || strings.Contains(strings.ToLower(a.Name), "x86_64") {
				continue
			}
			// Version lives in the file name, e.g. SmartTube_stable_32.74_x86.apk
			core := strings.TrimSuffix(strings.ToLower(a.Name), "_x86.apk")
			core = strings.TrimSuffix(core, "-x86.apk")
			v := ""
			if m := versionRe.FindAllString(core, -1); len(m) > 0 {
				v = m[len(m)-1]
			}
			c := &apkCandidate{Name: a.Name, URL: a.URL, Version: v}
			if v == "" {
				if fallback == nil {
					fallback = c
				}
				continue
			}
			if best == nil || compareVersions(v, best.Version) > 0 {
				best = c
			}
		}
	}
	if best != nil {
		return best, nil
	}
	if fallback != nil {
		return fallback, nil
	}
	return nil, fmt.Errorf("no %s x86 APK found in releases", channel)
}

// rollingAPKURL is the fixed-name asset SmartTube itself uses for restores.
func rollingAPKURL(repo, channel string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/latest/smarttube_%s_x86.apk", repo, channel)
}

// compareVersions compares dotted numeric versions: -1, 0, 1.
func compareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

var versionNameRe = regexp.MustCompile(`versionName=([^\s]+)`)

// parseInstalledVersion extracts versionName from `dumpsys package <pkg>` output.
func parseInstalledVersion(dumpsys string) string {
	if m := versionNameRe.FindStringSubmatch(dumpsys); m != nil {
		if v := versionRe.FindString(m[1]); v != "" {
			return v
		}
		return m[1]
	}
	return ""
}

var focusRe = regexp.MustCompile(`mCurrentFocus=Window\{[^ ]+ [^ ]+ ([^/}\s]+)`)
var focusAppRe = regexp.MustCompile(`mFocusedApp=.*?ActivityRecord\{[^ ]+ [^ ]+ ([^/}\s]+)`)

// parseFocusedPackage extracts the package that currently has focus from `dumpsys window`.
func parseFocusedPackage(dumpsys string) string {
	if m := focusRe.FindStringSubmatch(dumpsys); m != nil {
		return m[1]
	}
	if m := focusAppRe.FindStringSubmatch(dumpsys); m != nil {
		return m[1]
	}
	return ""
}

// isHomeLauncher reports whether pkg is an Android TV home screen.
func isHomeLauncher(pkg string) bool {
	switch pkg {
	case "com.google.android.tvlauncher", "com.google.android.leanbacklauncher",
		"com.google.android.apps.tv.launcherx", "com.android.tv.launcher",
		"com.android.launcher", "com.android.launcher3":
		return true
	}
	return strings.Contains(pkg, "launcher")
}

var resumedRe = regexp.MustCompile(`_set_resumed_activity.*?\[\d+,([^/,\]\s]+)/`)

// parseResumedLine extracts the package from an events-log line such as
// "I/wm_set_resumed_activity( 588): [0,com.google.android.tvlauncher/.MainActivity,resumeTopActivity]".
func parseResumedLine(line string) string {
	if m := resumedRe.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return ""
}

// sessionPlaying reports whether pkg's media session is PLAYING in
// `dumpsys media_session` output (sessions are listed one after another,
// each with a "package=" line).
func sessionPlaying(dump, pkg string) bool {
	parts := strings.Split(dump, "package=")
	for _, p := range parts[1:] {
		if strings.HasPrefix(p, pkg) &&
			(strings.Contains(p, "state=PLAYING") || strings.Contains(p, "PlaybackState {state=3,")) {
			return true
		}
	}
	return false
}
