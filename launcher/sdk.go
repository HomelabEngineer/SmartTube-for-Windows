package main

import (
	"archive/zip"
	"crypto/sha1"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Google's public Android SDK repository. The launcher reads these XML
// manifests directly so it doesn't need Java/sdkmanager on the user's PC.
const (
	sdkRepoBase           = "https://dl.google.com/android/repository/"
	sdkRepoXML            = sdkRepoBase + "repository2-3.xml"
	atvImageBase          = sdkRepoBase + "sys-img/android-tv/"
	atvImageXML           = atvImageBase + "sys-img2-3.xml"
	platformToolsFallback = sdkRepoBase + "platform-tools-latest-windows.zip"
)

// Preferred Android TV API levels, best first. SmartTube ships an x86 build
// specifically for x86 Android environments.
var preferredAPIs = []int{34, 33, 31, 30, 35, 36, 28}

type repoXML struct {
	Channels []struct {
		ID   string `xml:"id,attr"`
		Name string `xml:",chardata"`
	} `xml:"channel"`
	Packages []remotePackage `xml:"remotePackage"`
}

type remotePackage struct {
	Path     string `xml:"path,attr"`
	Obsolete string `xml:"obsolete"`
	Revision struct {
		Major int `xml:"major"`
		Minor int `xml:"minor"`
		Micro int `xml:"micro"`
	} `xml:"revision"`
	ChannelRef struct {
		Ref string `xml:"ref,attr"`
	} `xml:"channelRef"`
	Archives []struct {
		HostOS   string `xml:"host-os"`
		HostArch string `xml:"host-arch"`
		Complete struct {
			Size      int64 `xml:"size"`
			Checksums []struct {
				Type  string `xml:"type,attr"`
				Value string `xml:",chardata"`
			} `xml:"checksum"`
			URL string `xml:"url"`
		} `xml:"complete"`
	} `xml:"archives>archive"`
}

type sdkArchive struct {
	Path     string
	Revision string
	URL      string
	SHA1     string
	Size     int64
}

func (p remotePackage) rev() string {
	return fmt.Sprintf("%d.%d.%d", p.Revision.Major, p.Revision.Minor, p.Revision.Micro)
}

func (p remotePackage) revKey() int {
	return p.Revision.Major*1_000_000 + p.Revision.Minor*1_000 + p.Revision.Micro
}

func parseRepo(data []byte) (*repoXML, error) {
	var r repoXML
	if err := xml.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// stableChannelID returns the id of the channel called "stable" (normally channel-0).
func (r *repoXML) stableChannelID() string {
	for _, c := range r.Channels {
		if strings.TrimSpace(c.Name) == "stable" {
			return c.ID
		}
	}
	return "channel-0"
}

// pickHostPackage finds the newest stable Windows x64 archive of a host tool (emulator, platform-tools).
func (r *repoXML) pickHostPackage(path, base string) (*sdkArchive, error) {
	stable := r.stableChannelID()
	var best *sdkArchive
	bestKey := -1
	for _, p := range r.Packages {
		if p.Path != path {
			continue
		}
		if ref := p.ChannelRef.Ref; ref != "" && ref != stable {
			continue
		}
		for _, a := range p.Archives {
			if a.HostOS != "windows" || (a.HostArch != "" && a.HostArch != "x64") {
				continue
			}
			if p.revKey() > bestKey {
				bestKey = p.revKey()
				best = &sdkArchive{Path: p.Path, Revision: p.rev(), URL: joinURL(base, a.Complete.URL),
					SHA1: sha1Of(a.Complete.Checksums), Size: a.Complete.Size}
			}
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no Windows package %q in SDK repository", path)
	}
	return best, nil
}

var atvPathRe = regexp.MustCompile(`^system-images;android-(\d+);android-tv;(x86|x86_64)$`)

// pickTVImage selects an Android TV (no Google services, no setup wizard) x86 image.
func (r *repoXML) pickTVImage(base string, wantAPI int) (*sdkArchive, int, string, error) {
	stable := r.stableChannelID()
	type cand struct {
		api int
		abi string
		pkg remotePackage
	}
	byAPI := map[int][]cand{}
	for _, p := range r.Packages {
		m := atvPathRe.FindStringSubmatch(p.Path)
		if m == nil || len(p.Archives) == 0 {
			continue
		}
		if ref := p.ChannelRef.Ref; ref != "" && ref != stable {
			continue
		}
		api, _ := strconv.Atoi(m[1])
		byAPI[api] = append(byAPI[api], cand{api, m[2], p})
	}
	order := preferredAPIs
	if wantAPI > 0 {
		order = append([]int{wantAPI}, preferredAPIs...)
	}
	for _, api := range order {
		cs := byAPI[api]
		if len(cs) == 0 {
			continue
		}
		// SmartTube's emulator build is x86 (32-bit); prefer the matching image.
		sort.SliceStable(cs, func(i, j int) bool {
			if cs[i].abi != cs[j].abi {
				return cs[i].abi == "x86"
			}
			return cs[i].pkg.revKey() > cs[j].pkg.revKey()
		})
		c := cs[0]
		a := c.pkg.Archives[0]
		return &sdkArchive{Path: c.pkg.Path, Revision: c.pkg.rev(), URL: joinURL(base, a.Complete.URL),
			SHA1: sha1Of(a.Complete.Checksums), Size: a.Complete.Size}, c.api, c.abi, nil
	}
	return nil, 0, "", errors.New("no Android TV x86 system image found in SDK repository")
}

// sha1Of returns the 40-hex-digit sha1 checksum if present (empty = skip verification).
func sha1Of(cs []struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}) string {
	for _, c := range cs {
		v := strings.ToLower(strings.TrimSpace(c.Value))
		if (c.Type == "" || c.Type == "sha1") && len(v) == 40 {
			return v
		}
	}
	return ""
}

func joinURL(base, u string) string {
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return u
	}
	return base + u
}

var httpClient = &http.Client{Timeout: 0} // downloads can be large; per-request deadlines handled by callers

func fetchBytes(url string, timeout time.Duration) ([]byte, error) {
	c := &http.Client{Timeout: timeout}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "SmartTube-Windows-Launcher")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// download fetches url to dest, printing progress to log and verifying a sha1
// (if given). Returns the sha256-free path; partial files are resumed-free but cleaned up.
func download(url, dest, wantSHA1, label string, progress func(string)) error {
	_ = os.MkdirAll(filepath.Dir(dest), 0o755)
	if wantSHA1 != "" {
		if got, err := fileHash(dest, sha1.New()); err == nil && got == wantSHA1 {
			progress(fmt.Sprintf("%s: already downloaded", label))
			return nil
		}
	}
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		lastErr = downloadOnce(url, dest, wantSHA1, label, progress)
		if lastErr == nil {
			return nil
		}
		progress(fmt.Sprintf("%s: attempt %d failed: %v", label, attempt, lastErr))
		time.Sleep(time.Duration(attempt*2) * time.Second)
	}
	return lastErr
}

func downloadOnce(url, dest, wantSHA1, label string, progress func(string)) error {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "SmartTube-Windows-Launcher")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	h := sha1.New()
	total := resp.ContentLength
	var done int64
	lastPct := -1
	lastPrint := time.Now()
	buf := make([]byte, 256*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return werr
			}
			h.Write(buf[:n])
			done += int64(n)
			if total > 0 {
				pct := int(done * 100 / total)
				if pct/5 != lastPct/5 || time.Since(lastPrint) > 5*time.Second {
					lastPct = pct
					lastPrint = time.Now()
					progress(fmt.Sprintf("%s: %3d%%  (%d / %d MB)", label, pct, done>>20, total>>20))
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return rerr
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if wantSHA1 != "" {
		if got := hex.EncodeToString(h.Sum(nil)); got != wantSHA1 {
			os.Remove(tmp)
			return fmt.Errorf("checksum mismatch (got %s, want %s)", got, wantSHA1)
		}
	}
	_ = os.Remove(dest)
	return os.Rename(tmp, dest)
}

func fileHash(path string, h hash.Hash) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// unzipTop extracts a zip whose entries all live under a single top-level
// folder (as every SDK archive does) and places that folder's contents at destDir.
func unzipTop(zipPath, destDir string, progress func(string)) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	staging := destDir + ".extracting"
	_ = os.RemoveAll(staging)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return err
	}
	total := len(zr.File)
	for i, f := range zr.File {
		name := filepath.FromSlash(f.Name)
		// Strip the first path component.
		parts := strings.SplitN(name, string(filepath.Separator), 2)
		if len(parts) < 2 || parts[1] == "" {
			continue
		}
		target := filepath.Join(staging, parts[1])
		if !strings.HasPrefix(target, filepath.Clean(staging)+string(filepath.Separator)) {
			return fmt.Errorf("unsafe path in archive: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(target, 0o755)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
		if progress != nil && total > 50 && i%(total/10) == 0 {
			progress(fmt.Sprintf("  extracting %d%%", i*100/total))
		}
	}
	_ = os.RemoveAll(destDir)
	if err := os.MkdirAll(filepath.Dir(destDir), 0o755); err != nil {
		return err
	}
	return os.Rename(staging, destDir)
}
