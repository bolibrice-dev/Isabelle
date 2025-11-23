package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/deepch/vdk/av"
	"github.com/pion/webrtc/v3"
)

// inlineMP4Session keeps track of an MP4 playback that reuses an existing PeerConnection.
type inlineMP4Session struct {
	path     string
	playback *mp4Playback
}

var inlineMP4 = struct {
	sync.Mutex
	sessions map[*Moxer]*inlineMP4Session
}{
	sessions: make(map[*Moxer]*inlineMP4Session),
}

type inlineMP4Request struct {
	Cam       string `json:"cam"`
	Camera    string `json:"camera"`
	Path      string `json:"path"`
	File      string `json:"file"`
	Recording string `json:"recording"`
}

// StartInlineMP4Playback loads the requested MP4 recording and starts streaming it over the
// existing PeerConnection referenced by element.
func StartInlineMP4Playback(camID string, rawParam string, element *Moxer) (string, error) {
	if element == nil {
		return "", fmt.Errorf("\r\ninline mp4: missing peer connection")
	}

	hint, err := parseInlineMP4Hint(camID, rawParam)
	//fmt.Printf("\r\n hint is: %v", hint)
	if err != nil {
		return "", err
	}

	absPath, err := resolveInlineRecordingPath(hint.Cam, hint.Path)
	//fmt.Printf("\r\n abspath is: %v", absPath)
	if err != nil {
		return "", err
	}

	videoTrack := element.GetVideoTrack()
	if videoTrack == nil {
		return "", fmt.Errorf("inline mp4: video track unavailable")
	}

	audioTrack := element.GetAudioTrack()
	videoCodec := currentVideoCodecForElement(element)

	playback, err := newMP4Playback(absPath, videoCodec, videoTrack, audioTrack, "", "")
	if err != nil {
		return "", err
	}

	inlineMP4.Lock()
	if existing := inlineMP4.sessions[element]; existing != nil {
		existing.playback.Stop()
	}
	inlineMP4.sessions[element] = &inlineMP4Session{path: absPath, playback: playback}
	inlineMP4.Unlock()

	element.SetMP4(absPath)
	playback.Start()
	return absPath, nil
}

// StopInlineMP4Playback halts any inline MP4 playback associated with the given peer connection.
func StopInlineMP4Playback(element *Moxer) {
	if element == nil {
		return
	}
	inlineMP4.Lock()
	session := inlineMP4.sessions[element]
	if session != nil {
		session.playback.Stop()
		delete(inlineMP4.sessions, element)
	}
	inlineMP4.Unlock()
	element.SetMP4("none")
}

type inlineMP4Hint struct {
	Cam  string
	Path string
}

func parseInlineMP4Hint(defaultCam string, raw string) (inlineMP4Hint, error) {
	hint := inlineMP4Hint{Cam: strings.TrimSpace(defaultCam)}
	raw = strings.TrimSpace(raw)
	//fmt.Printf("\r\nraw: %v", raw)
	if raw == "" {
		return hint, fmt.Errorf("inline mp4: empty request")
	}

	if strings.HasPrefix(raw, "{") {
		var req inlineMP4Request
		if err := json.Unmarshal([]byte(raw), &req); err == nil {
			if cam := strings.TrimSpace(firstNonEmpty(req.Cam, req.Camera)); cam != "" {
				hint.Cam = cam
			}
			if path := strings.TrimSpace(firstNonEmpty(req.Path, req.File, req.Recording)); path != "" {
				hint.Path = path
			}
		}
	}

	if hint.Path == "" {
		if strings.Contains(raw, "////") {
			parts := strings.Split(raw, "////")
			for i := len(parts) - 1; i >= 0; i-- {
				candidate := strings.TrimSpace(parts[i])
				if candidate != "" {
					hint.Path = candidate
					break
				}
			}
		}
	}

	if hint.Path == "" {
		hint.Path = raw
	}

	if hint.Cam == "" {
		return hint, fmt.Errorf("inline mp4: unable to determine camera context")
	}

	return hint, nil
}

func resolveInlineRecordingPath(cam string, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return "", fmt.Errorf("inline mp4: missing recording path")
	}

	joined := ""
	if ip, err := getCameraIPById(cam); err == nil {
		joined = filepath.ToSlash(filepath.Join("recordings", ip, requested))
	}

	//fmt.Printf("\r\njoined is %v", joined)

	var err error
	var abs string
	if abs, err = normalizeMP4FilePath(joined); err == nil {
		return abs, nil
	}

	//fmt.Printf("\r\n wtf man?? %v", err)

	return "", fmt.Errorf("\r\ninline mp4: recording %q not found", requested)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func currentVideoCodecForElement(element *Moxer) string {
	if element == nil {
		return webrtc.MimeTypeH264
	}
	for _, stream := range element.GetStreams() {
		if stream == nil || stream.codec == nil {
			continue
		}
		if stream.codec.Type().IsVideo() {
			switch stream.codec.Type() {
			case av.H264:
				return webrtc.MimeTypeH264
			case av.VP8:
				return webrtc.MimeTypeVP8
			case av.VP9:
				return webrtc.MimeTypeVP9
			}
		}
	}
	return webrtc.MimeTypeH264
}

func inlinePlaybackActive(element *Moxer) bool {
	if element == nil {
		return false
	}
	val := strings.TrimSpace(element.GetMP4())
	if val == "" {
		return false
	}
	return !strings.EqualFold(val, "none")
}
